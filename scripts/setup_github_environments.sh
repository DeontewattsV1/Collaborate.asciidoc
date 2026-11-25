#!/usr/bin/env bash
# Creates every GitHub Environment the workflows use, restricts each to the
# main branch, fills in the non-secret variables, and then asks for each API
# key without echoing it. Press Enter at a key prompt to skip it.
#
#   gh auth login            # once; needs admin on the repository
#   bash scripts/setup_github_environments.sh
#
# Re-running is safe: environments and variables are updated in place, and a
# skipped key leaves any existing secret untouched. Keys go straight from the
# hidden prompt to GitHub's encrypted secret store; they are never written to
# disk, passed as command-line arguments, or committed.
set -euo pipefail

REPO="${REPO:-DeontewattsV1/Collaborate.asciidoc}"

# Default model IDs. Change any of them here, or later in
# Settings > Environments > <name> > Variables. Check each provider's model
# list: IDs change often.
declare -A MODEL=(
  [agent-openai]="${OPENAI_MODEL:-gpt-6.2-sol}"
  [agent-anthropic]="${ANTHROPIC_MODEL:-claude-sonnet-5-5}"
  [agent-gemini]="${GEMINI_MODEL:-gemini-3.8-flash}"
  [agent-xai]="${XAI_MODEL:-grok-4.7}"
)

command -v gh >/dev/null || { echo "Install the GitHub CLI first: https://cli.github.com"; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "Run 'gh auth login' first."; exit 1; }

ensure_env() { # name
  local env="$1"
  gh api -X PUT "repos/$REPO/environments/$env" --silent \
    -F "deployment_branch_policy[protected_branches]=false" \
    -F "deployment_branch_policy[custom_branch_policies]=true"
  if ! gh api "repos/$REPO/environments/$env/deployment-branch-policies" --jq '.branch_policies[].name' | grep -qx main; then
    gh api -X POST "repos/$REPO/environments/$env/deployment-branch-policies" --silent -f name=main -f type=branch
  fi
  echo "environment $env: ready (deployments limited to main)"
}

set_var() { # env name value
  [ -n "$3" ] || return 0
  gh variable set "$2" --env "$1" --repo "$REPO" --body "$3" >/dev/null
  echo "  variable $2 = $3"
}

ask_secret() { # env name description
  local value
  if [ ! -t 0 ]; then echo "  (not a terminal; skipped secret $2)"; return 0; fi
  read -r -s -p "  $3 for $1 ($2), Enter to skip: " value; echo
  if [ -n "$value" ]; then
    printf '%s' "$value" | gh secret set "$2" --env "$1" --repo "$REPO" >/dev/null
    echo "  secret $2 saved"
  fi
  unset value
}

echo "Configuring $REPO"
for env in agent-openai agent-anthropic agent-gemini agent-xai; do
  ensure_env "$env"
  set_var "$env" MODEL_NAME "${MODEL[$env]}"
  ask_secret "$env" MODEL_API_KEY "API key"
done

echo
echo "Done. Copy each MODEL_API_KEY onto the VM as the matching provider variable (see docs/DEV-GUILD.md). GitHub will not show the secret back."
echo "Optional: in Settings > Environments, add yourself as a required reviewer to approve each paid run."
