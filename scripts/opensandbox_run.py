#!/usr/bin/env python3
"""Run one command against a source archive inside a live OpenSandbox sandbox.

The archive is uploaded into a fresh sandbox, extracted, and the command runs
there; nothing from the archive executes on the calling machine. The
sandbox is always killed afterwards unless --keep is given. The process exits
with the sandbox command's exit code, so a failing test fails the workflow.

Configuration comes only from the environment (never from arguments, so keys
do not appear in process listings or logs):
  OPEN_SANDBOX_API_KEY   API key (required)
  OPEN_SANDBOX_DOMAIN    API host[:port] of your OpenSandbox server (required)
  OPEN_SANDBOX_PROTOCOL  https (default) or http
"""
from __future__ import annotations

import argparse
import os
import re
import shlex
import sys
from datetime import timedelta
from pathlib import Path

DOMAIN = re.compile(r"^[A-Za-z0-9.-]+(:[0-9]{1,5})?$")
IMAGE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$")
MAX_ARCHIVE = 50 * 1024 * 1024
REMOTE_ARCHIVE = "/tmp/collaborate-src.tar.gz"


def config_from_env(env: dict[str, str]) -> dict[str, str]:
    key = env.get("OPEN_SANDBOX_API_KEY", "")
    domain = env.get("OPEN_SANDBOX_DOMAIN", "")
    protocol = env.get("OPEN_SANDBOX_PROTOCOL", "") or "https"
    if not key:
        raise ValueError("OPEN_SANDBOX_API_KEY is not set (add it as a secret of the sandbox-opensandbox environment)")
    if not domain or not DOMAIN.fullmatch(domain):
        raise ValueError("OPEN_SANDBOX_DOMAIN must be a host[:port] such as api.example.com (environment variable)")
    if protocol not in ("https", "http"):
        raise ValueError("OPEN_SANDBOX_PROTOCOL must be https or http")
    if protocol == "http" and not domain.split(":")[0] in ("localhost", "127.0.0.1"):
        raise ValueError("refusing to send the API key over plain http to a non-local host")
    return {"api_key": key, "domain": domain, "protocol": protocol}


def build_script(workdir: str, command: str) -> str:
    """Shell text run in the sandbox: extract the archive, then run the command."""
    w = shlex.quote(workdir)
    return (f"set -e; mkdir -p {w}; tar -xzf {REMOTE_ARCHIVE} -C {w}; rm -f {REMOTE_ARCHIVE}; "
            f"cd {w}; {command}")


def run(args: argparse.Namespace, env: dict[str, str], sdk=None, out=sys.stdout) -> int:
    cfg = config_from_env(env)
    if not IMAGE.fullmatch(args.image):
        raise ValueError("invalid image reference")
    archive = Path(args.archive)
    data = archive.read_bytes()
    if not data or len(data) > MAX_ARCHIVE:
        raise ValueError("archive must be 1 byte to 50 MiB")
    if not args.command.strip():
        raise ValueError("command is empty")
    if sdk is None:  # imported lazily so tests can supply a fake SDK
        from opensandbox.config import ConnectionConfigSync
        from opensandbox.models.execd import RunCommandOpts
        from opensandbox.models.execd_sync import ExecutionHandlersSync
        from opensandbox.sync import SandboxSync
        sdk = {"Config": ConnectionConfigSync, "Opts": RunCommandOpts,
               "Handlers": ExecutionHandlersSync, "Sandbox": SandboxSync}

    conn = sdk["Config"](api_key=cfg["api_key"], domain=cfg["domain"], protocol=cfg["protocol"])
    sandbox = sdk["Sandbox"].create(args.image, timeout=timedelta(minutes=args.lifetime_minutes),
                                    ready_timeout=timedelta(minutes=3), connection_config=conn,
                                    metadata={"source": "collaborate-github-actions"})
    print(f"sandbox {sandbox.id} created from {args.image}", file=out, flush=True)
    try:
        sandbox.files.write_file(REMOTE_ARCHIVE, data)

        def emit(stream):
            def handler(msg):
                stream.write(getattr(msg, "text", str(msg)))
                if not getattr(msg, "text", "").endswith("\n"):
                    stream.write("\n")
                stream.flush()
            return handler

        handlers = sdk["Handlers"](on_stdout=emit(out), on_stderr=emit(out))
        opts = sdk["Opts"](timeout=timedelta(minutes=args.command_timeout_minutes))
        execution = sandbox.commands.run(build_script(args.workdir, args.command),
                                         opts=opts, handlers=handlers)
        if getattr(execution, "error", None):
            err = execution.error
            print(f"sandbox command error: {getattr(err, 'name', '')}: {getattr(err, 'value', err)}", file=out)
            code = execution.exit_code if isinstance(execution.exit_code, int) and execution.exit_code else 1
        elif getattr(execution, "complete", None) is None:
            print("sandbox command stream ended before completion; result unknown", file=out)
            code = 1
        else:
            code = execution.exit_code if isinstance(execution.exit_code, int) else 1
        print(f"sandbox command exit code {code}", file=out, flush=True)
        return code
    finally:
        if args.keep:
            print(f"--keep: sandbox {sandbox.id} left running until its lifetime expires", file=out)
        else:
            try:
                sandbox.kill()
                print(f"sandbox {sandbox.id} deletion requested", file=out)
            finally:
                close = getattr(sandbox, "close", None)
                if close:
                    close()


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--archive", required=True, help="local .tar.gz of the source to test")
    ap.add_argument("--image", default="golang:1.26")
    ap.add_argument("--command", required=True, help="shell command run inside the extracted source")
    ap.add_argument("--workdir", default="/workspace/repo")
    ap.add_argument("--lifetime-minutes", type=int, default=30)
    ap.add_argument("--command-timeout-minutes", type=int, default=20)
    ap.add_argument("--keep", action="store_true")
    args = ap.parse_args(argv)
    try:
        return run(args, dict(os.environ))
    except (ValueError, OSError) as exc:
        print("ERROR: " + str(exc), file=sys.stderr)
        return 2
    except Exception as exc:  # SDK/network failures: report the type and message, never the config
        print(f"ERROR: OpenSandbox call failed: {type(exc).__name__}: {exc}", file=sys.stderr)
        return 3


if __name__ == "__main__":
    raise SystemExit(main())
