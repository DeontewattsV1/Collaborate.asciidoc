import { stepCountIs, streamText } from "ai";
import { jsonSchema, tool } from "ai";
import { cookies } from "next/headers";
import {
  checkGuildAuth,
  decryptCustomAgents,
  getOwnerToken,
  readGroupPrefs,
} from "../../../../lib/auth.js";
import {
  attemptsFor,
  demoStreamEvents,
  GEMMA,
  isRateLimit,
  MAX_TURNS,
  modelMessages,
  normalizeAttachments,
  parseMentions,
  planSpeakers,
  providerRequest,
  summaryOnly,
  validateHandoff,
} from "../../../../lib/models.js";
import { languageModel } from "../../../../lib/providers.js";
import {
  appendLine,
  buildCatalog,
  getMessages,
  hasVmBackend,
  listThreads,
  mergeGroupPrefs,
  vmFetch,
} from "../../../../lib/store.js";

export const dynamic = "force-dynamic";

function encode(event, data) {
  return `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
}

async function mirrorVm(token, method, path, body) {
  if (!hasVmBackend()) return null;
  try {
    return await vmFetch(token, method, path, body);
  } catch {
    return null;
  }
}

function buildStandaloneTools({ threadId, from, known, catalog, token, send }) {
  return {
    lookup_log: tool({
      description: "Read the latest lines in this thread.",
      inputSchema: jsonSchema({ type: "object", properties: {}, additionalProperties: false }),
      execute: async () => {
        const lines = getMessages(threadId, 0).slice(-8);
        const msg = appendLine({
          threadId,
          from,
          kind: "tool",
          tool: "lookup_log",
          text: `Read ${lines.length} thread lines`,
          catalog,
        });
        send("tool", { from, name: "lookup_log", summary: `Read ${lines.length} thread lines`, message: msg });
        await mirrorVm(token, "POST", `/v1/threads/${threadId}/lines`, {
          from, kind: "tool", tool: "lookup_log", text: `Read ${lines.length} thread lines`,
        });
        return { name: "lookup_log", summary: `Read ${lines.length} lines`, lines: lines.map((m) => ({ from: m.from, text: m.text })) };
      },
    }),
    handoff: tool({
      description: "Write a task card and hand it to another character.",
      inputSchema: jsonSchema({
        type: "object",
        properties: {
          task: { type: "string" },
          assignee: { type: "string", description: "Character id, without @" },
          status: { type: "string", enum: ["open", "handed-off", "done"] },
        },
        required: ["task", "assignee", "status"],
        additionalProperties: false,
      }),
      execute: async (args) => {
        const checked = validateHandoff(args, known);
        if (!checked.ok) return { error: checked.error };
        const toolMsg = appendLine({
          threadId,
          from,
          kind: "tool",
          tool: "handoff",
          text: `Handed to @${checked.card.assignee}: ${checked.card.task}`,
          catalog,
        });
        send("tool", { from, name: "handoff", summary: `Handed to @${checked.card.assignee}`, message: toolMsg });
        const cardMsg = appendLine({
          threadId,
          from,
          kind: "card",
          text: checked.card.task,
          task: checked.card.task,
          assignee: checked.card.assignee,
          card_status: checked.card.status,
          catalog,
        });
        send("card", { from, ...checked.card, message: cardMsg });
        await mirrorVm(token, "POST", `/v1/threads/${threadId}/lines`, {
          from, kind: "card", text: checked.card.task, task: checked.card.task,
          assignee: checked.card.assignee, card_status: checked.card.status,
        });
        return { name: "handoff", summary: `Handed to @${checked.card.assignee}`, card: checked.card };
      },
    }),
  };
}

export async function POST(request) {
  const token = getOwnerToken();
  const jar = await cookies();
  const auth = checkGuildAuth({
    jar,
    token,
    authHeader: request.headers.get("authorization"),
  });
  if (!auth.ok) {
    return Response.json({ error: auth.error }, { status: auth.status });
  }

  let body = {};
  try {
    body = await request.json();
  } catch {
    return Response.json({ error: "bad json" }, { status: 400 });
  }

  let attachments = [];
  try {
    attachments = normalizeAttachments(body.attachments);
  } catch (err) {
    return Response.json({ error: err.message }, { status: err.status || 400 });
  }

  const threadId = String(body.thread || "guild");
  if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(threadId)) {
    return Response.json({ error: "bad thread" }, { status: 400 });
  }

  const text = String(body.text || "").trim();
  const files = attachments.map((item) => item.name);
  if (!text && !files.length) {
    return Response.json({ error: "message is empty" }, { status: 400 });
  }

  const cookieAgents = decryptCustomAgents(jar.get("guild_custom_agents")?.value || "", token);
  const cookiePrefs = readGroupPrefs(jar.get("guild_group_prefs")?.value || "", token);
  const prefs = mergeGroupPrefs(cookiePrefs);
  const vmChars = await mirrorVm(token, "GET", "/v1/characters");
  const catalog = buildCatalog({
    env: process.env,
    customAgents: cookieAgents,
    vmCharacters: vmChars && vmChars.characters,
    includeSecrets: true,
    groupTitle: prefs.title,
  });
  const threads = listThreads({ catalog, cookiePrefs });
  const thread = threads.find((t) => t.id === threadId) || {
    id: threadId,
    group: threadId === "guild",
    members: ["owner"],
  };

  const senderId = auth.role === "owner" ? "owner" : (auth.participant && auth.participant.id) || "guest";
  const senderName = auth.role === "owner" ? prefs.ownerName : (auth.participant && auth.participant.name) || "Guest";
  const ownerMessage = appendLine({
    threadId,
    from: senderId,
    senderName,
    text: text || "Sent an attachment.",
    files,
    catalog,
  });
  await mirrorVm(token, "POST", `/v1/threads/${threadId}/messages`, {
    text: text || "Sent an attachment.",
    reply: "hold",
    files,
  });

  const encoder = new TextEncoder();
  const stream = new ReadableStream({
    async start(controller) {
      const send = (event, data) => controller.enqueue(encoder.encode(encode(event, data)));
      try {
        send("message", { message: ownerMessage });

        const known = new Set(catalog.map((c) => c.id));
        const activeIds = new Set(catalog.filter((c) => c.online).map((c) => c.id));
        const byId = new Map(catalog.map((c) => [c.id, c]));
        const customPriority = Array.isArray(body.priorityIds) && body.priorityIds.length
          ? body.priorityIds
          : catalog.filter((c) => c.kind === "custom" && c.online).slice(-1).map((c) => c.id);

        const queue = planSpeakers({
          members: thread.members,
          group: Boolean(thread.group),
          text,
          known,
          activeIds,
          priorityIds: customPriority,
        });

        const demo = process.env.GUILD_DEMO === "1" && !process.env.GOOGLE_GENERATIVE_AI_API_KEY;
        if (demo) {
          const from = queue[0] || "claude";
          const events = demoStreamEvents({ from, userText: text });
          let say = "";
          let summary = "";
          for (const event of events) {
            if (event.event === "done") continue;
            if (event.event === "delta") say += event.data.text;
            if (event.event === "reasoning") summary = event.data.summary;
            send(event.event, event.data);
          }
          const msg = appendLine({
            threadId,
            from,
            text: say.trim(),
            reasoning: summary,
            catalog,
          });
          send("message", { message: msg });
          send("done", { via: "demo", messages: [msg] });
          controller.close();
          return;
        }

        if (queue.length === 0 && thread.group && (thread.members || []).filter((m) => m !== "owner").length === 0) {
          const hint = appendLine({
            threadId,
            from: "system",
            kind: "system",
            text: "No agents are in this group yet. Tap + Add / Invite to pick members or @mention any character.",
            catalog,
          });
          send("message", { message: hint });
          send("done", { via: "empty-group", messages: [hint] });
          controller.close();
          return;
        }

        const seen = new Set([senderId]);
        const committed = [];
        let turns = 0;

        while (queue.length && turns < MAX_TURNS) {
          const id = queue.shift();
          if (!id || seen.has(id)) continue;
          seen.add(id);
          const character = byId.get(id);
          if (!character) continue;

          if (!character.online) {
            const sys = appendLine({
              threadId,
              from: "system",
              kind: "system",
              text: `${character.name} needs ${character.envVar || "a key"} on the server (${character.note}).`,
              catalog,
            });
            send("message", { message: sys });
            committed.push(sys);
            continue;
          }

          const ordered = attemptsFor(process.env, id, character);
          if (!ordered.length) {
            if (character.id === "operator") {
              const opMsg = appendLine({
                threadId,
                from: "operator",
                text: "The log has it. Model keys are unset on this server right now.",
                catalog,
              });
              send("message", { message: opMsg });
              committed.push(opMsg);
              turns += 1;
            }
            continue;
          }

          turns += 1;
          send("typing", { from: id });

          const contextLines = committed.length
            ? `${text}\n\n(Just said in this round:\n${committed
                .filter((m) => m.kind !== "system")
                .map((m) => `${m.name}: ${m.text}`)
                .join("\n")})`
            : text;

          let skipGoogle = false;
          let spoken = false;
          let lastError = "";

          for (const attempt of ordered) {
            if (skipGoogle && attempt.provider === "google") continue;
            let say = "";
            let thought = "";
            try {
              const useTools = attempt.provider === "google" && attempt.modelId !== GEMMA;
              const req = providerRequest({
                ...attempt,
                system: character.system,
                character,
                messages: modelMessages({ text: contextLines, attachments }),
                tools: useTools ? buildStandaloneTools({ threadId, from: id, known, catalog, token, send }) : undefined,
              });
              const result = await streamText({
                model: languageModel(attempt),
                system: req.system,
                messages: req.messages,
                tools: req.tools,
                providerOptions: req.providerOptions,
                stopWhen: useTools ? stepCountIs(3) : undefined,
                maxRetries: 0,
              });
              for await (const part of result.fullStream) {
                if (part.type === "error") throw part.error;
                if (part.type === "text-delta") {
                  say += part.text || "";
                  send("delta", { from: id, text: part.text || "" });
                } else if (part.type === "reasoning-delta") {
                  thought += part.text || "";
                }
              }
            } catch (err) {
              lastError = String((err && err.message) || "model error");
              if (!say.trim()) {
                if (isRateLimit(err)) continue;
                if (attempt.provider === "google") {
                  skipGoogle = true;
                  continue;
                }
                break;
              }
            }
            if (!say.trim()) continue;
            const summary = summaryOnly(thought);
            if (summary) send("reasoning", { from: id, summary });
            const msg = appendLine({
              threadId,
              from: id,
              text: say.trim(),
              reasoning: summary,
              catalog,
            });
            committed.push(msg);
            send("message", { message: msg });
            await mirrorVm(token, "POST", `/v1/threads/${threadId}/lines`, {
              from: id, kind: "message", text: say.trim(), reasoning: summary,
            });
            if (thread.group) {
              for (const m of parseMentions(say, known)) {
                if (!seen.has(m)) queue.push(m);
              }
            }
            spoken = true;
            break;
          }

          if (!spoken) {
            const errMsg = appendLine({
              threadId,
              from: "system",
              kind: "system",
              text: `${character.name} couldn't reach its model right now${lastError ? ` (${lastError.slice(0, 80)})` : ""}.`,
              catalog,
            });
            send("message", { message: errMsg });
            committed.push(errMsg);
          }
        }

        if (!committed.some((m) => m.kind !== "system") && hasVmBackend()) {
          await mirrorVm(token, "POST", `/v1/threads/${threadId}/respond`, {});
          send("fallback", { reason: "vm" });
        }

        send("done", { via: committed.some((m) => m.kind !== "system") ? "gemini" : "fallback", messages: committed });
      } catch {
        send("fallback", { reason: "error" });
        send("done", { via: "error", messages: [] });
      }
      controller.close();
    },
  });

  return new Response(stream, {
    headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" },
  });
}
