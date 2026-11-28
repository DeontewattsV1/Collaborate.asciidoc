import { createHash, randomUUID } from "node:crypto";
import { ROLES, requireRole, withTx } from "../rooms/authz.js";
import { RoomApiError, denied, rejected } from "../rooms/errors.js";
import { PROVIDERS, guessProvider, last4 } from "./detector.js";
import {
  fingerprint,
  handleDigest,
  loadVaultKeys,
  newHandle,
  openSecret,
  sealSecret,
  zeroize,
} from "./crypto.js";
import { SYNC_TARGETS, buildPlan, syncApplyEnabled } from "./sync.js";
import { verifyValue } from "./verify.js";

// The secret vault: room-scoped, deny-by-default storage for credentials
// dropped into a chat.
//
// - Owners add, rotate, revoke, grant, verify and sync. Members and viewers
//   see redacted cards only. Agents see nothing until an owner grants them a
//   secret; then they can mint a short-lived scoped handle and use it.
// - Raw values leave the vault in exactly two ways: server-side use through
//   withSecretValue (the value is zeroized afterwards), or redeemHandle for a
//   grant that the owner explicitly marked reveal:true.
// - Every action writes a hash-chained audit row. Audit rows never hold values.
//
// Callers pass an `auth` resolver: (client, roomId, roles) => {id, role}.
// bearerAuth wraps the durable-room identities; ownerCookieAuth is the
// single-owner chat, which only ever addresses the "guild" room.

export const ENVIRONMENTS = Object.freeze(["dev", "staging", "prod"]);
export const MAX_VALUE_BYTES = 16 * 1024;
export const MAX_GRANT_SECONDS = 30 * 24 * 3600;
export const HANDLE_SECONDS = 5 * 60;
const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;
const SCOPE_RE = /^[A-Za-z0-9:._*/-]{1,64}$/;
const AGENT_RE = /^[A-Za-z0-9_-]{1,64}$/;
const SECRET_ID_RE = /^s_[0-9a-f]{32}$/;
const GRANT_ID_RE = /^g_[0-9a-f]{32}$/;

const newId = (prefix) => `${prefix}_${randomUUID().replace(/-/g, "")}`;

export function bearerAuth(token) {
  return async (client, roomId, roles) => {
    const { identity, role } = await requireRole(client, token, roomId, roles);
    return { id: identity.id, role };
  };
}

export function ownerCookieAuth(roomId = "guild") {
  return async (_client, requested, roles) => {
    if (requested !== roomId || !roles.includes("owner")) throw denied();
    return { id: "owner", role: "owner" };
  };
}

// An agent acting for itself has no room role; it is matched against grants.
export function agentAuth(agentId) {
  if (typeof agentId !== "string" || !AGENT_RE.test(agentId)) throw denied();
  return { agentId };
}

// ---- validation --------------------------------------------------------

function cleanScopes(list) {
  if (list === undefined || list === null) return [];
  if (!Array.isArray(list) || list.length > 20) throw rejected("invalid scopes");
  const out = [...new Set(list.map((s) => String(s).trim()).filter(Boolean))];
  if (!out.every((s) => SCOPE_RE.test(s))) throw rejected("invalid scopes");
  return out;
}

function cleanEnvironments(list) {
  if (list === undefined || list === null) return [];
  if (!Array.isArray(list) || !list.every((e) => ENVIRONMENTS.includes(e))) throw rejected("invalid environments");
  return [...new Set(list)];
}

function cleanExpiry(value, { max = 2 * 365 * 24 * 3600 } = {}) {
  if (value === undefined || value === null || value === "") return null;
  const at = new Date(value);
  const now = Date.now();
  if (Number.isNaN(at.getTime()) || at.getTime() <= now || at.getTime() > now + max * 1000) throw rejected("invalid expiry");
  return at;
}

function cleanSource({ sourceName, sourceUrl }) {
  const name = typeof sourceName === "string" ? sourceName.trim() : "";
  if (name.length > 100 || /[\u0000-\u001f]/.test(name)) throw rejected("invalid source name");
  let url = typeof sourceUrl === "string" ? sourceUrl.trim() : "";
  if (url) {
    let parsed;
    try {
      parsed = new URL(url);
    } catch {
      throw rejected("invalid source link");
    }
    if (parsed.protocol !== "https:" || parsed.username || parsed.password || url.length > 500) throw rejected("source link must be https");
    // Never keep query strings or fragments: they often carry tokens.
    url = `${parsed.origin}${parsed.pathname}`;
  }
  return { sourceName: name, sourceUrl: url };
}

function cleanValue(value) {
  if (typeof value !== "string") throw rejected("invalid secret value");
  const v = value.trim();
  if (!v || Buffer.byteLength(v, "utf8") > MAX_VALUE_BYTES || /[\u0000]/.test(v)) throw rejected("invalid secret value");
  return v;
}

function cleanProvider(provider, value) {
  if (provider === undefined || provider === null || provider === "") return guessProvider(value);
  if (!Object.hasOwn(PROVIDERS, provider)) throw rejected("unknown provider");
  return provider;
}

function cleanName(name) {
  if (typeof name !== "string" || !NAME_RE.test(name)) throw rejected("name must look like an environment variable (A-Z, 0-9, _)");
  return name;
}

function cleanGrant(input = {}) {
  const agentId = input.agentId;
  if (typeof agentId !== "string" || !AGENT_RE.test(agentId)) throw rejected("invalid agent id");
  const ttl = input.ttlSeconds === undefined ? 24 * 3600 : Number(input.ttlSeconds);
  if (!Number.isSafeInteger(ttl) || ttl < 60 || ttl > MAX_GRANT_SECONDS) throw rejected("invalid grant ttl");
  const maxUses = input.maxUses === undefined || input.maxUses === null ? null : Number(input.maxUses);
  if (maxUses !== null && (!Number.isSafeInteger(maxUses) || maxUses < 1 || maxUses > 100000)) throw rejected("invalid max uses");
  return { agentId, scopes: cleanScopes(input.scopes), ttl, maxUses, reveal: input.reveal === true };
}

// ---- audit -------------------------------------------------------------

const SENSITIVE_DETAIL = /value|secret|token|key|password|ciphertext/i;

// jsonb reorders object keys, so hash a canonical (sorted-key) form.
function canonical(v) {
  if (Array.isArray(v)) return v.map(canonical);
  if (v && typeof v === "object") return Object.fromEntries(Object.keys(v).sort().map((k) => [k, canonical(v[k])]));
  return v;
}

export async function audit(client, { roomId, secretId = null, actor, action, detail = {} }) {
  const safe = {};
  for (const [k, v] of Object.entries(detail)) {
    if (SENSITIVE_DETAIL.test(k)) continue;
    safe[k] = typeof v === "string" ? v.slice(0, 200) : v;
  }
  await client.query("select pg_advisory_xact_lock(hashtext($1))", [`secret-audit:${roomId}`]);
  const prev = await client.query(
    "select hash from guild_secret_audit where room_id = $1 order by id desc limit 1",
    [roomId],
  );
  const prevHash = prev.rows.length ? prev.rows[0].hash : "";
  const at = new Date().toISOString();
  const hash = createHash("sha256")
    .update(JSON.stringify([prevHash, roomId, secretId, String(actor), action, canonical(safe), at]))
    .digest("hex");
  await client.query(
    `insert into guild_secret_audit (room_id, secret_id, actor, action, detail, prev_hash, hash, created_at)
     values ($1, $2, $3, $4, $5::jsonb, $6, $7, $8)`,
    [roomId, secretId, String(actor), action, JSON.stringify(safe), prevHash, hash, at],
  );
}

export async function verifyAuditChain(db, roomId) {
  const { rows } = await db.query(
    "select secret_id, actor, action, detail, prev_hash, hash, created_at from guild_secret_audit where room_id = $1 order by id",
    [roomId],
  );
  let prev = "";
  for (const r of rows) {
    const at = new Date(r.created_at).toISOString();
    const expect = createHash("sha256").update(JSON.stringify([prev, roomId, r.secret_id, r.actor, r.action, canonical(r.detail), at])).digest("hex");
    if (r.prev_hash !== prev || r.hash !== expect) return { ok: false, rows: rows.length };
    prev = r.hash;
  }
  return { ok: true, rows: rows.length };
}

// ---- cards -------------------------------------------------------------

export function cardFrom(row, grants) {
  const card = {
    id: row.id,
    name: row.name,
    provider: row.provider,
    providerLabel: PROVIDERS[row.provider] || PROVIDERS.generic,
    sourceName: row.source_name,
    sourceUrl: row.source_url,
    last4: row.last4,
    scopes: row.scopes || [],
    environments: row.environments || [],
    status: row.revoked_at ? "revoked" : (row.expires_at && new Date(row.expires_at) <= new Date() ? "expired" : row.status),
    version: row.version,
    createdBy: row.created_by,
    createdAt: new Date(row.created_at).toISOString(),
    expiresAt: row.expires_at ? new Date(row.expires_at).toISOString() : null,
    verifiedAt: row.verified_at ? new Date(row.verified_at).toISOString() : null,
    rotatedAt: row.rotated_at ? new Date(row.rotated_at).toISOString() : null,
  };
  if (grants) {
    card.grants = grants.map((g) => ({
      id: g.id,
      agentId: g.agent_id,
      scopes: g.scopes || [],
      reveal: g.reveal,
      expiresAt: new Date(g.expires_at).toISOString(),
      maxUses: g.max_uses,
      uses: g.uses,
    }));
  }
  return card;
}

const CARD_COLUMNS = `id, room_id, name, provider, source_name, source_url, scopes, environments, status,
  last4, version, created_by, created_at, expires_at, verified_at, rotated_at, revoked_at`;

async function loadSecret(client, roomId, secretId, { lock = false } = {}) {
  if (typeof secretId !== "string" || !SECRET_ID_RE.test(secretId)) throw denied();
  const { rows } = await client.query(
    `select ${CARD_COLUMNS}, ciphertext, nonce, wrapped_key from guild_secrets
      where id = $1 and room_id = $2 ${lock ? "for update" : ""}`,
    [secretId, roomId],
  );
  if (!rows.length) throw denied();
  return rows[0];
}

async function liveGrants(client, secretId) {
  const { rows } = await client.query(
    `select id, agent_id, scopes, reveal, expires_at, max_uses, uses from guild_secret_grants
      where secret_id = $1 and revoked_at is null and expires_at > now()
        and (max_uses is null or uses < max_uses)
      order by created_at`,
    [secretId],
  );
  return rows;
}

// ---- writes ------------------------------------------------------------

// insertSecret runs inside an existing transaction. Used by addSecret and by
// the chat capture path in sendMessage.
export async function insertSecret(client, keys, { roomId, actorId, name, value, provider, sourceName = "", sourceUrl = "", scopes = [], environments = [], expiresAt = null }) {
  const id = newId("s");
  const sealed = sealSecret(keys, value, { roomId, secretId: id, version: 1 });
  const fp = fingerprint(keys, value);
  const { rows } = await client.query(
    `insert into guild_secrets (id, room_id, name, provider, source_name, source_url, scopes, environments,
        last4, fingerprint, ciphertext, nonce, wrapped_key, key_version, created_by, expires_at)
     values ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10, $11, $12, $13, $14, $15, $16)
     on conflict do nothing
     returning ${CARD_COLUMNS}`,
    [id, roomId, name, provider, sourceName, sourceUrl, JSON.stringify(scopes), JSON.stringify(environments),
      last4(value), fp, sealed.ciphertext, sealed.nonce, sealed.wrappedKey, sealed.keyVersion, actorId, expiresAt],
  );
  if (!rows.length) throw new RoomApiError(409, "a live secret with that name already exists");
  await audit(client, { roomId, secretId: id, actor: actorId, action: "secret.add", detail: { name, provider, sourceName, sourceUrl, scopes, environments } });
  return rows[0];
}

async function insertGrant(client, { roomId, secretId, actorId, grant }) {
  const id = newId("g");
  await client.query(
    `insert into guild_secret_grants (id, secret_id, agent_id, scopes, reveal, expires_at, max_uses, granted_by)
     values ($1, $2, $3, $4::jsonb, $5, now() + make_interval(secs => $6), $7, $8)`,
    [id, secretId, grant.agentId, JSON.stringify(grant.scopes), grant.reveal, grant.ttl, grant.maxUses, actorId],
  );
  await audit(client, {
    roomId, secretId, actor: actorId, action: "grant.add",
    detail: { grant: id, agent: grant.agentId, scopes: grant.scopes, reveal: grant.reveal, ttl: grant.ttl, maxUses: grant.maxUses },
  });
  return id;
}

export async function addSecret(db, auth, roomId, input = {}, { env = process.env } = {}) {
  const keys = loadVaultKeys(env);
  const value = cleanValue(input.value);
  const name = cleanName(input.name);
  const provider = cleanProvider(input.provider, value);
  const source = cleanSource(input);
  const scopes = cleanScopes(input.scopes);
  const environments = cleanEnvironments(input.environments);
  const expiresAt = cleanExpiry(input.expiresAt);
  const grants = (Array.isArray(input.allowedAgents) ? input.allowedAgents : []).slice(0, 21).map(cleanGrant);
  if (grants.length > 20) throw rejected("too many agents");
  return withTx(db, async (client) => {
    const actor = await auth(client, roomId, ROLES.admin);
    const row = await insertSecret(client, keys, { roomId, actorId: actor.id, name, value, provider, ...source, scopes, environments, expiresAt });
    for (const grant of grants) await insertGrant(client, { roomId, secretId: row.id, actorId: actor.id, grant });
    return cardFrom(row, await liveGrants(client, row.id));
  });
}

export async function listSecrets(db, auth, roomId) {
  return withTx(db, async (client) => {
    const actor = await auth(client, roomId, ROLES.read);
    const { rows } = await client.query(
      `select ${CARD_COLUMNS} from guild_secrets where room_id = $1 order by created_at desc limit 200`,
      [roomId],
    );
    const owner = actor.role === "owner";
    const out = [];
    for (const row of rows) out.push(cardFrom(row, owner ? await liveGrants(client, row.id) : undefined));
    return out;
  });
}

export async function revokeSecret(db, auth, roomId, secretId) {
  return withTx(db, async (client) => {
    const actor = await auth(client, roomId, ROLES.admin);
    const row = await loadSecret(client, roomId, secretId, { lock: true });
    // Crypto-shred: drop the wrapped key and ciphertext, keep the metadata.
    const { rows } = await client.query(
      `update guild_secrets set revoked_at = coalesce(revoked_at, now()), status = 'revoked',
          ciphertext = '\\x00'::bytea, wrapped_key = decode(repeat('00', 60), 'hex'), updated_at = now()
        where id = $1 returning ${CARD_COLUMNS}`,
      [row.id],
    );
    await client.query("update guild_secret_grants set revoked_at = coalesce(revoked_at, now()) where secret_id = $1", [row.id]);
    await audit(client, { roomId, secretId: row.id, actor: actor.id, action: "secret.revoke", detail: { name: row.name } });
    return cardFrom(rows[0], []);
  });
}

export async function rotateSecret(db, auth, roomId, secretId, input = {}, { env = process.env } = {}) {
  const keys = loadVaultKeys(env);
  const value = cleanValue(input.value);
  return withTx(db, async (client) => {
    const actor = await auth(client, roomId, ROLES.admin);
    const row = await loadSecret(client, roomId, secretId, { lock: true });
    if (row.revoked_at) throw rejected("secret is revoked");
    const version = row.version + 1;
    const sealed = sealSecret(keys, value, { roomId, secretId: row.id, version });
    const { rows } = await client.query(
      `update guild_secrets set ciphertext = $2, nonce = $3, wrapped_key = $4, key_version = $5, version = $6,
          fingerprint = $7, last4 = $8, status = 'unverified', verified_at = null, rotated_at = now(), updated_at = now()
        where id = $1 returning ${CARD_COLUMNS}`,
      [row.id, sealed.ciphertext, sealed.nonce, sealed.wrappedKey, sealed.keyVersion, version, fingerprint(keys, value), last4(value)],
    );
    // Outstanding handles die with the old value.
    await client.query(
      "delete from guild_secret_handles where grant_id in (select id from guild_secret_grants where secret_id = $1)",
      [row.id],
    );
    await audit(client, { roomId, secretId: row.id, actor: actor.id, action: "secret.rotate", detail: { version } });
    return cardFrom(rows[0], await liveGrants(client, row.id));
  });
}

export async function grantAgent(db, auth, roomId, secretId, input = {}) {
  const grant = cleanGrant(input);
  return withTx(db, async (client) => {
    const actor = await auth(client, roomId, ROLES.admin);
    const row = await loadSecret(client, roomId, secretId, { lock: true });
    if (row.revoked_at) throw rejected("secret is revoked");
    const id = await insertGrant(client, { roomId, secretId: row.id, actorId: actor.id, grant });
    return { id, secretId: row.id, agentId: grant.agentId };
  });
}

export async function revokeGrant(db, auth, roomId, secretId, grantId) {
  if (typeof grantId !== "string" || !GRANT_ID_RE.test(grantId)) throw denied();
  return withTx(db, async (client) => {
    const actor = await auth(client, roomId, ROLES.admin);
    const row = await loadSecret(client, roomId, secretId);
    const { rowCount } = await client.query(
      "update guild_secret_grants set revoked_at = coalesce(revoked_at, now()) where id = $1 and secret_id = $2",
      [grantId, row.id],
    );
    if (!rowCount) throw denied();
    await client.query("delete from guild_secret_handles where grant_id = $1", [grantId]);
    await audit(client, { roomId, secretId: row.id, actor: actor.id, action: "grant.revoke", detail: { grant: grantId } });
    return { revoked: true };
  });
}

// ---- agent access --------------------------------------------------------

// findGrant is deny-by-default: no live grant for this agent, scope, expiry
// and use budget means no access, with the same error as a missing secret.
async function findGrant(client, roomId, secretId, agentId, scope) {
  const row = await loadSecret(client, roomId, secretId);
  if (row.revoked_at || (row.expires_at && new Date(row.expires_at) <= new Date()) || row.status === "invalid") throw denied();
  const { rows } = await client.query(
    `select id, scopes, reveal, max_uses, uses from guild_secret_grants
      where secret_id = $1 and agent_id = $2 and revoked_at is null and expires_at > now()
        and (max_uses is null or uses < max_uses)
      order by created_at for update`,
    [row.id, agentId],
  );
  const grant = rows.find((g) => !scope || (g.scopes || []).includes(scope) || (g.scopes || []).includes("*"));
  if (!grant) throw denied();
  return { row, grant };
}

export async function issueHandle(db, agent, roomId, secretId, { scope = "", env = process.env } = {}) {
  const keys = loadVaultKeys(env);
  if (scope && !SCOPE_RE.test(scope)) throw rejected("invalid scope");
  return withTx(db, async (client) => {
    const { row, grant } = await findGrant(client, roomId, secretId, agent.agentId, scope);
    const handle = newHandle();
    const expiresAt = new Date(Date.now() + HANDLE_SECONDS * 1000);
    await client.query(
      "insert into guild_secret_handles (digest, grant_id, scope, expires_at) values ($1, $2, $3, $4)",
      [handleDigest(keys, handle), grant.id, scope, expiresAt],
    );
    await audit(client, { roomId, secretId: row.id, actor: `agent:${agent.agentId}`, action: "handle.issue", detail: { grant: grant.id, scope } });
    return { handle, expiresAt: expiresAt.toISOString(), secretId: row.id, name: row.name, scope, reveal: grant.reveal };
  });
}

// withSecretValue spends one use of a handle and passes the decrypted value
// (a Buffer) to fn. The buffer is zeroized when fn settles, so fn must not
// keep a reference. Handles are single use.
export async function withSecretValue(db, handle, fn, { requireReveal = false, agentId = null, env = process.env } = {}) {
  const keys = loadVaultKeys(env);
  if (typeof handle !== "string" || !/^sh_[A-Za-z0-9_-]{43}$/.test(handle)) throw denied();
  let value = null;
  const meta = await withTx(db, async (client) => {
    const { rows } = await client.query(
      `delete from guild_secret_handles h using guild_secret_grants g, guild_secrets s
        where h.digest = $1 and g.id = h.grant_id and s.id = g.secret_id
        returning h.expires_at, h.scope, g.id as grant_id, g.agent_id, g.reveal, g.max_uses, g.uses, g.expires_at as grant_expires,
                  g.revoked_at as grant_revoked, s.id as secret_id, s.room_id, s.name, s.version, s.revoked_at, s.expires_at as secret_expires,
                  s.ciphertext, s.nonce, s.wrapped_key`,
      [handleDigest(keys, handle)],
    );
    if (!rows.length) throw denied();
    const r = rows[0];
    const now = new Date();
    const dead = new Date(r.expires_at) <= now || r.grant_revoked || new Date(r.grant_expires) <= now
      || (r.max_uses !== null && r.uses >= r.max_uses) || r.revoked_at || (r.secret_expires && new Date(r.secret_expires) <= now);
    if (dead || (requireReveal && !r.reveal) || (agentId !== null && agentId !== r.agent_id)) {
      await audit(client, { roomId: r.room_id, secretId: r.secret_id, actor: `agent:${r.agent_id}`, action: "handle.denied", detail: { grant: r.grant_id, reveal: requireReveal } });
      return { denied: true };
    }
    await client.query("update guild_secret_grants set uses = uses + 1 where id = $1", [r.grant_id]);
    value = openSecret(keys, { ciphertext: r.ciphertext, nonce: r.nonce, wrappedKey: r.wrapped_key }, { roomId: r.room_id, secretId: r.secret_id, version: r.version });
    await audit(client, { roomId: r.room_id, secretId: r.secret_id, actor: `agent:${r.agent_id}`, action: requireReveal ? "handle.reveal" : "handle.use", detail: { grant: r.grant_id, scope: r.scope } });
    return { name: r.name, secretId: r.secret_id, agentId: r.agent_id };
  });
  if (meta.denied) throw denied();
  try {
    return await fn(value, meta);
  } finally {
    zeroize(value);
  }
}

// redeemHandle returns the raw value as a string, only for reveal grants.
export async function redeemHandle(db, handle, { agentId, env = process.env } = {}) {
  if (typeof agentId !== "string" || !AGENT_RE.test(agentId)) throw denied();
  return withSecretValue(db, handle, (buf, meta) => ({ name: meta.name, value: buf.toString("utf8") }), { requireReveal: true, agentId, env });
}

// ---- owner-only internals: verification and sync -------------------------

export async function withOwnerSecret(db, auth, roomId, secretId, fn, { action, detail = {}, env = process.env } = {}) {
  const keys = loadVaultKeys(env);
  return withTx(db, async (client) => {
    const actor = await auth(client, roomId, ROLES.admin);
    const row = await loadSecret(client, roomId, secretId, { lock: true });
    if (row.revoked_at) throw rejected("secret is revoked");
    const value = openSecret(keys, { ciphertext: row.ciphertext, nonce: row.nonce, wrappedKey: row.wrapped_key }, { roomId, secretId: row.id, version: row.version });
    try {
      const out = await fn({ client, actor, row, value });
      await audit(client, { roomId, secretId: row.id, actor: actor.id, action, detail: { ...detail, ...(out && out.audit) } });
      return out;
    } finally {
      zeroize(value);
    }
  });
}

export async function listAudit(db, auth, roomId, { limit = 200 } = {}) {
  return withTx(db, async (client) => {
    await auth(client, roomId, ROLES.admin);
    const { rows } = await client.query(
      `select id, secret_id, actor, action, detail, created_at, hash from guild_secret_audit
        where room_id = $1 order by id desc limit $2`,
      [roomId, Math.min(Math.max(Number(limit) || 200, 1), 500)],
    );
    return rows.map((r) => ({ id: Number(r.id), secretId: r.secret_id, actor: r.actor, action: r.action, detail: r.detail, at: new Date(r.created_at).toISOString(), hash: r.hash }));
  });
}

// captureFindings stores secrets detected in a chat message. Only owners can
// capture; for anyone else the values are dropped (the message is still
// redacted). Runs inside the caller's transaction.
export async function captureFindings(client, keys, { roomId, actor, findings, sourceName = "chat" }) {
  if (!keys || actor.role !== "owner") return [];
  const cards = [];
  for (const f of findings.slice(0, 5)) {
    const base = (f.hint && NAME_RE.test(f.hint) ? f.hint : `${f.provider.toUpperCase()}_KEY`).slice(0, 48);
    const name = `${base}_${randomUUID().slice(0, 6).toUpperCase()}`;
    const row = await insertSecret(client, keys, { roomId, actorId: actor.id, name, value: f.value, provider: f.provider, sourceName });
    cards.push(cardFrom(row));
  }
  return cards;
}

export async function verifySecret(db, auth, roomId, secretId, { env = process.env, fetchImpl } = {}) {
  return withOwnerSecret(db, auth, roomId, secretId, async ({ client, row, value }) => {
    const result = await verifyValue(row.provider, value.toString("utf8"), { env, fetchImpl });
    const status = result.status;
    const { rows } = await client.query(
      `update guild_secrets set status = $2, verified_at = case when $2 = 'unverified' then verified_at else now() end, updated_at = now()
        where id = $1 returning ${CARD_COLUMNS}`,
      [row.id, status],
    );
    return { card: cardFrom(rows[0]), reason: result.reason, audit: { status, reason: result.reason } };
  }, { action: "secret.verify", env });
}

const APPROVAL_ID_RE = /^a_[0-9a-f]{32}$/;

// planSync is the dry run. It records a single-use approval bound to the plan.
export async function planSync(db, auth, roomId, secretId, { target, environment } = {}, { env = process.env } = {}) {
  if (!Object.hasOwn(SYNC_TARGETS, target || "")) throw rejected("unknown sync target");
  if (!ENVIRONMENTS.includes(environment)) throw rejected("invalid environment");
  return withTx(db, async (client) => {
    const actor = await auth(client, roomId, ROLES.admin);
    const row = await loadSecret(client, roomId, secretId, { lock: true });
    if (row.revoked_at) throw rejected("secret is revoked");
    const card = cardFrom(row);
    if (!card.environments.includes(environment)) throw rejected("secret is not scoped to that environment");
    const plan = buildPlan({ card, target, environment, env });
    const approvalId = newId("a");
    await client.query(
      `insert into guild_secret_sync_approvals (id, secret_id, target, environment, plan_digest, approved_by, expires_at)
       values ($1, $2, $3, $4, $5, $6, now() + interval '10 minutes')`,
      [approvalId, row.id, target, environment, plan.digest, actor.id],
    );
    await audit(client, { roomId, secretId: row.id, actor: actor.id, action: "sync.plan", detail: { target, environment, approval: approvalId } });
    return { ...plan, approvalId };
  });
}

// applySync needs the approval from planSync, an owner, and SECRETS_SYNC_APPLY=on.
export async function applySync(db, auth, roomId, secretId, { approvalId } = {}, { env = process.env, fetchImpl = globalThis.fetch } = {}) {
  if (!syncApplyEnabled(env)) throw new RoomApiError(403, "sync apply is disabled (SECRETS_SYNC_APPLY is not on); this was a dry run");
  if (typeof approvalId !== "string" || !APPROVAL_ID_RE.test(approvalId)) throw rejected("approval required");
  return withOwnerSecret(db, auth, roomId, secretId, async ({ client, actor, row, value }) => {
    const { rows } = await client.query(
      `update guild_secret_sync_approvals set consumed_at = now()
        where id = $1 and secret_id = $2 and consumed_at is null and expires_at > now()
        returning target, environment, plan_digest, approved_by`,
      [approvalId, row.id],
    );
    if (!rows.length) throw rejected("approval is missing, used or expired");
    const a = rows[0];
    const plan = buildPlan({ card: cardFrom(row), target: a.target, environment: a.environment, env });
    if (!plan || plan.digest !== a.plan_digest || a.approved_by !== actor.id) throw rejected("secret changed since the plan; plan again");
    const out = await SYNC_TARGETS[a.target].apply({ name: row.name, value: value.toString("utf8"), environment: a.environment }, { env, fetchImpl });
    return { target: a.target, environment: a.environment, destination: out.destination, audit: { target: a.target, environment: a.environment, approval: approvalId, destination: out.destination } };
  }, { action: "sync.apply", env });
}

// announceCard posts the redacted card into a durable room's guild thread.
export async function announceCard(db, roomId, actorId, card, { text } = {}) {
  const body = {
    name: actorId,
    text: text || `Added secret ${card.name} (${card.providerLabel}${card.last4 ? ` …${card.last4}` : ""})`,
    files: [],
    secret: publicCard(card),
    time: new Date().toISOString(),
    mine: false,
    status: "",
  };
  await db.query(
    `insert into guild_messages (room_id, id, thread_id, from_id, kind, body)
     values ($1, $2, 'guild', $3, 'person', $4::jsonb)`,
    [roomId, `m_${randomUUID()}`, actorId, JSON.stringify(body)],
  );
}

// publicCard is what every room member may see in the transcript.
export function publicCard(card) {
  const { grants, ...rest } = card;
  return { ...rest, agents: (grants || []).map((g) => g.agentId) };
}
