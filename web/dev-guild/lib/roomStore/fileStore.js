import { existsSync, mkdirSync, readFileSync, renameSync, unlinkSync, writeFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { join } from "node:path";
import { summarizeReactions } from "../social.js";
import { StoreConflictError, StoreError, StoreValidationError } from "./errors.js";
import {
  clone,
  emptyPrefs,
  newRoom,
  normalizeId,
  normalizeLimit,
  publicRoom,
  sanitizeCustomAgent,
} from "./shape.js";

const DEFAULT_DIR = "/tmp/dev-guild-room";

function now() {
  return new Date().toISOString();
}

function bodyOf(body) {
  if (body === undefined) return {};
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    throw new StoreValidationError("message body must be an object");
  }
  return clone(body);
}

function membersOf(members) {
  if (!Array.isArray(members)) return [];
  return members.map((id) => String(id));
}

// Single-instance durable adapter. It writes one JSON file and keeps the
// current app's shape; Postgres is the multi-instance adapter.
export function fileStore({ dir = process.env.GUILD_DATA_DIR || DEFAULT_DIR, file = "room-state.json" } = {}) {
  const statePath = join(dir, file);
  let chain = Promise.resolve();

  function withLock(fn) {
    const run = chain.then(fn, fn);
    chain = run.then(() => undefined, () => undefined);
    return run;
  }

  function readState() {
    if (!existsSync(statePath)) return { version: 1, rooms: {} };
    let parsed;
    try {
      parsed = JSON.parse(readFileSync(statePath, "utf8"));
    } catch {
      throw new StoreError("room state file is not valid JSON");
    }
    if (!parsed || parsed.version !== 1 || !parsed.rooms || typeof parsed.rooms !== "object") {
      throw new StoreError("room state file has an unsupported shape");
    }
    return parsed;
  }

  function writeState(state) {
    mkdirSync(dir, { recursive: true });
    const temp = `${statePath}.${process.pid}.${randomUUID()}.tmp`;
    try {
      writeFileSync(temp, JSON.stringify(state));
      renameSync(temp, statePath);
    } catch (err) {
      try {
        unlinkSync(temp);
      } catch {
        // best effort
      }
      throw err;
    }
  }

  function roomFor(state, roomId) {
    const id = normalizeId(roomId, "room id");
    if (!state.rooms[id]) {
      state.rooms[id] = { ...newRoom(id), messages: [], reactions: {}, nextSeq: 0 };
    }
    return state.rooms[id];
  }

  function withRoom(roomId, fn) {
    return withLock(async () => {
      const state = readState();
      const room = roomFor(state, roomId);
      const result = await fn(room, state);
      writeState(state);
      return result;
    });
  }

  return {
    kind: "file",

    async loadRoom(roomId = "guild") {
      return withLock(async () => {
        const state = readState();
        const room = roomFor(state, roomId);
        writeState(state);
        return publicRoom(room);
      });
    },

    async savePrefs({ roomId = "guild", prefs, expectedVersion } = {}) {
      return withRoom(roomId, async (room) => {
        if (!Number.isInteger(expectedVersion)) {
          throw new StoreValidationError("expectedVersion must be an integer");
        }
        if (expectedVersion !== room.prefs.version) throw new StoreConflictError();
        const next = { ...emptyPrefs(), ...clone(prefs || {}), version: room.prefs.version + 1 };
        room.prefs = next;
        return clone(next);
      });
    },

    async appendMessage({ roomId = "guild", threadId, from, kind, body } = {}) {
      return withRoom(roomId, async (room) => {
        const thread = normalizeId(threadId, "thread id");
        const sender = normalizeId(from, "from");
        const messageKind = normalizeId(kind, "kind");
        room.nextSeq = Number(room.nextSeq || 0) + 1;
        const message = {
          seq: room.nextSeq,
          id: `m_${room.nextSeq}_${randomUUID().slice(0, 8)}`,
          threadId: thread,
          from: sender,
          kind: messageKind,
          body: bodyOf(body),
          createdAt: now(),
        };
        room.messages = room.messages || [];
        room.messages.push(message);
        return clone(message);
      });
    },

    async listMessages({ roomId = "guild", threadId, after = 0, limit = 200 } = {}) {
      const state = readState();
      const room = state.rooms[normalizeId(roomId, "room id")];
      if (!room) return [];
      const thread = normalizeId(threadId, "thread id");
      const cursor = Number(after) || 0;
      return (room.messages || [])
        .filter((message) => message.threadId === thread && message.seq > cursor)
        .slice(0, normalizeLimit(limit))
        .map((message) => clone(message));
    },

    async addReaction({ roomId = "guild", threadId, messageId, emoji, actorId } = {}) {
      return withRoom(roomId, async (room) => {
        const key = `${normalizeId(threadId, "thread id")}:${normalizeId(messageId, "message id")}`;
        const reaction = normalizeId(emoji, "emoji");
        const actor = normalizeId(actorId, "actor id");
        room.reactions = room.reactions || {};
        room.reactions[key] = room.reactions[key] || {};
        const actors = new Set(room.reactions[key][reaction] || []);
        const added = !actors.has(actor);
        actors.add(actor);
        room.reactions[key][reaction] = Array.from(actors);
        return { added };
      });
    },

    async removeReaction({ roomId = "guild", threadId, messageId, emoji, actorId } = {}) {
      return withRoom(roomId, async (room) => {
        const key = `${normalizeId(threadId, "thread id")}:${normalizeId(messageId, "message id")}`;
        const reaction = normalizeId(emoji, "emoji");
        const actor = normalizeId(actorId, "actor id");
        const actors = new Set(room.reactions?.[key]?.[reaction] || []);
        if (!actors.has(actor)) return { removed: false };
        actors.delete(actor);
        if (actors.size) room.reactions[key][reaction] = Array.from(actors);
        else delete room.reactions[key][reaction];
        if (!Object.keys(room.reactions[key] || {}).length) delete room.reactions[key];
        return { removed: true };
      });
    },

    async listReactions({ roomId = "guild", threadId, messageIds, viewer = "owner" } = {}) {
      const state = readState();
      const room = state.rooms[normalizeId(roomId, "room id")];
      if (!room) return {};
      const thread = normalizeId(threadId, "thread id");
      const ids = Array.isArray(messageIds) ? messageIds.map(String) : [];
      const out = {};
      for (const messageId of ids) {
        const summary = summarizeReactions(room.reactions?.[`${thread}:${messageId}`], viewer);
        if (summary.length) out[messageId] = summary;
      }
      return out;
    },

    async upsertParticipant({ roomId = "guild", participant } = {}) {
      return withRoom(roomId, async (room) => {
        const input = participant || {};
        const id = normalizeId(input.id, "participant id");
        const name = String(input.name || "").trim();
        if (!name) throw new StoreValidationError("participant name is required");
        const kind = normalizeId(input.kind, "participant kind");
        const existing = room.participants.find((item) => item.id === id);
        const next = {
          id,
          name,
          kind,
          persona: String(input.persona || ""),
          createdAt: existing?.createdAt || now(),
          lastSeenAt: input.lastSeenAt !== undefined ? input.lastSeenAt : (existing?.lastSeenAt || null),
          revokedAt: input.revokedAt !== undefined ? input.revokedAt : (existing?.revokedAt || null),
        };
        const index = room.participants.findIndex((item) => item.id === id);
        if (index >= 0) room.participants[index] = next;
        else room.participants.push(next);
        return clone(next);
      });
    },

    async listParticipants(roomId = "guild") {
      const state = readState();
      const room = state.rooms[normalizeId(roomId, "room id")];
      return room ? clone(room.participants || []) : [];
    },

    async revokeParticipant({ roomId = "guild", id } = {}) {
      return withRoom(roomId, async (room) => {
        const participantId = normalizeId(id, "participant id");
        const participant = room.participants.find((item) => item.id === participantId);
        if (!participant) return false;
        participant.revokedAt = now();
        return true;
      });
    },

    async upsertThread({ roomId = "guild", thread } = {}) {
      return withRoom(roomId, async (room) => {
        const input = thread || {};
        const id = normalizeId(input.id, "thread id");
        const title = String(input.title || "").trim();
        if (!title) throw new StoreValidationError("thread title is required");
        const existing = room.threads.find((item) => item.id === id);
        const next = {
          id,
          title,
          members: membersOf(input.members),
          group: Boolean(input.group),
          createdAt: existing?.createdAt || now(),
        };
        const index = room.threads.findIndex((item) => item.id === id);
        if (index >= 0) room.threads[index] = next;
        else room.threads.push(next);
        return clone(next);
      });
    },

    async listThreads(roomId = "guild") {
      const state = readState();
      const room = state.rooms[normalizeId(roomId, "room id")];
      return room ? clone(room.threads || []) : [];
    },

    async upsertCustomAgent({ roomId = "guild", agent } = {}) {
      return withRoom(roomId, async (room) => {
        const input = sanitizeCustomAgent(agent);
        const id = normalizeId(input.id, "agent id");
        const name = String(input.name || "").trim();
        if (!name) throw new StoreValidationError("agent name is required");
        const existing = room.customAgents.find((item) => item.id === id);
        const next = { ...input, id, name, createdAt: existing?.createdAt || now(), updatedAt: now() };
        const index = room.customAgents.findIndex((item) => item.id === id);
        if (index >= 0) room.customAgents[index] = next;
        else room.customAgents.push(next);
        return clone(next);
      });
    },

    async listCustomAgents(roomId = "guild") {
      const state = readState();
      const room = state.rooms[normalizeId(roomId, "room id")];
      return room ? clone(room.customAgents || []) : [];
    },

    async removeCustomAgent({ roomId = "guild", id } = {}) {
      return withRoom(roomId, async (room) => {
        const agentId = normalizeId(id, "agent id");
        const before = room.customAgents.length;
        room.customAgents = room.customAgents.filter((item) => item.id !== agentId);
        return room.customAgents.length !== before;
      });
    },

    async health() {
      try {
        mkdirSync(dir, { recursive: true });
        readState();
        return { ok: true, kind: "file", migrated: true };
      } catch (err) {
        return { ok: false, kind: "file", migrated: false, error: err.message };
      }
    },

    async close() {},
  };
}
