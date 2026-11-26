import { StoreValidationError } from "./errors.js";

// Keep the adapter defaults identical to the current lib/store.js prefs shape,
// so a later migration can compare old and new state without translating it.
export function emptyPrefs() {
  return { version: 0, title: "", ownerName: "", setupDone: false, added: [], removed: [] };
}

export function newRoom(id) {
  return { id, prefs: emptyPrefs(), threads: [], participants: [], customAgents: [] };
}

export function clone(value) {
  return structuredClone(value);
}

export function normalizeId(value, field = "id") {
  const id = String(value || "").trim();
  if (!id) throw new StoreValidationError(`${field} is required`);
  return id;
}

export function normalizeLimit(value, fallback = 200) {
  const n = Number(value);
  if (!Number.isFinite(n)) return fallback;
  return Math.min(Math.max(Math.trunc(n), 1), 1000);
}

// Custom agents share one flat shape across adapters: adapter-owned id, name,
// createdAt and updatedAt plus arbitrary metadata fields (kind, persona, model
// and hasKey, for example). API keys stay in the encrypted cookie; the room
// store strips a key even if a caller accidentally passes one.
export function sanitizeCustomAgent(agent) {
  if (!agent || typeof agent !== "object" || Array.isArray(agent)) {
    throw new StoreValidationError("custom agent must be an object");
  }
  const { apiKey, ...rest } = agent;
  return rest;
}

export function iso(value) {
  if (!value) return null;
  return value instanceof Date ? value.toISOString() : String(value);
}

export function publicRoom(room) {
  return {
    id: room.id,
    prefs: clone(room.prefs),
    threads: clone(room.threads),
    participants: clone(room.participants),
    customAgents: clone(room.customAgents),
  };
}
