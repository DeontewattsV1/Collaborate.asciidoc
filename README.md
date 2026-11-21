# Collaborate.asciidoc

Multi-agent dispatch, parallel coordination, shared context, and verification.

## Current prototype

**Transition note:** two independent pilots currently coexist. `collaborate` (`cmd/collaborate`) is the Ed25519-authenticated broker scaffold described below. `collab` (`cmd/collab`) is the newer local pilot described by [`docs/SECURITY.md`](docs/SECURITY.md); it has a separate database schema, shared bearer-token client model, and MCP stdio adapter. Their brokers, ledgers, and MCP tools are not interchangeable. Keep their state directories, ports, and host configurations separate. CI tests both; the project has not yet consolidated them into one canonical implementation.

The `collaborate` scaffold authenticates Ed25519-signed agent requests, maintains expiring hierarchical path leases, and stores a signed, SHA-256 hash-chained event ledger in SQLite. It can create per-agent Git worktrees, accept edits through a lease-checked broker write endpoint, independently derive commit receipt metadata from the server-side Git checkout, run operator-configured gates against the merge candidate, and queue changes for independent review before merging them into a session trunk. The separate `collab` pilot does not inherit these properties; see its security notes.

The `collaborate` CLI includes an MCP 2025-06-18 stdio bridge exposing six signed coordination tools for one configured agent: claim, renew, release, say, inbox, and receipt. Its two-agent protocol test uses separate Ed25519 identities against one broker. The separate `collab mcp` bridge follows the pilot's shared-token protocol. Neither bridge has been connected to an external model host in a live run. The optional OpenAI-compatible adapter makes one completion; it is not an MCP model host or an autonomous tool-calling loop.

The existing [`Event schema`](Event%20schema), [`collab-agent`](collab-agent), and [`AsciiDoc`](AsciiDoc) files describe the protocol and target experience; the latter two include aspirational features that are not all implemented by this CLI. A sample deterministic report is available at [`examples/collaborate.AsciiDoc`](examples/collaborate.AsciiDoc).

## Requirements and verification

- Go 1.22+, Git, a C compiler, and `CGO_ENABLED=1` for the `collab` pilot.
- Install SQLite development headers (`libsqlite3-dev` on Debian/Ubuntu) to build `cmd/collab`; `cmd/collaborate` uses its Go-module SQLite driver.
- Python 3.10+ for the optional live MCP model-client harness and its unit tests.

```sh
go mod tidy
go test ./...
go test -race ./...
go vet ./...
python3 -m unittest discover -s scripts -p 'test_*.py'
go build -o ./collaborate ./cmd/collaborate
```

The tests cover simultaneous conflicting claims, lease expiry/takeover, SQLite close/reopen persistence, signed request replay rejection, hierarchical lease overlap, key rotation/revocation, session-scoped review/read access, trusted receipt derivation, gate pass/fail, unleased and symlink-escape write rejection, independent review, queue integration, and append-only export recovery/tamper detection. The suite does not yet perform a real TLS handshake or kill/restart a broker process end-to-end.

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

- `serve --db FILE [--addr 127.0.0.1:8787] [--lease-ttl 90s] [--gates-file FILE] [--export-dir DIR] [--tls-cert FILE --tls-key FILE --client-ca FILE --admin-token-file FILE]` — start the broker. Remote binds require TLS, verified client certificates (mTLS), and an operator token; only the unauthenticated liveness probe `GET /health` is reachable without a client certificate.
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
- `mcp --repo DIR --agent ID` — run the stdio MCP bridge for exactly one agent. See [`examples/mcp-hosts.template.json`](examples/mcp-hosts.template.json). It intentionally exposes no shell or arbitrary filesystem operation.
- `adapter list`, `adapter run --name echo|openai-compatible --prompt TEXT` — run the deterministic fixture or a single OpenAI-compatible completion. Provider configuration uses `COLLAB_MODEL_BASE_URL`, `COLLAB_MODEL`, and `COLLAB_MODEL_API_KEY` (or `OPENAI_API_KEY`).

Set `COLLAB_AGENT` to choose the signing identity; command-level `--agent` overrides it. Project config and private keys are under `.collab/`, which should remain private and untracked.

## CI and the GitHub merge boundary

`.github/workflows/ci.yml` runs on GitHub-hosted runners with only `contents: read`; checkout credentials are not persisted. The stable required-check context is `go-verify`; it runs formatting, uncached tests, race tests, `go vet`, and builds both CLIs with CGO/system SQLite enabled. This is remote CI evidence, not proof that the code is correct or that tests themselves are adequate.

The repository ruleset `main-require-independent-ci` (ID `27378635`) is active on the default branch. It requires pull requests and the `go-verify` status check from the GitHub Actions app, requires the branch to be current with the base, and grants no bypass actors. The submitted payload sets required approving reviews to zero; GitHub’s stored rule also enables an extra approval for unattributed changes. The checked-in [`main-required-ci.json`](.github/rulesets/main-required-ci.json) is the declarative payload; changes to GitHub’s live setting are separate. See [`docs/GITHUB-CI.md`](docs/GITHUB-CI.md) for rationale and official references.

The live MCP pilot connected OpenAI GPT-6 Luna and Google Gemini 3.8 Flash through separate MCP stdio processes, and tested each under a separate Unix UID. Own-workspace access succeeded; sibling worktree/key and broker-state access were denied. See [`docs/MCP-LIVE-PILOT.md`](docs/MCP-LIVE-PILOT.md) and [`scripts/mcp_live_client.py`](scripts/mcp_live_client.py) for evidence and the harness. This validates a custom model-client harness, not a third-party IDE/assistant host. The tested boundary is Unix file permissions, not a hostile-code sandbox: the model host and its MCP child share the agent UID, and network/process capabilities are not restricted. Do not treat the example host template as isolation by itself.

## Remote deployment, backup, and trust boundaries

For a non-loopback bind, configure `--tls-cert`, `--tls-key`, `--client-ca`, and `--admin-token-file`. The listener requires verified client certificates for every route except the liveness probe (`GET`/`HEAD /health`, exact path, no query), which returns only `{"status":"ok"}` so external uptime monitors such as Grafana Synthetic Monitoring can reach it without a client certificate. Any certificate a client does present is still verified against `--client-ca` during the handshake. The listener refuses remote startup without an operator token. Session creation and agent registration require `X-Collab-Admin-Token`; the CLI reads it from a file or `COLLAB_ADMIN_TOKEN`, and does not save it in project config. Full-ledger export also requires this operator token. Client TLS trust and mTLS identity are configured at `init` with `--ca`, `--client-cert`, and `--client-key`.

Sensitive reads (leases, events, queue, rendered reports, and verification) require an agent-signed request and are limited to that identity's session. A remote broker also needs access to the authoritative server-side Git checkout named during `init`; mTLS secures transport but does not provide that checkout. Protect the database, TLS keys, operator token, Git checkout, gate configuration, backup directory, and local agent keys with operating-system access controls. The broker currently assumes a single active broker process per SQLite ledger/export directory.

`--export-dir` maintains `events.jsonl` by appending events and syncing writes. On restart, the broker verifies the database chain, checks that the backup exactly matches a prefix of the ledger, and appends any missing suffix. A backup mismatch fails startup rather than silently rewriting history. `export --out FILE` provides a full-ledger snapshot that can be appended repeatedly only when the existing file exactly matches the verified server ledger prefix. These are tamper-evident exports, not immutable storage by themselves; copy them to separately controlled/offline storage for stronger backup guarantees.

Agent requests are timestamped and nonce-protected. The database stores event signatures and historical key versions so rotation/revocation does not invalidate verification of older events. Receipts are derived from the server checkout (commit tree, changed paths, and patch hash); gate results record outcome metadata and an output digest, not raw logs. Queue approval must come from an agent in the same session other than the submitter. The integration branch uses a compare-and-swap update-ref; a concurrent branch movement leaves the queue item retryable.

On Linux, the broker-mediated `write` endpoint uses directory file descriptors, no-follow opens, and atomic replacement to reject traversal and symlink escapes. It blocks unleased writes made through that endpoint, but cannot stop an agent with direct filesystem access from bypassing it; use OS/container isolation or a filesystem guard if that enforcement is required. The adapter API is extensible at compile time; there are no dynamic Go-plugin loading hooks yet.
