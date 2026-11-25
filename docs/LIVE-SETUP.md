# Keys for the Dev Guild

Provider keys stay in GitHub Environments. The guild bridge on the VM reads them from the process environment. The phone never sees them.

## 1. Create the environments

From a machine where `gh auth login` has admin on the repository, in a terminal:

```bash
bash scripts/setup_github_environments.sh
```

The script creates each environment, limits it to `main`, sets `MODEL_NAME`, and prompts for `MODEL_API_KEY` without echoing it. Press Enter to skip a provider. Re-running does not erase a key you skip.

| Environment | Secret | Variable | Who speaks with it |
|---|---|---|---|
| `agent-openai` | `MODEL_API_KEY` | `MODEL_NAME` (default `gpt-6.2-sol`) | Codex and ChatGPT, and tool mascots if no other key is set |
| `agent-anthropic` | `MODEL_API_KEY` | `MODEL_NAME` (default `claude-sonnet-5-5`) | Claude, and tool mascots first |
| `agent-gemini` | `MODEL_API_KEY` | `MODEL_NAME` (default `gemini-3.8-flash`) | Gemini |
| `agent-xai` | `MODEL_API_KEY` | `MODEL_NAME` (default `grok-4.7`) | Grok and Tay |

GitHub will not show a secret back. On the VM, export the value as `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, or `XAI_API_KEY`. See [`DEV-GUILD.md`](DEV-GUILD.md).

The Vercel app can also use `AI_GATEWAY_API_KEY` (AI Gateway). That key is not one of these environments. If it is missing or the gateway returns an error, the VM keys above are the fallback.

## Never

- Never paste a key into a file that is committed, an issue, a PR, a workflow input, or a chat. If one was exposed, rotate it.
- Never put provider keys in Vercel except `AI_GATEWAY_API_KEY` for the gateway. Per-provider keys stay on the VM.

## Container image

Every push to `main` publishes the agent terminal as a signed image, `ghcr.io/deontewattsv1/collaborate.asciidoc`.

```bash
docker run --rm -p 8787:8787 -v agentterm-data:/data ghcr.io/deontewattsv1/collaborate.asciidoc:main
```

The server listens on port 8787 inside the container and runs as a non-root user. Put TLS in front before exposing it. The guild bridge on the Oracle VM is loopback plus a tunnel, described in [`DEV-GUILD.md`](DEV-GUILD.md).
