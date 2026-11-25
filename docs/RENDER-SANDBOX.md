# Render Sandboxes: Live Multi-Agent Demo

Use Render Sandboxes for **short-lived live demos** showing two real `collaborate` agents communicating through the real `internal/broker` server, Ed25519-signed ledger, and MCP stdio tools. Keep heavy or long-running workloads on your always-available VMs (Oracle `192.9.227.203` and GCP `136.66.157.149`).

## Pricing and free-tier status

- **No free sandbox tier:** Render's Free compute plans apply to web services, Postgres, and Key Value—**not** Sandboxes.
- **Usage billing (`1c-2g` default: 1 CPU, 2 GB RAM):** **$0.12 per active CPU-hour + $0.038 per hour of provisioned memory** (`$0.0020/CPU-minute + $0.000633/minute`, up to `~$0.158/hour`).
- **Workspace:** `collaboration_sandbox` (`tea-dc3ge117lnhs73fnjpig`, Hobby tier `$0/month` base fee, sandbox group `sbg-dc3h680u01pc73eld1ng` in Oregon, concurrency limit 100).
- **Short runs stay tiny:** a 1-minute demo costs under `$0.003` (less than a third of a cent). Keep `SANDBOX_TIMEOUT_SECONDS` small (default `300`, minimum `60`) so no sandbox is ever left running indefinitely. Suspended sandboxes incur `$0` CPU/memory; snapshot storage is `$0.08/GB-month`.

## What the live demo runs

`scripts/sandbox/render-sandbox.mjs` cross-compiles `./cmd/collaborate` (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64`), boots a fresh Render sandbox, uploads `collaborate` and `scripts/sandbox/render-demo.sh`, and streams the live output:

1. Starts the real `internal/broker` HTTP server (`collaborate serve`) with a gate config and SQLite hash-chained ledger.
2. Initializes a temporary Git repository and registers two separate Ed25519 agent identities (`claude` and `codex`) with isolated worktrees.
3. `claude` claims a lease on `notes.txt`, writes through the lease-checking broker, commits a server-verified receipt, and submits to the review queue.
4. Both agents exchange live signed messages via `collaborate mcp` (`collaborate_say` and `collaborate_inbox`) and `collaborate adapter run`:
   - If optional LLM API keys (`ANTHROPIC_API_KEY`, `GEMINI_API_KEY` / `GOOGLE_API_KEY`, `OPENAI_API_KEY` / `COLLAB_MODEL_API_KEY`) are present in the caller environment, the script forwards them into the sandbox (never logged or committed) and calls the live provider adapter.
   - If no LLM keys are set, it runs the repo's real non-LLM `echo` adapter and MCP `say`/`inbox` tools over the real broker at zero LLM cost.
5. `codex` inspects the queue, independently approves, integrates through the broker gate, and `collaborate verify` + `collaborate render` confirm the 12-event SHA-256 hash chain.

## 1. Launch locally with Render CLI + Node SDK

Install the Render CLI and verify workspace access (once):

```bash
curl -fsSL https://raw.githubusercontent.com/render-oss/cli/refs/heads/main/bin/install.sh | sh
export RENDER_API_KEY='rnd_...'   # never commit or echo
export RENDER_WORKSPACE_ID='tea-dc3ge117lnhs73fnjpig'
render workspace set "$RENDER_WORKSPACE_ID"
```

Quick CLI smoke test (terminates immediately):

```bash
sbx_id=$(render sandboxes create --group sbg-dc3h680u01pc73eld1ng --timeout 120 --wait -o json | jq -r '.id')
render sandboxes exec "$sbx_id" -- echo 'Hello from Render Sandboxes'
render sandboxes stop "$sbx_id" --confirm
```

Run the live two-agent broker demo via Node SDK:

```bash
cd scripts/sandbox
npm install --no-save --no-audit --no-fund @renderinc/sdk
SANDBOX_GROUP_ID=sbg-dc3h680u01pc73eld1ng SANDBOX_TIMEOUT_SECONDS=300 node render-sandbox.mjs
```

### Keep a sandbox up for a live demo (`keep=true`)

Set `SANDBOX_KEEP=true` with a finite `SANDBOX_TIMEOUT_SECONDS` (for example `300` or `600`). `render-sandbox.mjs` enforces `timeoutSeconds >= 60` and leaves the broker running inside `/tmp/collab-render-demo` while Render automatically terminates the sandbox when the timeout expires:

```bash
SANDBOX_KEEP=true SANDBOX_TIMEOUT_SECONDS=300 node scripts/sandbox/render-sandbox.mjs
# While live, inspect or run extra agent commands inside the printed <sandbox-id>:
render sandboxes exec <sandbox-id> -- /tmp/collaborate status --repo /tmp/collab-render-demo/repo --agent claude
render sandboxes exec <sandbox-id> -- /tmp/collaborate render --repo /tmp/collab-render-demo/repo --agent claude
# Stop early as soon as the demo finishes:
render sandboxes stop <sandbox-id> --confirm
```

## 2. Launch from iPhone Secure ShellFish (on Oracle or GCP VM)

You do **not** need to install the Render CLI on your Oracle (`192.9.227.203`) or GCP (`136.66.157.149`) VM unless you want to drive Render directly from the phone:

- **Option A (no Render CLI on VM):** connect in Secure ShellFish to `sandbox@192.9.227.203` or `sandbox@136.66.157.149` and run the demo script directly on the VM at `$0` compute (`bash ~/workspace/Collaborate.asciidoc/scripts/sandbox/render-demo.sh` with `COLLAB_BIN=~/.local/bin/collaborate`), or dispatch the GitHub workflow with `gh workflow run render-sandbox.yml -f timeoutSeconds=300 -f keep=false` (requires GitHub Actions billing to be active).
- **Option B (drive Render from the VM):** if you install Node 22 + `@renderinc/sdk` and export `RENDER_API_KEY` on the VM (in `~/.secrets/`, never inside the repo), run `node ~/workspace/Collaborate.asciidoc/scripts/sandbox/render-sandbox.mjs`.

## 3. Launch from GitHub Actions

1. Ensure GitHub Environment `sandbox-render` has secret `RENDER_API_KEY` and variables `RENDER_WORKSPACE_ID` (`tea-dc3ge117lnhs73fnjpig`) and `RENDER_SANDBOX_GROUP_ID` (`sbg-dc3h680u01pc73eld1ng`) via `bash scripts/setup_github_environments.sh`.
2. In GitHub, open **Actions -> render-sandbox -> Run workflow** on `main` (only the repository owner can trigger it).
3. Choose optional `plan` (blank = `1c-2g`), `timeoutSeconds` (default `300`), and `keep` (`true` leaves the sandbox up until `timeoutSeconds` expires).
4. Note: GitHub-hosted runners require active GitHub Actions billing/quota on the repository account; if Actions is billing-blocked, use Option 1 or 2 above.
