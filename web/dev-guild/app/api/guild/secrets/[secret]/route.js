import { cookies } from "next/headers";
import { runSecretAction } from "../../../../../lib/secrets/actions.js";
import { readJsonLimited } from "../../../../../lib/secrets/guard.js";
import { json, ownerContext, secretError } from "../../../../../lib/secrets/http.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(request, { params }) {
  try {
    const { secret } = await params;
    const { db, auth, roomId } = ownerContext(request, await cookies(), "secrets-write", { limit: 20 });
    return json(await runSecretAction(db, auth, roomId, secret, await readJsonLimited(request)));
  } catch (err) {
    return secretError(err);
  }
}
