import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import {
  getGroupName,
  getOwnerName,
  getOwnerToken,
  isOwnerCookie,
  readGroupPrefs,
  readInviteSession,
} from "../../../lib/auth.js";
import {
  clientIpFromRequest,
  isDemoMode,
  isLiveDemo,
  peekDemoRateLimit,
} from "../../../lib/demo.js";
import { mergeGroupPrefs } from "../../../lib/store.js";

export const dynamic = "force-dynamic";

export async function GET(request) {
  if (isDemoMode()) {
    const demoJar = await cookies();
    const rl = peekDemoRateLimit(clientIpFromRequest(request), { cookieVal: demoJar.get("guild_demo_rl")?.value });
    return NextResponse.json({
      ok: true,
      demo: true,
      liveDemo: isLiveDemo(),
      role: "demo",
      participant: { id: "owner", name: "You" },
      group: { title: process.env.GUILD_GROUP_NAME || "Dev Guild Demo", setupDone: true },
      rateLimit: { limit: rl.limit, remaining: rl.remaining, retryAfterSeconds: rl.retryAfterSeconds },
    });
  }
  const token = getOwnerToken();
  if (!token) return NextResponse.json({ ok: false }, { status: 503 });
  const jar = await cookies();
  const prefs = mergeGroupPrefs(readGroupPrefs(jar.get("guild_group_prefs")?.value || "", token));
  if (isOwnerCookie(jar.get("guild_session")?.value || "", token)) {
    return NextResponse.json({
      ok: true,
      role: "owner",
      participant: { id: "owner", name: prefs.ownerName || getOwnerName() },
      group: { title: prefs.title || getGroupName(), setupDone: Boolean(prefs.setupDone) },
    });
  }
  const invite = readInviteSession(jar.get("guild_invite")?.value || "", token);
  if (invite) {
    return NextResponse.json({
      ok: true,
      role: "invite",
      participant: invite,
      group: { title: prefs.title || getGroupName(), setupDone: true },
    });
  }
  return NextResponse.json({ ok: false });
}
