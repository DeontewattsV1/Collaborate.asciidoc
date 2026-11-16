# Collaborate.asciidoc
multi agent dispatch parallel coordination shared context verification

## Current prototype

This repository now includes a local-first Go prototype for multi-agent repository coordination. Its broker records signed requests in an append-only, SHA-256 hash-chained SQLite ledger; agents can claim and release path leases, exchange short messages, and write signed Git commit receipts. `collaborate render` creates a deterministic AsciiDoc session report.

This is an MVP scaffold, not a production multi-agent runtime. It does **not** launch or adapt model CLIs, isolate model worktrees automatically, or prevent an agent from editing files without first claiming them. Leases coordinate clients that follow the protocol; an OS-level filesystem guard or controlled edit shim is still needed to enforce writes.

The existing [`Event schema`](Event%20schema), [`collab-agent`](collab-agent), and [`AsciiDoc`](AsciiDoc) files describe the intended protocol and target experience. The implemented subset and its limitations are documented below. A rendered sample from the runnable prototype is available at [`examples/collaborate.AsciiDoc`](examples/collaborate.AsciiDoc).

## Requirements

- Go 1.22+
- Git
- The SQLite driver is a Go module dependency and is downloaded during the first build.

## Build and test

```sh
go mod tidy
go test ./...
go vet ./...
go build -o ./collaborate ./cmd/collaborate
```

## Two-agent local demo

In terminal 1, start the broker. It intentionally binds only to loopback; do not expose it directly to the network.

```sh
./collaborate serve --db /tmp/collaborate-demo.sqlite
```

In terminal 2, make a throwaway repository and initialize a session:

```sh
mkdir -p /tmp/collaborate-demo-repo
cd /tmp/collaborate-demo-repo
git init
git config user.name Demo
git config user.email demo@example.invalid
echo 'hello' > note.txt
git add note.txt && git commit -m initial
/path/to/collaborate init --session demo-1 --repo "$PWD"
/path/to/collaborate register --agent A --model model-a --repo "$PWD"
/path/to/collaborate register --agent B --model model-b --repo "$PWD"
```

Agent A claims the file. Agent B's competing claim is rejected while A's lease is live:

```sh
COLLAB_AGENT=A /path/to/collaborate claim --intent 'update note' note.txt
COLLAB_AGENT=B /path/to/collaborate claim --intent 'competing update' note.txt
# Expected: HTTP 409 lease conflict naming agent A
```

After editing and staging a change, commit and record its receipt, then verify and render:

```sh
echo 'collaboration works' >> note.txt
git add note.txt
COLLAB_AGENT=A /path/to/collaborate commit -m 'Update note'
/path/to/collaborate verify --repo "$PWD"
/path/to/collaborate render --repo "$PWD" > collaborate.AsciiDoc
```

The receipt contains the commit SHA, a SHA-256 of the commit patch, and the paths reported by Git. It is a signed agent attestation, not independent proof that the broker checked out and re-derived the commit. Keep `.collab/` private: it contains local config and agent private keys.

## CLI commands

- `serve --db FILE [--addr 127.0.0.1:8787] [--lease-ttl 90s]` — run the loopback broker.
- `init --session ID --repo DIR [--url URL]` — create session config and register the session with the broker.
- `register --agent ID --model MODEL` — generate an Ed25519 keypair locally and register its public key.
- `claim --intent TEXT PATH...`, `release PATH...`, `renew` — manage exclusive, expiring exact-path leases.
- `say MESSAGE...` — append an agent message.
- `commit -m MESSAGE` — commit already-staged Git changes and append a signed receipt; `receipt` records the current `HEAD` without creating a commit.
- `spawn --agent ID --branch BRANCH` — create a Git worktree and branch (does not start an AI agent).
- `status`, `verify`, `render` — inspect active leases, verify the whole ledger, and render the AsciiDoc report.

Set `COLLAB_AGENT` to select the signing identity. Private keys live at `.collab/keys/<agent>.key` and are created with mode `0600` on Unix-like systems.

## Design and security boundaries

- The service refuses non-loopback listen addresses. The prototype has no remote authentication/TLS deployment configuration.
- Session creation and agent registration are unauthenticated bootstrap routes. Loopback binding is part of the prototype's trust boundary.
- Agent API requests are Ed25519-signed and timestamped with one-time nonces. Events contain the request digest/signature and link to the prior event hash. Verification checks both the hash chain and signatures against registered public keys.
- Lease acquisition is all-or-nothing for the requested exact paths, with TTL expiry and server-side conflict checks. Directory-prefix overlap, hunk-level leases, and write interception are not implemented.
- Commit receipts currently trust the client-provided commit SHA and file list. The broker does not inspect Git objects, re-run tests, enforce a merge queue, check gate logs, or require reviewer quorum.
- The AsciiDoc renderer uses fixed ordering and event timestamps, so repeated renders of the same ledger are byte-identical.

## Suggested next steps

1. Add integration tests for concurrent claims, lease expiry, request replay, and broker restart.
2. Derive receipt file lists and patch hashes on a trusted server-side checkout, and independently re-run configured gates.
3. Add an edit shim or filesystem guard so unleased writes are blocked rather than merely discouraged.
4. Isolate agents in separate worktrees, add a merge queue/review workflow, and handle overlapping directory paths.
5. Add an authenticated remote deployment (TLS/mTLS or a private network), key rotation/revocation, and append-only backup/export.
6. Implement model adapters as separate plugins that call the broker protocol; this repo currently coordinates identities but does not invoke models.
