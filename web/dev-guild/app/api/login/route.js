import { timingSafeEqual } from "crypto";
import { NextResponse } from "next/server";
import { cookieOptions, getOwnerToken, sealOwner } from "../../../lib/auth.js";
import { isDemoMode } from "../../../lib/demo.js";

export const dynamic = "force-dynamic";

export async function POST(request) {
  if (isDemoMode()) {
    return NextResponse.json({ error: "Sign-in is disabled in public demo mode." }, { status: 401 });
  }
  const token = getOwnerToken();
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
  const res = NextResponse.json({ ok: true, role: "owner" });
  res.cookies.set("guild_session", sealOwner(token), cookieOptions());
  return res;
}
