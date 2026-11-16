# GitHub CI integration gate

The repository workflow [`ci.yml`](../.github/workflows/ci.yml) uses GitHub-hosted runners, grants `contents: read`, and publishes the stable job check `go-verify`. It runs format checks, uncached tests, the race detector, `go vet`, and builds both CLIs with CGO enabled and the system SQLite development library installed.

The proposed [`main-required-ci.json`](../.github/rulesets/main-required-ci.json) targets the repository default branch. If an administrator applies it, updates must come through pull requests and the strict `go-verify` status check must pass. It requires the check to originate from the GitHub Actions integration (`integration_id` 15368) and defines no bypass actors. It does not require human approvals. The JSON file is declarative only: it does not activate a GitHub setting. Confirm that the expected check has run and appears under this exact context before activating the ruleset. Rulesets layer with any existing branch-protection settings.

The CI result demonstrates that the configured commands ran on GitHub-hosted infrastructure. It does not prove code correctness, test sufficiency, or hostile-agent isolation. The workflow supplies no provider API keys or other application secrets to pull-request code. The checkout step disables persisted Git credentials.

## Official references

- GitHub, [Available rules for rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets): required checks, strict/loose freshness, and expected check source.
- GitHub, [About rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets): enforcement states, branch targeting, bypass and rule layering.
- GitHub, [REST API endpoints for repository rules](https://docs.github.com/en/rest/repos/rules): ruleset payload schema and required-status-check fields.
- GitHub Actions, [Automatic token authentication](https://docs.github.com/en/actions/security-for-github-actions/security-guides/automatic-token-authentication): least-privilege `GITHUB_TOKEN` permissions.
- GitHub API, [GitHub Actions integration metadata](https://api.github.com/apps/github-actions): integration identity used by the ruleset check source.
- Model Context Protocol, [Protocol overview](https://modelcontextprotocol.io/docs/2026-07-28/getting-started/intro) and [Build an MCP client](https://modelcontextprotocol.io/docs/2026-07-28/develop/build-client): client/server protocol and client tool-call flow.
