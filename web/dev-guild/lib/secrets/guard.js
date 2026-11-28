import { redactSecrets } from "./detector.js";

// HTTP hardening shared by the secret routes: same-origin checks for
// cookie-authenticated calls, a small in-memory rate limiter, bounded JSON
// bodies and log redaction.

export const MAX_BODY_BYTES = 32 * 1024;

export class GuardError extends Error {
  constructor(status, message, headers = {}) {
    super(message);
    this.name = "GuardError";
    this.status = status;
    this.headers = headers;
  }
}

// Cookie-authenticated, state-changing requests must come from our own origin.
// Bearer-token requests are not exposed to CSRF, but a present Origin header
// must still match. Sec-Fetch-Site=cross-site is always refused.
export function assertSameOrigin(request, { env = process.env, cookieAuth = true } = {}) {
  const method = request.method.toUpperCase();
  if (method === "GET" || method === "HEAD") return;
  const site = request.headers.get("sec-fetch-site");
  if (site && site !== "same-origin" && site !== "none") throw new GuardError(403, "cross-site request refused");
  const origin = request.headers.get("origin");
  const allowed = new Set([new URL(request.url).origin]);
  const host = request.headers.get("x-forwarded-host") || request.headers.get("host");
  const proto = request.headers.get("x-forwarded-proto") || new URL(request.url).protocol.replace(":", "");
  if (host) allowed.add(`${proto}://${host}`);
  for (const o of String(env.GUILD_ALLOWED_ORIGINS || "").split(",").map((s) => s.trim()).filter(Boolean)) allowed.add(o);
  if (origin) {
    if (!allowed.has(origin)) throw new GuardError(403, "cross-origin request refused");
  } else if (cookieAuth) {
    throw new GuardError(403, "origin header required");
  }
}

const buckets = globalThis.__secretRateBuckets || (globalThis.__secretRateBuckets = new Map());

// rateLimit allows `limit` calls per `windowMs` per key (sliding window).
export function rateLimit(key, { limit = 30, windowMs = 60_000, now = Date.now() } = {}) {
  const hits = (buckets.get(key) || []).filter((t) => t > now - windowMs);
  if (hits.length >= limit) {
    const retry = Math.max(1, Math.ceil((hits[0] + windowMs - now) / 1000));
    buckets.set(key, hits);
    throw new GuardError(429, "too many secret requests; slow down", { "Retry-After": String(retry) });
  }
  hits.push(now);
  buckets.set(key, hits);
  if (buckets.size > 10_000) buckets.delete(buckets.keys().next().value);
}

export function resetRateLimitsForTest() {
  buckets.clear();
}

export function clientKey(request) {
  const fwd = request.headers.get("fly-client-ip") || request.headers.get("x-forwarded-for") || "";
  return fwd.split(",")[0].trim() || "local";
}

export async function readJsonLimited(request, max = MAX_BODY_BYTES) {
  const declared = Number(request.headers.get("content-length") || 0);
  if (declared > max) throw new GuardError(413, "request too large");
  const raw = await request.text();
  if (Buffer.byteLength(raw, "utf8") > max) throw new GuardError(413, "request too large");
  try {
    const body = JSON.parse(raw || "{}");
    return body && typeof body === "object" && !Array.isArray(body) ? body : {};
  } catch {
    throw new GuardError(400, "bad json");
  }
}

export const NO_STORE = Object.freeze({ "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer" });

// redactArg makes any console argument safe: strings and Error messages are
// scrubbed, objects are serialized then scrubbed.
export function redactArg(arg) {
  if (typeof arg === "string") return redactSecrets(arg).text;
  if (arg instanceof Error) {
    const e = new Error(redactSecrets(arg.message).text);
    e.name = arg.name;
    e.stack = redactSecrets(String(arg.stack || "")).text;
    if (arg.code) e.code = arg.code;
    return e;
  }
  if (arg && typeof arg === "object") {
    try {
      const s = JSON.stringify(arg);
      const r = redactSecrets(s);
      return r.redacted ? r.text : arg;
    } catch {
      return "[unserializable]";
    }
  }
  return arg;
}

// installLogRedaction wraps console methods once per process.
export function installLogRedaction(target = console) {
  if (target.__secretRedaction) return false;
  for (const m of ["log", "info", "warn", "error", "debug", "trace"]) {
    const orig = target[m];
    if (typeof orig !== "function") continue;
    target[m] = (...args) => orig.apply(target, args.map(redactArg));
  }
  Object.defineProperty(target, "__secretRedaction", { value: true });
  return true;
}
