import { createHash, randomBytes, randomUUID } from "node:crypto";
import { denied, rejected, unauthorized } from "./errors.js";

// Room authorization on top of migration 002.
//
// Rules (ported from the v5 staging reference, enforced in Postgres):
// - Identities authenticate with an opaque 256-bit credential; only its
//   SHA-256 hash is stored, and a revoked identity is unauthorized.
// - Every check runs inside the same transaction as the write it guards and
//   locks the caller's membership row (FOR SHARE), so a concurrent revoke
//   cannot interleave between the check and the write.
// - owner: read, send, upload, download, grant, revoke.
//   member: read, send, upload, download. viewer: read, download.
// - Only identities can be granted; personas are not identities.
// - The sender of a message is always the authenticated identity.
// - A room id the caller has no live membership in is indistinguishable from a
//   room that does not exist.

export const MAX_TEXT = 4000;
export const MAX_FILES_PER_MESSAGE = 20;
export const HISTORY_PAGE = 200;
export const ROLES = Object.freeze({
  read: ["owner", "member", "viewer"],
  write: ["owner", "member"],
  admin: ["owner"],
});

const ID_RE = /^[A-Za-z0-9_-]{1,64}$/;
const ROOM_RE = /^[A-Za-z0-9_-]{1,64}$/;
const FILE_RE = /^[0-9a-f]{32}$/;

export function hashCredential(token) {
  return createHash("sha256").update(String(token), "utf8").digest("hex");
}

export function newCredential() {
  return randomBytes(32).toString("base64url");
}

export function assertRoomId(roomId) {
  if (typeof roomId !== "string" || !ROOM_RE.test(roomId)) throw denied();
  return roomId;
}

export async function withTx(db, fn) {
  const client = typeof db.connect === "function" ? await db.connect() : db;
  const release = client !== db && typeof client.release === "function";
  try {
    await client.query("begin");
    const out = await fn(client);
    await client.query("commit");
    return out;
  } catch (err) {
    await client.query("rollback").catch(() => {});
    throw err;
  } finally {
    if (release) client.release();
  }
}

// Operator-only: never expose identity provisioning as anonymous signup.
export async function provisionIdentity(db, { id, displayName = "" } = {}) {
  if (typeof id !== "string" || !ID_RE.test(id)) throw rejected("invalid identity id");
  const token = newCredential();
  await db.query(
    "insert into guild_identities (id, display_name, credential_hash) values ($1, $2, $3)",
    [id, String(displayName || id).slice(0, 80), hashCredential(token)],
  );
  return { id, token };
}

export async function revokeIdentity(db, id) {
  const { rowCount } = await db.query(
    "update guild_identities set revoked_at = coalesce(revoked_at, now()) where id = $1",
    [String(id || "")],
  );
  return rowCount > 0;
}

export async function identityFor(client, token) {
  if (typeof token !== "string" || token.length < 20 || token.length > 128) throw unauthorized();
  const { rows } = await client.query(
    "select id, display_name from guild_identities where credential_hash = $1 and revoked_at is null",
    [hashCredential(token)],
  );
  if (!rows.length) throw unauthorized();
  return { id: rows[0].id, displayName: rows[0].display_name };
}

// requireRole must be called inside withTx. FOR SHARE blocks a concurrent
// revoke or role change until this transaction commits.
export async function requireRole(client, token, roomId, roles = ROLES.read, { lock = "share" } = {}) {
  const identity = await identityFor(client, token);
  const room = assertRoomId(roomId);
  const { rows } = await client.query(
    `select role from guild_room_memberships
      where room_id = $1 and identity_id = $2 and revoked_at is null
      for ${lock === "update" ? "update" : "share"}`,
    [room, identity.id],
  );
  if (!rows.length || !roles.includes(rows[0].role)) throw denied();
  return { identity, role: rows[0].role, roomId: room };
}

export async function createRoom(db, token, { title } = {}) {
  const clean = typeof title === "string" ? title.trim() : "";
  if (!clean || clean.length > 100) throw rejected("invalid room title");
  return withTx(db, async (client) => {
    const identity = await identityFor(client, token);
    const roomId = randomUUID().replace(/-/g, "");
    await client.query("insert into guild_rooms (id, prefs) values ($1, $2::jsonb)", [roomId, JSON.stringify({ title: clean })]);
    await client.query(
      "insert into guild_room_memberships (room_id, identity_id, role, granted_by) values ($1, $2, 'owner', $2)",
      [roomId, identity.id],
    );
    return { id: roomId, title: clean, role: "owner" };
  });
}

export async function grantMembership(db, token, roomId, { identityId, role } = {}) {
  if (role !== "member" && role !== "viewer") throw rejected("role must be member or viewer");
  if (typeof identityId !== "string" || !ID_RE.test(identityId)) throw rejected("invalid identity id");
  return withTx(db, async (client) => {
    const { identity } = await requireRole(client, token, roomId, ROLES.admin);
    if (identityId === identity.id) throw rejected("owners cannot change their own role");
    const target = await client.query(
      "select 1 from guild_identities where id = $1 and revoked_at is null",
      [identityId],
    );
    // Personas and unknown ids are not identities and can never be granted.
    if (!target.rowCount) throw rejected("unknown identity");
    const existing = await client.query(
      "select role, revoked_at from guild_room_memberships where room_id = $1 and identity_id = $2 for update",
      [roomId, identityId],
    );
    if (existing.rowCount && existing.rows[0].role === "owner" && !existing.rows[0].revoked_at) {
      throw rejected("cannot change another owner");
    }
    await client.query(
      `insert into guild_room_memberships (room_id, identity_id, role, granted_by)
       values ($1, $2, $3, $4)
       on conflict (room_id, identity_id) do update
          set role = excluded.role, granted_by = excluded.granted_by,
              revoked_at = null, updated_at = now()`,
      [roomId, identityId, role, identity.id],
    );
    return { roomId, identityId, role };
  });
}

export async function revokeMembership(db, token, roomId, { identityId } = {}) {
  if (typeof identityId !== "string" || !ID_RE.test(identityId)) throw rejected("invalid identity id");
  return withTx(db, async (client) => {
    const { identity } = await requireRole(client, token, roomId, ROLES.admin);
    if (identityId === identity.id) throw rejected("owners cannot revoke themselves");
    const { rows } = await client.query(
      "select role from guild_room_memberships where room_id = $1 and identity_id = $2 and revoked_at is null for update",
      [roomId, identityId],
    );
    if (!rows.length) return { roomId, identityId, revoked: false };
    if (rows[0].role === "owner") throw rejected("cannot revoke another owner");
    await client.query(
      "update guild_room_memberships set revoked_at = now(), updated_at = now() where room_id = $1 and identity_id = $2",
      [roomId, identityId],
    );
    return { roomId, identityId, revoked: true };
  });
}

function messageFrom(row) {
  const body = row.body || {};
  return {
    seq: Number(row.seq),
    id: row.id,
    from: row.from_id,
    name: body.name || row.from_id,
    text: body.text || "",
    files: Array.isArray(body.files) ? body.files : [],
    time: body.time || (row.created_at instanceof Date ? row.created_at.toISOString() : String(row.created_at || "")),
  };
}

function validateSend({ text, files = [], idempotencyKey } = {}) {
  if (typeof text !== "string" || text.length > MAX_TEXT) throw rejected("invalid message");
  if (!Array.isArray(files) || files.length > MAX_FILES_PER_MESSAGE || !files.every((f) => typeof f === "string" && FILE_RE.test(f))) {
    throw rejected("invalid message");
  }
  if (!text.trim() && !files.length) throw rejected("invalid message");
  if (idempotencyKey !== undefined && idempotencyKey !== null
    && (typeof idempotencyKey !== "string" || idempotencyKey.length < 1 || idempotencyKey.length > 128)) {
    throw rejected("invalid idempotency key");
  }
  return { text: text.trim(), files: [...new Set(files)], idempotencyKey: idempotencyKey || null };
}

// sendMessage writes to the room's "guild" thread so durable-room messages
// also appear in the existing web UI transcript for that room.
export async function sendMessage(db, token, roomId, input = {}) {
  const { text, files, idempotencyKey } = validateSend(input);
  return withTx(db, async (client) => {
    const { identity } = await requireRole(client, token, roomId, ROLES.write);
    if (idempotencyKey) {
      const claim = await client.query(
        `insert into guild_send_idempotency (room_id, identity_id, idem_key)
         values ($1, $2, $3) on conflict do nothing returning idem_key`,
        [roomId, identity.id, idempotencyKey],
      );
      if (!claim.rowCount) {
        const prior = await client.query(
          `select m.seq, m.id, m.from_id, m.body, m.created_at
             from guild_send_idempotency i
             join guild_messages m on m.room_id = i.room_id and m.id = i.message_id
            where i.room_id = $1 and i.identity_id = $2 and i.idem_key = $3`,
          [roomId, identity.id, idempotencyKey],
        );
        if (!prior.rowCount) throw rejected("duplicate request still in progress");
        return { ...messageFrom(prior.rows[0]), duplicate: true };
      }
    }
    for (const fileId of files) {
      const f = await client.query(
        "select 1 from guild_attachments where room_id = $1 and id = $2 and scan_status = 'clean' for share",
        [roomId, fileId],
      );
      if (!f.rowCount) throw rejected("attachment unavailable");
    }
    const messageId = `m_${randomUUID()}`;
    const body = { name: identity.displayName || identity.id, text, files, time: new Date().toISOString(), mine: false, status: "" };
    const { rows } = await client.query(
      `insert into guild_messages (room_id, id, thread_id, from_id, kind, body)
       values ($1, $2, 'guild', $3, 'person', $4::jsonb)
       returning seq, id, from_id, body, created_at`,
      [roomId, messageId, identity.id, JSON.stringify(body)],
    );
    if (idempotencyKey) {
      await client.query(
        "update guild_send_idempotency set message_id = $4 where room_id = $1 and identity_id = $2 and idem_key = $3",
        [roomId, identity.id, idempotencyKey, messageId],
      );
    }
    return { ...messageFrom(rows[0]), duplicate: false };
  });
}

export async function listRoomMessages(db, token, roomId, { after = 0 } = {}) {
  const cursor = Number(after);
  if (!Number.isSafeInteger(cursor) || cursor < 0) throw rejected("invalid cursor");
  return withTx(db, async (client) => {
    await requireRole(client, token, roomId, ROLES.read);
    const { rows } = await client.query(
      `select seq, id, from_id, body, created_at from guild_messages
        where room_id = $1 and thread_id = 'guild' and seq > $2 and kind not in ('tool', 'card')
        order by seq limit $3`,
      [roomId, cursor, HISTORY_PAGE],
    );
    const messages = rows.map(messageFrom);
    return { messages, next: messages.length ? messages[messages.length - 1].seq : cursor };
  });
}

// Upload pre-check: the scan is slow, so fail fast before reading the scanner.
export async function assertCanUpload(db, token, roomId) {
  return withTx(db, (client) => requireRole(client, token, roomId, ROLES.write));
}

// recordAttachment re-checks membership after the scan, then stores the
// object and its metadata in one transaction. If the metadata write fails the
// stored object is removed so no unreferenced object is left behind.
export async function recordAttachment(db, token, roomId, { fileId, name, sha256, size, mime }, storage, data) {
  return withTx(db, async (client) => {
    const { identity } = await requireRole(client, token, roomId, ROLES.write);
    const key = await storage.put({ roomId, fileId, data, sha256 });
    try {
      await client.query(
        `insert into guild_attachments (room_id, id, uploader_id, object_key, name, sha256, size_bytes, mime, scan_status)
         values ($1, $2, $3, $4, $5, $6, $7, $8, 'clean')`,
        [roomId, fileId, identity.id, key, name, sha256, size, mime],
      );
    } catch (err) {
      await storage.remove(key).catch(() => {});
      throw err;
    }
    return { id: fileId, name, size, sha256, mime };
  });
}

export async function readAttachment(db, token, roomId, fileId, storage) {
  if (typeof fileId !== "string" || !FILE_RE.test(fileId)) throw denied();
  return withTx(db, async (client) => {
    await requireRole(client, token, roomId, ROLES.read);
    const { rows } = await client.query(
      `select object_key, name, sha256, size_bytes, mime from guild_attachments
        where room_id = $1 and id = $2 and scan_status = 'clean'`,
      [roomId, fileId],
    );
    if (!rows.length) throw denied();
    const meta = rows[0];
    const data = await storage.get({ roomId, key: meta.object_key, sha256: meta.sha256, size: Number(meta.size_bytes) });
    return { data, name: meta.name, mime: meta.mime, sha256: meta.sha256 };
  });
}
