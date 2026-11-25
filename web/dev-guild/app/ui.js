"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

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

function Avatar({ id, size = "" }) {
  const cls = size === "lg" ? "avatar-lg" : `avatar ${size}`;
  return <img className={cls} alt="" src={`/avatars/${id}.png`} />;
}

export default function App() {
  const [ready, setReady] = useState(false);
  const [authed, setAuthed] = useState(false);
  const [pass, setPass] = useState("");
  const [loginError, setLoginError] = useState("");
  const [characters, setCharacters] = useState([]);
  const [threads, setThreads] = useState([]);
  const [active, setActive] = useState("guild");
  const [mobileList, setMobileList] = useState(true);
  const [messages, setMessages] = useState([]);
  const [text, setText] = useState("");
  const [pending, setPending] = useState(false);
  const [profile, setProfile] = useState(null);
  const [composing, setComposing] = useState(false);
  const [error, setError] = useState("");
  const scroller = useRef(null);
  const after = useRef(0);

  const byId = useMemo(() => {
    const map = { owner: { id: "owner", name: "Deonte", kind: "owner", color: "#0A84FF" } };
    for (const c of characters) map[c.id] = c;
    return map;
  }, [characters]);

  const loadRoster = useCallback(async () => {
    const [cast, convos] = await Promise.all([
      api("/api/guild/v1/characters"),
      api("/api/guild/v1/threads"),
    ]);
    setCharacters(cast.characters || []);
    setThreads(convos.threads || []);
  }, []);

  const loadMessages = useCallback(async (thread, reset) => {
    const start = reset ? 0 : after.current;
    const data = await api(`/api/guild/v1/threads/${thread}/messages?after=${start}`);
    const incoming = data.messages || [];
    if (reset) {
      setMessages(incoming);
      after.current = incoming.reduce((m, row) => Math.max(m, row.seq || 0), 0);
    } else if (incoming.length) {
      setMessages((prev) => [...prev, ...incoming]);
      after.current = incoming.reduce((m, row) => Math.max(m, row.seq || 0), after.current);
      if (incoming.some((row) => row.from !== "owner")) setPending(false);
    }
    return incoming;
  }, []);

  useEffect(() => {
    api("/api/session")
      .then((data) => setAuthed(!!data.ok))
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
    loadMessages(active, true).catch((err) => setError(err.message));
    const timer = setInterval(() => {
      loadMessages(active, false).catch(() => {});
    }, 1200);
    return () => clearInterval(timer);
  }, [authed, active, loadMessages]);

  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages, pending, active]);

  async function signIn(event) {
    event.preventDefault();
    setLoginError("");
    try {
      await api("/api/login", { method: "POST", body: JSON.stringify({ passphrase: pass }) });
      setAuthed(true);
      setPass("");
    } catch (err) {
      setLoginError(err.message);
    }
  }

  async function send() {
    const body = text.trim();
    if (!body || pending) return;
    setText("");
    setPending(true);
    setError("");
    try {
      await api(`/api/guild/v1/threads/${active}/messages`, {
        method: "POST",
        body: JSON.stringify({ text: body }),
      });
      await loadMessages(active, false);
    } catch (err) {
      setPending(false);
      setError(err.message);
    }
  }

  async function openDM(id) {
    const thread = await api("/api/guild/v1/threads", {
      method: "POST",
      body: JSON.stringify({ with: id }),
    });
    await loadRoster();
    setActive(thread.id);
    setComposing(false);
    setMobileList(false);
    setProfile(null);
  }

  const thread = threads.find((t) => t.id === active) || threads[0];
  const demo = characters.some((c) => (c.note || "").startsWith("Demo voice"));
  const mention = text.match(/(^|\s)@([a-z0-9-]*)$/);
  const suggestions = mention
    ? characters.filter((c) => c.id.includes(mention[2]) || c.name.toLowerCase().includes(mention[2])).slice(0, 6)
    : [];

  if (!ready) return <main className="login" />;
  if (!authed) {
    return (
      <main className="login">
        <form onSubmit={signIn}>
          <img className="avatar-lg" alt="" src="/avatars/claude.png" />
          <h1>Dev Guild</h1>
          <p>The passphrase stays on this server. Model keys stay on the VM.</p>
          <input
            autoFocus
            type="password"
            autoComplete="current-password"
            placeholder="Passphrase"
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

  const title = thread ? thread.title : "Dev Guild";
  const subtitle = thread && thread.group ? `${(thread.members || []).length - 1} characters` : "iMessage";

  return (
    <div className="app">
      <aside className={mobileList ? "sidebar" : "sidebar hide"}>
        <header>
          <h1>Messages</h1>
          <div className="grow" />
          <button className="icon-btn" onClick={() => setComposing(true)} aria-label="New message">✎</button>
        </header>
        {composing ? (
          <div className="picker list">
            <h2>New message</h2>
            {characters.map((c) => (
              <button className="row" key={c.id} onClick={() => openDM(c.id)}>
                <Avatar id={c.avatar.replace(".png", "")} />
                <span>
                  <div className="who">{c.name}</div>
                  <div className="preview">{c.online ? c.role : "Offline"}</div>
                </span>
                <div className="grow" />
                <span className={c.online ? "dot on" : "dot"} />
              </button>
            ))}
          </div>
        ) : (
          <div className="list">
            {threads.map((t) => {
              const face = (t.members || []).find((id) => id !== "owner") || "claude";
              return (
                <button
                  className={t.id === active ? "row active" : "row"}
                  key={t.id}
                  onClick={() => { setActive(t.id); setMobileList(false); }}
                >
                  {t.group ? (
                    <span className="stack">
                      {(t.members || []).filter((id) => id !== "owner").slice(0, 3).map((id) => (
                        <Avatar key={id} id={id} />
                      ))}
                    </span>
                  ) : (
                    <Avatar id={face} />
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
          <button className="icon-btn" onClick={() => thread && setProfile(byId[(thread.members || []).find((id) => id !== "owner")] || null)} aria-label="Conversation">
            <Avatar id={(thread && (thread.members || []).find((id) => id !== "owner")) || "claude"} size="sm" />
          </button>
          <div className="titles">
            <strong>{title}</strong>
            <span>{subtitle}</span>
          </div>
        </header>
        {demo ? <div className="banner">Demo voice is on. Replies stay in character and no model is called.</div> : null}
        {error ? <div className="banner">{error}</div> : null}
        <div className="transcript" ref={scroller}>
          {messages.map((m, i) => {
            const prev = messages[i - 1];
            const showTime = !prev || new Date(m.time) - new Date(prev.time) > 5 * 60 * 1000;
            if (m.kind === "system") {
              return <div className="system" key={m.id}>{m.text}</div>;
            }
            const person = byId[m.from] || { name: m.name, color: m.color || "#8e8e93", id: m.from };
            const lastMine = m.mine && messages.slice(i + 1).every((n) => !n.mine);
            return (
              <div key={m.id}>
                {showTime ? <div className="stamp">{timeLabel(m.time)}</div> : null}
                <div className={m.mine ? "bubble-row mine" : "bubble-row"}>
                  {!m.mine ? (
                    <button className="icon-btn" onClick={() => setProfile(person)} aria-label={person.name}>
                      <Avatar id={m.from} size="sm" />
                    </button>
                  ) : null}
                  <div className={m.mine ? "bubble out" : "bubble in"}>
                    {!m.mine && thread && thread.group ? (
                      <span className="name" style={{ color: person.color || "#8e8e93" }}>{person.name}</span>
                    ) : null}
                    {m.text}
                  </div>
                </div>
                {lastMine && m.status ? <div className="receipt">{m.status}</div> : null}
              </div>
            );
          })}
          {pending ? (
            <div className="bubble-row">
              <Avatar id="operator" size="sm" />
              <div className="bubble in" aria-label="Typing">
                <span className="typing"><i /><i /><i /></span>
              </div>
            </div>
          ) : null}
        </div>
        <form className="composer" onSubmit={(e) => { e.preventDefault(); send(); }}>
          {suggestions.length ? (
            <div className="mentions">
              {suggestions.map((c) => (
                <button
                  type="button"
                  key={c.id}
                  onClick={() => setText((value) => value.replace(/@([a-z0-9-]*)$/, `@${c.id} `))}
                >
                  <Avatar id={c.id} size="sm" />
                  <span>{c.name}</span>
                  <span className={c.online ? "dot on" : "dot"} />
                </button>
              ))}
            </div>
          ) : null}
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
              <Avatar id={profile.id} size="lg" />
              <h2>{profile.name}</h2>
              <p className="role">{profile.kind === "agent" ? "Agent" : "Tool"} · {profile.role}</p>
              <p className="catch">“{profile.catchphrase}”</p>
            </div>
            <div className="facts">
              <div><h3>Look</h3><p>{profile.look}</p></div>
              <div><h3>Voice</h3><p>{profile.voice}</p></div>
              <div><h3>Quirk</h3><p>{profile.quirk}</p></div>
              <div><h3>With the others</h3><p>{profile.talks}</p></div>
              <div><h3>Expertise</h3><p>{profile.expertise}</p></div>
            </div>
            <p className="presence">{profile.online ? "Online" : "Offline"} — {profile.note || profile.enable}</p>
            {profile.id !== "owner" ? (
              <button className="text-btn" onClick={() => openDM(profile.id)}>Message {profile.name}</button>
            ) : null}
          </article>
        </div>
      ) : null}
    </div>
  );
}
