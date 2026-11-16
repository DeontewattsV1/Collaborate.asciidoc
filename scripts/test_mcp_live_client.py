import http.server
import json
import os
import tempfile
import threading
import unittest
from pathlib import Path

import mcp_live_client as pilot


class EndpointTests(unittest.TestCase):
    def test_validate_endpoint_allows_https_and_loopback_http(self):
        pilot.validate_endpoint("https://llm.example.invalid/v1")
        pilot.validate_endpoint("http://127.0.0.1:8080/v1")

    def test_validate_endpoint_rejects_cleartext_remote_and_url_secrets(self):
        with self.assertRaises(RuntimeError):
            pilot.validate_endpoint("http://model.example.invalid/v1")
        with self.assertRaises(RuntimeError):
            pilot.validate_endpoint("https://user:secret@model.example.invalid/v1")

    def test_credentials_require_private_owned_regular_file(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "provider.json"
            path.write_text(json.dumps({"api_base": "https://llm.example.invalid/v1", "api_key": "test-only"}))
            path.chmod(0o600)
            base, key = pilot.load_credentials(str(path))
            self.assertEqual(base, "https://llm.example.invalid/v1")
            self.assertEqual(key, "test-only")
            path.chmod(0o644)
            with self.assertRaises(RuntimeError):
                pilot.load_credentials(str(path))

    @unittest.skipUnless(hasattr(os, "O_NOFOLLOW"), "requires O_NOFOLLOW support")
    def test_credentials_reject_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / "target.json"
            target.write_text(json.dumps({"api_base": "https://llm.example.invalid/v1", "api_key": "test-only"}))
            target.chmod(0o600)
            link = Path(directory) / "link.json"
            link.symlink_to(target)
            with self.assertRaises(OSError):
                pilot.load_credentials(str(link))

    def test_required_call_requires_expected_name_and_object_arguments(self):
        good = {"tool_calls": [{"function": {"name": "collaborate_say", "arguments": '{"message":"marker"}'}}]}
        _, arguments = pilot.required_call(good, "collaborate_say")
        self.assertEqual(arguments, {"message": "marker"})
        wrong = {"tool_calls": [{"function": {"name": "shell_exec", "arguments": "{}"}}]}
        with self.assertRaises(RuntimeError):
            pilot.required_call(wrong, "collaborate_say")
        non_object = {"tool_calls": [{"function": {"name": "collaborate_say", "arguments": "[]"}}]}
        with self.assertRaises(RuntimeError):
            pilot.required_call(non_object, "collaborate_say")

    def test_model_api_redirect_is_not_followed(self):
        hits = []

        class RedirectHandler(http.server.BaseHTTPRequestHandler):
            target = ""

            def do_POST(self):
                self.send_response(302)
                self.send_header("Location", self.target)
                self.end_headers()

            def log_message(self, *_args):
                pass

        class SinkHandler(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                hits.append(self.path)
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"choices":[{"message":{"content":"unexpected"}}]}')

            def log_message(self, *_args):
                pass

        sink = http.server.ThreadingHTTPServer(("127.0.0.1", 0), SinkHandler)
        redirect = http.server.ThreadingHTTPServer(("127.0.0.1", 0), RedirectHandler)
        RedirectHandler.target = f"http://127.0.0.1:{sink.server_port}/steal"
        threads = [threading.Thread(target=server.serve_forever, daemon=True) for server in (sink, redirect)]
        for thread in threads:
            thread.start()
        try:
            with self.assertRaises(RuntimeError):
                pilot.chat(
                    f"http://127.0.0.1:{redirect.server_port}/v1",
                    "test-only",
                    "gpt-6-luna",
                    [{"role": "user", "content": "synthetic"}],
                    {"name": "collaborate_say", "inputSchema": {"type": "object", "properties": {}}},
                )
            self.assertEqual(hits, [])
        finally:
            for server in (redirect, sink):
                server.shutdown()
                server.server_close()
            for thread in threads:
                thread.join(timeout=2)


if __name__ == "__main__":
    unittest.main()
