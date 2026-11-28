import { rejected } from "../rooms/errors.js";
import { addSecret, announceCard, applySync, grantAgent, planSync, revokeGrant, revokeSecret, rotateSecret, verifySecret } from "./vault.js";

// One dispatcher for POST /secrets/:id {action, ...} so the durable-room and
// owner-cookie routes behave identically.
export async function runSecretAction(db, auth, roomId, secretId, body = {}) {
  switch (body.action) {
    case "rotate":
      return { card: await rotateSecret(db, auth, roomId, secretId, { value: body.value }) };
    case "revoke":
      return { card: await revokeSecret(db, auth, roomId, secretId) };
    case "verify":
      return verifySecret(db, auth, roomId, secretId);
    case "grant":
      return { grant: await grantAgent(db, auth, roomId, secretId, body) };
    case "revoke-grant":
      return revokeGrant(db, auth, roomId, secretId, body.grantId);
    case "plan-sync":
      return { plan: await planSync(db, auth, roomId, secretId, body) };
    case "apply-sync":
      return { sync: await applySync(db, auth, roomId, secretId, body) };
    default:
      throw rejected("unknown action");
  }
}

export async function addAndAnnounce(db, auth, roomId, body, { announce } = {}) {
  const card = await addSecret(db, auth, roomId, body);
  if (announce) await announce(card);
  else if (roomId !== "guild") await announceCard(db, roomId, card.createdBy, card);
  return card;
}
