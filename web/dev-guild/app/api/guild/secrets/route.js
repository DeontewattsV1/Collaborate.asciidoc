import { cookies } from "next/headers";
import { appendLine } from "../../../../lib/store.js";
import { addAndAnnounce } from "../../../../lib/secrets/actions.js";
import { readJsonLimited } from "../../../../lib/secrets/guard.js";
import { json, ownerContext, secretError } from "../../../../lib/secrets/http.js";
import { listSecrets, publicCard } from "../../../../lib/secrets/vault.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// The owner-cookie chat's vault. Needs GUILD_STORE=postgres, DATABASE_URL and
// SECRETS_MASTER_KEY; otherwise it answers 503 and stores nothing.
export async function GET(request) {
  try {
    const { db, auth, roomId } = ownerContext(request, await cookies(), "secrets-read", { limit: 120 });
    return json({ secrets: await listSecrets(db, auth, roomId) });
  } catch (err) {
    return secretError(err);
  }
}

export async function POST(request) {
  try {
    const { db, auth, roomId } = ownerContext(request, await cookies(), "secrets-write", { limit: 20 });
    const body = await readJsonLimited(request);
    const threadId = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(String(body.thread || "")) ? body.thread : "guild";
    const card = await addAndAnnounce(db, auth, roomId, body, {
      announce: (c) => appendLine({
        threadId,
        from: "owner",
        text: `Added secret ${c.name} (${c.providerLabel}${c.last4 ? ` …${c.last4}` : ""})`,
        secret: publicCard(c),
      }),
    });
    return json({ secret: card }, 201);
  } catch (err) {
    return secretError(err);
  }
}
