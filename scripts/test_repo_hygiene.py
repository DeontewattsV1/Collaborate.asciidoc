"""Tests for scripts/repo_hygiene.py."""
import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "repo_hygiene.py"
spec = importlib.util.spec_from_file_location("repo_hygiene", SCRIPT)
hygiene = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = hygiene
spec.loader.exec_module(hygiene)

GOOD_WORKFLOW = "name: x\non:\n  push:\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps: []\n"
SNIPPET = "- name: sandbox-launch-action\n  uses: crafting-dev/sandbox-launch-action@v0.5.3\n  with:\n    template:\n"
DOCKER_WF = GOOD_WORKFLOW + "      - uses: docker/build-push-action@x\n        with:\n          context: .\n"


class HygieneTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        (self.root / ".github/workflows").mkdir(parents=True)

    def tearDown(self):
        self.tmp.cleanup()

    def write(self, path, text="x"):
        p = self.root / path
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)
        return path

    def test_commands_committed_as_files_are_caught(self):
        # File names that look like pasted commands.
        for name in ["go run ./cmd/agentterm serve --registry .github/agent-registry.json",
                     "gh auth login bash scripts/setup_github_environments.sh",
                     "bash scripts/setup_github_environments.sh",
                     "node run-command.mjs"]:
            with self.subTest(name=name):
                self.assertTrue(hygiene.check(self.root, [self.write(name)]), name)

    def test_ordinary_paths_with_spaces_pass(self):
        files = [self.write("Design Notes"), self.write("docs/AGENT-TERMINAL.md"), self.write("go.mod"),
                 self.write("scripts/sandbox/bootstrap-ubuntu.sh")]
        self.assertEqual(hygiene.check(self.root, files), [])

    def test_workflow_dir_holds_only_real_workflows(self):
        ok = self.write(".github/workflows/ok.yml", GOOD_WORKFLOW)
        self.assertEqual(hygiene.check(self.root, [ok]), [])
        snippet = self.write(".github/workflows/sandbox-launch-action.yml", SNIPPET)
        self.assertEqual(len(hygiene.check(self.root, [snippet])), 2)  # no on:, no jobs:
        stray = self.write(".github/workflows/crafting-dev/x/.sdk", SNIPPET)
        mjs = self.write(".github/workflows/node run-command.mjs", "x")
        self.assertTrue(hygiene.check(self.root, [stray]))
        self.assertTrue(hygiene.check(self.root, [mjs]))

    def test_docker_workflow_needs_a_dockerfile(self):
        wf = self.write(".github/workflows/docker-publish.yml", DOCKER_WF)
        self.assertTrue(any("Dockerfile" in p for p in hygiene.check(self.root, [wf])))
        self.write("Dockerfile", "FROM scratch\n")
        self.assertEqual(hygiene.check(self.root, [wf]), [])

    def test_this_repository_is_clean(self):
        root = Path(__file__).resolve().parents[1]
        problems = hygiene.check(root, hygiene.tracked_files(root))
        self.assertEqual(problems, [], "\n".join(problems))

    def test_cli_exit_codes(self):
        root = Path(__file__).resolve().parents[1]
        self.assertEqual(subprocess.run([sys.executable, str(SCRIPT), str(root)],
                                        capture_output=True).returncode, 0)


if __name__ == "__main__":
    unittest.main()
