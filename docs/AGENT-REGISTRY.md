# GitHub agent registry

The repository-controlled registry lives at [`.github/agent-registry.json`](../.github/agent-registry.json), and its shape is defined by [`.github/agent-registry.schema.json`](../.github/agent-registry.schema.json) using JSON Schema Draft 2020-12. The PR check reads registry policy from the protected base commit, never from the proposed PR version, so a PR cannot authorize its own author.

The checked-in registry includes entries for Manus, Claude, ChatGPT, GitHub Copilot, OpenAI Codex, Cursor, Gemini CLI, and Windsurf. They are deliberately marked `unconfigured`: no platform bot IDs have been guessed, and an installed GitHub App's installation ID is not a user ID. Until an administrator supplies verified GitHub account identities, these entries cannot identify PR authors or invoke runtime tools. Each begins with an empty tool allowlist. The workflow therefore fails closed for unconfigured authors; configure at least the repository's intended collaborators on the protected base branch before relying on the check as a merge gate.

## Identity and branch matching

An active agent has one or more GitHub identities, each containing `account_type` (`user` or `bot`), the current `login`, and immutable numeric `github_user_id`. Matching uses account type and numeric user ID, not the mutable login. A GitHub App bot uses its bot account ID (often displayed with a `[bot]` suffix), never the App installation ID. An OAuth-backed platform that opens PRs as a human is represented by that human GitHub account. If several platforms share that account, distinct branch prefixes such as `claude/` and `codex/` disambiguate them.

Only `active` entries can identify authors or authorize tools. `suspended` entries preserve identity metadata but are denied. `unconfigured` entries omit identities and deny all tools. Branch prefixes are reserved across every status so a suspended or not-yet-configured namespace cannot be reassigned accidentally. Prefixes are case-sensitive, include the trailing slash, and may not be duplicated or overlap (`agent/` and `agent/review/` are conflicting assignments).

An unknown identity is rejected. A shared active identity requires a branch prefix that selects exactly one matching agent. If a configured prefix identifies a different agent than the PR author identity, the check rejects the PR rather than guessing. A unique identity can identify its agent without a prefix unless another configured prefix contradicts it. Commit-message `Co-Authored-By` trailers are not authorization evidence.

## Runtime tool allowlist

Each agent has a required `allowed_tools` array; an empty array means deny all. Grants are exact namespaced strings: `mcp:collaborate_claim`, `mcp:collaborate_renew`, `mcp:collaborate_release`, `mcp:collaborate_say`, `mcp:collaborate_inbox`, or `mcp:collaborate_receipt` for MCP calls, and `adapter:<provider-name>` for third-party WASI provider adapters. The runtime resolves an active `agent_id` and checks the exact tool string immediately before exposing or executing it. Missing entries, suspended/unconfigured entries, and unlisted tools are denied.

To activate a platform, verify the actual author account shown by GitHub, set `status` to `active`, add its user/bot identity and numeric `github_user_id`, and grant only the tools it needs. For example, an MCP-only entry can grant `mcp:collaborate_claim` and `mcp:collaborate_say` without allowing receipt access or any provider adapter. Validate edits with `go test ./...` and the `agent-registry` Actions check. Registry changes themselves should be protected by the repository's normal review and ruleset controls.

## Pull-request enforcement

`.github/workflows/agent-registry.yml` runs on `pull_request_target` and checks out only the event's base commit with checkout credentials disabled. It does not check out or execute PR code. The job token is limited to read access to repository contents and pull requests. The Go checker validates registry consistency, verifies the PR author and branch against the base registry, then reads changed paths for the PR and other open PRs to the same base branch whose author/branch also resolves to an active registry entry. Unregistered open PRs do not create authoritative claims. Exact file conflicts and ancestor/descendant path conflicts are reported and fail the `agent-registry` status check. A missing/invalid registry, API failure, unrecognized author, or ambiguous current-PR identity also fails closed.

This check detects overlap among open PRs visible to the token at the time of the run; it is not an atomic reservation or a replacement for branch protection, independent review, or merge-queue serialization. The live GitHub ruleset currently requires `go-verify`; adding `agent-registry` to the live ruleset is a separate repository-settings change and should only be done after the registry has active identities and the check is enabled on the protected base branch.
