import { createHmac, timingSafeEqual } from "crypto";
import { generateText, streamText } from "ai";
import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { completeWithGateway, deliverOwnerMessage, gatewayModel } from "../../../../lib/gateway.js";

export const dynamic = "force-dynamic";

function allowed(got, token) {
  if (!token || !got) return false;
  const want = createHmac("sha256", token).update("dev-guild-owner").digest("hex");
  const a = Buffer.from(got);
  const b = Buffer.from(want);
  return a.length === b.length && timingSafeEqual(a, b);
}

function backendFetch(token) {
  const root = new URL(process.env.GUILD_BACKEND_URL || "http://127.0.0.1:8790");
  return async (method, path, body) => {
    const target = new URL(String(path || "").replace(/^\//, ""), new URL(root.origin + "/"));
    if (target.origin !== root.origin || target.username || target.password) {
      const error = new Error("bad path");
      error.status = 400;
      throw error;
    }
    const init = {
      method,
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    };
    if (body !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    const res = await fetch(target, init);
    const text = await res.text();
    let data = {};
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = { error: text };
      }
    }
    if (!res.ok) {
      const error = new Error(data.error || "The guild bridge refused the request.");
      error.status = res.status;
      throw error;
    }
    return data;
  };
}

async function proxy(request, { params }) {
  const token = process.env.GUILD_OWNER_TOKEN || "";
  const jar = await cookies();
  if (!allowed(jar.get("guild_session")?.value || "", token)) {
    return NextResponse.json({ error: "Sign in again." }, { status: 401 });
  }
  const parts = (await params).path || [];
  if (!parts.every((part) => /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(part))) {
    return NextResponse.json({ error: "bad path" }, { status: 400 });
  }
  if (request.method === "POST" && parts.at(-1) === "messages") {
    return postWithGateway(request, token, parts);
  }
  const root = new URL(process.env.GUILD_BACKEND_URL || "http://127.0.0.1:8790");
  const target = new URL(parts.join("/"), new URL(root.origin + "/"));
  if (target.origin !== root.origin) {
    return NextResponse.json({ error: "bad path" }, { status: 400 });
  }
  const incoming = new URL(request.url);
  const after = incoming.searchParams.get("after");
  if (after !== null) {
    if (!/^[0-9]{1,12}$/.test(after)) {
      return NextResponse.json({ error: "bad path" }, { status: 400 });
    }
    target.searchParams.set("after", after);
  }
  const headers = { Authorization: `Bearer ${token}` };
  const init = { method: request.method, headers, cache: "no-store" };
  if (request.method !== "GET" && request.method !== "HEAD") {
    headers["Content-Type"] = "application/json";
    init.body = await request.text();
  }
  let upstream;
  try {
    upstream = await fetch(target, init);
  } catch {
    return NextResponse.json({ error: "The guild bridge on the VM isn't reachable." }, { status: 502 });
  }
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": upstream.headers.get("content-type") || "application/json" },
  });
}

async function postWithGateway(request, token, parts) {
  let body = {};
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "bad json" }, { status: 400 });
  }
  const threadId = parts.at(-2);
  try {
    const result = await deliverOwnerMessage({
      threadId,
      text: body.text || "",
      apiKey: process.env.AI_GATEWAY_API_KEY || "",
      model: gatewayModel(),
      backend: backendFetch(token),
      complete: (job) =>
        completeWithGateway({
          ...job,
          generateText,
          streamText,
        }),
    });
    return NextResponse.json({ pending: true, via: result.via });
  } catch (err) {
    const status = err.status || 502;
    return NextResponse.json({ error: err.message || "The guild bridge isn't reachable." }, { status });
  }
}

export const GET = proxy;
export const POST = proxy;
