import { cookies } from "next/headers";
import { json, ownerContext, secretError } from "../../../../../lib/secrets/http.js";
import { listAudit } from "../../../../../lib/secrets/vault.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request) {
  try {
    const { db, auth, roomId } = ownerContext(request, await cookies(), "secrets-read", { limit: 60 });
    return json({ audit: await listAudit(db, auth, roomId) });
  } catch (err) {
    return secretError(err);
  }
}
