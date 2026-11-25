# Going live: keys, environments and sandboxes

Everything is pre-configured. The only manual work is supplying API keys.

## 1. One command

From a machine where `gh auth login` has been run with admin rights on the repository:

```bash
bash scripts/setup_github_environments.sh
```

It creates each GitHub Environment below and limits deployments to `main`. It fills in the non-secret variables, then prompts (hidden input) for each key. Press Enter to skip a provider you do not use; re-run the script any time to add one later.

| Environment | Secret (you type it) | Variables (pre-filled; change in Settings › Environments) | Used by |
|---|---|---|---|
| `agent-openai` | `MODEL_API_KEY` | `MODEL_NAME` = `gpt-6.2-sol` | `agent-model-bridge` |
| `agent-anthropic` | `MODEL_API_KEY` | `MODEL_NAME` = `claude-sonnet-5-5` | `agent-model-bridge` |
| `agent-gemini` | `MODEL_API_KEY` | `MODEL_NAME` = `gemini-3.8-flash` | `agent-model-bridge` |
| `agent-xai` | `MODEL_API_KEY` | `MODEL_NAME` = `grok-4.7` | `agent-model-bridge` |
| `sandbox-opensandbox` | `OPEN_SANDBOX_API_KEY` | `OPEN_SANDBOX_DOMAIN` (your server's host[:port], asked once), `OPEN_SANDBOX_PROTOCOL` = `https` | `opensandbox-sandbox` |
| `sandbox-render` | `RENDER_API_KEY` | `RENDER_WORKSPACE_ID` = `tea-dc3ge117lnhs73fnjpig`, `RENDER_SANDBOX_GROUP_ID` = `sbg-dc3h680u01pc73eld1ng` | `render-sandbox` |

Model IDs change often; confirm each default against the provider's current model list before the first paid run. The setup script also accepts overrides such as `OPENAI_MODEL=... bash scripts/setup_github_environments.sh`.

**Key scope:** create a key that can only call the model or sandbox API. It needs no GitHub, billing-admin or organization-admin permissions. Each key lives only in its own environment, so a job for one provider never receives another provider's key.

The Crafting preview workflow (`sandbox-launch`) uses repository **variables** instead (`CRAFTING_TEMPLATE`, `CRAFTING_CHECKOUTS`). It stays skipped until they are set.

## 2. Run things (Actions tab › choose workflow › Run workflow)

| Workflow | What it does |
|---|---|
| `opensandbox-sandbox` | Archives any ref, uploads it into a fresh live OpenSandbox sandbox (`golang:1.26`), runs `go vet` and `go test` there, streams the output, then kills the sandbox. The workflow fails if the tests fail. |
| `agent-model-bridge` | Sends one open PR's diff to the selected model providers. Choose `review` for findings only, or `challenge` to have each model cross-examine the others. Posts one evidence comment, only if the PR head has not moved. |
| `render-sandbox` | Launches a Render sandbox, runs the live two-agent broker + MCP demo, and terminates it (or leaves it until `timeoutSeconds` if `keep=true`). See [`docs/RENDER-SANDBOX.md`](RENDER-SANDBOX.md). |

All three run only from `main` and only when started by the repository owner. None of them can push, merge or approve anything.

## 3. Use the sandboxes from your AI apps (MCP)

`.mcp.json` at the repository root registers two MCP servers for Claude Code. It contains no keys; it reads them from your shell:

```bash
export SIMULAR_BOX_API_KEY=cb_...          # Simular box (computer-use sandbox)
export OPEN_SANDBOX_API_KEY=...            # OpenSandbox
export OPEN_SANDBOX_DOMAIN=your.server:port
pip install "opensandbox-mcp==1.2.1"       # provides the opensandbox-mcp command
claude                                     # approve the project MCP servers when asked; /mcp to check
```

For other apps:

- **Simular box:** `npx @simular-ai/box@1.2.1 connect` installs it into Claude Code, Codex, Cursor, OpenCode and VS Code, and `npx @simular-ai/box@1.2.1 doctor` diagnoses problems.
- **OpenSandbox in Cursor:** use the same `opensandbox-mcp` command in `.cursor/mcp.json`.

## Never

- Never paste a key into a file, issue, PR, workflow input or chat. If one was exposed, rotate it at the provider and re-run the setup script.
- Never move these secrets to repository-level secrets, where every workflow could read them.

## What has and has not been verified

| Check | Status |
|---|---|
| Unit tests (offline, fake SDK and fake `gh`) | PASS |
| `actionlint` | PASS |
| Wiring against the real OpenSandbox SDK 1.2.1 | PASS |
| Setup script dry run | PASS |
| Live provider, OpenSandbox server, Render and box calls | **NOT RUN** |

The live calls need your keys and a reachable OpenSandbox server. The first workflow run after setup is the live test.

## Container image

Every push to `main` builds and publishes the agent terminal as a signed image, `ghcr.io/deontewattsv1/collaborate.asciidoc` (the `Docker` workflow; no setup needed). Pull requests build the image without pushing it.

```bash
docker run --rm -p 8787:8787 -v agentterm-data:/data ghcr.io/deontewattsv1/collaborate.asciidoc:main
```

The server listens on port 8787 and runs as a non-root user. Its signed log and server key persist in the `/data` volume.

## Unconfigured workflows stay green

When a workflow's environment has no key yet, `opensandbox-sandbox`, `render-sandbox` and `agent-model-bridge` finish successfully with a **"not configured, nothing ran"** warning and job summary. They never report a configured run that did not happen. Real problems still fail the run: a failing test, a bad PR number, a key with an invalid `MODEL_NAME`, or a provider error.
