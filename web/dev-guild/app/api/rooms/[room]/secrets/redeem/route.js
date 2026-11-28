import { readJsonLimited } from "../../../../../../lib/secrets/guard.js";
import { agentIdentity, json, roomContext, secretError } from "../../../../../../lib/secrets/http.js";
import { redeemHandle } from "../../../../../../lib/secrets/vault.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// POST /api/rooms/:room/secrets/redeem {handle}
// Returns the raw value ONLY when the owner granted this agent reveal:true.
// Otherwise agents use handles through server-side tools and never see values.
export async function POST(request, { params }) {
  try {
    const { room } = await params;
    const { db, token } = roomContext(request, "secrets-redeem", { limit: 30 });
    const body = await readJsonLimited(request);
    const agentId = await agentIdentity(db, token, room);
    return json(await redeemHandle(db, body.handle, { agentId }));
  } catch (err) {
    return secretError(err);
  }
}
