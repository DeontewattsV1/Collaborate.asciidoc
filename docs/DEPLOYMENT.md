# Current deployment

This is the hosted setup as of 27 November 2026. It is the source of truth
for where Collaborate runs today. Optional and unused platforms are listed
at the end so older docs are not mistaken for the live path.

The Fly image, `fly.toml`, `Dockerfile.fly`, and `fly/` helper files land
through [pull request 103](https://github.com/DeontewattsV1/Collaborate.asciidoc/pull/103).
This page describes that setup; it does not duplicate those files.

## Architecture

One Fly.io app, `collaborate-asciidoc`, in `iad`, serves the public product:

- **One machine:** `shared-cpu-1x` with 256 MB RAM. Do not add machines.
- **One volume:** 1 GB, mounted at `/data`. Agent terminal data (`log.jsonl`,
  `server.key`) stays there. Guild chat state uses the app's memory/tmp store
  with the file kept under `/data/guild` so it survives auto-stop.
- **One image** (`Dockerfile.fly`) runs three processes:
  - the Next.js Dev Guild app on `127.0.0.1:3000`
  - `agentterm` on `127.0.0.1:8787`
  - a tiny Go reverse proxy on `0.0.0.0:8080` (Fly's `http_service` port)
- **Auto-stop:** `auto_stop_machines = 'stop'`, `auto_start_machines = true`,
  `min_machines_running = 0`. The machine stops after a few idle minutes and
  starts again on the next request. Opening the public URL starts it.

```
Internet
   │
   ▼
https://collaborate-asciidoc.fly.dev   (Fly shared IPv4, existing IPv6)
   │
   ▼
proxy :8080
   ├─ /                     → Dev Guild (Next.js)
   ├─ /api/health           → Dev Guild
   ├─ /terminal             → agentterm UI
   └─ /api/info, /api/state, /api/log, /api/verify,
      /api/stream, /api/clock-in, /api/entries
                            → agentterm (existing clients keep these paths)
```

Postgres and ClamAV are not started on this machine. 256 MB is too small
for them. Uploads stay fail-closed.

## Public URLs

| URL | What it is |
|---|---|
| https://collaborate-asciidoc.fly.dev/ | Dev Guild web app (owner token / invite codes) |
| https://collaborate-asciidoc.fly.dev/api/health | Liveness (`ok`, store kind, uploads status) |
| https://collaborate-asciidoc.fly.dev/terminal | Shared agent terminal UI |
| https://deontewattsv1.github.io/Collaborate.asciidoc/ | Static iPhone-style tour (`site/index.html`) on GitHub Pages |

The earlier Vercel Hobby demo at `dev-guild-demo.vercel.app` is not the
current host. Treat its status as unverified unless someone confirms it is
still running. Do not present it as the live app.

## Store, storage, AI, and auth

- **Room store:** default is the in-memory + `/tmp` adapter. On Fly the
  `/tmp` file is kept on `/data/guild`. `GUILD_STORE=postgres` and
  `DATABASE_URL` are supported in the app (see the durable-rooms work in
  PR 101) but there is no hosted Postgres yet.
- **Attachments:** a private Cloudflare R2 bucket, `collaborate-staging`,
  over the S3 API. Uploads are disabled (fail closed) until a ClamAV
  scanner host exists. clamd needs about 1–1.5 GB of RAM, which this
  machine does not have.
- **Models:** a free-tier Google Gemini key (`GOOGLE_GENERATIVE_AI_API_KEY`).
  Optional Groq, OpenRouter `:free`, Hugging Face, and Ollama keys are
  documented in [`.env.example`](../web/dev-guild/.env.example). Bring-your-own-key
  (BYOK) for members is planned, not shipped.
- **Auth:** a shared owner passphrase (`GUILD_OWNER_TOKEN`) and owner-signed
  invite codes. Keycloak / OIDC is planned, not running.

## Cost (approximate)

Figures use [Fly's public pricing](https://docs.fly.io/about/pricing) for
`iad`. There is no true $0 path while the existing 1 GB volume exists.

| Item | Typical monthly cost |
|---|---|
| 1 GB volume | $0.15, whether the machine is up or down |
| Stopped machine rootfs (~0.57 GB) | about $0.09, only while stopped |
| Compute (`shared-cpu-1x` 256 MB) | $2.19 if it ran all month; billed per second only while started |
| Light personal use (auto-stop) | about **$0.20–$0.40** total, mostly the volume |
| Shared IPv4 and IPv6 | free |
| Dedicated IPv4 | $2; **do not create one** |

Do not create machines, volumes, dedicated IPv4 addresses, managed
Postgres, or Upstash. Stay on `shared-cpu-1x`. Raise this same machine to
512 MB only if it OOMs; do not add a second machine.

## Deploy and rollback

Never omit `--ha=false --update-only`. `fly deploy` otherwise tries to
create a spare machine (and a second volume).

```sh
# From a checkout that contains fly.toml and Dockerfile.fly (PR 103):
flyctl deploy --ha=false --update-only --yes -a collaborate-asciidoc
```

Rollback to the previous agentterm-only image and port 8787 (keep a copy
of that older `fly.toml`):

```sh
flyctl deploy --ha=false --update-only --yes -a collaborate-asciidoc \
  --image registry.fly.io/collaborate-asciidoc:deployment-0dc8abd9ae14e01433e514ae385cba16 \
  --config path/to/agentterm-only-fly.toml
```

A current combined-image release was
`registry.fly.io/collaborate-asciidoc:deployment-01M8FP5V657824FQCWJXBHS9WM`.

## Secrets (names only)

Set on the Fly app; never commit values. The browser never receives them.

| Name | Purpose |
|---|---|
| `GUILD_OWNER_TOKEN` | Owner passphrase and invite signing |
| `GOOGLE_GENERATIVE_AI_API_KEY` | Free Gemini key (also accepted as `GEMINI_API_KEY` on the host) |
| `R2_ENDPOINT`, `R2_BUCKET`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY` | Private R2 bucket (`collaborate-staging`) |
| `DATABASE_URL` | Only if `GUILD_STORE=postgres`; **not set** today |
| `CLAMD_HOST` / `CLAMSCAN_PATH` | Scanner; **not set** today, so uploads stay disabled |
| `SECRETS_MASTER_KEY` | Secret vault master key; **not set** today, so the vault is off (chat redaction still runs). See [`SECRETS.md`](SECRETS.md) |

Full variable names for local development are in
[`web/dev-guild/.env.example`](../web/dev-guild/.env.example).

## Roadmap and unused platforms

These are optional, legacy, or planned. They are not how the live app runs.

| Item | Status |
|---|---|
| Oracle Cloud 1 GB Always Free VM | Exists but unused. SSH key access for this project's box was never completed |
| Oracle Ampere A1 (2–4 OCPU) | Unavailable (out of capacity). Pay-as-you-go was declined (card hold) |
| Vercel Hobby (`dev-guild-demo.vercel.app`) | Former public demo path; not the current host |
| Cloudflare Pages / Cloudflare Tunnel | Not deployed. R2 is the only Cloudflare service in use |
| Dokploy, Coolify | Not installed |
| Keycloak / OIDC | Planned; auth is still the owner token and invite codes |
| Valkey | Not in use |
| ClamAV / clamd | Required for uploads; not hosted yet |
| Uptime Kuma | Not in use |
| Native iOS app | Reference source only, not compiled. Scope is the Liquid Glass-style chat UI in `native/ios`. The free mobile path is the PWA (Add to Home Screen) |
| Hosted Postgres | Supported in code; no database is provisioned |

GitHub Codespaces (with a $0 spending limit) remains a valid optional
sandbox for iPhone or SSH work. See [`IPHONE-SANDBOX.md`](IPHONE-SANDBOX.md).
