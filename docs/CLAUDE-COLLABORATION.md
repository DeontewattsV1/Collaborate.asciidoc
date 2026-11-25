# Claude Code collaboration runbook

**Status:** Proposed workflow; not active until merged to the default branch, authenticated, and exercised. A workflow file is not proof that the Claude GitHub App or API authentication is available.

## Owner-controlled activation

1. Review and merge the workflow PR only through the live required gates. Do not relax `go-verify`, coordination, or repository rules.
2. Install/authorize the official [Claude GitHub App](https://github.com/apps/claude) for this repository if not already installed. Claude Code on the web and Claude Code GitHub Actions are separate entry points; one being available does not prove the other is set up.
3. Set the repository Actions secret `ANTHROPIC_API_KEY` from the Anthropic Console under GitHub **Settings → Secrets and variables → Actions**. Never paste it into issues, chat, PRs, or committed files. Anthropic also documents OAuth-token and workload-federation alternatives; the proposed workflow specifically uses the API-key route. API use may incur provider charges.
4. On an issue or PR *conversation comment* from the repository owner, mention `@claude` and give a bounded request, target paths, acceptance criteria, and tests. The workflow is deliberately owner-triggered; external comments and other bots do not start it. It also refuses fork PRs.
5. Check the `claude-code-collaboration` Actions run, the resulting branch/PR comments and commits, and the independent `go-verify` and `coordinate` checks. A run that was skipped or failed is not a success. Do not merge on the basis of AI self-review.

## Suggested division of responsibility

- **Claude Code:** implement a bounded change, follow `AGENTS.md`/`CLAUDE.md`, and report evidence on its own draft PR.
- **ChatGPT/Codex:** independently examine the changed files and review test results, propose adversarial cases, and report discrepancies without claiming independent human approval.
- **Human maintainer:** authorize scope and expenditures, provide credentials only through platform secrets, review changes, and decide whether to merge.

## Reusable task handoff

~~~text
Objective: [one falsifiable task]
Scope: [exact files and excluded files]
Collaborate-Claim: [path]
Acceptance tests: [exact commands or reproducible manual steps]
Security invariant: [what must never regress]
Evidence: [test logs, PR/commit links; NOT RUN where unavailable]
Review request: [what the second agent should try to falsify]
Human approval needed for: [any merge, secret, deployment or ruleset change]
~~~

## Initial suggested pilot

Ask Claude to review the existing path-claim checker **without changing its behavior** and identify one reproducible counterexample, or explicitly report none found within the tested cases. Review `internal/ghcoord/`, `docs/GITHUB-AGENTS.md`, and the live workflow as read-only inputs. Request a new scoped PR with a regression test only if a concrete bug is found. This is an **unrun proposal**, not an external Claude assessment.

## Security limitations

GitHub `contents: write`, `issues: write`, `pull-requests: write`, and `id-token: write` apply to the action job because it needs to collaborate in issues and PRs. Only the repository owner can trigger this workflow. The owner-trigger filter reduces accidental/public activation but does not sandbox the model, and GitHub issue/PR text can still contain prompt injection. The provider action's own access checks, branch protection, separate CI, and human review remain essential.

Official reference: https://github.com/anthropics/claude-code-action/tree/main/docs
