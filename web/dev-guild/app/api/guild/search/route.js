import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { checkGuildAuth, getOwnerToken } from "../../../../lib/auth.js";
import { searchHistory } from "../../../../lib/models.js";
import { getMessages, hasVmBackend, vmFetch } from "../../../../lib/store.js";

export const dynamic = "force-dynamic";

export async function GET(request) {
  const token = getOwnerToken();
  const jar = await cookies();
  const auth = checkGuildAuth({
    jar,
    token,
    authHeader: request.headers.get("authorization"),
  });
  if (!auth.ok) {
    return NextResponse.json({ error: auth.error }, { status: auth.status });
  }
  const url = new URL(request.url);
  const thread = url.searchParams.get("thread") || "guild";
  const q = url.searchParams.get("q") || "";
  if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(thread)) {
    return NextResponse.json({ error: "bad thread" }, { status: 400 });
  }
  let messages = [];
  if (hasVmBackend()) {
    try {
      const data = await vmFetch(token, "POST", "/v1/search", { thread });
      messages = data.messages || [];
    } catch {
      messages = getMessages(thread, 0);
    }
  } else {
    messages = getMessages(thread, 0);
  }
  const documents = messages.map((m) => ({ text: `${m.name}: ${m.text || m.task || ""}`, message: m }));
  const ranked = searchHistory({ query: q, documents });
  return NextResponse.json({
    via: ranked.via,
    messages: (ranked.documents || []).map((item) => item.message).filter(Boolean),
  });
}
