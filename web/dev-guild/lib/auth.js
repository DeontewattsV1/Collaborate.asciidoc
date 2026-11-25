import { createCipheriv, createDecipheriv, createHmac, randomBytes, scryptSync, timingSafeEqual } from "crypto";

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

export function sealOwner(token) {
  return createHmac("sha256", deriveKey(token)).update("dev-guild-owner").digest("hex");
}

export function isOwnerCookie(got, token) {
  if (!token || !got) return false;
  return safeEqual(got, sealOwner(token));
}

export function inviteCodeFor(token) {
  if (!token) return "";
  return "inv_" + createHmac("sha256", deriveKey(token)).update("dev-guild-invite-v1").digest("hex").slice(0, 20);
}

export function isValidInviteCode(code, token) {
  if (!token || !code) return false;
  return safeEqual(String(code).trim(), inviteCodeFor(token));
}

function signPayload(payload, purpose, token) {
  const data = Buffer.from(JSON.stringify(payload)).toString("base64url");
  const sig = createHmac("sha256", deriveKey(token)).update(`${purpose}:${data}`).digest("base64url");
  return `${data}.${sig}`;
}

function verifyPayload(raw, purpose, token) {
  if (!token || !raw || !String(raw).includes(".")) return null;
  const [data, sig] = String(raw).split(".");
  if (!data || !sig) return null;
  const want = createHmac("sha256", deriveKey(token)).update(`${purpose}:${data}`).digest("base64url");
  if (!safeEqual(sig, want)) return null;
  try {
    return JSON.parse(Buffer.from(data, "base64url").toString("utf8"));
  } catch {
    return null;
  }
}

export function sealInviteSession(participant, token) {
  return signPayload(participant, "dev-guild-invite-session", token);
}

export function readInviteSession(raw, token) {
  const data = verifyPayload(raw, "dev-guild-invite-session", token);
  if (!data || !isValidInviteCode(data.code, token)) return null;
  return data;
}

export function cookieOptions(maxAge = 60 * 60 * 24 * 30) {
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
  if (!token) return { ok: false, status: 503, error: "The server has no access passphrase configured." };
  if (isOwnerCookie(jar?.get("guild_session")?.value || "", token)) {
    return { ok: true, role: "owner", participant: { id: "owner", name: getOwnerName(env) } };
  }
  const inviteCookie = readInviteSession(jar?.get("guild_invite")?.value || "", token);
  if (inviteCookie) {
    return { ok: true, role: "invite", participant: inviteCookie };
  }
  const bearer = String(authHeader || "").replace(/^Bearer\s+/i, "").trim();
  const candidate = inviteParam || bearer;
  if (candidate && isValidInviteCode(candidate, token)) {
    return { ok: true, role: "invite-token", participant: { id: "guest", name: "Invited Agent" } };
  }
  return { ok: false, status: 401, error: "Sign in again." };
}
