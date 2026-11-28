import assert from "node:assert/strict";
import { createHash, randomBytes } from "node:crypto";
import { mkdtemp, readFile, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { MIGRATIONS_DIR, applyMigrations } from "../roomStore/migrate.js";
import { createRoom, grantMembership, listRoomMessages, provisionIdentity, sendMessage } from "../rooms/authz.js";
import { runSecretAction } from "./actions.js";
import {
  addSecret,
  agentAuth,
  applySync,
  bearerAuth,
  issueHandle,
  listAudit,
  listSecrets,
  ownerCookieAuth,
  planSync,
  redeemHandle,
  revokeSecret,
  rotateSecret,
  verifyAuditChain,
  verifySecret,
  withSecretValue,
} from "./vault.js";

// Contract tests against a REAL Postgres (GUILD_TEST_DATABASE_URL). All
// values are fake. No network: verification and sync use injected fetches.
const URL = process.env.GUILD_TEST_DATABASE_URL;
const skip = URL ? false : "set GUILD_TEST_DATABASE_URL to run";

const fake = (label, prefix = "sk-proj-") => `${prefix}${createHash("sha256").update(label).digest("base64url").replace(/[-_]/g, "x").slice(0, 40)}`;
const status = (code) => (err) => err.status === code;
const ENV = { SECRETS_MASTER_KEY: randomBytes(32).toString("base64") };

async function setup() {
  const { default: pg } = await import("pg");
  const pool = new pg.Pool({ connectionString: URL, max: 6 });
  const client = await pool.connect();
  try {
    await applyMigrations({ client, dir: MIGRATIONS_DIR });
    await client.query("truncate table guild_identities, guild_rooms cascade");
    await client.query("truncate table guild_secret_audit, guild_secret_sync_approvals, guild_secret_handles, guild_secret_grants, guild_secrets cascade");
  } finally {
    client.release();
  }
  const owner = await provisionIdentity(pool, { id: "deonte" });
  const member = await provisionIdentity(pool, { id: "teammate" });
  const agent = await provisionIdentity(pool, { id: "claude" });
  const outsider = await provisionIdentity(pool, { id: "outsider" });
  const room = await createRoom(pool, owner.token, { title: "Vault test" });
  await grantMembership(pool, owner.token, room.id, { identityId: member.id, role: "member" });
  await grantMembership(pool, owner.token, room.id, { identityId: agent.id, role: "member" });
  return { pool, owner, member, agent, outsider, roomId: room.id };
}

const opts = { env: ENV };

test("secret vault contract", { skip }, async (t) => {
  const ctx = await setup();
  const { pool, owner, member, outsider, roomId } = ctx;
  t.after(() => pool.end());
  const ownerAuth = bearerAuth(owner.token);
  const value = fake("openai-1");

  await t.test("fails closed without SECRETS_MASTER_KEY", async () => {
    await assert.rejects(addSecret(pool, ownerAuth, roomId, { name: "OPENAI_API_KEY", value }, { env: {} }), status(503));
  });

  await t.test("only owners add; members and outsiders are denied", async () => {
    await assert.rejects(addSecret(pool, bearerAuth(member.token), roomId, { name: "X_KEY", value }, opts), status(403));
    await assert.rejects(addSecret(pool, bearerAuth(outsider.token), roomId, { name: "X_KEY", value }, opts), status(403));
    await assert.rejects(addSecret(pool, ownerAuth, roomId, { name: "bad name", value }, opts), status(400));
    await assert.rejects(addSecret(pool, ownerAuth, roomId, { name: "OK", value, sourceUrl: "http://insecure.example" }, opts), status(400));
  });

  let card;
  await t.test("owner adds; only ciphertext and metadata are stored", async () => {
    card = await addSecret(pool, ownerAuth, roomId, {
      name: "OPENAI_API_KEY",
      value,
      sourceName: "OpenAI dashboard",
      sourceUrl: "https://platform.openai.com/api-keys?token=leak#frag",
      scopes: ["models:read"],
      environments: ["dev", "staging"],
    }, opts);
    assert.equal(card.provider, "openai");
    assert.equal(card.last4, value.slice(-4));
    assert.equal(card.sourceUrl, "https://platform.openai.com/api-keys");
    assert.equal(card.status, "unverified");
    assert.deepEqual(card.grants, []);
    const { rows } = await pool.query("select * from guild_secrets where id = $1", [card.id]);
    const dump = JSON.stringify(rows[0]) + Buffer.from(rows[0].ciphertext).toString("latin1");
    assert.ok(!dump.includes(value));
    await assert.rejects(addSecret(pool, ownerAuth, roomId, { name: "OPENAI_API_KEY", value: fake("dup") }, opts), status(409));
  });

  await t.test("members see cards without grants; outsiders see nothing", async () => {
    const list = await listSecrets(pool, bearerAuth(member.token), roomId);
    assert.equal(list.length, 1);
    assert.equal(list[0].grants, undefined);
    await assert.rejects(listSecrets(pool, bearerAuth(outsider.token), roomId), status(403));
  });

  await t.test("agents are denied by default", async () => {
    await assert.rejects(issueHandle(pool, agentAuth("claude"), roomId, card.id, opts), status(403));
  });

  await t.test("grant → scoped single-use handle → server-side use; no reveal without consent", async () => {
    await runSecretAction(pool, ownerAuth, roomId, card.id, { action: "grant", agentId: "claude", scopes: ["models:read"], maxUses: 2, ttlSeconds: 3600 });
    await assert.rejects(issueHandle(pool, agentAuth("claude"), roomId, card.id, { scope: "billing:write", ...opts }), status(403));
    await assert.rejects(issueHandle(pool, agentAuth("grok"), roomId, card.id, opts), status(403));
    const h1 = await issueHandle(pool, agentAuth("claude"), roomId, card.id, { scope: "models:read", ...opts });
    assert.equal(h1.reveal, false);
    let seen = null;
    await withSecretValue(pool, h1.handle, async (buf) => { seen = buf; assert.equal(buf.toString("utf8"), value); }, opts);
    assert.ok(seen.every((b) => b === 0), "value buffer is zeroized after use");
    await assert.rejects(withSecretValue(pool, h1.handle, async () => {}, opts), status(403), "handles are single use");
    const h2 = await issueHandle(pool, agentAuth("claude"), roomId, card.id, opts);
    await assert.rejects(redeemHandle(pool, h2.handle, { agentId: "claude", ...opts }), status(403), "no reveal grant");
  });

  await t.test("use budget and expiry are enforced", async () => {
    // One use is left of maxUses 2: refused reveals do not spend the budget.
    const h = await issueHandle(pool, agentAuth("claude"), roomId, card.id, opts);
    await withSecretValue(pool, h.handle, async () => {}, opts);
    await assert.rejects(issueHandle(pool, agentAuth("claude"), roomId, card.id, opts), status(403), "budget spent");
    await runSecretAction(pool, ownerAuth, roomId, card.id, { action: "grant", agentId: "gemini", reveal: true, ttlSeconds: 600 });
    const hg = await issueHandle(pool, agentAuth("gemini"), roomId, card.id, opts);
    await assert.rejects(redeemHandle(pool, hg.handle, { agentId: "claude", ...opts }), status(403), "handle bound to its agent");
    const hg2 = await issueHandle(pool, agentAuth("gemini"), roomId, card.id, opts);
    assert.equal((await redeemHandle(pool, hg2.handle, { agentId: "gemini", ...opts })).value, value);
    await pool.query("update guild_secret_grants set expires_at = now() - interval '1 second' where agent_id = 'gemini'");
    await assert.rejects(issueHandle(pool, agentAuth("gemini"), roomId, card.id, opts), status(403), "expired grant");
  });

  await t.test("verification is flag-gated and uses an injected fetch", async () => {
    const off = await verifySecret(pool, ownerAuth, roomId, card.id, opts);
    assert.equal(off.card.status, "unverified");
    const calls = [];
    const fetchImpl = async (url, init) => { calls.push({ url, init }); return new Response("{}", { status: 401 }); };
    const bad = await verifySecret(pool, ownerAuth, roomId, card.id, { env: { ...ENV, SECRETS_VERIFY: "on" }, fetchImpl });
    assert.equal(bad.card.status, "invalid");
    assert.equal(calls[0].url, "https://api.openai.com/v1/models?limit=1");
    assert.equal(calls[0].init.redirect, "error");
    const good = await verifySecret(pool, ownerAuth, roomId, card.id, { env: { ...ENV, SECRETS_VERIFY: "on" }, fetchImpl: async () => new Response("{}", { status: 200 }) });
    assert.equal(good.card.status, "verified");
    await assert.rejects(verifySecret(pool, bearerAuth(member.token), roomId, card.id, opts), status(403));
  });

  await t.test("sync: dry-run plan, apply disabled by default, approval single use", async () => {
    await assert.rejects(planSync(pool, ownerAuth, roomId, card.id, { target: "fly", environment: "prod" }, opts), status(400), "not scoped to prod");
    const plan = await planSync(pool, ownerAuth, roomId, card.id, { target: "fly", environment: "staging" }, { env: { ...ENV, SECRETS_SYNC_FLY_APP: "demo-app" } });
    assert.equal(plan.dryRun, true);
    assert.equal(plan.app, "demo-app");
    assert.ok(!JSON.stringify(plan).includes(value));
    await assert.rejects(applySync(pool, ownerAuth, roomId, card.id, { approvalId: plan.approvalId }, opts), status(403), "apply off");
    const calls = [];
    const env = { ...ENV, SECRETS_SYNC_APPLY: "on", SECRETS_SYNC_FLY_APP: "demo-app", SECRETS_SYNC_FLY_TOKEN: "fake-fly-token" };
    const fetchImpl = async (url, init) => { calls.push({ url, body: JSON.parse(init.body) }); return Response.json({ data: { setSecrets: { app: { name: "demo-app" } } } }); };
    await assert.rejects(applySync(pool, bearerAuth(member.token), roomId, card.id, { approvalId: plan.approvalId }, { env, fetchImpl }), status(403));
    const out = await applySync(pool, ownerAuth, roomId, card.id, { approvalId: plan.approvalId }, { env, fetchImpl });
    assert.equal(out.destination, "fly:demo-app");
    assert.equal(calls[0].body.variables.input.secrets[0].value, value);
    await assert.rejects(applySync(pool, ownerAuth, roomId, card.id, { approvalId: plan.approvalId }, { env, fetchImpl }), status(400), "approval reused");
    await assert.rejects(planSync(pool, ownerAuth, roomId, card.id, { target: "github", environment: "dev" }, opts).then(() => applySync(pool, ownerAuth, roomId, card.id, { approvalId: "a_" + "0".repeat(32) }, { env, fetchImpl })), status(400));
  });

  await t.test("dotenv sync writes a 0600 file", async () => {
    const dir = await mkdtemp(join(tmpdir(), "secret-sync-"));
    const env = { ...ENV, SECRETS_SYNC_APPLY: "on", SECRETS_SYNC_DOTENV_DIR: dir };
    const plan = await planSync(pool, ownerAuth, roomId, card.id, { target: "dotenv", environment: "dev" }, { env });
    await applySync(pool, ownerAuth, roomId, card.id, { approvalId: plan.approvalId }, { env });
    const file = join(dir, ".env.dev");
    assert.equal((await stat(file)).mode & 0o777, 0o600);
    assert.match(await readFile(file, "utf8"), /^OPENAI_API_KEY=".+"\n$/);
  });

  await t.test("rotate kills outstanding handles; revoke crypto-shreds", async () => {
    await runSecretAction(pool, ownerAuth, roomId, card.id, { action: "grant", agentId: "codex", ttlSeconds: 600 });
    const h = await issueHandle(pool, agentAuth("codex"), roomId, card.id, opts);
    const rotated = await rotateSecret(pool, ownerAuth, roomId, card.id, { value: fake("openai-2") }, opts);
    assert.equal(rotated.version, 2);
    assert.equal(rotated.status, "unverified");
    await assert.rejects(withSecretValue(pool, h.handle, async () => {}, opts), status(403));
    const h2 = await issueHandle(pool, agentAuth("codex"), roomId, card.id, opts);
    await withSecretValue(pool, h2.handle, async (buf) => assert.equal(buf.toString(), fake("openai-2")), opts);
    const revoked = await revokeSecret(pool, ownerAuth, roomId, card.id);
    assert.equal(revoked.status, "revoked");
    await assert.rejects(issueHandle(pool, agentAuth("codex"), roomId, card.id, opts), status(403));
    const { rows } = await pool.query("select length(ciphertext) as n from guild_secrets where id = $1", [card.id]);
    assert.equal(rows[0].n, 1);
  });

  await t.test("chat interception: owner drops a key, it is vaulted and redacted", async () => {
    const key = fake("dropped", "ghp_").slice(0, 40);
    const sent = await sendMessage(pool, owner.token, roomId, { text: `here's the deploy key ${key} for CI` }, opts);
    assert.ok(!sent.text.includes(key));
    assert.equal(sent.secrets.length, 1);
    assert.equal(sent.secrets[0].provider, "github");
    const fromMember = await sendMessage(pool, member.token, roomId, { text: `mine: ${fake("member", "ghp_").slice(0, 40)}` }, opts);
    assert.equal(fromMember.secrets, undefined, "non-owners never vault; the value is dropped");
    assert.equal(fromMember.redactedSecrets, 1);
    const { messages } = await listRoomMessages(pool, member.token, roomId);
    assert.ok(!JSON.stringify(messages).includes(key));
    const { rows } = await pool.query("select body::text as b from guild_messages where room_id = $1", [roomId]);
    assert.ok(rows.every((r) => !r.b.includes(key)), "nothing raw in message storage");
  });

  await t.test("audit log: complete, value-free, owner-only, hash-chained and append-only", async () => {
    const log = await listAudit(pool, ownerAuth, roomId);
    const actions = new Set(log.map((e) => e.action));
    for (const a of ["secret.add", "grant.add", "handle.issue", "handle.use", "handle.denied", "handle.reveal", "secret.verify", "sync.plan", "sync.apply", "secret.rotate", "secret.revoke"]) {
      assert.ok(actions.has(a), a);
    }
    const all = JSON.stringify(log);
    for (const v of [value, fake("openai-2")]) assert.ok(!all.includes(v));
    await assert.rejects(listAudit(pool, bearerAuth(member.token), roomId), status(403));
    assert.deepEqual((await verifyAuditChain(pool, roomId)).ok, true);
    await assert.rejects(pool.query("update guild_secret_audit set actor = 'x'"), /append-only/);
    await assert.rejects(pool.query("delete from guild_secret_audit"), /append-only/);
  });

  await t.test("owner-cookie auth only addresses the guild room", async () => {
    await assert.rejects(listSecrets(pool, ownerCookieAuth("guild"), roomId), status(403));
    const c = await addSecret(pool, ownerCookieAuth("guild"), "guild", { name: "GEMINI_API_KEY", value: fake("gem", "AIza").slice(0, 39) }, opts);
    assert.equal(c.provider, "google");
  });
});
