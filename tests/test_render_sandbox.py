"""Offline and local-broker tests for Render Sandboxes live multi-agent demo."""
import os
import re
import shutil
import socket
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / ".github" / "workflows" / "render-sandbox.yml"
RUNNER = ROOT / "scripts" / "sandbox" / "render-sandbox.mjs"
DEMO = ROOT / "scripts" / "sandbox" / "render-demo.sh"
SETUP_ENVS = ROOT / "scripts" / "setup_github_environments.sh"
DOC = ROOT / "docs" / "RENDER-SANDBOX.md"


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class RenderSandboxTest(unittest.TestCase):
    def test_workflow_guardrails_and_pinned_actions(self):
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("workflow_dispatch:", text)
        for key in ("plan:", "timeoutSeconds:", "keep:"):
            self.assertIn(key, text)
        self.assertIn("github.ref == 'refs/heads/main' && github.actor == github.repository_owner", text)
        self.assertIn("name: sandbox-render", text)
        self.assertIn("persist-credentials: false", text)
        for uses_line in re.findall(r"uses:\s*(\S+)", text):
            self.assertRegex(uses_line, r"@[0-9a-f]{40}$", f"action must be SHA-pinned: {uses_line}")

    def test_runner_js_syntax_and_safety_guards(self):
        text = RUNNER.read_text(encoding="utf-8")
        self.assertIn("tea-dc3ge117lnhs73fnjpig", text)
        self.assertIn("sbg-dc3h680u01pc73eld1ng", text)
        self.assertIn("timeoutSeconds < 60", text)
        self.assertIn("await sandboxes.terminate(sandboxId)", text)
        self.assertIn("finally", text)
        self.assertNotIn("console.log(env.RENDER_API_KEY", text)
        if shutil.which("node"):
            res = subprocess.run(["node", "--check", str(RUNNER)], capture_output=True, text=True, timeout=30)
            self.assertEqual(res.returncode, 0, res.stderr)

    def test_setup_environments_defaults(self):
        text = SETUP_ENVS.read_text(encoding="utf-8")
        self.assertIn("sbg-dc3h680u01pc73eld1ng", text)
        self.assertIn("tea-dc3ge117lnhs73fnjpig", text)
        res = subprocess.run(["bash", "-n", str(SETUP_ENVS)], capture_output=True, text=True, timeout=30)
        self.assertEqual(res.returncode, 0, res.stderr)

    def test_docs_cover_cli_shellfish_actions_keep_and_pricing(self):
        text = DOC.read_text(encoding="utf-8")
        for needle in (
            "$0.12",
            "$0.038",
            "192.9.227.203",
            "136.66.157.149",
            "ShellFish",
            "SANDBOX_KEEP=true",
            "Actions",
        ):
            self.assertIn(needle, text)

    def test_live_demo_script_syntax_and_real_broker_execution(self):
        syn = subprocess.run(["bash", "-n", str(DEMO)], capture_output=True, text=True, timeout=30)
        self.assertEqual(syn.returncode, 0, syn.stderr)
        if not shutil.which("go") or not shutil.which("git"):
            self.skipTest("go and git required for local broker execution test")

        with tempfile.TemporaryDirectory() as tmp:
            binary = Path(tmp) / "collaborate"
            build_env = dict(os.environ, CGO_ENABLED="0")
            build = subprocess.run(
                ["go", "build", "-trimpath", "-buildvcs=false", "-o", str(binary), "./cmd/collaborate"],
                cwd=ROOT,
                env=build_env,
                capture_output=True,
                text=True,
                timeout=120,
            )
            self.assertEqual(build.returncode, 0, build.stderr)

            port = free_port()
            demo_env = {
                "PATH": os.environ.get("PATH", ""),
                "HOME": tmp,
                "COLLAB_BIN": str(binary),
                "DEMO_ROOT": str(Path(tmp) / "demo"),
                "BROKER_ADDR": f"127.0.0.1:{port}",
                "KEEP_BROKER": "false",
            }
            res = subprocess.run(["bash", str(DEMO)], env=demo_env, capture_output=True, text=True, timeout=60)
            self.assertEqual(res.returncode, 0, f"stdout:\n{res.stdout}\nstderr:\n{res.stderr}")
            self.assertIn("Queue item 1 is ready; please inspect inbox and review notes.txt.", res.stdout)
            self.assertIn("Read claude message from inbox; checking queue and running review.", res.stdout)
            self.assertIn('"ok": true', res.stdout)
            self.assertIn('"checked": 12', res.stdout)


if __name__ == "__main__":
    unittest.main()
