import { runSecretAction } from "../../../../../../lib/secrets/actions.js";
import { readJsonLimited } from "../../../../../../lib/secrets/guard.js";
import { json, roomContext, secretError } from "../../../../../../lib/secrets/http.js";
import { revokeSecret } from "../../../../../../lib/secrets/vault.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// POST /api/rooms/:room/secrets/:secret {action: rotate|revoke|verify|grant|revoke-grant|plan-sync|apply-sync}
export async function POST(request, { params }) {
  try {
    const { room, secret } = await params;
    const { db, auth } = roomContext(request, "secrets-write", { limit: 20 });
    return json(await runSecretAction(db, auth, room, secret, await readJsonLimited(request)));
  } catch (err) {
    return secretError(err);
  }
}

// DELETE /api/rooms/:room/secrets/:secret — revoke and crypto-shred.
export async function DELETE(request, { params }) {
  try {
    const { room, secret } = await params;
    const { db, auth } = roomContext(request, "secrets-write", { limit: 20 });
    return json({ card: await revokeSecret(db, auth, room, secret) });
  } catch (err) {
    return secretError(err);
  }
}
