#!/usr/bin/env python3
"""Repository hygiene guard: catches repository-shape mistakes before CI.

  - a tracked path whose name looks like a pasted shell command
  - a workflow file that is not a workflow, or that builds a missing Dockerfile

Usage: python3 scripts/repo_hygiene.py [repo_root]   (exit 1 on any finding)
"""
from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

# A tracked path that starts like a shell command is almost certainly a
# command pasted into GitHub's "Add file" box.
COMMAND_WORDS = ("bash", "sh", "go", "gh", "git", "npm", "npx", "node", "python", "python3",
                 "pip", "docker", "curl", "cd", "export", "sudo", "make", "uv", "osb")
COMMAND_PATH = re.compile(r"^(?:.*/)?(?:%s) " % "|".join(map(re.escape, COMMAND_WORDS)))
WORKFLOW_SUFFIXES = (".yml", ".yaml")


def tracked_files(root: Path) -> list[str]:
    out = subprocess.run(["git", "-C", str(root), "ls-files", "-z"], check=True,
                         capture_output=True).stdout.decode()
    return [p for p in out.split("\0") if p]


def check(root: Path, files: list[str]) -> list[str]:
    problems: list[str] = []
    for path in files:
        if COMMAND_PATH.match(path) or (" " in path and re.search(r"(^|/)(--|\./)|\s-{1,2}[a-z]", path)):
            problems.append(f"{path!r}: file name looks like a shell command; run it in a terminal "
                            "instead of committing it (see docs/ENGINEERING-NOTES.md)")
        if path.startswith(".github/workflows/"):
            rel = path[len(".github/workflows/"):]
            if "/" in rel or not rel.endswith(WORKFLOW_SUFFIXES):
                problems.append(f"{path!r}: only .yml/.yaml workflow files belong directly in "
                                ".github/workflows/")
                continue
            text = (root / path).read_text(encoding="utf-8", errors="replace")
            if not re.search(r"(?m)^on:", text) and not re.search(r"(?m)^['\"]?on['\"]?:", text):
                problems.append(f"{path!r}: no top-level 'on:' trigger; this is not a workflow "
                                "(a pasted usage snippet?)")
            if not re.search(r"(?m)^jobs:", text):
                problems.append(f"{path!r}: no top-level 'jobs:'; this is not a workflow")
            if "docker/build-push-action" in text and re.search(r"(?m)^\s*context:\s*\.\s*$", text):
                if not (root / "Dockerfile").is_file():
                    problems.append(f"{path!r}: builds the repository root but there is no Dockerfile")
    return problems


def main(argv: list[str] | None = None) -> int:
    root = Path((argv or sys.argv[1:] or ["."])[0]).resolve()
    problems = check(root, tracked_files(root))
    for p in problems:
        print("hygiene: " + p, file=sys.stderr)
    if problems:
        print(f"hygiene: {len(problems)} problem(s); see docs/ENGINEERING-NOTES.md", file=sys.stderr)
        return 1
    print("hygiene: OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
