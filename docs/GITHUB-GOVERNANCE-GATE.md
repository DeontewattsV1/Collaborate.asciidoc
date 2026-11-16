# Governance release gate — declaration versus live enforcement

The checked-in ruleset file is a **proposal/declaration**, not an automatic GitHub configuration. This file does not change the actual ruleset and does not demonstrate enforcement. Do not merge or label governance complete based solely on the local policy test.

## Intended changes

- Keep main-require-independent-ci active and targeting ~DEFAULT_BRANCH, with no bypass actors.
- Require at least one approving pull-request reviewer and approval from someone other than the most recent pusher; dismiss stale approvals and resolve review threads.
- Require GitHub-verified signatures for commits introduced to the protected branch.
- Preserve strict freshness and go-verify from the GitHub Actions application (integration_id: 15368).

## Administrator action (separate from this PR)

An administrator with repository rules write permission must edit live ruleset 27378635 using the authenticated GitHub UI or an independently authorized administration client. No workflow job or pull request may silently apply these settings. Before applying the signature requirement, confirm contributor signing workflows and assess existing unsigned commits to avoid stranded changes. A solo maintainer will require a second authorized reviewer.

## Evidence required to close release gate

1. Read back the live ruleset through GitHub's authenticated API; establish required_approving_review_count >= 1, require_last_push_approval: true, rule required_signatures, no bypass actors, and the unchanged trusted-source go-verify context.
2. Observe a PR to main with a passing go-verify check on the exact latest head SHA. Confirm review/signature requirements block an unsigned or unapproved PR, then confirm an independently approved and signed candidate can pass GitHub's mergeability evaluation. Do not merge as part of the test.
3. Archive the ruleset JSON, commit SHA, PR URL, relevant check-run IDs and test outcomes, with independent readback. Do not treat static policy validation as proof of remote enforcement.

The Codex/Claude and independent HTTPS checkpoint-custodian gates remain separate and are not satisfied by this PR.
