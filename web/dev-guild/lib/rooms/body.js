// readCappedBody streams a request body and stops as soon as it exceeds
// `limit` bytes, so a chunked upload without Content-Length can not make the
// server buffer an unbounded body. Returns a Buffer or throws { status: 413 }.
export class BodyTooLargeError extends Error {
  constructor() {
    super("file too large");
    this.name = "BodyTooLargeError";
    this.status = 413;
  }
}

export async function readCappedBody(request, limit) {
  const declared = Number(request.headers.get("content-length") || 0);
  if (Number.isFinite(declared) && declared > limit) throw new BodyTooLargeError();
  if (!request.body) return Buffer.alloc(0);
  const reader = request.body.getReader();
  const chunks = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > limit) {
      await reader.cancel().catch(() => {});
      throw new BodyTooLargeError();
    }
    chunks.push(Buffer.from(value.buffer, value.byteOffset, value.byteLength));
  }
  return Buffer.concat(chunks, total);
}

// parseCappedForm reads at most `limit` bytes and parses them as the
// request's multipart form.
export async function parseCappedForm(request, limit) {
  const body = await readCappedBody(request, limit);
  const contentType = request.headers.get("content-type") || "";
  return new Request("http://local/upload", { method: "POST", headers: { "content-type": contentType }, body }).formData();
}
