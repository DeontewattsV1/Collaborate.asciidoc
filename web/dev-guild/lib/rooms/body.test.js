import assert from "node:assert/strict";
import test from "node:test";
import { BodyTooLargeError, parseCappedForm, readCappedBody } from "./body.js";

function streamOf(chunks) {
  return new ReadableStream({
    pull(controller) {
      const next = chunks.shift();
      if (next) controller.enqueue(next);
      else controller.close();
    },
  });
}

const chunked = (chunks) => new Request("http://x/", { method: "POST", body: streamOf(chunks), duplex: "half" });

test("reads a body under the cap", async () => {
  const buf = await readCappedBody(chunked([new Uint8Array([1, 2]), new Uint8Array([3])]), 10);
  assert.deepEqual([...buf], [1, 2, 3]);
});

test("a chunked body without Content-Length is cut off at the cap", async () => {
  const big = new Uint8Array(6);
  await assert.rejects(readCappedBody(chunked([big, big]), 10), BodyTooLargeError);
});

test("a declared Content-Length over the cap is refused before reading", async () => {
  const req = new Request("http://x/", { method: "POST", body: "x", headers: { "content-length": "999" } });
  await assert.rejects(readCappedBody(req, 10), (err) => err.status === 413);
});

test("parses a multipart form within the cap", async () => {
  const form = new FormData();
  form.append("file", new Blob(["hello"], { type: "text/plain" }), "hello.txt");
  const req = new Request("http://x/", { method: "POST", body: form });
  const parsed = await parseCappedForm(req, 10_000);
  const file = parsed.get("file");
  assert.equal(file.name, "hello.txt");
  assert.equal(await file.text(), "hello");
});
