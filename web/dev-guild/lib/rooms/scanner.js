import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import net from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { RoomApiError } from "./errors.js";

// ClamAV malware scanning, fail-closed. A file is accepted only when the
// scanner positively reports it clean. Any error, timeout, size-limit reply,
// unexpected reply, or missing scanner means the upload is rejected.

const DEFAULT_TIMEOUT_MS = 30_000;
const CHUNK = 64 * 1024;

export function parseClamdReply(reply) {
  const text = String(reply || "").replace(/\0/g, "").trim();
  if (/^stream: OK$/.test(text)) return { verdict: "clean" };
  const found = /^stream: (.+) FOUND$/.exec(text);
  if (found) return { verdict: "infected", signature: found[1] };
  return { verdict: "error", detail: text.slice(0, 200) || "empty reply" };
}

// clamd INSTREAM over TCP: "zINSTREAM\0", then <u32 BE length><bytes> chunks,
// then a zero-length chunk; clamd answers "stream: OK" or "stream: <sig> FOUND".
export function clamdScanner({ host, port = 3310, timeoutMs = DEFAULT_TIMEOUT_MS, connect = net.createConnection } = {}) {
  if (!host) throw new Error("clamd scanner needs a host");
  return {
    kind: "clamd",
    scan(data) {
      return new Promise((resolve) => {
        let settled = false;
        let reply = "";
        const done = (result) => {
          if (settled) return;
          settled = true;
          clearTimeout(timer);
          socket.destroy();
          resolve(result);
        };
        const socket = connect({ host, port: Number(port) });
        const timer = setTimeout(() => done({ verdict: "error", detail: "timeout" }), timeoutMs);
        socket.on("error", (err) => done({ verdict: "error", detail: err.code || "socket error" }));
        socket.on("data", (chunk) => {
          reply += chunk.toString("utf8");
          if (reply.includes("\0")) done(parseClamdReply(reply));
        });
        socket.on("end", () => done(reply ? parseClamdReply(reply) : { verdict: "error", detail: "closed" }));
        socket.on("connect", () => {
          socket.write("zINSTREAM\0");
          for (let i = 0; i < data.length; i += CHUNK) {
            const part = data.subarray(i, i + CHUNK);
            const len = Buffer.alloc(4);
            len.writeUInt32BE(part.length, 0);
            socket.write(len);
            socket.write(part);
          }
          socket.write(Buffer.alloc(4));
        });
      });
    },
  };
}

// clamscan fallback: private temp file, argv only (no shell), exit 0 = clean,
// 1 = infected, anything else = error.
export function clamscanScanner({ command = "clamscan", timeoutMs = 120_000, run = execFile } = {}) {
  return {
    kind: "clamscan",
    async scan(data) {
      let dir;
      try {
        dir = await mkdtemp(join(tmpdir(), "guild-scan-"));
        const file = join(dir, "payload");
        await writeFile(file, data, { mode: 0o600 });
        const code = await new Promise((resolve) => {
          run(command, ["--no-summary", "--alert-encrypted=yes", "--alert-exceeds-max=yes", file], { timeout: timeoutMs }, (err) => {
            if (!err) return resolve(0);
            resolve(typeof err.code === "number" ? err.code : -1);
          });
        });
        if (code === 0) return { verdict: "clean" };
        if (code === 1) return { verdict: "infected" };
        return { verdict: "error", detail: `exit ${code}` };
      } catch (err) {
        return { verdict: "error", detail: err.code || "scan failed" };
      } finally {
        if (dir) await rm(dir, { recursive: true, force: true }).catch(() => {});
      }
    },
  };
}

export function scannerFromEnv(env = process.env) {
  const timeoutMs = Number(env.CLAMD_TIMEOUT_MS) > 0 ? Number(env.CLAMD_TIMEOUT_MS) : DEFAULT_TIMEOUT_MS;
  const host = String(env.CLAMD_HOST || "").trim();
  if (host) return clamdScanner({ host, port: Number(env.CLAMD_PORT) || 3310, timeoutMs });
  const command = String(env.CLAMSCAN_PATH || "").trim();
  if (command) return clamscanScanner({ command, timeoutMs });
  return null;
}

// scanOrReject is the only gate between an upload and storage.
export async function scanOrReject(scanner, data) {
  if (!scanner) throw new RoomApiError(503, "uploads disabled: no malware scanner configured");
  let result;
  try {
    result = await scanner.scan(data);
  } catch {
    throw new RoomApiError(422, "file rejected: scanner unavailable");
  }
  if (!result || result.verdict !== "clean") {
    throw new RoomApiError(422, "file rejected");
  }
  return result;
}
