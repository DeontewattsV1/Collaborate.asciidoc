import { createHmac, timingSafeEqual } from "crypto";
import { stepCountIs, streamText } from "ai";
import { cookies } from "next/headers";
import { attemptsFor, demoStreamEvents, isRateLimit, modelMessages, normalizeAttachments, providerRequest, summaryOnly } from "../../../../lib/models.js";
import { languageModel } from "../../../../lib/providers.js";
import { guildTools } from "../../../../lib/tools.js";

export const dynamic = "force-dynamic";

function allowed(got, token) {
  if (!token || !got) return false;
  const want = createHmac("sha256", token).update("dev-guild-owner").digest("hex");
  const a = Buffer.from(got);
  const b = Buffer.from(want);
  return a.length === b.length && timingSafeEqual(a, b);
}

function backend(token) {
  const root = new URL(process.env.GUILD_BACKEND_URL || "http://127.0.0.1:8790");
  return async (method, path, body) => {
    const target = new URL(String(path || "").replace(/^\//, ""), new URL(root.origin + "/"));
    if (target.origin !== root.origin || target.username || target.password) {
      const error = new Error("bad path");
      error.status = 400;
      throw error;
    }
    const init = { method, headers: { Authorization: `Bearer ${token}` }, cache: "no-store" };
    if (body !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    const res = await fetch(target, init);
    const text = await res.text();
    const data = text ? JSON.parse(text) : {};
    if (!res.ok) {
      const error = new Error(data.error || "bridge refused");
      error.status = res.status;
      throw error;
    }
    return data;
  };
}

function encode(event, data) {
  return `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
}

async function persist(api, threadId, events, files) {
  let say = "";
  let from = "";
  let summary = "";
  for (const event of events) {
    if (event.event === "delta") {
      say += event.data.text;
      from = event.data.from;
    }
    if (event.event === "reasoning") summary = summaryOnly(event.data.summary);
    if (event.event === "tool") {
      await api("POST", `/v1/threads/${threadId}/lines`, {
        from: event.data.from, kind: "tool", tool: event.data.name, text: event.data.summary,
      });
    }
    if (event.event === "card") {
      await api("POST", `/v1/threads/${threadId}/lines`, {
        from: event.data.from, kind: "card", text: event.data.task, task: event.data.task,
        assignee: event.data.assignee, card_status: event.data.status,
      });
    }
  }
  if (say && from) {
    await api("POST", `/v1/threads/${threadId}/lines`, {
      from, kind: "message", text: say.trim(), reasoning: summary, files,
    });
  }
}

export async function POST(request) {
  const token = process.env.GUILD_OWNER_TOKEN || "";
  const jar = await cookies();
  if (!allowed(jar.get("guild_session")?.value || "", token)) {
    return Response.json({ error: "Sign in again." }, { status: 401 });
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
  const api = backend(token);
  const files = attachments.map((item) => item.name);
  try {
    await api("POST", `/v1/threads/${threadId}/messages`, { text: text || "Sent an attachment.", reply: "hold", files });
  } catch (err) {
    return Response.json({ error: err.message }, { status: err.status || 502 });
  }

  const encoder = new TextEncoder();
  const stream = new ReadableStream({
    async start(controller) {
      const send = (event, data) => controller.enqueue(encoder.encode(encode(event, data)));
      try {
        const attempts = attemptsFor(process.env);
        const demo = process.env.GUILD_DEMO === "1";
        if (!attempts.length) {
          if (!demo) {
            await api("POST", `/v1/threads/${threadId}/respond`, {});
            send("fallback", { reason: "unset" });
            send("done", { via: "provider" });
            controller.close();
            return;
          }
          const cast = await api("GET", "/v1/characters");
          const mentioned = (text.match(/@([a-z0-9-]{2,32})/) || [])[1];
          const from = (cast.characters || []).some((c) => c.id === mentioned) ? mentioned : "claude";
          const events = demoStreamEvents({ from, userText: text });
          for (const event of events) {
            if (event.event === "done") continue;
            send(event.event, event.data);
          }
          await persist(api, threadId, events, []);
          send("done", { via: "demo" });
          controller.close();
          return;
        }
        const cast = await api("GET", "/v1/characters");
        const known = new Set((cast.characters || []).map((c) => c.id));
        const mentioned = (text.match(/@([a-z0-9-]{2,32})/) || [])[1];
        const character = (cast.characters || []).find((c) => c.id === mentioned) || (cast.characters || []).find((c) => c.id === "claude");
        const from = character ? character.id : "claude";
        send("typing", { from });
        const ordered = attemptsFor(process.env, from);
        let skipGoogle = false;
        let finished = false;
        for (const attempt of ordered) {
          if (skipGoogle && attempt.provider === "google") continue;
          let say = "";
          let thought = "";
          try {
            const request = providerRequest({
              ...attempt,
              system: character && character.system,
              character,
              messages: modelMessages({ text, attachments }),
              tools: guildTools({ backend: api, threadId, from, known }),
            });
            const result = await streamText({
              model: languageModel(attempt),
              system: request.system,
              messages: request.messages,
              tools: request.tools,
              providerOptions: request.providerOptions,
              stopWhen: stepCountIs(4),
              maxRetries: 0,
            });
            for await (const part of result.fullStream) {
              if (part.type === "error") throw part.error;
              if (part.type === "text-delta") {
                say += part.text || "";
                send("delta", { from, text: part.text || "" });
              } else if (part.type === "reasoning-delta") {
                thought += part.text || "";
              } else if (part.type === "tool-result") {
                const output = part.output || {};
                send("tool", { from, name: part.toolName, summary: output.summary || part.toolName });
                if (output.card) {
                  send("card", { from, task: output.card.task, assignee: output.card.assignee, status: output.card.status });
                }
              }
            }
          } catch (err) {
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
          if (summary) send("reasoning", { from, summary });
          await persist(api, threadId, [
            { event: "delta", data: { from, text: say } },
            { event: "reasoning", data: { from, summary } },
          ], []);
          send("done", { via: attempt.provider });
          finished = true;
          break;
        }
        if (!finished) {
          await api("POST", `/v1/threads/${threadId}/respond`, {});
          send("fallback", { reason: "error" });
          send("done", { via: "provider" });
        }
      } catch {
        send("fallback", { reason: "error" });
        send("done", { via: "provider" });
      }
      controller.close();
    },
  });
  return new Response(stream, {
    headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" },
  });
}
