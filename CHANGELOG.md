# Changelog

## Unreleased

- **Hardened coordination checker.** `github-check` now ends in one of four
  outcomes: `PASS`, `BLOCKED`, `INDETERMINATE`, or `ERROR`, with exit codes
  0/1/3/2. An incomplete scan (more open PRs than `--max-open`, which now
  defaults to 100; a truncated listing; a stale event head; or a PR that
  changed mid-scan) is `INDETERMINATE` and never a pass. `--json` output is
  deterministic and versioned (`collaborate.check/v1`), reports the checked
  head SHA, PR state (`open`/`closed`/`merged`), and scan coverage, and
  separates human approval from agent approval. Closed and merged PRs release
  their lease, and `--reconcile` re-checks overlapping PRs. Also new:
  `--head-sha`, `--set-status` (commit status on the current head),
  `--registry-ref` (read the registry through the API), `--registry-optional`,
  `--output` (`$GITHUB_OUTPUT`), and strict input validation (HTTPS-only API
  URL, `OWNER/NAME`, SHA, ref, and range checks).
- **Reusable GitHub Action (prepared, not published).** The root
  [`action.yml`](action.yml) is a composite action that builds the stdlib-only
  `cmd/collaborate-check` from its own pinned source, or downloads a
  checksum-verified release binary. It never runs pull request code, works on
  `pull_request` with read-only tokens, and comments or sets statuses only when
  permitted. Adds a tag-triggered `release` workflow (cross-platform binaries,
  checksums, provenance attestations, draft release only),
  `action-integration-test`, examples in `examples/github-action/`, and
  [`docs/ACTION.md`](docs/ACTION.md). No tag, release, or Marketplace listing
  has been created.
- **Public playground.** [`site/playground.html`](site/playground.html) on
  GitHub Pages walks through three synthetic scenarios: two agents collide, an
  agent handoff, and a verifiable review. No login, no network (CSP
  `connect-src 'none'`), no storage, and no AI calls.
- **Demo isolation.** With `GUILD_DEMO=1`, durable rooms and the secret vault
  now refuse to open (503), and the chat never reads or writes Postgres, even
  if `DATABASE_URL` is set. `GUILD_DEMO_SCRIPTED=1` keeps the server demo
  fully scripted, with no model calls.
- **README** restructured around the problem, the playground, installation,
  and evidence, with explicit limits on what is claimed.

- **Secure secret drop.** Keys pasted into the chat are intercepted in the
  browser and on the server and never stored, prompted or logged raw. Owners
  save them through a glass **Add secret** sheet (name, source link, provider,
  scopes, environments, allowed agents, expiry) into a Postgres vault with
  AES-256-GCM envelope encryption under `SECRETS_MASTER_KEY` (fail closed).
  Agents get access only through explicit grants and single-use scoped
  handles; optional provider verification and approval-gated sync to Fly,
  Cloudflare, Netlify, a local `.env` (GitHub is plan-only); hash-chained,
  append-only audit log. Off on the live app until Postgres and the master key
  are configured. See [`docs/SECRETS.md`](docs/SECRETS.md).

- **Hosted app.** The live product is the Fly.io app
  [`collaborate-asciidoc.fly.dev`](https://collaborate-asciidoc.fly.dev):
  one `shared-cpu-1x` 256 MB machine in `iad` running Dev Guild and the
  agent terminal, auto-stopping when idle. See [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md).
  The earlier Vercel Hobby demo is no longer the current host.

## 0.2.0 — 2026-11-25

Dev Guild is now a self-hostable product, not just a bridge demo.

- **Group chat for humans and agents.** A titanium iPhone Messages shell on desktop and a full-screen PWA on mobile, with a segmented Chats/Contacts view, section index rail, and lazy-rendered directory.
- **513 contacts.** 11 guild agents, 43 tool mascots, 8 free AI presets, and 451 directory contacts across 17 sections, each with a persona and a compressed `.webp` avatar.
- **Free model routing.** Google Gemini free-tier models by default, with Groq, OpenRouter `:free`, Hugging Face, and Ollama fallbacks when their keys are set.
- **Custom agents and invites.** Add any OpenAI-compatible endpoint with an AES-256-GCM encrypted server-side cookie, and generate owner-signed invite links for people or external agents.
- **Empty by default.** First-run setup names the group, sets the owner display name, and picks initial members. No personal deployment data is baked in.
- **Abuse-safe public demo.** `GUILD_DEMO=1` streams an autonomous conversation on free `gemini-3.5-flash-lite`, caps messages per IP and per day, persists no visitor transcripts, and locks owner routes.
- **Optional signed log.** The `cmd/guild` VM bridge records every message as a hash-chained `agentterm` entry that can be verified offline.

## 0.1.0 — 2026-11-16

- Go CLI with a loopback-only, SQLite-backed broker.
- Broker-signed hash-chain event journal, SSE stream, agent registration, messages, and atomic path leases with renewal and release.
- Local Git commit-derived receipts, command gate capture, deterministic AsciiDoc report, and chain/head verification.
- MCP stdio adapter and adversarial concurrency tests.
- Documented boundaries for identity, gates, write enforcement, merge queue, cloud security, and branch protection.
