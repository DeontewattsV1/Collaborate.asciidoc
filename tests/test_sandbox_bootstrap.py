"""Offline checks for scripts/sandbox/bootstrap-ubuntu.sh and the iPhone sandbox guide.

The full admin+user flow was verified in a clean ubuntu:24.04 container (see
docs/IPHONE-SANDBOX.md); these tests guard the parts that can regress offline.
"""
import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "sandbox" / "bootstrap-ubuntu.sh"
GUIDE = ROOT / "docs" / "IPHONE-SANDBOX.md"


def run(*args, env=None):
    return subprocess.run(["bash", str(SCRIPT), *args], capture_output=True, text=True, env=env, timeout=60)


class BootstrapTest(unittest.TestCase):
    def test_syntax(self):
        self.assertEqual(subprocess.run(["bash", "-n", str(SCRIPT)]).returncode, 0)

    def test_usage_and_unknown_options(self):
        self.assertEqual(run().returncode, 2)
        self.assertEqual(run("nonsense").returncode, 2)

    def test_user_step_refuses_root_and_missing_tools(self):
        with tempfile.TemporaryDirectory() as home:
            env = {"HOME": home, "PATH": "/nonexistent"}
            res = subprocess.run(["/bin/bash", str(SCRIPT), "user", "--no-check"], capture_output=True, text=True,
                                 env=env, timeout=60)
            self.assertNotEqual(res.returncode, 0)
            if os.geteuid() == 0:
                self.assertIn("not root", res.stderr)
            else:
                self.assertIn("missing", res.stderr)

    def test_bashrc_block_is_marked_and_replaced(self):
        text = SCRIPT.read_text()
        self.assertIn("# >>> collaborate sandbox >>>", text)
        self.assertIn("# <<< collaborate sandbox <<<", text)
        # The awk filter that drops an old block before appending the new one.
        self.assertRegex(text, r"awk -v b=.*\$0==b\{skip=1\}")

    def test_agent_terminal_stays_on_loopback_without_exec(self):
        serve = [l for l in SCRIPT.read_text().splitlines() if "agentterm serve" in l]
        self.assertTrue(serve)
        for line in serve:
            self.assertIn("--addr 127.0.0.1:$port", line)
            self.assertNotIn("--allow-exec", line)
        self.assertIn('port="${AGENTTERM_PORT:-8787}"', SCRIPT.read_text())

    def test_admin_rejects_invalid_keys_before_touching_the_system(self):
        bad = [
            "not a key",
            "ssh-ed25519",
            "-----BEGIN OPENSSH PRIVATE KEY-----",
            "ssh-ed25519 AAAA;rm -rf / x",
            "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICezBZBmFJs6ePKSWbqyYErDAgrqHg6BHmhh1IpEHit5 a\nssh-rsa AAAA b",
        ]
        for key in bad:
            res = run("admin", "--key", key)
            self.assertNotEqual(res.returncode, 0, key)
            self.assertIn("not a valid SSH public key", res.stderr, key)
        res = run("admin", "--key")
        self.assertIn("needs a public key", res.stderr)

    def test_valid_key_passes_validation(self):
        key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICezBZBmFJs6ePKSWbqyYErDAgrqHg6BHmhh1IpEHit5 ShellFish@iPhone"
        src = SCRIPT.read_text().split("\nadmin() {")[0]
        res = subprocess.run(["bash", "-c", src.split("set -euo pipefail", 1)[1] + '\nvalid_pubkey "$1"', "_", key],
                             capture_output=True, text=True, timeout=30)
        self.assertEqual(res.returncode, 0, res.stderr)

    def test_user_rejects_bad_port(self):
        for port in ("80", "70000", "abc", "8080;id"):
            res = run("user", "--port", port)
            self.assertNotEqual(res.returncode, 0, port)
            self.assertIn("--port must be", res.stderr, port)

    def test_no_personal_endpoints_or_private_keys_committed(self):
        for path in (SCRIPT, GUIDE):
            text = path.read_text()
            self.assertNotRegex(text, r"[a-z0-9-]+\.[a-z0-9-]+\.ts\.net\b")
            self.assertNotIn("PRIVATE KEY-----", text)
            self.assertNotRegex(text, r"AAAA[A-Za-z0-9+/]{40,}")

    def test_no_word_processor_punctuation_in_commands(self):
        # The original guide shipped `gh run list –repo` (en-dash), which gh rejects.
        blocks = re.findall(r"```(?:bash)?\n(.*?)```", GUIDE.read_text(), re.S)
        inline = re.findall(r"`([^`\n]+)`", GUIDE.read_text())
        for text in [SCRIPT.read_text(), *blocks, *inline]:
            self.assertFalse(re.search(r"[–—‘’“”]", text), text[:80])

    def test_guide_branch_prefixes_match_registry(self):
        import json
        reg = json.loads((ROOT / ".github" / "agent-registry.json").read_text())
        active = {p for a in reg["agents"] if a["status"] == "active" for p in a["branch_prefixes"]}
        for prefix in ("claude/", "codex/", "gemini/", "manus/"):
            self.assertIn(prefix, active)
            self.assertIn(f"`{prefix}`", GUIDE.read_text())


if __name__ == "__main__":
    unittest.main()
