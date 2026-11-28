import { createCipheriv, createDecipheriv, createHmac, hkdfSync, randomBytes, timingSafeEqual } from "node:crypto";

// Envelope encryption for the secret vault.
//
// - SECRETS_MASTER_KEY is 32 random bytes, base64 or hex. Missing, short or
//   malformed keys fail closed: the vault refuses every operation.
// - Each secret gets its own random 256-bit data key. The value is sealed with
//   AES-256-GCM under the data key; the data key is wrapped with AES-256-GCM
//   under a key-encryption key derived from the master key (HKDF-SHA256).
// - Both layers bind additional authenticated data (room, secret id, version)
//   so a ciphertext copied to another row will not decrypt.
// - Fingerprints are HMAC-SHA256 under a separate derived key, so equal values
//   can be detected without storing anything that reveals the value.

export const KEY_VERSION = 1;
const ALG = "aes-256-gcm";

export class VaultConfigError extends Error {
  constructor(message) {
    super(message);
    this.name = "VaultConfigError";
    this.status = 503;
  }
}

export function zeroize(buf) {
  if (Buffer.isBuffer(buf) || buf instanceof Uint8Array) buf.fill(0);
}

export function parseMasterKey(raw) {
  const s = String(raw || "").trim();
  if (!s) throw new VaultConfigError("secret vault disabled: SECRETS_MASTER_KEY is not set");
  let key = null;
  if (/^[0-9a-f]{64}$/i.test(s)) key = Buffer.from(s, "hex");
  else if (/^[A-Za-z0-9+/_-]{43}=?$/.test(s)) key = Buffer.from(s.replace(/-/g, "+").replace(/_/g, "/"), "base64");
  if (!key || key.length !== 32) throw new VaultConfigError("secret vault disabled: SECRETS_MASTER_KEY must be 32 bytes (base64 or hex)");
  if (key.every((b) => b === key[0])) throw new VaultConfigError("secret vault disabled: SECRETS_MASTER_KEY is not random");
  return key;
}

export function deriveKeys(master) {
  const kek = Buffer.from(hkdfSync("sha256", master, Buffer.alloc(0), "collaborate/secrets/kek/v1", 32));
  const fp = Buffer.from(hkdfSync("sha256", master, Buffer.alloc(0), "collaborate/secrets/fingerprint/v1", 32));
  const handle = Buffer.from(hkdfSync("sha256", master, Buffer.alloc(0), "collaborate/secrets/handle/v1", 32));
  return { kek, fp, handle, version: KEY_VERSION };
}

export function loadVaultKeys(env = process.env) {
  const master = parseMasterKey(env.SECRETS_MASTER_KEY);
  try {
    return deriveKeys(master);
  } finally {
    zeroize(master);
  }
}

function gcmSeal(key, plaintext, aad) {
  const iv = randomBytes(12);
  const c = createCipheriv(ALG, key, iv);
  c.setAAD(Buffer.from(aad, "utf8"));
  const ct = Buffer.concat([c.update(plaintext), c.final()]);
  return { iv, ct, tag: c.getAuthTag() };
}

function gcmOpen(key, { iv, ct, tag }, aad) {
  const d = createDecipheriv(ALG, key, iv);
  d.setAAD(Buffer.from(aad, "utf8"));
  d.setAuthTag(tag);
  return Buffer.concat([d.update(ct), d.final()]);
}

export function aadFor({ roomId, secretId, version = 1 }) {
  return `collaborate-secret|${roomId}|${secretId}|${version}`;
}

// sealSecret returns only ciphertext material; the plaintext and the data key
// are zeroized before returning.
export function sealSecret(keys, value, ctx) {
  const plain = Buffer.from(String(value), "utf8");
  const dek = randomBytes(32);
  const aad = aadFor(ctx);
  try {
    const body = gcmSeal(dek, plain, aad);
    const wrap = gcmSeal(keys.kek, dek, `${aad}|dek`);
    return {
      ciphertext: Buffer.concat([body.ct, body.tag]),
      nonce: body.iv,
      wrappedKey: Buffer.concat([wrap.iv, wrap.ct, wrap.tag]),
      keyVersion: keys.version,
    };
  } finally {
    zeroize(plain);
    zeroize(dek);
  }
}

// openSecret returns a Buffer; callers must zeroize it when done. Any tamper
// (ciphertext, nonce, wrapped key or AAD) throws.
export function openSecret(keys, sealed, ctx) {
  const aad = aadFor(ctx);
  const wk = Buffer.from(sealed.wrappedKey);
  if (wk.length !== 12 + 32 + 16) throw new Error("secret envelope is malformed");
  const dek = gcmOpen(keys.kek, { iv: wk.subarray(0, 12), ct: wk.subarray(12, 44), tag: wk.subarray(44) }, `${aad}|dek`);
  try {
    const ct = Buffer.from(sealed.ciphertext);
    if (ct.length < 17) throw new Error("secret envelope is malformed");
    return gcmOpen(dek, { iv: Buffer.from(sealed.nonce), ct: ct.subarray(0, ct.length - 16), tag: ct.subarray(ct.length - 16) }, aad);
  } finally {
    zeroize(dek);
  }
}

export function fingerprint(keys, value) {
  return createHmac("sha256", keys.fp).update(String(value), "utf8").digest("hex");
}

export function handleDigest(keys, handle) {
  return createHmac("sha256", keys.handle).update(String(handle), "utf8").digest("hex");
}

export function safeEqualHex(a, b) {
  const x = Buffer.from(String(a || ""), "utf8");
  const y = Buffer.from(String(b || ""), "utf8");
  if (x.length !== y.length) {
    timingSafeEqual(x, x);
    return false;
  }
  return timingSafeEqual(x, y);
}

export function newHandle() {
  return `sh_${randomBytes(32).toString("base64url")}`;
}
