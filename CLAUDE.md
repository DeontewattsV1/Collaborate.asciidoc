# Claude Code in Collaborate.asciidoc

Follow [AGENTS.md](AGENTS.md). It is the single, canonical instruction file shared by Claude, Codex, Gemini, Copilot, and other agents. It contains the repository map, safety rules, and workflow.

- Verify with `scripts/check.sh` before every push. It is exactly what the required `go-verify` check runs.
- Use the project skills in `.claude/skills/`: `collaborate-pr-workflow` (branches, claims, required checks) and `automate-recurring-errors` (turn a recurring mistake into a guard).
- Read [`docs/ENGINEERING-NOTES.md`](docs/ENGINEERING-NOTES.md) before a non-trivial change.
- Work on a `claude/` branch with a draft PR and `Collaborate-Claim:` lines. If a PR for the task is already open, continue on it. Never merge your own PR.
- Two pilots coexist (`cmd/collaborate` and `cmd/collab`). Keep their trust boundaries and evidence separate.
