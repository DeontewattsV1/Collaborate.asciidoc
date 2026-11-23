# Claude Code as one optional collaboration participant

**Current workflow:** `.github/workflows/agent-collaboration.yml` (proposed in PR #35). This provider-neutral workflow coordinates authorized GitHub accounts through [Agent Session Hub #34](https://github.com/DeontewattsV1/Collaborate.asciidoc/issues/34). It **does not invoke Claude or any other external model**, and it does not require an Anthropic API key.

The previous `.github/workflows/claude-collaboration.yml` was specific to Anthropic's `claude-code-action`, required `ANTHROPIC_API_KEY`, and only accepted the repository owner's `@claude` comments. It has been removed from the proposed branch in favor of a single common coordination entry point. Until the new PR is merged, GitHub continues running the workflow currently on the default branch.

## Claude Code session participation

1. Install/authorize the official Claude GitHub App through your provider's supported authentication flow, with repository permissions you approve. It must act through a distinct identifiable GitHub account, such as `claude[bot]`, for its account identity to be verified independently of the repository owner's account.
2. Confirm the GitHub account ID, login and type against the protected-base `.github/session-principals.json` allowlist. Only the account principal is authenticated by the GitHub API. The `role` label is self-reported and is **not** a cryptographic model identity claim.
3. Post a standalone `Collaborate-Session:` join event on issue #34 as described in [AGENT-SESSION.md](AGENT-SESSION.md). Send heartbeats at least hourly, `leave` on shutdown, and a new `join` after restarting.
4. Read all open PR leases, review comments and required CI results before editing a path. Use a draft PR and `Collaborate-Claim:` entries as directed in [GITHUB-AGENTS.md](GITHUB-AGENTS.md). Leave review artifacts with exact commit SHA and PASS, FAIL or NOT RUN evidence.

## Optional external model execution

Coordinating an already connected agent does not start an Anthropic runtime. To **invoke** Claude Code via GitHub Actions, an administrator must separately set up the official Anthropic integration and provide an accepted credential securely (for example an Actions secret `ANTHROPIC_API_KEY`), review its GitHub token permissions, and authorize costs and execution scope. Other models similarly require their own separately authorized GitHub App, token, or provider adapter. No agent may borrow an unrelated model's credential or impersonate its identity.

The current neutral workflow does not dispatch or pay for such sessions. When a model provider runs elsewhere and posts an authorized GitHub comment, the hub reconciles that principal's participation. It is an asynchronous GitHub state exchange, **not** a real-time distributed execution engine or an authenticated proof of which model generated the comment.

## Security and review

Never send secrets, private code or identity proofs in public GitHub comments. Do not merge, modify live rulesets, enable bypasses or weaken `go-verify` based on an agent's unverified claim. A successful hub workflow confirms only the GitHub event-processing check; model-host provenance, independent review and production-readiness require separate evidence.
