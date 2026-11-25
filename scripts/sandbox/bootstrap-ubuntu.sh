#!/usr/bin/env bash
# Bootstrap a remote Ubuntu 24.04 server as the Collaborate sandbox that you
# drive from an iPhone (Secure ShellFish) or any SSH client. See
# docs/IPHONE-SANDBOX.md. Safe to re-run: every step checks before it changes.
#
#   Step 1, as the provider's admin account (uses sudo):
#     curl -fsSL https://raw.githubusercontent.com/DeontewattsV1/Collaborate.asciidoc/main/scripts/sandbox/bootstrap-ubuntu.sh | bash -s -- admin
#   Step 2, reconnect as the new "sandbox" user, then:
#     curl -fsSL https://raw.githubusercontent.com/DeontewattsV1/Collaborate.asciidoc/main/scripts/sandbox/bootstrap-ubuntu.sh | bash -s -- user
#
# Options for "admin": --key "ssh-ed25519 AAAA... name" (repeatable) installs that
# public key for the sandbox user, e.g. the key Secure ShellFish shows you.
# Options for "user": --port N (agent terminal port on 127.0.0.1, default 8787;
# use the port your SSH client forwards, e.g. 8080), --no-check (skip
# scripts/check.sh --quick), --no-serve (do not start the agent terminal).
# Set SANDBOX_USER or COLLAB_REPO to override the defaults below.
set -euo pipefail

SANDBOX_USER="${SANDBOX_USER:-sandbox}"
COLLAB_REPO="${COLLAB_REPO:-https://github.com/DeontewattsV1/Collaborate.asciidoc.git}"
WORKSPACE="${WORKSPACE:-$HOME/workspace}"
COLLAB_ROOT="$WORKSPACE/Collaborate.asciidoc"
MARK_BEGIN="# >>> collaborate sandbox >>>"
MARK_END="# <<< collaborate sandbox <<<"

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
die() { echo "bootstrap: $*" >&2; exit 1; }

valid_pubkey() {
  # One public key line: type, base64 body, optional comment. Never a private key.
  [[ "$1" =~ ^(ssh-ed25519|ecdsa-sha2-nistp(256|384|521)|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com|ssh-rsa)\ [A-Za-z0-9+/]+=*(\ [^[:cntrl:]]*)?$ ]] || return 1
  if command -v ssh-keygen >/dev/null; then ssh-keygen -l -f - <<<"$1" >/dev/null 2>&1 || return 1; fi
}

admin() {
  local extra_keys=()
  while [ $# -gt 0 ]; do
    case "$1" in
      --key) [ $# -ge 2 ] || die "--key needs a public key"; valid_pubkey "$2" || die "--key is not a valid SSH public key line"
             extra_keys+=("$2"); shift 2 ;;
      *) die "unknown option $1" ;;
    esac
  done
  [ "$EUID" -eq 0 ] && SUDO="" || SUDO="sudo"
  command -v apt-get >/dev/null || die "this script expects Ubuntu/Debian (apt-get)"
  say "Installing packages"
  $SUDO env DEBIAN_FRONTEND=noninteractive apt-get update -qq
  $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
    git gh curl wget jq tmux build-essential golang-go python3 python3-pip python3-venv \
    libsqlite3-dev sqlite3 ca-certificates unzip zip rsync htop ripgrep bubblewrap >/dev/null
  say "Creating the unprivileged '$SANDBOX_USER' account (no sudo)"
  if ! id "$SANDBOX_USER" >/dev/null 2>&1; then
    $SUDO adduser --disabled-password --gecos "" "$SANDBOX_USER" >/dev/null
  fi
  if id -nG "$SANDBOX_USER" | tr ' ' '\n' | grep -qx sudo; then
    die "'$SANDBOX_USER' is in the sudo group; remove it (sudo deluser $SANDBOX_USER sudo) and re-run"
  fi
  home="$(getent passwd "$SANDBOX_USER" | cut -d: -f6)"
  $SUDO install -d -m 700 -o "$SANDBOX_USER" -g "$SANDBOX_USER" "$home/.ssh"
  keys="$home/.ssh/authorized_keys"
  # Reuse the admin's authorized keys (your iPhone key) if the sandbox has none.
  if ! $SUDO test -s "$keys"; then
    src="${SUDO_USER:+$(getent passwd "$SUDO_USER" | cut -d: -f6)/.ssh/authorized_keys}"
    [ -n "$src" ] && [ -s "$src" ] || src="$HOME/.ssh/authorized_keys"
    if [ -s "$src" ]; then
      $SUDO cp "$src" "$keys"
      echo "copied $(wc -l < "$src") authorized key(s) from $src"
    else
      echo "NOTE: no authorized_keys found to copy; add your iPhone's public key to $keys"
      $SUDO touch "$keys"
    fi
  fi
  for k in ${extra_keys[@]+"${extra_keys[@]}"}; do
    if $SUDO grep -qxF -- "$k" "$keys"; then
      echo "key already installed: ${k##* }"
    else
      printf '%s\n' "$k" | $SUDO tee -a "$keys" >/dev/null
      echo "installed key: ${k##* }"
    fi
  done
  $SUDO chown "$SANDBOX_USER:$SANDBOX_USER" "$keys"
  $SUDO chmod 600 "$keys"
  say "Done. Reconnect as '$SANDBOX_USER' and run the 'user' step."
}

user() {
  local run_check=1 serve=1 port="${AGENTTERM_PORT:-8787}"
  while [ $# -gt 0 ]; do
    case "$1" in
      --no-check) run_check=0; shift ;;
      --no-serve) serve=0; shift ;;
      --port) [ $# -ge 2 ] || die "--port needs a number"; port="$2"; shift 2 ;;
      *) die "unknown option $1" ;;
    esac
  done
  [[ "$port" =~ ^[0-9]+$ ]] && [ "$port" -ge 1024 ] && [ "$port" -le 65535 ] || die "--port must be 1024-65535"
  [ "$EUID" -ne 0 ] || die "run the 'user' step as the unprivileged sandbox user, not root"
  if id -nG | tr ' ' '\n' | grep -qx sudo; then
    echo "WARNING: this account can use sudo; the sandbox is meant to run without admin rights"
  fi
  for t in git go python3 tmux gh; do command -v "$t" >/dev/null || die "$t is missing; run the 'admin' step first"; done
  export GOTOOLCHAIN=auto CGO_ENABLED=1 PATH="$HOME/.local/bin:$PATH"
  mkdir -p "$WORKSPACE" "$HOME/.local/bin"

  say "Repository"
  if [ -d "$COLLAB_ROOT/.git" ]; then
    git -C "$COLLAB_ROOT" fetch --prune -q origin
    echo "already cloned at $COLLAB_ROOT (fetched; your working tree is untouched)"
  else
    git clone -q "$COLLAB_REPO" "$COLLAB_ROOT"
  fi

  say "Shell environment (~/.bashrc)"
  local block
  block=$(cat <<EOF
$MARK_BEGIN
export COLLAB_ROOT="$COLLAB_ROOT"
export AGENTTERM_SERVER="http://127.0.0.1:$port"
export GOTOOLCHAIN=auto
export CGO_ENABLED=1
export PATH="\$HOME/.local/bin:\$PATH"
alias collab-home='cd "\$COLLAB_ROOT"'
alias collab-status='git -C "\$COLLAB_ROOT" status'
alias collab-check='(cd "\$COLLAB_ROOT" && bash scripts/check.sh)'
alias collab-quick='(cd "\$COLLAB_ROOT" && bash scripts/check.sh --quick)'
alias collab-sync='git -C "\$COLLAB_ROOT" fetch --prune origin'
alias collab-prs='gh pr list --repo DeontewattsV1/Collaborate.asciidoc'
alias collab-runs='gh run list --repo DeontewattsV1/Collaborate.asciidoc --limit 20'
alias collab-terminal='tmux attach -t collaborate-terminal'
collab-agent() { tmux new-session -A -s "\${1:?usage: collab-agent <name>}" -c "\$COLLAB_ROOT"; }
$MARK_END
EOF
)
  touch "$HOME/.bashrc"
  # Replace any previous block so re-running never duplicates it.
  awk -v b="$MARK_BEGIN" -v e="$MARK_END" '$0==b{skip=1} !skip{print} $0==e{skip=0}' "$HOME/.bashrc" > "$HOME/.bashrc.tmp"
  printf '%s\n' "$block" >> "$HOME/.bashrc.tmp"
  mv "$HOME/.bashrc.tmp" "$HOME/.bashrc"
  echo "installed (open a new shell or run: source ~/.bashrc)"

  say "Building agentterm, collaborate and collab (Go from go.mod via GOTOOLCHAIN=auto)"
  (cd "$COLLAB_ROOT" && go version && go mod download \
    && go build -o "$HOME/.local/bin/agentterm" ./cmd/agentterm \
    && go build -o "$HOME/.local/bin/collaborate" ./cmd/collaborate \
    && go build -o "$HOME/.local/bin/collab" ./cmd/collab)

  if [ "$run_check" -eq 1 ]; then
    say "Verifying with scripts/check.sh --quick"
    (cd "$COLLAB_ROOT" && bash scripts/check.sh --quick)
  fi

  if [ "$serve" -eq 1 ]; then
    say "Agent terminal (loopback only, command execution off)"
    if tmux has-session -t collaborate-terminal 2>/dev/null; then
      echo "already running in tmux session 'collaborate-terminal'"
    else
      tmux new-session -d -s collaborate-terminal -c "$COLLAB_ROOT" \
        "$HOME/.local/bin/agentterm serve --addr 127.0.0.1:$port --registry .github/agent-registry.json --data $HOME/.agentterm"
    fi
    for _ in $(seq 1 20); do
      curl -fsS http://127.0.0.1:$port/api/info >/dev/null 2>&1 && break
      sleep 0.5
    done
    curl -fsS http://127.0.0.1:$port/api/info >/dev/null || die "agent terminal did not start; run: tmux attach -t collaborate-terminal"
    echo "agent terminal is up at http://127.0.0.1:$port (server key: $(curl -fsS http://127.0.0.1:$port/api/info | jq -r .server_key | cut -c1-16)…)"
  fi

  say "Ready"
  cat <<EOF
Next:
  source ~/.bashrc
  gh auth login --hostname github.com --git-protocol https   # fine-grained token, this repo only
  collab-agent claude     # one tmux session per agent: claude, codex, gemini, manus, operator
  collab-terminal         # watch the shared agent terminal
To view the terminal's web page on your phone, forward port $port over SSH (Secure ShellFish: Port Forwarding $port -> $port); never expose it publicly.
EOF
}

case "${1:-}" in
  admin) shift; admin "$@" ;;
  user) shift; user "$@" ;;
  *) echo "usage: $0 admin [--key PUBKEY]... | user [--port N] [--no-check] [--no-serve]" >&2; exit 2 ;;
esac
