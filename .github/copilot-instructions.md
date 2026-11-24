# Copilot instructions for Collaborate.asciidoc

Follow [AGENTS.md](../AGENTS.md), the single instruction file shared by all agents. It contains the repository map, safety rules and workflow.

- Before every push, verify with `scripts/check.sh`. It is exactly what the required `go-verify` check runs.
- Work on a `copilot/` branch. Open a draft PR with `Collaborate-Claim:` lines, and never merge your own PR.
- After solving a non-obvious problem, log it in `.learnings/` (format in `.claude/skills/self-improvement/SKILL.md`).
