import { NextResponse } from "next/server";
import { createRoom } from "../../../lib/rooms/authz.js";
import { apiError, bearer, readJson, roomDb } from "../../../lib/rooms/http.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// POST /api/rooms {title} — the authenticated identity becomes the owner.
export async function POST(request) {
  try {
    const body = await readJson(request);
    const room = await createRoom(roomDb(), bearer(request), { title: body.title });
    return NextResponse.json(room, { status: 201 });
  } catch (err) {
    return apiError(err);
  }
}
