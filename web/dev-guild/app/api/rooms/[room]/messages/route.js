import { NextResponse } from "next/server";
import { listRoomMessages, sendMessage } from "../../../../../lib/rooms/authz.js";
import { apiError, bearer, readJson, roomDb } from "../../../../../lib/rooms/http.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// GET /api/rooms/:room/messages?after=<seq> — up to 200 messages after the cursor.
export async function GET(request, { params }) {
  try {
    const { room } = await params;
    const after = new URL(request.url).searchParams.get("after") || "0";
    if (!/^\d{1,15}$/.test(after)) return NextResponse.json({ error: "invalid cursor" }, { status: 400 });
    return NextResponse.json(await listRoomMessages(roomDb(), bearer(request), room, { after: Number(after) }));
  } catch (err) {
    return apiError(err);
  }
}

// POST /api/rooms/:room/messages {text, files?} with optional Idempotency-Key.
// The sender is the authenticated identity; any "from" in the body is ignored.
export async function POST(request, { params }) {
  try {
    const { room } = await params;
    const body = await readJson(request);
    const idempotencyKey = request.headers.get("idempotency-key") || undefined;
    const msg = await sendMessage(roomDb(), bearer(request), room, {
      text: body.text,
      files: body.files || [],
      idempotencyKey,
    });
    return NextResponse.json(msg, { status: msg.duplicate ? 200 : 201 });
  } catch (err) {
    return apiError(err);
  }
}
