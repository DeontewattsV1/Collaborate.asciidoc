import { createHmac, timingSafeEqual } from "crypto";
import { cookies } from "next/headers";
import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

function allowed(got, token) {
  if (!token || !got) return false;
  const want = createHmac("sha256", token).update("dev-guild-owner").digest("hex");
  const a = Buffer.from(got);
  const b = Buffer.from(want);
  return a.length === b.length && timingSafeEqual(a, b);
}

async function proxy(request, { params }) {
  const token = process.env.GUILD_OWNER_TOKEN || "";
  const jar = await cookies();
  if (!allowed(jar.get("guild_session")?.value || "", token)) {
    return NextResponse.json({ error: "Sign in again." }, { status: 401 });
  }
  const base = (process.env.GUILD_BACKEND_URL || "http://127.0.0.1:8790").replace(/\/$/, "");
  const parts = (await params).path || [];
  const url = new URL(request.url);
  const target = `${base}/${parts.join("/")}${url.search}`;
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

export const GET = proxy;
export const POST = proxy;
