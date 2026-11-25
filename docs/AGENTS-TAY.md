# Who is Tay?

**Tay** (`agent_id: "tay"`) is an AI assistant operated by repository owner Deonté Watts (`@DeontewattsV1`) through **Grok Bot**.

Some paths named in the log below (Render sandbox demo, OpenSandbox workflow) were removed later because they were not part of Dev Guild. This page stays as the record of what Tay did.

## Identity, Attribution, and Boundaries

| Property | Value / Detail |
|---|---|
| **Registry ID** | `tay` ([`.github/agent-registry.json`](../.github/agent-registry.json)) |
| **Git author identity** | `DeontewattsV1 <284501602+DeontewattsV1@users.noreply.github.com>` |
| **GitHub account** | `DeontewattsV1` (user ID `284501602`, shared with the maintainer and other registered agents) |
| **Branch prefixes** | `tay/` (registered in PR #78) and `claude/` (used before `tay/` was registered on `main` and for the community-profile PR) |
| **Tool allowlist** | `[]` (no runtime MCP/WASI tool grants in `.github/agent-registry.json`) |
| **Merge authority** | None. Cannot push to `main`, approve PRs, bypass rulesets, or self-merge. All repository changes land only through human-approved PRs. |

### Attribution note: Tay vs. Claude Code and other agents

Several earlier `claude/` pull requests in this repository (`#15`, `#17`, `#18`, `#33`, `#43`, `#53`, `#64`, `#67`, `#73`, `#74`, `#75`, and the initial commits of `#76`) were authored by Anthropic's Claude Code (`Claude <noreply@anthropic.com>`), **not** Tay. Work committed by Tay uses the maintainer's `DeontewattsV1 <284501602+DeontewattsV1@users.noreply.github.com>` git identity (Tay is an assistant name only, not a separate GitHub user account). Follow-up free-tier Codespaces/Oracle commits in PR `#76` (`51780f5`, `e1b87a8`) were likewise committed under the maintainer's `DeontewattsV1` identity during the ShellFish sandbox setup.

Like all agents sharing the `DeontewattsV1` account, a branch prefix labels who claims a change rather than providing cryptographic GitHub-account separation; per-agent GitHub Apps or machine accounts remain an open improvement ([`.learnings/FEATURE_REQUESTS.md`](../.learnings/FEATURE_REQUESTS.md)).

---

## Traceable Repository Changes (Pull Requests & Commits)

| PR | Branch (`head -> base`) | Status | Commits | Summary of Changes |
|---|---|---|---|---|
| [#77](https://github.com/DeontewattsV1/Collaborate.asciidoc/pull/77) | `tay/fix-code-scanning-1-2 -> main` | **OPEN** | `1a51e4e`, `6bee198` | Fixes GitHub Code Scanning alerts [#1](https://github.com/DeontewattsV1/Collaborate.asciidoc/security/code-scanning/1) (`internal/repoops/repo.go:42`) and [#2](https://github.com/DeontewattsV1/Collaborate.asciidoc/security/code-scanning/2) (`internal/agentterm/exec.go:108`) for `go/command-injection`, with regression tests in `internal/repoops/repo_test.go` and `internal/agentterm/agentterm_test.go`. |
| [#78](https://github.com/DeontewattsV1/Collaborate.asciidoc/pull/78) | `claude/register-tay-branch-prefix -> main` | **MERGED** (2026-11-25 09:29 PT, `da2d8df`) | `29b7f9c` | Registers `tay` (`tay/`) as `active` under `DeontewattsV1` (`284501602`) in `.github/agent-registry.json` and pins the mapping in `cmd/agent-registry-check/main_test.go`. Note: open PR [#80](https://github.com/DeontewattsV1/Collaborate.asciidoc/pull/80) proposes reverting this registration; until #80 is merged or closed, `tay/` remains active on `main`. |
| [#79](https://github.com/DeontewattsV1/Collaborate.asciidoc/pull/79) | `claude/render-demo-sandbox -> main` | **MERGED** (2026-11-25 09:28 PT, `85d2da9`) | `419cb3b` | Adds a live two-agent `collaborate` broker + MCP + Echo/LLM adapter demo for Render Sandboxes (`scripts/sandbox/render-sandbox.mjs`, `scripts/sandbox/render-demo.sh`, `docs/RENDER-SANDBOX.md`, `tests/test_render_sandbox.py`) and updates `.github/workflows/render-sandbox.yml`, `docs/LIVE-SETUP.md`, and `scripts/setup_github_environments.sh`. Verified live with a 12-event SHA-256 hash-chained ledger before shutting down the sandbox. |
| [#76](https://github.com/DeontewattsV1/Collaborate.asciidoc/pull/76) | `claude/iphone-sandbox-bootstrap -> main` | **MERGED** (2026-11-25 08:37 PT, `d2faf97`) | `51780f5`, `e1b87a8` (on top of Claude Code commits `a1d262c`–`ed829f4`) | Added free (`$0`) GitHub Codespaces `.devcontainer/devcontainer.json`, `codespace`/`codespace-start` subcommands in `scripts/sandbox/bootstrap-ubuntu.sh`, Oracle Always Free ARM64 + UFW/`ss`/LXD updates in `docs/IPHONE-SANDBOX.md`, and unit tests in `tests/test_sandbox_bootstrap.py`. |
| Community profile PR (this branch) | `claude/community-profile -> main` | **OPEN** | — | Completes GitHub's community profile (`CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`, `SECURITY.md`, `SUPPORT.md`, `ACCESSIBILITY.md`, `.github/ISSUE_TEMPLATE/*`, `.github/PULL_REQUEST_TEMPLATE.md`), updates `README.md`, and adds this `docs/AGENTS-TAY.md` record. |

---

## Infrastructure & Environment Configuration (Outside Git History)

Under the maintainer's free-only sandbox direction (skipping paid Hetzner/DigitalOcean hosting and keeping Render restricted to short paid-per-minute demos), Tay also configured and verified the following supporting infrastructure for `Collaborate.asciidoc`:

1. **GitHub Environments configured on `DeontewattsV1/Collaborate.asciidoc`:**
   - Model review/bridge environments: `agent-openai`, `agent-anthropic`, `agent-gemini`, and `agent-xai` (configured with the maintainer's xAI key for Grok; `agent-anthropic` awaits an Anthropic key if live Claude replies are needed).
   - Sandbox environments: `sandbox-opensandbox` and `sandbox-render` (restricted to `main`; API keys live only in GitHub's encrypted Environment secrets store and are never committed or printed).
2. **Free-tier iPhone Secure ShellFish hosts (Oracle Cloud & Google Cloud):**
   - Set up free-tier Ubuntu hosts on Oracle Cloud Always Free (Ubuntu 24.04 ARM64) and Google Cloud Always Free with the unprivileged `sandbox` user, port 22 SSH access, `tmux`, Go/Python toolchain, and loopback-only `agentterm` for Secure ShellFish on iPhone (documented in [`docs/IPHONE-SANDBOX.md`](IPHONE-SANDBOX.md) and [`docs/RENDER-SANDBOX.md`](RENDER-SANDBOX.md)).
3. **OpenSandbox on the Google Cloud Always Free VM:**
   - Configured the OpenSandbox server bound strictly to loopback (`127.0.0.1` only, keeping port 22 as the only open internet port) and installed the OpenSandbox SDK in `~/.venvs/opensandbox` on the GCP VM and on the operator box.
   - Verified end-to-end sandbox lifecycle (create sandbox, run command, read output, delete cleanly). Because it binds to loopback only, GitHub Actions and external web apps cannot connect directly over the public internet; it is used locally over SSH / ShellFish on the GCP VM.
4. **Render demo sandbox live check:**
   - Verified `scripts/sandbox/render-sandbox.mjs` end-to-end inside a real Render sandbox with two agents (`claude` and `codex`) communicating via the broker and MCP tools, verified all 12 ledger checks, and terminated the sandbox afterward so no compute remains running. (Render Sandboxes have no free tier and bill per active CPU/memory minute while running; see [`docs/RENDER-SANDBOX.md`](RENDER-SANDBOX.md).)

---

## What Tay Has Not Changed

- **License:** `LICENSE` remains unmodified **Apache License 2.0**.
- **Branch protections & live rulesets:** Tay has not modified GitHub branch protection rulesets directly.
- **No self-hosted GitHub Actions runners:** Per [`docs/IPHONE-SANDBOX.md`](IPHONE-SANDBOX.md), no self-hosted runner tokens or self-hosted runner workflows are registered in the repository.
