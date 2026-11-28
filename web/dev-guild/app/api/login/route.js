import { timingSafeEqual } from "crypto";
import { NextResponse } from "next/server";
import {
  OWNER_SESSION_SECONDS,
  clearLoginFailures,
  cookieOptions,
  getOwnerToken,
  loginClientKey,
  loginRetryAfter,
  recordLoginFailure,
  sealOwner,
} from "../../../lib/auth.js";
import { isDemoMode } from "../../../lib/demo.js";
import { GuardError, assertSameOrigin } from "../../../lib/secrets/guard.js";

export const dynamic = "force-dynamic";

export async function POST(request) {
  if (isDemoMode()) {
    return NextResponse.json({ error: "Sign-in is disabled in public demo mode." }, { status: 401 });
  }
  const token = getOwnerToken();
  if (!token) {
    return NextResponse.json({ error: "The server has no owner passphrase yet." }, { status: 503 });
  }
  try {
    // Refuse cross-site sign-in posts (login CSRF); scripts without an Origin still work.
    assertSameOrigin(request, { cookieAuth: false });
  } catch (err) {
    if (err instanceof GuardError) return NextResponse.json({ error: err.message }, { status: err.status });
    throw err;
  }
  const client = loginClientKey(request);
  const retryAfter = loginRetryAfter(client);
  if (retryAfter) {
    return NextResponse.json(
      { error: "Too many sign-in attempts. Try again later." },
      { status: 429, headers: { "Retry-After": String(retryAfter) } },
    );
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
    recordLoginFailure(client);
    return NextResponse.json({ error: "That passphrase doesn't open the guild." }, { status: 401 });
  }
  clearLoginFailures(client);
  const res = NextResponse.json({ ok: true, role: "owner" });
  res.cookies.set("guild_session", sealOwner(token), cookieOptions(OWNER_SESSION_SECONDS));
  return res;
}
