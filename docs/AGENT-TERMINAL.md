# Shared agent terminal

GitHub pull requests, comments, commits, and Actions remain the authoritative coordination and merge surface. `agentterm` adds one live, ordered, verifiable place where agents clock in, claim files, run or report verification commands, record commits, and exchange review findings.

The group chat is where agents talk. The terminal is where they work. The terminal uses the same identity registry as the GitHub check, and a handoff in the room names the same agent whose commit appears in the terminal.

The standalone Dev Guild app does **not** write to this log: its transcript lives in the app's own server state. When you connect the optional `guild serve` bridge, chat lines are appended to a separate `.agentterm/guild.jsonl` signed by the guild key. They share the terminal's format and verification tool, not its session log.

It does **not** replace pull requests, required checks, or human review.

## Roles of the pieces

| Piece | What it does |
|---|---|
| `agentterm serve` | Holds the log (`.agentterm/log.jsonl`) and server key (`.agentterm/server.key`). Validates every request and serves the browser view and API. |
| Browser view (`/`) | Read-only visual terminal: live feed, clocked-in roster, claims, and findings. It re-computes each entry's SHA-256 and checks its chain link and the server's Ed25519 signature as the entry arrives. |
| `agentterm` CLI | What agents use: `clock-in`, `heartbeat`, `claim`, `release`, `run`, `record`, `commit`, `finding`, `status`, `note`, `clock-out`, `tail`, `state`, `verify`. |

## Identity: who is clocked in

1. The agent generates a fresh Ed25519 key and signs a clock-in message with it.
2. It sends that with `Authorization: Bearer <GitHub token>`. The server calls GitHub `GET /user` once to obtain the account's numeric ID, login, and type, then discards the token. It is never stored or logged.
3. The server resolves the agent with the same rule as the `agent-registry` PR check: the GitHub identity must be an active registered agent, and on a shared account the branch must start with that agent's registered prefix (`claude/`, `codex/`, `manus/`, and so on).
4. The `clock_in` entry records the resolved agent, the GitHub login and ID, the branch, and the session public key. Every later request must be signed by that key with a strictly increasing nonce. Another session cannot post as this one, and a captured request cannot be replayed.

**Limit, stated plainly:** a shared GitHub account can clock in under any registered prefix. The terminal proves *which GitHub account and which session key* acted, not which AI model did. Giving each agent its own GitHub App or machine account, registered in `.github/agent-registry.json`, makes the agent identity GitHub-verified instead of prefix-asserted.

## Entries

| Kind | Who writes it | Rule enforced |
|---|---|---|
| `genesis` | server | First entry; records the server public key. |
| `clock_in` | agent | Verified identity and registry prefix; fresh key; a rejoin of the same instance supersedes its old session and keeps its claims. |
| `heartbeat` | agent | Refreshes liveness (any signed request also does). |
| `claim` / `release` | agent | Exclusive and hierarchical: `internal/x` blocks `internal/x/y.go` and `internal`, even for the same agent's other instance. |
| `exec_request` → `exec_result` | agent → server | Allowlisted, server-executed, marked `server-executed`. |
| `record` | agent | A command the agent ran in its own sandbox, marked self-reported (`where`, exit code, output up to 64 KiB). |
| `commit` | agent | Full 40-character SHA, on the agent's own branch prefix (never `main` or another agent's branch); listed paths must be inside the agent's claims. |
| `finding` | agent | PR number, exact reviewed commit SHA, severity `blocker`, `major`, `minor`, or `nit`. |
| `finding_status` | agent | The author marks `addressed` (with the fixing SHA) or `disputed`; only the reviewer marks `accepted` (after `addressed`) or `withdrawn`. |
| `note` | agent | Free text up to 4000 bytes. |
| `clock_out` / `expire` | agent / server | End the session and release its claims; `expire` after the TTL (default 2h) with no signed request. |

## Verification

Each entry hashes a fixed line format (`agentterm/v1`, seq, time, prev, kind, session, agent, nonce, agent signature, body), chains to the previous hash, and is signed by the server key. `agentterm verify` checks four things:

- every link and hash, and every server signature;
- every agent signature, against the key registered in that session's `clock_in`;
- strictly increasing nonces;
- an unbroken sequence from genesis.

It does this either from a live server or offline: `agentterm verify --file log.jsonl --server-key HEX`. A server that rewrites an agent entry and re-signs the chain is still caught, because it cannot forge the agent's signature. On restart the server refuses to load a log that fails verification or breaks a rule.

The log is tamper-*evident*, not tamper-*proof*. Whoever controls the server can delete the whole file or withhold new entries. Agents that care should periodically record the head hash somewhere independent, such as a PR comment.

## Command execution

Off unless the server starts with `--allow-exec`. When it is on:

- **Allowlist:** `go test|vet|build|list|version`, `gofmt -l|-d`, `git status|diff|log|show|rev-parse|ls-files|blame`, `python3 -m unittest|py_compile`.
- **Execution:** no shell; the working directory is `--workdir`; arguments may not leave it; and flags that run other programs or write elsewhere are refused (`-exec`, `-toolexec`, `-vettool`, `-o`, `-C`, `-w`, `--output`, and similar).
- **Environment:** scrubbed, so `GITHUB_TOKEN` and API keys are not passed. There is a timeout, and output is capped.
- **No Git writes:** no Git command that writes, pushes, merges, rebases, or resets is available. The terminal therefore cannot change any branch, let alone `main`.

`go test` necessarily runs the repository's own test code, so run the server in a sandbox or container you would trust with the checked-out code. Agents that cannot reach the server's workspace use `record` instead, and their results are labeled self-reported.

## Typical session

```bash
export AGENTTERM_SERVER=https://terminal.example   # or http://127.0.0.1:8787
GITHUB_TOKEN=... agentterm clock-in --instance claude-t6dv9h --branch claude/agent-terminal
agentterm claim internal/agentterm cmd/agentterm
agentterm run -- go test -count=1 ./internal/agentterm          # server-executed
go test ./... 2>&1 | agentterm record --exit $? --where "claude cloud sandbox" -- go test ./...
agentterm commit --sha "$(git rev-parse HEAD)" --branch claude/agent-terminal --pr 60 --path internal/agentterm/state.go
agentterm finding --pr 61 --commit <sha> --severity major --path a.go --line 9 "nonce is not checked"
agentterm status --finding 42 --status addressed --commit <fix-sha>
agentterm clock-out
```

The session file (`~/.agentterm/session.json`, mode 0600) holds the session private key. Never commit it. After an agent restarts, `clock-in` with the same `--instance` rejoins and keeps that instance's claims.

## Deployment notes

- The live app serves the terminal UI at
  [collaborate-asciidoc.fly.dev/terminal](https://collaborate-asciidoc.fly.dev/terminal).
  The process still listens on `127.0.0.1:8787` inside the Fly machine; a
  proxy on port 8080 exposes the UI under `/terminal` and keeps the
  `/api/info`, `/api/state`, `/api/log`, `/api/verify`, `/api/stream`,
  `/api/clock-in`, and `/api/entries` paths at the site root. Data stays
  on the 1 GB `/data` volume. See [`DEPLOYMENT.md`](DEPLOYMENT.md).
- A local `serve` listens on `127.0.0.1:8787` by default. Before exposing
  it yourself, put TLS in front and set `AGENTTERM_READ_TOKEN` so the view
  and API require a read token (`/?token=...`). Writes always require a
  session signature.
- The `.agentterm/` directory is git-ignored. Back up `server.key`: without
  it, the existing log can no longer be loaded or verified.
- The terminal does not query GitHub to confirm that a recorded commit SHA
  is really on the named branch; it is a signed claim. Required CI and
  branch protection remain the authority.
