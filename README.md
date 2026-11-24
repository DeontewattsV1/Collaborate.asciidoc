# Collaborate.asciidoc

Multi-agent dispatch, parallel coordination, shared context, and verification.

## For AI agents: start here

1. **Read [`AGENTS.md`](AGENTS.md).** It holds the rules, a map of the repository and the exact workflow. `CLAUDE.md`, `GEMINI.md` and `.github/copilot-instructions.md` point to it.
2. **Verify with one command:** `scripts/check.sh` (or `scripts/check.sh --quick`). It is exactly what the required `go-verify` check runs.
3. **Branch** from `origin/main` with your registered prefix (`claude/`, `codex/`, `manus/`), open a **draft PR** with `Collaborate-Claim:` lines, and never merge your own PR. The Claude skill `collaborate-pr-workflow` walks through each check.
4. **Learn from history:** read and log errors and lessons in [`.learnings/`](.learnings/), using the `self-improvement` skill.

## Current prototype

**Transition note:** two independent pilots currently coexist. `collaborate` (`cmd/collaborate`) is the Ed25519-authenticated broker scaffold described below. `collab` (`cmd/collab`) is the newer local pilot described by [`docs/SECURITY.md`](docs/SECURITY.md); it has a separate database schema, shared bearer-token client model, and MCP stdio adapter. Their brokers, ledgers, and MCP tools are not interchangeable. Keep their state directories, ports, and host configurations separate. CI tests both; the project has not yet consolidated them into one canonical implementation.

The `collaborate` scaffold authenticates Ed25519-signed agent requests, maintains expiring hierarchical path leases, and stores a signed, SHA-256 hash-chained event ledger in SQLite. It can create per-agent Git worktrees, accept edits through a lease-checked broker write endpoint, independently derive commit receipt metadata from the server-side Git checkout, run operator-configured gates against the merge candidate, and queue changes for independent review before merging them into a session trunk. The separate `collab` pilot does not inherit these properties; see its security notes.

The `collaborate` CLI includes an MCP 2025-06-18 stdio bridge exposing six signed coordination tools for one configured agent: claim, renew, release, say, inbox, and receipt. Its two-agent protocol test uses separate Ed25519 identities against one broker. The separate `collab mcp` bridge follows the pilot's shared-token protocol. The `collaborate adapter` command includes compiled-in OpenAI-compatible, Anthropic Messages, and Google Gemini `generateContent` providers, plus dynamically loaded third-party WASI providers. Each provider makes one completion and records its text through the authenticated broker; this is not an MCP model host or an autonomous tool-calling loop. See [`docs/WASI-PLUGINS.md`](docs/WASI-PLUGINS.md) for the WASI protocol and security boundary.

The existing [`Event schema`](Event%20schema), [`collab-agent`](collab-agent), and [`AsciiDoc`](AsciiDoc) files describe the protocol and target experience; the latter two include aspirational features that are not all implemented by this CLI. A sample deterministic report is available at [`examples/collaborate.AsciiDoc`](examples/collaborate.AsciiDoc).

## Requirements and verification

- Go at the version in `go.mod` (currently 1.26; with `GOTOOLCHAIN=auto` an older Go downloads it automatically), Git, a C compiler, and `CGO_ENABLED=1` for the `collab` pilot.
- Install SQLite development headers (`libsqlite3-dev` on Debian/Ubuntu) to build `cmd/collab`; `cmd/collaborate` uses its Go-module SQLite driver.
- Python 3.10+ for the optional live MCP model-client harness and its unit tests.

```sh
scripts/check.sh           # everything the required go-verify check runs
scripts/check.sh --quick   # same, without the race detector
```

The tests cover simultaneous conflicting claims, lease expiry/takeover, SQLite close/reopen persistence, signed request replay rejection, hierarchical lease overlap, key rotation/revocation, session-scoped review/read access, trusted receipt derivation, gate pass/fail, unleased and symlink-escape write rejection, independent review, queue integration, and append-only export recovery/tamper detection. The suite does not yet perform a real TLS handshake or kill/restart a broker process end-to-end.

## Any AI platform via GitHub

AI platforms that only reach the repository as a GitHub App, an authorized OAuth app, or under your account (Claude, Copilot, Codex, Cursor, Devin, Gemini/Jules, Manus, cubic, CodeRabbit, and others) can coordinate without running the broker. The `collaborate-coordination` workflow runs `collaborate github-check` on every pull request. It treats each open PR as its agent's lease over the paths it changes, plus `Collaborate-Claim: PATH` lines in its description, and fails a later PR that overlaps an earlier one. It identifies agents from `.github/collaborate-agents.json`, checks that approvals come from a different identity, and keeps a sticky comment listing all open agent work as shared context. See [`docs/GITHUB-AGENTS.md`](docs/GITHUB-AGENTS.md).

## Shared agent terminal

`agentterm` is one visual, continuously synchronized terminal that agents clock in to and out of. It gives a single ordered place for commands, outputs, file claims, commits and review findings, so collaborators do not have to reassemble work from scattered PR comments.

- **Clock in with a verified identity.** An agent presents a GitHub token once. The terminal asks GitHub who it is, resolves the agent through `.github/agent-registry.json` (identity plus registered branch prefix), and registers a fresh per-session Ed25519 key. Every later request is signed with that key and carries an increasing nonce.
- **One append-only, verifiable log.** Each entry is hash-chained and signed by the server; agent requests also carry the agent's signature. The browser view re-hashes and checks every entry as it arrives, and `agentterm verify` checks an exported log offline. Even the server operator cannot rewrite an agent's entry without the forgery being detected.
- **Join, leave, rejoin.** Clocking in again with the same instance resumes its claims and retires the old key. The live stream (SSE) replays from any point, so a reconnecting agent or browser misses nothing. Silent sessions expire and release their claims.
- **Guardrails.** Claims are exclusive and hierarchical, so two agents cannot take the same file. A commit is recorded only on the agent's own branch prefix and inside its claims. Only the reviewer can close a finding, and only after it is marked addressed with a fixing commit. Optional server-side execution is allowlisted and read-only (`go test/vet/build`, `gofmt -l`, read-only `git`, `python3 -m unittest`), with no shell and with credentials stripped. The terminal never pushes, merges or approves: work still lands through pull requests and required CI.

```bash
go run ./cmd/agentterm serve --registry .github/agent-registry.json   # http://127.0.0.1:8787
GITHUB_TOKEN=... agentterm clock-in --server http://127.0.0.1:8787 --instance claude-1 --branch claude/my-change
agentterm claim internal/foo && agentterm run -- go test ./internal/foo && agentterm clock-out
```

See [`docs/AGENT-TERMINAL.md`](docs/AGENT-TERMINAL.md) for the protocol, commands and limits.

## Git Locker assistant agents

Every one of the 1,000 seeds in the [Git Locker Index](agents/locker/GIT-LOCKER-INDEX.md) is a runnable assistant agent with its own role, workflow and domain lens. In `assist` mode, a router picks a team of specialists, each writes a brief, a more capable primary model answers using the briefs, and a guardian reviews the answer. Coordinator agents can delegate recursively, within limits on depth and number of model calls. Try `python3 scripts/locker_agents.py assist --task "..."` offline, or run **Actions → locker-agents** with your API keys. See [`docs/LOCKER-AGENTS.md`](docs/LOCKER-AGENTS.md).

## Live sandboxes, multi-model review and container image

- **`opensandbox-sandbox`** runs the test suite for any ref inside a live OpenSandbox sandbox.
- **`agent-model-bridge`** has OpenAI, Anthropic, Gemini and xAI models review a PR and cross-examine each other.
- **Container image:** each push to `main` publishes the agent terminal as a signed image, `ghcr.io/deontewattsv1/collaborate.asciidoc`.

Each provider key lives in its own GitHub Environment, restricted to `main`. Run `bash scripts/setup_github_environments.sh` once, **in a terminal**: it creates every environment and asks only for the keys. Workflows without a key finish green with a "not configured" notice. See [`docs/LIVE-SETUP.md`](docs/LIVE-SETUP.md) and [`docs/AGENT-MODEL-BRIDGE.md`](docs/AGENT-MODEL-BRIDGE.md).

## Observability

Set `OTEL_EXPORTER_OTLP_ENDPOINT` (plus the usual `OTEL_*` variables) before `collaborate serve` to export OpenTelemetry traces and metrics, for example to Grafana Cloud. See [`docs/OBSERVABILITY.md`](docs/OBSERVABILITY.md).

## Local setup

Create an operator-owned gate configuration **before starting the broker**; integration is refused when no trusted gate is configured:

```sh
cat > /tmp/collaborate-gates.json <<'JSON'
{
  "demo-1": [
    {"name": "note-present", "command": ["sh", "-c", "test -f note.txt"], "timeout_seconds": 30}
  ]
}
JSON
```

Start the broker on loopback (the default and recommended local setup):

```sh
./collaborate serve --db /tmp/collaborate-demo.sqlite \
  --gates-file /tmp/collaborate-gates.json \
  --export-dir /tmp/collaborate-backup
```

Create a Git repository with a first commit, then initialize and register agents:

```sh
mkdir -p /tmp/collaborate-demo-repo
cd /tmp/collaborate-demo-repo
git init -b main
git config user.name Demo
git config user.email demo@example.invalid
echo 'hello' > note.txt
git add note.txt && git commit -m initial
/path/to/collaborate init --session demo-1 --repo "$PWD"
/path/to/collaborate register --agent A --model model-a --repo "$PWD"
/path/to/collaborate register --agent B --model model-b --repo "$PWD"
```

Create agent A's isolated worktree and use it for edits. An edit submitted through `write` is rejected unless the agent owns a live lease that covers the path:

```sh
COLLAB_AGENT=A /path/to/collaborate spawn
# The command prints the created worktree path and branch; change into that path.
cd .collab/wt/A
COLLAB_AGENT=A /path/to/collaborate claim --intent 'update note' note.txt
printf 'collaboration works\n' | COLLAB_AGENT=A /path/to/collaborate write note.txt
git add note.txt
COLLAB_AGENT=A /path/to/collaborate commit -m 'Update note'
COLLAB_AGENT=A /path/to/collaborate submit
```

The `commit` command commits staged changes and asks the broker to derive and store a receipt from Git. Submission is separate and is accepted only for that agent's current registered branch tip after a verified receipt exists. Agent B can independently review the queue item; use the queue output to find its ID:

```sh
COLLAB_AGENT=B /path/to/collaborate queue
COLLAB_AGENT=B /path/to/collaborate review --queue-id 1 --verdict approve --reason 'looks good'
COLLAB_AGENT=A /path/to/collaborate integrate --queue-id 1
COLLAB_AGENT=A /path/to/collaborate verify --repo /tmp/collaborate-demo-repo
COLLAB_AGENT=A /path/to/collaborate render --repo /tmp/collaborate-demo-repo > collaborate.AsciiDoc
```

Gate configuration is not read from a submitted branch. Commands are passed as argv arrays without a shell by the broker; the example explicitly runs a shell. Gates are executed against the merge candidate before the trunk branch is advanced. Gate processes receive a reduced environment that omits inherited application secrets. **This is not an OS/container sandbox**: gates can still access the broker host filesystem and network, so configure trusted gates only and run the broker in an appropriately isolated environment.

## CLI reference

- `serve --db FILE [--addr 127.0.0.1:8787] [--lease-ttl 90s] [--gates-file FILE] [--export-dir DIR] [--tls-cert FILE --tls-key FILE --client-ca FILE --admin-token-file FILE]` — start the broker. Remote binds require TLS, verified client certificates (mTLS), and an operator token.
- `init --session ID [--repo DIR] [--url URL] [--trunk BRANCH] [--ca FILE --client-cert FILE --client-key FILE --admin-token-file FILE]` — create the session and local config.
- `register --agent ID --model MODEL [--repo DIR] [--admin-token-file FILE]` — provision an identity and create a local private key. Existing key files are never silently overwritten.
- `spawn [--agent ID] [--branch BRANCH]` — create the agent's worktree under `.collab/wt/` from the session trunk.
- `claim --intent TEXT PATH...`, `renew`, `release PATH...` — acquire/renew/release expiring leases. Ancestor/descendant claims conflict.
- `write PATH < CONTENT` — ask the broker to atomically write inside the registered worktree; requires a covering lease. This is a controlled edit shim, not an OS-level prohibition on other programs writing files.
- `say MESSAGE...` — append a collaboration message.
- `commit -m MESSAGE` / `receipt` — commit staged changes (or record current `HEAD`) and request a server-derived receipt. The commit must be the registered branch tip and all changed paths must be covered by live leases.
- `submit`, `queue`, `review --queue-id N --verdict approve|request_changes`, `integrate --queue-id N` — submit a verified commit, review it independently, rerun configured gates on the merge candidate, and merge through the session trunk. Queue approvals and session data reads are session-scoped.
- `rotate-key`, `revoke-key --version N` — rotate the current Ed25519 key and revoke an older key version. Old private keys are archived locally during rotation; protect or remove those archives as your key-retention policy requires.
- `status`, `verify`, `render` — signed, session-scoped reads. Set `COLLAB_AGENT` or use `--agent`; `verify` validates the full ledger chain.
- `export --out FILE [--repo DIR] [--admin-token-file FILE]` — append/validate a full-ledger JSONL snapshot; requires the operator token when one is configured.
- `github-check --pr N [--repo OWNER/NAME] [--registry FILE] [--comment] [--summary FILE] [--json]` — evaluate one pull request's leases, conflicts, and review independence through the GitHub API (requires `GITHUB_TOKEN`); used by the `collaborate-coordination` workflow.
- `mcp --repo DIR --agent ID` — run the stdio MCP bridge for exactly one agent. See [`examples/mcp-hosts.template.json`](examples/mcp-hosts.template.json). It intentionally exposes no shell or arbitrary filesystem operation.
- `adapter list [--plugins DIR]`, `adapter run --name echo|openai-compatible|anthropic|gemini --prompt TEXT`, or `adapter run --plugin MANIFEST.json --prompt TEXT` — run a compiled-in provider or a sandboxed WASI module. Dynamic modules use a strict JSON manifest and an API key environment variable; see [`docs/WASI-PLUGINS.md`](docs/WASI-PLUGINS.md). Compiled-in provider configuration uses `COLLAB_MODEL_BASE_URL`, `COLLAB_MODEL`, and `COLLAB_MODEL_API_KEY` (or `OPENAI_API_KEY`) for OpenAI-compatible endpoints; `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`, and `ANTHROPIC_API_KEY` for Anthropic; and `GEMINI_BASE_URL`, `GEMINI_MODEL`, and `GEMINI_API_KEY` (or `GOOGLE_API_KEY`) for Gemini.

Set `COLLAB_AGENT` to choose the signing identity; command-level `--agent` overrides it. Project config and private keys are under `.collab/`, which should remain private and untracked.

## CI and the GitHub merge boundary

`.github/workflows/ci.yml` runs on GitHub-hosted runners with only `contents: read`; checkout credentials are not persisted. The stable required-check context is `go-verify`; it runs formatting, uncached tests, race tests, `go vet`, and builds both CLIs with CGO/system SQLite enabled. This is remote CI evidence, not proof that the code is correct or that tests themselves are adequate.

The repository ruleset `main-require-independent-ci` (ID `27378635`) is active on the default branch and currently requires `go-verify`; the checked-in declarative proposal now includes both `go-verify` and `agent-registry`. The registry workflow runs independently but does not become a live merge blocker until the GitHub ruleset is updated. That live settings change is intentionally separate from this code change. The registry includes common AI platforms with no guessed bot IDs; unconfigured entries fail closed until verified identities are entered. See [`docs/AGENT-REGISTRY.md`](docs/AGENT-REGISTRY.md) and [`.github/agent-registry.schema.json`](.github/agent-registry.schema.json).

The live MCP pilot connected OpenAI GPT-6 Luna and Google Gemini 3.8 Flash through separate MCP stdio processes, and tested each under a separate Unix UID. Own-workspace access succeeded; sibling worktree/key and broker-state access were denied. See [`docs/MCP-LIVE-PILOT.md`](docs/MCP-LIVE-PILOT.md) and [`scripts/mcp_live_client.py`](scripts/mcp_live_client.py) for evidence and the harness. This validates a custom model-client harness, not a third-party IDE/assistant host. The tested boundary is Unix file permissions, not a hostile-code sandbox: the model host and its MCP child share the agent UID, and network/process capabilities are not restricted. Do not treat the example host template as isolation by itself.

## Remote deployment, backup, and trust boundaries

For a non-loopback bind, configure `--tls-cert`, `--tls-key`, `--client-ca`, and `--admin-token-file`. The listener requires verified client certificates and refuses remote startup without an operator token. Session creation and agent registration require `X-Collab-Admin-Token`; the CLI reads it from a file or `COLLAB_ADMIN_TOKEN`, and does not save it in project config. Full-ledger export also requires this operator token. Client TLS trust and mTLS identity are configured at `init` with `--ca`, `--client-cert`, and `--client-key`.

Sensitive reads (leases, events, queue, rendered reports, and verification) require an agent-signed request and are limited to that identity's session. A remote broker also needs access to the authoritative server-side Git checkout named during `init`; mTLS secures transport but does not provide that checkout. Protect the database, TLS keys, operator token, Git checkout, gate configuration, backup directory, and local agent keys with operating-system access controls. The broker currently assumes a single active broker process per SQLite ledger/export directory.

`--export-dir` maintains `events.jsonl` by appending events and syncing writes. On restart, the broker verifies the database chain, checks that the backup exactly matches a prefix of the ledger, and appends any missing suffix. A backup mismatch fails startup rather than silently rewriting history. `export --out FILE` provides a full-ledger snapshot that can be appended repeatedly only when the existing file exactly matches the verified server ledger prefix. These are tamper-evident exports, not immutable storage by themselves; copy them to separately controlled/offline storage for stronger backup guarantees.

Agent requests are timestamped and nonce-protected. The database stores event signatures and historical key versions so rotation/revocation does not invalidate verification of older events. Receipts are derived from the server checkout (commit tree, changed paths, and patch hash); gate results record outcome metadata and an output digest, not raw logs. Queue approval must come from an agent in the same session other than the submitter. The integration branch uses a compare-and-swap update-ref; a concurrent branch movement leaves the queue item retryable.

On Linux, the broker-mediated `write` endpoint uses directory file descriptors, no-follow opens, and atomic replacement to reject traversal and symlink escapes. It blocks unleased writes made through that endpoint, but cannot stop an agent with direct filesystem access from bypassing it; use OS/container isolation or a filesystem guard if that enforcement is required. Provider adapters implement a shared Go interface; bundled providers are compiled into the CLI, while third-party providers can be loaded as sandboxed WASI modules. WASI capability limits and remaining trust assumptions are documented in [`docs/WASI-PLUGINS.md`](docs/WASI-PLUGINS.md).
