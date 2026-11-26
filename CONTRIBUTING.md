# Contributing to Collaborate.asciidoc

Thank you for contributing. Whether you are a human contributor or an AI assistant operating with maintainer authorization, all changes follow the same branch, coordination, verification, and review rules.

Read [`AGENTS.md`](AGENTS.md) first. It is the single source of truth for repository rules, required checks, and the collaboration contract.

## Before you edit

1. **Check open pull requests and path leases.** Inspect open PRs and their `Collaborate coordination` sticky comments. Do not edit paths already claimed by another open PR unless you coordinate a handoff first.
2. **Understand the active pilots.** `collaborate` (`cmd/collaborate`) and `collab` (`cmd/collab`) are separate pilots with separate schemas, token models, and security guarantees. `agentterm` (`cmd/agentterm`) is the shared verifiable terminal. See [`README.md`](README.md) and [`docs/SECURITY.md`](docs/SECURITY.md).
3. **Never commit secrets or local state.** Do not commit `.collab/`, `.agentterm/`, API keys, tokens, private SSH keys, or host-specific credentials.

## Branching and identity

- Always branch from up-to-date `origin/main`. Never push directly to `main`, another agent's branch, or a release branch.
- If you are an AI agent, use your registered branch prefix from [`.github/agent-registry.json`](.github/agent-registry.json) (`claude/`, `codex/`, `gemini/`, `manus/`, `tay/`). Unregistered identities or mismatched prefixes fail the required `agent-registry` check. See [`docs/AGENT-REGISTRY.md`](docs/AGENT-REGISTRY.md).

## Pull request workflow

1. **Open a draft PR early** when starting non-trivial work so other collaborators can see the active scope.
2. **Declare claimed paths** in the PR description with one `Collaborate-Claim:` line per path or directory:

   ```text
   Collaborate-Claim: docs/
   Collaborate-Claim: path/to/file.go
   ```

   Keep changes small, scoped, and reversible.
3. **Use the PR template** ([`.github/PULL_REQUEST_TEMPLATE.md`](.github/PULL_REQUEST_TEMPLATE.md)) and separate **PASS**, **FAIL**, and **NOT RUN** evidence honestly. Never claim a test, external model call, or live sandbox check ran unless you ran it and can cite its output.
4. **Never approve or merge your own PR.** Independent human maintainer approval is required on `main`. A second AI agent's comment or review is not independent human approval.

## Verification

Run the single verification script before pushing:

```sh
scripts/check.sh           # full suite, exact match for required go-verify CI
scripts/check.sh --quick   # fast iteration, skips -race
```

Requirements:

- Go at the version in `go.mod`, with `CGO_ENABLED=1` and `GOTOOLCHAIN=auto`
- Git and a C compiler
- `libsqlite3-dev` on Debian or Ubuntu for `cmd/collab`
- Python 3.10+ for `repo_hygiene.py`, `validate_ruleset.py`, and the Python tests
- Optional: `actionlint` for workflow validation

### Required checks on `main`

- **`go-verify`** (`.github/workflows/ci.yml`) runs `scripts/check.sh`.
- **`agent-registry`** (`.github/workflows/agent-registry.yml`) checks the PR author and branch prefix against `.github/agent-registry.json` on `main`, and detects overlapping paths across active PRs.
- **`coordinate`** (`.github/workflows/collaborate-coordination.yml`) evaluates `Collaborate-Claim:` leases, file overlaps, and review independence.

## Reporting issues and accessibility barriers

- Use the forms in [`.github/ISSUE_TEMPLATE/`](.github/ISSUE_TEMPLATE/) for bug reports, feature requests, and accessibility barriers.
- For security vulnerabilities, **do not** open a public issue. Follow [`SECURITY.md`](SECURITY.md).
- For usage questions and setup help, see [`SUPPORT.md`](SUPPORT.md).

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
