# GitHub CI integration gate

The repository workflow [`ci.yml`](../.github/workflows/ci.yml) uses GitHub-hosted runners, grants `contents: read`, and publishes the stable job check `go-verify`. It runs format checks, uncached tests, the race detector, `go vet`, and builds both CLIs with CGO enabled and the system SQLite development library installed.

The checked-in [`main-required-ci.json`](../.github/rulesets/main-required-ci.json) targets the repository default branch. The live ruleset `main-require-independent-ci` (ID `27378635`) is active: updates must come through pull requests and the strict `go-verify` status check must pass. The check is bound to the GitHub Actions integration (`integration_id` 15368), and there are no bypass actors. The submitted payload sets required approving reviews to zero; GitHub’s stored ruleset additionally returned `require_extra_approval_for_unattributed_changes: true`. Thus no general fixed approval count is required, but GitHub may request an extra approval for unattributed changes. The JSON file remains declarative; editing it does not automatically update the live setting. Rulesets layer with any existing branch-protection settings.

The CI result demonstrates that the configured commands ran on GitHub-hosted infrastructure. It does not prove code correctness, test sufficiency, or hostile-agent isolation. The workflow supplies no provider API keys or other application secrets to pull-request code. The checkout step disables persisted Git credentials.

## Official references

- GitHub, [Available rules for rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets): required checks, strict/loose freshness, and expected check source.
- GitHub, [About rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets): enforcement states, branch targeting, bypass and rule layering.
- GitHub, [REST API endpoints for repository rules](https://docs.github.com/en/rest/repos/rules): ruleset payload schema and required-status-check fields.
- GitHub Actions, [Automatic token authentication](https://docs.github.com/en/actions/security-for-github-actions/security-guides/automatic-token-authentication): least-privilege `GITHUB_TOKEN` permissions.
- GitHub API, [GitHub Actions integration metadata](https://api.github.com/apps/github-actions): integration identity used by the ruleset check source.
- Model Context Protocol, [Protocol overview](https://modelcontextprotocol.io/docs/2026-07-28/getting-started/intro) and [Build an MCP client](https://modelcontextprotocol.io/docs/2026-07-28/develop/build-client): client/server protocol and client tool-call flow.
