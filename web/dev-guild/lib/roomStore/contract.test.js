import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { StoreConflictError } from "./errors.js";
import { fileStore } from "./fileStore.js";
import { memoryStore } from "./memoryStore.js";
import { MIGRATIONS_DIR, applyMigrations } from "./migrate.js";
import { postgresStore } from "./postgresStore.js";
import { emptyPrefs } from "./shape.js";

const ROOM = "guild";

function assertCustomAgentShape(agent, id, name) {
  assert.equal(agent.id, id);
  assert.equal(agent.name, name);
  assert.equal(typeof agent.createdAt, "string");
  assert.ok(Number.isFinite(Date.parse(agent.createdAt)));
  assert.equal(typeof agent.updatedAt, "string");
  assert.ok(Number.isFinite(Date.parse(agent.updatedAt)));
  assert.ok(Date.parse(agent.updatedAt) >= Date.parse(agent.createdAt));
  assert.equal(agent.apiKey, undefined);
  assert.deepEqual(Object.keys(agent).sort(), ["createdAt", "id", "kind", "name", "persona", "updatedAt"]);
}

async function exercise(store, { reopen } = {}) {
  const initial = await store.loadRoom(ROOM);
  assert.equal(initial.id, ROOM);
  assert.deepEqual(initial.prefs, emptyPrefs());
  assert.deepEqual(initial.threads, []);
  assert.deepEqual(initial.participants, []);
  assert.deepEqual(initial.customAgents, []);

  const saved = await store.savePrefs({
    roomId: ROOM,
    prefs: { title: "Backend Crew", ownerName: "Alex", setupDone: true, added: ["claude"], removed: [] },
    expectedVersion: 0,
  });
  assert.equal(saved.version, 1);
  await assert.rejects(
    () => store.savePrefs({ roomId: ROOM, prefs: { title: "Stale" }, expectedVersion: 0 }),
    StoreConflictError,
  );

  const thread = await store.upsertThread({
    roomId: ROOM,
    thread: { id: "dm-claude", title: "Claude", members: ["owner", "claude"], group: false },
  });
  assert.equal(thread.id, "dm-claude");
  const threads = await store.listThreads(ROOM);
  assert.equal(threads.length, 1);
  assert.deepEqual(threads[0].members, ["owner", "claude"]);

  const first = await store.appendMessage({
    roomId: ROOM,
    threadId: "guild",
    from: "owner",
    kind: "message",
    body: { text: "hello" },
  });
  const second = await store.appendMessage({
    roomId: ROOM,
    threadId: "guild",
    from: "review-bot",
    kind: "agent",
    body: { text: "hi" },
  });
  assert.ok(second.seq > first.seq);
  assert.notEqual(first.id, second.id);

  const afterFirst = await store.listMessages({ roomId: ROOM, threadId: "guild", after: first.seq });
  assert.deepEqual(afterFirst.map((message) => message.id), [second.id]);
  const all = await store.listMessages({ roomId: ROOM, threadId: "guild", after: 0 });
  assert.deepEqual(all[0].body, { text: "hello" });

  const parallel = await Promise.all(
    Array.from({ length: 20 }, (_, index) =>
      store.appendMessage({
        roomId: ROOM,
        threadId: "guild",
        from: "owner",
        kind: "message",
        body: { text: `burst ${index}` },
      }),
    ),
  );
  assert.equal(new Set(parallel.map((message) => message.seq)).size, 20);
  assert.equal(new Set(parallel.map((message) => message.id)).size, 20);

  const liked = await store.addReaction({
    roomId: ROOM,
    threadId: "guild",
    messageId: first.id,
    emoji: "👍",
    actorId: "owner",
  });
  assert.equal(liked.added, true);
  const duplicate = await store.addReaction({
    roomId: ROOM,
    threadId: "guild",
    messageId: first.id,
    emoji: "👍",
    actorId: "owner",
  });
  assert.equal(duplicate.added, false);
  await store.addReaction({
    roomId: ROOM,
    threadId: "guild",
    messageId: first.id,
    emoji: "🎉",
    actorId: "review-bot",
  });
  const forAgent = await store.listReactions({
    roomId: ROOM,
    threadId: "guild",
    messageIds: [first.id],
    viewer: "review-bot",
  });
  assert.deepEqual(forAgent[first.id], [
    { emoji: "👍", count: 1, mine: false },
    { emoji: "🎉", count: 1, mine: true },
  ]);

  await store.removeReaction({
    roomId: ROOM,
    threadId: "guild",
    messageId: first.id,
    emoji: "👍",
    actorId: "owner",
  });
  const afterRemove = await store.listReactions({
    roomId: ROOM,
    threadId: "guild",
    messageIds: [first.id, second.id],
    viewer: "owner",
  });
  assert.deepEqual(afterRemove[first.id], [{ emoji: "🎉", count: 1, mine: false }]);
  assert.equal(afterRemove[second.id], undefined);

  await store.upsertParticipant({
    roomId: ROOM,
    participant: { id: "review-bot", name: "Review Bot", kind: "agent", persona: "Reviews Go." },
  });
  let participants = await store.listParticipants(ROOM);
  assert.equal(participants.length, 1);
  assert.equal(participants[0].persona, "Reviews Go.");
  assert.equal(await store.revokeParticipant({ roomId: ROOM, id: "review-bot" }), true);
  participants = await store.listParticipants(ROOM);
  assert.ok(participants[0].revokedAt);
  assert.equal(await store.revokeParticipant({ roomId: ROOM, id: "review-bot" }), true);

  const agent = await store.upsertCustomAgent({
    roomId: ROOM,
    agent: { id: "nova", name: "Nova", kind: "custom", persona: "Ships things.", apiKey: "sk-secret" },
  });
  assertCustomAgentShape(agent, "nova", "Nova");
  assert.equal(agent.kind, "custom");
  assert.equal(agent.persona, "Ships things.");

  const agents = await store.listCustomAgents(ROOM);
  assert.equal(agents.length, 1);
  assert.deepEqual(agents[0], agent);
  assertCustomAgentShape(agents[0], "nova", "Nova");
  assert.deepEqual((await store.loadRoom(ROOM)).customAgents, agents);
  assert.equal(JSON.stringify(agents).includes("sk-secret"), false);

  const updated = await store.upsertCustomAgent({
    roomId: ROOM,
    agent: { id: "nova", name: "Nova Two", kind: "custom", persona: "Ships more.", apiKey: "sk-other" },
  });
  assertCustomAgentShape(updated, "nova", "Nova Two");
  assert.equal(updated.persona, "Ships more.");
  assert.equal(updated.createdAt, agent.createdAt);
  assert.ok(Date.parse(updated.updatedAt) >= Date.parse(agent.updatedAt));
  assert.equal(JSON.stringify(updated).includes("sk-other"), false);
  const updatedAgents = await store.listCustomAgents(ROOM);
  assert.equal(updatedAgents.length, 1);
  assert.deepEqual(updatedAgents[0], updated);
  assert.deepEqual((await store.loadRoom(ROOM)).customAgents, updatedAgents);

  if (reopen) {
    const again = await reopen();
    const persisted = await again.loadRoom(ROOM);
    assert.equal(persisted.prefs.title, "Backend Crew");
    assert.equal(persisted.threads[0].id, "dm-claude");
    assert.ok(persisted.participants[0].revokedAt);
    assert.equal((await again.listMessages({ roomId: ROOM, threadId: "guild", after: 0 })).length, 22);
    assert.deepEqual(persisted.customAgents, [updated]);
    await again.close();
  }

  assert.equal(await store.removeCustomAgent({ roomId: ROOM, id: "nova" }), true);
  assert.deepEqual(await store.listCustomAgents(ROOM), []);
  assert.equal(await store.removeCustomAgent({ roomId: ROOM, id: "nova" }), false);

  const health = await store.health();
  assert.equal(health.ok, true);
  assert.equal(health.migrated, true);
}

test("memory adapter satisfies the room store contract", async (t) => {
  const store = memoryStore();
  t.after(() => store.close());
  await exercise(store);
});

test("file adapter satisfies the room store contract and survives reopen", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "guild-room-"));
  const store = fileStore({ dir });
  t.after(async () => {
    await store.close();
    await rm(dir, { recursive: true, force: true });
  });
  await exercise(store, { reopen: async () => fileStore({ dir }) });
});

test(
  "postgres adapter satisfies the room store contract",
  { skip: process.env.GUILD_TEST_DATABASE_URL ? false : "set GUILD_TEST_DATABASE_URL to run" },
  async (t) => {
    const connectionString = process.env.GUILD_TEST_DATABASE_URL;
    const { default: pg } = await import("pg");
    const client = new pg.Client({ connectionString });
    await client.connect();
    try {
      await applyMigrations({ client, dir: MIGRATIONS_DIR });
      await client.query(
        "truncate table guild_reactions, guild_messages, guild_threads, guild_participants, guild_custom_agents, guild_rooms cascade",
      );
    } finally {
      await client.end();
    }
    const store = postgresStore({ connectionString });
    t.after(() => store.close());
    await exercise(store);
  },
);
