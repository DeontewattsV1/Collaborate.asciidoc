import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { containsSecret, detectSecrets, entropy, guessProvider, redactSecrets } from "./detector.js";

// Fake values only. They match provider formats but are not real credentials.
// Deterministic pseudo-random strings so the fixtures are stable.
let seed = 0;
const r = (n, set = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") => {
  let s = "";
  while (s.length < n) {
    seed += 1;
    for (const b of createHash("sha256").update(`fixture-${seed}`).digest()) {
      if (s.length < n) s += set[b % set.length];
    }
  }
  return s;
};

const FAKES = {
  openai: `sk-proj-${r(48)}`,
  anthropic: `sk-ant-api03-${r(60)}`,
  google: `AIza${r(35)}`,
  github: `ghp_${r(36)}`,
  aws: `AKIA${r(16, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567")}`,
  stripe: `sk_test_${r(30)}`,
  slack: `xoxb-${r(12, "0123456789")}-${r(24)}`,
  fly: `fo1_${r(40)}`,
  netlify: `nfp_${r(40)}`,
  cloudflare: `cfut_${r(40)}`,
  groq: `gsk_${r(52)}`,
};

test("detects each provider format and keeps only the last four characters", () => {
  for (const [provider, value] of Object.entries(FAKES)) {
    const hits = detectSecrets(`here you go: ${value} thanks`);
    assert.equal(hits.length, 1, provider);
    assert.equal(hits[0].provider, provider, provider);
    assert.equal(hits[0].last4, value.slice(-4));
    assert.equal(guessProvider(value), provider);
  }
});

test("detects KEY=value assignments and generic high-entropy tokens", () => {
  const generic = r(40, "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789");
  assert.equal(detectSecrets(`CLOUDFLARE_API_TOKEN=${r(40)}`)[0].hint, "CLOUDFLARE_API_TOKEN");
  assert.equal(detectSecrets(`try ${generic}`)[0].provider, "generic");
  assert.ok(detectSecrets("-----BEGIN PRIVATE KEY-----\nMIIEv\n-----END PRIVATE KEY-----")[0].provider === "private_key");
  assert.ok(entropy(generic) > 4);
});

test("ignores ordinary chat, commit SHAs, sha256 digests and urls", () => {
  for (const text of [
    "let's ship the docs today and merge PR 104",
    "commit 97ae2492ca0a5e0bbea3807bf43767bb777cd6d7 is on main",
    `sha256 ${"ab".repeat(32)}`,
    "see https://github.com/DeontewattsV1/Collaborate.asciidoc/pull/104",
    "PASSWORD=short",
  ]) {
    assert.equal(containsSecret(text), false, text);
  }
});

test("redaction removes every value and never returns values by default", () => {
  const text = `openai ${FAKES.openai} and github ${FAKES.github}`;
  const out = redactSecrets(text);
  assert.equal(out.redacted, true);
  assert.equal(out.findings.length, 2);
  for (const v of [FAKES.openai, FAKES.github]) assert.ok(!out.text.includes(v));
  assert.ok(out.text.includes(`OpenAI …${FAKES.openai.slice(-4)}`));
  assert.ok(out.findings.every((f) => !("value" in f)));
  const kept = redactSecrets(text, { keepValues: true });
  assert.equal(kept.findings[0].value, FAKES.openai);
  assert.deepEqual(redactSecrets("hello"), { text: "hello", findings: [], redacted: false });
});
