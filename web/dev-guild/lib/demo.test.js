import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { usesPostgres } from "./store.js";
import { demoAttempts, durableStoreBlockedReason, isLiveDemo, isScriptedDemo } from "./demo.js";

const read = (rel) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");

test("scripted demo never calls a model, even with a key present", () => {
  const env = { GUILD_DEMO: "1", GUILD_DEMO_SCRIPTED: "1", GOOGLE_GENERATIVE_AI_API_KEY: "test-key" };
  assert.equal(isScriptedDemo(env), true);
  assert.equal(isLiveDemo(env), false);
  assert.deepEqual(demoAttempts(env), []);
  assert.equal(isLiveDemo({ GUILD_DEMO: "1", GOOGLE_GENERATIVE_AI_API_KEY: "test-key" }), true);
  assert.equal(isScriptedDemo({ GUILD_DEMO_SCRIPTED: "1" }), false, "scripted flag means nothing outside demo mode");
});

test("demo mode blocks durable rooms and the secret vault", () => {
  assert.match(durableStoreBlockedReason({ GUILD_DEMO: "1", GUILD_STORE: "postgres", DATABASE_URL: "postgres://x" }), /disabled in public demo mode/);
  assert.equal(durableStoreBlockedReason({ GUILD_STORE: "postgres" }), "");
});

test("every durable room and vault route goes through the guarded roomDb", () => {
  const http = read("./rooms/http.js");
  assert.match(http, /export function roomDb\(env = process\.env\) \{\n  const blocked = durableStoreBlockedReason\(env\);\n  if \(blocked\) throw unavailable\(blocked\);/);
  const secrets = read("./secrets/http.js");
  assert.match(secrets, /db: roomDb\(\), auth: bearerAuth\(token\)/);
  assert.match(secrets, /db: roomDb\(\), auth: ownerCookieAuth\("guild"\)/);
  assert.match(secrets, /auth\.role !== "owner"/, "demo role must never reach the vault");
});

test("demo mode never uses the durable chat store", () => {
  assert.equal(usesPostgres({ GUILD_DEMO: "1", GUILD_STORE: "postgres" }), false);
  assert.equal(usesPostgres({ GUILD_DEMO: "", GUILD_STORE: "postgres" }), true);
});
