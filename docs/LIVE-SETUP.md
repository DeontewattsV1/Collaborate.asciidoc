# Keys for the Dev Guild

The live Fly app's model key and owner token are Fly secrets, not GitHub
Environment secrets. See [`DEPLOYMENT.md`](DEPLOYMENT.md) for the names
that are set there.

The following GitHub Environments still apply to the optional `guild`
bridge and to Actions-based provider workflows. They are never sent to
the browser.

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

The primary model path is the free Gemini API. On the live app, set `GOOGLE_GENERATIVE_AI_API_KEY` as a Fly secret (server-side only). The same name can be set as a repository Actions secret, or as a server-side variable on an optional Vercel self-host. Optional `GROQ_API_KEY` and `OPENROUTER_API_KEY` are the next free fallbacks. The VM keys above answer when those are unset or exhausted, if you still run the bridge. See [`DEV-GUILD.md`](DEV-GUILD.md) and [`DEPLOYMENT.md`](DEPLOYMENT.md).

## Never

- Never paste a key into a file that is committed, an issue, a PR, a workflow input, or a chat. If one was exposed, rotate it.
- `GOOGLE_GENERATIVE_AI_API_KEY` may be set on Fly (the current host) or, for an optional self-host, on Vercel, always server-side only. Per-provider keys for a guild bridge stay on that VM.

## Container image

Every push to `main` publishes the agent terminal as a signed image, `ghcr.io/deontewattsv1/collaborate.asciidoc`.

```bash
docker run --rm -p 8787:8787 -v agentterm-data:/data ghcr.io/deontewattsv1/collaborate.asciidoc:main
```

The GHCR image is the agentterm-only build. The live Fly app uses a combined image (Next.js + agentterm + a proxy on port 8080), described in [`DEPLOYMENT.md`](DEPLOYMENT.md). The terminal UI is at `/terminal`; existing agentterm API paths stay at `/api/*`.

A guild bridge on an Oracle VM is optional and unused today (loopback plus a tunnel). See [`DEV-GUILD.md`](DEV-GUILD.md).
