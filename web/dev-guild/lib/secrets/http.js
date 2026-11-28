import { NextResponse } from "next/server";
import { checkGuildAuth, getOwnerToken } from "../auth.js";
import { identityFor, requireRole, ROLES, withTx } from "../rooms/authz.js";
import { RoomApiError, unauthorized } from "../rooms/errors.js";
import { bearer, roomDb } from "../rooms/http.js";
import { GuardError, NO_STORE, assertSameOrigin, clientKey, rateLimit } from "./guard.js";
import { VaultConfigError } from "./crypto.js";
import { bearerAuth, ownerCookieAuth } from "./vault.js";

// Route plumbing for the secret API. Every response is no-store and errors
// are generic; nothing here ever echoes a request body.

export function json(data, status = 200, headers = {}) {
  return NextResponse.json(data, { status, headers: { ...NO_STORE, ...headers } });
}

export function secretError(err) {
  if (err instanceof RoomApiError || err instanceof GuardError || err instanceof VaultConfigError) {
    return json({ error: err.message }, err.status, err.headers || {});
  }
  if (err && Number.isInteger(err.status) && err.status >= 400 && err.status < 600 && typeof err.message === "string") {
    return json({ error: err.message }, err.status);
  }
  console.error("secret api error:", err && err.code ? err.code : err && err.name);
  return json({ error: "internal error" }, 500);
}

// guard runs the origin check and the rate limit for one route.
export function guard(request, bucket, { cookieAuth, limit = 30 } = {}) {
  assertSameOrigin(request, { cookieAuth });
  rateLimit(`${bucket}:${clientKey(request)}`, { limit });
}

// Durable rooms: per-identity bearer tokens.
export function roomContext(request, bucket, opts = {}) {
  const token = bearer(request);
  if (!token) throw unauthorized();
  guard(request, bucket, { cookieAuth: false, ...opts });
  return { db: roomDb(), auth: bearerAuth(token), token };
}

// The owner-cookie chat ("guild" room). Only the owner may touch the vault.
export function ownerContext(request, jar, bucket, opts = {}) {
  const auth = checkGuildAuth({ jar, token: getOwnerToken(), authHeader: request.headers.get("authorization") });
  if (!auth.ok) throw new RoomApiError(auth.status, auth.error);
  if (auth.role !== "owner") throw new RoomApiError(403, "only the owner can manage secrets");
  const cookieAuth = !request.headers.get("authorization");
  guard(request, bucket, { cookieAuth, ...opts });
  return { db: roomDb(), auth: ownerCookieAuth("guild"), roomId: "guild" };
}

// An agent identity calling for itself must also be a member of the room.
export async function agentIdentity(db, token, roomId) {
  return withTx(db, async (client) => {
    const { identity } = await requireRole(client, token, roomId, ROLES.read);
    return identity.id;
  });
}

export async function identityOnly(db, token) {
  return withTx(db, async (client) => (await identityFor(client, token)).id);
}
