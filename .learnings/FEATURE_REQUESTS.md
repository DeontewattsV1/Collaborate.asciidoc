# Feature requests

Capabilities the maintainer asked for. Format: `.claude/skills/self-improvement/SKILL.md`.

---

## [FEAT-20261124-001] per_agent_github_identity

**Logged**: 2026-11-24T21:00:00Z · **Priority**: high · **Status**: pending · **Area**: config

### Requested Capability
Agents exchange work without being able to assume another agent's identity.

### User Context
Claude, Codex/ChatGPT, Manus and Gemini all act through the shared `DeontewattsV1` account. The registry, the session hub and `agentterm` can prove which GitHub account and session key acted. They cannot prove which model acted, because anyone holding the token can choose any branch prefix.

### Complexity Estimate
medium (mostly maintainer setup)

### Suggested Implementation
Give each agent its own GitHub App or machine account, register it in `.github/agent-registry.json` with its numeric ID and branch prefix, and retire the shared-account entries.

### Metadata
- Frequency: recurring · Related Features: agent-registry, agentterm, session hub

---

## [FEAT-20261124-002] restore_session_hub

**Logged**: 2026-11-24T21:00:00Z · **Priority**: medium · **Status**: pending · **Area**: infra

### Requested Capability
The issue #34 roster where agents check in and out (join, heartbeat, leave).

### User Context
Revert `5bf7b37` (#69) removed it: `agent-collaboration.yml`, `scripts/session_hub.py`, its tests and docs. It is unclear whether the removal was intended.

### Complexity Estimate
simple (restore from `5bf7b37^`), plus the issue #44 role-pinning fix, which is ready with 23/23 tests passing.

### Suggested Implementation
Wait for the maintainer's decision, then restore the hub in one PR and the #44 fix in a separate PR.

### Metadata
- Frequency: first_time · Related Features: agentterm (which supersedes much of it)

---

## [FEAT-20261124-003] hosted_agent_terminal

**Logged**: 2026-11-24T21:00:00Z · **Priority**: medium · **Status**: in_progress · **Area**: infra

### Requested Capability
One shared, visual, continuously synchronized terminal that remote agents can join and rejoin.

### User Context
`agentterm` is built and is published as `ghcr.io/deontewattsv1/collaborate.asciidoc`. It runs only on localhost until someone hosts it.

### Complexity Estimate
medium

### Suggested Implementation
Choose a host, put TLS in front of it, and set `AGENTTERM_READ_TOKEN`. Back up `server.key`.

### Metadata
- Related Files: Dockerfile, docs/AGENT-TERMINAL.md
