#!/usr/bin/env bash
# Simulate two terminal agents and generate a signed, replayable AsciiDoc report.
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TMP=$(mktemp -d)
PORT=${COLLAB_DEMO_PORT:-18977}
PID=''
cleanup() { if [[ -n "$PID" ]]; then kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; fi; echo "Demo evidence directory: $TMP"; }
trap cleanup EXIT
(cd "$ROOT" && CGO_ENABLED=1 go build -o "$TMP/collab" ./cmd/collab)
export COLLAB_URL="http://127.0.0.1:$PORT"
export COLLAB_TOKEN="$(date +%s)-local-only-demo-broker-token"
"$TMP/collab" serve -listen "127.0.0.1:$PORT" -db "$TMP/state.sqlite" -key "$TMP/broker.key" -pub "$TMP/broker.pub" >"$TMP/broker.log" 2>&1 &
PID=$!
for i in {1..50}; do if curl -fsS -H "Authorization: Bearer $COLLAB_TOKEN" "$COLLAB_URL/healthz" >/dev/null 2>&1; then break; fi; sleep 0.1; done
"$TMP/collab" register -agent alpha -model model-a
"$TMP/collab" register -agent beta -model model-b
"$TMP/collab" say -agent alpha -to beta -body 'I am preparing the src contract; work in docs.'
"$TMP/collab" claim -agent alpha -path src -ttl 60
"$TMP/collab" claim -agent beta -path docs -ttl 60
if "$TMP/collab" claim -agent beta -path src/main.go -ttl 60; then echo 'FAIL: overlapping claim accepted' >&2; exit 1; else echo 'PASS: conflicting claim rejected'; fi
mkdir -p "$TMP/repo/src"
git -C "$TMP/repo" init -q -b main
git -C "$TMP/repo" config user.name 'Collab demo'
git -C "$TMP/repo" config user.email demo@example.invalid
printf 'package main\n' >"$TMP/repo/src/main.go"
git -C "$TMP/repo" add .
git -C "$TMP/repo" commit -qm 'Demo commit'
SHA=$(git -C "$TMP/repo" rev-parse HEAD)
"$TMP/collab" receipt -repo "$TMP/repo" -agent alpha -commit "$SHA" -summary 'Added initial source file'
"$TMP/collab" gate -agent alpha -commit "$SHA" -workdir "$TMP/repo" -- git status --porcelain
"$TMP/collab" report -db "$TMP/state.sqlite" -key "$TMP/broker.key" -out "$TMP/collaborate.adoc"
HEAD=$("$TMP/collab" events | python3 -c 'import json,sys; print(json.load(sys.stdin)[-1]["hash"])')
"$TMP/collab" verify -db "$TMP/state.sqlite" -pub "$TMP/broker.pub" -expected-head "$HEAD"
echo "Read the collaboration record at: $TMP/collaborate.adoc"
