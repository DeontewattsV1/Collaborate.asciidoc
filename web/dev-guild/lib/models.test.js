import assert from "node:assert/strict";
import test from "node:test";
import {
  FLASH_LITE,
  GEMMA,
  HYDROGEN,
  OPENROUTER_FREE,
  attachmentParts,
  attemptsFor,
  deliverOwnerMessage,
  demoStreamEvents,
  googleThinking,
  isRateLimit,
  modelFor,
  providerRequest,
  runFreeAttempts,
  runTool,
  searchHistory,
  validateHandoff,
} from "./models.js";

function card(id) {
  return { id, name: id, system: `You are ${id}. Catchphrase stays in the prompt.` };
}

const cast = [card("claude"), card("grok"), card("codex"), card("ollama")];
const threads = [{ id: "guild", group: true, members: ["owner", "claude", "grok", "codex"] }];

function backendFactory(log) {
  return async (method, path, body) => {
    log.push({ method, path, body });
    if (path === "/v1/characters") return { characters: cast };
    if (path === "/v1/threads") return { threads };
    return {};
  };
}

test("no free keys falls back to the VM and does not call a model", async () => {
  const log = [];
  let calls = 0;
  const result = await deliverOwnerMessage({
    threadId: "guild",
    text: "@claude hello",
    env: {},
    backend: backendFactory(log),
    complete: async () => {
      calls += 1;
      return { ok: true, text: "no" };
    },
  });
  assert.equal(calls, 0);
  assert.equal(result.via, "provider");
  assert.equal(result.reason, "unset");
  assert.ok(log.some((call) => String(call.path).endsWith("/respond")));
});

test("characters use free Gemini models only", () => {
  assert.equal(modelFor("claude"), HYDROGEN);
  assert.equal(modelFor("grok"), FLASH_LITE);
  assert.equal(modelFor("ollama"), GEMMA);
  assert.equal(modelFor("docker"), GEMMA);
  const chain = attemptsFor({ GOOGLE_GENERATIVE_AI_API_KEY: "k" }, "claude").map((a) => a.modelId);
  assert.deepEqual(chain, [HYDROGEN, FLASH_LITE, GEMMA]);
  for (const id of [HYDROGEN, FLASH_LITE, GEMMA]) {
    assert.equal(id.includes("carbon") || id.includes("argon") || id.includes("neon"), false);
  }
});

test("a 429 tries the next free Gemini model", async () => {
  const seen = [];
  const result = await runFreeAttempts({
    attempts: attemptsFor({ GOOGLE_GENERATIVE_AI_API_KEY: "k" }, "claude"),
    run: async (attempt) => {
      seen.push(attempt.modelId);
      if (attempt.modelId === HYDROGEN) {
        const err = new Error("429 RESOURCE_EXHAUSTED");
        err.statusCode = 429;
        throw err;
      }
      return { text: "Let me sit with that." };
    },
  });
  assert.equal(result.ok, true);
  assert.equal(result.modelId, FLASH_LITE);
  assert.deepEqual(seen, [HYDROGEN, FLASH_LITE]);
  assert.equal(isRateLimit({ status: 429 }), true);
});

test("a non-429 Google error skips the rest of Gemini and tries Groq", async () => {
  const seen = [];
  const result = await runFreeAttempts({
    attempts: attemptsFor({ GOOGLE_GENERATIVE_AI_API_KEY: "k", GROQ_API_KEY: "g" }, "claude"),
    run: async (attempt) => {
      seen.push(attempt.provider);
      if (attempt.provider === "google") {
        const err = new Error("invalid key");
        err.statusCode = 401;
        throw err;
      }
      return { text: "Groq answered." };
    },
  });
  assert.equal(result.ok, true);
  assert.equal(result.provider, "groq");
  assert.deepEqual(seen, ["google", "groq"]);
});

test("Groq and OpenRouter run only when set, and OpenRouter stays on :free", async () => {
  const none = attemptsFor({}, "grok");
  assert.equal(none.length, 0);
  const both = attemptsFor({ GROQ_API_KEY: "g", OPENROUTER_API_KEY: "o" }, "grok");
  assert.equal(both[0].provider, "groq");
  assert.ok(both.slice(1).every((item) => item.modelId.endsWith(":free")));
  assert.deepEqual(OPENROUTER_FREE.every((id) => id.endsWith(":free")), true);
});

test("Gemma requests omit thinkingConfig", () => {
  const request = providerRequest({
    provider: "google",
    modelId: GEMMA,
    character: { id: "ollama" },
    prompt: "hi",
  });
  assert.equal(request.providerOptions, undefined);
});

test("Google requests ask for thought summaries and never set a gateway", async () => {
  const request = providerRequest({
    provider: "google",
    modelId: HYDROGEN,
    character: { id: "claude", name: "Claude" },
    system: "You are Claude.",
    prompt: "hello",
    tools: { read_repo_file: {}, lookup_log: {}, handoff: {} },
  });
  assert.equal(request.providerOptions.gateway, undefined);
  assert.equal(request.providerOptions.google.thinkingConfig.includeThoughts, true);
  assert.equal(request.providerOptions.google.thinkingConfig.thinkingLevel, "high");
  assert.equal(googleThinking("grok").thinkingConfig.thinkingLevel, "low");
  assert.equal(googleThinking("operator").thinkingConfig.includeThoughts, false);
  assert.ok(request.tools.read_repo_file);
  assert.ok(request.tools.handoff);
});

test("a long thought trace is reduced to a short summary", async () => {
  const marker = "UNIQUE-TAIL-SHOULD-NOT-LEAK";
  const raw = `${"step one of private scratch work. ".repeat(40)}${marker}`;
  const result = await runFreeAttempts({
    attempts: [{ provider: "google", modelId: FLASH_LITE }],
    run: async () => ({ text: "Skip the ceremony.", reasoningText: raw }),
  });
  assert.equal(result.summary.length <= 240, true);
  assert.equal(result.reasoningText, undefined);
  assert.equal(JSON.stringify(result).includes(marker), false);
});

test("images and PDFs become input_image and input_file", () => {
  const png = Buffer.from("png").toString("base64");
  const pdf = Buffer.from("%PDF-1.4").toString("base64");
  const parts = attachmentParts([
    { name: "shot.png", mediaType: "image/png", data: png },
    { name: "notes.pdf", mediaType: "application/pdf", data: pdf },
  ]);
  assert.equal(parts[0].type, "input_image");
  assert.equal(parts[1].type, "input_file");
  assert.throws(() => attachmentParts([{ name: ".env", mediaType: "text/plain", data: png }]));
});

test("handoff writes a tool line and a task card", async () => {
  const calls = [];
  const result = await runTool({
    name: "handoff",
    from: "claude",
    threadId: "guild",
    known: new Set(["codex", "claude"]),
    args: { task: "Write the handler", assignee: "codex", status: "handed-off" },
    backend: async (method, path, body) => {
      calls.push(body);
      return {};
    },
  });
  assert.equal(result.card.status, "handed-off");
  assert.ok(calls.some((body) => body.kind === "tool" && body.tool === "handoff"));
  assert.ok(calls.some((body) => body.kind === "card"));
  assert.equal(validateHandoff({ task: "x", assignee: "codex", status: "nope" }, new Set(["codex"])).ok, false);
});

test("search returns recent matching lines and does not call a reranker", () => {
  const docs = [{ text: "alpha one" }, { text: "beta two" }, { text: "alpha three" }];
  const found = searchHistory({ query: "alpha", documents: docs });
  assert.equal(found.via, "recent");
  assert.deepEqual(found.documents.map((d) => d.text), ["alpha one", "alpha three"]);
  assert.equal(JSON.stringify(found).includes("cohere"), false);
});

test("demo stream has a summary, a tool, and a card", () => {
  const events = demoStreamEvents({ from: "claude", userText: "@claude a small API" });
  const names = events.map((event) => event.event);
  assert.ok(names.includes("typing") && names.includes("delta") && names.includes("reasoning"));
  assert.ok(names.includes("tool") && names.includes("card"));
  assert.equal(JSON.stringify(events).includes("reasoningText"), false);
});
import { BUILTIN_AGENTS, BUILTIN_CAST, BUILTIN_TOOLS, FREE_PRESETS, statusFor } from "./cast.js";
import { planSpeakers } from "./models.js";
import {
  checkGuildAuth,
  decryptCustomAgents,
  encryptCustomAgents,
  inviteCodeFor,
  isValidInviteCode,
  readGroupPrefs,
  sealGroupPrefs,
} from "./auth.js";
import { buildCatalog, configureGroup, getMessages, listThreads, resetStoreForTest } from "./store.js";

test("built-in cast includes all 11 agents, 43 tool mascots, and 8 free presets with no hardcoded owner", () => {
  resetStoreForTest();
  assert.equal(BUILTIN_AGENTS.length, 11);
  assert.equal(BUILTIN_TOOLS.length, 43);
  assert.equal(FREE_PRESETS.length, 8);
  assert.equal(BUILTIN_CAST.length, 62);
  const catalog = buildCatalog({ env: { GOOGLE_GENERATIVE_AI_API_KEY: "k" } });
  assert.equal(catalog.length, 62);
  assert.equal(JSON.stringify(catalog).includes("Deonte"), false);
});

test("first-run deploy starts empty and supports custom group name and picked members", () => {
  resetStoreForTest();
  const catalog = buildCatalog({ env: { GOOGLE_GENERATIVE_AI_API_KEY: "k" } });
  const initial = listThreads({ catalog, env: {} }).find((t) => t.id === "guild");
  assert.ok(initial && initial.group);
  assert.equal(initial.setupDone, false);
  assert.equal(initial.title, "Dev Guild");
  assert.equal(initial.ownerName, "Owner");
  assert.deepEqual(initial.members, ["owner"]);
  assert.equal(getMessages("guild", 0).length, 0);

  const emptySpeakers = planSpeakers({
    members: initial.members,
    group: true,
    text: "Hello anyone?",
    known: new Set(catalog.map((c) => c.id)),
    activeIds: new Set(catalog.filter((c) => c.online).map((c) => c.id)),
  });
  assert.deepEqual(emptySpeakers, []);

  const prefs = configureGroup(
    {
      title: "Backend Crew",
      ownerName: "Alex",
      members: ["claude", "grok", "nonexistent"],
      setupDone: true,
      replaceMembers: true,
    },
    null,
    catalog,
    {},
  );
  const token = "passphrase-123";
  const roundTrip = readGroupPrefs(sealGroupPrefs(prefs, token), token);
  assert.equal(roundTrip.title, "Backend Crew");
  assert.equal(roundTrip.ownerName, "Alex");
  assert.equal(roundTrip.setupDone, true);
  assert.deepEqual(roundTrip.added, ["claude", "grok"]);

  const configured = listThreads({ catalog, cookiePrefs: roundTrip, env: {} }).find((t) => t.id === "guild");
  assert.equal(configured.title, "Backend Crew");
  assert.equal(configured.ownerName, "Alex");
  assert.equal(configured.setupDone, true);
  assert.deepEqual(configured.members, ["owner", "claude", "grok"]);

  // Simulate another warm server instance still holding stale "claude" while
  // the client presents a newer replacement cookie clearing members to [].
  const clearedPrefs = {
    version: roundTrip.version + 10,
    title: "Backend Crew",
    ownerName: "Alex",
    setupDone: true,
    added: [],
    removed: [],
  };
  const clearedCookie = readGroupPrefs(sealGroupPrefs(clearedPrefs, token), token);
  const afterReplace = listThreads({ catalog, cookiePrefs: clearedCookie, env: {} }).find((t) => t.id === "guild");
  assert.deepEqual(afterReplace.members, ["owner"]);
});

test("free presets activate only when their free key is set and show needs key otherwise", () => {
  const envGeminiOnly = { GOOGLE_GENERATIVE_AI_API_KEY: "gkey" };
  const groqPreset = FREE_PRESETS.find((p) => p.id === "groq-llama");
  const orPreset = FREE_PRESETS.find((p) => p.id === "openrouter-gemma");
  const gemPreset = FREE_PRESETS.find((p) => p.id === "gemini-flash");
  assert.equal(statusFor(gemPreset, envGeminiOnly).online, true);
  assert.equal(statusFor(groqPreset, envGeminiOnly).online, false);
  assert.equal(statusFor(groqPreset, envGeminiOnly).statusLabel, "needs key");
  assert.equal(attemptsFor(envGeminiOnly, groqPreset.id, groqPreset).length, 0);
  assert.equal(statusFor(orPreset, envGeminiOnly).online, false);

  const envWithGroq = { GOOGLE_GENERATIVE_AI_API_KEY: "gkey", GROQ_API_KEY: "groq" };
  assert.equal(statusFor(groqPreset, envWithGroq).online, true);
  assert.equal(attemptsFor(envWithGroq, groqPreset.id, groqPreset)[0].provider, "groq");
});

test("custom agent API key stays encrypted in cookie and never leaks in catalog", () => {
  const token = "test-owner-token-123";
  const secretAgent = {
    id: "nova",
    name: "Nova",
    kind: "custom",
    endpointType: "openai",
    baseUrl: "https://api.example.com/v1",
    modelId: "llama-3.1-8b-instant",
    apiKey: "sk-super-secret-value",
  };
  const sealed = encryptCustomAgents([secretAgent], token);
  assert.equal(sealed.includes("sk-super-secret-value"), false);
  const restored = decryptCustomAgents(sealed, token);
  assert.equal(restored[0].apiKey, "sk-super-secret-value");

  const publicCatalog = buildCatalog({ env: {}, customAgents: restored, includeSecrets: false });
  const nova = publicCatalog.find((c) => c.id === "nova");
  assert.ok(nova);
  assert.equal(nova.hasKey, true);
  assert.equal(nova.apiKey, undefined);
  assert.equal(JSON.stringify(publicCatalog).includes("sk-super-secret-value"), false);

  const attempts = attemptsFor({}, "nova", restored[0]);
  assert.equal(attempts.length, 1);
  assert.equal(attempts[0].provider, "custom-openai");
});

test("group chat plans at least two active speakers when no mention is given", () => {
  const known = new Set(["tay", "claude", "grok", "gemini", "operator"]);
  const activeIds = new Set(["tay", "claude", "grok", "gemini", "operator"]);
  const speakers = planSpeakers({
    members: ["owner", "tay", "claude", "grok", "gemini", "operator"],
    group: true,
    text: "Hey team, say hello!",
    known,
    activeIds,
  });
  assert.ok(speakers.length >= 2);
  assert.equal(new Set(speakers).size, speakers.length);
});

test("invite code validates strictly against owner token", () => {
  const token = "guild-secret-passphrase";
  const code = inviteCodeFor(token);
  assert.ok(code.startsWith("inv_"));
  assert.equal(isValidInviteCode(code, token), true);
  assert.equal(isValidInviteCode("inv_wrong", token), false);
  assert.equal(checkGuildAuth({ jar: null, token, inviteParam: code }).ok, true);
  assert.equal(checkGuildAuth({ jar: null, token, inviteParam: "bad" }).ok, false);
});
import { appendLine } from "./store.js";
import { directoryCharacters } from "./directory.js";
import { checkDemoAutoLimit, checkDemoRateLimit, DEMO_AGENT_IDS } from "./demo.js";

test("directoryCharacters provides ~500 contacts across 17 sections with webp coding-agent avatars", () => {
  const dir = directoryCharacters({ GOOGLE_GENERATIVE_AI_API_KEY: "test" });
  assert.ok(dir.length >= 450, `expected >= 450 directory entries, got ${dir.length}`);
  const coding = dir.filter((c) => c.section === "AI coding agents");
  assert.equal(coding.length, 75);
  for (const agent of coding) {
    assert.ok(agent.avatar && agent.avatar.endsWith(".webp"), `expected .webp avatar for ${agent.id}`);
    assert.equal(agent.isPersona, true);
    assert.equal(agent.online, true);
  }
  const full = buildCatalog({ env: { GOOGLE_GENERATIVE_AI_API_KEY: "test" }, includeDirectory: true });
  assert.ok(full.length >= 500, `expected >= 500 total catalog contacts, got ${full.length}`);
});

test("GUILD_DEMO=1 restricts catalog, forces gemini-3.5-flash-lite, and never persists messages", () => {
  const demoEnv = { GUILD_DEMO: "1", GOOGLE_GENERATIVE_AI_API_KEY: "demo-key" };
  const demoCatalog = buildCatalog({ env: demoEnv, includeDirectory: true });
  assert.deepEqual(demoCatalog.map((c) => c.id).sort(), [...DEMO_AGENT_IDS].sort());

  const attempts = attemptsFor(demoEnv, "claude");
  assert.deepEqual(attempts, [{ provider: "google", modelId: FLASH_LITE }]);

  assert.equal(checkGuildAuth({ jar: null, token: "", env: demoEnv }).ok, true);
  assert.equal(checkGuildAuth({ jar: null, token: "", env: demoEnv }).role, "demo");

  const line = appendLine({ threadId: "guild", from: "owner", name: "Visitor", text: "Hi demo", env: demoEnv });
  assert.equal(line.text, "Hi demo");
  assert.deepEqual(getMessages({ threadId: "guild", env: demoEnv }), []);
});

test("demo rate limits enforce 10 user msgs/hr and autonomous IP/daily budgets", () => {
  const ip = `203.0.113.${Math.floor(Math.random() * 200) + 10}`;
  for (let i = 0; i < 10; i += 1) {
    const r = checkDemoRateLimit(ip, { now: 1000000 + i * 100 });
    assert.equal(r.allowed, true);
  }
  const blocked = checkDemoRateLimit(ip, { now: 1002000 });
  assert.equal(blocked.allowed, false);
  assert.equal(blocked.remaining, 0);

  const autoIp = `198.51.100.${Math.floor(Math.random() * 200) + 10}`;
  for (let i = 0; i < 30; i += 1) {
    assert.equal(checkDemoAutoLimit(autoIp, { now: 2000000 + i * 100 }).allowed, true);
  }
  assert.equal(checkDemoAutoLimit(autoIp, { now: 2004000 }).allowed, false);
});
