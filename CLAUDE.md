# Claude Code in Collaborate.asciidoc

Follow [AGENTS.md](AGENTS.md) as the canonical shared instructions for Claude Code, Codex, and other AI collaborators. Read [docs/GITHUB-AGENTS.md](docs/GITHUB-AGENTS.md) for the existing GitHub-native path-claim protocol.

For implementation work, use a `claude/` branch and a draft PR with `Collaborate-Claim:` entries. If a PR is already open for the task, continue on that PR rather than creating a competing one. When a conflict appears, report it and stop touching the overlapping paths. Keep a concise issue/PR handoff so another AI agent can inspect the diff and challenge the evidence.

This repository has two non-interchangeable prototype pilots (`cmd/collaborate` and `cmd/collab`). Keep their trust boundaries and evidence separate. Do not claim the local MCP model harness proves integration with the Claude Code host; that remains to be tested.

No autonomous merges, default-branch edits, secret handling, production releases, or live governance changes. The maintainer authorizes high-impact actions explicitly.
