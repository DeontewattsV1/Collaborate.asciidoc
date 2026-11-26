# Command reference

`collaborate` is the signed broker. It authenticates agents with Ed25519 keys, grants expiring path leases, writes a hash-chained ledger, derives commit receipts from the server-side checkout, runs operator-configured gates, and queues changes for independent review.

Build it with the repository's Go version (`go.mod`) and `CGO_ENABLED=1`:

```sh
go build -o collaborate ./cmd/collaborate
```

## Local walkthrough

Create an operator-owned gate configuration before starting the broker. Integration is refused when no trusted gate is configured.

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

Create a Git repository with a first commit, then initialize and register two agents:

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

Create agent A's isolated worktree. An edit submitted through `write` is rejected unless the agent owns a live lease that covers the path:

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

`commit` commits staged changes and asks the broker to derive and store a receipt from Git. Submission is separate and is accepted only for that agent's current registered branch tip after a verified receipt exists. Agent B can independently review the queue item:

```sh
COLLAB_AGENT=B /path/to/collaborate queue
COLLAB_AGENT=B /path/to/collaborate review --queue-id 1 --verdict approve --reason 'looks good'
COLLAB_AGENT=A /path/to/collaborate integrate --queue-id 1
COLLAB_AGENT=A /path/to/collaborate verify --repo /tmp/collaborate-demo-repo
COLLAB_AGENT=A /path/to/collaborate render --repo /tmp/collaborate-demo-repo > collaborate.AsciiDoc
```

Gate configuration is not read from a submitted branch. Commands are passed as argv arrays without a shell by the broker; the example explicitly runs a shell. Gates run against the merge candidate before the trunk branch is advanced. Gate processes receive a reduced environment that omits inherited application secrets. **This is not an OS or container sandbox**: gates can still access the broker host filesystem and network, so configure trusted gates only and run the broker in an appropriately isolated environment.

## Commands

- `serve --db FILE [--addr 127.0.0.1:8787] [--lease-ttl 90s] [--gates-file FILE] [--export-dir DIR] [--tls-cert FILE --tls-key FILE --client-ca FILE --admin-token-file FILE]` — start the broker. Remote binds require TLS, verified client certificates (mTLS), and an operator token.
- `init --session ID [--repo DIR] [--url URL] [--trunk BRANCH] [--ca FILE --client-cert FILE --client-key FILE --admin-token-file FILE]` — create the session and local config.
- `register --agent ID --model MODEL [--repo DIR] [--admin-token-file FILE]` — provision an identity and create a local private key. Existing key files are never silently overwritten.
- `spawn [--agent ID] [--branch BRANCH]` — create the agent's worktree under `.collab/wt/` from the session trunk.
- `claim --intent TEXT PATH...`, `renew`, `release PATH...` — acquire, renew, or release expiring leases. Ancestor and descendant claims conflict.
- `write PATH < CONTENT` — ask the broker to atomically write inside the registered worktree; requires a covering lease. This is a controlled edit shim, not an OS-level prohibition on other programs writing files.
- `say MESSAGE...` — append a collaboration message.
- `commit -m MESSAGE` / `receipt` — commit staged changes (or record current `HEAD`) and request a server-derived receipt. The commit must be the registered branch tip and all changed paths must be covered by live leases.
- `submit`, `queue`, `review --queue-id N --verdict approve|request_changes`, `integrate --queue-id N` — submit a verified commit, review it independently, rerun configured gates on the merge candidate, and merge through the session trunk. Queue approvals and session data reads are session-scoped.
- `rotate-key`, `revoke-key --version N` — rotate the current Ed25519 key and revoke an older key version. Old private keys are archived locally during rotation; protect or remove those archives as your key-retention policy requires.
- `status`, `verify`, `render` — signed, session-scoped reads. Set `COLLAB_AGENT` or use `--agent`; `verify` validates the full ledger chain.
- `export --out FILE [--repo DIR] [--admin-token-file FILE]` — append and validate a full-ledger JSONL snapshot; requires the operator token when one is configured.
- `github-check --pr N [--repo OWNER/NAME] [--registry FILE] [--comment] [--summary FILE] [--json]` — evaluate one pull request's leases, conflicts, and review independence through the GitHub API (requires `GITHUB_TOKEN`); used by the `collaborate-coordination` workflow.
- `mcp --repo DIR --agent ID` — run the stdio MCP bridge for exactly one agent. See [`examples/mcp-hosts.template.json`](../examples/mcp-hosts.template.json). It intentionally exposes no shell or arbitrary filesystem operation.
- `adapter list [--plugins DIR]`, `adapter run --name echo|openai-compatible|anthropic|gemini --prompt TEXT`, or `adapter run --plugin MANIFEST.json --prompt TEXT` — run a compiled-in provider or a sandboxed WASI module. Dynamic modules use a strict JSON manifest and an API key environment variable; see [`WASI-PLUGINS.md`](WASI-PLUGINS.md).

Set `COLLAB_AGENT` to choose the signing identity; command-level `--agent` overrides it. Project config and private keys live under `.collab/`, which must remain private and untracked.

## Provider configuration

| Provider | Variables |
|---|---|
| OpenAI-compatible | `COLLAB_MODEL_BASE_URL`, `COLLAB_MODEL`, `COLLAB_MODEL_API_KEY` (or `OPENAI_API_KEY`) |
| Anthropic | `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`, `ANTHROPIC_API_KEY` |
| Gemini | `GEMINI_BASE_URL`, `GEMINI_MODEL`, `GEMINI_API_KEY` (or `GOOGLE_API_KEY`) |

## Agent terminal

`agentterm` is the shared terminal. Start it with the registry it should trust:

```sh
go run ./cmd/agentterm serve --registry .github/agent-registry.json
GITHUB_TOKEN=... agentterm clock-in --server http://127.0.0.1:8787 --instance claude-1 --branch claude/my-change
agentterm claim internal/foo && agentterm run -- go test ./internal/foo && agentterm clock-out
```

See [`AGENT-TERMINAL.md`](AGENT-TERMINAL.md) for the protocol, entry types, and limits.

## Other binaries

```sh
go build -o collab ./cmd/collab                 # separate shared-token pilot
go build -o guild ./cmd/guild                   # Dev Guild VM bridge
go build -o agent-registry-check ./cmd/agent-registry-check
```
