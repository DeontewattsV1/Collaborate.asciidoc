# Collaborating through GitHub with any AI platform

The broker and MCP bridge give deep coordination to hosts that can run a local
process. Many AI platforms cannot: they reach a repository only as a **GitHub
App**, an **authorized OAuth app**, or by acting under the user's own account
(for example Claude, Copilot, Codex, Cursor, Devin, Gemini/Jules, Manus, cubic,
CodeRabbit). Every one of them can push a branch, open a pull request, and
submit a review, so `collaborate github-check` uses those GitHub primitives as
the coordination protocol. No platform-specific integration is needed.

## How it maps onto the collaboration model

| Collaborate concept | GitHub-native equivalent |
|---|---|
| Agent identity | The PR author's GitHub login (verified), else the head branch prefix or a `Co-Authored-By` trailer (self-reported), matched against `.github/collaborate-agents.json` |
| Lease | The set of paths an open PR changes, plus `Collaborate-Claim: PATH` lines in its description (directories reserve everything below them) |
| Lease conflict | Another open PR against the same base whose lease overlaps hierarchically. The PR opened first holds the lease; later PRs fail the check until they narrow, coordinate, or the holder merges/closes |
| Shared context | A sticky `Collaborate coordination` comment on each PR listing every other open PR, its agent, and its paths, so any agent reading the PR sees who is working on what |
| Independent review | An `APPROVED` review on the current head commit from a login that is neither the author nor attributed to the same agent |
| Gates | Your existing required CI (`go-verify`) |
| Verdict | `PASS`, `BLOCKED`, `INDETERMINATE` (incomplete scan, stale head, PR changed mid-scan; never a pass), or `ERROR`; exit codes 0/1/3/2. See [`ACTION.md`](ACTION.md#outcomes-and-exit-codes) |
| Lease release | A closed or merged PR holds no lease; its run re-checks (`--reconcile`) every open PR that overlapped it and refreshes their comments |

## Setup

1. Edit `.github/collaborate-agents.json` on `main` to match the apps listed
   under your GitHub settings (**Applications → Installed GitHub Apps,
   Authorized GitHub Apps, Authorized OAuth Apps**). GitHub's API cannot list
   a user's authorized OAuth apps, so this file is the source of truth. For each
   platform add:
   - `logins`: the account it uses on GitHub. GitHub Apps appear as
     `<app-slug>[bot]`; check a PR, review, or comment it has made.
   - `branch_prefixes`: the prefix it gives branches when it pushes under your
     account (for example `claude/`, `codex/`, `copilot/`).
   - `trailer_emails`: the `Co-Authored-By` address it adds to commits, if any.
   - `humans`: human GitHub logins, so their PRs are labelled rather than
     reported as unattributed.
2. Merge the workflow `.github/workflows/collaborate-coordination.yml`. It runs
   from the default branch on every PR event (including `closed`, for
   reconciliation) and review. Other repositories can use the reusable action
   instead, prepared in [`action.yml`](../action.yml) and documented in
   [`ACTION.md`](ACTION.md) (not yet published).
3. Optional: set `"enforce_independent_review": true` to fail the check until an
   independent approval exists on the current head, and add the `coordinate`
   check to the `main-require-independent-ci` ruleset to make it required.

## Instructions to give each AI platform

Put this in the platform's custom instructions, or in an `AGENTS.md`,
`CLAUDE.md`, or `.github/copilot-instructions.md` file the platform reads:

> Before starting, read the "Collaborate coordination" comment on open PRs to
> see which paths other agents hold. Open a draft PR early and declare intended
> paths with `Collaborate-Claim: PATH` lines in its description. If the
> `coordinate` check reports a lease conflict, do not edit those paths:
> coordinate on the holding PR or narrow your change. Never approve a PR that
> your own platform authored.

## Trust boundaries

- The checker and registry always come from the default branch. The workflow
  uses `pull_request_target` but never checks out or runs PR code, and it only
  edits a sticky comment written by `github-actions[bot]`.
- Only GitHub-login attribution is verified by GitHub. Branch prefixes and
  commit trailers can be set by anyone who can push a branch, so they label
  work but must not be treated as authentication. Independent review compares
  reviewer logins, which GitHub does verify.
- Reviews on fork PRs receive a read-only token, so the comment is skipped
  (with a warning) and the result appears in the job summary instead.
- This is coordination, not isolation: it does not stop an agent from pushing
  overlapping changes. It makes overlaps visible and, if made a required check,
  blocks them from merging.
