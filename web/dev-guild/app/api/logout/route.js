import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { getOwnerToken, readInviteSessionIds, readOwnerSession, revokeAllSessions, revokeSession } from "../../../lib/auth.js";
import { GuardError, assertSameOrigin } from "../../../lib/secrets/guard.js";

export const dynamic = "force-dynamic";

// POST /api/logout revokes this browser's session on the server.
// POST /api/logout {"everywhere": true} (owner only) revokes every owner and
// invite session issued so far. Changing GUILD_SESSION_EPOCH does the same
// and also rotates the invite code.
export async function POST(request) {
  const token = getOwnerToken();
  const jar = await cookies();
  let body = {};
  try {
    body = (await request.json()) || {};
  } catch {
    body = {};
  }
  let everywhere = false;
  if (token) {
    const owner = readOwnerSession(jar.get("guild_session")?.value || "", token);
    if (owner) {
      revokeSession(owner.sid, owner.exp);
      if (body.everywhere === true) {
        try {
          assertSameOrigin(request);
        } catch (err) {
          if (err instanceof GuardError) return NextResponse.json({ error: err.message }, { status: err.status });
          throw err;
        }
        revokeAllSessions();
        everywhere = true;
      }
    }
    const invite = readInviteSessionIds(jar.get("guild_invite")?.value || "", token);
    if (invite) revokeSession(invite.sid, invite.exp);
  }
  const res = NextResponse.json({ ok: true, everywhere });
  res.cookies.set("guild_session", "", { httpOnly: true, path: "/", maxAge: 0 });
  res.cookies.set("guild_invite", "", { httpOnly: true, path: "/", maxAge: 0 });
  return res;
}
