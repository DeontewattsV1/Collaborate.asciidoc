import { createHmac, timingSafeEqual } from "crypto";
import { cookies } from "next/headers";
import { NextResponse } from "next/server";

export const dynamic = "force-dynamic";

export async function GET() {
  const token = process.env.GUILD_OWNER_TOKEN || "";
  if (!token) return NextResponse.json({ ok: false }, { status: 503 });
  const jar = await cookies();
  const got = jar.get("guild_session")?.value || "";
  const want = createHmac("sha256", token).update("dev-guild-owner").digest("hex");
  const a = Buffer.from(got);
  const b = Buffer.from(want);
  const ok = a.length === b.length && timingSafeEqual(a, b);
  return NextResponse.json({ ok });
}
