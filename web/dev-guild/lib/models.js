// Free model path for Dev Guild. Callers pass generateText/streamText in tests
// so this file never contacts a provider unless a real key is present and a
// real SDK runner is used. Keys stay in the server environment.

export const FLASH_LITE = "gemini-3.5-flash-lite";
export const HYDROGEN = "gemini-4-hydrogen";
export const GEMMA = "gemma-4-31b-it";
export const GROQ_FREE = "llama-3.1-8b-instant";
export const OPENROUTER_FREE = ["google/gemma-3-12b-it:free", "meta-llama/llama-3.2-3b-instruct:free"];
export const MAX_TURNS = 3;
export const MAX_SUMMARY = 240;

// Free on the Gemini Developer API pricing page (AI Studio). Carbon, Argon,
// and Neon are paid and are not in this list.
export const FREE_GOOGLE = {
  deep: HYDROGEN,
  quick: FLASH_LITE,
  distinct: GEMMA,
};

const DEEP = new Set(["claude", "codex", "gemini", "manus"]);
const QUICK = new Set(["grok", "tay", "cursor", "copilot", "operator"]);

export const REASONING_EFFORT = {
  claude: "high",
  codex: "medium",
  chatgpt: "low",
  gemini: "medium",
  grok: "low",
  tay: "low",
  manus: "medium",
  cursor: "low",
  copilot: "low",
  operator: "none",
  ollama: "low",
};

export const HANDOFF_STATUSES = ["open", "handed-off", "done"];

export function reasoningEffort(id) {
  return REASONING_EFFORT[id] || "low";
}

export function voiceFor(id) {
  if (DEEP.has(id)) return "deep";
  if (QUICK.has(id)) return "quick";
  return "distinct";
}

export function modelFor(id) {
  return FREE_GOOGLE[voiceFor(id)];
}

export function googleChain(id) {
  const primary = modelFor(id);
  return [primary, ...Object.values(FREE_GOOGLE).filter((model) => model !== primary)];
}

export function isRateLimit(err) {
  const status = err && (err.statusCode || err.status);
  const message = String((err && err.message) || "");
  return status === 429 || /429|RESOURCE_EXHAUSTED|rate limit/i.test(message);
}

// Order: the character's free Gemini model, the other free Gemini models on
// 429, then Groq and OpenRouter only when those keys are set. OpenRouter
// entries must end in :free.
export function attemptsFor(env, characterId, character) {
  const source = env || {};
  if (character && character.kind === "custom") {
    if (character.endpointType === "openai") {
      if (character.baseUrl && character.modelId && character.apiKey) {
        return [{
          provider: "custom-openai",
          name: character.id,
          baseUrl: character.baseUrl,
          modelId: character.modelId,
          apiKey: character.apiKey,
        }];
      }
      return [];
    }
    const attempts = [];
    if (source.GOOGLE_GENERATIVE_AI_API_KEY) {
      const preferred = character.modelId || FLASH_LITE;
      const chain = [preferred, ...googleChain(characterId).filter((m) => m !== preferred)];
      for (const modelId of chain) attempts.push({ provider: "google", modelId });
    }
    if (source.GROQ_API_KEY) attempts.push({ provider: "groq", modelId: GROQ_FREE });
    return attempts;
  }
  if (character && character.kind === "preset") {
    const req = character.requiredEnv;
    if (req && !String(source[req] || "").trim()) return [];
    if (character.provider === "openrouter" && !String(character.modelId || "").endsWith(":free")) {
      throw new Error("OpenRouter models must use the :free suffix");
    }
    return [{ provider: character.provider, modelId: character.modelId }];
  }
  const attempts = [];
  if (source.GOOGLE_GENERATIVE_AI_API_KEY) {
    for (const modelId of googleChain(characterId)) attempts.push({ provider: "google", modelId });
  }
  if (source.GROQ_API_KEY) attempts.push({ provider: "groq", modelId: GROQ_FREE });
  if (source.OPENROUTER_API_KEY) {
    for (const modelId of OPENROUTER_FREE) {
      if (!modelId.endsWith(":free")) throw new Error("OpenRouter models must use the :free suffix");
      attempts.push({ provider: "openrouter", modelId });
    }
  }
  return attempts;
}

export async function runFreeAttempts({ attempts, run }) {
  let last = { ok: false, reason: attempts && attempts.length ? "error" : "unset" };
  let skipGoogle = false;
  for (const attempt of attempts || []) {
    if (skipGoogle && attempt.provider === "google") continue;
    try {
      const result = await run(attempt);
      if (result && result.ok === false) {
        last = result;
        if (result.reason === "rate-limit" || result.reason === "empty") continue;
        if (attempt.provider === "google") {
          skipGoogle = true;
          continue;
        }
        return result;
      }
      const { reasoningText, thought, ...safe } = result || {};
      const summary = safe.summary !== undefined ? summaryOnly(safe.summary) : summaryOnly(reasoningText || thought);
      return { ok: true, provider: attempt.provider, modelId: attempt.modelId, ...safe, summary };
    } catch (err) {
      if (isRateLimit(err)) {
        last = { ok: false, reason: "rate-limit", status: 429 };
        continue;
      }
      last = { ok: false, reason: "error", status: err.statusCode || err.status || 0 };
      if (attempt.provider === "google") {
        skipGoogle = true;
        continue;
      }
      return last;
    }
  }
  return last;
}

// thinkingConfig.includeThoughts asks Gemini for a thought trace. Callers
// must pass only summaryOnly() of that trace to the phone and the log.
export function googleThinking(id) {
  const effort = reasoningEffort(id);
  if (effort === "none") {
    return { thinkingConfig: { includeThoughts: false, thinkingLevel: "minimal" } };
  }
  const thinkingLevel = effort === "high" ? "high" : effort === "medium" ? "medium" : "low";
  return { thinkingConfig: { includeThoughts: true, thinkingLevel } };
}

export function providerRequest({ provider, modelId, system, prompt, character, messages, tools }) {
  const request = {
    provider,
    modelId,
    system,
    providerOptions: { google: googleThinking(character && character.id) },
  };
  if (messages) request.messages = messages;
  else request.prompt = prompt;
  if (tools) request.tools = tools;
  if (provider !== "google" || modelId === GEMMA) delete request.providerOptions;
  return request;
}

export function parseMentions(text, known) {
  const found = [];
  const seen = new Set();
  const re = /@([a-z0-9-]{2,32})/g;
  let match;
  while ((match = re.exec(text || ""))) {
    const id = match[1];
    if (!known.has(id) || seen.has(id)) continue;
    seen.add(id);
    found.push(id);
    if (found.length === 8) break;
  }
  return found;
}

export function planSpeakers({ members, group, text, known, activeIds, priorityIds }) {
  const mentioned = parseMentions(text, known);
  if (mentioned.length) return mentioned;
  if (!group) {
    const other = (members || []).find((id) => id !== "owner" && known.has(id));
    return other ? [other] : [];
  }
  const memberSet = members && members.length ? new Set(members) : null;
  const allow = (id) => {
    if (!id || id === "owner" || !known.has(id)) return false;
    if (memberSet && !memberSet.has(id)) return false;
    if (activeIds && !activeIds.has(id)) return false;
    return true;
  };
  const chosen = [];
  const defaults = ["tay", "claude", "grok", "gemini", "codex", "chatgpt"];
  for (const id of [...(priorityIds || []), ...defaults, ...(members || [])]) {
    if (!allow(id) || chosen.includes(id)) continue;
    chosen.push(id);
    if (chosen.length >= MAX_TURNS) break;
  }
  if (chosen.length) return chosen;
  return known.has("operator") ? ["operator"] : [];
}

async function thoughtOf(result) {
  if (!result) return "";
  const raw = result.reasoningText || result.reasoning || result.thought || "";
  const value = typeof raw.then === "function" ? await raw : raw;
  if (typeof value === "string") return value;
  if (Array.isArray(value)) return value.map((part) => part.text || "").join(" ");
  return "";
}

async function textOf(result, stream) {
  if (!result) return "";
  if (stream) {
    if (result.textStream) {
      let out = "";
      for await (const chunk of result.textStream) out += chunk;
      return out.trim();
    }
    if (result.text && typeof result.text.then === "function") {
      return String(await result.text).trim();
    }
  }
  return String(result.text || "").trim();
}

export function summaryOnly(text) {
  const clean = String(text || "").replace(/\s+/g, " ").trim();
  if (!clean) return "";
  return clean.slice(0, MAX_SUMMARY);
}

export async function completeWithProvider({
  env,
  character,
  system,
  prompt,
  messages,
  tools,
  run,
}) {
  const attempts = attemptsFor(env, character && character.id, character);
  if (!attempts.length) return { ok: false, reason: "unset" };
  if (typeof run !== "function") return { ok: false, reason: "sdk-missing" };
  return runFreeAttempts({
    attempts,
    run: async (attempt) => {
      const request = providerRequest({ ...attempt, system, prompt, character, messages, tools });
      try {
        const result = await run(request);
        const text = await textOf(result, Boolean(result && result.textStream));
        const thought = await thoughtOf(result);
        if (!text) return { ok: false, reason: "empty" };
        return { ok: true, text, summary: summaryOnly(thought), request };
      } catch (err) {
        if (isRateLimit(err)) return { ok: false, reason: "rate-limit", status: 429 };
        throw err;
      }
    },
  });
}

// runGatewayRound speaks each planned character through the free model chain.
// It stops on the first hard error and returns any lines already spoken.
export async function runGatewayRound({
  env,
  characters,
  thread,
  userText,
  complete,
}) {
  const known = new Set((characters || []).map((c) => c.id));
  const byId = new Map((characters || []).map((c) => [c.id, c]));
  if (env && attemptsFor(env).length === 0) return { ok: false, reason: "unset", lines: [] };
  const queue = planSpeakers({
    members: thread && thread.members,
    group: !!(thread && thread.group),
    text: userText,
    known,
  });
  const seen = new Set();
  const lines = [];
  let turns = 0;
  while (queue.length && turns < MAX_TURNS) {
    const id = queue.shift();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    const character = byId.get(id);
    if (!character) continue;
    turns += 1;
    const result = await complete({
      env,
      modelId: modelFor(character.id),
      system: character.system || "",
      prompt: userText,
      character,
    });
    if (!result || !result.ok) {
      return { ok: false, reason: (result && result.reason) || "error", lines, error: result && result.message };
    }
    lines.push({ from: id, text: result.text, reasoning: result.summary || "" });
    if (thread && thread.group) {
      for (const mention of parseMentions(result.text, known)) {
        if (!seen.has(mention)) queue.push(mention);
      }
    }
  }
  if (!lines.length) return { ok: false, reason: "empty", lines };
  return { ok: true, lines };
}

// deliverOwnerMessage logs the human line, tries the free model chain, and
// asks the VM providers to answer when no free key is set or every free
// attempt fails before a line was stored.
export async function deliverOwnerMessage({
  threadId,
  text,
  env,
  backend,
  complete,
}) {
  await backend("POST", `/v1/threads/${threadId}/messages`, { text, reply: "hold" });
  const cast = await backend("GET", "/v1/characters");
  const threads = await backend("GET", "/v1/threads");
  const thread = ((threads && threads.threads) || []).find((item) => item.id === threadId) || {
    id: threadId,
    group: true,
    members: ["owner"],
  };
  const round = await runGatewayRound({
    env,
    characters: (cast && cast.characters) || [],
    thread,
    userText: text,
    complete,
  });
  if (round.ok) {
    for (const line of round.lines) {
      await backend("POST", `/v1/threads/${threadId}/lines`, line);
    }
    return { via: "gemini", lines: round.lines.length };
  }
  if (round.lines && round.lines.length) {
    for (const line of round.lines) {
      await backend("POST", `/v1/threads/${threadId}/lines`, line);
    }
    return { via: "partial", reason: round.reason, lines: round.lines.length };
  }
  await backend("POST", `/v1/threads/${threadId}/respond`, {});
  return { via: "provider", reason: round.reason };
}

const IMAGE_TYPES = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);
const MAX_IMAGE_BYTES = 1_500_000;
const MAX_PDF_BYTES = 2_000_000;

function decodedLength(data) {
  const raw = String(data || "").replace(/\s/g, "");
  return Math.floor((raw.length * 3) / 4);
}

// normalizeAttachments keeps images and PDFs. Bytes stay out of the signed log;
// only the file name is stored. The model sees input_image or input_file.
export function normalizeAttachments(list) {
  const items = list || [];
  if (items.length > 4) {
    const error = new Error("too many attachments");
    error.status = 400;
    throw error;
  }
  return items.map((item) => {
    const mediaType = String(item.mediaType || "").toLowerCase();
    const name = String(item.name || "attachment").split(/[/\\]/).pop().slice(0, 80);
    const data = String(item.data || "");
    const image = IMAGE_TYPES.has(mediaType);
    const pdf = mediaType === "application/pdf";
    if (!image && !pdf) {
      const error = new Error("only images and PDFs can be attached");
      error.status = 400;
      throw error;
    }
    const size = decodedLength(data);
    if (!data || size > (image ? MAX_IMAGE_BYTES : MAX_PDF_BYTES)) {
      const error = new Error("attachment is empty or too large");
      error.status = 400;
      throw error;
    }
    return { name, mediaType, data };
  });
}

export function attachmentParts(attachments) {
  return normalizeAttachments(attachments).map((item) => {
    const url = `data:${item.mediaType};base64,${item.data}`;
    if (item.mediaType === "application/pdf") {
      return { type: "input_file", filename: item.name, file_data: url };
    }
    return { type: "input_image", image_url: url };
  });
}

export function modelMessages({ text, attachments }) {
  const content = [{ type: "text", text: text || "" }];
  for (const part of attachmentParts(attachments)) {
    if (part.type === "input_image") {
      content.push({ type: "image", image: part.image_url });
    } else {
      content.push({ type: "file", data: part.file_data, mediaType: "application/pdf", filename: part.filename });
    }
  }
  return [{ role: "user", content }];
}

export function validateHandoff(input, known) {
  const task = String((input && input.task) || "").trim();
  const assignee = String((input && input.assignee) || "").trim();
  const status = String((input && input.status) || "").trim();
  if (!task || task.length > 400) return { ok: false, error: "task is required" };
  if (!known || !known.has(assignee)) return { ok: false, error: "assignee is not in the guild" };
  if (!HANDOFF_STATUSES.includes(status)) return { ok: false, error: "status must be open, handed-off, or done" };
  return { ok: true, card: { task, assignee, status } };
}

// runTool executes one safe tool and returns the log line. File bytes and
// secrets are not part of the returned summary.
export async function runTool({ name, args, from, known, backend, threadId }) {
  const who = from || "operator";
  if (name === "read_repo_file") {
    const path = String((args && args.path) || "");
    const file = await backend("GET", `/v1/files?thread=${encodeURIComponent(threadId)}&from=${encodeURIComponent(who)}&path=${encodeURIComponent(path)}`);
    const text = String((file && file.text) || "");
    return { name, summary: `Read ${path} (${text.length} bytes)`, text: text.slice(0, 2000) };
  }
  if (name === "lookup_log") {
    const data = await backend("GET", `/v1/threads/${threadId}/messages?after=0`);
    const lines = ((data && data.messages) || []).slice(-8);
    await backend("POST", `/v1/threads/${threadId}/lines`, {
      from: who,
      kind: "tool",
      tool: "lookup_log",
      text: `Read ${lines.length} signed lines`,
    });
    return { name, summary: `Read ${lines.length} signed lines`, lines: lines.map((m) => ({ from: m.from, text: m.text })) };
  }
  if (name === "handoff") {
    const checked = validateHandoff(args, known);
    if (!checked.ok) {
      const error = new Error(checked.error);
      error.status = 400;
      throw error;
    }
    await backend("POST", `/v1/threads/${threadId}/lines`, {
      from: who,
      kind: "tool",
      tool: "handoff",
      text: `Handed to @${checked.card.assignee}: ${checked.card.task}`,
    });
    await backend("POST", `/v1/threads/${threadId}/lines`, {
      from: who,
      kind: "card",
      text: checked.card.task,
      task: checked.card.task,
      assignee: checked.card.assignee,
      card_status: checked.card.status,
    });
    return { name, summary: `Handed to @${checked.card.assignee}`, card: checked.card };
  }
  const error = new Error("unknown tool");
  error.status = 400;
  throw error;
}

// Search stays on the newest matching lines. Paid rerank is not used.
export function searchHistory({ query, documents }) {
  const docs = documents || [];
  const needle = String(query || "").trim().toLowerCase();
  const matched = needle ? docs.filter((item) => String(item.text || "").toLowerCase().includes(needle)) : docs;
  const chosen = (matched.length ? matched : docs).slice(-5);
  return { ok: true, via: "recent", documents: chosen };
}

export function demoStreamEvents({ from, userText }) {
  const say = from === "grok"
    ? "Skip the ceremony. Put the small API in the log and stop."
    : "Let me sit with that. I'd keep the API small enough to review.";
  const summary = from === "grok"
    ? "Short version: one handler, then stop."
    : "The constraint is size, so the change stays narrow.";
  const chunks = [];
  const words = say.split(" ");
  for (let i = 0; i < words.length; i += 4) {
    chunks.push(words.slice(i, i + 4).join(" ") + (i + 4 < words.length ? " " : ""));
  }
  const task = String(userText || "Review the chat API").replace(/@[\w-]+/g, "").trim().slice(0, 160);
  return [
    { event: "typing", data: { from } },
    ...chunks.map((text) => ({ event: "delta", data: { from, text } })),
    { event: "reasoning", data: { from, summary } },
    { event: "tool", data: { from, name: "lookup_log", summary: "Read the latest signed lines for this thread." } },
    { event: "card", data: { from, task: task || "Review the chat API", assignee: "codex", status: "handed-off" } },
    { event: "done", data: { via: "demo", text: say, summary } },
  ];
}
