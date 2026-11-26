---
name: collaborate-pr-workflow
description: "How to land a change in Collaborate.asciidoc so every required check passes: branch naming, Collaborate-Claim lines, scripts/check.sh, the agent-registry and coordinate checks, and what to do when a check reads policy from main. Use before creating a branch or PR, and whenever agent-registry, coordinate, or go-verify fails."
---

# Collaborate PR Workflow

Each pull request goes through four gates: **`go-verify`**, **`agent-registry`**, **`coordinate`**, and one human approval. Follow these steps and the first three pass on the first push.

## Quick Reference

| Situation | Action |
|-----------|--------|
| Starting work | `git fetch origin && git switch -c <prefix>/<topic> origin/main`, where `<prefix>` is your registered one (`claude/`, `codex/`, `gemini/`, `manus/`, `tay/`) |
| Before opening a PR | List the open PRs and their files. Update an existing PR from your branch, or yield the paths an earlier PR holds |
| Before every push | `scripts/check.sh` (add `--quick` while iterating). It runs exactly what `go-verify` runs |
| PR description | Open as a draft. Add one `Collaborate-Claim: <path>` line per file or directory you will touch |
| `agent-registry` fails: "branch prefix is required" | Your branch lacks a registered prefix. Push to a new, correctly named branch |
| `agent-registry` fails: "not an active registered agent" | The registry on `main` lost your identity. A PR cannot fix this for itself: fix the registry in a PR, explain once that the check reads `main`, and leave the merge to the maintainer |
| `coordinate` fails | An earlier open PR holds a lease on a path you changed. Drop that path from your PR, or ask its owner for a handoff. Never edit around the lease |
| `go-verify` fails on "repository hygiene" | A tracked file is named like a shell command, or a workflow file is not a real workflow. Delete or fix it; see `docs/ENGINEERING-NOTES.md` |
| A push is rejected because the remote has commits you lack | `git fetch origin <branch> && git merge --no-edit FETCH_HEAD`, then push. Never force-push |

## Usage

1. **Read first:** `AGENTS.md` (rules and a map of the repository) and `docs/ENGINEERING-NOTES.md`.
2. **Branch:** create it from `origin/main` with your registered prefix. Identity comes from `.github/agent-registry.json`, by GitHub account ID plus branch prefix.
3. **Change, then run** `scripts/check.sh`. Fix everything it reports before you push.
4. **Open a draft PR.** Mirror `Collaborate-Claim:` lines for every path, and list the evidence: the exact commands, PASS/FAIL, what was NOT RUN, and the head SHA.
5. **Watch the checks** and fix the root cause of any failure. Never skip, disable, or weaken a test or check to get green.
6. **Leave it for review.** Never approve or merge your own PR. The maintainer merges.

## Why the registry check reads `main`

`agent-registry` reads `.github/agent-registry.json` from the protected base branch so a PR cannot authorize itself. If a PR must repair the registry, that PR will fail its own check until a maintainer merges it. State that once on the PR and stop; do not try to make the check pass from inside the PR.
