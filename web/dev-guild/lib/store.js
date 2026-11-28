import { existsSync, readFileSync, writeFileSync } from "fs";
import { getGroupName, getOwnerName } from "./auth.js";
import { BUILTIN_CAST, statusFor, systemPromptFor } from "./cast.js";
import { DEMO_AGENT_IDS, isDemoMode } from "./demo.js";
import { directoryCharacters } from "./directory.js";
import { isReaction, summarizeReactions, viewerIdFromAuth } from "./social.js";
import { StoreConflictError } from "./roomStore/errors.js";
import { postgresStore } from "./roomStore/postgresStore.js";
import { redactSecrets } from "./secrets/detector.js";

const STATE_PATH = "/tmp/dev-guild-state.json";

function emptyPrefs() {
  return { version: 0, title: "", ownerName: "", setupDone: false, added: [], removed: [] };
}

function nextVersion(prevVersion) {
  return Math.max(Number(prevVersion || 0) + 1, Date.now());
}

function initialState() {
  return {
    seq: 0,
    messages: {
      guild: [],
    },
    threads: [],
    customAgents: [],
    invitedParticipants: [],
    reactions: {},
    groupPrefs: emptyPrefs(),
  };
}

function loadState() {
  if (globalThis.__devGuildState) return globalThis.__devGuildState;
  let state = initialState();
  try {
    if (existsSync(STATE_PATH)) {
      const parsed = JSON.parse(readFileSync(STATE_PATH, "utf8"));
      if (parsed && typeof parsed === "object" && parsed.messages) {
        state = {
          ...initialState(),
          ...parsed,
          reactions: { ...(parsed.reactions || {}) },
          groupPrefs: { ...emptyPrefs(), ...(parsed.groupPrefs || {}) },
        };
      }
    }
  } catch {
    state = initialState();
  }
  if (typeof state.seq !== "number") state.seq = 0;
  globalThis.__devGuildState = state;
  return state;
}

function saveState(state) {
  globalThis.__devGuildState = state;
  try {
    writeFileSync(STATE_PATH, JSON.stringify(state));
  } catch {
    // ignore read-only fs errors
  }
}

export function resetStoreForTest() {
  globalThis.__devGuildState = initialState();
}

// ---------------------------------------------------------------------------
// Durable backend selection. GUILD_STORE=postgres (with DATABASE_URL) routes
// every read and write below through the Postgres room store. When it is unset
// the original in-memory + /tmp state is used, so the demo and local dev work
// with no database. Demo mode never persists, whatever the backend.
// ---------------------------------------------------------------------------
const ROOM_ID = "guild";

export function usesPostgres(env) {
  const source = env || process.env;
  const kind = source.GUILD_STORE !== undefined ? source.GUILD_STORE : process.env.GUILD_STORE;
  return String(kind || "").trim().toLowerCase() === "postgres";
}

export function roomStore() {
  if (!globalThis.__devGuildRoomStore) {
    const connectionString = String(process.env.DATABASE_URL || "").trim();
    if (!connectionString) throw new Error("GUILD_STORE=postgres requires DATABASE_URL");
    globalThis.__devGuildRoomStore = postgresStore({ connectionString });
  }
  return globalThis.__devGuildRoomStore;
}

// Tests inject a postgres-backed store (or reset to null) through this hook.
export function setRoomStoreForTest(store) {
  globalThis.__devGuildRoomStore = store || null;
}

export function storeKind(env) {
  return usesPostgres(env) ? "postgres" : "memory";
}

function prefsFromRoom(room) {
  const { version: rowVersion, stamp, ...rest } = room.prefs || {};
  return {
    prefs: { ...emptyPrefs(), ...rest, version: Number(stamp || 0) || 0 },
    rowVersion: Number(rowVersion || 0) || 0,
  };
}

function profileOf(participant) {
  // The invite code is derived from the owner token; it is never persisted.
  const { id, name, kind, persona, code, createdAt, lastSeenAt, revokedAt, ...profile } = participant || {};
  return profile;
}

// readState returns the slice of state the catalog and thread helpers need, in
// the same shape for both backends.
async function readState(env) {
  if (!usesPostgres(env)) return loadState();
  const room = await roomStore().loadRoom(ROOM_ID);
  return {
    customAgents: room.customAgents || [],
    invitedParticipants: (room.participants || [])
      .filter((p) => !p.revokedAt)
      .map(({ createdAt, lastSeenAt, revokedAt, ...p }) => p),
    threads: room.threads || [],
    groupPrefs: prefsFromRoom(room).prefs,
  };
}

async function readServerPrefs(env) {
  if (!usesPostgres(env)) return { prefs: { ...emptyPrefs(), ...(loadState().groupPrefs || {}) }, rowVersion: 0 };
  return prefsFromRoom(await roomStore().loadRoom(ROOM_ID));
}

// writePrefs applies compute(previousMergedPrefs) and persists it. Postgres
// writes use the room's optimistic version and retry on a concurrent update.
async function writePrefs(cookiePrefs, env, compute) {
  if (!usesPostgres(env)) {
    const state = loadState();
    const prefs = compute(mergePrefs(state.groupPrefs, cookiePrefs, env));
    state.groupPrefs = prefs;
    saveState(state);
    return prefs;
  }
  for (let attempt = 0; attempt < 4; attempt += 1) {
    const { prefs: server, rowVersion } = await readServerPrefs(env);
    const prefs = compute(mergePrefs(server, cookiePrefs, env));
    const { version, ...rest } = prefs;
    try {
      await roomStore().savePrefs({ roomId: ROOM_ID, prefs: { ...rest, stamp: version }, expectedVersion: rowVersion });
      return prefs;
    } catch (err) {
      if (!(err instanceof StoreConflictError) || attempt === 3) throw err;
    }
  }
  throw new StoreConflictError();
}

export function hasVmBackend(env) {
  const source = env || process.env;
  return Boolean(String(source.GUILD_BACKEND_URL || "").trim());
}

export async function vmFetch(token, method, path, body, env) {
  const source = env || process.env;
  const rawRoot = String(source.GUILD_BACKEND_URL || "").trim();
  if (!rawRoot) {
    const err = new Error("vm backend unset");
    err.status = 502;
    throw err;
  }
  const root = new URL(rawRoot);
  const target = new URL(String(path || "").replace(/^\//, ""), new URL(root.origin + "/"));
  if (target.origin !== root.origin || target.username || target.password) {
    const err = new Error("bad path");
    err.status = 400;
    throw err;
  }
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 4000);
  try {
    const init = {
      method,
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
      signal: ctrl.signal,
    };
    if (body !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    const res = await fetch(target, init);
    const text = await res.text();
    let data = {};
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = { error: text };
      }
    }
    if (!res.ok) {
      const err = new Error(data.error || "The guild bridge refused the request.");
      err.status = res.status;
      throw err;
    }
    return data;
  } finally {
    clearTimeout(timer);
  }
}

export function sanitizeCustomAgents(list) {
  return (list || []).map((a) => ({
    ...a,
    hasKey: Boolean(a.apiKey || a.hasKey),
    apiKey: undefined,
  }));
}

function mergeAgentLists(stateAgents, cookieAgents) {
  const byId = new Map();
  for (const a of stateAgents || []) byId.set(a.id, a);
  for (const a of cookieAgents || []) byId.set(a.id, a);
  return Array.from(byId.values());
}

export async function mergeCustomAgents(cookieAgents, env) {
  const state = await readState(env);
  return mergeAgentLists(state.customAgents, cookieAgents);
}

export async function saveCustomAgent(agent, env) {
  if (usesPostgres(env)) {
    // The room store strips apiKey; keys stay in the encrypted cookie.
    await roomStore().upsertCustomAgent({ roomId: ROOM_ID, agent });
    return agent;
  }
  const state = loadState();
  const list = state.customAgents || [];
  const idx = list.findIndex((item) => item.id === agent.id);
  if (idx >= 0) list[idx] = agent;
  else list.push(agent);
  state.customAgents = list;
  if (!state.groupPrefs) state.groupPrefs = emptyPrefs();
  state.groupPrefs.removed = (state.groupPrefs.removed || []).filter((id) => id !== agent.id);
  if (!(state.groupPrefs.added || []).includes(agent.id)) {
    state.groupPrefs.added = [...(state.groupPrefs.added || []), agent.id];
  }
  saveState(state);
  return agent;
}

// Invited participants are AI personas or guests shown in the room. They are
// never authenticated identities and never carry room permissions; see
// lib/rooms/authz.js for identities and memberships.
export async function registerInvitedParticipant(participant, env) {
  if (usesPostgres(env)) {
    await roomStore().upsertParticipant({
      roomId: ROOM_ID,
      participant: { ...participant, profile: profileOf(participant) },
    });
    return participant;
  }
  const state = loadState();
  const list = state.invitedParticipants || [];
  const idx = list.findIndex((item) => item.id === participant.id);
  if (idx >= 0) list[idx] = participant;
  else list.push(participant);
  state.invitedParticipants = list;
  saveState(state);
  return participant;
}

function mergePrefs(serverPrefs, cookiePrefs, env) {
  const sp = { ...emptyPrefs(), ...(serverPrefs || {}) };
  const cp = { ...emptyPrefs(), ...(cookiePrefs || {}) };
  const hasCookiePrefs = Boolean(
    cp.version || cp.setupDone || cp.title || cp.ownerName || (cp.added && cp.added.length) || (cp.removed && cp.removed.length),
  );
  const cpVer = Number(cp.version || 0) || 0;
  const spVer = Number(sp.version || 0) || 0;
  const useCookie = hasCookiePrefs && cpVer >= spVer;
  const primary = useCookie ? cp : sp;
  const secondary = useCookie ? sp : cp;
  const removed = new Set(primary.removed || []);
  const added = [];
  const seen = new Set();
  for (const id of primary.added || []) {
    if (!id || id === "owner" || removed.has(id) || seen.has(id)) continue;
    seen.add(id);
    added.push(id);
  }
  return {
    version: Math.max(cpVer, spVer),
    title: String(primary.title || secondary.title || getGroupName(env)).trim().slice(0, 60) || "Dev Guild",
    ownerName: String(primary.ownerName || secondary.ownerName || getOwnerName(env)).trim().slice(0, 40) || "Owner",
    setupDone: Boolean(primary.setupDone || (!useCookie && secondary.setupDone)),
    added,
    removed: Array.from(removed),
  };
}

export async function mergeGroupPrefs(cookiePrefs, env) {
  if (isDemoMode(env)) return mergePrefs(emptyPrefs(), cookiePrefs, env);
  const { prefs } = await readServerPrefs(env);
  return mergePrefs(prefs, cookiePrefs, env);
}

export async function buildCatalog({ env, customAgents, vmCharacters, includeSecrets = false, includeDirectory = false, groupTitle = "" }) {
  const sourceEnv = env || process.env;
  if (isDemoMode(sourceEnv)) {
    const demoSet = new Set(DEMO_AGENT_IDS);
    const demoRaw = BUILTIN_CAST.filter((c) => demoSet.has(c.id));
    const room = groupTitle || getGroupName(sourceEnv) || "Dev Guild Demo";
    return demoRaw.map((c) => {
      const st = statusFor(c, sourceEnv);
      return {
        ...c,
        online: st.online,
        statusLabel: st.online ? "active" : st.statusLabel,
        note: st.note,
        needsKey: false,
        envVar: "",
        hasKey: false,
        system: `${systemPromptFor(c, demoRaw, room)}
Keep replies brief (1-3 short sentences) in this public demo.`,
      };
    });
  }
  const state = await readState(sourceEnv);
  const vmById = new Map((vmCharacters || []).map((c) => [c.id, c]));
  const combinedCustom = mergeAgentLists(state.customAgents, customAgents);
  const dirEntries = includeDirectory ? directoryCharacters(sourceEnv) : [];
  const allRaw = [...BUILTIN_CAST, ...combinedCustom, ...(state.invitedParticipants || []), ...dirEntries];
  const basePeers = allRaw.slice(0, 16);
  const room = groupTitle || getGroupName(sourceEnv);
  return allRaw.map((c) => {
    if (c.kind === "directory") {
      return {
        ...c,
        system: systemPromptFor(c, basePeers, room),
      };
    }
    const vm = vmById.get(c.id);
    const st = statusFor(c, sourceEnv);
    const online = vm && vm.online ? true : st.online;
    const note = vm && vm.online && !st.online ? vm.note : st.note;
    const statusLabel = online ? "active" : st.statusLabel;
    const entry = {
      ...c,
      online,
      statusLabel,
      note,
      needsKey: !online && Boolean(st.needsKey),
      envVar: st.envVar || c.requiredEnv || "",
      hasKey: Boolean(c.apiKey || c.hasKey),
      system: systemPromptFor(c, basePeers, room),
    };
    if (!includeSecrets) delete entry.apiKey;
    return entry;
  });
}

function membersFrom(prefs, catalog, vmThreads) {
  const removed = new Set(prefs.removed || []);
  const knownIds = new Set((catalog || []).map((c) => c.id));
  const members = ["owner"];

  const vmGuild = (vmThreads || []).find((t) => t.id === "guild");
  if (!prefs.setupDone && prefs.added.length === 0 && vmGuild && Array.isArray(vmGuild.members)) {
    for (const id of vmGuild.members) {
      if (id === "owner" || removed.has(id) || !knownIds.has(id) || members.includes(id)) continue;
      members.push(id);
    }
  }

  for (const id of prefs.added) {
    if (id === "owner" || removed.has(id) || !knownIds.has(id) || members.includes(id)) continue;
    members.push(id);
  }
  return members;
}

export async function resolveGroupMembers({ catalog, cookiePrefs, vmThreads, env }) {
  const prefs = await mergeGroupPrefs(cookiePrefs, env);
  return membersFrom(prefs, catalog, vmThreads);
}

export async function updateGroupMember(id, action, cookiePrefs, env) {
  return writePrefs(cookiePrefs, env, (prev) => {
    const prefs = { ...prev, added: [...prev.added], removed: [...prev.removed] };
    if (action === "add") {
      prefs.removed = prefs.removed.filter((item) => item !== id);
      if (!prefs.added.includes(id)) prefs.added.push(id);
    } else if (action === "remove") {
      prefs.added = prefs.added.filter((item) => item !== id);
      if (!prefs.removed.includes(id)) prefs.removed.push(id);
    }
    prefs.version = nextVersion(prefs.version);
    return prefs;
  });
}

export async function configureGroup({ title, ownerName, members, setupDone, replaceMembers = false }, cookiePrefs, catalog, env) {
  const knownIds = new Set((catalog || []).map((c) => c.id));
  return writePrefs(cookiePrefs, env, (prev) => {
    const nextTitle = title !== undefined
      ? (String(title || "").trim().slice(0, 60) || getGroupName(env))
      : prev.title;
    const nextOwner = ownerName !== undefined
      ? (String(ownerName || "").trim().slice(0, 40) || getOwnerName(env))
      : prev.ownerName;

    let added = [...prev.added];
    let removed = [...prev.removed];
    if (Array.isArray(members)) {
      const clean = [];
      const seen = new Set();
      for (const raw of members) {
        const id = String(raw || "").trim();
        if (!id || id === "owner" || !knownIds.has(id) || seen.has(id)) continue;
        seen.add(id);
        clean.push(id);
      }
      if (replaceMembers) {
        added = clean;
        removed = [];
      } else {
        for (const id of clean) {
          removed = removed.filter((item) => item !== id);
          if (!added.includes(id)) added.push(id);
        }
      }
    }

    return {
      version: nextVersion(prev.version),
      title: nextTitle,
      ownerName: nextOwner,
      setupDone: setupDone !== undefined ? Boolean(setupDone) : prev.setupDone,
      added,
      removed,
    };
  });
}

async function lastMessagesByThread(state, env) {
  if (usesPostgres(env)) return roomStore().lastMessages(ROOM_ID);
  const out = {};
  for (const [threadId, msgs] of Object.entries(state.messages || {})) {
    const last = [...(msgs || [])].reverse().find((m) => m.kind !== "tool" && m.kind !== "card");
    if (last) out[threadId] = { text: last.text, time: last.time };
  }
  return out;
}

export async function listThreads({ catalog, cookiePrefs, vmThreads, env }) {
  const sourceEnv = env || process.env;
  if (isDemoMode(sourceEnv)) {
    const demoMembers = ["owner", ...(catalog || []).map((c) => c.id)];
    const title = String((sourceEnv.GUILD_GROUP_NAME || "")).trim().slice(0, 60) || "Dev Guild Demo";
    return [{
      id: "guild",
      title,
      ownerName: "You",
      setupDone: true,
      members: demoMembers,
      group: true,
      last: "Public live demo — say hi to the guild!",
      last_at: "",
    }];
  }
  const state = usesPostgres(sourceEnv) ? await readState(sourceEnv) : loadState();
  const prefs = mergePrefs(state.groupPrefs, cookiePrefs, env);
  const members = membersFrom(prefs, catalog, vmThreads);
  const lastByThread = await lastMessagesByThread(state, sourceEnv);
  const lastGuild = lastByThread.guild;
  const count = Math.max(members.length - 1, 0);
  const defaultPreview = count === 0
    ? "Empty group — tap + Add / Invite to pick members"
    : `${count} member${count === 1 ? "" : "s"} in group`;

  const guildThread = {
    id: "guild",
    title: prefs.title,
    ownerName: prefs.ownerName,
    setupDone: prefs.setupDone,
    members,
    group: true,
    last: (lastGuild && lastGuild.text) || defaultPreview,
    last_at: (lastGuild && lastGuild.time) || "",
  };

  const dmById = new Map();
  for (const t of vmThreads || []) {
    if (t.id !== "guild") dmById.set(t.id, t);
  }
  for (const t of state.threads || []) {
    if (t.id !== "guild") {
      const last = lastByThread[t.id];
      const { createdAt, ...thread } = t;
      dmById.set(t.id, {
        ...thread,
        last: (last && last.text) || t.last || "",
        last_at: (last && last.time) || t.last_at || "",
      });
    }
  }
  const dms = Array.from(dmById.values()).sort((a, b) => a.title.localeCompare(b.title));
  return [guildThread, ...dms];
}

export async function openDMThread(characterId, catalog, env) {
  const c = catalog.find((item) => item.id === characterId);
  if (!c) {
    const err = new Error(`unknown character "${characterId}"`);
    err.status = 400;
    throw err;
  }
  const id = `dm-${characterId}`;
  const thread = { id, title: c.name, members: ["owner", characterId], group: false };
  if (isDemoMode(env)) {
    return thread;
  }
  if (usesPostgres(env)) {
    const existing = (await roomStore().listThreads(ROOM_ID)).find((t) => t.id === id);
    if (existing) {
      const { createdAt, ...rest } = existing;
      return rest;
    }
    await roomStore().upsertThread({ roomId: ROOM_ID, thread });
    return thread;
  }
  const state = loadState();
  const existing = (state.threads || []).find((t) => t.id === id);
  if (existing) return existing;
  state.threads = [...(state.threads || []), thread];
  if (!state.messages[id]) state.messages[id] = [];
  saveState(state);
  return thread;
}

export async function appendLine({ threadId, from, text, kind = "message", reasoning = "", tool = "", task = "", assignee = "", card_status = "", files = [], catalog = [], senderName = "", secret = null, env }) {
  // Defense in depth: no raw credential is ever written to a transcript, even
  // one an agent echoes back. Routes redact before this point too.
  text = redactSecrets(String(text || "")).text;
  reasoning = reasoning ? redactSecrets(String(reasoning)).text : reasoning;
  task = task ? redactSecrets(String(task)).text : task;
  const demo = isDemoMode(env);
  const durable = !demo && usesPostgres(env);
  const state = demo || durable ? null : loadState();
  let seq = 0;
  if (demo) {
    globalThis.__devGuildDemoSeq = (globalThis.__devGuildDemoSeq || 0) + 1;
    seq = globalThis.__devGuildDemoSeq;
  } else if (!durable) {
    if (!state.messages[threadId]) state.messages[threadId] = [];
    state.seq = (state.seq || 0) + 1;
    seq = state.seq;
  }
  const person = catalog.find((c) => c.id === from);
  const isOwner = from === "owner";
  const bubbleKind = kind === "system" || kind === "tool" || kind === "card" ? kind : (person ? person.kind : "agent");
  const msg = {
    seq,
    id: `m_${seq}_${Math.random().toString(36).slice(2, 7)}`,
    thread: threadId,
    from,
    name: isOwner ? (senderName || (demo ? "You" : getOwnerName(env))) : (person ? person.name : (senderName || from)),
    kind: bubbleKind,
    avatar: person ? person.avatar : (isOwner ? "" : "claude.png"),
    color: person ? person.color : (isOwner ? "#0A84FF" : "#8E8E93"),
    text: String(text || "").trim(),
    time: new Date().toISOString(),
    status: isOwner ? "Delivered" : "",
    mine: isOwner,
    reasoning: reasoning || undefined,
    tool: tool || undefined,
    task: task || undefined,
    assignee: assignee || undefined,
    card_status: card_status || undefined,
    files: files && files.length ? files : undefined,
    secret: secret || undefined,
  };
  if (durable) {
    const { seq: _seq, id: _id, thread: _thread, from: _from, kind: _kind, ...body } = msg;
    const saved = await roomStore().appendMessage({
      roomId: ROOM_ID,
      threadId,
      from: String(from || "unknown"),
      kind: bubbleKind || "agent",
      body: JSON.parse(JSON.stringify(body)),
    });
    msg.seq = saved.seq;
    msg.id = saved.id;
    msg.time = saved.createdAt || msg.time;
    return msg;
  }
  if (!demo) {
    state.messages[threadId] = [...state.messages[threadId], msg];
    saveState(state);
  }
  return msg;
}

// addReaction toggles one emoji for one actor. Reaction state is keyed by
// thread and message id, so it survives a reload with the transcript. In demo
// mode nothing is persisted; the client keeps the reaction for the session.
export async function addReaction({ threadId, messageId, emoji, from = "owner", env, allowMissing = false }) {
  const clean = String(emoji || "");
  if (!isReaction(clean)) {
    const err = new Error("unsupported reaction");
    err.status = 400;
    throw err;
  }
  if (isDemoMode(env)) return { persisted: false, messageId, reactions: [] };
  if (usesPostgres(env)) {
    const rs = roomStore();
    const exists = await rs.hasMessage({ roomId: ROOM_ID, threadId, messageId });
    if (!exists) {
      // VM-bridge messages live on the VM; the durable store cannot key a
      // reaction to a message it does not hold, so the client keeps it.
      if (allowMissing) return { persisted: false, messageId, reactions: [] };
      const err = new Error("message not found");
      err.status = 404;
      throw err;
    }
    const before = await rs.listReactions({ roomId: ROOM_ID, threadId, messageIds: [messageId], viewer: from });
    const mine = (before[messageId] || []).some((r) => r.emoji === clean && r.mine);
    const args = { roomId: ROOM_ID, threadId, messageId, emoji: clean, actorId: from };
    if (mine) await rs.removeReaction(args);
    else await rs.addReaction(args);
    const after = await rs.listReactions({ roomId: ROOM_ID, threadId, messageIds: [messageId], viewer: from });
    return { persisted: true, messageId, reactions: after[messageId] || [] };
  }
  const state = loadState();
  if (!allowMissing && !(state.messages[threadId] || []).some((m) => m.id === messageId)) {
    const err = new Error("message not found");
    err.status = 404;
    throw err;
  }
  if (!state.reactions) state.reactions = {};
  const key = `${threadId}:${messageId}`;
  const room = { ...(state.reactions[key] || {}) };
  const actors = new Set(room[clean] || []);
  if (actors.has(from)) actors.delete(from);
  else actors.add(from);
  if (actors.size) room[clean] = Array.from(actors);
  else delete room[clean];
  if (Object.keys(room).length) state.reactions[key] = room;
  else delete state.reactions[key];
  saveState(state);
  return { persisted: true, messageId, reactions: summarizeReactions(room, from) };
}

// invitedMessages reads the group with reaction state personalized for the
// authenticated participant rather than the owner of the browser session.
export async function invitedMessages({ auth, after = 0, env, threadId = "guild" } = {}) {
  return getMessages(threadId, after, env, viewerIdFromAuth(auth));
}

function markReadStatus(all) {
  let lastOwner = -1;
  all.forEach((m, idx) => {
    if (m.mine) lastOwner = idx;
  });
  if (lastOwner >= 0) {
    const read = all.slice(lastOwner + 1).some((m) => !m.mine && m.kind !== "system");
    all[lastOwner].status = read ? "Read" : "Delivered";
  }
  return all;
}

export async function getMessages(threadId, after = 0, env, viewer = "owner") {
  if (isDemoMode(env)) return [];
  if (usesPostgres(env)) {
    const rs = roomStore();
    const rows = await rs.listMessages({ roomId: ROOM_ID, threadId, after: Number(after || 0), limit: 1000 });
    const all = markReadStatus(rows.map((r) => ({
      ...(r.body || {}),
      seq: r.seq,
      id: r.id,
      thread: r.threadId,
      from: r.from,
      kind: r.kind,
      time: (r.body && r.body.time) || r.createdAt,
    })));
    const reactions = await rs.listReactions({ roomId: ROOM_ID, threadId, messageIds: all.map((m) => m.id), viewer });
    return all.map((m) => (reactions[m.id] ? { ...m, reactions: reactions[m.id] } : m));
  }
  const state = loadState();
  const all = markReadStatus((state.messages[threadId] || []).map((m) => ({ ...m })));
  return all
    .filter((m) => (m.seq || 0) > Number(after || 0))
    .map((m) => {
      const room = (state.reactions || {})[`${threadId}:${m.id}`];
      return room ? { ...m, reactions: summarizeReactions(room, viewer) } : m;
    });
}

export function slugifyAgentId(name, existingIds) {
  const base = String(name || "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 24) || "agent";
  if (!existingIds.has(base)) return base;
  let i = 2;
  while (existingIds.has(`${base}-${i}`)) i += 1;
  return `${base}-${i}`;
}
