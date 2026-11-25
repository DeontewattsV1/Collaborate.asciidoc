#!/usr/bin/env bash
# One command that runs everything the required `go-verify` CI check runs.
# CI calls this same script, so a local pass means a CI pass (apart from
# runner-only setup such as installing libsqlite3-dev).
#
#   scripts/check.sh           full check (as CI)
#   scripts/check.sh --quick   skip the race detector (much faster)
#
# Requirements: Go (version from go.mod), Python 3.10+, a C compiler and
# libsqlite3-dev for cmd/collab. actionlint is used if installed.
set -euo pipefail
cd "$(dirname "$0")/.."

quick=false
for arg in "$@"; do
  case "$arg" in
    --quick) quick=true ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done

export CGO_ENABLED=1
failed=()
gh_group() { if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::group::$1"; else printf '\n\033[1m== %s\033[0m\n' "$1"; fi; }
gh_end() { if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::endgroup::"; fi; }
step() { # name, command...
  local name="$1"; shift
  gh_group "$name"
  if "$@"; then gh_end; else gh_end; failed+=("$name"); echo "FAILED: $name" >&2; fi
}

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

step "ruleset policy"            python3 .github/scripts/validate_ruleset.py
step "repository hygiene"        python3 scripts/repo_hygiene.py
step "python tests (scripts/)"   python3 -m unittest discover -s scripts -p 'test_*.py'
step "dev guild gateway tests"  bash -c 'cd web/dev-guild && npm ci --ignore-scripts --no-audit --no-fund && node --test lib/gateway.test.js'
step "python tests (tests/)"     python3 -m unittest discover -s tests -p 'test_*.py'
step "gofmt"                     bash -c 'files=$(gofmt -l cmd internal); [ -z "$files" ] || { echo "$files"; exit 1; }'
step "go vet"                    go vet ./...
step "go test"                   go test -count=1 ./...
if [ "$quick" = false ]; then
  step "go test -race"           go test -race -count=1 ./...
fi
step "build binaries"            bash -c "go build -trimpath -buildvcs=false -o '$out/collaborate' ./cmd/collaborate \
                                   && go build -trimpath -buildvcs=false -o '$out/collab' ./cmd/collab \
                                   && go build -trimpath -buildvcs=false -o '$out/agentterm' ./cmd/agentterm \
                                   && go build -trimpath -buildvcs=false -o '$out/guild' ./cmd/guild"
if command -v actionlint >/dev/null 2>&1; then
  step "actionlint"              actionlint -shellcheck=
else
  echo "(actionlint not installed; skipped. CI-equivalent workflow checks still ran in repository hygiene.)"
fi
step "whitespace"                git diff --check

echo
if [ "${#failed[@]}" -gt 0 ]; then
  echo "check.sh: FAILED: ${failed[*]}" >&2
  exit 1
fi
echo "check.sh: all checks passed$([ "$quick" = true ] && echo ' (race detector skipped)')"
