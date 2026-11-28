// Behavioural tests for the static playground engine (site/playground.js).
// Run: node --test tests/playground.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const source = readFileSync(new URL("../site/playground.js", import.meta.url), "utf8");
const sandbox = {};
vm.runInNewContext(source, sandbox);
const P = sandbox.CollaboratePlayground;

const outcomes = (id) => {
  const sc = P.SCENARIOS.find((s) => s.id === id);
  return Array.from(sc.steps, (_, i) => P.evaluate(P.stateAt(sc, i)).outcome);
};

test("exposes exactly the three documented scenarios", () => {
  assert.deepEqual(Array.from(P.SCENARIOS, (s) => s.id), ["collide", "handoff", "review"]);
});

test("overlap is hierarchical, not a string prefix", () => {
  assert.equal(P.overlaps("src/auth", "src/auth/session.ts"), true);
  assert.equal(P.overlaps("src/auth", "src/authz/x.ts"), false);
});

test("scenario 1: the later PR yields to the earlier lease, then passes when narrowed", () => {
  assert.deepEqual(outcomes("collide"), ["PASS", "BLOCKED", "PASS"]);
  const sc = P.SCENARIOS[0];
  const r = P.evaluate(P.stateAt(sc, 1));
  assert.equal(r.blocking_conflict_count, 1);
  assert.equal(r.conflicts[0].holder, 24);
  assert.deepEqual([...r.conflicts[0].paths], ["src/auth/session.ts"]);
});

test("scenario 2: merging releases the lease and the reconciled PR passes", () => {
  assert.deepEqual(outcomes("handoff"), ["BLOCKED", "PASS", "PASS"]);
  const sc = P.SCENARIOS[1];
  const st = P.stateAt(sc, 1);
  assert.equal(P.evaluate(st).lease_held, false);
  assert.equal(P.evaluate({ ...st, target: 27 }).outcome, "PASS");
});

test("scenario 3: only an independent approval on the current head counts", () => {
  assert.deepEqual(outcomes("review"), ["BLOCKED", "BLOCKED", "PASS", "BLOCKED", "PASS"]);
  const sc = P.SCENARIOS[2];
  assert.equal(P.evaluate(P.stateAt(sc, 2)).human_approval, true);
  assert.equal(P.evaluate(P.stateAt(sc, 3)).human_approval, false);
});

test("data is synthetic: no real secrets, emails, or URLs", () => {
  assert.doesNotMatch(source, /https?:\/\//);
  assert.doesNotMatch(source, /[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[a-z]{2,}/);
  assert.doesNotMatch(source, /(ghp_|gho_|sk-|AIza|sb_secret_|xox[bp]-)/);
});
