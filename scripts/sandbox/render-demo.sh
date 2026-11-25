#!/usr/bin/env bash
# Live multi-agent communication demo inside a Render Sandbox (or locally).
# Starts the real internal/broker server (`collaborate serve`), initializes a
# temporary Git repo, registers two Ed25519-signed agent identities (`claude`
# and `codex`), spawns isolated worktrees, exchanges signed messages over
# `collaborate mcp` (collaborate_say / collaborate_inbox) plus `adapter run`
# (live LLM if API keys are set in env, otherwise deterministic echo adapter),
# completes claim -> write -> commit -> submit -> review -> integrate, and
# verifies the SHA-256 hash-chained SQLite ledger.
set -euo pipefail

COLLAB_BIN="${COLLAB_BIN:-/tmp/collaborate}"
if [ ! -x "$COLLAB_BIN" ]; then
  if command -v collaborate >/dev/null 2>&1; then
    COLLAB_BIN="$(command -v collaborate)"
  else
    echo "error: collaborate binary not found at $COLLAB_BIN or in PATH" >&2
    exit 1
  fi
fi
command -v git >/dev/null 2>&1 || { echo "error: git is required inside the sandbox" >&2; exit 1; }

DEMO_ROOT="${DEMO_ROOT:-/tmp/collab-render-demo}"
BROKER_ADDR="${BROKER_ADDR:-127.0.0.1:18787}"
SESSION_ID="${SESSION_ID:-render-live-demo}"
KEEP_BROKER="${KEEP_BROKER:-false}"

rm -rf "$DEMO_ROOT"
mkdir -p "$DEMO_ROOT/repo/.github" "$DEMO_ROOT/export"

cat > "$DEMO_ROOT/gates.json" <<JSON
{
  "$SESSION_ID": [
    {"name": "notes-present", "command": ["sh", "-c", "test -s notes.txt"], "timeout_seconds": 15}
  ]
}
JSON

echo "== Starting real internal/broker server on $BROKER_ADDR =="
"$COLLAB_BIN" serve \
  --db "$DEMO_ROOT/broker.sqlite" \
  --addr "$BROKER_ADDR" \
  --gates-file "$DEMO_ROOT/gates.json" \
  --export-dir "$DEMO_ROOT/export" \
  </dev/null >"$DEMO_ROOT/broker.log" 2>&1 &
BROKER_PID=$!

cleanup() {
  if [ "$KEEP_BROKER" != "true" ] && kill -0 "$BROKER_PID" 2>/dev/null; then
    kill "$BROKER_PID" 2>/dev/null || true
    wait "$BROKER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

ready=false
for _ in $(seq 1 50); do
  if command -v curl >/dev/null 2>&1; then
    if curl -fsS "http://$BROKER_ADDR/health" >/dev/null 2>&1; then
      ready=true
      break
    fi
  elif (echo >"/dev/tcp/${BROKER_ADDR%:*}/${BROKER_ADDR#*:}") >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.1
done
if [ "$ready" != "true" ]; then
  echo "error: broker failed to start; log:" >&2
  cat "$DEMO_ROOT/broker.log" >&2 || true
  exit 1
fi
echo "broker ready (pid=$BROKER_PID)"

REPO_DIR="$DEMO_ROOT/repo"
cd "$REPO_DIR"
git init -q -b main
git config user.name "Render Sandbox Demo"
git config user.email "demo@collaborate.local"

cat > .github/agent-registry.json <<'JSON'
{
  "$schema": "./agent-registry.schema.json",
  "schema_version": 1,
  "agents": [
    {
      "agent_id": "claude",
      "display_name": "Claude",
      "status": "active",
      "github_identities": [{"account_type": "user", "login": "demo-claude", "github_user_id": 1}],
      "branch_prefixes": ["claude/"],
      "allowed_tools": [
        "mcp:collaborate_claim",
        "mcp:collaborate_renew",
        "mcp:collaborate_release",
        "mcp:collaborate_say",
        "mcp:collaborate_inbox",
        "mcp:collaborate_receipt"
      ]
    },
    {
      "agent_id": "codex",
      "display_name": "Codex",
      "status": "active",
      "github_identities": [{"account_type": "user", "login": "demo-codex", "github_user_id": 2}],
      "branch_prefixes": ["codex/"],
      "allowed_tools": [
        "mcp:collaborate_claim",
        "mcp:collaborate_renew",
        "mcp:collaborate_release",
        "mcp:collaborate_say",
        "mcp:collaborate_inbox",
        "mcp:collaborate_receipt"
      ]
    }
  ]
}
JSON

printf 'initial shared document\n' > notes.txt
git add .github/agent-registry.json notes.txt
git commit -q -m "initial demo commit"

echo "== Initializing session and two Ed25519 agent identities =="
"$COLLAB_BIN" init --session "$SESSION_ID" --repo "$REPO_DIR" --url "http://$BROKER_ADDR"
"$COLLAB_BIN" register --agent claude --model demo-claude --repo "$REPO_DIR"
"$COLLAB_BIN" register --agent codex --model demo-codex --repo "$REPO_DIR"

echo "== Spawning isolated Git worktrees =="
COLLAB_AGENT=claude "$COLLAB_BIN" spawn
COLLAB_AGENT=codex "$COLLAB_BIN" spawn

echo "== Agent 1 (claude): claim lease, write via broker, commit, submit =="
cd "$REPO_DIR/.collab/wt/claude"
COLLAB_AGENT=claude "$COLLAB_BIN" claim --intent "draft collaborative update in notes.txt" notes.txt
printf 'initial shared document\nclaude: proposed update via signed broker lease\n' \
  | COLLAB_AGENT=claude "$COLLAB_BIN" write notes.txt
git add notes.txt
COLLAB_AGENT=claude "$COLLAB_BIN" commit -m "claude: propose notes update"
COLLAB_AGENT=claude "$COLLAB_BIN" submit

echo "== Agent 1 (claude): live message via adapter + MCP collaborate_say =="
if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
  export ANTHROPIC_MODEL="${ANTHROPIC_MODEL:-claude-3-5-haiku-latest}"
  COLLAB_AGENT=claude "$COLLAB_BIN" adapter run --name anthropic \
    --prompt "In one short sentence, tell codex that queue item 1 on notes.txt is ready for review."
elif [ -n "${GEMINI_API_KEY:-${GOOGLE_API_KEY:-}}" ]; then
  export GEMINI_MODEL="${GEMINI_MODEL:-gemini-2.5-flash}"
  COLLAB_AGENT=claude "$COLLAB_BIN" adapter run --name gemini \
    --prompt "In one short sentence, tell codex that queue item 1 on notes.txt is ready for review."
else
  COLLAB_AGENT=claude "$COLLAB_BIN" adapter run --name echo \
    --prompt "[claude adapter] Submitted notes.txt update (queue item 1) for codex review."
fi

printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"collaborate_say","arguments":{"message":"[claude mcp] Queue item 1 is ready; please inspect inbox and review notes.txt."}}}' \
  | "$COLLAB_BIN" mcp --repo "$REPO_DIR" --agent claude

echo "== Agent 2 (codex): read live inbox via MCP, reply, review, integrate =="
cd "$REPO_DIR/.collab/wt/codex"
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"collaborate_inbox","arguments":{"after":0}}}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"collaborate_say","arguments":{"message":"[codex mcp] Read claude message from inbox; checking queue and running review."}}}' \
  | "$COLLAB_BIN" mcp --repo "$REPO_DIR" --agent codex

if [ -n "${COLLAB_MODEL_API_KEY:-${OPENAI_API_KEY:-}}" ]; then
  export COLLAB_MODEL="${COLLAB_MODEL:-${OPENAI_MODEL:-gpt-4o-mini}}"
  COLLAB_AGENT=codex "$COLLAB_BIN" adapter run --name openai-compatible \
    --prompt "In one short sentence, confirm to claude that you reviewed and approved queue item 1."
else
  COLLAB_AGENT=codex "$COLLAB_BIN" adapter run --name echo \
    --prompt "[codex adapter] Verified claude receipt and approving queue item 1."
fi

COLLAB_AGENT=codex "$COLLAB_BIN" queue
COLLAB_AGENT=codex "$COLLAB_BIN" review --queue-id 1 --verdict approve --reason "verified signed receipt and notes.txt diff"
COLLAB_AGENT=codex "$COLLAB_BIN" integrate --queue-id 1

echo "== Verifying SHA-256 hash-chained ledger and rendering transcript =="
COLLAB_AGENT=claude "$COLLAB_BIN" verify
COLLAB_AGENT=claude "$COLLAB_BIN" render

if [ "$KEEP_BROKER" = "true" ]; then
  echo "KEEP_BROKER=true: broker left running on http://$BROKER_ADDR (pid=$BROKER_PID, demo=$DEMO_ROOT)"
fi
