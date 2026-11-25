import { createHmac, timingSafeEqual } from "crypto";
import { streamText } from "ai";
import { cookies } from "next/headers";
import { demoStreamEvents, gatewayModel, gatewayRequest, modelMessages, normalizeAttachments, summaryOnly } from "../../../../lib/gateway.js";

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
        const key = process.env.AI_GATEWAY_API_KEY || "";
        const demo = process.env.GUILD_DEMO === "1";
        if (!key) {
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
        const mentioned = (text.match(/@([a-z0-9-]{2,32})/) || [])[1];
        const character = (cast.characters || []).find((c) => c.id === mentioned) || (cast.characters || []).find((c) => c.id === "claude");
        const from = character ? character.id : "claude";
        send("typing", { from });
        let result;
        try {
          result = await streamText(gatewayRequest({
            model: gatewayModel(),
            system: character && character.system,
            character,
            messages: modelMessages({ text, attachments }),
          }));
        } catch {
          await api("POST", `/v1/threads/${threadId}/respond`, {});
          send("fallback", { reason: "error" });
          send("done", { via: "provider" });
          controller.close();
          return;
        }
        let say = "";
        if (result && result.textStream) {
          for await (const chunk of result.textStream) {
            say += chunk;
            send("delta", { from, text: chunk });
          }
        }
        if (!say.trim()) {
          await api("POST", `/v1/threads/${threadId}/respond`, {});
          send("fallback", { reason: "empty" });
          send("done", { via: "provider" });
          controller.close();
          return;
        }
        await persist(api, threadId, [
          { event: "delta", data: { from, text: say } },
        ], []);
        send("done", { via: "gateway" });
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
