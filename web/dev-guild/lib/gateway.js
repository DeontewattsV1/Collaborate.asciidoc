// Vercel AI Gateway for Dev Guild. The key is read from the environment on
// the server. Callers pass generateText/streamText in tests so this file never
// contacts the gateway unless a real key is present and a real SDK is used.

export const DEFAULT_GATEWAY_MODEL = "google/gemini-2.5-flash-lite";
export const MAX_TURNS = 3;

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
export async function completeWithGateway({
  apiKey,
  model,
  system,
  prompt,
  stream = false,
  generateText,
  streamText,
}) {
  if (!apiKey) return { ok: false, reason: "unset" };
  const run = stream ? streamText : generateText;
  if (typeof run !== "function") return { ok: false, reason: "sdk-missing" };
  try {
    const result = await run({
      model: model || DEFAULT_GATEWAY_MODEL,
      system,
      prompt,
    });
    const text = await textOf(result, stream);
    if (!text) return { ok: false, reason: "empty" };
    return { ok: true, text, model: model || DEFAULT_GATEWAY_MODEL };
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
