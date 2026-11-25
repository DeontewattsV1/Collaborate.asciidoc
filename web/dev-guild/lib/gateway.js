// Vercel AI Gateway for Dev Guild. The key is read from the environment on
// the server. Callers pass generateText/streamText in tests so this file never
// contacts the gateway unless a real key is present and a real SDK is used.

export const DEFAULT_GATEWAY_MODEL = "google/gemini-2.5-flash-lite";
export const RERANK_MODEL = "cohere/rerank-v3.5";
export const MAX_TURNS = 3;
export const MAX_SUMMARY = 240;

// Reasoning effort is per character. Operator does not reason. The summary
// shown in the chat is a separate short sentence, never the raw trace.
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

export const MODEL_FALLBACKS = {
  claude: ["anthropic/claude-haiku-4.5", "google/gemini-2.5-flash-lite"],
  codex: ["openai/gpt-4.1-mini", "google/gemini-2.5-flash-lite"],
  chatgpt: ["openai/gpt-4.1-mini", "google/gemini-2.5-flash-lite"],
  gemini: ["google/gemini-2.5-flash", "google/gemini-2.5-flash-lite"],
  grok: ["xai/grok-3-mini", "google/gemini-2.5-flash-lite"],
  tay: ["xai/grok-3-mini", "google/gemini-2.5-flash-lite"],
  default: ["google/gemini-2.5-flash-lite"],
};

export const HANDOFF_STATUSES = ["open", "handed-off", "done"];

export function reasoningEffort(id) {
  return REASONING_EFFORT[id] || "low";
}

export function modelFallbacks(id) {
  return MODEL_FALLBACKS[id] || MODEL_FALLBACKS.default;
}

// gatewayProviderOptions is what generateText/streamText send as
// providerOptions.gateway. zeroDataRetention defaults on. Tags attribute
// spend to the character. A Budget is set on the key in the Vercel dashboard,
// not in this request.
export function gatewayProviderOptions(character) {
  const name = (character && (character.name || character.id)) || "guild";
  const id = (character && character.id) || "";
  return {
    gateway: {
      models: modelFallbacks(id),
      caching: "auto",
      tags: ["dev-guild", name],
      user: "deonte",
      zeroDataRetention: true,
    },
  };
}

export function gatewayModel() {
  return (process.env.GUILD_GATEWAY_MODEL || DEFAULT_GATEWAY_MODEL).trim() || DEFAULT_GATEWAY_MODEL;
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

export function planSpeakers({ members, group, text, known }) {
  const mentioned = parseMentions(text, known);
  if (mentioned.length) return mentioned;
  if (!group) {
    const other = (members || []).find((id) => id !== "owner" && known.has(id));
    return other ? [other] : [];
  }
  return known.has("operator") ? ["operator"] : [];
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

// completeWithGateway calls generateText or streamText with a gateway model
// string. A missing key or a thrown error (including the gateway 403) is a
// soft failure so the caller can use the VM providers.
export function gatewayRequest({ model, system, prompt, character, messages, tools }) {
  const effort = reasoningEffort(character && character.id);
  const request = {
    model: model || DEFAULT_GATEWAY_MODEL,
    system,
    providerOptions: gatewayProviderOptions(character || {}),
  };
  if (effort !== "none") request.reasoning = effort;
  if (messages) request.messages = messages;
  else request.prompt = prompt;
  if (tools) request.tools = tools;
  return request;
}

export function summaryOnly(text) {
  const clean = String(text || "").replace(/\s+/g, " ").trim();
  if (!clean) return "";
  return clean.slice(0, MAX_SUMMARY);
}

export async function completeWithGateway({
  apiKey,
  model,
  system,
  prompt,
  character,
  messages,
  tools,
  summary,
  stream = false,
  generateText,
  streamText,
}) {
  if (!apiKey) return { ok: false, reason: "unset" };
  const run = stream ? streamText : generateText;
  if (typeof run !== "function") return { ok: false, reason: "sdk-missing" };
  try {
    const request = gatewayRequest({
      model: model || DEFAULT_GATEWAY_MODEL,
      system,
      prompt,
      character,
      messages,
      tools,
    });
    const result = await run(request);
    const text = await textOf(result, stream);
    if (!text) return { ok: false, reason: "empty" };
    return {
      ok: true,
      text,
      model: request.model,
      // reasoningText is the model's trace. It is not returned to the client.
      summary: summaryOnly(summary),
    };
  } catch (err) {
    const status = err && (err.statusCode || err.status);
    const message = err && err.message ? String(err.message) : "gateway error";
    return { ok: false, reason: "error", status: status || 0, message };
  }
}

// runGatewayRound speaks each planned character through the gateway. It stops
// on the first gateway error and returns any lines already spoken.
export async function runGatewayRound({
  apiKey,
  model,
  characters,
  thread,
  userText,
  complete,
}) {
  const known = new Set((characters || []).map((c) => c.id));
  const byId = new Map((characters || []).map((c) => [c.id, c]));
  if (!apiKey) return { ok: false, reason: "unset", lines: [] };
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
      apiKey,
      model: model || DEFAULT_GATEWAY_MODEL,
      system: character.system || "",
      prompt: userText,
      character,
    });
    if (!result || !result.ok) {
      return { ok: false, reason: (result && result.reason) || "error", lines, error: result && result.message };
    }
    lines.push({ from: id, text: result.text });
    if (thread && thread.group) {
      for (const mention of parseMentions(result.text, known)) {
        if (!seen.has(mention)) queue.push(mention);
      }
    }
  }
  if (!lines.length) return { ok: false, reason: "empty", lines };
  return { ok: true, lines };
}

// deliverOwnerMessage logs the human line, tries the gateway, and asks the VM
// providers to answer when the gateway key is missing or the gateway fails
// before any line was stored.
export async function deliverOwnerMessage({
  threadId,
  text,
  apiKey,
  model,
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
    apiKey,
    model: model || gatewayModel(),
    characters: (cast && cast.characters) || [],
    thread,
    userText: text,
    complete,
  });
  if (round.ok) {
    for (const line of round.lines) {
      await backend("POST", `/v1/threads/${threadId}/lines`, line);
    }
    return { via: "gateway", lines: round.lines.length };
  }
  if (round.lines && round.lines.length) {
    for (const line of round.lines) {
      await backend("POST", `/v1/threads/${threadId}/lines`, line);
    }
    return { via: "gateway-partial", reason: round.reason, lines: round.lines.length };
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

export function rerankBody(query, documents) {
  return {
    model: RERANK_MODEL,
    query,
    documents: (documents || []).map((item) => item.text || ""),
    top_n: Math.min(5, (documents || []).length || 5),
  };
}

// searchHistory asks the gateway to rerank. A missing key or a 403 returns the
// newest lines unchanged.
export async function searchHistory({ apiKey, query, documents, fetchImpl }) {
  const docs = documents || [];
  if (!apiKey || !query || docs.length === 0) return { ok: false, reason: apiKey ? "empty" : "unset", documents: docs.slice(-5) };
  const run = fetchImpl;
  if (typeof run !== "function") return { ok: false, reason: "unset", documents: docs.slice(-5) };
  try {
    const res = await run("https://ai-gateway.vercel.sh/v1/rerank", {
      method: "POST",
      headers: { Authorization: `Bearer ${apiKey}`, "Content-Type": "application/json" },
      body: JSON.stringify(rerankBody(query, docs)),
    });
    if (!res.ok) return { ok: false, reason: "error", status: res.status, documents: docs.slice(-5) };
    const data = await res.json();
    const order = (data.results || data.data || []).map((row) => row.index);
    const ranked = order.map((index) => docs[index]).filter(Boolean);
    return { ok: true, documents: ranked.length ? ranked : docs.slice(-5) };
  } catch {
    return { ok: false, reason: "error", documents: docs.slice(-5) };
  }
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
