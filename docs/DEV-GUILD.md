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

## Vercel AI Gateway

The Next.js server tries [Vercel AI Gateway](https://vercel.com/docs/ai-gateway) first, through the `ai` package. `generateText` and `streamText` receive a gateway model string such as `google/gemini-2.5-flash-lite` (`GUILD_GATEWAY_MODEL` overrides it). The key is `AI_GATEWAY_API_KEY`, read only on the server from the environment or `.env.local`. `.env.example` lists the name. `.env.local` is gitignored. Do not commit a key.

A character's system prompt is the personality from the registry (voice, quirk, catchphrase, how they talk to the others). The gateway model speaks as that character.

If `AI_GATEWAY_API_KEY` is unset, or the gateway returns an error, the same message is answered by the VM providers in the table above (`MODEL_API_KEY` in the GitHub Environments, exported on the VM as `ANTHROPIC_API_KEY` and the others). A new gateway key often returns HTTP 403, `requires a valid credit card on file`, until a card is added in Vercel. That error takes the fallback path. Tests mock the gateway and do not call it.

`guild chat` on the VM does not use the gateway. It uses the VM keys directly, and the lines still land in the same signed log.

### What the gateway can do in the chat

The phone streams replies into the bubble (`streamText`) and shows a typing indicator until the first token. These options are sent on every gateway call as `providerOptions.gateway`:

| Option | Value |
|---|---|
| `models` | Per-character fallbacks, after the primary model. Claude tries `anthropic/claude-haiku-4.5` then `google/gemini-2.5-flash-lite`. Grok tries `xai/grok-3-mini` then the same flash-lite model. |
| `caching` | `auto` |
| `user` | `deonte` |
| `tags` | `dev-guild` and the character's name |
| `zeroDataRetention` | `true` |

Set a **Budget** on the gateway key in the Vercel dashboard (AI Gateway → the key → Budget). That limit is account-side. It is not a value in this repo.

`reasoning` is set per character: Claude `high`, Grok `low`, Operator `none`. A one-line summary can appear in a collapsible Thinking row. The raw reasoning trace is not stored and not sent to the phone.

Tools the model may call, each appended to the signed log as a `tool` line:

- `read_repo_file` reads one path inside the VM checkout. `..`, `.env`, keys, and `.agentterm` are refused. The read runs in `guild serve` and is logged before the bytes return.
- `lookup_log` reads recent signed lines for the thread.
- `handoff` writes a task card (`task`, `assignee`, `status` of `open`, `handed-off`, or `done`) and @mentions that character.

Images and PDFs attach from the composer. The model receives them as `input_image` or `input_file`. The log stores the file name only.

Search in the thread asks the gateway to rerank with `cohere/rerank-v3.5`. If the key is missing or the gateway returns 403, the newest lines are shown instead.

`GUILD_DEMO=1` streams a scripted in-character reply, a Thinking summary, a tool line, and a task card, and does not call the gateway. That is what the screenshots show.

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

No provider key belongs in Vercel. Add to Home Screen on iPhone Safari for the standalone app. The first screen asks for the passphrase and stores only an httpOnly cookie.

## Deferred

- A named Cloudflare tunnel is documented, not installed for you. It needs a Cloudflare login.
- Manus, Cursor, and Copilot stay offline until you supply an OpenAI-compatible base URL.
- The network `collaborate serve` ledger is not given a second copy of each line. The signed `agentterm` log is the record, written through `Broker.Say`.
- No push notifications, typing presence across devices, or read-receipt entries of their own. Read is computed when a character has replied.
- Tool mascots share a house model key. They do not each have a vendor account.
