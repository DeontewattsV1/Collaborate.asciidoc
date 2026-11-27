# Fly.io deployment (collaborate-asciidoc)

One existing Fly app, one `shared-cpu-1x` / 256 MB machine, one 1 GB volume.
The container runs three processes:

- the Next.js Dev Guild app on `127.0.0.1:3000`
- `agentterm` on `127.0.0.1:8787`, with its data on the existing `/data` volume (`log.jsonl`, `server.key`)
- a tiny Go proxy on `0.0.0.0:8080` (Fly's `http_service` port)

Public routes:

- `https://collaborate-asciidoc.fly.dev/` — the web app
- `https://collaborate-asciidoc.fly.dev/api/health` — liveness
- `https://collaborate-asciidoc.fly.dev/terminal` — the agent terminal UI
- agentterm APIs stay at `/api/info`, `/api/state`, `/api/log`, `/api/verify`, `/api/stream`, `/api/clock-in`, and `/api/entries`, so existing clients keep working

Chat state uses the app's memory/tmp store, symlinked from `/tmp/dev-guild-state.json` to `/data/guild/` so it survives auto-stop. Postgres and ClamAV are not run here (uploads stay fail-closed). There is no dedicated IPv4.

## Deploy

Never omit `--ha=false --update-only`. `fly deploy` otherwise tries to create a spare machine (and a second volume).

```sh
flyctl deploy --ha=false --update-only --yes -a collaborate-asciidoc
```

## Rollback

This rolls back to the agentterm-only image and port 8787. Keep a copy of the pre-change `fly.toml` (internal_port 8787, no web proxy), then:

```sh
flyctl deploy --ha=false --update-only --yes -a collaborate-asciidoc \
  --image registry.fly.io/collaborate-asciidoc:deployment-0dc8abd9ae14e01433e514ae385cba16 \
  --config path/to/agentterm-only-fly.toml
```

## Cost controls

- `auto_stop_machines = 'stop'`, `min_machines_running = 0`: no compute charge while idle. The 1 GB volume still bills (~$0.15/month), and a stopped machine bills ~$0.15/GB-month of rootfs.
- Do not create machines, volumes, dedicated IPv4 ($2/month), managed Postgres, or Upstash.
- Stay on `shared-cpu-1x`. Raise memory to 512 MB only if the 256 MB machine OOMs.
- Do not enable volume snapshots unless you need them (first 10 GB/month is free; after that $0.08/GB-month).
