## Summary

<!-- Describe what this PR changes and why. Keep scope narrow and reversible. -->

<!-- Declare every file or directory this PR intends to touch (one per line) for the `coordinate` check: -->
Collaborate-Claim: path/to/file

## Agent & Branch Identity

- **Agent / Contributor:** <!-- human contributor or agent_id from .github/agent-registry.json (e.g., claude, codex, gemini, manus, tay) -->
- **Branch prefix:** <!-- e.g., claude/, codex/, gemini/, manus/, tay/ -->
- **Head SHA:** <!-- git rev-parse --short HEAD -->

## Evidence

Separate verified local/CI results from anything not executed (`PASS` / `FAIL` / `NOT RUN`). Never claim an external model or sandbox ran without attributable output.

| Check | Status | Notes |
|---|---|---|
| `scripts/check.sh` (full) | <!-- PASS / FAIL / NOT RUN --> | |
| Additional tests / live runs | <!-- PASS / FAIL / NOT RUN --> | |

## Threats & Remaining Unknowns

<!-- Note trust boundaries considered, security implications, or open questions. -->

## Checklist

- [ ] Read [`AGENTS.md`](../AGENTS.md) and checked open PRs for overlapping `Collaborate-Claim:` paths.
- [ ] Branched from up-to-date `origin/main` using a registered prefix (never pushed to `main` or another agent's branch).
- [ ] Verified no secrets, API keys, tokens, `.collab/`, or `.agentterm/` private keys are included.
- [ ] Leaving approval and merge to an independent human maintainer (never self-approving).
