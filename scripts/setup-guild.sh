#!/usr/bin/env bash
# Install or update the Dev Guild bridge on the Oracle VM (or any checkout).
# It never writes API keys. It reads the environment you already exported and
# prints which characters can speak.
#
#   export GUILD_OWNER_TOKEN='a long passphrase'
#   export ANTHROPIC_API_KEY=...          # optional, one or more providers
#   bash scripts/setup-guild.sh
#
# Re-running is safe. A running tmux session named dev-guild is replaced.
set -euo pipefail
cd "$(dirname "$0")/.."

say() { printf '\n== %s\n' "$1"; }

say "Build"
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}" CGO_ENABLED="${CGO_ENABLED:-1}"
mkdir -p "${HOME}/.local/bin"
go build -trimpath -buildvcs=false -o "${HOME}/.local/bin/guild" ./cmd/guild
go build -trimpath -buildvcs=false -o "${HOME}/.local/bin/agentterm" ./cmd/agentterm
go build -trimpath -buildvcs=false -o "${HOME}/.local/bin/collaborate" ./cmd/collaborate
export PATH="${HOME}/.local/bin:${PATH}"

if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
  say "GitHub Environment model names (keys stay in GitHub; they are not readable)"
  repo="${REPO:-DeontewattsV1/Collaborate.asciidoc}"
  for env in agent-openai agent-anthropic agent-gemini agent-xai; do
    name="$(gh variable get MODEL_NAME --env "$env" --repo "$repo" 2>/dev/null || true)"
    if [ -n "$name" ]; then
      echo "  $env MODEL_NAME=$name"
    else
      echo "  $env MODEL_NAME is unset"
    fi
  done
  echo "  Export the matching *_MODEL and paste MODEL_API_KEY into the matching *_API_KEY on this VM."
else
  echo "(gh is not logged in; skipped reading GitHub Environment variable names)"
fi

say "Who is online"
guild characters

if [ -z "${GUILD_OWNER_TOKEN:-}" ]; then
  echo
  echo "GUILD_OWNER_TOKEN is not set, so the bridge was not started."
  echo "Export it, then re-run this script. Do not put it in the repo."
  exit 0
fi

say "Service"
data="${GUILD_DATA:-${HOME}/.agentterm}"
mkdir -p "$data" "${HOME}/.config"
umask 077
envfile="${HOME}/.config/guild.env"
{
  for var in GUILD_OWNER_TOKEN GUILD_DEMO GUILD_WEB_ORIGIN \
    ANTHROPIC_API_KEY ANTHROPIC_MODEL ANTHROPIC_BASE_URL \
    OPENAI_API_KEY OPENAI_MODEL OPENAI_CHAT_MODEL OPENAI_BASE_URL \
    GEMINI_API_KEY GOOGLE_API_KEY GEMINI_MODEL GEMINI_BASE_URL \
    XAI_API_KEY XAI_MODEL XAI_BASE_URL TAY_MODEL \
    OLLAMA_HOST OLLAMA_MODEL \
    MANUS_API_KEY MANUS_BASE_URL MANUS_MODEL \
    CURSOR_API_KEY CURSOR_BASE_URL CURSOR_MODEL \
    COPILOT_API_KEY COPILOT_BASE_URL COPILOT_MODEL
  do
    if [ -n "${!var:-}" ]; then
      printf '%s=%q\n' "$var" "${!var}"
    fi
  done
} > "$envfile"
chmod 600 "$envfile"
echo "saved the variables that were set to ${envfile} (mode 600, outside the repo). Nothing was printed."
if command -v tmux >/dev/null 2>&1; then
  tmux kill-session -t dev-guild 2>/dev/null || true
  tmux new-session -d -s dev-guild -c "$PWD" \
    "set -a; . ${envfile}; set +a; exec ${HOME}/.local/bin/guild serve --data ${data} --addr 127.0.0.1:8790"
  echo "tmux session dev-guild is serving http://127.0.0.1:8790"
else
  echo "tmux is missing. Start it yourself:"
  echo "  guild serve --data ${data} --addr 127.0.0.1:8790"
fi

cat <<'EOF'

Loopback only. Do not open port 8790 in the Oracle security list.
Free path for Vercel, pick one:

  cloudflared tunnel --url http://127.0.0.1:8790
  tailscale funnel 8790

From Secure ShellFish, the same log is:

  guild tail
  collab-chat          # alias for: guild chat

Vercel project root: web/dev-guild
  GUILD_OWNER_TOKEN   same passphrase as this VM
  GUILD_BACKEND_URL   https URL of the tunnel, no path
Do not put provider keys in Vercel.

Verify the log:

  agentterm verify --file ~/.agentterm/guild.jsonl --server-key "$(guild pubkey)"
EOF
