import { createHmac, timingSafeEqual } from "crypto";
import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

function seal(token) {
  return createHmac("sha256", token).update("dev-guild-owner").digest("hex");
}

export async function POST(request) {
  const token = process.env.GUILD_OWNER_TOKEN || "";
  if (!token) {
    return NextResponse.json({ error: "The server has no owner passphrase yet." }, { status: 503 });
  }
  let body = {};
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "Say the passphrase." }, { status: 400 });
  }
  const given = String(body.passphrase || "");
  const a = Buffer.from(given);
  const b = Buffer.from(token);
  const match = a.length === b.length && timingSafeEqual(a, b);
  if (!match) {
    return NextResponse.json({ error: "That passphrase doesn't open the guild." }, { status: 401 });
  }
  const res = NextResponse.json({ ok: true });
  res.cookies.set("guild_session", seal(token), {
    httpOnly: true,
    sameSite: "lax",
    secure: process.env.NODE_ENV === "production",
    path: "/",
    maxAge: 60 * 60 * 24 * 30,
  });
  return res;
}
