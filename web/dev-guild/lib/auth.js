import { createCipheriv, createDecipheriv, createHmac, randomBytes, scryptSync, timingSafeEqual } from "crypto";
import { mkdirSync, readFileSync, renameSync, statSync, writeFileSync } from "fs";
import { dirname } from "path";
import { isDemoMode } from "./demo.js";

const keyCache = new Map();

function deriveKey(secret) {
  const s = String(secret || "");
  if (!s) return Buffer.alloc(0);
  let cached = keyCache.get(s);
  if (!cached) {
    cached = scryptSync(s, "dev-guild-kdf-v1", 32);
    if (keyCache.size > 32) keyCache.clear();
    keyCache.set(s, cached);
  }
  return cached;
}

export function getOwnerToken(env) {
  const source = env || process.env;
  return String(source.GUILD_OWNER_TOKEN || source.GUILD_ACCESS_CODE || "").trim();
}

export function getOwnerName(env) {
  const source = env || process.env;
  return String(source.GUILD_OWNER_NAME || "").trim().slice(0, 40) || "Owner";
}

export function getGroupName(env) {
  const source = env || process.env;
  return String(source.GUILD_GROUP_NAME || "").trim().slice(0, 60) || "Dev Guild";
}

function safeEqual(aStr, bStr) {
  const a = Buffer.from(String(aStr || ""));
  const b = Buffer.from(String(bStr || ""));
  return a.length > 0 && a.length === b.length && timingSafeEqual(a, b);
}

// Owner sessions (v2). The cookie is an AES-GCM sealed {sid, iat, exp, ep}:
// - exp is enforced on the server (OWNER_SESSION_SECONDS), not just by the
//   browser's cookie lifetime;
// - ep must match GUILD_SESSION_EPOCH (default "1"); changing that secret
//   revokes every owner and invite session and rotates the invite code;
// - logout revokes the session id, and "log out everywhere" sets a
//   not-before time. Both are kept in GUILD_SESSION_FILE (on Fly, the /data
//   volume) so revocations survive restarts.
// Pre-v2 cookies (a bare HMAC that never expired) are no longer accepted;
// the owner just signs in again with the same owner token.
export const OWNER_SESSION_SECONDS = 14 * 24 * 3600;
export const INVITE_SESSION_SECONDS = 30 * 24 * 3600;
const OWNER_PURPOSE = "dev-guild-owner-session-v2";

export function sessionEpoch(env) {
  const source = env || process.env;
  return String(source.GUILD_SESSION_EPOCH || "1").trim().slice(0, 64) || "1";
}

function sessionFile(env) {
  const source = env || process.env;
  return String(source.GUILD_SESSION_FILE || "/tmp/dev-guild-sessions.json").trim();
}

let revocationCache = { file: "", mtimeMs: -1, data: { notBefore: 0, revoked: {} } };

export function readRevocations(env) {
  const file = sessionFile(env);
  let mtimeMs = 0;
  try {
    mtimeMs = statSync(file).mtimeMs;
  } catch {
    return { notBefore: 0, revoked: {} };
  }
  if (revocationCache.file === file && revocationCache.mtimeMs === mtimeMs) return revocationCache.data;
  let data = { notBefore: 0, revoked: {} };
  try {
    const parsed = JSON.parse(readFileSync(file, "utf8"));
    data = {
      notBefore: Number(parsed.notBefore) || 0,
      revoked: parsed.revoked && typeof parsed.revoked === "object" ? parsed.revoked : {},
    };
  } catch {
    // A corrupt file fails closed for "everywhere" logouts: treat now as not-before.
    data = { notBefore: Date.now(), revoked: {} };
  }
  revocationCache = { file, mtimeMs, data };
  return data;
}

function writeRevocations(data, env) {
  const file = sessionFile(env);
  const now = Date.now();
  const revoked = {};
  for (const [sid, exp] of Object.entries(data.revoked || {})) {
    if (Number(exp) > now) revoked[sid] = Number(exp);
  }
  const clean = { notBefore: Number(data.notBefore) || 0, revoked };
  mkdirSync(dirname(file), { recursive: true });
  const tmp = `${file}.${process.pid}.tmp`;
  writeFileSync(tmp, JSON.stringify(clean), { mode: 0o600 });
  renameSync(tmp, file);
  revocationCache = { file: "", mtimeMs: -1, data: clean };
  return clean;
}

export function revokeSession(sid, exp, env) {
  if (!sid) return;
  const current = readRevocations(env);
  writeRevocations({ ...current, revoked: { ...current.revoked, [String(sid)]: Number(exp) || Date.now() + OWNER_SESSION_SECONDS * 1000 } }, env);
}

export function revokeAllSessions(env, now = Date.now()) {
  const current = readRevocations(env);
  writeRevocations({ ...current, notBefore: now }, env);
}

function sessionIsLive(data, env, now) {
  if (!data || typeof data !== "object") return false;
  const iat = Number(data.iat);
  const exp = Number(data.exp);
  if (!Number.isFinite(iat) || !Number.isFinite(exp) || exp <= now || iat > now + 60_000) return false;
  if (String(data.ep || "") !== sessionEpoch(env)) return false;
  const rev = readRevocations(env);
  if (iat < rev.notBefore) return false;
  if (data.sid && Object.hasOwn(rev.revoked, String(data.sid))) return false;
  return true;
}

export function sealOwner(token, { env, now = Date.now() } = {}) {
  if (!token) return "";
  return signPayload(
    { sid: randomBytes(16).toString("hex"), iat: now, exp: now + OWNER_SESSION_SECONDS * 1000, ep: sessionEpoch(env) },
    OWNER_PURPOSE,
    token,
  );
}

export function readOwnerSession(raw, token, { env, now = Date.now() } = {}) {
  if (!token || !raw) return null;
  const data = verifyPayload(raw, OWNER_PURPOSE, token);
  return sessionIsLive(data, env, now) ? data : null;
}

export function isOwnerCookie(got, token, opts = {}) {
  return Boolean(readOwnerSession(got, token, opts));
}

export function inviteCodeFor(token, env) {
  if (!token) return "";
  const ep = sessionEpoch(env);
  const label = ep === "1" ? "dev-guild-invite-v1" : `dev-guild-invite-v1:${ep}`;
  return "inv_" + createHmac("sha256", deriveKey(token)).update(label).digest("hex").slice(0, 20);
}

export function isValidInviteCode(code, token, env) {
  if (!token || !code) return false;
  return safeEqual(String(code).trim(), inviteCodeFor(token, env));
}

function signPayload(payload, purpose, token) {
  if (!token) return "";
  const key = deriveKey(token);
  const iv = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", key, iv);
  cipher.setAAD(Buffer.from(String(purpose || ""), "utf8"));
  const json = JSON.stringify(payload);
  const enc = Buffer.concat([cipher.update(json, "utf8"), cipher.final()]);
  const tag = cipher.getAuthTag();
  return `${iv.toString("base64url")}.${tag.toString("base64url")}.${enc.toString("base64url")}`;
}

function verifyPayload(raw, purpose, token) {
  if (!token || !raw) return null;
  const parts = String(raw).split(".");
  if (parts.length !== 3) return null;
  try {
    const [ivB, tagB, encB] = parts;
    const key = deriveKey(token);
    const decipher = createDecipheriv("aes-256-gcm", key, Buffer.from(ivB, "base64url"));
    decipher.setAAD(Buffer.from(String(purpose || ""), "utf8"));
    decipher.setAuthTag(Buffer.from(tagB, "base64url"));
    const plain = Buffer.concat([decipher.update(Buffer.from(encB, "base64url")), decipher.final()]).toString("utf8");
    return JSON.parse(plain);
  } catch {
    return null;
  }
}

export function sealInviteSession(participant, token, { env, now = Date.now() } = {}) {
  const data = { ...(participant || {}), _sid: randomBytes(12).toString("hex"), iat: now, exp: now + INVITE_SESSION_SECONDS * 1000, ep: sessionEpoch(env) };
  return signPayload(data, "dev-guild-invite-session-v2", token);
}

export function readInviteSession(raw, token, { env, now = Date.now() } = {}) {
  const data = verifyPayload(raw, "dev-guild-invite-session-v2", token);
  if (!data || !isValidInviteCode(data.code, token, env)) return null;
  if (!sessionIsLive({ ...data, sid: data._sid }, env, now)) return null;
  const { _sid, iat, exp, ep, ...participant } = data;
  return participant;
}

// The session id and expiry of a live invite cookie (used to revoke it).
export function readInviteSessionIds(raw, token, { env, now = Date.now() } = {}) {
  const data = verifyPayload(raw, "dev-guild-invite-session-v2", token);
  if (!data || !isValidInviteCode(data.code, token, env)) return null;
  if (!sessionIsLive({ ...data, sid: data._sid }, env, now)) return null;
  return { sid: data._sid, exp: data.exp };
}

// Failed owner sign-ins are throttled per client and globally, so the owner
// token can not be brute-forced. Successful sign-ins clear the client's count.
export const LOGIN_MAX_FAILURES = 5;
export const LOGIN_GLOBAL_MAX_FAILURES = 50;
export const LOGIN_WINDOW_MS = 15 * 60 * 1000;
const loginFailures = globalThis.__guildLoginFailures || (globalThis.__guildLoginFailures = new Map());

function recentFailures(key, now) {
  const hits = (loginFailures.get(key) || []).filter((t) => t > now - LOGIN_WINDOW_MS);
  loginFailures.set(key, hits);
  return hits;
}

export function loginRetryAfter(client, now = Date.now()) {
  const mine = recentFailures(`ip:${client}`, now);
  const all = recentFailures("all", now);
  const blocked = mine.length >= LOGIN_MAX_FAILURES ? mine : all.length >= LOGIN_GLOBAL_MAX_FAILURES ? all : null;
  if (!blocked) return 0;
  return Math.max(1, Math.ceil((blocked[0] + LOGIN_WINDOW_MS - now) / 1000));
}

export function recordLoginFailure(client, now = Date.now()) {
  recentFailures(`ip:${client}`, now).push(now);
  recentFailures("all", now).push(now);
  if (loginFailures.size > 10_000) loginFailures.delete(loginFailures.keys().next().value);
}

export function clearLoginFailures(client) {
  loginFailures.delete(`ip:${client}`);
}

export function resetLoginThrottleForTest() {
  loginFailures.clear();
}

// IPv6 clients are throttled per /64: one host (or one privacy-extension
// network) can trivially rotate addresses inside its /64.
export function loginClientKey(request) {
  const h = request && request.headers;
  if (!h) return "local";
  const fwd = h.get("fly-client-ip") || h.get("x-real-ip") || h.get("x-forwarded-for") || "";
  const ip = fwd.split(",")[0].trim().slice(0, 64);
  if (!ip) return "local";
  return ip.includes(":") ? ipv6Prefix64(ip) : ip;
}

function ipv6Prefix64(ip) {
  const addr = ip.replace(/^\[|\]$/g, "").split("%")[0].toLowerCase();
  if (addr.includes(".")) return addr; // IPv4-mapped; keep as is
  const [head, tail = ""] = addr.split("::");
  const left = head ? head.split(":") : [];
  const right = tail ? tail.split(":") : [];
  const fill = addr.includes("::") ? Array(Math.max(0, 8 - left.length - right.length)).fill("0") : [];
  const groups = [...left, ...fill, ...right].map((g) => (g || "0").replace(/^0+(?=.)/, ""));
  return `${groups.slice(0, 4).join(":")}::/64`;
}

export function cookieOptions(maxAge = INVITE_SESSION_SECONDS) {
  return {
    httpOnly: true,
    sameSite: "lax",
    secure: process.env.NODE_ENV === "production",
    path: "/",
    maxAge,
  };
}

export function encryptCustomAgents(agents, token) {
  if (!token) return "";
  const key = deriveKey(token);
  const iv = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", key, iv);
  const json = JSON.stringify(agents || []);
  const enc = Buffer.concat([cipher.update(json, "utf8"), cipher.final()]);
  const tag = cipher.getAuthTag();
  return `${iv.toString("base64url")}.${tag.toString("base64url")}.${enc.toString("base64url")}`;
}

export function decryptCustomAgents(raw, token) {
  if (!token || !raw) return [];
  const parts = String(raw).split(".");
  if (parts.length !== 3) return [];
  try {
    const [ivB, tagB, encB] = parts;
    const key = deriveKey(token);
    const decipher = createDecipheriv("aes-256-gcm", key, Buffer.from(ivB, "base64url"));
    decipher.setAuthTag(Buffer.from(tagB, "base64url"));
    const plain = Buffer.concat([decipher.update(Buffer.from(encB, "base64url")), decipher.final()]).toString("utf8");
    const list = JSON.parse(plain);
    return Array.isArray(list) ? list : [];
  } catch {
    return [];
  }
}

export function sealGroupPrefs(prefs, token) {
  const clean = {
    version: Number((prefs && prefs.version) || 0) || 0,
    title: String((prefs && prefs.title) || "").trim().slice(0, 60),
    ownerName: String((prefs && prefs.ownerName) || "").trim().slice(0, 40),
    setupDone: Boolean(prefs && prefs.setupDone),
    added: Array.isArray(prefs && prefs.added) ? prefs.added.map((id) => String(id)).slice(0, 128) : [],
    removed: Array.isArray(prefs && prefs.removed) ? prefs.removed.map((id) => String(id)).slice(0, 128) : [],
  };
  return signPayload(clean, "dev-guild-group-prefs", token);
}

export function readGroupPrefs(raw, token) {
  const data = verifyPayload(raw, "dev-guild-group-prefs", token);
  if (!data || typeof data !== "object") {
    return { version: 0, title: "", ownerName: "", setupDone: false, added: [], removed: [] };
  }
  return {
    version: Number(data.version || 0) || 0,
    title: String(data.title || "").trim().slice(0, 60),
    ownerName: String(data.ownerName || "").trim().slice(0, 40),
    setupDone: Boolean(data.setupDone),
    added: Array.isArray(data.added) ? data.added.map((id) => String(id)) : [],
    removed: Array.isArray(data.removed) ? data.removed.map((id) => String(id)) : [],
  };
}

export function checkGuildAuth({ jar, token, inviteParam, authHeader, env }) {
  if (isDemoMode(env)) {
    return { ok: true, role: "demo", participant: { id: "owner", name: "You" } };
  }
  if (!token) return { ok: false, status: 503, error: "The server has no access passphrase configured." };
  if (isOwnerCookie(jar?.get("guild_session")?.value || "", token, { env })) {
    return { ok: true, role: "owner", participant: { id: "owner", name: getOwnerName(env) } };
  }
  const inviteCookie = readInviteSession(jar?.get("guild_invite")?.value || "", token, { env });
  if (inviteCookie) {
    return { ok: true, role: "invite", participant: inviteCookie };
  }
  const bearer = String(authHeader || "").replace(/^Bearer\s+/i, "").trim();
  // Scripts (see the custom-agent curl example in the guild directory) sign in
  // as the owner with `Authorization: Bearer $GUILD_OWNER_TOKEN`.
  if (bearer && safeEqual(bearer, token)) {
    return { ok: true, role: "owner", participant: { id: "owner", name: getOwnerName(env) } };
  }
  const candidate = inviteParam || bearer;
  if (candidate && isValidInviteCode(candidate, token, env)) {
    return { ok: true, role: "invite-token", participant: { id: "guest", name: "Invited Agent" } };
  }
  return { ok: false, status: 401, error: "Sign in again." };
}
