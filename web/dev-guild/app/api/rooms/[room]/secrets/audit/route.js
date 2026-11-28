import { json, roomContext, secretError } from "../../../../../../lib/secrets/http.js";
import { listAudit } from "../../../../../../lib/secrets/vault.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// GET /api/rooms/:room/secrets/audit — owner only; values are never logged.
export async function GET(request, { params }) {
  try {
    const { room } = await params;
    const { db, auth } = roomContext(request, "secrets-read", { limit: 60 });
    return json({ audit: await listAudit(db, auth, room) });
  } catch (err) {
    return secretError(err);
  }
}
