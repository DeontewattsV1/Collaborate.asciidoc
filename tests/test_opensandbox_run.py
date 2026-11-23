"""Offline tests for scripts/opensandbox_run.py using a fake OpenSandbox SDK."""
import argparse
import importlib.util
import io
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace

SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "opensandbox_run.py"
spec = importlib.util.spec_from_file_location("opensandbox_run", SCRIPT)
osr = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = osr
spec.loader.exec_module(osr)

ENV = {"OPEN_SANDBOX_API_KEY": "k-test", "OPEN_SANDBOX_DOMAIN": "api.example.com"}


class FakeSandbox:
    created = []

    def __init__(self, exit_code=0, error=None, complete=True):
        self.id = "sbx_fake"
        self.files = SimpleNamespace(write_file=self._write)
        self.commands = SimpleNamespace(run=self._run)
        self.written = {}
        self.ran = []
        self.killed = False
        self.closed = False
        self._result = SimpleNamespace(exit_code=exit_code, error=error,
                                       complete=object() if complete else None)

    def _write(self, path, data):
        self.written[path] = data

    def _run(self, command, opts=None, handlers=None):
        self.ran.append(command)
        handlers.on_stdout(SimpleNamespace(text="ok  collaborate\n"))
        return self._result

    def kill(self):
        self.killed = True

    def close(self):
        self.closed = True


def fake_sdk(sandbox):
    class Sandbox:
        @staticmethod
        def create(image, **kw):
            Sandbox.kw = dict(kw, image=image)
            return sandbox
    return {"Config": lambda **kw: SimpleNamespace(**kw), "Opts": lambda **kw: kw,
            "Handlers": lambda **kw: SimpleNamespace(**kw), "Sandbox": Sandbox}


class OpenSandboxRunTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.archive = Path(self.tmp.name) / "src.tar.gz"
        self.archive.write_bytes(b"\x1f\x8bfake")

    def tearDown(self):
        self.tmp.cleanup()

    def args(self, **kw):
        base = dict(archive=str(self.archive), image="golang:1.26", command="go test ./...",
                    workdir="/workspace/repo", lifetime_minutes=30, command_timeout_minutes=20, keep=False)
        base.update(kw)
        return argparse.Namespace(**base)

    def test_success_uploads_runs_and_kills(self):
        sb = FakeSandbox(exit_code=0)
        sdk = fake_sdk(sb)
        out = io.StringIO()
        self.assertEqual(osr.run(self.args(), ENV, sdk, out), 0)
        self.assertEqual(sb.written[osr.REMOTE_ARCHIVE], b"\x1f\x8bfake")
        self.assertIn("tar -xzf /tmp/collaborate-src.tar.gz -C /workspace/repo", sb.ran[0])
        self.assertTrue(sb.ran[0].endswith("go test ./..."))
        self.assertTrue(sb.killed and sb.closed)
        self.assertEqual(sdk["Sandbox"].kw["connection_config"].protocol, "https")
        self.assertIn("ok  collaborate", out.getvalue())
        self.assertNotIn("k-test", out.getvalue())

    def test_failing_command_fails_and_still_kills(self):
        sb = FakeSandbox(exit_code=1)
        self.assertEqual(osr.run(self.args(), ENV, fake_sdk(sb), io.StringIO()), 1)
        self.assertTrue(sb.killed)

    def test_error_or_incomplete_stream_is_never_success(self):
        sb = FakeSandbox(exit_code=0, error=SimpleNamespace(name="Timeout", value="x"))
        self.assertEqual(osr.run(self.args(), ENV, fake_sdk(sb), io.StringIO()), 1)
        sb = FakeSandbox(exit_code=None, complete=False)
        self.assertEqual(osr.run(self.args(), ENV, fake_sdk(sb), io.StringIO()), 1)

    def test_keep_leaves_sandbox(self):
        sb = FakeSandbox()
        osr.run(self.args(keep=True), ENV, fake_sdk(sb), io.StringIO())
        self.assertFalse(sb.killed)

    def test_config_is_validated_before_any_call(self):
        for env, want in [({}, "OPEN_SANDBOX_API_KEY"),
                          ({"OPEN_SANDBOX_API_KEY": "k"}, "OPEN_SANDBOX_DOMAIN"),
                          ({**ENV, "OPEN_SANDBOX_DOMAIN": "https://x/y"}, "host[:port]"),
                          ({**ENV, "OPEN_SANDBOX_PROTOCOL": "http"}, "plain http")]:
            with self.assertRaisesRegex(ValueError, want.replace("[", r"\[").replace("]", r"\]")):
                osr.run(self.args(), env, fake_sdk(FakeSandbox()), io.StringIO())
        with self.assertRaisesRegex(ValueError, "image"):
            osr.run(self.args(image="x; rm -rf /"), ENV, fake_sdk(FakeSandbox()), io.StringIO())

    def test_workdir_is_shell_quoted(self):
        script = osr.build_script("/tmp/a b;x", "true")
        self.assertIn("'/tmp/a b;x'", script)


if __name__ == "__main__":
    unittest.main()
