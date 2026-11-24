"""Offline tests for scripts/locker_agents.py (Git Locker assistant agents)."""
import importlib.util
import io
import json
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "locker_agents.py"
spec = importlib.util.spec_from_file_location("locker_agents", SCRIPT)
la = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = la
spec.loader.exec_module(la)

AGENTS = la.build_index()
BY_NUM = {s["number"]: s for s in AGENTS}


class IndexTest(unittest.TestCase):
    def test_thousand_unique_agents_match_the_published_index(self):
        self.assertEqual(len(AGENTS), 1000)
        self.assertEqual(len({s["name"] for s in AGENTS}), 1000)
        # Rows copied verbatim from the Git Locker Index the maintainer supplied.
        for n, name in {1: "aether-agent-protocol", 8: "source-agent-agent", 52: "root-trust-kernel",
                        480: "civic-civic-garden", 658: "echo-lineage-agent", 727: "quartz-workflow-switchboard",
                        886: "dovetail-access-resolver", 1000: "civic-experience-codex"}.items():
            self.assertEqual(BY_NUM[n]["name"], name)
        self.assertEqual(BY_NUM[174]["concept"], "A constraint-aware scheduler for coordinating resources inside "
                         "user-owned data that syncs and remains useful offline.")
        self.assertEqual(BY_NUM[174]["mvp"], "Solver + calendar export")

    def test_every_agent_has_a_job_and_its_own_workflow(self):
        for s in AGENTS:
            self.assertTrue(s["job"] and s["role"])
            self.assertEqual(s["workflow"][0], "redact-input")
            self.assertEqual(s["workflow"][-1], "receipt")
            self.assertEqual("delegate (bounded)" in s["workflow"], s["delegates"])
        self.assertEqual(sum(s["delegates"] for s in AGENTS), 100)

    def test_committed_index_is_not_stale(self):
        self.assertEqual(la.main(["index", "--check"]), 0)


class RuntimeTest(unittest.TestCase):
    def ctx(self, depth=2, calls=12, model=None):
        return la.Context(AGENTS, model or la.mock_model(), depth, calls)

    def test_coordinator_delegates_recursively_and_merges(self):
        ctx = self.ctx()
        res = la.run_agent(ctx, ctx.by_name("lattice-agent-orchestrator"), "design an agent identity rollout")
        self.assertEqual(len(res["children"]), 2)
        self.assertTrue(all(c["depth"] == 1 for c in res["children"]))
        self.assertNotIn("DELEGATE:", res["brief"])
        kinds = [e["kind"] for e in ctx.trace]
        self.assertIn("delegate", kinds)
        self.assertEqual(kinds[-1], "receipt")

    def test_recursion_is_bounded_by_depth(self):
        # A model that always delegates to another coordinator would recurse forever without the depth cap.
        # Each call delegates to a fresh coordinator, so only the depth cap can stop the chain.
        coordinators = iter([s["name"] for s in AGENTS if s["delegates"] and s["name"] != "dovetail-agent-switchboard"])

        def always_delegate(system, prompt):
            return f"DELEGATE: {next(coordinators)} :: go deeper"
        ctx = self.ctx(depth=2, calls=40, model=always_delegate)
        res = la.run_agent(ctx, ctx.by_name("dovetail-agent-switchboard"), "task")

        def max_depth(n):
            return max([n["depth"]] + [max_depth(c) for c in n["children"]])
        self.assertLessEqual(max_depth(res), 2)

    def test_cycles_and_unknown_agents_are_refused(self):
        def bad(system, prompt):
            return "DELEGATE: lattice-agent-orchestrator :: myself\nDELEGATE: no-such-agent :: x"
        ctx = self.ctx(model=bad)
        res = la.run_agent(ctx, ctx.by_name("lattice-agent-orchestrator"), "task")
        self.assertEqual(res["children"], [])
        self.assertEqual(sum(e["kind"] == "delegate-refused" for e in ctx.trace), 2)

    def test_call_budget_is_enforced(self):
        def always_delegate(system, prompt):
            return "DELEGATE: basalt-agent-mesh :: a\nDELEGATE: lattice-agent-orchestrator :: b\nDELEGATE: dovetail-agent-switchboard :: c"
        ctx = self.ctx(depth=3, calls=5, model=always_delegate)
        la.run_agent(ctx, ctx.by_name("atlas-agent-mixer"), "task")
        self.assertLessEqual(ctx.calls, 5)

    def test_non_coordinators_never_delegate(self):
        def tries(system, prompt):
            self.assertNotIn("DELEGATE:", system)
            return "DELEGATE: lattice-agent-orchestrator :: sneaky"
        ctx = self.ctx(model=tries)
        res = la.run_agent(ctx, ctx.by_name("vector-agent-guardian"), "task")
        self.assertEqual(res["children"], [])

    def test_secrets_never_reach_models_or_traces(self):
        seen = []

        def spy(system, prompt):
            seen.append(system + prompt)
            return "ok sk-ant-THISISASECRETKEY12345678 done"
        ctx = self.ctx(model=spy)
        la.assist(ctx, "fix login; api_key=supersecretvalue123 and ghp_abcdefghijklmnopqrstuvwxyz12345", 2, spy)
        blob = json.dumps(ctx.trace) + "".join(seen)
        for secret in ("supersecretvalue123", "ghp_abcdefghijklmnopqrstuvwxyz12345", "THISISASECRETKEY"):
            self.assertNotIn(secret, blob)

    def test_trace_records_are_redacted_directly(self):
        ctx = self.ctx()
        ctx.record("x", 0, "note", "leaked ghp_abcdefghijklmnopqrstuvwxyz12345 here")
        self.assertNotIn("ghp_abcdefghijklmnopqrstuvwxyz12345", ctx.trace[0]["text"])

    def test_trace_is_hash_chained(self):
        ctx = self.ctx()
        la.run_agent(ctx, ctx.by_name("lattice-agent-orchestrator"), "task")
        for prev, cur in zip(ctx.trace, ctx.trace[1:]):
            self.assertEqual(cur["prev"], prev["hash"])
        tampered = dict(ctx.trace[1], text="changed")
        tampered.pop("hash")
        self.assertNotEqual(la.sha(json.dumps(tampered, sort_keys=True, ensure_ascii=False)), ctx.trace[1]["hash"])

    def test_assist_briefs_primary_and_reviews(self):
        ctx = self.ctx()
        out = la.assist(ctx, "harden GitHub Actions against secret leaks", 3, la.mock_model("primary"))
        self.assertEqual(len(out["team"]), 3)
        self.assertEqual(len({b["role"] for b in out["team"]}), 3)
        self.assertIn("brief", out["answer"])
        self.assertEqual(out["review"]["agent"].split("-")[-1], "guardian")

    def test_local_tools(self):
        self.assertIn("destructive command", la.tool_risk("then run rm -rf / on prod"))
        self.assertIn('"kind": "item"', la.tool_normalize("- first\n2) second"))
        self.assertTrue(la.tool_keywords("carbon carbon pipeline").startswith("Key terms: carbon"))


class CliTest(unittest.TestCase):
    def run_cli(self, *args):
        buf = io.StringIO()
        with redirect_stdout(buf):
            rc = la.main(list(args))
        return rc, buf.getvalue()

    def test_run_and_assist_write_traces(self):
        with tempfile.TemporaryDirectory() as d:
            rc, out = self.run_cli("run", "42", "--task", "review this plan", "--out", f"{d}/t.json", "--summary", f"{d}/s.md")
            self.assertEqual(rc, 0)
            doc = json.loads(Path(f"{d}/t.json").read_text())
            self.assertEqual(doc["result"]["agent"], "horizon-agent-prism")
            self.assertIn("Git Locker agents: run", Path(f"{d}/s.md").read_text())
            rc, out = self.run_cli("assist", "--task", "plan a provenance pipeline", "--assistants", "2")
            self.assertEqual(rc, 0)
            self.assertIn("Primary model answer", out)

    def test_limits_and_unknown_agents_fail_cleanly(self):
        import contextlib
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(self.run_cli("run", "nope", "--task", "x")[0], 1)
            self.assertEqual(self.run_cli("run", "1", "--task", "x", "--max-depth", "9")[0], 1)
            self.assertEqual(self.run_cli("run", "1", "--task", "x", "--max-calls", "999")[0], 1)
            self.assertEqual(self.run_cli("run", "1", "--task", "x", "--provider", "openai")[0], 1)  # no key


if __name__ == "__main__":
    unittest.main()
