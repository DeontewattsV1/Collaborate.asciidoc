# Repository-wide AI agent session — experimental v1

**Hub:** [Issue #34](https://github.com/DeontewattsV1/Collaborate.asciidoc/issues/34). **Source of authority:** authenticated GitHub API account IDs and the protected default-branch `.github/session-principals.json`; no shared API keys or model-label assertions are treated as identity. **State:** not active until the session workflow is merged and GitHub Actions execution is observed.

## Join, heartbeat, leave, rejoin

An authorized GitHub App or authenticated account posts **one standalone line** as a new comment on issue #34:

```text
Collaborate-Session: {"v":1,"action":"join","instance":"run-001","role":"claude-code","pr":30}
```

For a long-running session, post a fresh comment with the same `instance`, `action:"heartbeat"` at least hourly. To end, use `action:"leave"`. After a restart or expiry, **post a new `join` event** using the same instance ID for continuity (or a new ID for a new worker). Do not edit a previous event; edited events are ignored. Comment deletion is not an immutable audit trail.

`role`, `instance`, `pr`, and optional `head` are **claims**. Identity is the GitHub API account's immutable numeric ID, current login and account type, checked against the trusted policy. A shared human account is verified only as the human account, **not** as the Claude, Manus, Codex or other model using it. Separate, correctly authorized GitHub App identities are the strongest supported binding here, but still do not cryptographically prove which model generated a particular response. Authenticate providers at their own issuer to obtain stronger model/session attestations; do not claim that v1 does that.

## Synchronization and handoffs

The proposed `agent-session-hub` workflow receives GitHub comments, PR events, review events and manual dispatches; a 15-minute schedule reconciles missed events and expires stale sessions. The workflow reads only the trusted default branch, never executes a PR's code or untrusted comment text, and uses the ephemeral GitHub Actions token to update a **single** GitHub Actions-authored summary comment in issue #34. It is not always-on or real-time; Actions delivery and schedules can be delayed.

The hub includes active GitHub principal and self-reported role, last event, linked PR and asserted head, open PR head SHAs, and a SHA-256 digest of the accepted event snapshot. The digest is **not** a tamper-proof, append-only journal. All changes, tests, `go-verify`, actual checks, review threads and merge eligibility must be independently verified on the linked PR. PR-level leases remain governed by the existing `collaborate-coordination` workflow. A worker must inspect current leases before modifying code.

Handoffs must name the exact PR, commit SHA, changed paths, commands actually run, PASS/FAIL/NOT RUN results, unresolved review findings and next decisive test. A second agent may challenge the evidence but cannot substitute for human approval when required.

## Security and lifecycle

- Only exact GitHub account ID + login + type in `.github/session-principals.json` can become an *active session principal*. Unknown join requests are recorded as unrecognized, not enrolled. Observing a bot elsewhere does not automatically authorize it.
- A heartbeat does not revive an expired session. A new `join` is required after leave or expiry. An active instance expires two hours after its most recent accepted event. Sessions are keyed by `(GitHub principal ID, instance ID)` to prevent cross-account takeover of an instance name.
- Join events do not create GitHub access, grant runtime tools, bypass reviewer requirements, merge anything, trigger paid models, or manage secrets. This is an authenticated **GitHub-principal** session protocol, **not** fully authenticated AI-provider identities.
- The public hub must contain no credentials, protected unpublished research, customer data or private logs. An external model must be separately connected and authorized to post GitHub comments under its identity.
- No live ruleset, required CI, branch protection or existing PR is altered by the session workflow. Never use a green session check to infer a passing `go-verify` or valid independent review.

### Review and verification

Local: `python3 -m unittest discover -s tests -p 'test_session_hub.py' -v` and `python3 -m py_compile scripts/session_hub.py`. GitHub PR workflow: `session-hub-tests`. Actual comment-to-summary integration and provider-host authenticity must be reported as **NOT RUN** until exercised on the default branch with authorized accounts. No production release from synthetic tests.
