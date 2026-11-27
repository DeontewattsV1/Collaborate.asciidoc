import { NextResponse } from "next/server";
import { grantMembership, revokeMembership } from "../../../../../lib/rooms/authz.js";
import { apiError, bearer, readJson, roomDb } from "../../../../../lib/rooms/http.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// POST /api/rooms/:room/members {identity, role: "member"|"viewer"} — owners only.
export async function POST(request, { params }) {
  try {
    const { room } = await params;
    const body = await readJson(request);
    const out = await grantMembership(roomDb(), bearer(request), room, { identityId: body.identity, role: body.role });
    return NextResponse.json(out);
  } catch (err) {
    return apiError(err);
  }
}

// DELETE /api/rooms/:room/members {identity} — owners only.
export async function DELETE(request, { params }) {
  try {
    const { room } = await params;
    const body = await readJson(request);
    return NextResponse.json(await revokeMembership(roomDb(), bearer(request), room, { identityId: body.identity }));
  } catch (err) {
    return apiError(err);
  }
}
