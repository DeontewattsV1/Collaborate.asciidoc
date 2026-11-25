import assert from "node:assert/strict";
import test from "node:test";
import { completeWithGateway, deliverOwnerMessage, DEFAULT_GATEWAY_MODEL } from "./gateway.js";

function card(id, extra = "") {
  return {
    id,
    name: id,
    system: `You are ${id}. Catchphrase stays in the prompt. ${extra}`,
  };
}

test("missing AI_GATEWAY_API_KEY does not call the gateway", async () => {
  let calls = 0;
  const backend = [];
  const result = await deliverOwnerMessage({
    threadId: "guild",
    text: "@claude hello",
    apiKey: "",
    backend: async (method, path, body) => {
      backend.push({ method, path, body });
      if (path === "/v1/characters") return { characters: [card("claude"), card("grok")] };
      if (path === "/v1/threads") return { threads: [{ id: "guild", group: true, members: ["owner", "claude", "grok"] }] };
      return {};
    },
    complete: async () => {
      calls += 1;
      return { ok: true, text: "should not run" };
    },
  });
  assert.equal(calls, 0);
  assert.equal(result.via, "provider");
  assert.equal(result.reason, "unset");
  assert.ok(backend.some((call) => call.path.endsWith("/respond")));
  assert.equal(JSON.stringify(backend).includes("AI_GATEWAY"), false);
});

test("generateText receives the gateway model string and the personality", async () => {
  const seen = [];
  const text = await completeWithGateway({
    apiKey: "test-gateway-key",
    model: DEFAULT_GATEWAY_MODEL,
    system: "You are Claude. Let me sit with that.",
    prompt: "hello",
    generateText: async (args) => {
      seen.push(args);
      return { text: "Let me sit with that. One constraint." };
    },
  });
  assert.equal(text.ok, true);
  assert.equal(seen[0].model, "google/gemini-2.5-flash-lite");
  assert.match(seen[0].system, /Let me sit with that/);
  assert.equal(seen[0].prompt, "hello");
});

test("streamText chunks are joined", async () => {
  const text = await completeWithGateway({
    apiKey: "test-gateway-key",
    model: DEFAULT_GATEWAY_MODEL,
    system: "voice",
    prompt: "hi",
    stream: true,
    streamText: async () => ({
      textStream: (async function* () {
        yield "Skip ";
        yield "the ceremony.";
      })(),
    }),
  });
  assert.equal(text.ok, true);
  assert.equal(text.text, "Skip the ceremony.");
});

test("gateway 403 falls back to the VM providers and does not store a line", async () => {
  const backend = [];
  const result = await deliverOwnerMessage({
    threadId: "guild",
    text: "@claude @grok a small api",
    apiKey: "test-gateway-key",
    model: DEFAULT_GATEWAY_MODEL,
    backend: async (method, path, body) => {
      backend.push({ method, path, body });
      if (path === "/v1/characters") {
        return { characters: [card("claude"), card("grok"), card("codex")] };
      }
      if (path === "/v1/threads") {
        return { threads: [{ id: "guild", group: true, members: ["owner", "claude", "grok", "codex"] }] };
      }
      return {};
    },
    complete: async () =>
      completeWithGateway({
        apiKey: "test-gateway-key",
        model: DEFAULT_GATEWAY_MODEL,
        system: "x",
        prompt: "y",
        generateText: async () => {
          const err = new Error("requires a valid credit card on file");
          err.statusCode = 403;
          throw err;
        },
      }),
  });
  assert.equal(result.via, "provider");
  assert.equal(result.reason, "error");
  assert.equal(backend.some((call) => String(call.path).endsWith("/lines")), false);
  assert.equal(backend.some((call) => String(call.path).endsWith("/respond")), true);
  assert.equal(JSON.stringify(backend).includes("test-gateway-key"), false);
});

test("a successful gateway round stores each character once and does not fall back", async () => {
  const backend = [];
  const models = [];
  const result = await deliverOwnerMessage({
    threadId: "guild",
    text: "@claude @grok keep it reviewable",
    apiKey: "test-gateway-key",
    model: DEFAULT_GATEWAY_MODEL,
    backend: async (method, path, body) => {
      backend.push({ method, path, body });
      if (path === "/v1/characters") {
        return {
          characters: [
            card("claude", "Names the constraint."),
            card("grok"),
            card("codex"),
          ],
        };
      }
      if (path === "/v1/threads") {
        return { threads: [{ id: "guild", group: true, members: ["owner", "claude", "grok", "codex"] }] };
      }
      return {};
    },
    complete: async (job) => {
      models.push(job.model);
      assert.match(job.system, /Catchphrase stays/);
      if (job.system.includes("claude")) {
        return { ok: true, text: "Let me sit with that. @codex keep the diff narrow." };
      }
      return { ok: true, text: "Skip the ceremony." };
    },
  });
  assert.equal(result.via, "gateway");
  assert.deepEqual(models, [DEFAULT_GATEWAY_MODEL, DEFAULT_GATEWAY_MODEL, DEFAULT_GATEWAY_MODEL]);
  const lines = backend.filter((call) => String(call.path).endsWith("/lines")).map((call) => call.body.from);
  assert.deepEqual(lines, ["claude", "grok", "codex"]);
  assert.equal(backend.some((call) => String(call.path).endsWith("/respond")), false);
});
