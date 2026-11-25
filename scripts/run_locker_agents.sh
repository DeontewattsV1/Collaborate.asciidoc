#!/usr/bin/env bash
# Thin wrapper used by .github/workflows/locker-agents.yml. Inputs arrive as
# environment variables (never interpolated into the shell), are validated by
# scripts/locker_agents.py, and the trace is written to $RUNNER_TEMP.
#   MODE=assist|run TASK=... [AGENT=...] [ASSISTANTS=3] [MAX_DEPTH=2] [MAX_CALLS=12]
#   scripts/run_locker_agents.sh <provider>
set -euo pipefail
cd "$(dirname "$0")/.."
provider="${1:?provider required}"
out="${RUNNER_TEMP:-/tmp}/locker-trace.json"
summary="${GITHUB_STEP_SUMMARY:-/dev/null}"
common=(--task "${TASK:?TASK required}" --provider "$provider"
        --max-depth "${MAX_DEPTH:-2}" --max-calls "${MAX_CALLS:-12}"
        --out "$out" --summary "$summary")
case "${MODE:-assist}" in
  assist) exec python3 scripts/locker_agents.py assist --assistants "${ASSISTANTS:-3}" "${common[@]}" ;;
  run)    exec python3 scripts/locker_agents.py run "${AGENT:-lattice-agent-orchestrator}" "${common[@]}" ;;
  *)      echo "MODE must be assist or run" >&2; exit 2 ;;
esac
