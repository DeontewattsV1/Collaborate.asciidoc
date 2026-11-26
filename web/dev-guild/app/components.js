"use client";

import { useEffect, useState } from "react";

export const REPO_DEPLOY_URL =
  "https://vercel.com/new/clone?repository-url=https%3A%2F%2Fgithub.com%2FDeontewattsV1%2FCollaborate.asciidoc&root-directory=web%2Fdev-guild&env=GOOGLE_GENERATIVE_AI_API_KEY,GUILD_OWNER_TOKEN&envDescription=Free%20Gemini%20API%20key%20from%20Google%20AI%20Studio%20and%20your%20private%20group%20chat%20passphrase.&envLink=https%3A%2F%2Fgithub.com%2FDeontewattsV1%2FCollaborate.asciidoc%2Fblob%2Fmain%2Fdocs%2FDEV-GUILD.md&project-name=dev-guild&repository-name=dev-guild";

export function iosClock() {
  const d = new Date();
  let h = d.getHours() % 12;
  if (h === 0) h = 12;
  const m = String(d.getMinutes()).padStart(2, "0");
  return `${h}:${m}`;
}

export function timeLabel(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const now = new Date();
  const same = d.toDateString() === now.toDateString();
  const clock = d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  return same ? clock : `${d.toLocaleDateString([], { weekday: "short" })} ${clock}`;
}

export function Avatar({ avatar, emoji, color, name, size = "" }) {
  if (avatar) {
    const cls = size === "lg" ? "avatar-lg" : `avatar ${size}`;
    const src = avatar.startsWith("/") ? avatar : `/avatars/${avatar}`;
    return <img className={cls} alt="" src={src} loading="lazy" />;
  }
  const sz = size === "lg" ? "lg" : size === "sm" ? "sm" : size === "xs" ? "xs" : "md";
  const bg = color || "#5E5CE6";
  const glyph = emoji || (name ? name.slice(0, 2).toUpperCase() : "AI");
  return (
    <span
      className={`avatar-badge ${sz}`}
      style={{ background: `linear-gradient(145deg, ${bg}, #1c1c1e)` }}
      aria-hidden="true"
    >
      {glyph}
    </span>
  );
}

export function PhoneFrame({ children }) {
  const [clock, setClock] = useState("2:41");
  useEffect(() => {
    setClock(iosClock());
    const id = setInterval(() => setClock(iosClock()), 15000);
    return () => clearInterval(id);
  }, []);

  return (
    <div className="iphone-stage">
      <div className="iphone-shell">
        <span className="hw-btn hw-action" aria-hidden="true" />
        <span className="hw-btn hw-vol-up" aria-hidden="true" />
        <span className="hw-btn hw-vol-down" aria-hidden="true" />
        <span className="hw-btn hw-power" aria-hidden="true" />
        <div className="iphone-screen">
          <div className="ios-statusbar" aria-hidden="true">
            <span className="ios-time">{clock}</span>
            <div className="dynamic-island">
              <span className="island-cam" />
            </div>
            <div className="ios-icons">
              <svg width="16" height="11" viewBox="0 0 16 11" fill="currentColor">
                <rect x="0" y="7" width="2.8" height="4" rx="0.8" />
                <rect x="4.2" y="5" width="2.8" height="6" rx="0.8" />
                <rect x="8.4" y="2.6" width="2.8" height="8.4" rx="0.8" />
                <rect x="12.6" y="0" width="2.8" height="11" rx="0.8" />
              </svg>
              <svg width="14" height="11" viewBox="0 0 16 12" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round">
                <path d="M1.5 4.2C5.2 0.8 10.8 0.8 14.5 4.2" />
                <path d="M4.2 7.1C6.4 5.1 9.6 5.1 11.8 7.1" />
                <circle cx="8" cy="10" r="1.2" fill="currentColor" stroke="none" />
              </svg>
              <svg width="24" height="11" viewBox="0 0 25 12" fill="none">
                <rect x="0.8" y="1" width="20" height="10" rx="2.8" stroke="currentColor" strokeOpacity="0.45" strokeWidth="1.2" />
                <rect x="2.4" y="2.6" width="16.8" height="6.8" rx="1.5" fill="currentColor" />
                <path d="M22.4 4.2v3.6c.8-.3 1.3-1 1.3-1.8s-.5-1.5-1.3-1.8z" fill="currentColor" fillOpacity="0.55" />
              </svg>
            </div>
          </div>
          <div className="ios-viewport">{children}</div>
          <div className="ios-home-bar" aria-hidden="true">
            <span className="ios-home-pill" />
          </div>
        </div>
      </div>
    </div>
  );
}

export function ProfileSheet({ profile, demoMode, groupMemberSet, onClose, onOpenDM, onToggleMember }) {
  if (!profile || !profile.kind) return null;
  return (
    <div className="sheet" onClick={onClose}>
      <article className="card" onClick={(e) => e.stopPropagation()}>
        <div className="grabber" />
        <div className="hero">
          <Avatar avatar={profile.avatar} emoji={profile.emoji} color={profile.color} name={profile.name} size="lg" />
          <h2 style={{ margin: "4px 0 0" }}>{profile.name}</h2>
          <p className="role">{profile.section || profile.kind} · {profile.role}</p>
          {profile.isPersona ? (
            <span className="badge persona">Persona voiced by Free Gemini · Not the official product</span>
          ) : null}
          <p className="catch">“{profile.catchphrase}”</p>
        </div>
        <div className="facts">
          <div><h3>Look</h3><p>{profile.look}</p></div>
          <div><h3>Voice</h3><p>{profile.voice}</p></div>
          <div><h3>Quirk</h3><p>{profile.quirk}</p></div>
          <div><h3>Expertise</h3><p>{profile.expertise}</p></div>
          {profile.join ? (
            <div>
              <h3>How it joins</h3>
              <p>
                {profile.join.label}
                {profile.join.mode === "custom-agent" ? ` · needs ${profile.join.envKey}` : ""}
                {profile.join.mode === "invite" ? " · invite API" : ""}
              </p>
            </div>
          ) : null}
        </div>
        <p className="presence">{profile.online ? "Active" : "Needs key"} — {profile.note || profile.enable}</p>
        {profile.id !== "owner" ? (
          <div className="sheet-actions">
            <button type="button" className="pill-btn primary" onClick={() => onOpenDM(profile.id)}>Message {profile.name}</button>
            {!demoMode ? (
              <button type="button" className="pill-btn" onClick={() => onToggleMember(profile.id)}>
                {groupMemberSet.has(profile.id) ? "Remove from group" : "Add to group"}
              </button>
            ) : null}
          </div>
        ) : null}
      </article>
    </div>
  );
}

export function DemoSheet({ characters, onClose, onOpenDM }) {
  return (
    <div className="sheet" onClick={onClose}>
      <article className="card form-stack" onClick={(e) => e.stopPropagation()}>
        <div className="grabber" />
        <div className="sheet-head">
          <h2>Public Live Demo</h2>
          <button type="button" className="ios-text-btn" onClick={onClose}>Done</button>
        </div>
        <p className="presence" style={{ marginTop: 0 }}>
          Chatting with {characters.length} guild characters powered by free{" "}
          <code>gemini-3.5-flash-lite</code>. Turns stay in your browser session and never persist across visitors.
        </p>
        <div className="member-list">
          {characters.map((c) => (
            <div className="member-item" key={c.id}>
              <Avatar avatar={c.avatar} emoji={c.emoji} color={c.color} name={c.name} size="sm" />
              <div className="member-meta">
                <strong>{c.name}</strong>
                <span>@{c.id} · {c.role}</span>
              </div>
              <button type="button" className="pill-btn primary" onClick={() => { onClose(); onOpenDM(c.id); }}>
                DM
              </button>
            </div>
          ))}
        </div>
        <a className="pill-btn primary full" style={{ textAlign: "center", textDecoration: "none" }} href={REPO_DEPLOY_URL} target="_blank" rel="noreferrer">
          Deploy Your Own Free Copy (500+ Contacts &amp; Invites)
        </a>
      </article>
    </div>
  );
}
