import assert from "node:assert/strict";
import test from "node:test";
import { SYNC_TARGETS, buildPlan } from "./sync.js";
import { VERIFIERS, verifyValue } from "./verify.js";

const card = { id: `s_${"b".repeat(32)}`, name: "API_TOKEN", version: 1, last4: "wxyz" };

test("plans are dry runs, value-free and digest-bound", () => {
  const a = buildPlan({ card, target: "github", environment: "prod", env: { SECRETS_SYNC_GITHUB_REPO: "o/r" } });
  assert.equal(a.dryRun, true);
  assert.equal(a.applyEnabled, false);
  assert.equal(a.command, "gh secret set API_TOKEN --repo o/r --env prod");
  const b = buildPlan({ card: { ...card, version: 2 }, target: "github", environment: "prod", env: {} });
  assert.notEqual(a.digest, b.digest);
  assert.equal(buildPlan({ card, target: "nope", environment: "dev", env: {} }), null);
});

test("adapters send the expected request through the injected fetch only", async () => {
  const calls = [];
  const fetchImpl = async (url, init) => { calls.push({ url, init }); return new Response("{}", { status: 200 }); };
  const env = {
    SECRETS_SYNC_CF_ACCOUNT: "acct", SECRETS_SYNC_CF_SCRIPT_PROD: "worker-prod", SECRETS_SYNC_CF_TOKEN: "fake-cf",
    SECRETS_SYNC_NETLIFY_ACCOUNT: "team", SECRETS_SYNC_NETLIFY_SITE: "site1", SECRETS_SYNC_NETLIFY_TOKEN: "fake-nf",
  };
  await SYNC_TARGETS.cloudflare.apply({ name: "API_TOKEN", value: "fake-value", environment: "prod" }, { env, fetchImpl });
  assert.equal(calls[0].url, "https://api.cloudflare.com/client/v4/accounts/acct/workers/scripts/worker-prod/secrets");
  assert.deepEqual(JSON.parse(calls[0].init.body), { name: "API_TOKEN", text: "fake-value", type: "secret_text" });
  await SYNC_TARGETS.netlify.apply({ name: "API_TOKEN", value: "fake-value", environment: "staging" }, { env, fetchImpl });
  assert.equal(JSON.parse(calls[1].init.body)[0].values[0].context, "deploy-preview");
  await assert.rejects(SYNC_TARGETS.github.apply(), (err) => err.status === 501);
  await assert.rejects(SYNC_TARGETS.fly.apply({ name: "A", value: "v", environment: "dev" }, { env: {}, fetchImpl }), (err) => err.status === 503);
  assert.equal(calls.length, 2);
});

test("verification is off by default and maps provider answers", async () => {
  let called = false;
  const never = async () => { called = true; return new Response("{}"); };
  assert.equal((await verifyValue("openai", "fake", { env: {}, fetchImpl: never })).status, "unverified");
  assert.equal(called, false);
  const on = { SECRETS_VERIFY: "on" };
  assert.equal((await verifyValue("aws", "fake", { env: on, fetchImpl: never })).status, "unverified");
  assert.equal((await verifyValue("slack", "fake", { env: on, fetchImpl: async () => Response.json({ ok: false }) })).status, "invalid");
  assert.equal((await verifyValue("github", "fake", { env: on, fetchImpl: async () => new Response("", { status: 500 }) })).status, "unverified");
  assert.equal((await verifyValue("github", "fake", { env: on, fetchImpl: async () => { throw new Error("offline"); } })).status, "unverified");
  for (const make of Object.values(VERIFIERS)) assert.match(make("x").url, /^https:\/\//);
});
