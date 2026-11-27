import assert from "node:assert/strict";
import test from "node:test";
import { MIGRATIONS_DIR, applyMigrations } from "./roomStore/migrate.js";
import { postgresStore } from "./roomStore/postgresStore.js";
import {
  addReaction,
  appendLine,
  buildCatalog,
  configureGroup,
  getMessages,
  listThreads,
  mergeGroupPrefs,
  openDMThread,
  registerInvitedParticipant,
  setRoomStoreForTest,
  updateGroupMember,
} from "./store.js";

// The routes' store facade with GUILD_STORE=postgres, against a REAL Postgres.
const URL = process.env.GUILD_TEST_DATABASE_URL;

test("route store facade persists to Postgres when GUILD_STORE=postgres", { skip: URL ? false : "set GUILD_TEST_DATABASE_URL to run" }, async (t) => {
  const { default: pg } = await import("pg");
  const client = new pg.Client({ connectionString: URL });
  await client.connect();
  await applyMigrations({ client, dir: MIGRATIONS_DIR });
  await client.query("truncate table guild_identities, guild_rooms cascade");
  await client.end();

  const env = { GUILD_STORE: "postgres", GOOGLE_GENERATIVE_AI_API_KEY: "k" };
  setRoomStoreForTest(postgresStore({ connectionString: URL }));
  t.after(async () => {
    const s = globalThis.__devGuildRoomStore;
    setRoomStoreForTest(null);
    await s.close();
  });

  const catalog = await buildCatalog({ env });
  const prefs = await configureGroup({ title: "Durable Crew", members: ["claude"], setupDone: true }, null, catalog, env);
  assert.equal(prefs.title, "Durable Crew");
  assert.equal((await mergeGroupPrefs(null, env)).title, "Durable Crew");
  await updateGroupMember("claude", "remove", null, env);
  assert.deepEqual((await mergeGroupPrefs(null, env)).removed, ["claude"]);

  await registerInvitedParticipant({ id: "guest-1", name: "Guest", kind: "custom", avatar: "codex-cloud.png", code: "secret-invite" }, env);
  const withGuest = await buildCatalog({ env });
  const guest = withGuest.find((c) => c.id === "guest-1");
  assert.equal(guest.avatar, "codex-cloud.png");
  assert.equal(guest.code, undefined, "invite codes are never persisted");

  const line = await appendLine({ threadId: "guild", from: "owner", text: "durable hello", catalog, env });
  assert.ok(line.seq > 0);
  const reply = await appendLine({ threadId: "guild", from: "claude", text: "ack", catalog, env });
  const msgs = await getMessages("guild", 0, env);
  assert.deepEqual(msgs.map((m) => m.text), ["durable hello", "ack"]);
  assert.equal(msgs[0].status, "Read");
  assert.deepEqual((await getMessages("guild", line.seq, env)).map((m) => m.id), [reply.id]);

  const on = await addReaction({ threadId: "guild", messageId: reply.id, emoji: "🎉", env });
  assert.deepEqual(on.reactions, [{ emoji: "🎉", count: 1, mine: true }]);
  const off = await addReaction({ threadId: "guild", messageId: reply.id, emoji: "🎉", env });
  assert.deepEqual(off.reactions, []);
  await assert.rejects(() => addReaction({ threadId: "guild", messageId: "m_missing", emoji: "👍", env }), /not found/);
  assert.equal((await addReaction({ threadId: "guild", messageId: "m_vm", emoji: "👍", env, allowMissing: true })).persisted, false);

  const dm = await openDMThread("claude", catalog, env);
  assert.equal(dm.id, "dm-claude");
  assert.deepEqual(await openDMThread("claude", catalog, env), dm);
  const threads = await listThreads({ catalog, env });
  assert.equal(threads[0].last, "ack");
  assert.ok(threads.some((th) => th.id === "dm-claude"));
});
