import { readAttachment } from "../../../../../../lib/rooms/authz.js";
import { apiError, bearer, roomDb } from "../../../../../../lib/rooms/http.js";
import { storageFromEnv } from "../../../../../../lib/rooms/storage.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// GET /api/rooms/:room/attachments/:file — membership check, then the bytes
// are re-hashed and compared with the recorded SHA-256 before being returned.
export async function GET(request, { params }) {
  try {
    const { room, file } = await params;
    const storage = await storageFromEnv();
    if (!storage) return Response.json({ error: "private storage not configured" }, { status: 503 });
    const out = await readAttachment(roomDb(), bearer(request), room, file, storage);
    return new Response(out.data, {
      status: 200,
      headers: {
        "Content-Type": "application/octet-stream",
        "Content-Disposition": `attachment; filename="${out.name.replace(/"/g, "")}"`,
        "X-Content-Type-Options": "nosniff",
        "Cache-Control": "private, no-store",
        "X-Content-SHA256": out.sha256,
      },
    });
  } catch (err) {
    return apiError(err);
  }
}
