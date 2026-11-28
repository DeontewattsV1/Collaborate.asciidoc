import { readJsonLimited } from "../../../../../../../lib/secrets/guard.js";
import { agentIdentity, json, roomContext, secretError } from "../../../../../../../lib/secrets/http.js";
import { agentAuth, issueHandle } from "../../../../../../../lib/secrets/vault.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// POST /api/rooms/:room/secrets/:secret/handle {scope?}
// The caller is an agent identity that is a member of the room and holds a
// live grant. It gets a single-use handle that expires in five minutes.
export async function POST(request, { params }) {
  try {
    const { room, secret } = await params;
    const { db, token } = roomContext(request, "secrets-handle", { limit: 60 });
    const body = await readJsonLimited(request);
    const agentId = await agentIdentity(db, token, room);
    return json(await issueHandle(db, agentAuth(agentId), room, secret, { scope: body.scope || "" }), 201);
  } catch (err) {
    return secretError(err);
  }
}
