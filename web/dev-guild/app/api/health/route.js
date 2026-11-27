import { NextResponse } from "next/server";
import { scannerFromEnv } from "../../../lib/rooms/scanner.js";
import { storageConfigFromEnv } from "../../../lib/rooms/storage.js";
import { roomStore, storeKind } from "../../../lib/store.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

// GET /api/health — liveness plus which durable features are configured.
// Reports names only; never secrets or connection strings.
export async function GET() {
  const store = storeKind();
  let db = "not configured";
  if (store === "postgres") {
    try {
      const h = await roomStore().health();
      db = h && h.ok === false ? "error" : "ok";
    } catch {
      db = "error";
    }
  }
  const uploads = scannerFromEnv() && storageConfigFromEnv() && store === "postgres" ? "enabled" : "disabled (fail closed)";
  const ok = store !== "postgres" || db === "ok";
  return NextResponse.json({ ok, store, db, uploads }, { status: ok ? 200 : 503 });
}
