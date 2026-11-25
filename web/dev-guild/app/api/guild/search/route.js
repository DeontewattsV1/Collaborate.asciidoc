import { createHmac, timingSafeEqual } from "crypto";
import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { searchHistory } from "../../../../lib/gateway.js";

export const dynamic = "force-dynamic";

function allowed(got, token) {
  if (!token || !got) return false;
  const want = createHmac("sha256", token).update("dev-guild-owner").digest("hex");
  const a = Buffer.from(got);
  const b = Buffer.from(want);
  return a.length === b.length && timingSafeEqual(a, b);
}

export async function GET(request) {
  const token = process.env.GUILD_OWNER_TOKEN || "";
  const jar = await cookies();
  if (!allowed(jar.get("guild_session")?.value || "", token)) {
    return NextResponse.json({ error: "Sign in again." }, { status: 401 });
  }
  const url = new URL(request.url);
  const thread = url.searchParams.get("thread") || "guild";
  const q = url.searchParams.get("q") || "";
  if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(thread)) {
    return NextResponse.json({ error: "bad thread" }, { status: 400 });
  }
  const base = process.env.GUILD_BACKEND_URL || "http://127.0.0.1:8790";
  const root = new URL(base);
  const target = new URL("/v1/search", root);
  if (target.origin !== root.origin) {
    return NextResponse.json({ error: "bad backend" }, { status: 500 });
  }
  const res = await fetch(target, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ thread }),
    cache: "no-store",
  });
  const data = await res.json();
  const documents = (data.messages || []).map((m) => ({ text: `${m.name}: ${m.text || m.task || ""}`, message: m }));
  const ranked = await searchHistory({
    apiKey: process.env.AI_GATEWAY_API_KEY || "",
    query: q,
    documents,
    fetchImpl: fetch,
  });
  return NextResponse.json({
    via: ranked.ok ? "rerank" : "recent",
    messages: (ranked.documents || []).map((item) => item.message).filter(Boolean),
  });
}
