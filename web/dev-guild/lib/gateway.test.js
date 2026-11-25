import assert from "node:assert/strict";
import test from "node:test";
import {
  attachmentParts,
  completeWithGateway,
  DEFAULT_GATEWAY_MODEL,
  deliverOwnerMessage,
  demoStreamEvents,
  gatewayProviderOptions,
  reasoningEffort,
  runTool,
  searchHistory,
  validateHandoff,
} from "./gateway.js";

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

test("gateway options set fallbacks, caching, tags, user, and zero data retention", async () => {
  const seen = [];
  await completeWithGateway({
    apiKey: "test-gateway-key",
    model: DEFAULT_GATEWAY_MODEL,
    character: { id: "claude", name: "Claude" },
    system: "You are Claude.",
    prompt: "hello",
    summary: "Kept the change small. " + "x".repeat(500),
    generateText: async (args) => {
      seen.push(args);
      return { text: "Let me sit with that.", reasoningText: "raw chain of thought that must not leak" };
    },
  }).then((result) => {
    assert.equal(result.summary.length <= 240, true);
    assert.equal(JSON.stringify(result).includes("raw chain of thought"), false);
    assert.equal(reasoningEffort("claude"), "high");
    assert.equal(reasoningEffort("grok"), "low");
    assert.equal(seen[0].reasoning, "high");
    assert.equal(seen[0].providerOptions.gateway.caching, "auto");
    assert.equal(seen[0].providerOptions.gateway.zeroDataRetention, true);
    assert.equal(seen[0].providerOptions.gateway.user, "deonte");
    assert.ok(seen[0].providerOptions.gateway.tags.includes("Claude"));
    assert.ok(seen[0].providerOptions.gateway.models.length >= 1);
    assert.deepEqual(seen[0].providerOptions.gateway, gatewayProviderOptions({ id: "claude", name: "Claude" }).gateway);
  });
});

test("images and PDFs become input_image and input_file", () => {
  const png = Buffer.from("png").toString("base64");
  const pdf = Buffer.from("%PDF-1.4").toString("base64");
  const parts = attachmentParts([
    { name: "shot.png", mediaType: "image/png", data: png },
    { name: "notes.pdf", mediaType: "application/pdf", data: pdf },
  ]);
  assert.equal(parts[0].type, "input_image");
  assert.match(parts[0].image_url, /^data:image\/png;base64,/);
  assert.equal(parts[1].type, "input_file");
  assert.equal(parts[1].filename, "notes.pdf");
  assert.throws(() => attachmentParts([{ name: ".env", mediaType: "text/plain", data: png }]));
});

test("handoff schema and tool log", async () => {
  const known = new Set(["codex", "claude"]);
  assert.equal(validateHandoff({ task: "Write the handler", assignee: "codex", status: "handed-off" }, known).ok, true);
  assert.equal(validateHandoff({ task: "x", assignee: "codex", status: "nope" }, known).ok, false);
  const calls = [];
  const result = await runTool({
    name: "handoff",
    from: "claude",
    threadId: "guild",
    known,
    args: { task: "Write the handler", assignee: "codex", status: "handed-off" },
    backend: async (method, path, body) => {
      calls.push({ method, path, body });
      return {};
    },
  });
  assert.equal(result.card.assignee, "codex");
  assert.ok(calls.some((call) => call.body && call.body.kind === "tool" && call.body.tool === "handoff"));
  assert.ok(calls.some((call) => call.body && call.body.kind === "card" && call.body.card_status === "handed-off"));
});

test("rerank uses cohere/rerank-v3.5 and a 403 keeps the original order", async () => {
  const docs = [{ text: "alpha" }, { text: "beta" }];
  const ok = await searchHistory({
    apiKey: "test-gateway-key",
    query: "beta",
    documents: docs,
    fetchImpl: async (url, init) => {
      const body = JSON.parse(init.body);
      assert.equal(body.model, "cohere/rerank-v3.5");
      assert.equal(url, "https://ai-gateway.vercel.sh/v1/rerank");
      return { ok: true, status: 200, json: async () => ({ results: [{ index: 1 }, { index: 0 }] }) };
    },
  });
  assert.deepEqual(ok.documents.map((d) => d.text), ["beta", "alpha"]);
  const denied = await searchHistory({
    apiKey: "test-gateway-key",
    query: "beta",
    documents: docs,
    fetchImpl: async () => ({ ok: false, status: 403, json: async () => ({}) }),
  });
  assert.equal(denied.ok, false);
  assert.equal(denied.documents.length, 2);
});

test("demo stream shows a summary, a tool, and a card, and no raw trace", () => {
  const events = demoStreamEvents({ from: "claude", userText: "@claude a small API" });
  const names = events.map((event) => event.event);
  assert.ok(names.includes("typing"));
  assert.ok(names.includes("delta"));
  assert.ok(names.includes("reasoning"));
  assert.ok(names.includes("tool"));
  assert.ok(names.includes("card"));
  const raw = JSON.stringify(events);
  assert.equal(raw.includes("reasoningText"), false);
  assert.match(events.find((event) => event.event === "reasoning").data.summary, /constraint/);
});
