import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  LOGIN_MAX_FAILURES,
  OWNER_SESSION_SECONDS,
  checkGuildAuth,
  clearLoginFailures,
  inviteCodeFor,
  isOwnerCookie,
  loginRetryAfter,
  readInviteSession,
  readInviteSessionIds,
  readOwnerSession,
  recordLoginFailure,
  resetLoginThrottleForTest,
  revokeAllSessions,
  revokeSession,
  sealInviteSession,
  sealOwner,
} from "./auth.js";

const TOKEN = "owner-token-for-tests-0123456789abcdef";
const envFor = (extra = {}) => ({ GUILD_SESSION_FILE: join(mkdtempSync(join(tmpdir(), "guild-sess-")), "s.json"), ...extra });
const jarWith = (cookies) => ({ get: (name) => (cookies[name] ? { value: cookies[name] } : undefined) });

test("owner sessions are unique per sign-in and verify on the server", () => {
  const env = envFor();
  const a = sealOwner(TOKEN, { env });
  const b = sealOwner(TOKEN, { env });
  assert.notEqual(a, b);
  assert.ok(isOwnerCookie(a, TOKEN, { env }));
  assert.equal(isOwnerCookie(a, "another-token", { env }), false);
  const auth = checkGuildAuth({ jar: jarWith({ guild_session: a }), token: TOKEN, env });
  assert.equal(auth.role, "owner");
});

test("owner sessions expire server-side", () => {
  const env = envFor();
  const now = Date.now();
  const raw = sealOwner(TOKEN, { env, now });
  assert.ok(isOwnerCookie(raw, TOKEN, { env, now: now + 1000 }));
  assert.equal(isOwnerCookie(raw, TOKEN, { env, now: now + OWNER_SESSION_SECONDS * 1000 + 1 }), false);
});

test("the legacy never-expiring HMAC cookie is refused", async () => {
  const { createHmac, scryptSync } = await import("node:crypto");
  const legacy = createHmac("sha256", scryptSync(TOKEN, "dev-guild-kdf-v1", 32)).update("dev-guild-owner").digest("hex");
  assert.equal(isOwnerCookie(legacy, TOKEN, { env: envFor() }), false);
});

test("logout revokes one session; everywhere revokes all earlier sessions", () => {
  const env = envFor();
  const now = Date.now();
  const one = sealOwner(TOKEN, { env, now: now - 5000 });
  const two = sealOwner(TOKEN, { env, now: now - 5000 });
  const s = readOwnerSession(one, TOKEN, { env });
  revokeSession(s.sid, s.exp, env);
  assert.equal(isOwnerCookie(one, TOKEN, { env }), false);
  assert.ok(isOwnerCookie(two, TOKEN, { env }));
  revokeAllSessions(env, now);
  assert.equal(isOwnerCookie(two, TOKEN, { env }), false);
  assert.ok(isOwnerCookie(sealOwner(TOKEN, { env, now: now + 1 }), TOKEN, { env }));
});

test("changing GUILD_SESSION_EPOCH revokes sessions and rotates the invite code", () => {
  const env1 = envFor();
  const env2 = { ...env1, GUILD_SESSION_EPOCH: "2" };
  const raw = sealOwner(TOKEN, { env: env1 });
  assert.equal(isOwnerCookie(raw, TOKEN, { env: env2 }), false);
  assert.notEqual(inviteCodeFor(TOKEN, env1), inviteCodeFor(TOKEN, env2));
  assert.equal(inviteCodeFor(TOKEN, env1), inviteCodeFor(TOKEN, {}), "default epoch keeps existing invite links");
});

test("invite sessions expire, can be revoked and keep the participant shape", () => {
  const env = envFor();
  const now = Date.now();
  const code = inviteCodeFor(TOKEN, env);
  const raw = sealInviteSession({ code, id: "bot", name: "Bot", kind: "agent" }, TOKEN, { env, now });
  assert.deepEqual(readInviteSession(raw, TOKEN, { env }), { code, id: "bot", name: "Bot", kind: "agent" });
  assert.equal(readInviteSession(raw, TOKEN, { env, now: now + 31 * 24 * 3600 * 1000 }), null);
  const ids = readInviteSessionIds(raw, TOKEN, { env });
  revokeSession(ids.sid, ids.exp, env);
  assert.equal(readInviteSession(raw, TOKEN, { env }), null);
});

test("owner bearer token still signs scripts in", () => {
  const auth = checkGuildAuth({ jar: null, token: TOKEN, authHeader: `Bearer ${TOKEN}`, env: envFor() });
  assert.equal(auth.role, "owner");
});

test("failed sign-ins are throttled per client and cleared on success", () => {
  resetLoginThrottleForTest();
  const now = Date.now();
  for (let i = 0; i < LOGIN_MAX_FAILURES; i++) {
    assert.equal(loginRetryAfter("1.2.3.4", now), 0);
    recordLoginFailure("1.2.3.4", now);
  }
  assert.ok(loginRetryAfter("1.2.3.4", now) > 0);
  assert.equal(loginRetryAfter("5.6.7.8", now), 0, "another client is not blocked");
  assert.equal(loginRetryAfter("1.2.3.4", now + 16 * 60 * 1000), 0, "window slides");
  clearLoginFailures("1.2.3.4");
  assert.equal(loginRetryAfter("1.2.3.4", now), 0);
  resetLoginThrottleForTest();
});
