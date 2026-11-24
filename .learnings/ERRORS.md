# Errors

Command, CI and workflow failures, with root cause and fix. Format: `.claude/skills/self-improvement/SKILL.md`.

---

## [ERR-20261124-001] pasted_command_became_a_file

**Logged**: 2026-11-24T21:00:00Z · **Priority**: high · **Status**: promoted · **Area**: config

### Summary
Shell commands pasted into GitHub's web "Add file" box created files whose *names* are the commands. The commands themselves never ran.

### Error
```
go run ./cmd/agentterm serve --registry .github/agent-registry.json   (a file)
gh auth login bash scripts/setup_github_environments.sh               (a file)
bash scripts/setup_github_environments.sh                             (a file, plus a copy under .github/workflows/)
```

### Context
The commands came from README and docs code blocks and were meant to run in a terminal. Because the setup script never ran, the GitHub Environments and API keys were never created, and the live sandbox and model workflows had nothing to use.

### Suggested Fix
Run commands in a terminal: Codespaces, or a local shell after `gh auth login`. Agents should flag any new file whose name looks like a command.

### Metadata
- Reproducible: yes · Related Files: docs/LIVE-SETUP.md, scripts/setup_github_environments.sh
- Pattern-Key: ops.command_pasted_as_file · Recurrence-Count: 3 · First-Seen: 2026-11-23 · Last-Seen: 2026-11-23
- **Promoted**: AGENTS.md

### Resolution
- **Resolved**: 2026-11-24 · **Commit/PR**: #67, #73 · **Notes**: All stray files removed. The setup script still has to run in a real terminal.
- **Systemic fix**: 2026-11-24 · **Commit/PR**: #74 · **Notes**: `scripts/repo_hygiene.py` now runs inside the required `go-verify` check and fails on any tracked file whose name is a shell command. Replayed against `main@79cb659` and `main@ea6bcd8`, it flags every one of these files.

---

## [ERR-20261121-001] agent-registry workflow: directory not found

**Logged**: 2026-11-24T21:00:00Z · **Priority**: high · **Status**: resolved · **Area**: infra

### Summary
The `pull_request_target` check checked out `github.event.pull_request.base.sha`. That is the base the PR was opened against, so it can predate the checker. The run then failed with "directory not found".

### Error
```
stat ./cmd/agent-registry-check: directory not found
```

### Context
This hit PRs opened before the checker merged. Revert `5bf7b37` (#69) put the bug back on 2026-11-23.

### Suggested Fix
In a privileged workflow, check out `${{ github.sha }}` (the tip of the trusted base). Never check out the PR head. `scripts/test_workflow_trust.py` enforces this.

### Metadata
- Reproducible: yes · Related Files: .github/workflows/agent-registry.yml, scripts/test_workflow_trust.py
- See Also: ERR-20261124-002

### Resolution
- **Resolved**: 2026-11-24 · **Commit/PR**: #33, restored in #73 (aa3f0ac)

---

## [ERR-20261124-002] agent-registry: every agent PR rejected after a revert

**Logged**: 2026-11-24T21:00:00Z · **Priority**: critical · **Status**: resolved · **Area**: config

### Summary
Revert `5bf7b37` (#69, *Revert "Add to main"*) reset every agent in `.github/agent-registry.json` to `unconfigured`. The required `agent-registry` check then failed on every agent PR.

### Error
```
agent-registry: PR #73 author/branch is not authorized: GitHub author "DeontewattsV1" (ID 284501602) is not an active registered agent
```

### Context
The same revert also removed 20 files, about 1,200 lines in total. Among them were the session hub, the registry hardening and its tests. The check reads the registry from trusted `main`, never from the PR, so the PR that fixes the registry cannot pass the check itself. Merging it needed the maintainer.

### Suggested Fix
Before merging any PR whose title starts with "Revert", read its diff stat. Treat a large revert of security or policy files as a design decision. Do not merge it as a cleanup.

### Metadata
- Reproducible: yes · Related Files: .github/agent-registry.json, cmd/agent-registry-check/main_test.go
- Pattern-Key: ops.unreviewed_revert · Recurrence-Count: 1 · First-Seen: 2026-11-23

### Resolution
- **Resolved**: 2026-11-24 · **Commit/PR**: #73 (aa3f0ac) · **Notes**: The registry, checker and tests were restored from `5bf7b37^`. Guard: `TestCheckedInRegistryBootstrap` (in `go-verify`) fails if the registry is reset again, and `scripts/test_workflow_trust.py` fails if the checkout ref regresses. Restoring the session hub is still pending a maintainer decision (FEAT-20261124-002).

---

## [ERR-20261123-001] sandbox-launch-action.yml failed on every push

**Logged**: 2026-11-24T21:00:00Z · **Priority**: medium · **Status**: resolved · **Area**: infra

### Summary
A GitHub Marketplace "usage" snippet was committed as a workflow file. It has no `on:` and no `jobs:`, so it failed instantly on every push.

### Suggested Fix
Validate workflow files with `actionlint` before committing them. The snippet's action was already wired correctly in `sandbox-launch.yml`.

### Metadata
- Related Files: .github/workflows/sandbox-launch.yml · Pattern-Key: ops.template_committed_unadapted · See Also: ERR-20261124-003

### Resolution
- **Resolved**: 2026-11-23 · **Commit/PR**: #64
- **Systemic fix**: 2026-11-24 · **Commit/PR**: #74 · **Notes**: `scripts/repo_hygiene.py`, which runs in `go-verify`, requires every file in `.github/workflows/` to be a `.yml`/`.yaml` workflow with top-level `on:` and `jobs:`, and a Docker build of `.` to have a `Dockerfile`.

---

## [ERR-20261124-003] Docker workflow failed: no Dockerfile

**Logged**: 2026-11-24T21:00:00Z · **Priority**: medium · **Status**: resolved · **Area**: infra

### Summary
GitHub's Docker starter workflow was added with nothing to build, so it failed on every push and every nightly run.

### Suggested Fix
A template workflow needs the artifact it expects: a Dockerfile, a site, a package. Build it locally before merging.

### Metadata
- Related Files: Dockerfile, .github/workflows/docker-publish.yml · Pattern-Key: ops.template_committed_unadapted · Recurrence-Count: 2 · See Also: ERR-20261123-001

### Resolution
- **Resolved**: 2026-11-24 · **Commit/PR**: #73 · **Notes**: A multi-stage distroless image of `cmd/agentterm` was added. The image was built and run before merge.
- **Systemic fix**: 2026-11-24 · **Commit/PR**: #74 · **Notes**: `scripts/repo_hygiene.py`, which runs in `go-verify`, requires every file in `.github/workflows/` to be a `.yml`/`.yaml` workflow with top-level `on:` and `jobs:`, and a Docker build of `.` to have a `Dockerfile`.

---

## [ERR-20261124-004] Docker image: nonroot user cannot write /data

**Logged**: 2026-11-24T21:00:00Z · **Priority**: medium · **Status**: resolved · **Area**: infra

### Error
```
agentterm: open /data/server.key: permission denied
```

### Context
`USER nonroot` together with `VOLUME /data` created the volume owned by root. This was found only by actually running the built image.

### Suggested Fix
Create the directory in the build stage, then `COPY --chown=65532:65532` it into the distroless stage. Always `docker run` an image before shipping it.

### Resolution
- **Resolved**: 2026-11-24 · **Commit/PR**: #73

---

## [ERR-20261123-002] agentterm exec: wrong Go toolchain

**Logged**: 2026-11-24T21:00:00Z · **Priority**: medium · **Status**: resolved · **Area**: backend

### Error
```
go: go.mod requires go >= 1.26.0 (running go 1.24.7; GOTOOLCHAIN=local)
```

### Context
The scrubbed exec environment hard-coded `GOTOOLCHAIN=local` and `CGO_ENABLED=0`. That blocked Go's checksum-verified toolchain selection, which is how this repository gets Go 1.26.

### Suggested Fix
When scrubbing an environment, remove credentials but pass toolchain settings through unchanged.

### Metadata
- Related Files: internal/agentterm/exec.go

### Resolution
- **Resolved**: 2026-11-23 · **Commit/PR**: #53

---

## [ERR-20261123-003] go vet copylocks in a test

**Logged**: 2026-11-24T21:00:00Z · **Priority**: low · **Status**: resolved · **Area**: tests

### Error
```
agentterm_test.go:154:12: assignment copies lock value to forged
```

### Context
Copying a struct that contains a `sync.Mutex` (`forged := *codex`) is a vet error. `go vet` had run before the test file existed. The error was caught by running vet through the agent terminal itself.

### Suggested Fix
Build a new value instead of copying one. Re-run `go vet` after adding tests, not only after adding code.

### Resolution
- **Resolved**: 2026-11-23 · **Commit/PR**: #53

---

## [ERR-20261124-005] local docker build: x509 unknown authority

**Logged**: 2026-11-24T21:00:00Z · **Priority**: low · **Status**: wont_fix · **Area**: infra

### Error
```
go: ... proxy.golang.org ... tls: failed to verify certificate: x509: certificate signed by unknown authority
```

### Context
This affects only the Claude cloud sandbox, which inspects TLS through its own proxy. GitHub runners are not affected.

### Suggested Fix
For local verification only, build a scratch copy of the Dockerfile that adds `/root/.ccr/ca-bundle.crt`, passed with `--build-context`. Never commit that change.

### Resolution
- **Notes**: Environment-specific. The committed Dockerfile is unchanged.
