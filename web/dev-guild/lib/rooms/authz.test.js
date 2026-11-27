import assert from "node:assert/strict";
import test from "node:test";
import { MIGRATIONS_DIR, applyMigrations } from "../roomStore/migrate.js";
import {
  createRoom,
  grantMembership,
  hashCredential,
  listRoomMessages,
  provisionIdentity,
  readAttachment,
  recordAttachment,
  revokeIdentity,
  revokeMembership,
  sendMessage,
  assertCanUpload,
} from "./authz.js";
import { sha256Hex } from "./storage.js";

// Contract tests against a REAL Postgres. Run with GUILD_TEST_DATABASE_URL
// pointing at a disposable database; the tables are truncated first.
const URL = process.env.GUILD_TEST_DATABASE_URL;
const skip = URL ? false : "set GUILD_TEST_DATABASE_URL to run";

async function setup() {
  const { default: pg } = await import("pg");
  const pool = new pg.Pool({ connectionString: URL, max: 6 });
  const client = await pool.connect();
  try {
    await applyMigrations({ client, dir: MIGRATIONS_DIR });
    await client.query("truncate table guild_identities, guild_rooms cascade");
  } finally {
    client.release();
  }
  return pool;
}

function memStorage({ failPut = false } = {}) {
  const objects = new Map();
  return {
    objects,
    async put({ roomId, fileId, data, sha256 }) {
      if (failPut) throw new Error("storage down");
      assert.equal(sha256Hex(data), sha256);
      const key = `rooms/${roomId}/${fileId}`;
      objects.set(key, Buffer.from(data));
      return key;
    },
    async get({ key, sha256, size }) {
      const data = objects.get(key);
      if (!data || data.length !== size || sha256Hex(data) !== sha256) {
        throw Object.assign(new Error("attachment integrity check failed"), { status: 502 });
      }
      return data;
    },
    async remove(key) { objects.delete(key); },
  };
}

const status = (code) => (err) => err.status === code;
const fid = (c) => c.repeat(32);

test("identities, roles and room isolation are enforced in Postgres", { skip }, async (t) => {
  const pool = await setup();
  t.after(() => pool.end());

  const owner = await provisionIdentity(pool, { id: "deonte", displayName: "Deonte" });
  const member = await provisionIdentity(pool, { id: "codex" });
  const viewer = await provisionIdentity(pool, { id: "reviewer" });
  const outsider = await provisionIdentity(pool, { id: "stranger" });

  // Only the hash is stored.
  const { rows } = await pool.query("select credential_hash from guild_identities where id = 'deonte'");
  assert.equal(rows[0].credential_hash, hashCredential(owner.token));
  assert.notEqual(rows[0].credential_hash, owner.token);
  assert.ok(owner.token.length >= 43, "256-bit credential");

  await assert.rejects(() => createRoom(pool, "not-a-real-credential-xxxx", { title: "x" }), status(401));
  const room = await createRoom(pool, owner.token, { title: "Infra" });
  const other = await createRoom(pool, outsider.token, { title: "Elsewhere" });

  await grantMembership(pool, owner.token, room.id, { identityId: "codex", role: "member" });
  await grantMembership(pool, owner.token, room.id, { identityId: "reviewer", role: "viewer" });

  // Only owners grant or revoke; personas/unknown ids cannot be granted.
  await assert.rejects(() => grantMembership(pool, member.token, room.id, { identityId: "stranger", role: "member" }), status(403));
  await assert.rejects(() => grantMembership(pool, owner.token, room.id, { identityId: "claude-persona", role: "member" }), status(400));
  await assert.rejects(() => grantMembership(pool, owner.token, room.id, { identityId: "codex", role: "owner" }), status(400));
  await assert.rejects(() => revokeMembership(pool, viewer.token, room.id, { identityId: "codex" }), status(403));

  // Sender is server-derived.
  const sent = await sendMessage(pool, member.token, room.id, { text: "hello", from: "deonte" });
  assert.equal(sent.from, "codex");
  await sendMessage(pool, owner.token, room.id, { text: "hi codex" });

  // Viewers read but cannot send or upload.
  await assert.rejects(() => sendMessage(pool, viewer.token, room.id, { text: "nope" }), status(403));
  await assert.rejects(() => assertCanUpload(pool, viewer.token, room.id), status(403));
  const seen = await listRoomMessages(pool, viewer.token, room.id);
  assert.deepEqual(seen.messages.map((m) => [m.from, m.text]), [["codex", "hello"], ["deonte", "hi codex"]]);

  // No cross-room access, and a foreign room looks the same as a missing one.
  await assert.rejects(() => listRoomMessages(pool, outsider.token, room.id), status(403));
  await assert.rejects(() => sendMessage(pool, owner.token, other.id, { text: "x" }), status(403));
  await assert.rejects(() => listRoomMessages(pool, owner.token, "does-not-exist"), status(403));

  // Revocation takes effect on the next request.
  await revokeMembership(pool, owner.token, room.id, { identityId: "codex" });
  await assert.rejects(() => sendMessage(pool, member.token, room.id, { text: "after revoke" }), status(403));
  await assert.rejects(() => listRoomMessages(pool, member.token, room.id), status(403));
  await assert.rejects(() => revokeMembership(pool, owner.token, room.id, { identityId: "deonte" }), status(400));

  // A revoked identity is unauthorized everywhere.
  await revokeIdentity(pool, "reviewer");
  await assert.rejects(() => listRoomMessages(pool, viewer.token, room.id), status(401));
});

test("history pages 200 at a time and sends are idempotent", { skip }, async (t) => {
  const pool = await setup();
  t.after(() => pool.end());
  const owner = await provisionIdentity(pool, { id: "owner1" });
  const room = await createRoom(pool, owner.token, { title: "Paging" });
  await pool.query(
    `insert into guild_messages (room_id, id, thread_id, from_id, kind, body)
     select $1, 'm_seed_' || g, 'guild', 'owner1', 'person', jsonb_build_object('text', 'n' || g)
       from generate_series(1, 250) g`,
    [room.id],
  );
  const first = await listRoomMessages(pool, owner.token, room.id);
  assert.equal(first.messages.length, 200);
  const second = await listRoomMessages(pool, owner.token, room.id, { after: first.next });
  assert.equal(second.messages.length, 50);
  assert.equal(second.messages[49].text, "n250");
  await assert.rejects(() => listRoomMessages(pool, owner.token, room.id, { after: -1 }), status(400));

  const results = await Promise.all(
    Array.from({ length: 5 }, () => sendMessage(pool, owner.token, room.id, { text: "once", idempotencyKey: "k-1" })),
  );
  const ids = new Set(results.filter((r) => r.id).map((r) => r.id));
  assert.equal(ids.size, 1, "concurrent retries with one key produce one message");
  const { rows } = await pool.query("select count(*)::int as n from guild_messages where room_id = $1 and body->>'text' = 'once'", [room.id]);
  assert.equal(rows[0].n, 1);
});

test("attachments: scanned bytes only, room-scoped, SHA-256 verified", { skip }, async (t) => {
  const pool = await setup();
  t.after(() => pool.end());
  const owner = await provisionIdentity(pool, { id: "own" });
  const viewer = await provisionIdentity(pool, { id: "view" });
  const outsider = await provisionIdentity(pool, { id: "out" });
  const room = await createRoom(pool, owner.token, { title: "Files" });
  const other = await createRoom(pool, outsider.token, { title: "Other" });
  await grantMembership(pool, owner.token, room.id, { identityId: "view", role: "viewer" });

  const storage = memStorage();
  const data = Buffer.from("clean synthetic file");
  const meta = { fileId: fid("a"), name: "notes.txt", sha256: sha256Hex(data), size: data.length, mime: "text/plain" };
  await recordAttachment(pool, owner.token, room.id, meta, storage, data);
  assert.ok(storage.objects.has(`rooms/${room.id}/${fid("a")}`));

  // Viewer can download (verified), outsider cannot, viewer cannot upload.
  const got = await readAttachment(pool, viewer.token, room.id, fid("a"), storage);
  assert.deepEqual(got.data, data);
  await assert.rejects(() => readAttachment(pool, outsider.token, room.id, fid("a"), storage), status(403));
  await assert.rejects(() => readAttachment(pool, outsider.token, other.id, fid("a"), storage), status(403));
  await assert.rejects(
    () => recordAttachment(pool, viewer.token, room.id, { ...meta, fileId: fid("b") }, storage, data),
    status(403),
  );

  // Tampered object fails the integrity check.
  storage.objects.set(`rooms/${room.id}/${fid("a")}`, Buffer.from("tampered!!"));
  await assert.rejects(() => readAttachment(pool, owner.token, room.id, fid("a"), storage), status(502));

  // A message may only reference clean files from the same room.
  await sendMessage(pool, owner.token, room.id, { text: "see file", files: [fid("a")] });
  await assert.rejects(() => sendMessage(pool, outsider.token, other.id, { text: "steal", files: [fid("a")] }), status(400));

  // Storage failure leaves no metadata row; metadata failure removes the object.
  await assert.rejects(() => recordAttachment(pool, owner.token, room.id, { ...meta, fileId: fid("c") }, memStorage({ failPut: true }), data));
  const { rows } = await pool.query("select count(*)::int as n from guild_attachments where id = $1", [fid("c")]);
  assert.equal(rows[0].n, 0);
  const dup = memStorage();
  await assert.rejects(() => recordAttachment(pool, owner.token, room.id, { ...meta }, dup, data));
  assert.equal(dup.objects.size, 0, "object removed when the metadata insert fails");
});
