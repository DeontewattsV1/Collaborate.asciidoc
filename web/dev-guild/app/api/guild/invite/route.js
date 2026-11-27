import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { getOwnerToken, inviteCodeFor, isOwnerCookie, readGroupPrefs } from "../../../../lib/auth.js";
import { isDemoMode } from "../../../../lib/demo.js";
import { mergeGroupPrefs } from "../../../../lib/store.js";

export const dynamic = "force-dynamic";

export async function GET(request) {
  if (isDemoMode()) {
    return NextResponse.json({ error: "Invites are disabled in public demo mode." }, { status: 401 });
  }
  const token = getOwnerToken();
  if (!token) return NextResponse.json({ error: "Access passphrase unset." }, { status: 503 });
  const jar = await cookies();
  if (!isOwnerCookie(jar.get("guild_session")?.value || "", token)) {
    return NextResponse.json({ error: "Owner sign-in required." }, { status: 401 });
  }
  const prefs = await mergeGroupPrefs(readGroupPrefs(jar.get("guild_group_prefs")?.value || "", token));
  const code = inviteCodeFor(token);
  const origin = new URL(request.url).origin;
  return NextResponse.json({
    code,
    groupTitle: prefs.title,
    path: `/?invite=${code}`,
    url: `${origin}/?invite=${code}`,
    joinEndpoint: `${origin}/api/guild/invite/join`,
    messageEndpoint: `${origin}/api/guild/invite/message`,
  });
}
