# CI and the merge boundary

The repository workflow [`ci.yml`](../.github/workflows/ci.yml) runs on GitHub-hosted runners with `contents: read` and publishes the stable check `go-verify`. It runs formatting, uncached tests, the race detector, `go vet`, and builds every binary with CGO enabled and the system SQLite development library installed.

`scripts/check.sh` is the same sequence locally. A local pass means the required check passes, apart from runner-only setup such as installing `libsqlite3-dev`.

## What the ruleset should require

The checked-in [`main-required-ci.json`](../.github/rulesets/main-required-ci.json) is a proposal for the default branch:

- require the `go-verify` and `agent-registry` checks from the GitHub Actions integration;
- require a pull request before merging, with a strict up-to-date branch;
- require at least one independent approval, dismiss stale approvals, and resolve review threads;
- allow exactly one bypass: repository admins, and only when merging a pull request (`bypass_mode: pull_request`), so the sole maintainer can merge their own PRs while direct pushes to `main` stay blocked.

`.github/scripts/validate_ruleset.py` (run by `scripts/check.sh`) validates this JSON. When `GITHUB_TOKEN` or `GH_TOKEN` is set it also reads the live ruleset through the API and fails if it is missing, not `active`, or weaker than the checked-in policy; without a token it prints that the live check was skipped. CI does not pass a token to that step, so run `GH_TOKEN=$(gh auth token) python3 .github/scripts/validate_ruleset.py` locally to check the live state.

Editing the checked-in JSON does not change the live repository. A repository administrator must apply the ruleset through the GitHub UI or an authorized administration client. Apply the `agent-registry` requirement only after the intended identities are configured and the check has produced a successful run.

## What CI proves

A green run shows that the configured commands ran on GitHub-hosted infrastructure against that commit. It does not prove code correctness, test sufficiency, or hostile-agent isolation. The workflow supplies no provider API keys or application secrets to pull-request code, and the checkout step disables persisted Git credentials.

The `coordinate` workflow is a conflict detector, not an isolation boundary. It makes overlapping PR leases visible and, when made a required check, blocks them from merging.

## Applying a ruleset change

1. Read the live ruleset through the authenticated GitHub API and compare it with the checked-in proposal.
2. Confirm that the required check names match the jobs that actually run, and that the Actions integration is the expected source.
3. Apply the change with an administrator account. Do not let a workflow or a pull request modify live rulesets.
4. Open a test PR, confirm that an unapproved or stale branch is blocked, then confirm that an independently approved and up-to-date branch can merge. Do not merge the test PR.

## References

- GitHub, [Available rules for rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets)
- GitHub, [About rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets)
- GitHub, [REST API endpoints for repository rules](https://docs.github.com/en/rest/repos/rules)
- GitHub Actions, [Automatic token authentication](https://docs.github.com/en/actions/security-for-github-actions/security-guides/automatic-token-authentication)
- Model Context Protocol, [Protocol overview](https://modelcontextprotocol.io/docs/2026-07-28/getting-started/intro)
