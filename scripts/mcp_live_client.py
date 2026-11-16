#!/usr/bin/env python3
"""Minimal live model client that exposes only tools discovered from one MCP stdio server.

This is a pilot harness, not a production agent runtime. It uses a live
OpenAI-compatible Chat Completions endpoint and starts one per-agent
`collaborate mcp` subprocess. Provider credentials are not passed to the MCP
subprocess. No shell or filesystem tools are presented to the model.
"""
from __future__ import annotations

import argparse
import json
import os
import pathlib
import select
import stat
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
from typing import Any

PROTOCOL = "2025-06-18"
EXPECTED_TOOLS = {
    "collaborate_claim",
    "collaborate_renew",
    "collaborate_release",
    "collaborate_say",
    "collaborate_inbox",
    "collaborate_receipt",
}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req: Any, fp: Any, code: int, msg: str, headers: Any, newurl: str) -> None:
        return None


def load_credentials(filename: str) -> tuple[str, str]:
    flags = os.O_RDONLY | getattr(os, "O_CLOEXEC", 0) | getattr(os, "O_NOFOLLOW", 0)
    fd = os.open(filename, flags)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode):
            raise RuntimeError("credentials path must be a regular file")
        if info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise RuntimeError("credentials file must be owned by this UID and inaccessible to group/other")
        with os.fdopen(fd, "r", encoding="utf-8", closefd=False) as stream:
            credentials = json.load(stream)
    finally:
        os.close(fd)
    base = credentials.get("api_base")
    key = credentials.get("api_key")
    if not isinstance(base, str) or not base or not isinstance(key, str) or not key:
        raise RuntimeError("credentials file must contain non-empty api_base and api_key strings")
    return base, key


def validate_endpoint(base: str) -> None:
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme not in ("https", "http") or not parsed.hostname:
        raise RuntimeError("model API base must be an absolute HTTP(S) URL")
    if parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise RuntimeError("model API base must not contain credentials, query, or fragment")
    if parsed.scheme == "http" and parsed.hostname not in ("localhost", "127.0.0.1", "::1"):
        raise RuntimeError("cleartext model API is permitted only for loopback endpoints")


class MCPClient:
    def __init__(self, binary: str, repo: str, agent: str) -> None:
        # Intentionally omit all provider credentials and unrelated environment
        # variables from the broker-facing MCP process.
        env = {"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/nonexistent", "LANG": "C.UTF-8"}
        self.proc = subprocess.Popen(
            [binary, "mcp", "--repo", repo, "--agent", agent],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=sys.stderr,
            text=True,
            bufsize=1,
            env=env,
            close_fds=True,
        )
        self.next_id = 1

    def rpc(self, method: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
        assert self.proc.stdin is not None and self.proc.stdout is not None
        request_id = self.next_id
        self.next_id += 1
        request: dict[str, Any] = {"jsonrpc": "2.0", "id": request_id, "method": method}
        if params is not None:
            request["params"] = params
        self.proc.stdin.write(json.dumps(request, separators=(",", ":")) + "\n")
        self.proc.stdin.flush()
        ready, _, _ = select.select([self.proc.stdout], [], [], 20)
        if not ready:
            raise RuntimeError(f"timed out waiting for MCP response to {method}")
        line = self.proc.stdout.readline()
        if not line:
            raise RuntimeError(f"MCP server exited before replying to {method}")
        response = json.loads(line)
        if not isinstance(response, dict) or response.get("jsonrpc") != "2.0":
            raise RuntimeError(f"invalid MCP JSON-RPC response for {method}")
        if response.get("id") != request_id:
            raise RuntimeError(f"MCP response ID mismatch for {method}")
        if "error" in response:
            raise RuntimeError(f"MCP {method} failed: {response['error'].get('message', 'unknown error')}")
        return response.get("result", {})

    def start(self) -> list[dict[str, Any]]:
        initialized = self.rpc(
            "initialize",
            {
                "protocolVersion": PROTOCOL,
                "capabilities": {},
                "clientInfo": {"name": "collaborate-live-pilot", "version": "0.1.0"},
            },
        )
        if initialized.get("protocolVersion") != PROTOCOL:
            raise RuntimeError(f"unexpected MCP protocol version: {initialized.get('protocolVersion')}")
        assert self.proc.stdin is not None
        self.proc.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
        self.proc.stdin.flush()
        tools = self.rpc("tools/list").get("tools", [])
        names = {tool.get("name") for tool in tools}
        if names != EXPECTED_TOOLS:
            raise RuntimeError(f"unexpected MCP capability set: {sorted(names)}")
        # Probe that the server does not accidentally accept a shell capability.
        probe = self.rpc("tools/call", {"name": "shell_exec", "arguments": {}})
        if not probe.get("isError"):
            raise RuntimeError("MCP server unexpectedly accepted a shell tool")
        return tools

    def call(self, name: str, arguments: dict[str, Any]) -> str:
        result = self.rpc("tools/call", {"name": name, "arguments": arguments})
        if result.get("isError"):
            content = result.get("content", [])
            message = content[0].get("text", "tool error") if content else "tool error"
            raise RuntimeError(f"MCP tool {name} failed: {message}")
        content = result.get("content", [])
        return "\n".join(part.get("text", "") for part in content if part.get("type") == "text")

    def close(self) -> None:
        if self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=5)


def chat(base: str, key: str, model: str, messages: list[dict[str, Any]], tool: dict[str, Any]) -> dict[str, Any]:
    function = {
        "type": "function",
        "function": {
            "name": tool["name"],
            "description": tool.get("description", ""),
            "parameters": tool.get("inputSchema", {"type": "object", "properties": {}}),
        },
    }
    body: dict[str, Any] = {
        "model": model,
        "messages": messages,
        "tools": [function],
        "tool_choice": {"type": "function", "function": {"name": tool["name"]}},
        "reasoning": {"effort": "low"},
    }
    # Gemini's OpenAI-compatible route requires max_tokens; GPT uses
    # max_completion_tokens to leave room for its reasoning budget.
    if model.lower().startswith("gemini-"):
        body["max_tokens"] = 512
    else:
        body["max_completion_tokens"] = 512
    raw = json.dumps(body).encode()
    request = urllib.request.Request(
        base.rstrip("/") + "/chat/completions",
        data=raw,
        headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"},
        method="POST",
    )
    try:
        opener = urllib.request.build_opener(NoRedirect())
        with opener.open(request, timeout=180) as response:
            payload = json.loads(response.read())
    except urllib.error.HTTPError as exc:
        detail = exc.read(2048).decode("utf-8", "replace")
        raise RuntimeError(f"model API returned HTTP {exc.code}: {detail}") from None
    choices = payload.get("choices") or []
    if not choices:
        raise RuntimeError("model API returned no choices")
    return choices[0]["message"]


def required_call(message: dict[str, Any], expected: str) -> tuple[dict[str, Any], dict[str, Any]]:
    calls = message.get("tool_calls") or []
    if len(calls) != 1:
        raise RuntimeError(f"model did not return exactly one MCP tool call: {len(calls)}")
    call = calls[0]
    function = call.get("function", {})
    if function.get("name") != expected:
        raise RuntimeError(f"model selected {function.get('name')!r}; expected {expected!r}")
    try:
        arguments = json.loads(function.get("arguments", "{}"))
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"model returned invalid tool arguments: {exc}") from None
    if not isinstance(arguments, dict):
        raise RuntimeError("model tool arguments must be a JSON object")
    return call, arguments


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--role", choices=("sender", "responder"), required=True)
    parser.add_argument("--agent", required=True)
    parser.add_argument("--repo", required=True, help="private per-agent MCP config/key directory")
    parser.add_argument("--mcp-binary", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--marker", required=True, help="synthetic round-trip marker; do not include secrets/source")
    parser.add_argument("--credentials-file", help="temporary JSON file with api_base and api_key; never commit it")
    parser.add_argument("--own-workdir")
    parser.add_argument("--peer-workdir")
    parser.add_argument("--peer-key")
    parser.add_argument("--broker-private")
    parser.add_argument("--broker-db")
    parser.add_argument("--broker-key")
    args = parser.parse_args()

    if args.credentials_file:
        base, key = load_credentials(args.credentials_file)
    else:
        base = os.environ.get("OPENAI_API_BASE") or os.environ.get("OPENAI_BASE_URL")
        key = os.environ.get("OPENAI_API_KEY")
    if not base or not key:
        raise RuntimeError("OPENAI_API_BASE/OPENAI_BASE_URL and OPENAI_API_KEY are required")
    validate_endpoint(base)

    permission_checks: list[str] = []
    if args.own_workdir:
        own = pathlib.Path(args.own_workdir)
        fd, marker_name = tempfile.mkstemp(prefix=".permission-probe-", dir=own)
        own_marker = pathlib.Path(marker_name)
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as marker:
                marker.write(args.agent)
            if own_marker.read_text(encoding="utf-8") != args.agent:
                raise RuntimeError("could not read back own workdir probe")
        finally:
            own_marker.unlink(missing_ok=True)
        permission_checks.append("own-workdir-read-write:allowed")
    if args.peer_workdir:
        peer = pathlib.Path(args.peer_workdir)
        try:
            list(peer.iterdir())
        except PermissionError:
            permission_checks.append("peer-workdir-list:denied")
        else:
            raise RuntimeError("agent can list peer workdir")
        probe_path = peer / ".unauthorized-write-probe"
        try:
            fd = os.open(probe_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
        except PermissionError:
            permission_checks.append("peer-workdir-write:denied")
        else:
            os.close(fd)
            probe_path.unlink(missing_ok=True)
            raise RuntimeError("agent can write peer workdir")
    if args.broker_private:
        try:
            list(pathlib.Path(args.broker_private).iterdir())
        except PermissionError:
            permission_checks.append("broker-private-state-list:denied")
        else:
            raise RuntimeError("agent can list broker-private state")
    for label, filename in (("peer-private-key", args.peer_key), ("broker-database", args.broker_db), ("broker-private-key", args.broker_key)):
        if not filename:
            continue
        try:
            with open(filename, "rb") as protected:
                protected.read(1)
        except PermissionError:
            permission_checks.append(f"{label}-read:denied")
        else:
            raise RuntimeError(f"agent can read protected {label}")

    mcp = MCPClient(args.mcp_binary, args.repo, args.agent)
    try:
        tools = mcp.start()
        by_name = {tool["name"]: tool for tool in tools}
        if args.role == "sender":
            prompt = (
                f"For this synthetic MCP connectivity test, send exactly this marker to the shared "
                f"collaboration ledger using the provided tool: {args.marker}. Do not add any other content."
            )
            message = chat(
                base,
                key,
                args.model,
                [{"role": "user", "content": prompt}],
                by_name["collaborate_say"],
            )
            call, arguments = required_call(message, "collaborate_say")
            if set(arguments) != {"message"} or not isinstance(arguments["message"], str):
                raise RuntimeError("sender model returned invalid collaborate_say arguments")
            text = arguments["message"]
            if text != args.marker:
                raise RuntimeError("sender model did not return the exact synthetic marker")
            result = mcp.call("collaborate_say", {"message": text})
            print(json.dumps({"agent": args.agent, "model": args.model, "role": args.role, "mcp_tool": "collaborate_say", "broker_result": result, "permission_checks": permission_checks, "status": "ok"}))
            return 0

        inbox_message = chat(
            base,
            key,
            args.model,
            [{"role": "user", "content": "Read the shared collaboration inbox from sequence 0 using the provided tool. Do not reply yet."}],
            by_name["collaborate_inbox"],
        )
        inbox_call, inbox_arguments = required_call(inbox_message, "collaborate_inbox")
        if set(inbox_arguments) - {"after"}:
            raise RuntimeError("responder model returned unexpected inbox arguments")
        inbox_arguments.setdefault("after", 0)
        if isinstance(inbox_arguments["after"], bool) or not isinstance(inbox_arguments["after"], int) or inbox_arguments["after"] < 0:
            raise RuntimeError("responder model returned an invalid inbox cursor")
        inbox_result = mcp.call("collaborate_inbox", inbox_arguments)
        if args.marker not in inbox_result:
            raise RuntimeError("responder's MCP inbox did not contain the sender marker")

        response_message = chat(
            base,
            key,
            args.model,
            [
                {"role": "user", "content": f"The MCP inbox result is below. Reply to the other agent by calling the provided tool. Include this exact marker in your reply: {args.marker}\n\nInbox: {inbox_result}"},
            ],
            by_name["collaborate_say"],
        )
        _, response_arguments = required_call(response_message, "collaborate_say")
        if set(response_arguments) != {"message"} or not isinstance(response_arguments["message"], str):
            raise RuntimeError("responder model returned invalid collaborate_say arguments")
        reply = response_arguments["message"]
        if args.marker not in reply:
            raise RuntimeError("responder model's tool arguments omitted the synthetic marker")
        result = mcp.call("collaborate_say", {"message": reply})
        print(json.dumps({"agent": args.agent, "model": args.model, "role": args.role, "mcp_tools": ["collaborate_inbox", "collaborate_say"], "broker_result": result, "permission_checks": permission_checks, "status": "ok"}))
        return 0
    finally:
        mcp.close()


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"mcp_live_client: {exc}", file=sys.stderr)
        raise SystemExit(1)
