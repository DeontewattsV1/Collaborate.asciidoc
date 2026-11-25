"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

const STARTER_IDS = ["claude", "grok", "gemini", "codex", "tay"];

async function api(path, options) {
  const res = await fetch(path, {
    ...options,
    headers: { "Content-Type": "application/json", ...(options && options.headers) },
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const error = new Error(data.error || "The guild didn't answer.");
    error.status = res.status;
    throw error;
  }
  return data;
}

function timeLabel(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const now = new Date();
  const same = d.toDateString() === now.toDateString();
  const clock = d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  return same ? clock : `${d.toLocaleDateString([], { weekday: "short" })} ${clock}`;
}

function Avatar({ id, avatar, size = "" }) {
  const cls = size === "lg" ? "avatar-lg" : `avatar ${size}`;
  const file = avatar || `${id || "claude"}.png`;
  const src = file.startsWith("/") ? file : `/avatars/${file}`;
  return <img className={cls} alt="" src={src} />;
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

function readCachedMessages(thread) {
  try {
    const raw = window.localStorage.getItem(`devguild_msgs_v1_${thread}`);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function writeCachedMessages(thread, list) {
  try {
    window.localStorage.setItem(`devguild_msgs_v1_${thread}`, JSON.stringify((list || []).slice(-120)));
  } catch {
    // ignore storage quota errors
  }
}

export default function App() {
  const [ready, setReady] = useState(false);
  const [authed, setAuthed] = useState(false);
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
  const [mobileList, setMobileList] = useState(true);

  const [messages, setMessages] = useState([]);
  const [text, setText] = useState("");
  const [pending, setPending] = useState(false);
  const [live, setLive] = useState(null);
  const [files, setFiles] = useState([]);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState(null);
  const [profile, setProfile] = useState(null);
  const [composing, setComposing] = useState(false);
  const [error, setError] = useState("");

  const [groupSheet, setGroupSheet] = useState(null); // null | "members" | "settings" | "custom" | "invite"
  const [memberFilter, setMemberFilter] = useState("");
  const [inviteInfo, setInviteInfo] = useState(null);
  const [copied, setCopied] = useState("");

  // First-run group setup & settings state
  const [setupTitle, setSetupTitle] = useState("Dev Guild");
  const [setupOwner, setSetupOwner] = useState("");
  const [setupMembers, setSetupMembers] = useState([]);
  const [setupFilter, setSetupFilter] = useState("");
  const [setupBusy, setSetupBusy] = useState(false);
  const [settingsMsg, setSettingsMsg] = useState("");

  // Custom agent form state
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
  const after = useRef(0);

  const guildThread = useMemo(
    () => threads.find((t) => t.id === "guild") || threads[0],
    [threads],
  );
  const effectiveOwnerName = (guildThread && guildThread.ownerName) || ownerName || "Owner";

  const byId = useMemo(() => {
    const map = { owner: { id: "owner", name: effectiveOwnerName, kind: "owner", color: "#0A84FF" } };
    for (const c of characters) map[c.id] = c;
    return map;
  }, [characters, effectiveOwnerName]);

  const groupMemberSet = useMemo(
    () => new Set((guildThread && guildThread.members) || []),
    [guildThread],
  );

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

  const loadMessages = useCallback(async (thread, reset) => {
    const start = reset ? 0 : after.current;
    const data = await api(`/api/guild/v1/threads/${thread}/messages?after=${start}`);
    const incoming = data.messages || [];
    setMessages((prev) => {
      const base = reset ? mergeMessages(readCachedMessages(thread), prev) : prev;
      const next = mergeMessages(base, incoming);
      writeCachedMessages(thread, next);
      after.current = next.reduce((m, row) => Math.max(m, row.seq || 0), 0);
      return next;
    });
    if (incoming.some((row) => row.from !== "owner")) setPending(false);
    return incoming;
  }, []);

  useEffect(() => {
    try {
      const params = new URLSearchParams(window.location.search);
      const inv = params.get("invite") || "";
      if (inv) setInviteParam(inv);
    } catch {
      // ignore
    }
    api("/api/session")
      .then((data) => {
        setAuthed(!!data.ok);
        if (data.role) setRole(data.role);
        if (data.participant && data.participant.name) {
          setOwnerName(data.participant.name);
          setSetupOwner(data.participant.name);
        }
        if (data.group && data.group.title) {
          setSetupTitle(data.group.title);
        }
      })
      .catch(() => setAuthed(false))
      .finally(() => setReady(true));
    if ("serviceWorker" in navigator) {
      navigator.serviceWorker.register("/sw.js").catch(() => {});
    }
  }, []);

  useEffect(() => {
    if (!authed) return;
    loadRoster().catch((err) => setError(err.message));
  }, [authed, loadRoster]);

  useEffect(() => {
    if (!authed || !active) return;
    after.current = 0;
    setPending(false);
    const cached = readCachedMessages(active);
    if (cached.length) setMessages(cached);
    else setMessages([]);
    loadMessages(active, true).catch((err) => setError(err.message));
    const timer = setInterval(() => {
      loadMessages(active, false).catch(() => {});
    }, 2500);
    return () => clearInterval(timer);
  }, [authed, active, loadMessages]);

  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages, pending, live, active]);

  async function signIn(event) {
    event.preventDefault();
    setLoginError("");
    try {
      const res = await api("/api/login", { method: "POST", body: JSON.stringify({ passphrase: pass }) });
      setRole(res.role || "owner");
      setAuthed(true);
      setPass("");
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
      if (res.messages) {
        setMessages((prev) => mergeMessages(prev, res.messages));
      }
      setMobileList(false);
    } catch (err) {
      setLoginError(err.message);
    }
  }

  function applyEvent(block) {
    const name = (block.match(/^event: (.+)$/m) || [])[1];
    const raw = (block.match(/^data: (.+)$/m) || [])[1];
    if (!name || !raw) return;
    const data = JSON.parse(raw);
    if (name === "typing") setLive({ from: data.from, text: "", reasoning: "" });
    if (name === "delta") {
      setLive((prev) => ({ from: data.from, text: ((prev && prev.text) || "") + data.text, reasoning: prev && prev.reasoning }));
    }
    if (name === "reasoning") setLive((prev) => ({ ...(prev || { from: data.from, text: "" }), reasoning: data.summary }));
    if (name === "tool" && data.message) {
      setMessages((prev) => {
        const next = mergeMessages(prev, [data.message]);
        writeCachedMessages(active, next);
        return next;
      });
    }
    if (name === "card" && data.message) {
      setMessages((prev) => {
        const next = mergeMessages(prev, [data.message]);
        writeCachedMessages(active, next);
        return next;
      });
    }
    if (name === "message" && data.message) {
      setLive((prev) => (prev && prev.from === data.message.from ? null : prev));
      setMessages((prev) => {
        const next = mergeMessages(prev, [data.message]);
        writeCachedMessages(active, next);
        return next;
      });
    }
    if (name === "done") {
      setLive(null);
      if (Array.isArray(data.messages) && data.messages.length) {
        setMessages((prev) => {
          const next = mergeMessages(prev, data.messages);
          writeCachedMessages(active, next);
          return next;
        });
      }
    }
    if (name === "fallback") setPending(true);
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

  async function send() {
    const body = text.trim();
    if ((!body && files.length === 0) || pending) return;
    setText("");
    const chosen = files;
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
      await loadMessages(active, false);
      await loadRoster();
    } catch (err) {
      setError(err.message);
    } finally {
      setPending(false);
    }
  }

  async function searchHistory(event) {
    event.preventDefault();
    if (!query.trim()) {
      setHits(null);
      return;
    }
    const data = await api(`/api/guild/search?thread=${encodeURIComponent(active)}&q=${encodeURIComponent(query)}`);
    setHits(data.messages || []);
  }

  async function openDM(id) {
    const thread = await api("/api/guild/v1/threads", {
      method: "POST",
      body: JSON.stringify({ with: id }),
    });
    await loadRoster();
    setActive(thread.id);
    setComposing(false);
    setSidebarTab("chats");
    setMobileList(false);
    setProfile(null);
  }

  async function toggleGroupMember(id) {
    const inGroup = groupMemberSet.has(id);
    await api("/api/guild/v1/group/members", {
      method: "POST",
      body: JSON.stringify({ id, action: inGroup ? "remove" : "add" }),
    });
    await loadRoster();
  }

  async function completeGroupSetup(event, startEmpty = false) {
    if (event) event.preventDefault();
    if (setupBusy) return;
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
      await loadMessages("guild", true);
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
    if (setupBusy) return;
    setSetupBusy(true);
    setSettingsMsg("");
    try {
      await api("/api/guild/v1/group", {
        method: "POST",
        body: JSON.stringify({
          title: setupTitle.trim() || "Dev Guild",
          ownerName: setupOwner.trim() || "Owner",
          setupDone: true,
        }),
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
    if (tab === "invite" && role === "owner" && !inviteInfo) {
      try {
        const data = await api("/api/guild/invite");
        setInviteInfo(data);
      } catch (err) {
        setError(err.message);
      }
    }
  }

  async function submitCustomAgent(event) {
    event.preventDefault();
    if (!customName.trim() || customBusy) return;
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

  function toggleSetupMember(id) {
    setSetupMembers((prev) => (prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]));
  }

  const thread = threads.find((t) => t.id === active) || threads[0];
  const demo = characters.some((c) => (c.note || "").startsWith("Demo voice"));
  const mention = text.match(/(^|\s)@([a-z0-9-]*)$/);
  const suggestions = mention
    ? characters.filter((c) => c.id.includes(mention[2]) || c.name.toLowerCase().includes(mention[2])).slice(0, 6)
    : [];

  const filteredContacts = useMemo(() => {
    const q = contactFilter.trim().toLowerCase();
    return q
      ? characters.filter((c) =>
          c.name.toLowerCase().includes(q)
          || c.id.toLowerCase().includes(q)
          || (c.role || "").toLowerCase().includes(q),
        )
      : characters;
  }, [characters, contactFilter]);

  const contactGroups = useMemo(() => ([
    { key: "agents", label: "Guild Agents", items: filteredContacts.filter((c) => c.kind === "agent") },
    { key: "presets", label: "Free Public AI Presets", items: filteredContacts.filter((c) => c.kind === "preset") },
    { key: "custom", label: "Custom & Invited Agents", items: filteredContacts.filter((c) => c.kind === "custom") },
    { key: "tools", label: "Tool Mascots", items: filteredContacts.filter((c) => c.kind === "tool") },
  ]).filter((g) => g.items.length > 0), [filteredContacts]);

  const filteredMembers = useMemo(() => {
    const q = memberFilter.trim().toLowerCase();
    return q
      ? characters.filter((c) => c.name.toLowerCase().includes(q) || c.id.toLowerCase().includes(q))
      : characters;
  }, [characters, memberFilter]);

  const filteredSetupMembers = useMemo(() => {
    const q = setupFilter.trim().toLowerCase();
    return q
      ? characters.filter((c) => c.name.toLowerCase().includes(q) || c.id.toLowerCase().includes(q) || (c.role || "").toLowerCase().includes(q))
      : characters;
  }, [characters, setupFilter]);

  if (!ready) return <main className="login" />;
  if (!authed) {
    if (inviteParam) {
      return (
        <main className="login">
          <form onSubmit={joinWithInvite}>
            <img className="avatar-lg" alt="" src="/avatars/tay.png" />
            <h1>Join Group Chat</h1>
            <p>You were invited to the group chat. Pick your name (or agent persona) to join.</p>
            <input
              autoFocus
              placeholder="Your name or agent name"
              value={guestName}
              onChange={(e) => setGuestName(e.target.value)}
              aria-label="Display name"
            />
            <div className="seg">
              <button type="button" className={guestKind === "person" ? "active" : ""} onClick={() => setGuestKind("person")}>Person</button>
              <button type="button" className={guestKind === "agent" ? "active" : ""} onClick={() => setGuestKind("agent")}>AI Agent</button>
            </div>
            {guestKind === "agent" ? (
              <input
                placeholder="Agent persona (optional)"
                value={guestPersona}
                onChange={(e) => setGuestPersona(e.target.value)}
                aria-label="Agent persona"
              />
            ) : null}
            {loginError ? <div className="err">{loginError}</div> : null}
            <button type="submit">Join Group Chat</button>
            <button type="button" className="text-btn" onClick={() => setInviteParam("")}>Owner sign in instead</button>
          </form>
        </main>
      );
    }
    return (
      <main className="login">
        <form onSubmit={signIn}>
          <img className="avatar-lg" alt="" src="/avatars/claude.png" />
          <h1>Dev Guild</h1>
          <p>Enter your access passphrase to open your group chat.</p>
          <input
            autoFocus
            type="password"
            autoComplete="current-password"
            placeholder="Access passphrase"
            value={pass}
            onChange={(e) => setPass(e.target.value)}
            aria-label="Passphrase"
          />
          {loginError ? <div className="err">{loginError}</div> : null}
          <button type="submit">Open the guild</button>
        </form>
      </main>
    );
  }

  const showOnboarding = role === "owner" && guildThread && guildThread.setupDone === false && characters.length > 0;
  const title = thread ? thread.title : "Dev Guild";
  const memberCount = thread && thread.group ? Math.max((thread.members || []).length - 1, 0) : 0;
  const subtitle = thread && thread.group
    ? (memberCount === 0 ? "Empty group" : `${memberCount} member${memberCount === 1 ? "" : "s"}`)
    : "iMessage";

  return (
    <div className="app">
      <aside className={mobileList ? "sidebar" : "sidebar hide"}>
        <header>
          <h1>{sidebarTab === "contacts" ? "Contacts" : "Messages"}</h1>
          <div className="grow" />
          <button className="icon-btn" onClick={() => openGroupSheet("custom")} aria-label="Add custom agent">+ Agent</button>
          <button className="icon-btn" onClick={() => setComposing(!composing)} aria-label="New message">✎</button>
        </header>
        <div className="tabs">
          <button
            type="button"
            className={sidebarTab === "chats" ? "tab active" : "tab"}
            onClick={() => { setSidebarTab("chats"); setComposing(false); }}
          >
            Chats ({threads.length})
          </button>
          <button
            type="button"
            className={sidebarTab === "contacts" ? "tab active" : "tab"}
            onClick={() => { setSidebarTab("contacts"); setComposing(false); }}
          >
            Contacts ({characters.length})
          </button>
        </div>
        {composing ? (
          <div className="picker list">
            <h2>New 1:1 message</h2>
            {characters.map((c) => (
              <button className="row" key={c.id} onClick={() => openDM(c.id)}>
                <Avatar id={c.id} avatar={c.avatar} />
                <span>
                  <div className="who">{c.name}</div>
                  <div className="preview">{c.online ? c.role : c.note}</div>
                </span>
                <div className="grow" />
                <span className={c.online ? "dot on" : "dot"} />
              </button>
            ))}
          </div>
        ) : sidebarTab === "contacts" ? (
          <div className="list contacts-list">
            <div className="contact-tools">
              <input
                type="search"
                placeholder="Search contacts..."
                aria-label="Search contacts"
                value={contactFilter}
                onChange={(e) => setContactFilter(e.target.value)}
              />
              <button type="button" className="pill-btn primary" onClick={() => openGroupSheet("custom")}>
                + Add Custom Agent
              </button>
            </div>
            {contactGroups.map((group) => (
              <div key={group.key} className="contact-group">
                <h2>{group.label} ({group.items.length})</h2>
                {group.items.map((c) => {
                  const inGroup = groupMemberSet.has(c.id);
                  return (
                    <div className="row contact-row" key={c.id}>
                      <button type="button" className="contact-main" onClick={() => setProfile(c)}>
                        <Avatar id={c.id} avatar={c.avatar} />
                        <span>
                          <div className="who">
                            {c.name}{" "}
                            <span className={c.online ? "badge ok" : "badge warn"}>
                              {c.online ? "Active" : "Needs key"}
                            </span>
                          </div>
                          <div className="preview">{c.online ? c.role : (c.envVar ? `Needs ${c.envVar}` : c.note)}</div>
                        </span>
                      </button>
                      <div className="contact-actions">
                        <button
                          type="button"
                          className={inGroup ? "pill-btn on" : "pill-btn"}
                          onClick={() => toggleGroupMember(c.id)}
                          title={inGroup ? "In group chat" : "Add to group chat"}
                        >
                          {inGroup ? "In group" : "+ Group"}
                        </button>
                        <button type="button" className="pill-btn" onClick={() => openDM(c.id)}>
                          Chat
                        </button>
                      </div>
                    </div>
                  );
                })}
              </div>
            ))}
          </div>
        ) : (
          <div className="list">
            {threads.map((t) => {
              const faceId = (t.members || []).find((id) => id !== "owner") || "claude";
              const faceChar = byId[faceId];
              return (
                <button
                  className={t.id === active ? "row active" : "row"}
                  key={t.id}
                  onClick={() => { setActive(t.id); setMobileList(false); }}
                >
                  {t.group ? (
                    <span className="stack">
                      {(t.members || []).filter((id) => id !== "owner").slice(0, 3).map((id) => (
                        <Avatar key={id} id={id} avatar={byId[id] && byId[id].avatar} />
                      ))}
                      {(t.members || []).filter((id) => id !== "owner").length === 0 ? (
                        <Avatar id="claude" avatar="claude.png" />
                      ) : null}
                    </span>
                  ) : (
                    <Avatar id={faceId} avatar={faceChar && faceChar.avatar} />
                  )}
                  <span>
                    <div className="who">{t.title}</div>
                    <div className="preview">{t.last || "No messages yet"}</div>
                  </span>
                </button>
              );
            })}
          </div>
        )}
      </aside>

      <section className={mobileList ? "thread hide" : "thread"}>
        <header className="bar">
          <button className="back text-btn" onClick={() => setMobileList(true)}>‹ Messages</button>
          <button
            className="icon-btn"
            onClick={() => {
              if (thread && thread.group) openGroupSheet("members");
              else if (thread) setProfile(byId[(thread.members || []).find((id) => id !== "owner")] || null);
            }}
            aria-label="Conversation"
          >
            <Avatar
              id={(thread && (thread.members || []).find((id) => id !== "owner")) || "tay"}
              avatar={(byId[(thread && (thread.members || []).find((id) => id !== "owner")) || "tay"] || {}).avatar}
              size="sm"
            />
          </button>
          <div className="titles">
            <strong>{title}</strong>
            <span>{subtitle}</span>
          </div>
          <div className="grow" />
          <button type="button" className="pill-btn primary" onClick={() => openGroupSheet("members")}>
            + Add / Invite
          </button>
        </header>

        {demo ? <div className="banner">Demo voice is on. Replies stay in character and no model is called.</div> : null}
        {error ? <div className="banner">{error}</div> : null}

        <div className="transcript" ref={scroller}>
          {messages.length === 0 && thread && thread.group ? (
            <div className="empty-room">
              <h2>{title}</h2>
              {memberCount === 0 ? (
                <>
                  <p>Your group chat starts empty. Pick built-in agents, free AI presets, custom endpoints, or share an invite link.</p>
                  <button type="button" className="pill-btn primary" onClick={() => openGroupSheet("members")}>
                    + Pick Members or Invite
                  </button>
                </>
              ) : (
                <p>Send a message or @mention any member to start the conversation.</p>
              )}
            </div>
          ) : null}
          {messages.map((m, i) => {
            const prev = messages[i - 1];
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
            const lastMine = m.mine && messages.slice(i + 1).every((n) => !n.mine);
            return (
              <div key={m.id}>
                {showTime ? <div className="stamp">{timeLabel(m.time)}</div> : null}
                <div className={m.mine ? "bubble-row mine" : "bubble-row"}>
                  {!m.mine ? (
                    <button className="icon-btn" onClick={() => setProfile(person)} aria-label={person.name}>
                      <Avatar id={m.from} avatar={person.avatar || m.avatar} size="sm" />
                    </button>
                  ) : null}
                  <div className={m.mine ? "bubble out" : "bubble in"}>
                    {!m.mine && thread && thread.group ? (
                      <span className="name" style={{ color: person.color || "#8e8e93" }}>{person.name}</span>
                    ) : null}
                    {m.reasoning ? (
                      <details className="thinking">
                        <summary>Thinking</summary>
                        <p>{m.reasoning}</p>
                      </details>
                    ) : null}
                    {m.text}
                    {(m.files || []).map((name) => <span className="file-chip" key={name}>{name}</span>)}
                  </div>
                </div>
                {lastMine && m.status ? <div className="receipt">{m.status}</div> : null}
              </div>
            );
          })}
          {live && live.text ? (
            <div className="bubble-row">
              <Avatar id={live.from || "claude"} avatar={(byId[live.from] || {}).avatar} size="sm" />
              <div className="bubble in">
                {thread && thread.group && byId[live.from] ? (
                  <span className="name" style={{ color: byId[live.from].color || "#8e8e93" }}>{byId[live.from].name}</span>
                ) : null}
                {live.reasoning ? (
                  <details className="thinking">
                    <summary>Thinking</summary>
                    <p>{live.reasoning}</p>
                  </details>
                ) : null}
                {live.text}
              </div>
            </div>
          ) : null}
          {pending && !(live && live.text) ? (
            <div className="bubble-row">
              <Avatar id={(live && live.from) || "tay"} avatar={(byId[(live && live.from) || "tay"] || {}).avatar} size="sm" />
              <div className="bubble in" aria-label="Typing">
                <span className="typing"><i /><i /><i /></span>
              </div>
            </div>
          ) : null}
        </div>

        <form className="search" onSubmit={searchHistory}>
          <input aria-label="Search this chat" placeholder="Search this chat" value={query} onChange={(e) => setQuery(e.target.value)} />
        </form>
        {hits ? (
          <div className="hits">
            {hits.length === 0 ? <p>No matching lines.</p> : hits.slice(0, 3).map((m) => <p key={m.id}>{m.name}: {m.text || m.task}</p>)}
          </div>
        ) : null}

        <form className="composer" onSubmit={(e) => { e.preventDefault(); send(); }}>
          {suggestions.length ? (
            <div className="mentions">
              {suggestions.map((c) => (
                <button
                  type="button"
                  key={c.id}
                  onClick={() => setText((value) => value.replace(/@([a-z0-9-]*)$/, `@${c.id} `))}
                >
                  <Avatar id={c.id} avatar={c.avatar} size="sm" />
                  <span>{c.name}</span>
                  <span className={c.online ? "dot on" : "dot"} />
                </button>
              ))}
            </div>
          ) : null}
          <label className="attach">
            +
            <input
              type="file"
              accept="image/png,image/jpeg,image/gif,image/webp,application/pdf"
              aria-label="Attach image or PDF"
              onChange={(e) => setFiles(Array.from(e.target.files || []))}
            />
          </label>
          <textarea
            rows={1}
            placeholder="iMessage"
            aria-label="Message"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                send();
              }
            }}
          />
          <button className="send" type="submit" disabled={!text.trim()} aria-label="Send">↑</button>
        </form>
      </section>

      {profile && profile.kind ? (
        <div className="sheet" onClick={() => setProfile(null)}>
          <article className="card" onClick={(e) => e.stopPropagation()}>
            <div className="hero">
              <Avatar id={profile.id} avatar={profile.avatar} size="lg" />
              <h2>{profile.name}</h2>
              <p className="role">{profile.kind} · {profile.role}</p>
              <p className="catch">“{profile.catchphrase}”</p>
            </div>
            <div className="facts">
              <div><h3>Look</h3><p>{profile.look}</p></div>
              <div><h3>Voice</h3><p>{profile.voice}</p></div>
              <div><h3>Quirk</h3><p>{profile.quirk}</p></div>
              <div><h3>With the others</h3><p>{profile.talks}</p></div>
              <div><h3>Expertise</h3><p>{profile.expertise}</p></div>
            </div>
            <p className="presence">{profile.online ? "Active" : "Needs key"} — {profile.note || profile.enable}</p>
            {profile.id !== "owner" ? (
              <div className="sheet-actions">
                <button className="pill-btn primary" onClick={() => openDM(profile.id)}>Message {profile.name}</button>
                <button className="pill-btn" onClick={() => toggleGroupMember(profile.id)}>
                  {groupMemberSet.has(profile.id) ? "Remove from group" : "Add to group"}
                </button>
              </div>
            ) : null}
          </article>
        </div>
      ) : null}

      {showOnboarding ? (
        <div className="sheet">
          <form className="card wide form-stack" onSubmit={(e) => completeGroupSetup(e, false)}>
            <div className="sheet-head">
              <h2>Create Your Group Chat</h2>
              <button type="button" className="text-btn" disabled={setupBusy} onClick={() => completeGroupSetup(null, true)}>
                Start Empty
              </button>
            </div>
            <p className="presence" style={{ marginTop: 0 }}>
              Name your group and pick which agents or free AI presets join. You can change this or invite others anytime.
            </p>
            <div className="two-col">
              <label>
                <span>Group Name</span>
                <input
                  className="field-input"
                  value={setupTitle}
                  onChange={(e) => setSetupTitle(e.target.value)}
                  placeholder="My Dev Guild"
                />
              </label>
              <label>
                <span>Your Name</span>
                <input
                  className="field-input"
                  value={setupOwner}
                  onChange={(e) => setSetupOwner(e.target.value)}
                  placeholder="Your display name"
                />
              </label>
            </div>
            <div className="quick-row">
              <button
                type="button"
                className="pill-btn"
                onClick={() => setSetupMembers(STARTER_IDS.filter((id) => characters.some((c) => c.id === id)))}
              >
                Starter Team (5)
              </button>
              <button
                type="button"
                className="pill-btn"
                onClick={() => setSetupMembers(characters.filter((c) => c.online).map((c) => c.id))}
              >
                All Active
              </button>
              <button type="button" className="pill-btn" onClick={() => setSetupMembers([])}>
                Clear ({setupMembers.length})
              </button>
            </div>
            <input
              className="field-input"
              type="search"
              placeholder="Filter agents, presets, or tool mascots..."
              value={setupFilter}
              onChange={(e) => setSetupFilter(e.target.value)}
            />
            <div className="member-list">
              {filteredSetupMembers.map((c) => {
                const picked = setupMembers.includes(c.id);
                return (
                  <div className="member-item" key={c.id}>
                    <Avatar id={c.id} avatar={c.avatar} size="sm" />
                    <div className="member-meta">
                      <strong>{c.name} <span className={c.online ? "badge ok" : "badge warn"}>{c.online ? "Active" : "Needs key"}</span></strong>
                      <span>@{c.id} · {c.role}</span>
                    </div>
                    <button
                      type="button"
                      className={picked ? "pill-btn on" : "pill-btn"}
                      onClick={() => toggleSetupMember(c.id)}
                    >
                      {picked ? "Selected ✓" : "+ Pick"}
                    </button>
                  </div>
                );
              })}
            </div>
            <button type="submit" className="pill-btn primary full" disabled={setupBusy}>
              {setupBusy ? "Creating..." : `Create Group Chat (${setupMembers.length} selected)`}
            </button>
          </form>
        </div>
      ) : null}

      {groupSheet ? (
        <div className="sheet" onClick={() => setGroupSheet(null)}>
          <article className="card wide" onClick={(e) => e.stopPropagation()}>
            <div className="sheet-head">
              <h2>Add &amp; Invite to Group</h2>
              <button type="button" className="text-btn" onClick={() => setGroupSheet(null)}>Done</button>
            </div>
            <div className="tabs">
              <button type="button" className={groupSheet === "members" ? "tab active" : "tab"} onClick={() => openGroupSheet("members")}>
                Roster ({groupMemberSet.size > 0 ? groupMemberSet.size - 1 : 0})
              </button>
              {role === "owner" ? (
                <button type="button" className={groupSheet === "settings" ? "tab active" : "tab"} onClick={() => openGroupSheet("settings")}>
                  Group
                </button>
              ) : null}
              <button type="button" className={groupSheet === "custom" ? "tab active" : "tab"} onClick={() => openGroupSheet("custom")}>
                + Custom Agent
              </button>
              <button type="button" className={groupSheet === "invite" ? "tab active" : "tab"} onClick={() => openGroupSheet("invite")}>
                Invite Link
              </button>
            </div>

            {groupSheet === "members" ? (
              <div className="sheet-body">
                <input
                  className="field-input"
                  type="search"
                  placeholder="Filter built-in characters & presets..."
                  value={memberFilter}
                  onChange={(e) => setMemberFilter(e.target.value)}
                />
                <div className="member-list">
                  {filteredMembers.map((c) => {
                    const inGroup = groupMemberSet.has(c.id);
                    return (
                      <div className="member-item" key={c.id}>
                        <Avatar id={c.id} avatar={c.avatar} size="sm" />
                        <div className="member-meta">
                          <strong>{c.name} <span className={c.online ? "badge ok" : "badge warn"}>{c.online ? "Active" : "Needs key"}</span></strong>
                          <span>@{c.id} · {c.role}</span>
                        </div>
                        <button type="button" className={inGroup ? "pill-btn on" : "pill-btn"} onClick={() => toggleGroupMember(c.id)}>
                          {inGroup ? "In group ✓" : "+ Add"}
                        </button>
                      </div>
                    );
                  })}
                </div>
              </div>
            ) : null}

            {groupSheet === "settings" && role === "owner" ? (
              <form className="sheet-body form-stack" onSubmit={saveGroupSettings}>
                <label>
                  <span>Group Name</span>
                  <input
                    className="field-input"
                    value={setupTitle}
                    onChange={(e) => setSetupTitle(e.target.value)}
                    placeholder="My Dev Guild"
                  />
                </label>
                <label>
                  <span>Your Display Name</span>
                  <input
                    className="field-input"
                    value={setupOwner}
                    onChange={(e) => setSetupOwner(e.target.value)}
                    placeholder="Owner"
                  />
                </label>
                {settingsMsg ? <p className="presence">{settingsMsg}</p> : null}
                <button type="submit" className="pill-btn primary full" disabled={setupBusy}>
                  {setupBusy ? "Saving..." : "Save Group Settings"}
                </button>
              </form>
            ) : null}

            {groupSheet === "custom" ? (
              <form className="sheet-body form-stack" onSubmit={submitCustomAgent}>
                <label>
                  <span>Agent Name</span>
                  <input
                    className="field-input"
                    required
                    placeholder="e.g. Nova"
                    value={customName}
                    onChange={(e) => setCustomName(e.target.value)}
                  />
                </label>
                <label>
                  <span>Persona / Instructions</span>
                  <textarea
                    className="field-input"
                    rows={3}
                    placeholder="How this agent talks, thinks, and collaborates..."
                    value={customPersona}
                    onChange={(e) => setCustomPersona(e.target.value)}
                  />
                </label>
                <div className="two-col">
                  <label>
                    <span>Role (optional)</span>
                    <input
                      className="field-input"
                      placeholder="e.g. Security reviewer"
                      value={customRole}
                      onChange={(e) => setCustomRole(e.target.value)}
                    />
                  </label>
                  <label>
                    <span>Catchphrase (optional)</span>
                    <input
                      className="field-input"
                      placeholder="e.g. Trust, then verify."
                      value={customCatch}
                      onChange={(e) => setCustomCatch(e.target.value)}
                    />
                  </label>
                </div>
                <label>
                  <span>Model / Endpoint</span>
                  <div className="seg">
                    <button
                      type="button"
                      className={customEndpoint === "gemini" ? "active" : ""}
                      onClick={() => { setCustomEndpoint("gemini"); setCustomModel("gemini-3.5-flash-lite"); }}
                    >
                      Free Gemini (default)
                    </button>
                    <button
                      type="button"
                      className={customEndpoint === "openai" ? "active" : ""}
                      onClick={() => { setCustomEndpoint("openai"); setCustomModel(""); }}
                    >
                      OpenAI-compatible
                    </button>
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
                    <label>
                      <span>Base URL</span>
                      <input
                        className="field-input"
                        required
                        placeholder="https://api.groq.com/openai/v1"
                        value={customBaseUrl}
                        onChange={(e) => setCustomBaseUrl(e.target.value)}
                      />
                    </label>
                    <div className="two-col">
                      <label>
                        <span>Model ID</span>
                        <input
                          className="field-input"
                          required
                          placeholder="llama-3.1-8b-instant"
                          value={customModel}
                          onChange={(e) => setCustomModel(e.target.value)}
                        />
                      </label>
                      <label>
                        <span>API Key (stored server-side only)</span>
                        <input
                          className="field-input"
                          type="password"
                          required
                          placeholder="sk-..."
                          value={customApiKey}
                          onChange={(e) => setCustomApiKey(e.target.value)}
                        />
                      </label>
                    </div>
                  </div>
                )}
                {customMsg ? <p className="presence">{customMsg}</p> : null}
                <button type="submit" className="pill-btn primary full" disabled={customBusy || !customName.trim()}>
                  {customBusy ? "Adding..." : "Add Agent to Group"}
                </button>
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
                        <button type="button" className="pill-btn primary" onClick={() => copyText("link", inviteInfo.url)}>
                          {copied === "link" ? "Copied" : "Copy"}
                        </button>
                      </div>
                    </div>
                    <div>
                      <h3>Invite Token</h3>
                      <div className="copy-row">
                        <input className="field-input" readOnly value={inviteInfo.code} />
                        <button type="button" className="pill-btn" onClick={() => copyText("code", inviteInfo.code)}>
                          {copied === "code" ? "Copied" : "Copy"}
                        </button>
                      </div>
                    </div>
                    <div>
                      <h3>External Agent Join &amp; Post API</h3>
                      <pre className="code-box">{`curl -X POST ${inviteInfo.messageEndpoint} \\
  -H "Content-Type: application/json" \\
  -d '{"invite":"${inviteInfo.code}","from":"my-agent","name":"My Agent","text":"Hello @tay!"}'`}</pre>
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
    </div>
  );
}
