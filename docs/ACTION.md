# Collaborate Check: the reusable GitHub Action

> **Status: published.** `v1.0.0` is a published GitHub release (tag
> `v1.0.0` = commit `0348acdda4c98d3892dd6b79467a240c4daeb0cd`) and is listed on the
> [GitHub Marketplace](https://github.com/marketplace/actions/collaborate-check).
> Pin the action by that **full commit SHA** with the tag as a comment, never
> by a moving tag or branch. The published release has no binary assets yet,
> so use the default `mode: source` (the release-built archives are still on
> a draft release).
> You can also copy [`collaborate-coordination.yml`](../.github/workflows/collaborate-coordination.yml),
> which runs the same checker and works today.

Collaborate Check packages the existing Go coordination checker
([`internal/ghcoord`](../internal/ghcoord), standalone binary
[`cmd/collaborate-check`](../cmd/collaborate-check)) as a composite action. Each
open pull request is a *lease* over the paths it changes plus any
`Collaborate-Claim: PATH` lines in its description. When two open PRs against the
same base overlap, the one opened first holds the lease and the later one is
`BLOCKED`. Approvals count only on the current head and only from an identity
other than the author. A human approval is reported separately from an agent
approval. See [`GITHUB-AGENTS.md`](GITHUB-AGENTS.md) for the model.

## Install

1. Add `.github/collaborate-agents.json` to your default branch (start from
   [`examples/github-action/collaborate-agents.json`](../examples/github-action/collaborate-agents.json)).
   This step is optional: without the file, the check still runs and agents show as unattributed.
2. Add the workflow from [`examples/github-action/coordinate.yml`](../examples/github-action/coordinate.yml):

```yaml
name: Collaborate Check
on:
  pull_request_target:
    types: [opened, synchronize, reopened, edited, ready_for_review, converted_to_draft, closed]
  pull_request_review:
    types: [submitted, edited, dismissed]
permissions:
  contents: read
  pull-requests: write
jobs:
  coordinate:
    runs-on: ubuntu-latest
    steps:
      # v1.0.0, pinned by full commit SHA.
      - uses: DeontewattsV1/Collaborate.asciidoc@0348acdda4c98d3892dd6b79467a240c4daeb0cd # v1.0.0
```

`pull_request_target` lets the action comment on fork PRs. That's safe here
because the action never checks out or executes pull request code; see the
[security model](#security-model). If you'd rather not use
`pull_request_target`, run it on `pull_request`. It works with a read-only
token, and the comment is skipped with a warning while the job summary still
shows the result.

## Outcomes and exit codes

| Outcome | Exit | Meaning |
|---|---|---|
| `PASS` | 0 | A complete scan found no blocking coordination issue |
| `BLOCKED` | 1 | A confirmed overlap with an earlier open PR, or `enforce_independent_review` is on and there is no independent approval on the current head (or changes were requested) |
| `ERROR` | 2 | Invalid input, configuration, permission, or API failure |
| `INDETERMINATE` | 3 | The scan was incomplete: more open PRs than `max-open`, a truncated listing, the event's head SHA is no longer the PR head, or the PR changed during the scan |

`INDETERMINATE` is **never** reported as `PASS`. With
`fail-on-indeterminate: "false"` it exits 0 with a warning, and the report still
says `INDETERMINATE`. A closed or merged PR no longer holds a lease, so its own
run passes, and with `reconcile` the action re-checks every open PR that
overlapped it, refreshing their comments and statuses.

## Inputs

| Input | Default | Description |
|---|---|---|
| `github-token` | `${{ github.token }}` | API token. Needs `contents: read` and `pull-requests: read`, plus `pull-requests: write` to comment and `statuses: write` for `set-status` |
| `repository` | `${{ github.repository }}` | `OWNER/NAME` |
| `pr-number` | triggering PR | PR to check |
| `registry` | `.github/collaborate-agents.json` | Registry path in the repository |
| `registry-ref` | default branch | Ref the registry is read from (through the API, never from the workspace) |
| `registry-required` | `false` | Fail if the registry file is missing |
| `comment` | `true` | Create or update one sticky comment |
| `summary` | `true` | Write the report to the job summary |
| `set-status` | `false` | Set the `collaborate/coordination` commit status on the checked head SHA |
| `reconcile` | `true` | Re-check open PRs that overlap this one |
| `fail-on-indeterminate` | `true` | Fail on an incomplete scan |
| `max-open` | `100` | Other open PRs to compare (1–1000) |
| `checker-source` | `source` | `source` builds from the action's pinned source; `release` downloads a release binary |
| `version` | | Release tag for `release` mode, e.g. `v1.0.0` |
| `checksum` | | Expected SHA-256 of the release archive for your runner |
| `verify-attestation` | `false` | Verify build provenance with `gh attestation verify` (`release` mode) |
| `release-repository` | `DeontewattsV1/Collaborate.asciidoc` | Where releases are published |

## Outputs

| Output | Description |
|---|---|
| `outcome` | `PASS`, `BLOCKED`, `INDETERMINATE`, or `ERROR` |
| `exit-code` | `0`, `1`, `2`, or `3` |
| `blocking-conflicts` | Number of earlier PRs this one must yield to |
| `head-sha` | Head commit the verdict applies to |
| `report` | Path to the JSON report |

The action step fails whenever the outcome isn't a pass. To read the outputs
after a failure, set `continue-on-error: true` on the step and check
`steps.<id>.outputs.outcome`.

## Merge enforcement against the current head

A workflow job result shows up on the PR, but a later event (such as another PR
closing) can't change an earlier job's result. For enforcement that stays
correct, use `set-status: "true"` with `permissions: statuses: write`, and make
the `collaborate/coordination` commit status a required check in your ruleset.
The status is always written to the PR's **current head SHA** as GitHub reports
it at the end of the scan, and reconciliation updates the statuses of
overlapping PRs when a lease is released. A merged or closed PR gets no new status.

## JSON report (schema `collaborate.check/v1`)

`collaborate-check --json` (and `collaborate github-check --json`) prints a
deterministic document: the field order is fixed, lists are sorted, and empty
lists are `[]`. The top-level fields are:

```json
{
  "schema_version": "collaborate.check/v1",
  "outcome": "BLOCKED",
  "repository": "OWNER/NAME",
  "head_sha": "…40 hex…",
  "pr_state": "open",
  "lease_held": true,
  "coverage": { "complete": true, "other_open_compared": 3, "other_open_listed": 3, "gaps": [] },
  "pr": { "number": 27, "files": ["…"], "claims": ["…"], "attribution": { "…": "…" } },
  "open": [],
  "conflicts": [{ "other": { "number": 24 }, "paths": ["src/auth/session.ts"], "holder": 24, "blocking": true }],
  "reviews": [],
  "independent_approval": false,
  "human_approval": false,
  "changes_requested": false,
  "enforce_independent_review": false,
  "notes": [],
  "blocking_conflict_count": 1,
  "reconciled": [{ "number": 31, "head_sha": "…", "outcome": "PASS", "comment_updated": true, "status_set": false }]
}
```

`coverage.gaps` uses stable codes: `open_pr_limit`,
`open_pr_listing_truncated`, `files_truncated:#N`, `reviews_truncated`,
`stale_event_head`, and `pr_changed_during_scan`. On an error the document is
`{"schema_version", "outcome": "ERROR", "error"}`. Fields may be added within
`v1`. Removing or renaming a field requires a new schema version.

## Security model

- **No pull request code runs.** The action has no checkout step. The checker
  reads PR metadata (files, commits, reviews) through the REST API only.
- **A trusted checker.** In `source` mode (the default), the checker is built
  from the action's own directory, which is the commit you pinned after `@`. It
  depends only on the Go standard library, uses `cache: false` so nothing can
  poison it, and builds with `-mod=readonly`. In `release` mode the archive is
  downloaded over HTTPS and checked against the release's
  `checksums.txt`. You must also pin its SHA-256 (`checksum`) or verify its
  build provenance (`verify-attestation: true`), or the action refuses to run.
- **A trusted registry.** The agent registry is fetched through the API from
  the default branch (or `registry-ref`), never from the workspace or the PR head.
- **No script injection.** Inputs reach shell steps only through environment
  variables and are validated (PR number, `OWNER/NAME`, refs, paths, booleans,
  ranges) before use. [`tests/test_action.py`](../tests/test_action.py) checks
  that no `${{ }}` expression appears inside any shell script.
- **Least privilege.** On a read-only token (a `pull_request` run from a fork) the
  comment and status are skipped with a warning and nothing else changes. The
  API URL must be HTTPS, and the token is never sent to another host:
  pagination links outside the API base are refused.
- **Pin by full SHA.** Tags can move. Production workflows should pin the
  reviewed full commit SHA, with the version in a trailing comment.

## Releases (maintainers)

[`.github/workflows/release.yml`](../.github/workflows/release.yml) runs only
when a maintainer pushes a `vX.Y.Z` tag. Running it by hand only builds and
checksums. It tests the checker, builds `collaborate-check` for
linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, and windows/amd64 with
`CGO_ENABLED=0 -trimpath` and normalized archive metadata, writes
`collaborate-check_<version>_checksums.txt`, attests build provenance, and
creates a **draft** release. Nothing is published automatically.

To cut the first release:

1. Merge to `main` and wait for `independent-ci` and `action-integration-test` to pass.
2. `git tag -s v1.0.0 <reviewed-sha> && git push origin v1.0.0` (maintainer only).
3. Review the draft release and the checksums in the run summary, then publish it.
4. Optionally move a `v1` major tag to the same commit for convenience. Pinning
   by SHA is still recommended.
5. Marketplace: on the published release, tick **Publish this Action to the
   GitHub Marketplace**. This needs the repository's `action.yml` at the root
   (it is) and acceptance of the Marketplace developer agreement. It is a manual
   step for the repository owner.

## Local use

```sh
go build -o collaborate-check ./cmd/collaborate-check
GITHUB_TOKEN=$(gh auth token) ./collaborate-check --repo OWNER/NAME --pr 27 \
  --registry .github/collaborate-agents.json --registry-ref main --json
echo "exit $?"   # 0 PASS, 1 BLOCKED, 2 ERROR, 3 INDETERMINATE
```
