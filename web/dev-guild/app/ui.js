"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Avatar, DemoSheet, PhoneFrame, ProfileSheet, timeLabel } from "./components";
import { REACTIONS, roomVibe, starterPrompts } from "../lib/social.js";
import { redactSecrets } from "../lib/secrets/detector.js";
import { SecretCard, SecretSheet } from "./secrets";

const STARTER_IDS = ["claude", "grok", "gemini", "codex", "tay"];
const DEMO_CAST_IDS = ["claude", "grok", "chatgpt", "gemini", "tay"];
const DEMO_TOPICS = [
  "keeping multi-agent PRs conflict-free with Collaborate-Claim paths",
  "why agentterm writes a SHA-256 hash-chained log for every guild message",
  "routing Dev Guild replies through free Gemini models with zero credit card",
  "making an open-source multi-agent group chat feel like native iOS Messages",
  "keeping diffs narrow so go-verify and scripts/check.sh stay green",
];

async function api(path, options) {
  const res = await fetch(path, {
    ...options,
    headers: { "Content-Type": "application/json", ...(options && options.headers) },
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const error = new Error(data.error || "The guild didn't answer.");
    error.status = res.status;
    error.data = data;
    throw error;
  }
  return data;
}

function mergeMessages(prev, incoming) {
  const byId = new Map();
  for (const m of prev || []) {
    if (m && m.id) byId.set(m.id, m);
  }
  for (const m of incoming || []) {
    if (m && m.id) byId.set(m.id, m);
  }
  return Array.from(byId.values()).sort((a, b) => {
    const sa = Number(a.seq || 0);
    const sb = Number(b.seq || 0);
    if (sa !== sb) return sa - sb;
    return String(a.time || "").localeCompare(String(b.time || ""));
  });
}

function readCachedMessages(thread, isDemo) {
  if (isDemo) return [];
  try {
    const raw = window.localStorage.getItem(`devguild_msgs_v1_${thread}`);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function writeCachedMessages(thread, list, isDemo) {
  if (isDemo) return;
  try {
    window.localStorage.setItem(`devguild_msgs_v1_${thread}`, JSON.stringify((list || []).slice(-120)));
  } catch {
    // ignore storage quota errors
  }
}

export default function App() {
  const [ready, setReady] = useState(false);
  const [authed, setAuthed] = useState(false);
  const [demoMode, setDemoMode] = useState(false);
  const [liveDemo, setLiveDemo] = useState(false);
  const [rateInfo, setRateInfo] = useState(null);
  const [autoPlay, setAutoPlay] = useState(true);
  const [role, setRole] = useState("owner");
  const [ownerName, setOwnerName] = useState("Owner");
  const [inviteParam, setInviteParam] = useState("");
  const [pass, setPass] = useState("");
  const [guestName, setGuestName] = useState("");
  const [guestKind, setGuestKind] = useState("person");
  const [guestPersona, setGuestPersona] = useState("");
  const [loginError, setLoginError] = useState("");

  const [characters, setCharacters] = useState([]);
  const [threads, setThreads] = useState([]);
  const [active, setActive] = useState("guild");
  const [sidebarTab, setSidebarTab] = useState("chats");
  const [contactFilter, setContactFilter] = useState("");
  const [sectionFilter, setSectionFilter] = useState("All");
  const [visibleContactsCount, setVisibleContactsCount] = useState(60);
  const [mobileList, setMobileList] = useState(false);

  const [messages, setMessages] = useState([]);
  const [text, setText] = useState("");
  const [pending, setPending] = useState(false);
  const [live, setLive] = useState(null);
  const [files, setFiles] = useState([]);
  const [showSearch, setShowSearch] = useState(false);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState(null);
  const [profile, setProfile] = useState(null);
  const [composing, setComposing] = useState(false);
  const [error, setError] = useState("");
  const [secretDraft, setSecretDraft] = useState(null);
  const [reactionFor, setReactionFor] = useState(null);

  const [groupSheet, setGroupSheet] = useState(null);
  const [memberFilter, setMemberFilter] = useState("");
  const [inviteInfo, setInviteInfo] = useState(null);
  const [copied, setCopied] = useState("");

  const [setupTitle, setSetupTitle] = useState("Dev Guild");
  const [setupOwner, setSetupOwner] = useState("");
  const [setupMembers, setSetupMembers] = useState([]);
  const [setupFilter, setSetupFilter] = useState("");
  const [setupBusy, setSetupBusy] = useState(false);
  const [settingsMsg, setSettingsMsg] = useState("");

  const [customName, setCustomName] = useState("");
  const [customPersona, setCustomPersona] = useState("");
  const [customRole, setCustomRole] = useState("");
  const [customCatch, setCustomCatch] = useState("");
  const [customEndpoint, setCustomEndpoint] = useState("gemini");
  const [customModel, setCustomModel] = useState("gemini-3.5-flash-lite");
  const [customBaseUrl, setCustomBaseUrl] = useState("");
  const [customApiKey, setCustomApiKey] = useState("");
  const [customBusy, setCustomBusy] = useState(false);
  const [customMsg, setCustomMsg] = useState("");

  const scroller = useRef(null);
  const sectionRefs = useRef({});
  const after = useRef(0);
  const autoTurnIdx = useRef(0);
  const autoTopicIdx = useRef(0);
  const pendingRef = useRef(false);
  const messagesRef = useRef([]);
  const pressTimer = useRef(null);

  useEffect(() => { pendingRef.current = pending; }, [pending]);
  useEffect(() => { messagesRef.current = messages; }, [messages]);

  const guildThread = useMemo(() => threads.find((t) => t.id === "guild") || threads[0], [threads]);
  const effectiveOwnerName = (guildThread && guildThread.ownerName) || ownerName || "Owner";

  const byId = useMemo(() => {
    const map = { owner: { id: "owner", name: effectiveOwnerName, kind: "owner", color: "#0A84FF" } };
    for (const c of characters) map[c.id] = c;
    return map;
  }, [characters, effectiveOwnerName]);

  const groupMemberSet = useMemo(() => new Set((guildThread && guildThread.members) || []), [guildThread]);

  const loadRoster = useCallback(async () => {
    const [cast, convos] = await Promise.all([
      api("/api/guild/v1/characters"),
      api("/api/guild/v1/threads"),
    ]);
    const list = cast.characters || [];
    const threadList = convos.threads || [];
    setCharacters(list);
    setThreads(threadList);
    const g = threadList.find((t) => t.id === "guild");
    if (g) {
      if (g.title) setSetupTitle(g.title);
      if (g.ownerName) {
        setOwnerName(g.ownerName);
        setSetupOwner((prev) => prev || g.ownerName);
      }
    }
  }, []);

  const loadMessages = useCallback(async (thread, reset, isDemo) => {
    if (isDemo) return [];
    const start = reset ? 0 : after.current;
    const data = await api(`/api/guild/v1/threads/${thread}/messages?after=${start}`);
    const incoming = data.messages || [];
    setMessages((prev) => {
      const base = reset ? mergeMessages(readCachedMessages(thread, isDemo), prev) : prev;
      const next = mergeMessages(base, incoming);
      writeCachedMessages(thread, next, isDemo);
      after.current = next.reduce((m, row) => Math.max(m, row.seq || 0), 0);
      return next;
    });
    if (incoming.some((row) => row.from !== "owner")) setPending(false);
    return incoming;
  }, []);

  useEffect(() => {
    try {
      const inv = new URLSearchParams(window.location.search).get("invite") || "";
      if (inv) setInviteParam(inv);
    } catch {
      // ignore
    }
    api("/api/session")
      .then((data) => {
        setAuthed(!!data.ok);
        setDemoMode(Boolean(data.demo));
        setLiveDemo(Boolean(data.liveDemo));
        if (data.rateLimit) setRateInfo(data.rateLimit);
        if (data.role) setRole(data.role);
        if (data.participant && data.participant.name) {
          setOwnerName(data.participant.name);
          setSetupOwner(data.participant.name);
        }
        if (data.group && data.group.title) setSetupTitle(data.group.title);
      })
      .catch(() => setAuthed(false))
      .finally(() => setReady(true));
    if ("serviceWorker" in navigator) navigator.serviceWorker.register("/sw.js").catch(() => {});
  }, []);

  useEffect(() => {
    if (!authed) return;
    loadRoster().catch((err) => setError(err.message));
  }, [authed, loadRoster]);

  useEffect(() => {
    if (!authed || !active) return;
    after.current = 0;
    setPending(false);
    if (demoMode) {
      setMessages([]);
      return;
    }
    const cached = readCachedMessages(active, false);
    setMessages(cached.length ? cached : []);
    loadMessages(active, true, false).catch((err) => setError(err.message));
    const timer = setInterval(() => loadMessages(active, false, false).catch(() => {}), 2500);
    return () => clearInterval(timer);
  }, [authed, active, demoMode, loadMessages]);

  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages, pending, live, active]);

  const applyEvent = useCallback((block) => {
    const name = (block.match(/^event: (.+)$/m) || [])[1];
    const raw = (block.match(/^data: (.+)$/m) || [])[1];
    if (!name || !raw) return;
    const data = JSON.parse(raw);
    if (name === "typing") setLive({ from: data.from, text: "", reasoning: "" });
    if (name === "delta") {
      setLive((prev) => ({ from: data.from, text: ((prev && prev.text) || "") + data.text, reasoning: prev && prev.reasoning }));
    }
    if (name === "reasoning") setLive((prev) => ({ ...(prev || { from: data.from, text: "" }), reasoning: data.summary }));
    if ((name === "tool" || name === "card" || name === "message") && data.message) {
      if (name === "message") setLive((prev) => (prev && prev.from === data.message.from ? null : prev));
      setMessages((prev) => {
        const next = mergeMessages(prev, [data.message]);
        writeCachedMessages(active, next, demoMode);
        return next;
      });
    }
    if (name === "done") {
      setLive(null);
      if (data.rateLimit) setRateInfo(data.rateLimit);
      if (Array.isArray(data.messages) && data.messages.length) {
        setMessages((prev) => {
          const next = mergeMessages(prev, data.messages);
          writeCachedMessages(active, next, demoMode);
          return next;
        });
      }
    }
    if (name === "fallback") setPending(true);
  }, [active, demoMode]);

  const triggerAutonomousTurn = useCallback(async () => {
    if (!demoMode || !autoPlay || pendingRef.current) return;
    if (typeof document !== "undefined" && document.visibilityState !== "visible") return;
    if (active !== "guild") return;

    const speakerId = DEMO_CAST_IDS[autoTurnIdx.current % DEMO_CAST_IDS.length];
    autoTurnIdx.current += 1;
    if (autoTurnIdx.current % DEMO_CAST_IDS.length === 0) {
      autoTopicIdx.current = (autoTopicIdx.current + 1) % DEMO_TOPICS.length;
    }
    const topic = DEMO_TOPICS[autoTopicIdx.current];
    const history = (messagesRef.current || [])
      .filter((m) => m && m.kind !== "system" && m.text)
      .slice(-4)
      .map((m) => ({ from: m.from, name: m.name, text: m.text }));

    setPending(true);
    setLive(null);
    try {
      const res = await fetch("/api/guild/stream", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ thread: "guild", autonomous: true, speakerId, topic, history }),
      });
      if (res.status === 429) { setAutoPlay(false); return; }
      if (!res.ok || !res.body) return;
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      while (true) {
        const step = await reader.read();
        if (step.done) break;
        buf += decoder.decode(step.value, { stream: true });
        const blocks = buf.split("\n\n");
        buf = blocks.pop() || "";
        for (const block of blocks) applyEvent(block);
      }
    } catch {
      // stay quiet on transient network error
    } finally {
      setLive(null);
      setPending(false);
    }
  }, [demoMode, autoPlay, active, applyEvent]);

  useEffect(() => {
    if (!authed || !demoMode || !autoPlay || active !== "guild") return;
    let timerId;
    let cancelled = false;
    const schedule = (delayMs) => {
      clearTimeout(timerId);
      timerId = setTimeout(async () => {
        if (cancelled) return;
        await triggerAutonomousTurn();
        if (!cancelled && autoPlay) schedule(22000 + Math.floor(Math.random() * 14000));
      }, delayMs);
    };
    schedule((messagesRef.current || []).length === 0 ? 1800 : 25000);
    const onVis = () => {
      if (document.visibilityState === "visible" && autoPlay) schedule(12000);
      else clearTimeout(timerId);
    };
    document.addEventListener("visibilitychange", onVis);
    return () => {
      cancelled = true;
      clearTimeout(timerId);
      document.removeEventListener("visibilitychange", onVis);
    };
  }, [authed, demoMode, autoPlay, active, triggerAutonomousTurn]);

  async function signIn(event) {
    event.preventDefault();
    setLoginError("");
    try {
      const res = await api("/api/login", { method: "POST", body: JSON.stringify({ passphrase: pass }) });
      setRole(res.role || "owner");
      setAuthed(true);
      setPass("");
      setMobileList(false);
    } catch (err) {
      setLoginError(err.message);
    }
  }

  async function joinWithInvite(event) {
    event.preventDefault();
    setLoginError("");
    try {
      const res = await api("/api/guild/invite/join", {
        method: "POST",
        body: JSON.stringify({
          invite: inviteParam,
          name: guestName || "Guest Agent",
          kind: guestKind,
          persona: guestPersona,
        }),
      });
      setRole("invite");
      setAuthed(true);
      if (res.messages) setMessages((prev) => mergeMessages(prev, res.messages));
      setMobileList(false);
    } catch (err) {
      setLoginError(err.message);
    }
  }

  async function filePayload(list) {
    const out = [];
    for (const file of list) {
      const data = await new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result).split(",")[1] || "");
        reader.onerror = () => reject(new Error("could not read attachment"));
        reader.readAsDataURL(file);
      });
      out.push({ name: file.name, mediaType: file.type, data });
    }
    return out;
  }

  async function send(overrideText) {
    const body = String(overrideText !== undefined ? overrideText : text).trim();
    if ((!body && files.length === 0) || pending) return;
    // Client-side interception: a message that looks like it holds a
    // credential is never sent. The owner gets the vault sheet instead.
    const scan = redactSecrets(body, { keepValues: true });
    if (scan.redacted) {
      setText(scan.text);
      if (!demoMode && role === "owner") {
        const hit = scan.findings[0];
        setSecretDraft({ value: hit.value, provider: hit.provider, name: hit.hint || "", intercepted: true });
      } else {
        setError("That looked like a secret, so it wasn't sent. Only the owner can store secrets.");
      }
      scan.findings.forEach((f) => { f.value = ""; });
      return;
    }
    if (overrideText === undefined) setText("");
    const chosen = demoMode ? [] : files;
    setFiles([]);
    setPending(true);
    setLive(null);
    setError("");
    try {
      const attachments = await filePayload(chosen);
      const res = await fetch("/api/guild/stream", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ thread: active, text: body, attachments }),
      });
      if (!res.ok || !res.body) {
        const err = await res.json().catch(() => ({}));
        throw new Error(err.error || "The guild didn't answer.");
      }
      const hdrRemaining = res.headers.get("X-RateLimit-Remaining");
      const hdrLimit = res.headers.get("X-RateLimit-Limit");
      if (hdrRemaining !== null && hdrLimit !== null) {
        setRateInfo({ limit: Number(hdrLimit), remaining: Number(hdrRemaining) });
      }
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      while (true) {
        const step = await reader.read();
        if (step.done) break;
        buf += decoder.decode(step.value, { stream: true });
        const blocks = buf.split("\n\n");
        buf = blocks.pop() || "";
        for (const block of blocks) applyEvent(block);
      }
      setLive(null);
      if (!demoMode) {
        await loadMessages(active, false, false);
        await loadRoster();
      }
    } catch (err) {
      setError(err.message);
    } finally {
      setPending(false);
    }
  }

  async function searchHistory(event) {
    event.preventDefault();
    const q = query.trim();
    if (!q) { setHits(null); return; }
    if (demoMode) {
      const lower = q.toLowerCase();
      setHits(messages.filter((m) => `${m.name || ""} ${m.text || ""}`.toLowerCase().includes(lower)).slice(-5));
      return;
    }
    const data = await api(`/api/guild/search?thread=${encodeURIComponent(active)}&q=${encodeURIComponent(q)}`);
    setHits(data.messages || []);
  }

  async function openDM(id) {
    const thread = await api("/api/guild/v1/threads", { method: "POST", body: JSON.stringify({ with: id }) });
    if (demoMode) {
      setThreads((prev) => (prev.some((t) => t.id === thread.id) ? prev : [...prev, thread]));
      setMessages([]);
    } else {
      await loadRoster();
    }
    setActive(thread.id);
    setComposing(false);
    setSidebarTab("chats");
    setMobileList(false);
    setProfile(null);
  }

  async function toggleGroupMember(id) {
    if (demoMode) return;
    await api("/api/guild/v1/group/members", {
      method: "POST",
      body: JSON.stringify({ id, action: groupMemberSet.has(id) ? "remove" : "add" }),
    });
    await loadRoster();
  }

  async function completeGroupSetup(event, startEmpty = false) {
    if (event) event.preventDefault();
    if (setupBusy || demoMode) return;
    setSetupBusy(true);
    setError("");
    try {
      await api("/api/guild/v1/group", {
        method: "POST",
        body: JSON.stringify({
          title: setupTitle.trim() || "Dev Guild",
          ownerName: setupOwner.trim() || "Owner",
          members: startEmpty ? [] : setupMembers,
          setupDone: true,
          replaceMembers: true,
        }),
      });
      await loadRoster();
      await loadMessages("guild", true, false);
      setActive("guild");
      setMobileList(false);
    } catch (err) {
      setError(err.message);
    } finally {
      setSetupBusy(false);
    }
  }

  async function saveGroupSettings(event) {
    event.preventDefault();
    if (setupBusy || demoMode) return;
    setSetupBusy(true);
    setSettingsMsg("");
    try {
      await api("/api/guild/v1/group", {
        method: "POST",
        body: JSON.stringify({ title: setupTitle.trim() || "Dev Guild", ownerName: setupOwner.trim() || "Owner", setupDone: true }),
      });
      await loadRoster();
      setSettingsMsg("Group settings saved.");
    } catch (err) {
      setSettingsMsg(err.message);
    } finally {
      setSetupBusy(false);
    }
  }

  async function openGroupSheet(tab) {
    setGroupSheet(tab);
    setCustomMsg("");
    setSettingsMsg("");
    if (tab === "invite" && role === "owner" && !demoMode && !inviteInfo) {
      try {
        setInviteInfo(await api("/api/guild/invite"));
      } catch (err) {
        setError(err.message);
      }
    }
  }

  async function submitCustomAgent(event) {
    event.preventDefault();
    if (!customName.trim() || customBusy || demoMode) return;
    setCustomBusy(true);
    setCustomMsg("");
    try {
      const res = await api("/api/guild/agents", {
        method: "POST",
        body: JSON.stringify({
          name: customName,
          persona: customPersona,
          role: customRole,
          catchphrase: customCatch,
          endpointType: customEndpoint,
          modelId: customModel,
          baseUrl: customBaseUrl,
          apiKey: customApiKey,
        }),
      });
      await loadRoster();
      setActive("guild");
      setMobileList(false);
      setCustomMsg(`Added ${res.agent.name} (@${res.agent.id}) to the group.`);
      setCustomName("");
      setCustomPersona("");
      setCustomRole("");
      setCustomCatch("");
      setCustomApiKey("");
    } catch (err) {
      setCustomMsg(err.message);
    } finally {
      setCustomBusy(false);
    }
  }

  function copyText(label, val) {
    if (!navigator.clipboard) return;
    navigator.clipboard.writeText(val).then(() => {
      setCopied(label);
      setTimeout(() => setCopied(""), 1800);
    }).catch(() => {});
  }

  function react(messageId, emoji) {
    if (!messageId || !REACTIONS.includes(emoji)) return;
    setMessages((prev) => prev.map((m) => {
      if (m.id !== messageId) return m;
      const current = Array.isArray(m.reactions) ? m.reactions : [];
      const hit = current.find((r) => r.emoji === emoji);
      let next;
      if (hit && hit.mine) {
        next = current.map((r) => (r.emoji === emoji ? { ...r, mine: false, count: r.count - 1 } : r)).filter((r) => r.count > 0);
      } else if (hit) {
        next = current.map((r) => (r.emoji === emoji ? { ...r, mine: true, count: r.count + 1 } : r));
      } else {
        next = [...current, { emoji, count: 1, mine: true }];
      }
      return { ...m, reactions: next };
    }));
    api(`/api/guild/v1/threads/${active}/messages/${messageId}/reactions`, {
      method: "POST",
      body: JSON.stringify({ emoji }),
    }).then((res) => {
      if (res.persisted === false) return;
      setMessages((prev) => prev.map((m) => (m.id === messageId ? { ...m, reactions: res.reactions || [] } : m)));
    }).catch((err) => {
      setError(err.message);
      loadMessages(active, false, demoMode).catch(() => {});
    });
  }

  function toggleSetupMember(id) {
    setSetupMembers((prev) => (prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]));
  }

  const thread = threads.find((t) => t.id === active) || threads[0];
  const mention = text.match(/(^|\s)@([a-z0-9-]*)$/);
  const suggestions = mention
    ? characters.filter((c) => c.id.includes(mention[2]) || c.name.toLowerCase().includes(mention[2])).slice(0, 6)
    : [];

  const sectionNames = useMemo(() => {
    const names = ["All", "Guild Agents", "Free Public AI Presets"];
    if (characters.some((c) => c.kind === "custom")) names.push("Custom & Invited");
    const seen = new Set(names);
    for (const c of characters) {
      if (c.section && !seen.has(c.section)) { seen.add(c.section); names.push(c.section); }
    }
    if (characters.some((c) => c.kind === "tool")) names.push("Tool Mascots");
    return names;
  }, [characters]);

  const filteredContacts = useMemo(() => {
    const q = contactFilter.trim().toLowerCase();
    return characters.filter((c) => {
      if (sectionFilter !== "All") {
        if (sectionFilter === "Guild Agents" && c.kind !== "agent") return false;
        if (sectionFilter === "Free Public AI Presets" && c.kind !== "preset") return false;
        if (sectionFilter === "Custom & Invited" && c.kind !== "custom") return false;
        if (sectionFilter === "Tool Mascots" && c.kind !== "tool") return false;
        if (
          !["Guild Agents", "Free Public AI Presets", "Custom & Invited", "Tool Mascots"].includes(sectionFilter) &&
          c.section !== sectionFilter
        ) return false;
      }
      if (!q) return true;
      return (
        c.name.toLowerCase().includes(q) ||
        c.id.toLowerCase().includes(q) ||
        (c.role || "").toLowerCase().includes(q) ||
        (c.section || "").toLowerCase().includes(q) ||
        (c.persona || "").toLowerCase().includes(q)
      );
    });
  }, [characters, contactFilter, sectionFilter]);

  useEffect(() => { setVisibleContactsCount(60); }, [contactFilter, sectionFilter]);

  const visibleContacts = useMemo(() => filteredContacts.slice(0, visibleContactsCount), [filteredContacts, visibleContactsCount]);

  const contactGroups = useMemo(() => {
    const groups = new Map();
    const labelFor = (c) => {
      if (c.kind === "agent") return "Guild Agents";
      if (c.kind === "preset") return "Free Public AI Presets";
      if (c.kind === "custom") return "Custom & Invited Agents";
      if (c.kind === "tool") return "Tool Mascots";
      return c.section || "Directory";
    };
    for (const c of visibleContacts) {
      const label = labelFor(c);
      if (!groups.has(label)) groups.set(label, []);
      groups.get(label).push(c);
    }
    return Array.from(groups.entries()).map(([label, items]) => ({ key: label, label, items }));
  }, [visibleContacts]);

  const railTokens = useMemo(() => {
    const seen = new Set();
    const out = [];
    for (const g of contactGroups) {
      const ch = g.label[0].toUpperCase();
      if (!seen.has(ch)) { seen.add(ch); out.push({ ch, key: g.key }); }
    }
    return out;
  }, [contactGroups]);

  const filteredMembers = useMemo(() => {
    const q = memberFilter.trim().toLowerCase();
    const base = q
      ? characters.filter((c) => c.name.toLowerCase().includes(q) || c.id.toLowerCase().includes(q) || (c.section || "").toLowerCase().includes(q))
      : characters;
    return base.slice(0, 60);
  }, [characters, memberFilter]);

  const filteredSetupMembers = useMemo(() => {
    const q = setupFilter.trim().toLowerCase();
    const base = q
      ? characters.filter((c) => c.name.toLowerCase().includes(q) || c.id.toLowerCase().includes(q) || (c.role || "").toLowerCase().includes(q))
      : characters;
    return base.slice(0, 60);
  }, [characters, setupFilter]);

  if (!ready) return <PhoneFrame><main className="login" /></PhoneFrame>;

  if (!authed) {
    if (inviteParam) {
      return (
        <PhoneFrame>
          <main className="login">
            <form onSubmit={joinWithInvite}>
              <img className="avatar-lg" alt="" src="/avatars/tay.webp" />
              <h1>Join Group Chat</h1>
              <p>You were invited to the group chat. Pick your name (or agent persona) to join.</p>
              <input autoFocus placeholder="Your name or agent name" value={guestName} onChange={(e) => setGuestName(e.target.value)} aria-label="Display name" />
              <div className="seg">
                <button type="button" className={guestKind === "person" ? "active" : ""} onClick={() => setGuestKind("person")}>Person</button>
                <button type="button" className={guestKind === "agent" ? "active" : ""} onClick={() => setGuestKind("agent")}>AI Agent</button>
              </div>
              {guestKind === "agent" ? (
                <input placeholder="Agent persona (optional)" value={guestPersona} onChange={(e) => setGuestPersona(e.target.value)} aria-label="Agent persona" />
              ) : null}
              {loginError ? <div className="err">{loginError}</div> : null}
              <button type="submit">Join Group Chat</button>
              <button type="button" className="ios-text-btn" onClick={() => setInviteParam("")}>Owner sign in instead</button>
            </form>
          </main>
        </PhoneFrame>
      );
    }
    return (
      <PhoneFrame>
        <main className="login">
          <form onSubmit={signIn}>
            <img className="avatar-lg" alt="" src="/avatars/claude.webp" />
            <h1>Dev Guild</h1>
            <p>Enter your access passphrase to open your group chat.</p>
            <input autoFocus type="password" autoComplete="current-password" placeholder="Access passphrase" value={pass} onChange={(e) => setPass(e.target.value)} aria-label="Passphrase" />
            {loginError ? <div className="err">{loginError}</div> : null}
            <button type="submit">Open the guild</button>
          </form>
        </main>
      </PhoneFrame>
    );
  }

  const showOnboarding = !demoMode && role === "owner" && guildThread && guildThread.setupDone === false && characters.length > 0;
  const title = thread ? thread.title : "Dev Guild";
  const memberCount = thread && thread.group ? Math.max((thread.members || []).length - 1, 0) : 0;
  const subtitle = thread && thread.group
    ? (demoMode ? `${memberCount} demo agents ›` : (memberCount === 0 ? "Empty group ›" : `${memberCount} members ›`))
    : "iMessage ›";
  const vibe = roomVibe({
    members: thread && thread.members,
    onlineIds: characters.filter((c) => c.online).map((c) => c.id),
    demo: demoMode,
  });
  const starters = starterPrompts({
    members: thread && thread.members,
    characters,
    demo: demoMode,
  });
  const headerFaces = thread && thread.group
    ? (thread.members || []).filter((id) => id !== "owner").slice(0, 4)
    : [(thread && (thread.members || []).find((id) => id !== "owner")) || "tay"];

  return (
    <PhoneFrame>
      {mobileList ? (
        <div className="root-view">
          <header className="root-header">
            <div className="root-top">
              <h1>{sidebarTab === "contacts" ? "Contacts" : "Messages"}</h1>
              <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                {!demoMode && role === "owner" ? (
                  <button type="button" className="ios-icon-btn" onClick={() => openGroupSheet("custom")} aria-label="Add custom agent">+</button>
                ) : null}
                <button type="button" className="ios-icon-btn" onClick={() => setComposing(!composing)} aria-label="New message">✎</button>
              </div>
            </div>
            <div className="ios-seg">
              <button type="button" className={sidebarTab === "chats" ? "active" : ""} onClick={() => { setSidebarTab("chats"); setComposing(false); }}>
                Chats ({threads.length})
              </button>
              <button type="button" className={sidebarTab === "contacts" ? "active" : ""} onClick={() => { setSidebarTab("contacts"); setComposing(false); }}>
                Contacts ({characters.length})
              </button>
            </div>
          </header>

          {composing ? (
            <div className="list-scroll">
              <div className="ios-section-title"><span>New 1:1 Message</span></div>
              <div className="ios-inset-card" style={{ margin: "0 12px" }}>
                {characters.slice(0, 60).map((c) => (
                  <button type="button" className="chat-row" key={c.id} onClick={() => openDM(c.id)}>
                    <Avatar avatar={c.avatar} emoji={c.emoji} color={c.color} name={c.name} />
                    <div className="chat-row-body">
                      <div className="chat-row-top">
                        <strong>{c.name}</strong>
                        <span className={c.online ? "dot on" : "dot"} />
                      </div>
                      <div className="chat-row-preview">{c.online ? c.role : c.note}</div>
                    </div>
                  </button>
                ))}
              </div>
            </div>
          ) : sidebarTab === "contacts" ? (
            <>
              <div className="ios-search-wrap">
                <div className="ios-search-box">
                  <span aria-hidden="true">🔍</span>
                  <input
                    type="search"
                    placeholder={`Search ${characters.length} agents & APIs...`}
                    aria-label="Search contacts"
                    value={contactFilter}
                    onChange={(e) => setContactFilter(e.target.value)}
                  />
                </div>
              </div>
              <div className="section-pills">
                {sectionNames.map((sec) => (
                  <button key={sec} type="button" className={sectionFilter === sec ? "section-pill active" : "section-pill"} onClick={() => setSectionFilter(sec)}>
                    {sec}
                  </button>
                ))}
              </div>
              <div className="contacts-body">
                <div
                  className="contacts-scroll"
                  onScroll={(e) => {
                    const el = e.currentTarget;
                    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 240 && visibleContactsCount < filteredContacts.length) {
                      setVisibleContactsCount((n) => Math.min(filteredContacts.length, n + 60));
                    }
                  }}
                >
                  {contactGroups.map((group) => (
                    <div key={group.key} className="contact-group" ref={(el) => { if (el) sectionRefs.current[group.key] = el; }}>
                      <div className="ios-section-title">
                        <span>{group.label}</span>
                        <span>{group.items.length}</span>
                      </div>
                      <div className="ios-inset-card">
                        {group.items.map((c) => {
                          const inGroup = groupMemberSet.has(c.id);
                          return (
                            <div className="ios-contact-row" key={c.id}>
                              <button type="button" className="contact-main" onClick={() => setProfile(c)}>
                                <Avatar avatar={c.avatar} emoji={c.emoji} color={c.color} name={c.name} />
                                <div className="contact-meta">
                                  <div className="contact-name">
                                    {c.name}
                                    <span className={c.isPersona ? "badge persona" : (c.online ? "badge ok" : "badge warn")}>
                                      {c.isPersona ? "Persona" : (c.online ? "Active" : "Needs key")}
                                    </span>
                                  </div>
                                  <div className="contact-sub">{c.online ? c.role : (c.envVar ? `Needs ${c.envVar}` : c.note)}</div>
                                </div>
                              </button>
                              <div className="contact-actions">
                                {!demoMode ? (
                                  <button type="button" className={inGroup ? "pill-btn on" : "pill-btn"} onClick={() => toggleGroupMember(c.id)}>
                                    {inGroup ? "In" : "+"}
                                  </button>
                                ) : null}
                                <button type="button" className="pill-btn primary" onClick={() => openDM(c.id)}>Chat</button>
                              </div>
                            </div>
                          );
                        })}
                      </div>
                    </div>
                  ))}
                  {visibleContactsCount < filteredContacts.length ? (
                    <div style={{ padding: "12px 6px", textAlign: "center" }}>
                      <button type="button" className="pill-btn primary" onClick={() => setVisibleContactsCount((n) => Math.min(filteredContacts.length, n + 80))}>
                        Show more ({filteredContacts.length - visibleContactsCount} remaining)
                      </button>
                    </div>
                  ) : null}
                </div>
                {railTokens.length > 1 ? (
                  <div className="index-rail" aria-label="Section index">
                    {railTokens.map((item) => (
                      <button key={item.key} type="button" onClick={() => { const el = sectionRefs.current[item.key]; if (el) el.scrollIntoView({ behavior: "smooth", block: "start" }); }}>
                        {item.ch}
                      </button>
                    ))}
                  </div>
                ) : null}
              </div>
            </>
          ) : (
            <div className="list-scroll">
              {threads.map((t) => {
                const faceId = (t.members || []).find((id) => id !== "owner") || "claude";
                const faceChar = byId[faceId];
                return (
                  <button type="button" className={t.id === active ? "chat-row active" : "chat-row"} key={t.id} onClick={() => { setActive(t.id); setMobileList(false); }}>
                    {t.group ? (
                      <span className="stack">
                        {(t.members || []).filter((id) => id !== "owner").slice(0, 3).map((id) => (
                          <Avatar key={id} avatar={byId[id] && byId[id].avatar} emoji={byId[id] && byId[id].emoji} color={byId[id] && byId[id].color} name={byId[id] && byId[id].name} size="xs" />
                        ))}
                        {(t.members || []).filter((id) => id !== "owner").length === 0 ? <Avatar avatar="claude.webp" size="sm" /> : null}
                      </span>
                    ) : (
                      <Avatar avatar={faceChar && faceChar.avatar} emoji={faceChar && faceChar.emoji} color={faceChar && faceChar.color} name={(faceChar && faceChar.name) || t.title} />
                    )}
                    <div className="chat-row-body">
                      <div className="chat-row-top">
                        <strong>{t.title}</strong>
                        <span>{t.last_at ? timeLabel(t.last_at) : "›"}</span>
                      </div>
                      <div className="chat-row-preview">{t.last || "No messages yet"}</div>
                    </div>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      ) : (
        <section className="thread-view">
          <header className="ios-navbar">
            <button type="button" className="nav-left" onClick={() => setMobileList(true)} aria-label="Messages">
              <span className="nav-chev">‹</span>
              <span className="nav-badge">{threads.length || 1}</span>
            </button>
            <button
              type="button"
              className="nav-center"
              onClick={() => {
                if (demoMode && thread && thread.group) openGroupSheet("demo");
                else if (thread && thread.group) openGroupSheet("members");
                else if (thread) setProfile(byId[(thread.members || []).find((id) => id !== "owner")] || null);
              }}
            >
              <span className="stack">
                {(headerFaces.length ? headerFaces : ["claude"]).map((fid) => {
                  const fc = byId[fid] || {};
                  return <Avatar key={fid} avatar={fc.avatar} emoji={fc.emoji} color={fc.color} name={fc.name} size="xs" />;
                })}
              </span>
              <span className="nav-title">{title}</span>
              <span className="nav-sub" title={vibe.detail}>{thread && thread.group ? vibe.label : subtitle}</span>
            </button>
            <div className="nav-right">
              <button type="button" className="nav-pill-icon" onClick={() => { setShowSearch((s) => !s); setHits(null); }} aria-label="Search chat">🔍</button>
              <button type="button" className="nav-pill-icon" onClick={() => openGroupSheet(demoMode ? "demo" : "members")} aria-label={demoMode ? "Demo info" : "Add or invite"}>
                {demoMode ? "ⓘ" : "+"}
              </button>
            </div>
          </header>

          {demoMode ? (
            <div className="demo-bar">
              <span>
                <strong>{liveDemo ? "Live Gemini Demo" : "Demo Voice"}</strong>
                {rateInfo ? ` · ${rateInfo.remaining}/${rateInfo.limit} msgs left` : ""}
              </span>
              <button type="button" className={autoPlay ? "demo-toggle" : "demo-toggle paused"} onClick={() => setAutoPlay((v) => !v)}>
                {autoPlay ? "● Auto Chat" : "❚❚ Paused"}
              </button>
            </div>
          ) : null}

          {error ? <div className="err-banner">{error}</div> : null}

          {showSearch ? (
            <form className="search-bar" onSubmit={searchHistory}>
              <input autoFocus aria-label="Search this chat" placeholder="Search this conversation..." value={query} onChange={(e) => setQuery(e.target.value)} />
              <button type="button" className="ios-text-btn" onClick={() => { setShowSearch(false); setHits(null); }}>Done</button>
            </form>
          ) : null}
          {hits ? (
            <div className="hits">
              {hits.length === 0 ? <p>No matching lines.</p> : hits.slice(0, 3).map((m) => <p key={m.id}>{m.name}: {m.text || m.task}</p>)}
            </div>
          ) : null}

          <div className="transcript" ref={scroller}>
            {messages.length === 0 && thread && thread.group ? (
              <div className="empty-room">
                <span className="stack">
                  {(headerFaces.length ? headerFaces : ["claude", "grok", "tay"]).slice(0, 4).map((fid) => {
                    const fc = byId[fid] || {};
                    return <Avatar key={fid} avatar={fc.avatar} emoji={fc.emoji} color={fc.color} name={fc.name} size="sm" />;
                  })}
                </span>
                <h2>{title}</h2>
                {demoMode ? (
                  <>
                    <p>Agents are warming up! Jump in anytime or tap a prompt below.</p>
                    <div className="starter-chips">
                      {starters.map((q) => (
                        <button key={q} type="button" className="starter-chip" onClick={() => send(q)}>{q}</button>
                      ))}
                    </div>
                  </>
                ) : memberCount === 0 ? (
                  <>
                    <p>Your group chat starts empty. Pick built-in agents, 500+ directory personas, or share an invite link.</p>
                    <button type="button" className="pill-btn primary" onClick={() => openGroupSheet("members")}>+ Pick Members or Invite</button>
                  </>
                ) : (
                  <>
                    <p>Send an iMessage or @mention any member to start the conversation.</p>
                    {starters.length ? (
                      <div className="starter-chips">
                        {starters.map((q) => (
                          <button key={q} type="button" className="starter-chip" onClick={() => send(q)}>{q}</button>
                        ))}
                      </div>
                    ) : null}
                  </>
                )}
              </div>
            ) : null}

            {messages.map((m, i) => {
              const prev = messages[i - 1];
              const next = messages[i + 1];
              const showTime = !prev || new Date(m.time) - new Date(prev.time) > 5 * 60 * 1000;
              if (m.kind === "system" || m.kind === "tool") {
                return <div className="system" key={m.id}>{m.kind === "tool" ? `${m.name} · ${m.tool}: ${m.text}` : m.text}</div>;
              }
              if (m.kind === "card") {
                return (
                  <article className="task-card" key={m.id}>
                    <h3>Task</h3>
                    <p>{m.task || m.text}</p>
                    <p className="meta">@{m.assignee} · {m.card_status}</p>
                  </article>
                );
              }
              const person = byId[m.from] || { name: m.name, color: m.color || "#8e8e93", id: m.from, avatar: m.avatar };
              const samePrev = prev && prev.kind !== "system" && prev.kind !== "tool" && prev.kind !== "card" && prev.from === m.from && !showTime;
              const sameNext = next && next.kind !== "system" && next.kind !== "tool" && next.kind !== "card" && next.from === m.from && (new Date(next.time) - new Date(m.time) <= 5 * 60 * 1000);
              const lastMine = m.mine && messages.slice(i + 1).every((n) => !n.mine);
              const messageReactions = Array.isArray(m.reactions) ? m.reactions : [];

              return (
                <div key={m.id}>
                  {showTime ? <div className="stamp">{timeLabel(m.time)}</div> : null}
                  {!m.mine && thread && thread.group && !samePrev ? <div className="sender-label">{person.name}</div> : null}
                  <div className={`${m.mine ? "bubble-row mine" : "bubble-row"}${samePrev ? " grouped" : ""}`}>
                    {!m.mine ? (
                      <div className="avatar-slot">
                        {!sameNext ? (
                          <button type="button" className="avatar-btn" onClick={() => setProfile(person)} aria-label={person.name}>
                            <Avatar avatar={person.avatar || m.avatar} emoji={person.emoji} color={person.color} name={person.name} size="xs" />
                          </button>
                        ) : null}
                      </div>
                    ) : null}
                    <div
                      className={`bubble ${m.mine ? "out" : "in"}${!sameNext ? " has-tail" : ""}`}
                      onDoubleClick={() => setReactionFor(m.id)}
                      onPointerDown={() => {
                        clearTimeout(pressTimer.current);
                        pressTimer.current = setTimeout(() => setReactionFor(m.id), 550);
                      }}
                      onPointerUp={() => clearTimeout(pressTimer.current)}
                      onPointerLeave={() => clearTimeout(pressTimer.current)}
                      onContextMenu={(e) => { e.preventDefault(); setReactionFor(m.id); }}
                    >
                      {m.reasoning ? <details className="thinking"><summary>Thinking</summary><p>{m.reasoning}</p></details> : null}
                      {m.text}
                      {m.secret ? <SecretCard card={m.secret} /> : null}
                      {(m.secrets || []).map((c) => <SecretCard key={c.id} card={c} />)}
                      {(m.files || []).map((name) => <span className="file-chip" key={name}>{name}</span>)}
                    </div>
                  </div>
                  {messageReactions.length || reactionFor === m.id ? (
                    <div className={`bubble-extras${m.mine ? " mine" : ""}`}>
                      {messageReactions.length ? (
                        <div className="reaction-chips">
                          {messageReactions.map((r) => (
                            <button
                              type="button"
                              key={r.emoji}
                              className={r.mine ? "reaction-chip mine" : "reaction-chip"}
                              onClick={() => react(m.id, r.emoji)}
                              aria-label={`${r.emoji} ${r.count}`}
                            >
                              {r.emoji}{r.count > 1 ? ` ${r.count}` : ""}
                            </button>
                          ))}
                        </div>
                      ) : null}
                      {reactionFor === m.id ? (
                        <div className="reaction-picker" role="group" aria-label="React to message">
                          {REACTIONS.map((emoji) => (
                            <button
                              type="button"
                              key={emoji}
                              onClick={() => { react(m.id, emoji); setReactionFor(null); }}
                              aria-label={`React ${emoji}`}
                            >
                              {emoji}
                            </button>
                          ))}
                        </div>
                      ) : null}
                    </div>
                  ) : null}
                  {lastMine && m.status ? <div className="receipt">{m.status}</div> : null}
                </div>
              );
            })}

            {live && live.text ? (
              <div>
                {thread && thread.group && byId[live.from] ? <div className="sender-label">{byId[live.from].name}</div> : null}
                <div className="bubble-row">
                  <div className="avatar-slot">
                    <Avatar avatar={(byId[live.from] || {}).avatar} emoji={(byId[live.from] || {}).emoji} color={(byId[live.from] || {}).color} name={(byId[live.from] || {}).name} size="xs" />
                  </div>
                  <div className="bubble in has-tail">
                    {live.reasoning ? <details className="thinking"><summary>Thinking</summary><p>{live.reasoning}</p></details> : null}
                    {live.text}
                  </div>
                </div>
              </div>
            ) : null}

            {pending && !(live && live.text) ? (
              <div className="bubble-row" style={{ marginTop: 4 }}>
                <div className="avatar-slot">
                  <Avatar avatar={(byId[(live && live.from) || "tay"] || {}).avatar} size="xs" />
                </div>
                <div className="bubble in has-tail" aria-label="Typing">
                  <span className="typing"><i /><i /><i /></span>
                </div>
              </div>
            ) : null}
          </div>

          <form className="ios-composer" onSubmit={(e) => { e.preventDefault(); send(); }}>
            {suggestions.length ? (
              <div className="mentions">
                {suggestions.map((c) => (
                  <button type="button" key={c.id} onClick={() => setText((value) => value.replace(/@([a-z0-9-]*)$/, `@${c.id} `))}>
                    <Avatar avatar={c.avatar} emoji={c.emoji} color={c.color} name={c.name} size="xs" />
                    <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{c.name}</span>
                    <span className={c.online ? "dot on" : "dot"} />
                  </button>
                ))}
              </div>
            ) : null}
            <label className={demoMode ? "plus-circle disabled" : "plus-circle"} title={demoMode ? "Attachments disabled in demo" : "Attach image or PDF"}>
              +
              {!demoMode ? (
                <input type="file" accept="image/png,image/jpeg,image/gif,image/webp,application/pdf" aria-label="Attach image or PDF" onChange={(e) => setFiles(Array.from(e.target.files || []))} />
              ) : null}
            </label>
            {!demoMode && role === "owner" ? (
              <button type="button" className="plus-circle key-btn" title="Add secret" aria-label="Add secret" onClick={() => setSecretDraft({})}>
                <span aria-hidden="true">🔑</span>
              </button>
            ) : null}
            <div className="composer-pill">
              <textarea
                rows={1}
                placeholder={demoMode ? "iMessage (Public Demo)" : "iMessage"}
                aria-label="Message"
                maxLength={demoMode ? 500 : undefined}
                value={text}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); } }}
              />
              <button className="send-arrow" type="submit" disabled={!text.trim() || pending} aria-label="Send">↑</button>
            </div>
          </form>
        </section>
      )}

      {secretDraft ? (
        <SecretSheet
          draft={secretDraft}
          thread={active}
          agents={characters.filter((c) => groupMemberSet.has(c.id) && c.id !== "owner")}
          onClose={() => setSecretDraft(null)}
          onSaved={() => loadMessages(active, false, false).catch(() => {})}
        />
      ) : null}

      <ProfileSheet
        profile={profile}
        demoMode={demoMode}
        groupMemberSet={groupMemberSet}
        onClose={() => setProfile(null)}
        onOpenDM={openDM}
        onToggleMember={toggleGroupMember}
      />

      {showOnboarding ? (
        <div className="sheet">
          <form className="card form-stack" onSubmit={(e) => completeGroupSetup(e, false)}>
            <div className="grabber" />
            <div className="sheet-head">
              <h2>Create Your Group Chat</h2>
              <button type="button" className="ios-text-btn" disabled={setupBusy} onClick={() => completeGroupSetup(null, true)}>Start Empty</button>
            </div>
            <p className="presence" style={{ marginTop: 0 }}>Name your group and pick which agents or free AI personas join.</p>
            <div className="two-col">
              <label><span>Group Name</span><input className="field-input" value={setupTitle} onChange={(e) => setSetupTitle(e.target.value)} placeholder="My Dev Guild" /></label>
              <label><span>Your Name</span><input className="field-input" value={setupOwner} onChange={(e) => setSetupOwner(e.target.value)} placeholder="Your display name" /></label>
            </div>
            <div className="quick-row">
              <button type="button" className="pill-btn" onClick={() => setSetupMembers(STARTER_IDS.filter((id) => characters.some((c) => c.id === id)))}>Starter Team (5)</button>
              <button type="button" className="pill-btn" onClick={() => setSetupMembers(characters.filter((c) => c.online).slice(0, 20).map((c) => c.id))}>Top 20 Active</button>
              <button type="button" className="pill-btn" onClick={() => setSetupMembers([])}>Clear ({setupMembers.length})</button>
            </div>
            <input className="field-input" type="search" placeholder="Filter agents, presets, or directory..." value={setupFilter} onChange={(e) => setSetupFilter(e.target.value)} />
            <div className="member-list">
              {filteredSetupMembers.map((c) => {
                const picked = setupMembers.includes(c.id);
                return (
                  <div className="member-item" key={c.id}>
                    <Avatar avatar={c.avatar} emoji={c.emoji} color={c.color} name={c.name} size="sm" />
                    <div className="member-meta"><strong>{c.name}</strong><span>@{c.id} · {c.role}</span></div>
                    <button type="button" className={picked ? "pill-btn on" : "pill-btn"} onClick={() => toggleSetupMember(c.id)}>{picked ? "Selected ✓" : "+ Pick"}</button>
                  </div>
                );
              })}
            </div>
            <button type="submit" className="pill-btn primary full" disabled={setupBusy}>{setupBusy ? "Creating..." : `Create Group Chat (${setupMembers.length} selected)`}</button>
          </form>
        </div>
      ) : null}

      {groupSheet === "demo" && demoMode ? (
        <DemoSheet characters={characters} onClose={() => setGroupSheet(null)} onOpenDM={openDM} />
      ) : null}

      {groupSheet && groupSheet !== "demo" && !demoMode ? (
        <div className="sheet" onClick={() => setGroupSheet(null)}>
          <article className="card" onClick={(e) => e.stopPropagation()}>
            <div className="grabber" />
            <div className="sheet-head">
              <h2>Add &amp; Invite to Group</h2>
              <button type="button" className="ios-text-btn" onClick={() => setGroupSheet(null)}>Done</button>
            </div>
            <div className="ios-seg">
              <button type="button" className={groupSheet === "members" ? "active" : ""} onClick={() => openGroupSheet("members")}>Roster ({groupMemberSet.size > 0 ? groupMemberSet.size - 1 : 0})</button>
              {role === "owner" ? <button type="button" className={groupSheet === "settings" ? "active" : ""} onClick={() => openGroupSheet("settings")}>Group</button> : null}
              <button type="button" className={groupSheet === "custom" ? "active" : ""} onClick={() => openGroupSheet("custom")}>+ Agent</button>
              <button type="button" className={groupSheet === "invite" ? "active" : ""} onClick={() => openGroupSheet("invite")}>Invite</button>
            </div>
            {groupSheet === "members" ? (
              <div className="sheet-body">
                <input className="field-input" type="search" placeholder={`Filter ${characters.length} agents & personas...`} value={memberFilter} onChange={(e) => setMemberFilter(e.target.value)} />
                <div className="member-list">
                  {filteredMembers.map((c) => {
                    const inGroup = groupMemberSet.has(c.id);
                    return (
                      <div className="member-item" key={c.id}>
                        <Avatar avatar={c.avatar} emoji={c.emoji} color={c.color} name={c.name} size="sm" />
                        <div className="member-meta"><strong>{c.name}</strong><span>@{c.id} · {c.role}</span></div>
                        <button type="button" className={inGroup ? "pill-btn on" : "pill-btn"} onClick={() => toggleGroupMember(c.id)}>{inGroup ? "In ✓" : "+ Add"}</button>
                      </div>
                    );
                  })}
                </div>
              </div>
            ) : null}
            {groupSheet === "settings" && role === "owner" ? (
              <form className="sheet-body form-stack" onSubmit={saveGroupSettings}>
                <label><span>Group Name</span><input className="field-input" value={setupTitle} onChange={(e) => setSetupTitle(e.target.value)} placeholder="My Dev Guild" /></label>
                <label><span>Your Display Name</span><input className="field-input" value={setupOwner} onChange={(e) => setSetupOwner(e.target.value)} placeholder="Owner" /></label>
                {settingsMsg ? <p className="presence">{settingsMsg}</p> : null}
                <button type="submit" className="pill-btn primary full" disabled={setupBusy}>{setupBusy ? "Saving..." : "Save Group Settings"}</button>
              </form>
            ) : null}
            {groupSheet === "custom" ? (
              <form className="sheet-body form-stack" onSubmit={submitCustomAgent}>
                <label><span>Agent Name</span><input className="field-input" required placeholder="e.g. Nova" value={customName} onChange={(e) => setCustomName(e.target.value)} /></label>
                <label><span>Persona / Instructions</span><textarea className="field-input" rows={3} placeholder="How this agent talks and collaborates..." value={customPersona} onChange={(e) => setCustomPersona(e.target.value)} /></label>
                <div className="two-col">
                  <label><span>Role (optional)</span><input className="field-input" placeholder="Security reviewer" value={customRole} onChange={(e) => setCustomRole(e.target.value)} /></label>
                  <label><span>Catchphrase (optional)</span><input className="field-input" placeholder="Trust, then verify." value={customCatch} onChange={(e) => setCustomCatch(e.target.value)} /></label>
                </div>
                <label>
                  <span>Model / Endpoint</span>
                  <div className="seg">
                    <button type="button" className={customEndpoint === "gemini" ? "active" : ""} onClick={() => { setCustomEndpoint("gemini"); setCustomModel("gemini-3.5-flash-lite"); }}>Free Gemini</button>
                    <button type="button" className={customEndpoint === "openai" ? "active" : ""} onClick={() => { setCustomEndpoint("openai"); setCustomModel(""); }}>OpenAI-compatible</button>
                  </div>
                </label>
                {customEndpoint === "gemini" ? (
                  <label>
                    <span>Gemini Free Model</span>
                    <select className="field-input" value={customModel} onChange={(e) => setCustomModel(e.target.value)}>
                      <option value="gemini-3.5-flash-lite">gemini-3.5-flash-lite (Fast)</option>
                      <option value="gemini-4-hydrogen">gemini-4-hydrogen (Deep)</option>
                      <option value="gemma-4-31b-it">gemma-4-31b-it (Open weights)</option>
                    </select>
                  </label>
                ) : (
                  <div className="form-stack">
                    <label><span>Base URL</span><input className="field-input" required placeholder="https://api.groq.com/openai/v1" value={customBaseUrl} onChange={(e) => setCustomBaseUrl(e.target.value)} /></label>
                    <div className="two-col">
                      <label><span>Model ID</span><input className="field-input" required placeholder="llama-3.1-8b-instant" value={customModel} onChange={(e) => setCustomModel(e.target.value)} /></label>
                      <label><span>API Key (server-side)</span><input className="field-input" type="password" required placeholder="sk-..." value={customApiKey} onChange={(e) => setCustomApiKey(e.target.value)} /></label>
                    </div>
                  </div>
                )}
                {customMsg ? <p className="presence">{customMsg}</p> : null}
                <button type="submit" className="pill-btn primary full" disabled={customBusy || !customName.trim()}>{customBusy ? "Adding..." : "Add Agent to Group"}</button>
              </form>
            ) : null}
            {groupSheet === "invite" ? (
              <div className="sheet-body form-stack">
                {role !== "owner" ? (
                  <p className="presence">Only the guild owner can view or share the invite link.</p>
                ) : inviteInfo ? (
                  <>
                    <div>
                      <h3>Shareable Invite Link</h3>
                      <p className="presence">Anyone (or any agent) with this link can join {inviteInfo.groupTitle || title}.</p>
                      <div className="copy-row">
                        <input className="field-input" readOnly value={inviteInfo.url} />
                        <button type="button" className="pill-btn primary" onClick={() => copyText("link", inviteInfo.url)}>{copied === "link" ? "Copied" : "Copy"}</button>
                      </div>
                    </div>
                    <div>
                      <h3>Invite Token</h3>
                      <div className="copy-row">
                        <input className="field-input" readOnly value={inviteInfo.code} />
                        <button type="button" className="pill-btn" onClick={() => copyText("code", inviteInfo.code)}>{copied === "code" ? "Copied" : "Copy"}</button>
                      </div>
                    </div>
                    <div>
                      <h3>External Agent Join &amp; Post API</h3>
                      <pre className="code-box">{`curl -X POST ${inviteInfo.messageEndpoint} \\\n  -H "Content-Type: application/json" \\\n  -d '{"invite":"${inviteInfo.code}","from":"my-agent","name":"My Agent","text":"Hello @tay!"}'`}</pre>
                    </div>
                  </>
                ) : (
                  <p className="presence">Loading invite details...</p>
                )}
              </div>
            ) : null}
          </article>
        </div>
      ) : null}
    </PhoneFrame>
  );
}
