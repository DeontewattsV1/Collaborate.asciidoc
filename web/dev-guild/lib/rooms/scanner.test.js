import assert from "node:assert/strict";
import net from "node:net";
import test from "node:test";
import { clamdScanner, clamscanScanner, parseClamdReply, scanOrReject, scannerFromEnv } from "./scanner.js";

// A fake clamd that reads the INSTREAM framing and answers with `reply`
// (or misbehaves) so the client's fail-closed paths can be exercised.
async function fakeClamd(behaviour) {
  const seen = { command: "", bytes: 0 };
  const server = net.createServer((sock) => {
    let buf = Buffer.alloc(0);
    sock.on("data", (chunk) => {
      buf = Buffer.concat([buf, chunk]);
      if (!seen.command && buf.includes(0)) {
        const idx = buf.indexOf(0);
        seen.command = buf.subarray(0, idx).toString();
        buf = buf.subarray(idx + 1);
      }
      while (buf.length >= 4) {
        const len = buf.readUInt32BE(0);
        if (len === 0) {
          buf = buf.subarray(4);
          behaviour(sock, seen);
          return;
        }
        if (buf.length < 4 + len) break;
        seen.bytes += len;
        buf = buf.subarray(4 + len);
      }
    });
    sock.on("error", () => {});
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  return { port: server.address().port, seen, close: () => new Promise((r) => server.close(r)) };
}

test("clamd replies are parsed strictly", () => {
  assert.deepEqual(parseClamdReply("stream: OK\0"), { verdict: "clean" });
  assert.deepEqual(parseClamdReply("stream: Eicar-Signature FOUND\0"), { verdict: "infected", signature: "Eicar-Signature" });
  assert.equal(parseClamdReply("INSTREAM size limit exceeded. ERROR\0").verdict, "error");
  assert.equal(parseClamdReply("stream: OK trailing\0").verdict, "error");
  assert.equal(parseClamdReply("").verdict, "error");
});

test("clamd INSTREAM: clean, infected, garbage, hang-up and timeout", async (t) => {
  const data = Buffer.alloc(150 * 1024, 7);
  const cases = [
    ["clean", (s) => s.end("stream: OK\0")],
    ["infected", (s) => s.end("stream: Eicar-Test-Signature FOUND\0")],
    ["error", (s) => s.end("UNKNOWN COMMAND\0")],
    ["error", (s) => s.destroy()],
    ["error", () => {}],
  ];
  for (const [want, behaviour] of cases) {
    const srv = await fakeClamd(behaviour);
    t.after(srv.close);
    const scanner = clamdScanner({ host: "127.0.0.1", port: srv.port, timeoutMs: 300 });
    const got = await scanner.scan(data);
    assert.equal(got.verdict, want);
    assert.equal(srv.seen.command, "zINSTREAM");
    assert.equal(srv.seen.bytes, data.length);
  }
});

test("clamd connection refused is an error, never clean", async () => {
  const srv = await fakeClamd(() => {});
  const port = srv.port;
  await srv.close();
  const got = await clamdScanner({ host: "127.0.0.1", port, timeoutMs: 500 }).scan(Buffer.from("x"));
  assert.equal(got.verdict, "error");
});

test("clamscan exit codes: 0 clean, 1 infected, anything else error", async () => {
  const fake = (code) => (cmd, args, opts, cb) => {
    assert.ok(args.includes("--alert-encrypted=yes"));
    cb(code === 0 ? null : Object.assign(new Error("exit"), { code }));
  };
  assert.equal((await clamscanScanner({ run: fake(0) }).scan(Buffer.from("a"))).verdict, "clean");
  assert.equal((await clamscanScanner({ run: fake(1) }).scan(Buffer.from("a"))).verdict, "infected");
  assert.equal((await clamscanScanner({ run: fake(2) }).scan(Buffer.from("a"))).verdict, "error");
  assert.equal((await clamscanScanner({ run: fake("ENOENT") }).scan(Buffer.from("a"))).verdict, "error");
});

test("scanOrReject fails closed for every non-clean outcome", async () => {
  await assert.rejects(() => scanOrReject(null, Buffer.from("a")), (e) => e.status === 503);
  const throws = { scan: async () => { throw new Error("boom"); } };
  await assert.rejects(() => scanOrReject(throws, Buffer.from("a")), (e) => e.status === 422);
  for (const result of [{ verdict: "infected" }, { verdict: "error" }, { verdict: "CLEAN" }, null, undefined, "clean"]) {
    await assert.rejects(() => scanOrReject({ scan: async () => result }, Buffer.from("a")), (e) => e.status === 422);
  }
  assert.deepEqual(await scanOrReject({ scan: async () => ({ verdict: "clean" }) }, Buffer.from("a")), { verdict: "clean" });
});

test("no scanner configured means no scanner (uploads disabled)", () => {
  assert.equal(scannerFromEnv({}), null);
  assert.equal(scannerFromEnv({ CLAMD_HOST: "clamd.internal" }).kind, "clamd");
  assert.equal(scannerFromEnv({ CLAMSCAN_PATH: "/usr/bin/clamscan" }).kind, "clamscan");
});
