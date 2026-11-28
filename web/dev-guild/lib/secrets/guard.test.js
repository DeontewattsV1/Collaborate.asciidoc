import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { GuardError, assertSameOrigin, installLogRedaction, rateLimit, readJsonLimited, redactArg, resetRateLimitsForTest } from "./guard.js";

const fakeKey = `ghp_${createHash("sha256").update("guard-fixture").digest("base64").replace(/[^A-Za-z0-9]/g, "").slice(0, 36)}`;
const req = (method, headers = {}, body) => new Request("https://guild.example/api/guild/secrets", { method, headers, body });

test("cookie-authenticated writes need a same-origin Origin header", () => {
  assert.doesNotThrow(() => assertSameOrigin(req("POST", { origin: "https://guild.example" }), { env: {} }));
  assert.throws(() => assertSameOrigin(req("POST", {}), { env: {} }), GuardError);
  assert.throws(() => assertSameOrigin(req("POST", { origin: "https://evil.example" }), { env: {} }), GuardError);
  assert.throws(() => assertSameOrigin(req("POST", { origin: "https://guild.example", "sec-fetch-site": "cross-site" }), { env: {} }), GuardError);
  assert.doesNotThrow(() => assertSameOrigin(req("POST", {}), { env: {}, cookieAuth: false }));
  assert.doesNotThrow(() => assertSameOrigin(req("GET", {}), { env: {} }));
});

test("rate limit blocks after the budget and reports Retry-After", () => {
  resetRateLimitsForTest();
  for (let i = 0; i < 3; i += 1) rateLimit("k", { limit: 3, now: 1000 + i });
  assert.throws(() => rateLimit("k", { limit: 3, now: 1010 }), (err) => err.status === 429 && Boolean(err.headers["Retry-After"]));
  assert.doesNotThrow(() => rateLimit("k", { limit: 3, now: 70_000 }));
});

test("bodies are size-limited", async () => {
  await assert.rejects(readJsonLimited(req("POST", {}, JSON.stringify({ v: "x".repeat(40_000) }))), (err) => err.status === 413);
  assert.deepEqual(await readJsonLimited(req("POST", {}, "{\"a\":1}")), { a: 1 });
  await assert.rejects(readJsonLimited(req("POST", {}, "{bad")), (err) => err.status === 400);
});

test("log redaction scrubs strings, errors and objects", () => {
  const lines = [];
  const fake = { log: (...a) => lines.push(a), error: (...a) => lines.push(a) };
  installLogRedaction(fake);
  fake.log("token", fakeKey, { auth: fakeKey });
  fake.error(new Error(`bad key ${fakeKey}`));
  const flat = JSON.stringify(lines.map((a) => a.map((x) => (x instanceof Error ? x.message : x))));
  assert.ok(!flat.includes(fakeKey));
  assert.ok(flat.includes("GitHub"));
  assert.equal(redactArg(42), 42);
});
