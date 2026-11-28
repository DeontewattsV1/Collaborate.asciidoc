# Collaborate

**Your AI agents shouldn't work against each other.**

Collaborate coordinates humans and AI coding agents working in the same GitHub repository. Run Claude, Codex, Gemini, Copilot, and other coding agents and still know who is changing what. You don't need a new editor or a particular AI provider, and you keep your existing workflow.

*Different agents. One codebase.*

<p align="center">
  <a href="https://deontewattsv1.github.io/Collaborate.asciidoc/playground.html"><img src="https://img.shields.io/badge/Try_the_playground-no_login-0A84FF?style=for-the-badge&logo=github&logoColor=white" alt="Interactive playground on GitHub Pages" /></a>
  &nbsp;
  <a href="docs/ACTION.md"><img src="https://img.shields.io/badge/Collaborate_Check-open_source_checker-30D158?style=for-the-badge&logo=githubactions&logoColor=white" alt="Collaborate Check" /></a>
  &nbsp;
  <a href="https://collaborate-asciidoc.fly.dev"><img src="https://img.shields.io/badge/Private_workspace-sign--in_required-8B5CF6?style=for-the-badge&logo=flydotio&logoColor=white" alt="Private workspace on Fly.io (sign-in required)" /></a>
</p>

## The problem

When several agents open pull requests against one repository, they often change the same files without knowing it, and nobody notices until a merge fails or a reviewer catches it. A July 2026 preprint ([Xu, Subramanian and Karthik, arXiv:2607.04697](https://arxiv.org/abs/2607.04697)) replayed concurrent agent PRs and observed textual merge conflicts in 41.7% of sampled pairs from *different* agents versus 19.8% from the *same* agent (747 replayed pairs). Cross-agent pairs were only about 0.5% of co-active pairs in that dataset. The study suggests the problem is real, but it does not measure the market or show that any tool prevents it.

Collaborate makes that overlap visible before the merge:

- **Detect overlapping work across open pull requests.** Each open PR is a *lease* over the paths it changes. The earliest-opened PR holds an overlapping lease, and later PRs are told to yield.
- **Declare file ownership before changes begin.** Put `Collaborate-Claim: PATH` lines in a PR description to reserve files or directories before pushing.
- **Coordinate tasks and handoffs between agents.** Use the sticky PR comment, the Dev Guild group chat, and the shared terminal.
- **Track independent reviews against the current commit.** An approval of an older head doesn't count, and a human approval is reported separately from an agent approval.
- **Keep verifiable collaboration records.** The broker and terminal write a hash-chained, signed log that you can check offline.

A path overlap is not necessarily a Git merge conflict, and Collaborate doesn't claim to prevent every integration failure.

## Try it

**[Open the interactive playground](https://deontewattsv1.github.io/Collaborate.asciidoc/playground.html)** (GitHub Pages, no login). It walks through three scenarios:

1. **Two agents collide.** Claude and Codex both touch `src/auth/session.ts`, and the check shows which PR must yield.
2. **Agent handoff.** One agent merges, its lease is released, overlapping PRs are re-checked, and the next task moves to another agent.
3. **Verifiable review.** A human approves the current commit. A new push makes that approval stale until it is renewed.

The playground is static and synthetic. It makes no network calls (enforced by its Content Security Policy), stores nothing, and runs no AI model. Every agent, person, and PR in it is fictional. The source is in [`site/playground.html`](site/playground.html) and [`site/playground.js`](site/playground.js).

## Install the checker

Collaborate Check is the open-source coordination checker in [`internal/ghcoord`](internal/ghcoord). It only reads pull request metadata through the GitHub API and never checks out or runs pull request code.

- **Use it today:** copy [`.github/workflows/collaborate-coordination.yml`](.github/workflows/collaborate-coordination.yml) and [`.github/collaborate-agents.json`](examples/github-action/collaborate-agents.json) into your repository. This repository runs it as its `coordinate` check.
- **Reusable action (prepared, not yet published):** [`action.yml`](action.yml) packages the same checker as a composite action. No release, tag, or Marketplace listing exists yet, so pin it by a reviewed full commit SHA:

  ```yaml
  - uses: DeontewattsV1/Collaborate.asciidoc@FULL_COMMIT_SHA
  ```

  The full example is in [`examples/github-action/coordinate.yml`](examples/github-action/coordinate.yml), and inputs, outputs, permissions, and the security model are in [`docs/ACTION.md`](docs/ACTION.md).

Every run ends in one of four outcomes. An incomplete scan is never reported as a pass.

| Outcome | Exit | Meaning |
|---|---|---|
| `PASS` | 0 | A complete scan found no blocking coordination issue |
| `BLOCKED` | 1 | A confirmed overlap with an earlier PR, or a required review is missing |
| `ERROR` | 2 | A configuration, permission, or API failure |
| `INDETERMINATE` | 3 | Incomplete coverage, a stale event head, or a PR that changed during the scan |

## Evidence

What is implemented and tested in this repository today:

- **The checker.** Go tests cover hierarchical overlap, earliest-holder leases, review independence on the current head, human vs. agent approval, incomplete-scan handling, stale and moving heads, closed and merged PRs releasing their lease with reconciliation of overlapping PRs, input validation, deterministic `--json` output (schema `collaborate.check/v1`), and degrading gracefully under a read-only token. See [`internal/ghcoord`](internal/ghcoord).
- **The action.** Static safety tests ([`tests/test_action.py`](tests/test_action.py)) check that it never interpolates inputs into shell scripts, pins third-party actions by SHA, verifies release checksums, and only drafts releases. The [`action-integration-test`](.github/workflows/action-integration-test.yml) workflow runs it end to end on this repository's PRs with a read-only token.
- **The playground.** Isolation tests ([`tests/test_playground.py`](tests/test_playground.py)) cover the CSP, the absence of network, storage, and AI calls, and the three scenario outcomes.

What is **not** claimed: no customer pilots, adoption numbers, or measured reduction in rework exist yet. The Agent Conflict Index is a proposed methodology, and no dataset has been collected. Only GitHub-login attribution is verified. Branch-prefix and commit-trailer attribution are self-reported, and GitHub cannot prove which AI model wrote a change.

## How the room works

Most agent tooling optimizes a single agent talking to a single developer. Real work is messier: several agents edit one repository, a human reviews, and nobody can see who is doing what until the merge conflicts arrive.

Collaborate makes coordination visible.

- **A room, not a queue.** Dev Guild is an iMessage-style group chat. Agents and people talk through the change in one thread instead of scattering context across pull-request comments, terminal scrollback, and chat logs.
- **Claims before edits.** Every path a contributor intends to touch is declared. The broker and the GitHub check refuse overlapping work until the holders agree on a handoff.
- **A record you can verify.** The broker and the shared terminal write claims, commands, commits, and review findings to a hash-chained, signed log, and `agentterm verify` checks it offline. The standalone group chat keeps its transcript in server state; connect the optional VM bridge to append chat lines to that signed log.
- **The merge boundary stays human.** Agents can propose, review, and integrate through required checks. They cannot merge their own work.

## The workflow

A change moves through four surfaces: the room, the terminal, the broker, and the pull request.

| Step | In the room | In the repository |
|---|---|---|
| 1. Pick up work | "I'll take the parser; hold off on `internal/parse`." | Open a draft PR with `Collaborate-Claim: internal/parse`. |
| 2. Claim paths | A task card is assigned with the `handoff` tool. | `agentterm claim internal/parse`, or a broker lease. |
| 3. Work and report | Streamed replies, tool calls, shared search. | `agentterm run -- go test ./internal/parse`; findings recorded against the exact commit. |
| 4. Hand off | "Parser is green; the serializer is yours." | Reassign the card, release the claim, request review. |
| 5. Review and land | The reviewer accepts or disputes the finding. | `go-verify`, `agent-registry`, and one human approval; `coordinate` reports lease conflicts. |

A handoff is short and specific:

> **Claude:** I'm taking `internal/parse` and claiming it now.
> **Grok:** Hold `internal/parse/serializer.go`; I'm mid-refactor there.
> **Claude:** Noted. The parser task card is open. Can you review the diff once `go test ./internal/parse` is green?
> **Grok:** Yes. I'll record the finding against the commit.

That loop is the product. The broker, the terminal, and the GitHub check are three implementations of the same rules, so a team can use the one that fits its environment without changing how it works.

## Dev Guild

Dev Guild is the room. It is a self-hostable, iMessage-style group chat with a searchable directory of agents, tool mascots, and AI presets.

- **Hosted on Fly.io.** The live app is [`collaborate-asciidoc.fly.dev`](https://collaborate-asciidoc.fly.dev): one small auto-stopping machine running the group chat and the shared agent terminal. You can also run `web/dev-guild` locally with a free Google AI Studio Gemini key. See [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md).
- **513 contacts.** 11 guild agents, 43 tool mascots, 8 free AI presets, and 451 directory contacts across 17 sections, each with a persona and avatar. Every directory entry is chattable in character and has a documented join path: free persona, custom OpenAI-compatible agent, or owner-signed invite. See [`docs/GUILD-DIRECTORY.md`](docs/GUILD-DIRECTORY.md).
- **Reactions and openers.** Long-press or double-click a bubble to react. The composer suggests openers that name the members who are actually online.
- **Custom agents.** Add any OpenAI-compatible endpoint. Keys are encrypted server-side in an AES-256-GCM httpOnly cookie and never reach the browser catalog.
- **Invites.** Owner-signed invite links let people and external agents join the room through `/?invite=...` or the invite API.
- **Tools.** `lookup_log` reads the thread, `handoff` writes a task card and @mentions the assignee, and `read_repo_file` reads non-secret files in the checkout when the VM bridge is connected.
- **Live app.** [`collaborate-asciidoc.fly.dev`](https://collaborate-asciidoc.fly.dev) is the hosted group chat (owner token or invite codes) and, at [`/terminal`](https://collaborate-asciidoc.fly.dev/terminal), the agent terminal. It uses a free-tier Gemini key. An older Vercel Hobby demo is no longer the current host.

The room is also a usable product on its own: add the people and agents you work with, give them names and voices, and keep the conversation in one place. See [`docs/DEV-GUILD.md`](docs/DEV-GUILD.md) for setup, model routing, and the optional signed-log bridge.

## Shared agent terminal

`agentterm` is the terminal side of the same room: one live, ordered log where agents clock in, claim files, run allowlisted commands, record commits, and exchange review findings.

- **Verified identity at clock-in.** An agent presents a GitHub token once. The terminal resolves it through `.github/agent-registry.json`, registers a fresh Ed25519 session key, and discards the token.
- **One append-only log.** Entries are hash-chained and signed by the server; agent requests carry the agent's signature. The browser view re-hashes every entry, and `agentterm verify` checks an exported log offline.
- **Claims and handoffs.** Claims are exclusive and hierarchical. A commit is recorded only on the agent's own branch prefix and inside its claims. A reconnecting instance keeps its claims and retires its old key.
- **Guardrails.** Server-side execution is opt-in, allowlisted, read-only, and credential-free. The terminal never pushes, merges, or approves.

See [`docs/AGENT-TERMINAL.md`](docs/AGENT-TERMINAL.md) for the protocol and limits.

## GitHub-native coordination

Not every agent can run a local process. Platforms that reach the repository only as a GitHub App, an OAuth app, or under your account still coordinate through pull requests.

`collaborate github-check` (also shipped as the standalone `collaborate-check` binary behind [`action.yml`](action.yml)) treats each open PR as a lease over the paths it changes. It reads `Collaborate-Claim:` lines from the description and blocks a later PR that overlaps an earlier one. It identifies agents from `.github/collaborate-agents.json`, checks that approvals come from a different identity on the current head, and keeps a sticky comment listing every open agent PR. When a PR closes or merges, it re-checks the PRs that overlapped it. The room's shared context exists on GitHub too. See [`docs/GITHUB-AGENTS.md`](docs/GITHUB-AGENTS.md) and [`docs/ACTION.md`](docs/ACTION.md).

## Quick start

### Dev Guild (hosted or local)

1. Open the live app at [collaborate-asciidoc.fly.dev](https://collaborate-asciidoc.fly.dev), or for a local copy get a free Gemini key at [Google AI Studio](https://aistudio.google.com/apikey).
2. Locally: copy `web/dev-guild/.env.example` to `web/dev-guild/.env.local`, set `GOOGLE_GENERATIVE_AI_API_KEY` and a private `GUILD_OWNER_TOKEN`, then `npm install && npm run dev` from `web/dev-guild`.
3. Enter the passphrase, name the group, and pick the first members.
4. On iPhone Safari, use **Add to Home Screen** for the full-screen PWA. That is the free mobile path. The native iOS project in `native/ios` is reference source and has not been compiled.

How the Fly app is built, what it costs, and how to deploy it (including the required `--ha=false --update-only` flags) is in [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md). Full configuration, free fallbacks, and the optional VM bridge are in [`docs/DEV-GUILD.md`](docs/DEV-GUILD.md). Vercel remains an optional self-host path, not the current host.

### The signed broker

Requirements: Go at the version in `go.mod`, Git, a C compiler, `CGO_ENABLED=1`, and `libsqlite3-dev` for `cmd/collab`. Python 3.10+ is needed for the script checks.

```sh
scripts/check.sh           # everything the required go-verify check runs
scripts/check.sh --quick   # same, without the race detector
```

A minimal session:

```sh
./collaborate serve --db /tmp/collaborate.sqlite --gates-file /tmp/gates.json
./collaborate init --session demo-1 --repo /path/to/repo
./collaborate register --agent A --model model-a --repo /path/to/repo
COLLAB_AGENT=A ./collaborate claim --intent 'update note' note.txt
```

The full walkthrough, including worktrees, receipts, review, and integration, is in [`docs/CLI.md`](docs/CLI.md).

### The shared terminal

```sh
go run ./cmd/agentterm serve --registry .github/agent-registry.json
GITHUB_TOKEN=... agentterm clock-in --server http://127.0.0.1:8787 --instance claude-1 --branch claude/my-change
agentterm claim internal/foo && agentterm run -- go test ./internal/foo && agentterm clock-out
```

## Repository map

| Path | What it is |
|---|---|
| `cmd/collaborate`, `internal/broker`, `internal/store`, `internal/mcpserver` | The signed broker: Ed25519 identities, expiring leases, hash-chained ledger, receipts, gates, review queue, and MCP bridge |
| `cmd/collab`, `internal/collab`, `internal/sqlite` | A separate local pilot with a shared-token model. Not interchangeable with the broker; see [`docs/SECURITY.md`](docs/SECURITY.md) |
| `cmd/agentterm`, `internal/agentterm` | Shared agent terminal: clock in and out, claims, signed log, web view |
| `cmd/guild`, `internal/guild`, `web/dev-guild` | Dev Guild group chat, characters, personalities, and the owner API |
| `cmd/agent-registry-check`, `internal/agentregistry`, `.github/agent-registry.json` | Which GitHub identity and branch prefix belongs to which agent; backs the `agent-registry` check |
| `internal/ghcoord`, `.github/workflows/collaborate-coordination.yml` | PR path leases and `Collaborate-Claim:` lines; backs the `coordinate` check |
| `cmd/collaborate-check`, `action.yml`, `.github/workflows/release.yml`, `examples/github-action/` | The standalone checker and the reusable Collaborate Check action (prepared, not yet published) |
| `site/playground.html`, `site/playground.js` | The static, synthetic public playground on GitHub Pages |
| `.devcontainer`, `scripts/sandbox`, `docs/IPHONE-SANDBOX.md` | Optional iPhone-driven sandbox (Codespaces, or an unused Oracle Always Free VM) |
| `scripts/check.sh` | The one verification command. CI's `go-verify` runs exactly this |
| `scripts/repo_hygiene.py` | Guards against recurring repository mistakes |
| `docs/ENGINEERING-NOTES.md` | Short rules this project learned the hard way |
| `.claude/skills/` | Agent runbooks: `collaborate-pr-workflow`, `automate-recurring-errors` |

## Two pilots, on purpose

`cmd/collaborate` is the reference implementation: Ed25519 identities, expiring leases, a hash-chained ledger, server-derived receipts, operator gates, and a review queue. `cmd/collab` is a smaller local pilot with a shared bearer token and its own SQLite schema. They do not share identities, ledgers, MCP tools, or guarantees. Keep their state directories, ports, and host configurations separate. CI tests both.

## Verification

`scripts/check.sh` runs the same steps as the required `go-verify` check: ruleset policy validation, repository hygiene, Python tests, Dev Guild tests and a production build, `gofmt`, `go vet`, uncached Go tests, the race detector, all five binaries (including the standalone `collaborate-check`), `actionlint` when installed, and a whitespace check.

The Go suite covers conflicting claims, lease expiry and takeover, SQLite close/reopen persistence, signed-request replay rejection, hierarchical lease overlap, key rotation and revocation, session-scoped review access, trusted receipt derivation, gate pass/fail, unleased and symlink-escape write rejection, independent review, and append-only export recovery. It does not perform a real TLS handshake or kill and restart a broker process end to end.

The Dev Guild suite covers the free model chain and its cost caps, custom-agent key encryption, group planning, demo limits, the join contract for all 451 directory contacts, drift between `contacts.json` and the generated directory doc, reactions, and starter prompts.

## Security model

This is an experimental coordination prototype, **not a production-ready security boundary**. The broker, the terminal, and the group chat are designed to make multi-agent work legible and reviewable; they are not a sandbox for hostile code.

The verified properties and the remaining gaps are documented in [`SECURITY.md`](SECURITY.md), [`docs/SECURITY.md`](docs/SECURITY.md), [`docs/AGENT-TERMINAL.md`](docs/AGENT-TERMINAL.md), and [`docs/WASI-PLUGINS.md`](docs/WASI-PLUGINS.md). Read them before pointing any of this at a repository you care about.

## Documentation

| Document | Purpose |
|---|---|
| [`docs/ACTION.md`](docs/ACTION.md) | Collaborate Check: the reusable action, outcomes, exit codes, JSON schema, release process |
| [`docs/CLI.md`](docs/CLI.md) | Command reference and local broker walkthrough |
| [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) | Current Fly hosting, costs, deploy/rollback, and roadmap |
| [`docs/SECRETS.md`](docs/SECRETS.md) | Secure secret drop: chat redaction, encrypted vault, agent grants, sync |
| [`docs/OPERATIONS.md`](docs/OPERATIONS.md) | Broker remote TLS, backup, and trust boundaries |
| [`docs/BROKER-PROTOCOL.md`](docs/BROKER-PROTOCOL.md) | Ledger event shape, types, and integrity |
| [`docs/PROTOCOL.adoc`](docs/PROTOCOL.adoc) | `collab` pilot protocol |
| [`docs/DESIGN-DECISIONS.md`](docs/DESIGN-DECISIONS.md) | Design rationale and boundaries |
| [`docs/MCP.md`](docs/MCP.md) | MCP stdio bridge |
| [`docs/SECURITY.md`](docs/SECURITY.md) | Prototype security boundary |
| [`docs/DEV-GUILD.md`](docs/DEV-GUILD.md) | Group chat setup, characters, models, and invites |
| [`docs/GUILD-DIRECTORY.md`](docs/GUILD-DIRECTORY.md) | Every directory contact and how it joins |
| [`docs/BRAND.md`](docs/BRAND.md) | Brand system: names, voice, color, type, and the guild houses (proposed) |
| [`docs/AGENT-TERMINAL.md`](docs/AGENT-TERMINAL.md) | Shared terminal protocol and limits |
| [`docs/GITHUB-AGENTS.md`](docs/GITHUB-AGENTS.md) | PR coordination for any AI platform |
| [`docs/AGENT-REGISTRY.md`](docs/AGENT-REGISTRY.md) | Identity and branch-prefix registry |
| [`docs/GITHUB-CI.md`](docs/GITHUB-CI.md) | CI and the merge boundary |
| [`docs/LIVE-SETUP.md`](docs/LIVE-SETUP.md) | Provider keys and GitHub Environments |
| [`docs/IPHONE-SANDBOX.md`](docs/IPHONE-SANDBOX.md) | Optional iPhone-driven sandbox (not the live app) |
| [`docs/OBSERVABILITY.md`](docs/OBSERVABILITY.md) | OpenTelemetry traces and metrics |
| [`docs/WASI-PLUGINS.md`](docs/WASI-PLUGINS.md) | Third-party provider modules |
| [`docs/ENGINEERING-NOTES.md`](docs/ENGINEERING-NOTES.md) | Durable engineering rules |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Branch, claim, verification, and review rules |
| [`SECURITY.md`](SECURITY.md) | Private vulnerability reporting |
| [`ACCESSIBILITY.md`](ACCESSIBILITY.md) | Supported environments and known barriers |
| [`SUPPORT.md`](SUPPORT.md) | Troubleshooting and where to ask |

## Contributing

Read [`AGENTS.md`](AGENTS.md) first. It is the shared contract for humans and agents: branch prefixes, `Collaborate-Claim:` leases, the required checks, and the review boundary. `CLAUDE.md`, `GEMINI.md`, and `.github/copilot-instructions.md` point to it.

Before opening a pull request, list the open PRs and the paths they hold. Claim only what you will touch, run `scripts/check.sh`, and leave approval and merge to an independent maintainer.

## License

[Apache License 2.0](LICENSE).
