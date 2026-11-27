import assert from "node:assert/strict";
import test from "node:test";
import { createObjectStorage, objectKey, sha256Hex, storageConfigFromEnv } from "./storage.js";

class Cmd { constructor(input) { this.input = input; } }
const commands = {
  PutObjectCommand: class extends Cmd { get op() { return "put"; } },
  GetObjectCommand: class extends Cmd { get op() { return "get"; } },
  DeleteObjectCommand: class extends Cmd { get op() { return "delete"; } },
};

function fakeBucket() {
  const objects = new Map();
  const calls = [];
  return {
    objects,
    calls,
    client: {
      async send(cmd) {
        calls.push(cmd);
        const { Key, Body } = cmd.input;
        if (cmd.op === "put") objects.set(Key, Buffer.from(Body));
        if (cmd.op === "delete") objects.delete(Key);
        if (cmd.op === "get") {
          if (!objects.has(Key)) throw Object.assign(new Error("NoSuchKey"), { name: "NoSuchKey" });
          const bytes = objects.get(Key);
          return { Body: { transformToByteArray: async () => new Uint8Array(bytes) } };
        }
        return {};
      },
    },
  };
}

const ROOM = "room1";
const FILE = "a".repeat(32);

test("object keys are room-scoped and validated", () => {
  assert.equal(objectKey(ROOM, FILE), `rooms/${ROOM}/${FILE}`);
  assert.throws(() => objectKey("../other", FILE));
  assert.throws(() => objectKey(ROOM, "../../x"));
});

test("put sends a SHA-256 checksum and refuses mismatched bytes", async () => {
  const b = fakeBucket();
  const s = createObjectStorage({ client: b.client, bucket: "collaborate-staging", commands });
  const data = Buffer.from("hello room");
  const key = await s.put({ roomId: ROOM, fileId: FILE, data, sha256: sha256Hex(data) });
  assert.equal(key, `rooms/${ROOM}/${FILE}`);
  const put = b.calls[0].input;
  assert.equal(put.Bucket, "collaborate-staging");
  assert.equal(put.ChecksumSHA256, Buffer.from(sha256Hex(data), "hex").toString("base64"));
  assert.equal(put.ACL, undefined, "never sets a public ACL");
  await assert.rejects(() => s.put({ roomId: ROOM, fileId: FILE, data, sha256: "0".repeat(64) }), (e) => e.status === 502);
  await assert.rejects(() => s.put({ roomId: ROOM, fileId: FILE, data: Buffer.alloc(0), sha256: sha256Hex(Buffer.alloc(0)) }), (e) => e.status === 400);
});

test("get verifies size and SHA-256 and refuses keys outside the room", async () => {
  const b = fakeBucket();
  const s = createObjectStorage({ client: b.client, bucket: "x", commands });
  const data = Buffer.from("payload");
  const sha = sha256Hex(data);
  const key = await s.put({ roomId: ROOM, fileId: FILE, data, sha256: sha });
  assert.deepEqual(await s.get({ roomId: ROOM, key, sha256: sha, size: data.length }), data);
  b.objects.set(key, Buffer.from("tampered"));
  await assert.rejects(() => s.get({ roomId: ROOM, key, sha256: sha, size: data.length }), (e) => e.status === 502);
  await assert.rejects(() => s.get({ roomId: "room2", key, sha256: sha, size: data.length }), (e) => e.status === 502);
});

test("storage is unconfigured unless every R2 setting is present and HTTPS", () => {
  const full = { R2_ENDPOINT: "https://acct.r2.cloudflarestorage.com", R2_BUCKET: "b", R2_ACCESS_KEY_ID: "k", R2_SECRET_ACCESS_KEY: "s" };
  assert.ok(storageConfigFromEnv(full));
  assert.equal(storageConfigFromEnv({ ...full, R2_BUCKET: "" }), null);
  assert.equal(storageConfigFromEnv({ ...full, R2_ENDPOINT: "http://insecure" }), null);
  assert.equal(storageConfigFromEnv({}), null);
});
