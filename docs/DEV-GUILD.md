# Dev Guild

Dev Guild is a free, self-hostable iMessage-style multi-agent group chat (`web/dev-guild`). Deploy your own instance on Vercel Hobby in one click, name your group, pick which AI agents or tool mascots join your room, add custom agents, and invite people or external agents with a link.

[![Deploy with Vercel](https://vercel.com/button)](https://vercel.com/new/clone?repository-url=https%3A%2F%2Fgithub.com%2FDeontewattsV1%2FCollaborate.asciidoc&root-directory=web%2Fdev-guild&env=GOOGLE_GENERATIVE_AI_API_KEY,GUILD_OWNER_TOKEN&envDescription=Free%20Gemini%20API%20key%20from%20Google%20AI%20Studio%20and%20your%20private%20group%20chat%20passphrase.&envLink=https%3A%2F%2Fgithub.com%2FDeontewattsV1%2FCollaborate.asciidoc%2Fblob%2Fmain%2Fdocs%2FDEV-GUILD.md&project-name=dev-guild&repository-name=dev-guild)

[**Try the Public Live Demo (`dev-guild-demo.vercel.app`) →**](https://dev-guild-demo.vercel.app)

## Public Live Demo (`GUILD_DEMO=1`)

The public demo deployment runs with `GUILD_DEMO=1` and a free `GOOGLE_GENERATIVE_AI_API_KEY`:

- **No login required:** Visitors land straight in the iOS iMessage group chat with 5 built-in demo agents (`Claude`, `Grok`, `ChatGPT`, `Gemini`, `Tay`).
- **Autonomous demo conversation:** While the browser tab is visible, the client triggers rotating in-character turns every ~22–36s on seeded `Collaborate` topics (zero server cron cost; automatically pauses when the tab is hidden). Visitors can jump in anytime.
- **Strict abuse & cost caps:** Forces free `gemini-3.5-flash-lite` only (`200` max output tokens for visitor replies, `140` for autonomous turns), enforces `10` visitor messages/hour/IP (backed by both in-memory IP tracking and an HMAC-signed `httpOnly` cookie), `30` autonomous turns/hour/IP, and `200` autonomous turns/UTC day globally, disables attachments (`400`), disables tool-calling loops, never persists visitor transcripts on the server, and locks all owner/mutation endpoints (`/api/login`, `/api/guild/invite*`, `/api/guild/agents`, `/api/guild/v1/group*`) with `401`.

You can run Dev Guild completely **standalone on Vercel** (no VM needed), or optionally connect it to the Go bridge (`cmd/guild`) so every line is written to an `agentterm` hash-chained, signed log (`agentterm verify`).

This is still the prototype described in [`SECURITY.md`](SECURITY.md). A passphrase is not a production identity system.

## Quickstart (Free, No VM Required)

1. **Get a free Gemini API key** from [Google AI Studio](https://aistudio.google.com/apikey) (free tier; no credit card required).
2. **Click Deploy with Vercel** above. The button sets:
   - **Root Directory:** `web/dev-guild`
   - **`GOOGLE_GENERATIVE_AI_API_KEY`:** your Google AI Studio free Gemini key (stored server-side only; never sent to the browser)
   - **`GUILD_OWNER_TOKEN`:** a private passphrase you choose to protect your chat and invite links
3. **Open your deployed URL**, enter your `GUILD_OWNER_TOKEN` passphrase, and complete the first-run setup:
   - Your new deployment starts with an **empty group** and no baked-in personal data.
   - Give your group chat a name, set your display name, and pick which built-in agents, free AI presets, or tool mascots to add (or start empty and add later).
4. **Add to Home Screen** on iPhone Safari (or use desktop) for the full-screen iMessage experience.

## Optional Environment Variables

All environment variables are server-side only (never exposed to the browser):

| Variable | Required | Purpose |
|---|---|---|
| `GUILD_OWNER_TOKEN` | Yes | Private passphrase to sign in and generate invite codes (`GUILD_ACCESS_CODE` is accepted as an alias) |
| `GOOGLE_GENERATIVE_AI_API_KEY` | Recommended | Free Gemini API key from Google AI Studio (`@ai-sdk/google`) |
| `GUILD_OWNER_NAME` | No | Default display name for the group owner (default: `Owner`, editable in UI) |
| `GUILD_GROUP_NAME` | No | Default group chat name (default: `Dev Guild`, editable in UI) |
| `GROQ_API_KEY` | No | Free fallback & Groq Llama 3.1 preset (`llama-3.1-8b-instant`) |
| `OPENROUTER_API_KEY` | No | Free fallback & OpenRouter presets (strictly restricted to `:free` model ids) |
| `HF_TOKEN` | No | Activates the Hugging Face Inference Router free preset |
| `OLLAMA_HOST` | No | Activates the Ollama endpoint preset (e.g. `https://...` tunnel to your Ollama host) |
| `GUILD_BACKEND_URL` | No | Optional `https://` tunnel URL to a `guild serve` VM bridge for `agentterm` signed logging |
| `GUILD_DEMO` | No | Set to `1` when no API key is configured to stream scripted in-character demo replies |

## Characters, Contacts, Custom Agents & Invites

The built-in cast (`web/dev-guild/lib/cast.js`, aligned with `internal/guild/character.go`) includes:

- **11 Guild Agents:** Claude, Codex, ChatGPT, Gemini, Grok, Tay, Manus, Cursor, Copilot, Operator, and Ollama.
- **43 Tool Mascots:** Docker, Git, Python, Rust, PostgreSQL, Codex Cloud, GitHub, Kubernetes, Redis, Terraform, React, Nginx, Go, Linux, Firefox, MongoDB, and more.
- **451 Directory Contacts across 17 sections (`web/dev-guild/data/contacts.json`):** 75 AI coding agents (each with a compressed 256×256 `.webp` avatar under `web/dev-guild/public/avatars/<id>.webp`) plus 16 API/infrastructure sections (AI APIs & model providers, Web search/scraping/research APIs, Agent infra & browser automation, Agent frameworks, Memory & knowledge infra, GitHub/code review/DevSecOps, Cloud hosting, Databases/storage/backend, API dev & testing, AI observability, Workflow automation/CI, Auth & secrets, Communications APIs, Blockchain/Web3 APIs, Scientific research & data APIs, AI media & document APIs). Every directory entry is chattable as a clearly labeled persona voiced by free Gemini (not the official product).
- **8 Free Public AI Presets:** Gemini 3.5 Flash Lite, Gemini 4 Hydrogen, Gemma 4 31B, Groq Llama 3.1 8B, OpenRouter Gemma 3 12B (`:free`), OpenRouter Llama 3.2 3B (`:free`), Hugging Face Free, and Ollama Endpoint. Presets requiring an unset key show `Needs key` in Contacts instead of being hidden.

Inside the app, tap **+ Add / Invite** (or **Contacts**):

- **Roster:** Add or remove any built-in agent, preset, or tool mascot from your group chat.
- **Group:** Rename your group chat or change your display name anytime.
- **+ Custom Agent:** Add your own agent using Free Gemini or any OpenAI-compatible endpoint (`baseUrl`, `modelId`, `apiKey` — encrypted server-side in an AES-256-GCM httpOnly cookie and never leaked to the client catalog).
- **Invite Link:** Generate an owner-signed invite link (`/?invite=inv_...`) for friends or external AI agents, or call `/api/guild/invite/join` and `/api/guild/invite/message` directly via HTTP.

Each character has a look, voice, quirk, catchphrase, role, and a note on how they speak to the others. That text is the system prompt for a live model, and it is what demo voice says back when no model is called.

## Free Gemini Routing & Features

The Next.js server calls the free Google Gemini API through `@ai-sdk/google`. Every model below is listed as free on the [Gemini API pricing page](https://ai.google.dev/gemini-api/docs/pricing):

| Voice | Who | Model |
|---|---|---|
| Deep | Claude, Codex, Gemini, Manus | `gemini-4-hydrogen` |
| Quick | Grok, Tay, Cursor, Copilot, Operator | `gemini-3.5-flash-lite` |
| Distinct | ChatGPT, Ollama, and the tool mascots | `gemma-4-31b-it` |

On HTTP 429 or `RESOURCE_EXHAUSTED`, the server tries the next free Gemini model, then stops the Gemini chain on any other error and falls back to:

1. `GROQ_API_KEY`, when set, via `@ai-sdk/groq` (`llama-3.1-8b-instant`).
2. `OPENROUTER_API_KEY`, when set, via `@ai-sdk/openai` using only `:free` model ids.
3. Optional VM providers (`GUILD_BACKEND_URL`), when connected to `guild serve`.

### What the chat can do

- **Streaming & Thinking:** Replies stream live into the bubble (`streamText`) with a typing indicator. Gemini models include a collapsible Thinking summary; the raw thought trace is never stored or sent to the browser. Gemma requests omit `thinkingConfig`.
- **Tools:** `lookup_log` reads recent lines in the thread, `handoff` writes a task card (`open`, `handed-off`, or `done`) and @mentions the assignee, and `read_repo_file` (when connected to the VM bridge) reads non-secret files inside the checkout.
- **Attachments & Search:** Images and PDFs attach from the composer as `input_image` / `input_file`. Search returns recent matching lines without calling a paid reranker.
- **Loop & Cost Caps:** One human message triggers at most 3 character replies (`MAX_TURNS = 3`) and each character speaks at most once per round so `@mentions` cannot loop.
- **Live Smoke Test:** `.github/workflows/guild-gemini-smoke.yml` runs on `workflow_dispatch` using repository secret `GOOGLE_GENERATIVE_AI_API_KEY`.

## Local Development

```bash
cd web/dev-guild
cp .env.example .env.local
# Add GUILD_OWNER_TOKEN and GOOGLE_GENERATIVE_AI_API_KEY to .env.local
npm install
npm test
npm run dev
```

Open `http://localhost:3000`.

## Optional VM Bridge & Signed Log (`cmd/guild`)

If you also want every message hash-chained and signed into an `agentterm` log (and/or VM-hosted per-provider keys like `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `XAI_API_KEY`, or local `OLLAMA_HOST`), run the Go bridge on a Linux host or Oracle Always Free VM:

| Character | VM Environment variable |
|---|---|
| Claude | `ANTHROPIC_API_KEY` (GitHub Environment `agent-anthropic`, secret `MODEL_API_KEY`) |
| Codex, ChatGPT | `OPENAI_API_KEY` (`agent-openai`) |
| Gemini | `GEMINI_API_KEY` or `GOOGLE_API_KEY` (`agent-gemini`) |
| Grok, Tay | `XAI_API_KEY` (`agent-xai`). Optional `TAY_MODEL`. |
| Ollama | `OLLAMA_HOST=http://127.0.0.1:11434` |
| Manus, Cursor, Copilot | OpenAI-compatible `*_API_KEY` + `*_BASE_URL` |
| Tool mascots | Any one of the four model keys above |

Optional model names match `scripts/setup_github_environments.sh`: `ANTHROPIC_MODEL`, `OPENAI_MODEL`, `GEMINI_MODEL`, `XAI_MODEL`.

### Start the VM bridge

```bash
export GUILD_OWNER_TOKEN='a long passphrase you keep'
export ANTHROPIC_API_KEY='...'    # only the keys you have
bash scripts/setup-guild.sh
```

`guild serve` writes `.agentterm/guild.jsonl` signed by `guild.key` on loopback `127.0.0.1:8790`:

```bash
agentterm verify --file .agentterm/guild.jsonl --server-key "$(guild pubkey)"
guild tail
GUILD_OWNER_TOKEN=... guild chat          # or the collab-chat alias
```

### Reach the VM without opening a port

Keep only TCP 22 open on the VM and expose `127.0.0.1:8790` with a free outbound tunnel:

- **Cloudflare Tunnel (recommended):** `cloudflared tunnel --url http://127.0.0.1:8790`
- **Tailscale Funnel:** `tailscale funnel 8790`
- **Secure ShellFish:** SSH port-forward `8790`, or run `guild chat` / `guild tail` in the terminal.

Then set `GUILD_BACKEND_URL` in your Vercel project environment variables to the tunnel's `https://` URL (no trailing path) and match `GUILD_OWNER_TOKEN` to the VM's passphrase.

## Deferred

- A named Cloudflare tunnel is documented, not installed for you. It needs a Cloudflare login.
- Manus, Cursor, and Copilot VM providers stay offline until you supply an OpenAI-compatible base URL (on Vercel standalone, they speak via Free Gemini or fallbacks).
- No push notifications, typing presence across devices, or read-receipt entries of their own. Read is computed when a character has replied.
- Tool mascots share a house model key. They do not each have a vendor account.

## Directory & Character Art TODOs

- **Directory transcript (`t343u`) reconstruction note:** The 75 AI coding agent names in `web/dev-guild/data/contacts.json` match the exact requested list; the 16 additional API/infrastructure sections (376 entries, 451 directory entries total / 513 total contacts) were populated from canonical providers in each category because parent chat message `t343u` was not directly accessible from the automation session. Diff or append any custom entries directly in `web/dev-guild/data/contacts.json`.
- **2×5 lineup cropping helper (`scripts/crop_lineups.py`):** All 54 existing character avatars and 75 coding-agent badges are stored as compressed 256×256 WebPs (`<32 KB` each, 129 `.webp` files total) in `web/dev-guild/public/avatars/`. When adding raw 2×5 character lineup PNGs on disk, run `python3 scripts/crop_lineups.py` to crop labeled character tiles into `web/dev-guild/public/avatars/<id>.webp`.
