import { createHash } from "node:crypto";
import { RoomApiError } from "./errors.js";

// Private object storage for room attachments over the S3 API. Staging uses a
// Cloudflare R2 bucket (private by default, encrypted at rest by R2, HTTPS
// only); any S3-compatible private bucket works. There is no public URL:
// objects are only read back through readAttachment after a membership check.

export const MAX_FILE_BYTES = 25 * 1024 * 1024;
const PREFIX = "rooms";
const SEGMENT = /^[A-Za-z0-9_-]{1,64}$/;

export function sha256Hex(data) {
  return createHash("sha256").update(data).digest("hex");
}

export function objectKey(roomId, fileId) {
  if (!SEGMENT.test(String(roomId)) || !/^[0-9a-f]{32}$/.test(String(fileId))) {
    throw new RoomApiError(400, "invalid object key");
  }
  return `${PREFIX}/${roomId}/${fileId}`;
}

function integrityError() {
  return new RoomApiError(502, "attachment integrity check failed");
}

async function bodyBytes(body, limit) {
  if (!body) return Buffer.alloc(0);
  if (typeof body.transformToByteArray === "function") {
    const arr = await body.transformToByteArray();
    if (arr.length > limit) throw integrityError();
    return Buffer.from(arr);
  }
  const chunks = [];
  let total = 0;
  for await (const chunk of body) {
    total += chunk.length;
    if (total > limit) throw integrityError();
    chunks.push(Buffer.from(chunk));
  }
  return Buffer.concat(chunks);
}

// commands lets tests inject fakes for PutObject/GetObject/DeleteObject.
export function createObjectStorage({ client, bucket, commands }) {
  if (!client || !bucket || !commands) throw new Error("object storage needs client, bucket and commands");
  const { PutObjectCommand, GetObjectCommand, DeleteObjectCommand } = commands;
  return {
    kind: "s3",
    bucket,
    async put({ roomId, fileId, data, sha256 }) {
      if (!Buffer.isBuffer(data) || data.length === 0 || data.length > MAX_FILE_BYTES) {
        throw new RoomApiError(400, "invalid file");
      }
      if (sha256Hex(data) !== sha256) throw integrityError();
      const key = objectKey(roomId, fileId);
      await client.send(new PutObjectCommand({
        Bucket: bucket,
        Key: key,
        Body: data,
        ContentLength: data.length,
        ContentType: "application/octet-stream",
        ChecksumSHA256: Buffer.from(sha256, "hex").toString("base64"),
        Metadata: { sha256 },
      }));
      return key;
    },
    async get({ roomId, key, sha256, size }) {
      if (!String(key).startsWith(`${PREFIX}/${roomId}/`)) throw integrityError();
      const res = await client.send(new GetObjectCommand({ Bucket: bucket, Key: key }));
      const data = await bodyBytes(res.Body, MAX_FILE_BYTES);
      if (data.length !== size || sha256Hex(data) !== sha256) throw integrityError();
      return data;
    },
    async remove(key) {
      await client.send(new DeleteObjectCommand({ Bucket: bucket, Key: key }));
    },
  };
}

export function storageConfigFromEnv(env = process.env) {
  const endpoint = String(env.R2_ENDPOINT || "").trim();
  const bucket = String(env.R2_BUCKET || "").trim();
  const accessKeyId = String(env.R2_ACCESS_KEY_ID || "").trim();
  const secretAccessKey = String(env.R2_SECRET_ACCESS_KEY || "").trim();
  if (!endpoint || !bucket || !accessKeyId || !secretAccessKey) return null;
  if (!endpoint.startsWith("https://")) return null;
  return { endpoint, bucket, accessKeyId, secretAccessKey };
}

// storageFromEnv returns null when R2 is not configured; uploads then fail
// closed with 503 instead of falling back to any local or public store.
export async function storageFromEnv(env = process.env) {
  const cfg = storageConfigFromEnv(env);
  if (!cfg) return null;
  if (globalThis.__guildObjectStorage) return globalThis.__guildObjectStorage;
  const sdk = await import("@aws-sdk/client-s3");
  const client = new sdk.S3Client({
    region: "auto",
    endpoint: cfg.endpoint,
    credentials: { accessKeyId: cfg.accessKeyId, secretAccessKey: cfg.secretAccessKey },
    // We verify SHA-256 ourselves; avoid SDK default CRC checksums R2 may not echo.
    requestChecksumCalculation: "WHEN_REQUIRED",
    responseChecksumValidation: "WHEN_REQUIRED",
  });
  globalThis.__guildObjectStorage = createObjectStorage({ client, bucket: cfg.bucket, commands: sdk });
  return globalThis.__guildObjectStorage;
}
