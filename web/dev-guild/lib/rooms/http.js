import { NextResponse } from "next/server";
import pg from "pg";
import { RoomApiError, unavailable } from "./errors.js";

// The identity and room API requires the durable store. With GUILD_STORE
// unset (memory demo) every room route answers 503 rather than pretending.
export function roomDb(env = process.env) {
  if (String(env.GUILD_STORE || "").trim().toLowerCase() !== "postgres") {
    throw unavailable("durable rooms need GUILD_STORE=postgres");
  }
  const connectionString = String(env.DATABASE_URL || "").trim();
  if (!connectionString) throw unavailable("durable rooms need DATABASE_URL");
  if (!globalThis.__guildRoomPool) {
    globalThis.__guildRoomPool = new pg.Pool({ connectionString, max: 4, idleTimeoutMillis: 10_000 });
  }
  return globalThis.__guildRoomPool;
}

export function bearer(request) {
  const header = request.headers.get("authorization") || "";
  const m = /^Bearer\s+(\S+)$/i.exec(header);
  return m ? m[1] : "";
}

export function apiError(err) {
  if (err instanceof RoomApiError) {
    return NextResponse.json({ error: err.message }, { status: err.status });
  }
  console.error("room api error:", err && err.code ? err.code : err && err.name);
  return NextResponse.json({ error: "internal error" }, { status: 500 });
}

export async function readJson(request) {
  try {
    const body = await request.json();
    return body && typeof body === "object" && !Array.isArray(body) ? body : {};
  } catch {
    return {};
  }
}
