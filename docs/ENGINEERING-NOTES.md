# Engineering notes

Rules this project has learned the hard way. They are short on purpose: each one changes how the next change is made. Add a note only when it would have prevented a real mistake.

## Run commands in a terminal

GitHub's **Add file** box creates a file whose name is whatever you paste. A command pasted there becomes a tracked file named `bash scripts/...`, and nothing runs. Use a terminal, a Codespace, or `gh` from a shell. `scripts/repo_hygiene.py` fails the required check on any tracked path that looks like a shell command.

## Read the diff before merging a revert

A pull request titled "Revert" can restore a policy, a registry, or a security check that the project needs. Read its diff stat first. A large revert of policy files is a design decision, not cleanup.

## Privileged workflows read policy from the protected branch

A `pull_request_target` workflow must check out the trusted base commit (`${{ github.sha }}`), never the pull request head, and must set `persist-credentials: false`. A pull request cannot fix the policy that its own check reads from `main`. Say so once on the PR and leave the merge to the maintainer.

## Prove a test can fail

A test that passes after its guard is removed proves nothing. Temporarily remove the guard and confirm that at least one test fails. Build the fake input so no other safeguard can mask the guard under test. After a scripted edit, assert that the anchor matched exactly once.

## Check open pull requests before opening one

List the open PRs and the paths they hold. Continue an existing PR from your branch instead of opening a duplicate. Yield a path another PR already holds, or agree on a handoff. Never force-push to replace a branch.

## Build what a template produces

A workflow that references a Dockerfile needs a Dockerfile. A template workflow needs the artifact it expects. Run `actionlint` on workflows and `docker run` on images before merging them.

## Read back what you sent

A tool that accepts a message may store something different. Before describing an outward message as accepted, fetch it back or read the API response. Do not trust the text you sent.

## Automate a mistake the second time

A written rule does not stop a recurring mistake. When the same failure happens twice, add the smallest test or `scripts/repo_hygiene.py` rule that fails on the exact bad input, and prove it against the commit that had the bug. Keep the rule's error message specific enough to explain the fix.

## Keep one source of truth

`AGENTS.md` holds the repository rules and map. Per-agent files point to it. `scripts/check.sh` is the verification, and CI runs that same script. Read versions from `go.mod`, not from prose. When a fact changes, change it in one place.
