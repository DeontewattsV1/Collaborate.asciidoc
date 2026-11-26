# GitHub agent registry

The repository-controlled registry lives at [`.github/agent-registry.json`](../.github/agent-registry.json). Its shape is defined by [`.github/agent-registry.schema.json`](../.github/agent-registry.schema.json) using JSON Schema Draft 2020-12.

The PR check reads registry policy from the protected base commit, never from the proposed PR version, so a PR cannot authorize its own author.

The checked-in registry marks an entry active only after its GitHub identity is verified. Entries without a verified identity stay unconfigured and deny all access.

## Identity and branch matching

An active agent has one or more GitHub identities. Each identity contains `account_type` (`user` or `bot`), the current `login`, and the immutable numeric `github_user_id`. Matching uses account type and numeric user ID, not the mutable login.

A GitHub App bot uses its bot account ID, often displayed with a `[bot]` suffix, never the App installation ID. An OAuth-backed platform that opens PRs as a human is represented by that human account. If several platforms share that account, distinct branch prefixes such as `claude/` and `codex/` disambiguate them.

Only `active` entries can identify authors or authorize tools. `suspended` entries preserve identity metadata but are denied. `unconfigured` entries omit identities and deny all tools. Branch prefixes are reserved across every status, so a suspended or not-yet-configured namespace cannot be reassigned accidentally. Prefixes are case-sensitive, include the trailing slash, and may not be duplicated or overlap (`agent/` and `agent/review/` conflict).

An unknown identity is rejected. A shared active identity requires a branch prefix that selects exactly one matching agent. If a configured prefix identifies a different agent than the PR author identity, the check rejects the PR rather than guessing. A unique identity can identify its agent without a prefix unless another configured prefix contradicts it. Commit-message `Co-Authored-By` trailers are not authorization evidence.

## Runtime tool allowlist

Each agent has a required `allowed_tools` array. An empty array denies all tools. Grants are exact namespaced strings:

- `mcp:collaborate_claim`, `mcp:collaborate_renew`, `mcp:collaborate_release`, `mcp:collaborate_say`, `mcp:collaborate_inbox`, `mcp:collaborate_receipt` for MCP calls;
- `adapter:<provider-name>` for third-party WASI provider adapters.

The runtime resolves an active `agent_id` and checks the exact tool string immediately before exposing or executing it. Missing entries, suspended or unconfigured entries, and unlisted tools are denied.

## Activating a platform

1. Verify the author account that GitHub actually shows on a PR, review, or comment.
2. Set the entry's `status` to `active`.
3. Add its identity and numeric `github_user_id`.
4. Grant only the tools it needs.
5. Validate with `go test ./...` and the `agent-registry` check.

Registry changes should go through the repository's normal review and ruleset controls.

## Pull-request enforcement

`.github/workflows/agent-registry.yml` runs on `pull_request_target` and checks out only the event's base commit with checkout credentials disabled. It does not check out or execute PR code. The job token is limited to read access to repository contents and pull requests.

The Go checker validates registry consistency, verifies the PR author and branch against the base registry, then reads changed paths for the PR and for other open PRs to the same base branch whose author or branch also resolves to an active registry entry. Unregistered open PRs do not create authoritative claims. Exact file conflicts and ancestor or descendant path conflicts are reported and fail the `agent-registry` status check. A missing or invalid registry, an API failure, an unrecognized author, or an ambiguous current-PR identity also fails closed.

This check detects overlap among open PRs visible to the token at the time of the run. It is not an atomic reservation or a replacement for branch protection, independent review, or merge-queue serialization.
