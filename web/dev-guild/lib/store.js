import { existsSync, readFileSync, writeFileSync } from "fs";
import { BUILTIN_CAST, statusFor, systemPromptFor } from "./cast.js";

const STATE_PATH = "/tmp/dev-guild-state.json";

function initialState() {
  return {
    seq: 0,
    messages: {
      guild: [
        {
          seq: 1,
          id: "m_welcome",
          thread: "guild",
          from: "tay",
          name: "Tay",
          kind: "agent",
          avatar: "tay.png",
          color: "#F5C16C",
          text: "On it, Deonte. The Dev Guild is live — all agents, presets, and tool mascots are in this room. @mention anyone or talk to the whole room.",
          time: new Date().toISOString(),
          mine: false,
        },
      ],
    },
    threads: [],
    customAgents: [],
    invitedParticipants: [],
    groupPrefs: { added: [], removed: [] },
  };
}

function loadState() {
  if (globalThis.__devGuildState) return globalThis.__devGuildState;
  let state = initialState();
  try {
    if (existsSync(STATE_PATH)) {
      const parsed = JSON.parse(readFileSync(STATE_PATH, "utf8"));
      if (parsed && typeof parsed === "object" && parsed.messages) {
        state = { ...initialState(), ...parsed };
      }
    }
  } catch {
    state = initialState();
  }
  if (!state.seq) state.seq = 1;
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

export function mergeCustomAgents(cookieAgents) {
  const state = loadState();
  const byId = new Map();
  for (const a of state.customAgents || []) byId.set(a.id, a);
  for (const a of cookieAgents || []) byId.set(a.id, a);
  return Array.from(byId.values());
}

export function saveCustomAgent(agent) {
  const state = loadState();
  const list = state.customAgents || [];
  const idx = list.findIndex((item) => item.id === agent.id);
  if (idx >= 0) list[idx] = agent;
  else list.push(agent);
  state.customAgents = list;
  if (!state.groupPrefs) state.groupPrefs = { added: [], removed: [] };
  state.groupPrefs.removed = (state.groupPrefs.removed || []).filter((id) => id !== agent.id);
  if (!(state.groupPrefs.added || []).includes(agent.id)) {
    state.groupPrefs.added = [...(state.groupPrefs.added || []), agent.id];
  }
  saveState(state);
  return agent;
}

export function registerInvitedParticipant(participant) {
  const state = loadState();
  const list = state.invitedParticipants || [];
  const idx = list.findIndex((item) => item.id === participant.id);
  if (idx >= 0) list[idx] = participant;
  else list.push(participant);
  state.invitedParticipants = list;
  saveState(state);
  return participant;
}

export function buildCatalog({ env, customAgents, vmCharacters, includeSecrets = false }) {
  const state = loadState();
  const vmById = new Map((vmCharacters || []).map((c) => [c.id, c]));
  const combinedCustom = mergeCustomAgents(customAgents);
  const allRaw = [...BUILTIN_CAST, ...combinedCustom, ...(state.invitedParticipants || [])];
  const basePeers = allRaw.slice(0, 20);
  return allRaw.map((c) => {
    const vm = vmById.get(c.id);
    const st = statusFor(c, env);
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
      system: systemPromptFor(c, basePeers),
    };
    if (!includeSecrets) delete entry.apiKey;
    return entry;
  });
}

export function resolveGroupMembers({ catalog, cookiePrefs }) {
  const state = loadState();
  const removed = new Set([
    ...((state.groupPrefs && state.groupPrefs.removed) || []),
    ...((cookiePrefs && cookiePrefs.removed) || []),
  ]);
  const added = new Set([
    ...((state.groupPrefs && state.groupPrefs.added) || []),
    ...((cookiePrefs && cookiePrefs.added) || []),
  ]);
  const members = ["owner"];
  for (const c of catalog) {
    if (removed.has(c.id) && !added.has(c.id)) continue;
    members.push(c.id);
  }
  for (const id of added) {
    if (!members.includes(id)) members.push(id);
  }
  return members;
}

export function updateGroupMember(id, action, cookiePrefs) {
  const state = loadState();
  const prefs = {
    added: [...new Set([...((state.groupPrefs && state.groupPrefs.added) || []), ...((cookiePrefs && cookiePrefs.added) || [])])],
    removed: [...new Set([...((state.groupPrefs && state.groupPrefs.removed) || []), ...((cookiePrefs && cookiePrefs.removed) || [])])],
  };
  if (action === "add") {
    prefs.removed = prefs.removed.filter((item) => item !== id);
    if (!prefs.added.includes(id)) prefs.added.push(id);
  } else if (action === "remove") {
    prefs.added = prefs.added.filter((item) => item !== id);
    if (!prefs.removed.includes(id)) prefs.removed.push(id);
  }
  state.groupPrefs = prefs;
  saveState(state);
  return prefs;
}

export function listThreads({ catalog, cookiePrefs, vmThreads }) {
  const state = loadState();
  const members = resolveGroupMembers({ catalog, cookiePrefs });
  const guildMsgs = state.messages.guild || [];
  const lastGuild = [...guildMsgs].reverse().find((m) => m.kind !== "tool" && m.kind !== "card");
  const guildThread = {
    id: "guild",
    title: "The Dev Guild",
    members,
    group: true,
    last: (lastGuild && lastGuild.text) || "All agents & presets ready",
    last_at: (lastGuild && lastGuild.time) || new Date().toISOString(),
  };

  const dmById = new Map();
  for (const t of vmThreads || []) {
    if (t.id !== "guild") dmById.set(t.id, t);
  }
  for (const t of state.threads || []) {
    if (t.id !== "guild") {
      const msgs = state.messages[t.id] || [];
      const last = [...msgs].reverse().find((m) => m.kind !== "tool" && m.kind !== "card");
      dmById.set(t.id, {
        ...t,
        last: (last && last.text) || t.last || "",
        last_at: (last && last.time) || t.last_at || "",
      });
    }
  }
  const dms = Array.from(dmById.values()).sort((a, b) => a.title.localeCompare(b.title));
  return [guildThread, ...dms];
}

export function openDMThread(characterId, catalog) {
  const state = loadState();
  const c = catalog.find((item) => item.id === characterId);
  if (!c) {
    const err = new Error(`unknown character "${characterId}"`);
    err.status = 400;
    throw err;
  }
  const id = `dm-${characterId}`;
  const existing = (state.threads || []).find((t) => t.id === id);
  if (existing) return existing;
  const thread = { id, title: c.name, members: ["owner", characterId], group: false };
  state.threads = [...(state.threads || []), thread];
  if (!state.messages[id]) state.messages[id] = [];
  saveState(state);
  return thread;
}

export function appendLine({ threadId, from, text, kind = "message", reasoning = "", tool = "", task = "", assignee = "", card_status = "", files = [], catalog = [], senderName = "" }) {
  const state = loadState();
  if (!state.messages[threadId]) state.messages[threadId] = [];
  state.seq = (state.seq || 1) + 1;
  const person = catalog.find((c) => c.id === from);
  const isOwner = from === "owner";
  const bubbleKind = kind === "system" || kind === "tool" || kind === "card" ? kind : (person ? person.kind : "agent");
  const msg = {
    seq: state.seq,
    id: `m_${state.seq}_${Math.random().toString(36).slice(2, 7)}`,
    thread: threadId,
    from,
    name: isOwner ? (senderName || "Deonte") : (person ? person.name : (senderName || from)),
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
  };
  state.messages[threadId] = [...state.messages[threadId], msg];
  saveState(state);
  return msg;
}

export function getMessages(threadId, after = 0) {
  const state = loadState();
  const all = (state.messages[threadId] || []).map((m) => ({ ...m }));
  let lastOwner = -1;
  all.forEach((m, idx) => {
    if (m.mine) lastOwner = idx;
  });
  if (lastOwner >= 0) {
    const read = all.slice(lastOwner + 1).some((m) => !m.mine && m.kind !== "system");
    all[lastOwner].status = read ? "Read" : "Delivered";
  }
  return all.filter((m) => (m.seq || 0) > Number(after || 0));
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
