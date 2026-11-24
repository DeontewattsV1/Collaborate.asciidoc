import importlib.util
import io
from contextlib import redirect_stderr, redirect_stdout
import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

MODULE = Path(__file__).resolve().parents[1] / "scripts/agent_model_bridge.py"
spec = importlib.util.spec_from_file_location("agent_model_bridge", MODULE)
bridge = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bridge)
SHA = "a" * 40


def snapshot(providers=None, phase="review"):
    providers = providers or ["openai"]
    d = {"v": 1, "repository": bridge.REPO, "pr": 7, "head_sha": SHA,
         "providers": providers, "phase": phase,
         "files": [{"path": "a.txt", "status": "modified", "patch": "@@ -1 +1 @@\n-old\n+new"}],
         "provenance": "mock PR API", "snapshot_limit": {"files": 60, "patch_chars": 48000}}
    d["snapshot_sha256"] = bridge.digest(d)
    return d


class ScopingTests(unittest.TestCase):
    def test_provider_allowlist_and_cross_review_guard(self):
        self.assertEqual(bridge.providers_for("all", "challenge"), list(bridge.PROVIDERS))
        with self.assertRaises(ValueError):
            bridge.providers_for("openai", "challenge")
        with self.assertRaises(ValueError):
            bridge.providers_for("unexpected-env", "review")

    def test_snapshot_rejects_sha_mismatch_and_tampering(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d) / "snapshot.json"
            bridge.write_json(p, snapshot())
            self.assertEqual(bridge.load_snapshot(p, SHA)["pr"], 7)
            with self.assertRaisesRegex(ValueError, "authorized PR head"):
                bridge.load_snapshot(p, "b" * 40)
            altered = snapshot()
            altered["files"][0]["patch"] = "attacker replacement"
            bridge.write_json(p, altered)
            with self.assertRaisesRegex(ValueError, "SHA-256 mismatch"):
                bridge.load_snapshot(p)


    def test_scope_produces_digest_for_complete_unchanged_pr(self):
        info = {"state": "open", "base": {"ref": "main"}, "head": {"sha": SHA}, "changed_files": 1}
        files = [{"filename": "a.txt", "status": "modified", "patch": "@@ -1 +1 @@\n-a\n+b"}]
        with patch.object(bridge, "get_pr", side_effect=[info, info]), patch.object(bridge, "github_request", return_value=files):
            ss = bridge.scope_snapshot(7, "all", "challenge")
            self.assertEqual(ss["providers"], list(bridge.PROVIDERS))
            self.assertEqual(ss["snapshot_sha256"], bridge.digest({k:v for k,v in ss.items() if k != "snapshot_sha256"}))

    def test_scope_refuses_incomplete_binary_diff(self):
        info = {"state": "open", "base": {"ref": "main"}, "head": {"sha": SHA}, "changed_files": 1}
        with patch.object(bridge, "get_pr", return_value=info), patch.object(bridge, "github_request", return_value=[{"filename": "blob.bin", "patch": None}]):
            with self.assertRaisesRegex(ValueError, "missing textual patch"):
                bridge.scope_snapshot(7, "openai", "review")

    def test_scope_refuses_changed_head(self):
        info = {"state": "open", "base": {"ref": "main"}, "head": {"sha": SHA}, "changed_files": 1}
        later = {**info, "head": {"sha": "b" * 40}}
        files = [{"filename": "a.txt", "status": "modified", "patch": "good"}]
        with patch.object(bridge, "get_pr", side_effect=[info, later]), patch.object(bridge, "github_request", return_value=files):
            with self.assertRaisesRegex(ValueError, "PR changed"):
                bridge.scope_snapshot(7, "openai", "review")


class ProviderTests(unittest.TestCase):
    def test_missing_secret_and_invalid_model_fail_closed(self):
        with self.assertRaisesRegex(ValueError, "MODEL_API_KEY"):
            bridge.output_from_model("openai", "gpt-4.1-mini", "s", "p", "")
        with self.assertRaisesRegex(ValueError, "MODEL_NAME"):
            bridge.output_from_model("openai", "../../bad name", "s", "p", "secret")

    def test_provider_endpoints_are_fixed(self):
        self.assertEqual(bridge.REPO, "DeontewattsV1/Collaborate.asciidoc")
        with patch.object(bridge, "urlopen") as request:
            request.return_value.__enter__.return_value.read.return_value = json.dumps({"choices": [{"message": {"content": "NOT RUN"}}]}).encode()
            output = bridge.output_from_model("openai", "gpt-4.1-mini", "system", "prompt", "test-key")
            self.assertEqual(output, "NOT RUN")
            req = request.call_args[0][0]
            self.assertEqual(req.full_url, "https://api.openai.com/v1/chat/completions")
            self.assertEqual(json.loads(req.data)["max_completion_tokens"], bridge.TOKEN_BUDGET)


    def test_reasoning_budget_exhaustion_is_reported_clearly(self):
        cases = [
            ("openai", {"choices": [{"message": {"content": ""}, "finish_reason": "length"}]}, "whole 8192-token budget"),
            ("gemini", {"candidates": [{"content": {"parts": []}, "finishReason": "MAX_TOKENS"}]}, "used up by reasoning"),
            ("gemini", {"candidates": [], "promptFeedback": {"blockReason": "SAFETY"}}, "blockReason=SAFETY"),
            ("anthropic", {"content": [], "stop_reason": "max_tokens"}, "whole 8192-token budget"),
        ]
        for provider, response, want in cases:
            with self.subTest(provider=provider, want=want), patch.object(bridge, "urlopen") as call:
                call.return_value.__enter__.return_value.read.return_value = json.dumps(response).encode()
                with self.assertRaisesRegex(ValueError, want):
                    bridge.output_from_model(provider, "model-example", "system", "prompt", "test-key")

    def test_gemini_payload_uses_header_key_and_budget(self):
        with patch.object(bridge, "urlopen") as call:
            call.return_value.__enter__.return_value.read.return_value = json.dumps(
                {"candidates": [{"content": {"parts": [{"text": "ok"}]}, "finishReason": "STOP"}]}).encode()
            self.assertEqual(bridge.output_from_model("gemini", "gemini-3.8-flash", "s", "p", "g-key"), "ok")
            req = call.call_args[0][0]
            self.assertEqual(req.full_url, "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.8-flash:generateContent")
            self.assertEqual(req.get_header("X-goog-api-key"), "g-key")
            self.assertNotIn("g-key", req.full_url)
            body = json.loads(req.data)
            self.assertEqual(body["generationConfig"]["maxOutputTokens"], bridge.TOKEN_BUDGET)
            self.assertEqual(body["systemInstruction"]["parts"][0]["text"], "s")

    def test_openai_payload_uses_bearer_and_chat_roles(self):
        with patch.object(bridge, "urlopen") as call:
            call.return_value.__enter__.return_value.read.return_value = json.dumps(
                {"choices": [{"message": {"content": "ok"}, "finish_reason": "stop"}]}).encode()
            bridge.output_from_model("openai", "gpt-6.2-sol", "s", "p", "o-key")
            req = call.call_args[0][0]
            self.assertEqual(req.get_header("Authorization"), "Bearer o-key")
            body = json.loads(req.data)
            self.assertEqual([m["role"] for m in body["messages"]], ["system", "user"])
            self.assertNotIn("max_tokens", body)

    def test_provider_payloads_anthropic_gemini_xai(self):
        cases = [
            ("anthropic", {"content": [{"type": "text", "text": "anthropic result"}]}, "https://api.anthropic.com/v1/messages", "anthropic result"),
            ("gemini", {"candidates": [{"content": {"parts": [{"text": "gemini result"}]}}]}, "https://generativelanguage.googleapis.com/v1beta/models/model-example:generateContent", "gemini result"),
            ("xai", {"choices": [{"message": {"content": "xai result"}}]}, "https://api.x.ai/v1/chat/completions", "xai result"),
        ]
        for provider, response, endpoint, expected in cases:
            with self.subTest(provider=provider), patch.object(bridge, "urlopen") as call:
                call.return_value.__enter__.return_value.read.return_value = json.dumps(response).encode()
                actual = bridge.output_from_model(provider, "model-example", "system", "prompt", "test-key")
                self.assertEqual(actual, expected)
                self.assertEqual(call.call_args.args[0].full_url, endpoint)

    def test_provider_changes_cannot_authenticate_other_peer(self):
        ss = snapshot(["openai", "anthropic"], "challenge")
        with tempfile.TemporaryDirectory() as d:
            p = Path(d) / "review-anthropic.json"
            bad = bridge.evidence_record(ss, "openai", "review", "test", "text")
            bridge.write_json(p, bad)
            with self.assertRaisesRegex(ValueError, "unauthorized provider identity"):
                bridge.load_evidence(d, ss, "review")

    def test_challenge_reads_other_provider_review_not_self(self):
        ss = snapshot(["openai", "anthropic"], "challenge")
        with tempfile.TemporaryDirectory() as d:
            bridge.write_json(Path(d) / "review-openai.json", bridge.evidence_record(ss, "openai", "review", "model-a", "A opinion"))
            bridge.write_json(Path(d) / "review-anthropic.json", bridge.evidence_record(ss, "anthropic", "review", "model-b", "B opinion"))
            prompt = bridge.build_prompt(ss, "challenge", "openai", d)
            self.assertIn("B opinion", prompt)
            self.assertNotIn("A opinion", prompt)
            self.assertIn("untrusted", prompt.lower())

    def test_output_fence_keeps_untrusted_fence_inside(self):
        output = bridge.fence("hi\n```\nmalicious close\n```")
        self.assertTrue(output.startswith("````text\n"))
        self.assertTrue(output.endswith("\n````"))


class PublicationTests(unittest.TestCase):
    def test_publication_is_provenance_labeled_not_approval(self):
        ss = snapshot()
        rec = bridge.evidence_record(ss, "openai", "review", "gpt", "Potential issue; independent test NOT RUN")
        body = bridge.publication_text(ss, [rec], [], "1234")
        self.assertIn(bridge.MARKER, body)
        self.assertIn("not an independent human review", body)
        self.assertIn("NOT RUN", body)
        self.assertNotIn("MODEL_API_KEY", body)


    def test_publish_success_posts_bounded_comment_from_separate_stage(self):
        ss = snapshot()
        with tempfile.TemporaryDirectory() as d:
            base = Path(d)
            bridge.write_json(base / "snapshot.json", ss)
            (base / "reviews").mkdir()
            bridge.write_json(base / "reviews/review-openai.json", bridge.evidence_record(ss, "openai", "review", "gpt", "testable finding"))
            current = {"state": "open", "base": {"ref": "main"}, "head": {"sha": SHA}}
            with patch.object(bridge, "get_pr", return_value=current), patch.object(bridge, "github_request", return_value={"id": 12}) as post:
                rc = bridge.main(["publish", "--pr", "7", "--phase", "review", "--expected-sha", SHA,
                                  "--snapshot", str(base / "snapshot.json"), "--reviews", str(base / "reviews"),
                                  "--challenges", str(base / "missing"), "--github-run", "999"])
                self.assertEqual(rc, 0)
                self.assertEqual(post.call_args.kwargs["method"], "POST")
                self.assertEqual(post.call_args.args[0], "/issues/7/comments")
                self.assertIn("testable finding", post.call_args.kwargs["payload"]["body"])

    def test_publish_refuses_stale_pr(self):
        ss = snapshot()
        with tempfile.TemporaryDirectory() as d:
            base = Path(d)
            bridge.write_json(base / "snapshot.json", ss)
            (base / "reviews").mkdir()
            bridge.write_json(base / "reviews/review-openai.json", bridge.evidence_record(ss, "openai", "review", "gpt", "finding"))
            wrong_head = {"state": "open", "base": {"ref": "main"}, "head": {"sha": "b" * 40}, "changed_files": 1}
            with patch.object(bridge, "get_pr", return_value=wrong_head), patch.object(bridge, "github_request") as post:
                with redirect_stderr(io.StringIO()):
                    rc = bridge.main(["publish", "--pr", "7", "--phase", "review", "--expected-sha", SHA,
                                      "--snapshot", str(base / "snapshot.json"), "--reviews", str(base / "reviews"),
                                      "--challenges", str(base / "missing"), "--github-run", "999"])
                self.assertEqual(rc, 1)
                post.assert_not_called()


class UnconfiguredProviderTests(unittest.TestCase):
    """A provider without a key is skipped visibly, never treated as a pass of its review."""

    def _publish(self, base, providers, reviews):
        ss = snapshot(providers)
        bridge.write_json(base / "snapshot.json", ss)
        (base / "reviews").mkdir()
        for p in reviews:
            bridge.write_json(base / f"reviews/review-{p}.json", bridge.evidence_record(ss, p, "review", "m", f"{p} finding"))
        current = {"state": "open", "base": {"ref": "main"}, "head": {"sha": SHA}}
        with patch.object(bridge, "get_pr", return_value=current), patch.object(bridge, "github_request") as post:
            with redirect_stdout(io.StringIO()) as out:
                rc = bridge.main(["publish", "--pr", "7", "--phase", "review", "--expected-sha", SHA,
                                  "--snapshot", str(base / "snapshot.json"), "--reviews", str(base / "reviews"),
                                  "--challenges", str(base / "missing"), "--github-run", "1"])
        return rc, post, out.getvalue()

    def test_review_without_key_skips_with_warning_and_writes_nothing(self):
        ss = snapshot()
        with tempfile.TemporaryDirectory() as d, patch.dict(os.environ, {"MODEL_API_KEY": "", "MODEL_NAME": ""}):
            base = Path(d)
            bridge.write_json(base / "snapshot.json", ss)
            with patch.object(bridge, "urlopen") as call, redirect_stdout(io.StringIO()) as out:
                rc = bridge.main(["review", "--provider", "openai", "--snapshot", str(base / "snapshot.json"),
                                  "--expected-sha", SHA, "--out", str(base / "review-openai.json")])
            self.assertEqual(rc, 0)
            self.assertIn("::warning", out.getvalue())
            self.assertFalse((base / "review-openai.json").exists())
            call.assert_not_called()

    def test_key_without_valid_model_still_fails(self):
        ss = snapshot()
        with tempfile.TemporaryDirectory() as d, patch.dict(os.environ, {"MODEL_API_KEY": "k", "MODEL_NAME": "bad name!"}):
            base = Path(d)
            bridge.write_json(base / "snapshot.json", ss)
            with redirect_stderr(io.StringIO()):
                rc = bridge.main(["review", "--provider", "openai", "--snapshot", str(base / "snapshot.json"),
                                  "--expected-sha", SHA, "--out", str(base / "r.json")])
            self.assertEqual(rc, 1)

    def test_publish_with_no_reviews_posts_nothing(self):
        with tempfile.TemporaryDirectory() as d:
            rc, post, out = self._publish(Path(d), ["openai"], [])
        self.assertEqual(rc, 0)
        post.assert_not_called()
        self.assertIn("Nothing to publish", out)

    def test_partial_publish_names_missing_providers(self):
        with tempfile.TemporaryDirectory() as d:
            rc, post, _ = self._publish(Path(d), list(bridge.PROVIDERS), ["openai", "gemini"])
        self.assertEqual(rc, 0)
        body = post.call_args.kwargs["payload"]["body"]
        self.assertIn("openai finding", body)
        self.assertIn("not configured (no output):** anthropic, xai", body)


if __name__ == "__main__":
    unittest.main()
