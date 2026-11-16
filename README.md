# Collaborate.asciidoc

Multi-agent dispatch, parallel coordination, shared context, and verification.

## Current prototype

This repository includes a local-first Go coordination broker. It authenticates Ed25519-signed agent requests, maintains expiring hierarchical path leases, and stores a signed, SHA-256 hash-chained event ledger in SQLite. It can create per-agent Git worktrees, accept edits through a lease-checked broker write endpoint, independently derive commit receipt metadata from the server-side Git checkout, run operator-configured gates against the merge candidate, and queue changes for independent review before merging them into a session trunk.

The CLI can invoke an OpenAI-compatible chat provider through a separate adapter package and record the response through the signed broker protocol. It does not launch model CLIs, provide model tool-calling/file-edit plans, or include provider-specific Anthropic/local-model plugins. The deterministic `echo` adapter remains available for offline protocol demos.

The existing [`Event schema`](Event%20schema), [`collab-agent`](collab-agent), and [`AsciiDoc`](AsciiDoc) files describe the protocol and target experience; the latter two include aspirational features that are not all implemented by this CLI. A sample deterministic report is available at [`examples/collaborate.AsciiDoc`](examples/collaborate.AsciiDoc).

## Requirements and verification

- Go 1.22+ and Git.
- The SQLite driver is a Go module dependency downloaded at build time.

```sh
go mod tidy
go test ./...
go test -race ./...
go vet ./...
go build -o ./collaborate ./cmd/collaborate
```

The tests cover simultaneous conflicting claims, lease expiry/takeover, SQLite close/reopen persistence, signed request replay rejection, hierarchical lease overlap, key rotation/revocation, session-scoped review/read access, trusted receipt derivation, gate pass/fail, unleased and symlink-escape write rejection, independent review, queue integration, and append-only export recovery/tamper detection. The suite does not yet perform a real TLS handshake or kill/restart a broker process end-to-end.

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
- `adapter list`, `adapter run --name echo|openai-compatible --prompt TEXT` — run the deterministic fixture or an OpenAI-compatible chat plugin. Provider configuration uses `COLLAB_MODEL_BASE_URL` (default `https://api.openai.com/v1`), `COLLAB_MODEL`, and `COLLAB_MODEL_API_KEY` (or `OPENAI_API_KEY`). The provider response is recorded via the signed broker `say` capability; model-driven file tools are not implemented.

Set `COLLAB_AGENT` to choose the signing identity; command-level `--agent` overrides it. Project config and private keys are under `.collab/`, which should remain private and untracked.

## Remote deployment, backup, and trust boundaries

For a non-loopback bind, configure `--tls-cert`, `--tls-key`, `--client-ca`, and `--admin-token-file`. The listener requires verified client certificates and refuses remote startup without an operator token. Session creation and agent registration require `X-Collab-Admin-Token`; the CLI reads it from a file or `COLLAB_ADMIN_TOKEN`, and does not save it in project config. Full-ledger export also requires this operator token. Client TLS trust and mTLS identity are configured at `init` with `--ca`, `--client-cert`, and `--client-key`.

Sensitive reads (leases, events, queue, rendered reports, and verification) require an agent-signed request and are limited to that identity's session. A remote broker also needs access to the authoritative server-side Git checkout named during `init`; mTLS secures transport but does not provide that checkout. Protect the database, TLS keys, operator token, Git checkout, gate configuration, backup directory, and local agent keys with operating-system access controls. The broker currently assumes a single active broker process per SQLite ledger/export directory.

`--export-dir` maintains `events.jsonl` by appending events and syncing writes. On restart, the broker verifies the database chain, checks that the backup exactly matches a prefix of the ledger, and appends any missing suffix. A backup mismatch fails startup rather than silently rewriting history. `export --out FILE` provides a full-ledger snapshot that can be appended repeatedly only when the existing file exactly matches the verified server ledger prefix. These are tamper-evident exports, not immutable storage by themselves; copy them to separately controlled/offline storage for stronger backup guarantees.

Agent requests are timestamped and nonce-protected. The database stores event signatures and historical key versions so rotation/revocation does not invalidate verification of older events. Receipts are derived from the server checkout (commit tree, changed paths, and patch hash); gate results record outcome metadata and an output digest, not raw logs. Queue approval must come from an agent in the same session other than the submitter. The integration branch uses a compare-and-swap update-ref; a concurrent branch movement leaves the queue item retryable.

On Linux, the broker-mediated `write` endpoint uses directory file descriptors, no-follow opens, and atomic replacement to reject traversal and symlink escapes. It blocks unleased writes made through that endpoint, but cannot stop an agent with direct filesystem access from bypassing it; use OS/container isolation or a filesystem guard if that enforcement is required. The adapter API is extensible at compile time; there are no dynamic Go-plugin loading hooks yet.
