# iPhone sandbox terminal (Secure ShellFish) -- Free ($0) Setup

Drive the repository and its agents from an iPhone at **$0 cost**. Secure ShellFish is the SSH terminal on the phone; everything runs on a free remote Linux environment.

> **Paid servers are not used.** Do not spin up DigitalOcean Droplets or Hetzner Cloud servers unless you intend to pay monthly/hourly hosting charges. Use one of the two free ($0) paths below instead.

```
iPhone · Secure ShellFish
  ├─ Path A (free tier, $0 limit): GitHub Codespaces (.devcontainer/devcontainer.json + sshd)
  └─ Path B (permanent free tier): Oracle Cloud Always Free Ubuntu 24.04 (ARM64 Ampere A1), user "sandbox" (no sudo)
                                     ├─ tmux: claude · codex · gemini · manus · operator
                                     ├─ tmux: collaborate-terminal -> agentterm on 127.0.0.1:8787 (or 8080)
                                     └─ Collaborate.asciidoc -> GitHub PRs and CI
```

Agent work still lands only through pull requests and the required checks. The phone is a control terminal, not a way around them.

## Choose a free ($0) host

| Path | Cost | Specs & Limits | Best For |
|---|---|---|---|
| **Path A: GitHub Codespaces** | **$0** (set spending limit to `$0`) | 120 free core-hours/month (60 hours on 2-core) + 15 GB-month storage; auto-sleeps after 15 min idle | Fastest start; pre-configured via `.devcontainer/devcontainer.json`; clones private repo automatically |
| **Path B: Oracle Cloud Always Free** | **$0/month** | Up to 4 ARM64 Ampere A1 OCPUs + 24 GB RAM total (e.g. 2 OCPU / 12 GB RAM) or AMD Micro, 200 GB block storage | Always-on SSH server on port 22 with unprivileged `sandbox` user; subject to regional capacity and idle-VM reclamation |

## Path A: GitHub Codespaces ($0 spending limit)

1. **Enforce a $0 budget first:** on GitHub, open **Settings -> Billing and licensing -> Spending limits** (or Codespaces spending limit) and confirm the Codespaces spending limit is **$0** so GitHub stops rather than charges if you reach 120 core-hours/month.
2. **Create the Codespace:** on `DeontewattsV1/Collaborate.asciidoc`, choose **Code -> Codespaces -> Create codespace on main** (or configure options: **2-core**, **15-minute idle timeout**).
   - `.devcontainer/devcontainer.json` automatically installs Go (`GOTOOLCHAIN=auto` matching `go.mod`), `gh`, `sshd`, `tmux`, `python3`, `libsqlite3-dev`, builds `agentterm`, `collaborate`, and `collab` into `~/.local/bin`, installs the `collab-*` aliases in `~/.bashrc`, and starts `agentterm` on `127.0.0.1:8787` inside tmux session `collaborate-terminal` on every boot.
3. **Connect from Secure ShellFish:**
   - Open **Cloud Servers -> GitHub Codespaces** in Secure ShellFish, authorize GitHub, and select your Codespace for `DeontewattsV1/Collaborate.asciidoc`.
   - **Honest fallback:** if **Cloud Servers -> GitHub Codespaces** is missing, disabled, or requires a paid ShellFish unlock in your app version, `gh codespace ssh` cannot run natively on an iPhone without a local `gh` shell. Either connect over Tailscale installed inside the Codespace (`sshd` is already enabled by `.devcontainer/devcontainer.json`), or use **Path B (Oracle Cloud Always Free)** for direct SSH on port 22.
4. Once connected, load the aliases and inspect the terminal:

```bash
source ~/.bashrc
collab-status
collab-terminal
```

## Path B: Oracle Cloud Always Free (Ubuntu 24.04 ARM64)

1. **Create an Always Free VM:** in Oracle Cloud Infrastructure (OCI), create a Compute instance labeled **Always Free-eligible**:
   - **Image:** Canonical Ubuntu 24.04 LTS (`aarch64` / ARM64)
   - **Shape:** `VM.Standard.A1.Flex` (Ampere A1, e.g. 2 OCPUs / 12 GB RAM; Always Free allows up to 4 OCPUs and 24 GB RAM total) or `VM.Standard.E2.1.Micro`
   - **SSH key:** paste your Secure ShellFish **Secure Enclave** public key (`ssh-ed25519 ...`) during VM creation.
   - **Firewall (VCN Security List / NSG):** allow **only TCP port 22** (or restrict to your Tailscale network). Leave ports `8080` and `8787` closed. On the Ubuntu host itself, you can also verify/lock down UFW (`sudo ufw allow OpenSSH && sudo ufw enable && sudo ufw status`) and confirm `agentterm` binds only loopback (`ss -ltn`).
   - **Local Ubuntu Server / LXD alternative ($0):** the same `admin` and `user` steps work unchanged on your own Ubuntu 24.04 machine or an LXD container (`lxc launch ubuntu:24.04`), and Ubuntu Pro is free for up to 5 personal machines if you want ESM/Livepatch security updates.
2. **Admin step (as `ubuntu`):** connect from Secure ShellFish with Username `ubuntu`, Host `<oracle-public-ip>`, Port `22`, **Key agent forwarding OFF**.
   - Note: while `Collaborate.asciidoc` is private, raw GitHub URLs return 404 without a token. Either run after making the script reachable, pass a fine-grained read token header to `curl`, or run `gh auth login` first.

```bash
curl -fsSL https://raw.githubusercontent.com/DeontewattsV1/Collaborate.asciidoc/main/scripts/sandbox/bootstrap-ubuntu.sh \
  | bash -s -- admin --key "ssh-ed25519 AAAA...your-key... ShellFish@iPhone"
```

This step installs the toolchain (`git`, `gh`, `tmux`, `golang-go`, `python3`, `libsqlite3-dev`, etc.), creates the unprivileged **`sandbox`** user without sudo, and installs your Secure Enclave public key in `/home/sandbox/.ssh/authorized_keys` with mode `600`. It is safe to re-run.

3. **User step (as `sandbox`):** in Secure ShellFish, change the server's Username from `ubuntu` to `sandbox`, reconnect, sign in to GitHub if the repo is private, and run the `user` step:

```bash
gh auth login --hostname github.com --git-protocol https
gh auth setup-git
curl -fsSL https://raw.githubusercontent.com/DeontewattsV1/Collaborate.asciidoc/main/scripts/sandbox/bootstrap-ubuntu.sh \
  | bash -s -- user --port 8080
source ~/.bashrc
```

This step clones the repository to `~/workspace/Collaborate.asciidoc`, writes a marked `~/.bashrc` block (replaced, never duplicated), builds `agentterm`, `collaborate`, and `collab` into `~/.local/bin`, runs `scripts/check.sh --quick`, and starts `agentterm` in tmux on `127.0.0.1:8080` with command execution off.

Options for `user` / `codespace`:
- `--port N`: sets the agent terminal's loopback port (default `8787`; use `8080` to match a ShellFish `8080 -> 8080` forward).
- `--no-check`: skips `scripts/check.sh --quick`.
- `--no-serve`: skips starting `agentterm`.

## GitHub fine-grained token

Sign in with a **fine-grained personal access token limited to `DeontewattsV1/Collaborate.asciidoc`**:

| Permission | Access |
|---|---|
| Contents, Pull requests | Read and write |
| Actions, Checks, Metadata | Read |
| Administration, Secrets | None |

```bash
gh auth login --hostname github.com --git-protocol https
```

## Snippets to save in Secure ShellFish

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

**Run commands in the terminal.** Pasting them into GitHub's web "Add file" box creates files named after the commands. The `go-verify` check blocks those files.

## Agents

- Install each agent's own CLI (Claude Code, Codex CLI, Gemini CLI, OpenCode) as the `sandbox` / Codespace user and run each one in its own tmux session (`collab-agent claude`, etc.).
- Turn on **Shell Integration** in Secure ShellFish to get a notification when an agent finishes or needs your input.
- Each agent works on its registered branch prefix: `claude/`, `codex/`, `gemini/` or `manus/` (see `.github/agent-registry.json`). Copilot, Cursor, Windsurf and ChatGPT are present but unconfigured. OpenCode is not in the registry, so add it there before it opens PRs.
- To view the agent terminal's web page on the phone, forward its port over SSH (or use Codespaces' private port forward). Never expose it publicly.

## Secure ShellFish connection settings

| Setting | Value | Notes |
|---|---|---|
| **SSH Key** | Generated in **Secure Enclave** (`ssh-ed25519`) | Private key never leaves the iPhone |
| **Key agent forwarding** | **OFF** | Prevents the remote host from using forwarded SSH identities |
| **Persistence** | `tmux` | Keeps agent and `agentterm` sessions alive when switching apps |
| **Port Forwarding** | local `8080` -> server `8080` (matching `user --port 8080`, or `8787` -> `8787` for Codespaces) | Reaches loopback `127.0.0.1` over SSH while ports stay closed in the firewall; open `http://localhost:8080` on iPhone |
| **Host** | Server public IP, or optional Tailscale MagicDNS (`<machine>.<tailnet>.ts.net`) | With Tailscale you can close port 22 to the public internet and allow SSH only on `tailscale0` |
| **HTTP Proxy** (`localhost:2080`) | Optional | SOCKS5 proxy over SSH; turn off when unused |

Your tailnet name, server IP, key fingerprints, and GitHub tokens belong only in Secure ShellFish or on the host -- never commit them to this repository. Do not register self-hosted GitHub Actions runners (`actions-runner` tokens) or add self-hosted runner workflows.

## What this does and does not isolate

| Control | Coverage |
|---|---|
| tmux sessions | Separate terminal activity, but share one Linux user and one set of credentials. |
| `sandbox` user (Path B) | No sudo, so an agent cannot change the system. (Note: Codespaces containers run as `vscode`/`codespace` with container-scoped sudo.) |
| Stronger isolation | Needs one Linux user, container or VM per agent, each with its own credentials. |
| Shared GitHub account | Claude, Codex, Gemini and Manus all act through `DeontewattsV1`. The branch prefix says which agent claims to act; it does not prove it. Give each agent its own GitHub App or machine account to fix this (see `.learnings/FEATURE_REQUESTS.md`). |
| Provider API keys | Stay in GitHub Environments for the Actions workflows and do not reach this server. Give the server only the keys a task needs. |

## Verification status

| Item | Status |
|---|---|
| `bootstrap-ubuntu.sh admin` and `user` on a clean Ubuntu 24.04 container | **PASS** (verified in PR #76: re-run idempotency, non-root `sandbox` with no sudo, key permissions `600`, all three builds, `check.sh --quick`, `agentterm` on `127.0.0.1:8080` with `exec:false`, `agentterm verify` OK) |
| `bootstrap-ubuntu.sh codespace` / `codespace-start` and `.devcontainer/devcontainer.json` offline + local Debian 13 non-root build/`check.sh --quick`/`agentterm` run | **PASS** (simulated `admin` key/newline handling + live non-root `user`/`codespace` clone, `~/.bashrc` block, Go builds, `check.sh --quick`, tmux `agentterm` on loopback, `agentterm verify`) |
| Real GitHub Codespace spin-up, real Oracle Cloud Always Free ARM64 VM, iPhone SSH / ShellFish Cloud Servers & Shell Integration | **NOT RUN** (needs your GitHub Codespace / Oracle Cloud account and iPhone) |
