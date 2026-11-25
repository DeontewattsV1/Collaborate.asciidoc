import crypto from "node:crypto";
import { FLASH_LITE } from "./models.js";

export const DEMO_AGENT_IDS = ["claude", "grok", "chatgpt", "gemini", "tay"];
export const DEMO_RATE_LIMIT = 10;
export const DEMO_WINDOW_MS = 60 * 60 * 1000;
export const DEMO_MAX_OUTPUT_TOKENS = 200;
export const DEMO_MAX_INPUT_CHARS = 500;
export const DEMO_MAX_TURNS = 2;

export const DEMO_AUTO_IP_LIMIT = 30;
export const DEMO_AUTO_DAILY_LIMIT = 200;
export const DEMO_AUTO_OUTPUT_TOKENS = 140;

export const DEMO_TOPICS = [
  "keeping multi-agent PRs conflict-free with Collaborate-Claim paths",
  "why agentterm writes a SHA-256 hash-chained log for every guild message",
  "routing Dev Guild replies through free Gemini models with zero credit card",
  "making an open-source multi-agent group chat feel like native iOS Messages",
  "keeping diffs narrow so go-verify and scripts/check.sh stay green",
];

export function isDemoMode(env) {
  const source = env || process.env;
  return String(source.GUILD_DEMO || "").trim() === "1";
}

export function isLiveDemo(env) {
  const source = env || process.env;
  return isDemoMode(source) && Boolean(String(source.GOOGLE_GENERATIVE_AI_API_KEY || "").trim());
}

export function clientIpFromRequest(request) {
  if (!request || !request.headers) return "127.0.0.1";
  const realIp = String(request.headers.get("x-real-ip") || "").trim();
  if (realIp) return realIp.slice(0, 64);
  const xff = String(request.headers.get("x-forwarded-for") || "").trim();
  if (xff) {
    const first = xff.split(",")[0].trim();
    if (first) return first.slice(0, 64);
  }
  return "127.0.0.1";
}

function demoSigningKey(env) {
  const source = env || process.env;
  const seed = String(source.DEMO_SIGNING_SECRET || source.GOOGLE_GENERATIVE_AI_API_KEY || "dev-guild-demo-local");
  return crypto.createHash("sha256").update(`dev-guild-demo-rl:${seed}`).digest();
}

export function readDemoCookieTimestamps(cookieVal, { windowMs = DEMO_WINDOW_MS, now = Date.now(), env } = {}) {
  if (!cookieVal || typeof cookieVal !== "string" || !cookieVal.includes(".")) return [];
  const [payloadB64, sig] = cookieVal.split(".");
  if (!payloadB64 || !sig) return [];
  const expected = crypto.createHmac("sha256", demoSigningKey(env)).update(payloadB64).digest("base64url");
  if (sig.length !== expected.length) return [];
  try {
    if (!crypto.timingSafeEqual(Buffer.from(sig), Buffer.from(expected))) return [];
    const arr = JSON.parse(Buffer.from(payloadB64, "base64url").toString("utf8"));
    if (!Array.isArray(arr)) return [];
    const cutoff = now - windowMs;
    return arr.filter((n) => typeof n === "number" && Number.isFinite(n) && n > cutoff).slice(-50);
  } catch {
    return [];
  }
}

export function sealDemoCookieTimestamps(timestamps, { env } = {}) {
  const payloadB64 = Buffer.from(JSON.stringify((timestamps || []).slice(-50)), "utf8").toString("base64url");
  const sig = crypto.createHmac("sha256", demoSigningKey(env)).update(payloadB64).digest("base64url");
  return `${payloadB64}.${sig}`;
}

export function demoCookieHeader(cookieValue) {
  const prod = process.env.NODE_ENV === "production" ? "; Secure" : "";
  return `guild_demo_rl=${cookieValue}; Path=/; HttpOnly; SameSite=Lax; Max-Age=3600${prod}`;
}

function getBucketStore() {
  if (!globalThis.__devGuildDemoRateLimit) {
    globalThis.__devGuildDemoRateLimit = new Map();
  }
  return globalThis.__devGuildDemoRateLimit;
}

function getAutoStore() {
  if (!globalThis.__devGuildDemoAutoLimit) {
    globalThis.__devGuildDemoAutoLimit = { day: "", globalCount: 0, byIp: new Map() };
  }
  return globalThis.__devGuildDemoAutoLimit;
}

export function resetDemoRateLimitForTest() {
  globalThis.__devGuildDemoRateLimit = new Map();
  globalThis.__devGuildDemoAutoLimit = { day: "", globalCount: 0, byIp: new Map() };
}

function mergeTimestamps(a, b, cutoff) {
  const combined = a.length >= b.length ? [...a] : [...b];
  const seen = new Set(combined);
  for (const ts of a.length >= b.length ? b : a) {
    if (!seen.has(ts)) {
      seen.add(ts);
      combined.push(ts);
    }
  }
  return combined.filter((ts) => ts > cutoff).sort((x, y) => x - y);
}

export function peekDemoRateLimit(ip, { limit = DEMO_RATE_LIMIT, windowMs = DEMO_WINDOW_MS, now = Date.now(), cookieVal, env } = {}) {
  const store = getBucketStore();
  const key = String(ip || "127.0.0.1");
  const cutoff = now - windowMs;
  const raw = store.get(key) || [];
  const ipActive = raw.filter((ts) => ts > cutoff);
  if (ipActive.length !== raw.length) {
    if (ipActive.length) store.set(key, ipActive);
    else store.delete(key);
  }
  const cookieActive = readDemoCookieTimestamps(cookieVal, { windowMs, now, env });
  const active = cookieActive.length > ipActive.length ? cookieActive : ipActive;
  const remaining = Math.max(0, limit - active.length);
  const oldest = active.length ? active[0] : now;
  const retryAfterSeconds = active.length >= limit ? Math.max(1, Math.ceil((oldest + windowMs - now) / 1000)) : 0;
  return { allowed: active.length < limit, limit, remaining, used: active.length, retryAfterSeconds };
}

export function checkDemoRateLimit(ip, { limit = DEMO_RATE_LIMIT, windowMs = DEMO_WINDOW_MS, now = Date.now(), cookieVal, env } = {}) {
  const store = getBucketStore();
  const key = String(ip || "127.0.0.1");
  const cutoff = now - windowMs;
  const ipActive = (store.get(key) || []).filter((ts) => ts > cutoff);
  const cookieActive = readDemoCookieTimestamps(cookieVal, { windowMs, now, env });
  const active = ipActive.length >= cookieActive.length ? ipActive : cookieActive;
  if (store.size > 1000) {
    for (const [k, list] of store.entries()) {
      if (!list.some((ts) => ts > cutoff)) store.delete(k);
    }
  }
  if (active.length >= limit) {
    store.set(key, active);
    const retryAfterSeconds = Math.max(1, Math.ceil((active[0] + windowMs - now) / 1000));
    return {
      allowed: false,
      limit,
      remaining: 0,
      used: active.length,
      retryAfterSeconds,
      cookieValue: sealDemoCookieTimestamps(active, { env }),
    };
  }
  const updated = [...active, now].filter((ts) => ts > cutoff);
  store.set(key, updated);
  return {
    allowed: true,
    limit,
    remaining: Math.max(0, limit - updated.length),
    used: updated.length,
    retryAfterSeconds: 0,
    cookieValue: sealDemoCookieTimestamps(updated, { env }),
  };
}

export function checkDemoAutoLimit(
  ip,
  { ipLimit = DEMO_AUTO_IP_LIMIT, dailyLimit = DEMO_AUTO_DAILY_LIMIT, windowMs = DEMO_WINDOW_MS, now = Date.now() } = {},
) {
  const store = getAutoStore();
  const dayKey = new Date(now).toISOString().slice(0, 10);
  if (store.day !== dayKey) {
    store.day = dayKey;
    store.globalCount = 0;
  }
  if (store.globalCount >= dailyLimit) {
    return { allowed: false, reason: "daily", remainingGlobal: 0, remainingIp: 0, retryAfterSeconds: 3600 };
  }
  const key = String(ip || "127.0.0.1");
  const cutoff = now - windowMs;
  const active = (store.byIp.get(key) || []).filter((ts) => ts > cutoff);
  if (active.length >= ipLimit) {
    store.byIp.set(key, active);
    const retryAfterSeconds = Math.max(1, Math.ceil((active[0] + windowMs - now) / 1000));
    return { allowed: false, reason: "ip", remainingGlobal: dailyLimit - store.globalCount, remainingIp: 0, retryAfterSeconds };
  }
  active.push(now);
  store.byIp.set(key, active);
  store.globalCount += 1;
  return {
    allowed: true,
    remainingGlobal: dailyLimit - store.globalCount,
    remainingIp: Math.max(0, ipLimit - active.length),
    retryAfterSeconds: 0,
  };
}

export function demoAttempts(env) {
  const source = env || process.env;
  if (!String(source.GOOGLE_GENERATIVE_AI_API_KEY || "").trim()) return [];
  return [{ provider: "google", modelId: FLASH_LITE }];
}
