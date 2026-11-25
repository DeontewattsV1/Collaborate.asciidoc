import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { getOwnerToken, isOwnerCookie, readInviteSession } from "../../../lib/auth.js";

export const dynamic = "force-dynamic";

export async function GET() {
  const token = getOwnerToken();
  if (!token) return NextResponse.json({ ok: false }, { status: 503 });
  const jar = await cookies();
  if (isOwnerCookie(jar.get("guild_session")?.value || "", token)) {
    return NextResponse.json({ ok: true, role: "owner", participant: { id: "owner", name: "Deonte" } });
  }
  const invite = readInviteSession(jar.get("guild_invite")?.value || "", token);
  if (invite) {
    return NextResponse.json({ ok: true, role: "invite", participant: invite });
  }
  return NextResponse.json({ ok: false });
}
