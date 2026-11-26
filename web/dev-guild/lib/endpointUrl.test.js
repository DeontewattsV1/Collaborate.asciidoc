import assert from "node:assert/strict";
import { test } from "node:test";
import { isPrivateHost, validEndpointUrl } from "./endpointUrl.js";

const strict = { GUILD_ALLOW_PRIVATE_ENDPOINTS: "" };

test("accepts public https endpoints", () => {
  assert.equal(validEndpointUrl("https://api.groq.com/openai/v1/", strict), "https://api.groq.com/openai/v1");
  assert.equal(validEndpointUrl("https://openrouter.ai/api/v1", strict), "https://openrouter.ai/api/v1");
  assert.equal(validEndpointUrl("http://8.8.8.8/v1", strict), "http://8.8.8.8/v1");
});

test("rejects non-http schemes, garbage, and embedded credentials", () => {
  for (const raw of ["", "not a url", "file:///etc/passwd", "gopher://x/", "javascript:alert(1)", "https://user:pw@api.example.com/v1"]) {
    assert.equal(validEndpointUrl(raw, strict), "", raw);
  }
});

test("rejects loopback, private, link-local, and metadata targets (SSRF)", () => {
  const blocked = [
    "http://localhost:11434/v1",
    "http://LOCALHOST./v1",
    "http://foo.localhost/v1",
    "http://127.0.0.1/v1",
    "http://127.1/v1",
    "http://2130706433/v1",
    "http://0x7f000001/v1",
    "http://0177.0.0.1/v1",
    "http://0.0.0.0/v1",
    "http://10.0.0.5/v1",
    "http://172.16.0.1/v1",
    "http://172.31.255.255/v1",
    "http://192.168.1.10/v1",
    "http://100.64.0.1/v1",
    "http://169.254.169.254/latest/meta-data/",
    "http://metadata.google.internal/computeMetadata/v1/",
    "http://printer.local/v1",
    "http://[::1]/v1",
    "http://[::]/v1",
    "http://[::ffff:127.0.0.1]/v1",
    "http://[::ffff:a9fe:a9fe]/v1",
    "http://[fd00::1]/v1",
    "http://[fe80::1]/v1",
  ];
  for (const raw of blocked) {
    assert.equal(validEndpointUrl(raw, strict), "", raw);
  }
});

test("does not over-block public neighbours of private ranges", () => {
  assert.equal(isPrivateHost("172.15.0.1"), false);
  assert.equal(isPrivateHost("172.32.0.1"), false);
  assert.equal(isPrivateHost("100.128.0.1"), false);
  assert.equal(isPrivateHost("[2606:4700::1111]"), false);
  assert.equal(isPrivateHost("local-models.example.com"), false);
});

test("GUILD_ALLOW_PRIVATE_ENDPOINTS=1 opts in to local models for self-hosters", () => {
  const env = { GUILD_ALLOW_PRIVATE_ENDPOINTS: "1" };
  assert.equal(validEndpointUrl("http://127.0.0.1:1234/v1", env), "http://127.0.0.1:1234/v1");
  assert.equal(validEndpointUrl("file:///etc/passwd", env), "");
});
