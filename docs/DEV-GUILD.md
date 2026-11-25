# Dev Guild

Dev Guild is a private iMessage-style chat. Deonte texts chibi characters. The characters can answer each other. Every line, human and character, is appended to an `agentterm` log and can be checked with `agentterm verify`.

The phone app is a Next.js client meant for Vercel Hobby. The bridge is a small Go process on the Oracle VM. Model keys stay in that process. The browser only holds an httpOnly session cookie.

This is still the prototype described in [`SECURITY.md`](SECURITY.md). A passphrase is not a production identity system.

## Characters

The cast is `internal/guild`. Agents (Claude, Codex, ChatGPT, Gemini, Grok, Tay, Manus, Cursor, Copilot, Operator, Ollama) are separate from the tool mascots on The Dev Guild sheets. Codex the knight is not Codex Cloud the bot. Ollama uses the llama from sheet IV.

Each character has a look, voice, quirk, catchphrase, role, and a note on how they speak to the others. That text is the system prompt for a live model, and it is what demo voice says back when no model is called. The profile sheet in the app shows the same words.

A character is online only when the key it needs is in the server environment. The others show how to turn them on. Operator is always on and does not call a provider. Tool mascots speak through whichever of Anthropic, OpenAI, Gemini, or xAI is configured, still in that mascot's voice.

| Character | Environment variable |
|---|---|
| Claude | `ANTHROPIC_API_KEY` (GitHub Environment `agent-anthropic`, secret `MODEL_API_KEY`) |
| Codex, ChatGPT | `OPENAI_API_KEY` (`agent-openai`) |
| Gemini | `GEMINI_API_KEY` or `GOOGLE_API_KEY` (`agent-gemini`) |
| Grok, Tay | `XAI_API_KEY` (`agent-xai`). Tay has her own prompt. Optional `TAY_MODEL`. |
| Ollama | `OLLAMA_HOST=http://127.0.0.1:11434` |
| Manus, Cursor, Copilot | An OpenAI-compatible base URL plus that product's key. There is no free public chat endpoint wired in. |
| Tool mascots | Any one of the four model keys above |

Optional model names match `scripts/setup_github_environments.sh`: `ANTHROPIC_MODEL`, `OPENAI_MODEL`, `GEMINI_MODEL`, `XAI_MODEL`.

## Free Gemini

The Next.js server calls the free Google Gemini API through `@ai-sdk/google`. `google()` reads `GOOGLE_GENERATIVE_AI_API_KEY` on the server. The browser never receives it. `.env.example` lists the name. `.env.local` is gitignored. Do not commit a key, and do not print it in logs.

The same value is a repository Actions secret named `GOOGLE_GENERATIVE_AI_API_KEY`. Set it on the Vercel project as well (Production and Preview, server-side only): Project → Settings → Environment Variables → `GOOGLE_GENERATIVE_AI_API_KEY`. The Vercel app cannot see the Actions secret by itself.

The default model is `gemini-3.5-flash-lite`. `gemini-2.5-flash-lite` routes to that model on 1 December 2026. Every model below is listed as free on the [Gemini API pricing page](https://ai.google.dev/gemini-api/docs/pricing).

| Voice | Who | Model |
|---|---|---|
| Deep | Claude, Codex, Gemini, Manus | `gemini-4-hydrogen` |
| Quick | Grok, Tay, Cursor, Copilot, Operator | `gemini-3.5-flash-lite` |
| Distinct | ChatGPT, Ollama, and the tool mascots | `gemma-4-31b-it` |

A character's system prompt is still the personality from the registry (voice, quirk, catchphrase, how they talk to the others).

On HTTP 429 or `RESOURCE_EXHAUSTED`, the server tries the next free Gemini model, then stops the Gemini chain on any other error. After Gemini:

1. `GROQ_API_KEY`, when set, calls `@ai-sdk/groq` with `llama-3.1-8b-instant`.
2. `OPENROUTER_API_KEY`, when set, calls OpenRouter through `@ai-sdk/openai` and only model ids that end in `:free`.
3. The VM providers in the table above (`MODEL_API_KEY` in the GitHub Environments, exported on the VM as `ANTHROPIC_API_KEY` and the others).

`guild chat` on the VM uses those VM keys directly. The lines still land in the same signed log. Tests pass mock runners and do not call a provider.

### What the chat can do

The phone streams replies into the bubble (`streamText`) and shows a typing indicator until the first token.

Gemini thinking uses `providerOptions.google.thinkingConfig`. Claude asks for `thinkingLevel: high` with `includeThoughts: true`. Grok uses `low`. Operator turns thoughts off. A short summary can appear in a collapsible Thinking row. The raw thought trace is not stored and not sent to the phone. Gemma requests omit `thinkingConfig`.

Tools the model may call, each appended to the signed log as a `tool` line:

- `read_repo_file` reads one path inside the VM checkout. `..`, `.env`, keys, and `.agentterm` are refused. The read runs in `guild serve` and is logged before the bytes return.
- `lookup_log` reads recent signed lines for the thread.
- `handoff` writes a task card (`task`, `assignee`, `status` of `open`, `handed-off`, or `done`) and @mentions that character.

Images and PDFs attach from the composer. The model receives them as `input_image` or `input_file`. The log stores the file name only.

Search in the thread returns the newest lines whose text contains the query.

`GUILD_DEMO=1` with no free key set streams a scripted in-character reply, a Thinking summary, a tool line, and a task card, and does not call a provider. That is what the screenshots show.

A live smoke test is `.github/workflows/guild-gemini-smoke.yml`. It runs only on `workflow_dispatch`, reads the Actions secret, and fails when the reply text is empty. It prints `non-empty: yes` and a length. It does not print the key.

## What gets logged

`guild serve` writes `.agentterm/guild.jsonl`. Each line is an `agentterm` entry of kind `chat`, hash-chained and signed by `guild.key`. Model replies go through `adapter.Broker.Say`, the same broker method `collaborate adapter run` uses, and that `Say` writes the log. The chat broker refuses path claims and file writes.

```bash
agentterm verify --file .agentterm/guild.jsonl --server-key "$(guild pubkey)"
guild tail
GUILD_OWNER_TOKEN=... guild chat          # or the collab-chat alias
```

One human message can cause at most 3 character replies (the turn cap) and about 8000 estimated tokens (the cost cap). A character speaks once per round, so two characters @mentioning each other cannot loop. `GUILD_DEMO=1` answers in character and does not call a provider.

## VM setup

On the `sandbox` user, from a current checkout:

```bash
export GUILD_OWNER_TOKEN='a long passphrase you keep'
export ANTHROPIC_API_KEY='...'    # only the keys you have
bash scripts/setup-guild.sh
```

The script builds `guild`, prints who is online, and starts `guild serve` in tmux session `dev-guild` on `127.0.0.1:8790`. It does not write secrets into the repo. GitHub can show `MODEL_NAME` for the four `agent-*` environments. It cannot show the API key back. Paste keys into the VM environment yourself.

Replies are asynchronous, so the Vercel function returns before the model finishes. The phone polls.

## Reach the VM without opening a port

The VM should keep only TCP 22 open. The bridge listens on loopback. Pick one free outbound tunnel:

**Cloudflare Tunnel (recommended).** `cloudflared` is a single static binary and stays small on a 1 GB VM.

```bash
curl -fsSL -o /tmp/cloudflared.tgz https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-arm64.tgz
mkdir -p "$HOME/.local/bin" && tar -C "$HOME/.local/bin" -xzf /tmp/cloudflared.tgz
cloudflared tunnel --url http://127.0.0.1:8790
```

A quick tunnel prints an `https://*.trycloudflare.com` URL. A named tunnel (free Cloudflare account) keeps a stable hostname. Point the tunnel at loopback only.

**Tailscale Funnel** is the other free option if the VM is already on a tailnet. `tailscale funnel 8790` publishes the loopback port with Tailscale's TLS. SSH can stay on port 22 or move to Tailscale.

**Secure ShellFish** still reaches the same log over SSH: port-forward `8790`, or skip the web app and run `collab-chat` / `guild tail` in the terminal. Do not open 8790 in the Oracle security list.

## Vercel Hobby

Import the repo. Set the project root to `web/dev-guild`. Production environment variables, server-side only:

| Name | Value |
|---|---|
| `GUILD_OWNER_TOKEN` | Same passphrase as the VM |
| `GUILD_BACKEND_URL` | The `https://` tunnel or Funnel URL, no path |
| `GOOGLE_GENERATIVE_AI_API_KEY` | The free Gemini key. Same value as the Actions secret. Server-side only |
| `GROQ_API_KEY` | Optional. Used only when Gemini is exhausted |
| `OPENROUTER_API_KEY` | Optional. Used only with `:free` model ids |

The VM's per-provider keys stay on the VM. Add to Home Screen on iPhone Safari for the standalone app. The first screen asks for the passphrase and stores only an httpOnly cookie.

## Deferred

- A named Cloudflare tunnel is documented, not installed for you. It needs a Cloudflare login.
- Manus, Cursor, and Copilot stay offline until you supply an OpenAI-compatible base URL.
- The network `collaborate serve` ledger is not given a second copy of each line. The signed `agentterm` log is the record, written through `Broker.Say`.
- No push notifications, typing presence across devices, or read-receipt entries of their own. Read is computed when a character has replied.
- Tool mascots share a house model key. They do not each have a vendor account.
