"use client";

import { useEffect, useRef, useState } from "react";
import { PROVIDERS, guessProvider } from "../lib/secrets/detector.js";

const ENVS = ["dev", "staging", "prod"];

function envName(provider) {
  return provider && provider !== "generic" && provider !== "private_key" ? `${provider.toUpperCase()}_API_KEY` : "";
}

// Redacted secret card shown in the transcript. It never holds a value.
export function SecretCard({ card }) {
  if (!card) return null;
  const status = card.status || "unverified";
  return (
    <div className="secret-card" role="group" aria-label={`Secret ${card.name}`}>
      <div className="secret-card-head">
        <span aria-hidden="true">🔐</span>
        <strong>{card.name}</strong>
        <span className={`secret-status ${status}`}>{status}</span>
      </div>
      <div className="secret-card-meta">
        {card.providerLabel || card.provider}{card.last4 ? ` · …${card.last4}` : ""}
        {card.sourceUrl ? (
          <> · <a href={card.sourceUrl} target="_blank" rel="noopener noreferrer nofollow">{card.sourceName || new URL(card.sourceUrl).host}</a></>
        ) : card.sourceName ? ` · ${card.sourceName}` : ""}
      </div>
      {(card.scopes || []).length || (card.environments || []).length || (card.agents || []).length ? (
        <div className="secret-card-tags">
          {(card.environments || []).map((e) => <span key={`e-${e}`} className="tag env">{e}</span>)}
          {(card.scopes || []).map((s) => <span key={`s-${s}`} className="tag">{s}</span>)}
          {(card.agents || []).map((a) => <span key={`a-${a}`} className="tag agent">@{a}</span>)}
        </div>
      ) : null}
    </div>
  );
}

// "Add secret" glass sheet. The value lives only in this component's state
// and is cleared on close, on success and on failure.
export function SecretSheet({ draft, agents = [], thread = "guild", onClose, onSaved }) {
  const [value, setValue] = useState(draft?.value || "");
  const [name, setName] = useState(draft?.name || envName(draft?.provider));
  const [provider, setProvider] = useState(draft?.provider || "");
  const [sourceName, setSourceName] = useState("");
  const [sourceUrl, setSourceUrl] = useState("");
  const [scopes, setScopes] = useState("");
  const [envs, setEnvs] = useState(["dev"]);
  const [allowed, setAllowed] = useState([]);
  const [ttlHours, setTtlHours] = useState(24);
  const [reveal, setReveal] = useState(false);
  const [expires, setExpires] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const first = useRef(null);

  useEffect(() => {
    first.current?.focus();
    return () => setValue("");
  }, []);

  const detected = value ? guessProvider(value) : "";
  const close = () => { setValue(""); onClose(); };
  const toggle = (list, setList, id) => setList(list.includes(id) ? list.filter((x) => x !== id) : [...list, id]);

  async function submit(e) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setErr("");
    try {
      const res = await fetch("/api/guild/secrets", {
        method: "POST",
        credentials: "same-origin",
        cache: "no-store",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          thread,
          name: name.trim(),
          value,
          provider: provider || undefined,
          sourceName: sourceName.trim(),
          sourceUrl: sourceUrl.trim(),
          scopes: scopes.split(",").map((s) => s.trim()).filter(Boolean),
          environments: envs,
          expiresAt: expires ? new Date(`${expires}T23:59:59`).toISOString() : undefined,
          allowedAgents: allowed.map((agentId) => ({ agentId, scopes: scopes.split(",").map((s) => s.trim()).filter(Boolean), ttlSeconds: Math.round(Number(ttlHours) * 3600), reveal })),
        }),
      });
      const data = await res.json().catch(() => ({}));
      setValue("");
      if (!res.ok) throw new Error(data.error || "The vault refused the secret.");
      onSaved?.(data.secret);
      onClose();
    } catch (error) {
      setErr(`${error.message} The value was cleared; paste it again to retry.`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="sheet" onClick={close}>
      <form className="card form-stack glass-sheet secret-sheet" onClick={(e) => e.stopPropagation()} onSubmit={submit} autoComplete="off" aria-labelledby="secret-sheet-title">
        <div className="grabber" />
        <div className="sheet-head">
          <h2 id="secret-sheet-title">Add Secret</h2>
          <button type="button" className="ios-text-btn" onClick={close}>Cancel</button>
        </div>
        <p className="presence" style={{ marginTop: 0 }}>
          Encrypted on the server and never shown in the chat. The chat gets a redacted card. Agents get access only if you pick them below.
        </p>
        {draft?.intercepted ? <div className="err-banner" role="status">That message looked like it held a secret, so it wasn't sent.</div> : null}
        <label><span>Secret value</span>
          <input ref={first} className="field-input" type="password" name="secret-value" autoComplete="new-password" spellCheck={false} autoCapitalize="off" autoCorrect="off" required value={value} onChange={(e) => setValue(e.target.value)} />
        </label>
        <div className="two-col">
          <label><span>Name (env var)</span><input className="field-input" required pattern="[A-Za-z_][A-Za-z0-9_]{0,63}" value={name} onChange={(e) => setName(e.target.value.toUpperCase())} placeholder="OPENAI_API_KEY" /></label>
          <label><span>Provider</span>
            <select className="field-input" value={provider} onChange={(e) => setProvider(e.target.value)}>
              <option value="">Detect{detected ? ` (${PROVIDERS[detected]})` : ""}</option>
              {Object.entries(PROVIDERS).map(([id, label]) => <option key={id} value={id}>{label}</option>)}
            </select>
          </label>
        </div>
        <div className="two-col">
          <label><span>Where it's from</span><input className="field-input" maxLength={100} value={sourceName} onChange={(e) => setSourceName(e.target.value)} placeholder="OpenAI dashboard" /></label>
          <label><span>Source link (https)</span><input className="field-input" type="url" pattern="https://.*" maxLength={500} value={sourceUrl} onChange={(e) => setSourceUrl(e.target.value)} placeholder="https://platform.openai.com/api-keys" /></label>
        </div>
        <label><span>Scopes (comma separated)</span><input className="field-input" value={scopes} onChange={(e) => setScopes(e.target.value)} placeholder="models:read, chat" /></label>
        <fieldset className="secret-fieldset">
          <legend>Environments</legend>
          {ENVS.map((env) => (
            <label key={env} className="check"><input type="checkbox" checked={envs.includes(env)} onChange={() => toggle(envs, setEnvs, env)} /> {env}</label>
          ))}
        </fieldset>
        <fieldset className="secret-fieldset">
          <legend>Allowed agents (none by default)</legend>
          {agents.length ? agents.map((a) => (
            <label key={a.id} className="check"><input type="checkbox" checked={allowed.includes(a.id)} onChange={() => toggle(allowed, setAllowed, a.id)} /> {a.name}</label>
          )) : <span className="presence">No agents in this chat yet.</span>}
        </fieldset>
        {allowed.length ? (
          <div className="two-col">
            <label><span>Agent access (hours)</span><input className="field-input" type="number" min={1} max={720} value={ttlHours} onChange={(e) => setTtlHours(e.target.value)} /></label>
            <label className="check"><input type="checkbox" checked={reveal} onChange={(e) => setReveal(e.target.checked)} /> Let agents read the raw value</label>
          </div>
        ) : null}
        <label><span>Secret expires (optional)</span><input className="field-input" type="date" value={expires} onChange={(e) => setExpires(e.target.value)} /></label>
        {err ? <div className="err-banner" role="alert">{err}</div> : null}
        <button type="submit" className="pill-btn primary full" disabled={busy || !value || !name}>{busy ? "Encrypting..." : "Save to Vault"}</button>
      </form>
    </div>
  );
}
