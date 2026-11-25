# iPhone sandbox terminal (Secure ShellFish)

Drive the repository and its agents from an iPhone. Secure ShellFish is the SSH terminal on the phone; everything runs on a remote Linux server.

```
iPhone · Secure ShellFish ──SSH (Secure Enclave key)──▶ Ubuntu 24.04 server, user "sandbox" (no sudo)
                                                         ├─ tmux: claude · codex · gemini · manus · operator
                                                         ├─ tmux: collaborate-terminal → agentterm on 127.0.0.1:8787
                                                         └─ ~/workspace/Collaborate.asciidoc → GitHub PRs and CI
```

Agent work still lands only through pull requests and the required checks. The phone is a control terminal, not a way around them.

## 1. Server (in Secure ShellFish)

Add a server and create a VM. The app can provision one on DigitalOcean or Hetzner Cloud.

- Ubuntu 24.04 LTS, 4 vCPU, 8 GB RAM, 80 GB disk. A smaller server works for trying it out.
- Authentication: a **Secure Enclave** SSH key (hardware-backed, non-exportable). Agent forwarding off.
- Leave port 8787 closed in the firewall. Only SSH (22) needs to be reachable.

Cloud servers bill while they exist, even when idle.

## 2. Admin step

Connect as the provider's admin account and paste:

```bash
curl -fsSL https://raw.githubusercontent.com/DeontewattsV1/Collaborate.asciidoc/main/scripts/sandbox/bootstrap-ubuntu.sh \
  | bash -s -- admin --key "ssh-ed25519 AAAA...your-key... ShellFish@iPhone"
```

Paste in the public key that Secure ShellFish shows under **Keys**. Use the key line that begins `ssh-ed25519`, and keep it in quotes. That key is the public half only; the private half stays in the iPhone's Secure Enclave. `--key` can be repeated, and re-running it never adds a second copy. Anything that is not a valid public key line is refused before the server is touched. You can leave out `--key`; the step then reuses the admin account's `authorized_keys`.

This step:

- installs the toolchain (git, gh, tmux, Go, Python, SQLite headers, and so on);
- creates the **`sandbox`** user without sudo;
- installs your iPhone key in `/home/sandbox/.ssh/authorized_keys` with mode `600`.

It is safe to re-run.

## 3. User step

In Secure ShellFish, change the server's username to `sandbox`, reconnect, and paste:

```bash
curl -fsSL https://raw.githubusercontent.com/DeontewattsV1/Collaborate.asciidoc/main/scripts/sandbox/bootstrap-ubuntu.sh | bash -s -- user --port 8080
source ~/.bashrc
```

This step:

1. Clones the repository to `~/workspace/Collaborate.asciidoc`.
2. Installs a marked block in `~/.bashrc` that is replaced, never duplicated, on re-run.
3. Builds `agentterm`, `collaborate` and `collab`. With `GOTOOLCHAIN=auto`, the Go version required by `go.mod` is fetched automatically.
4. Runs `scripts/check.sh --quick`.
5. Starts the agent terminal in tmux, on loopback, with command execution off.

Options:

- `--port N` sets the agent terminal's port on 127.0.0.1. The default is 8787; use the port your SSH client forwards, such as 8080 below.
- `--no-check` skips the check.
- `--no-serve` doesn't start the agent terminal.

Then sign in to GitHub with a **fine-grained token limited to this repository**:

| Permission | Access |
|---|---|
| Contents, Pull requests | Read and write |
| Actions, Checks, Metadata | Read |
| Administration, Secrets | None |

```bash
gh auth login --hostname github.com --git-protocol https
```

## 4. Snippets to save in Secure ShellFish

| Snippet | Command |
|---|---|
| Repository status | `collab-status` |
| Full check (same as required CI) | `collab-check` |
| Quick check | `collab-quick` |
| Fetch latest | `collab-sync` |
| Open PRs | `collab-prs` |
| Recent Actions runs | `collab-runs` |
| Agent session (create or reattach) | `collab-agent claude` (or `codex`, `gemini`, `manus`, `operator`) |
| Shared agent terminal | `collab-terminal` |
| Verify the agent terminal's signed log | `agentterm verify` |
| List sessions | `tmux ls` |

**Copy commands from this page, not from a word processor.** Word processors turn `--` into an en-dash (U+2013), which commands reject.

**Run commands in the terminal.** Pasting them into GitHub's web "Add file" box creates files named after the commands. The `go-verify` check now blocks those files.

## 5. Agents

- Install each agent's own CLI (Claude Code, Codex CLI, Gemini CLI, OpenCode) as the `sandbox` user and run each one in its own tmux session.
- Turn on **Shell Integration** in Secure ShellFish to get a notification when an agent finishes or needs your input.
- Each agent works on its registered branch prefix: `claude/`, `codex/`, `gemini/` or `manus/` (see `.github/agent-registry.json`). Copilot, Cursor, Windsurf and ChatGPT are present but unconfigured. OpenCode is not in the registry, so add it there before it opens PRs.
- To view the agent terminal's web page on the phone, forward its port over SSH. Never expose it publicly.

## 5a. Secure ShellFish connection settings

| Setting | Value | Where it runs |
|---|---|---|
| **Port Forwarding** | local `8080` → server `8080`, matching `user --port 8080` | Through the SSH connection to the server's loopback, so the port stays closed in the firewall. Open `http://localhost:8080` on the phone. |
| **Host** | The server's public IP, or its Tailscale MagicDNS name (`<machine>.<tailnet>.ts.net`) if the server and phone are on the same tailnet | With Tailscale you can close port 22 to the internet and allow SSH only on the `tailscale0` interface. |
| **HTTP Proxy** (`localhost:2080`, the `proxy2080.pac` file) | Optional | A SOCKS5 proxy on the iPhone, carried over SSH. It makes the phone's web traffic leave from the server. The server needs no setup for it; turn it off when you don't need it. |
| **Dynamic port forwarding** | Optional | Starts only after you tap a link in the terminal. Use the regular forward above for the agent terminal. |

Your tailnet name, server IP and key fingerprints are your own configuration. Keep them in Secure ShellFish, not in this repository. A test fails if a `.ts.net` name or a key body is committed to the script or this guide.

## 6. What this does and does not isolate

| Control | Coverage |
|---|---|
| tmux sessions | Separate terminal activity, but share one Linux user and one set of credentials. |
| `sandbox` user | No sudo, so an agent cannot change the system. |
| Stronger isolation | Needs one Linux user, container or VM per agent, each with its own credentials. |
| Shared GitHub account | Claude, Codex, Gemini and Manus all act through `DeontewattsV1`. The branch prefix says which agent claims to act; it does not prove it. Give each agent its own GitHub App or machine account to fix this (see `.learnings/FEATURE_REQUESTS.md`). |
| Provider API keys | Stay in GitHub Environments for the Actions workflows and do not reach this server. Give the server only the keys a task needs. |

## Verification status

| Item | Status |
|---|---|
| `bootstrap-ubuntu.sh admin` and `user` on a clean Ubuntu 24.04 container | **PASS**, including re-run idempotency, a non-root `sandbox` with no sudo, key permissions `600`, all three builds, `check.sh --quick`, the agent terminal answering on 127.0.0.1:8787, `agentterm verify` OK, and refusal to run the user step as root |
| A real cloud VM, an SSH connection from an iPhone, ShellFish Shell Integration | **NOT RUN**: needs your server and phone |
