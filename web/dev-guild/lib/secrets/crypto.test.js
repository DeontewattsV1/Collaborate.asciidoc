import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import test from "node:test";
import { VaultConfigError, deriveKeys, fingerprint, loadVaultKeys, openSecret, parseMasterKey, safeEqualHex, sealSecret, zeroize } from "./crypto.js";

const masterB64 = () => randomBytes(32).toString("base64");
const ctx = { roomId: "room1", secretId: `s_${"a".repeat(32)}`, version: 1 };

test("the vault fails closed without a usable master key", () => {
  for (const raw of [undefined, "", "short", "0".repeat(64), Buffer.alloc(32, 7).toString("base64")]) {
    assert.throws(() => loadVaultKeys({ SECRETS_MASTER_KEY: raw }), VaultConfigError);
  }
  assert.equal(parseMasterKey(randomBytes(32).toString("hex")).length, 32);
  assert.equal(parseMasterKey(masterB64()).length, 32);
});

test("seal/open roundtrip; output carries no plaintext", () => {
  const keys = loadVaultKeys({ SECRETS_MASTER_KEY: masterB64() });
  const value = "fake-value-for-tests-1234567890";
  const sealed = sealSecret(keys, value, ctx);
  assert.equal(sealed.nonce.length, 12);
  assert.equal(sealed.wrappedKey.length, 60);
  assert.ok(!Buffer.from(sealed.ciphertext).includes(Buffer.from(value)));
  const out = openSecret(keys, sealed, ctx);
  assert.equal(out.toString("utf8"), value);
  zeroize(out);
  assert.ok(out.every((b) => b === 0));
});

test("any tamper or context swap fails authentication", () => {
  const keys = loadVaultKeys({ SECRETS_MASTER_KEY: masterB64() });
  const sealed = sealSecret(keys, "fake-value-xyz-000000", ctx);
  const flip = (buf, i) => { const b = Buffer.from(buf); b[i] ^= 1; return b; };
  assert.throws(() => openSecret(keys, { ...sealed, ciphertext: flip(sealed.ciphertext, 0) }, ctx));
  assert.throws(() => openSecret(keys, { ...sealed, nonce: flip(sealed.nonce, 3) }, ctx));
  assert.throws(() => openSecret(keys, { ...sealed, wrappedKey: flip(sealed.wrappedKey, 20) }, ctx));
  assert.throws(() => openSecret(keys, sealed, { ...ctx, roomId: "room2" }));
  assert.throws(() => openSecret(keys, sealed, { ...ctx, version: 2 }));
  const other = loadVaultKeys({ SECRETS_MASTER_KEY: masterB64() });
  assert.throws(() => openSecret(other, sealed, ctx));
});

test("fingerprints are keyed, stable and constant-time compared", () => {
  const master = randomBytes(32);
  const a = deriveKeys(master);
  const b = deriveKeys(randomBytes(32));
  const fa = fingerprint(a, "fake");
  assert.match(fa, /^[0-9a-f]{64}$/);
  assert.equal(fa, fingerprint(deriveKeys(master), "fake"));
  assert.notEqual(fa, fingerprint(b, "fake"));
  assert.equal(safeEqualHex(fa, fa), true);
  assert.equal(safeEqualHex(fa, fa.slice(1)), false);
});
