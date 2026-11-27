import { randomUUID } from "node:crypto";
import pg from "pg";
import { summarizeReactions } from "../social.js";
import { StoreConflictError, StoreSchemaError, StoreValidationError } from "./errors.js";
import {
  clone,
  emptyPrefs,
  iso,
  normalizeId,
  normalizeLimit,
  sanitizeCustomAgent,
} from "./shape.js";

function bodyOf(body) {
  if (body === undefined) return {};
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    throw new StoreValidationError("message body must be an object");
  }
  return clone(body);
}

function threadFrom(row) {
  return {
    id: row.id,
    title: row.title,
    members: row.members || [],
    group: Boolean(row.is_group),
    createdAt: iso(row.created_at),
  };
}

function participantFrom(row) {
  return {
    ...(row.profile || {}),
    id: row.id,
    name: row.name,
    kind: row.kind,
    persona: row.persona || "",
    createdAt: iso(row.created_at),
    lastSeenAt: iso(row.last_seen_at),
    revokedAt: iso(row.revoked_at),
  };
}

// Display-only fields for an invited persona (avatar, colour, voice). They are
// never permissions; migration 002 adds the column.
function profileOf(input) {
  const profile = input && input.profile;
  if (!profile || typeof profile !== "object" || Array.isArray(profile)) return {};
  const { apiKey, code, ...rest } = profile;
  return clone(rest);
}

function agentFrom(row) {
  return {
    ...(row.metadata || {}),
    id: row.id,
    name: row.name,
    createdAt: iso(row.created_at),
    updatedAt: iso(row.updated_at),
  };
}

export function postgresStore({ connectionString, pool } = {}) {
  if (!pool && !connectionString) {
    throw new StoreValidationError("postgres room store requires DATABASE_URL or a pool");
  }
  const ownsPool = !pool;
  const db = pool || new pg.Pool({ connectionString, max: 3, idleTimeoutMillis: 10_000 });
  let ready = null;

  async function assertSchema() {
    const { rows } = await db.query(
      "select to_regclass('public.guild_rooms') as rooms, to_regclass('public.guild_schema_migrations') as migrations",
    );
    if (!rows[0]?.rooms || !rows[0]?.migrations) throw new StoreSchemaError();
  }

  function ensureReady() {
    if (!ready) {
      ready = assertSchema().catch((err) => {
        ready = null;
        throw err;
      });
    }
    return ready;
  }

  return {
    kind: "postgres",

    async loadRoom(roomId = "guild") {
      await ensureReady();
      const id = normalizeId(roomId, "room id");
      await db.query("insert into guild_rooms (id) values ($1) on conflict (id) do nothing", [id]);
      const [roomResult, threadResult, participantResult, agentResult] = await Promise.all([
        db.query("select prefs, prefs_version from guild_rooms where id = $1", [id]),
        db.query("select id, title, members, is_group, created_at from guild_threads where room_id = $1 order by created_at", [id]),
        db.query("select id, name, kind, persona, profile, created_at, last_seen_at, revoked_at from guild_participants where room_id = $1 order by created_at", [id]),
        db.query("select id, name, metadata, created_at, updated_at from guild_custom_agents where room_id = $1 order by created_at", [id]),
      ]);
      const room = roomResult.rows[0] || {};
      return {
        id,
        prefs: { ...emptyPrefs(), ...(room.prefs || {}), version: Number(room.prefs_version || 0) },
        threads: threadResult.rows.map(threadFrom),
        participants: participantResult.rows.map(participantFrom),
        customAgents: agentResult.rows.map(agentFrom),
      };
    },

    async savePrefs({ roomId = "guild", prefs, expectedVersion } = {}) {
      await ensureReady();
      const id = normalizeId(roomId, "room id");
      if (!Number.isInteger(expectedVersion)) {
        throw new StoreValidationError("expectedVersion must be an integer");
      }
      const { rows } = await db.query(
        `update guild_rooms
            set prefs = $2::jsonb, prefs_version = prefs_version + 1, updated_at = now()
          where id = $1 and prefs_version = $3
          returning prefs_version`,
        [id, JSON.stringify(prefs || {}), expectedVersion],
      );
      if (!rows.length) throw new StoreConflictError();
      return { ...clone(prefs || {}), version: Number(rows[0].prefs_version) };
    },

    async appendMessage({ roomId = "guild", threadId, from, kind, body } = {}) {
      await ensureReady();
      const id = normalizeId(roomId, "room id");
      const thread = normalizeId(threadId, "thread id");
      const sender = normalizeId(from, "from");
      const messageKind = normalizeId(kind, "kind");
      const messageId = `m_${randomUUID()}`;
      const messageBody = bodyOf(body);
      const { rows } = await db.query(
        `insert into guild_messages (room_id, id, thread_id, from_id, kind, body)
         values ($1, $2, $3, $4, $5, $6::jsonb)
         returning seq, created_at`,
        [id, messageId, thread, sender, messageKind, JSON.stringify(messageBody)],
      );
      return {
        seq: Number(rows[0].seq),
        id: messageId,
        threadId: thread,
        from: sender,
        kind: messageKind,
        body: messageBody,
        createdAt: iso(rows[0].created_at),
      };
    },

    async listMessages({ roomId = "guild", threadId, after = 0, limit = 200 } = {}) {
      await ensureReady();
      const id = normalizeId(roomId, "room id");
      const thread = normalizeId(threadId, "thread id");
      const cursor = Number(after) || 0;
      const { rows } = await db.query(
        `select seq, id, thread_id, from_id, kind, body, created_at
           from guild_messages
          where room_id = $1 and thread_id = $2 and seq > $3
          order by seq
          limit $4`,
        [id, thread, cursor, normalizeLimit(limit)],
      );
      return rows.map((row) => ({
        seq: Number(row.seq),
        id: row.id,
        threadId: row.thread_id,
        from: row.from_id,
        kind: row.kind,
        body: row.body || {},
        createdAt: iso(row.created_at),
      }));
    },

    async hasMessage({ roomId = "guild", threadId, messageId } = {}) {
      await ensureReady();
      const { rowCount } = await db.query(
        "select 1 from guild_messages where room_id = $1 and thread_id = $2 and id = $3",
        [normalizeId(roomId, "room id"), normalizeId(threadId, "thread id"), String(messageId || "")],
      );
      return rowCount > 0;
    },

    // lastMessages returns the newest chat line (not tool or card) per thread,
    // for thread-list previews, without loading whole transcripts.
    async lastMessages(roomId = "guild") {
      await ensureReady();
      const { rows } = await db.query(
        `select distinct on (thread_id) thread_id, body, created_at
           from guild_messages
          where room_id = $1 and kind not in ('tool', 'card')
          order by thread_id, seq desc`,
        [normalizeId(roomId, "room id")],
      );
      const out = {};
      for (const row of rows) {
        out[row.thread_id] = { text: (row.body && row.body.text) || "", time: (row.body && row.body.time) || iso(row.created_at) };
      }
      return out;
    },

    async addReaction({ roomId = "guild", threadId, messageId, emoji, actorId } = {}) {
      await ensureReady();
      const id = normalizeId(roomId, "room id");
      const thread = normalizeId(threadId, "thread id");
      const message = normalizeId(messageId, "message id");
      const reaction = normalizeId(emoji, "emoji");
      const actor = normalizeId(actorId, "actor id");
      const { rowCount } = await db.query(
        `insert into guild_reactions (room_id, thread_id, message_id, emoji, actor_id)
         values ($1, $2, $3, $4, $5)
         on conflict do nothing`,
        [id, thread, message, reaction, actor],
      );
      return { added: rowCount > 0 };
    },

    async removeReaction({ roomId = "guild", threadId, messageId, emoji, actorId } = {}) {
      await ensureReady();
      const id = normalizeId(roomId, "room id");
      const thread = normalizeId(threadId, "thread id");
      const message = normalizeId(messageId, "message id");
      const reaction = normalizeId(emoji, "emoji");
      const actor = normalizeId(actorId, "actor id");
      const { rowCount } = await db.query(
        "delete from guild_reactions where room_id = $1 and thread_id = $2 and message_id = $3 and emoji = $4 and actor_id = $5",
        [id, thread, message, reaction, actor],
      );
      return { removed: rowCount > 0 };
    },

    async listReactions({ roomId = "guild", threadId, messageIds, viewer = "owner" } = {}) {
      await ensureReady();
      const id = normalizeId(roomId, "room id");
      const thread = normalizeId(threadId, "thread id");
      const ids = [...new Set((Array.isArray(messageIds) ? messageIds : []).map(String))];
      if (!ids.length) return {};
      const { rows } = await db.query(
        `select message_id, emoji, actor_id
           from guild_reactions
          where room_id = $1 and thread_id = $2 and message_id = any($3::text[])`,
        [id, thread, ids],
      );
      const byMessage = new Map();
      for (const row of rows) {
        const room = byMessage.get(row.message_id) || {};
        const actors = new Set(room[row.emoji] || []);
        actors.add(row.actor_id);
        room[row.emoji] = Array.from(actors);
        byMessage.set(row.message_id, room);
      }
      const out = {};
      for (const messageId of ids) {
        const summary = summarizeReactions(byMessage.get(messageId), viewer);
        if (summary.length) out[messageId] = summary;
      }
      return out;
    },

    async upsertParticipant({ roomId = "guild", participant } = {}) {
      await ensureReady();
      const id = normalizeId(roomId, "room id");
      const input = participant || {};
      const participantId = normalizeId(input.id, "participant id");
      const name = String(input.name || "").trim();
      if (!name) throw new StoreValidationError("participant name is required");
      const kind = normalizeId(input.kind, "participant kind");
      const { rows } = await db.query(
        `insert into guild_participants (room_id, id, name, kind, persona, last_seen_at, profile)
         values ($1, $2, $3, $4, $5, $6, $7::jsonb)
         on conflict (room_id, id) do update
            set name = excluded.name,
                kind = excluded.kind,
                persona = excluded.persona,
                profile = excluded.profile,
                last_seen_at = coalesce(excluded.last_seen_at, guild_participants.last_seen_at)
         returning id, name, kind, persona, profile, created_at, last_seen_at, revoked_at`,
        [id, participantId, name, kind, String(input.persona || ""), input.lastSeenAt || null, JSON.stringify(profileOf(input))],
      );
      return participantFrom(rows[0]);
    },

    async listParticipants(roomId = "guild") {
      await ensureReady();
      const id = normalizeId(roomId, "room id");
      const { rows } = await db.query(
        "select id, name, kind, persona, profile, created_at, last_seen_at, revoked_at from guild_participants where room_id = $1 order by created_at",
        [id],
      );
      return rows.map(participantFrom);
    },

    async revokeParticipant({ roomId = "guild", id } = {}) {
      await ensureReady();
      const room = normalizeId(roomId, "room id");
      const participantId = normalizeId(id, "participant id");
      const { rowCount } = await db.query(
        "update guild_participants set revoked_at = coalesce(revoked_at, now()) where room_id = $1 and id = $2",
        [room, participantId],
      );
      return rowCount > 0;
    },

    async upsertThread({ roomId = "guild", thread } = {}) {
      await ensureReady();
      const room = normalizeId(roomId, "room id");
      const input = thread || {};
      const id = normalizeId(input.id, "thread id");
      const title = String(input.title || "").trim();
      if (!title) throw new StoreValidationError("thread title is required");
      const { rows } = await db.query(
        `insert into guild_threads (room_id, id, title, members, is_group)
         values ($1, $2, $3, $4::jsonb, $5)
         on conflict (room_id, id) do update
            set title = excluded.title,
                members = excluded.members,
                is_group = excluded.is_group
         returning id, title, members, is_group, created_at`,
        [room, id, title, JSON.stringify(Array.isArray(input.members) ? input.members.map(String) : []), Boolean(input.group)],
      );
      return threadFrom(rows[0]);
    },

    async listThreads(roomId = "guild") {
      await ensureReady();
      const id = normalizeId(roomId, "room id");
      const { rows } = await db.query(
        "select id, title, members, is_group, created_at from guild_threads where room_id = $1 order by created_at",
        [id],
      );
      return rows.map(threadFrom);
    },

    async upsertCustomAgent({ roomId = "guild", agent } = {}) {
      await ensureReady();
      const room = normalizeId(roomId, "room id");
      const input = sanitizeCustomAgent(agent);
      const id = normalizeId(input.id, "agent id");
      const name = String(input.name || "").trim();
      if (!name) throw new StoreValidationError("agent name is required");
      const metadata = { ...input };
      delete metadata.id;
      delete metadata.name;
      delete metadata.createdAt;
      delete metadata.updatedAt;
      const { rows } = await db.query(
        `insert into guild_custom_agents (room_id, id, name, metadata)
         values ($1, $2, $3, $4::jsonb)
         on conflict (room_id, id) do update
            set name = excluded.name, metadata = excluded.metadata, updated_at = now()
         returning id, name, metadata, created_at, updated_at`,
        [room, id, name, JSON.stringify(metadata)],
      );
      return agentFrom(rows[0]);
    },

    async listCustomAgents(roomId = "guild") {
      await ensureReady();
      const id = normalizeId(roomId, "room id");
      const { rows } = await db.query(
        "select id, name, metadata, created_at, updated_at from guild_custom_agents where room_id = $1 order by created_at",
        [id],
      );
      return rows.map(agentFrom);
    },

    async removeCustomAgent({ roomId = "guild", id } = {}) {
      await ensureReady();
      const room = normalizeId(roomId, "room id");
      const agentId = normalizeId(id, "agent id");
      const { rowCount } = await db.query("delete from guild_custom_agents where room_id = $1 and id = $2", [room, agentId]);
      return rowCount > 0;
    },

    async health() {
      try {
        await assertSchema();
        return { ok: true, kind: "postgres", migrated: true };
      } catch (err) {
        return { ok: false, kind: "postgres", migrated: false, error: err.message };
      }
    },

    async close() {
      if (ownsPool) await db.end();
    },
  };
}
