import { randomUUID } from "node:crypto";
import { NextResponse } from "next/server";
import { assertCanUpload, recordAttachment } from "../../../../../lib/rooms/authz.js";
import { apiError, bearer, roomDb } from "../../../../../lib/rooms/http.js";
import { scanOrReject, scannerFromEnv } from "../../../../../lib/rooms/scanner.js";
import { MAX_FILE_BYTES, sha256Hex, storageFromEnv } from "../../../../../lib/rooms/storage.js";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const NAME_RE = /^[\w .()-]{1,180}$/;

// POST /api/rooms/:room/attachments (multipart field "file").
// Order: membership check -> scanner/storage configured -> read bytes ->
// ClamAV scan (fail closed) -> membership re-check + store + metadata in one tx.
export async function POST(request, { params }) {
  try {
    const { room } = await params;
    const db = roomDb();
    const token = bearer(request);
    await assertCanUpload(db, token, room);

    const scanner = scannerFromEnv();
    if (!scanner) return NextResponse.json({ error: "uploads disabled: no malware scanner configured" }, { status: 503 });
    const storage = await storageFromEnv();
    if (!storage) return NextResponse.json({ error: "uploads disabled: private storage not configured" }, { status: 503 });

    const declared = Number(request.headers.get("content-length") || 0);
    if (declared > MAX_FILE_BYTES + 64 * 1024) return NextResponse.json({ error: "file too large" }, { status: 413 });
    const form = await request.formData().catch(() => null);
    const file = form && form.get("file");
    if (!file || typeof file.arrayBuffer !== "function") return NextResponse.json({ error: "missing file" }, { status: 400 });
    const name = String(file.name || "upload").trim();
    if (!NAME_RE.test(name) || name === "." || name === "..") return NextResponse.json({ error: "invalid file name" }, { status: 400 });
    const data = Buffer.from(await file.arrayBuffer());
    if (data.length === 0 || data.length > MAX_FILE_BYTES) return NextResponse.json({ error: "invalid file size" }, { status: 413 });

    await scanOrReject(scanner, data);

    const meta = {
      fileId: randomUUID().replace(/-/g, ""),
      name,
      sha256: sha256Hex(data),
      size: data.length,
      mime: String(file.type || "application/octet-stream").slice(0, 120),
    };
    const saved = await recordAttachment(db, token, room, meta, storage, data);
    return NextResponse.json(saved, { status: 201 });
  } catch (err) {
    return apiError(err);
  }
}
