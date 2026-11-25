# Security Policy

> **Prototype status:** `Collaborate.asciidoc` is an experimental multi-agent
> coordination prototype and is **NOT a production-ready security boundary**.
> Detailed verified properties, unguaranteed boundaries, and production release
> gates are documented in [`docs/SECURITY.md`](docs/SECURITY.md),
> [`docs/AGENT-TERMINAL.md`](docs/AGENT-TERMINAL.md), and
> [`docs/WASI-PLUGINS.md`](docs/WASI-PLUGINS.md).

## Supported Versions

Only the current `main` branch (`0.1.x` prototype) receives security fixes.

| Version / Branch | Supported | Notes |
|---|---|---|
| `main` (`0.1.x`) | Yes | Receives security and bug-fix PRs |
| Older snapshots / closed branches | No | Upgrade to `main` |

## Reporting a Vulnerability

**Please do not report security vulnerabilities, exposed credentials, or exploit
payloads in public GitHub issues, discussions, or pull requests.**

Report vulnerabilities privately through GitHub Security Advisories:

- **Private Security Advisory:**
  https://github.com/DeontewattsV1/Collaborate.asciidoc/security/advisories/new

If you cannot access the advisory form, open a minimal non-sensitive issue
requesting private contact with `@DeontewattsV1` without including exploit
details, tokens, or sensitive logs.

### What to include

- Affected component (`cmd/collaborate`, `cmd/collab`, `cmd/agentterm`,
  `cmd/agent-registry-check`, `.github/workflows/`, `scripts/`, etc.)
- Commit SHA or branch
- Clear reproduction steps or proof-of-concept
- Expected vs. actual security boundary behavior and impact

The maintainer reviews reports on a best-effort basis and will coordinate a fix
through a branch and pull request before public disclosure when applicable.

## Credential and Key Handling Rules

- Never commit `.collab/` directories, `.agentterm/` keys/logs, `.env` files,
  SSH private keys, or API tokens.
- Workflow API keys belong only in branch-restricted **GitHub Environments**
  (`agent-openai`, `agent-anthropic`, `agent-gemini`, `agent-xai`,
  `sandbox-opensandbox`, `sandbox-render`), created via
  `scripts/setup_github_environments.sh`, never in repository-wide secrets or
  source files.
- Privileged workflows (`pull_request_target`) must check out the trusted base
  commit (`${{ github.sha }}` or `${{ github.event.repository.default_branch }}`)
  with `persist-credentials: false`, never PR head code.
- If a credential is ever pasted into chat, an issue, or a commit, rotate or
  revoke it immediately at the provider.
