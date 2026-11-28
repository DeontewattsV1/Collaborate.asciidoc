import { addAndAnnounce } from "../../../../../lib/secrets/actions.js";
import { readJsonLimited } from "../../../../../lib/secrets/guard.js";
import { json, roomContext, secretError } from "../../../../../lib/secrets/http.js";
import { listSecrets } from "../../../../../lib/secrets/vault.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// GET /api/rooms/:room/secrets — redacted cards (grants only for owners).
export async function GET(request, { params }) {
  try {
    const { room } = await params;
    const { db, auth } = roomContext(request, "secrets-read", { limit: 120 });
    return json({ secrets: await listSecrets(db, auth, room) });
  } catch (err) {
    return secretError(err);
  }
}

// POST /api/rooms/:room/secrets — owner adds a secret. The value is sealed
// immediately and never returned.
export async function POST(request, { params }) {
  try {
    const { room } = await params;
    const { db, auth } = roomContext(request, "secrets-write", { limit: 20 });
    const body = await readJsonLimited(request);
    return json({ secret: await addAndAnnounce(db, auth, room, body) }, 201);
  } catch (err) {
    return secretError(err);
  }
}
