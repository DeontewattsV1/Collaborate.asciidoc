#!/bin/bash
# Start agentterm, the Next.js standalone server, and the path-based
# proxy. Existing files on /data (log.jsonl, server.key) are left
# untouched. Guild chat state is kept in /data/guild so it survives
# auto-stop without mixing with agentterm's files.
set -euo pipefail

# Start as root only long enough to make the Fly volume writable for the
# unprivileged `node` user, then re-exec this script as that user.
if [ "$(id -u)" = "0" ]; then
  mkdir -p /data/guild
  chown -R node:node /data
  exec setpriv --reuid=node --regid=node --init-groups "$0" "$@"
fi

mkdir -p /data/guild
# The route store writes /tmp/dev-guild-state.json. Point that path
# at the volume so idle stops do not wipe chat state.
if [ -e /tmp/dev-guild-state.json ] && [ ! -L /tmp/dev-guild-state.json ]; then
  if [ ! -e /data/guild/dev-guild-state.json ]; then
    mv /tmp/dev-guild-state.json /data/guild/dev-guild-state.json
  else
    rm -f /tmp/dev-guild-state.json
  fi
fi
ln -sfn /data/guild/dev-guild-state.json /tmp/dev-guild-state.json

/usr/local/bin/agentterm serve \
  --addr 127.0.0.1:8787 \
  --registry /etc/agentterm/agent-registry.json \
  --data /data &
agent_pid=$!

cd /app
HOSTNAME="${HOSTNAME:-127.0.0.1}" PORT="${PORT:-3000}" node server.js &
web_pid=$!

/usr/local/bin/flyproxy \
  -listen 0.0.0.0:8080 \
  -web 127.0.0.1:3000 \
  -term 127.0.0.1:8787 &
proxy_pid=$!

cleanup() {
  trap - INT TERM EXIT
  kill "$agent_pid" "$web_pid" "$proxy_pid" 2>/dev/null || true
  wait "$agent_pid" "$web_pid" "$proxy_pid" 2>/dev/null || true
}
trap cleanup INT TERM EXIT

# Exit if any child dies so Fly can restart the machine.
# wait -n is allowed to fail under set -e, so capture status first.
set +e
wait -n "$agent_pid" "$web_pid" "$proxy_pid"
status=$?
set -e
cleanup
exit "$status"
