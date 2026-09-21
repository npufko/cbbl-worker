# cbbl-worker

Demo parsing for [cbbl](https://v2.cbbl.net), a CS2 stats site. Public so the standard GitHub
Actions runners are free.

This repo holds no data and no site code. It is woken by a `repository_dispatch` from cbbl, downloads
one demo to the runner's disk, parses it, posts the result back, and exits. The demo is deleted with
the runner — nothing is stored here or anywhere else.

## What runs

`.github/workflows/parse-demo.yml`, triggered **only** by `repository_dispatch` (type `parse-demo`)
with `client_payload: { docId, url }`. There is no `pull_request` trigger, so a fork can never run
with these secrets.

1. Build `cbbl-parse` and `cbbl-highlight` from `parser/` (Go 1.24, demoinfocs v5).
2. `worker/src/parse-job.mjs`: check the URL's host against the allowlist, download, parse.
3. `POST /api/ingest/parsed` to cbbl with a bearer token.
4. On the first successful parse of a match, render a 2D replay of the best play among tracked
   players and upload it to Discord.

## Layout

| Path | What |
|---|---|
| `parser/cmd/cbbl-parse` | demo → per-map stats JSON (rating, KAST, opening duels, clutches) |
| `parser/cmd/cbbl-highlight` | demo → best-play scoring, and a 720×720 H.264 2D replay via ffmpeg |
| `parser/internal/` | demo opening (`.dem`, `.dem.zst`, `.dem.bz2`), stats collector, rating, renderer |
| `worker/src/parse-job.mjs` | the job: allowlist, download, parse, report |
| `worker/src/discord-clip.mjs` | multipart upload of the clip to a Discord channel webhook |

Clips are drawn from demo data only — player positions, kills, utility. No game footage and no Valve
assets are used, and nothing renders on anyone's PC.

## Required configuration

Settings → Secrets and variables → Actions.

**Secrets**

| Name | Value |
|---|---|
| `CBBL_URL` | `https://v2.cbbl.net` |
| `WORKER_SECRET` | shared with cbbl's `WORKER_SECRET`; authenticates `POST /api/ingest/parsed` |
| `DISCORD_MATCHES_WEBHOOK` | channel webhook URL, bare (no query string, no trailing slash) |

**Variables**

| Name | Value |
|---|---|
| `DEMO_URL_HOSTS` | comma-separated allowlist of demo CDN hosts |
| `TRACKED_STEAM_IDS` | comma-separated SteamID64s; only these players get a highlight clip |

The worker re-checks `DEMO_URL_HOSTS` itself before fetching anything, even though cbbl already
allowlisted the host — this repo must never become an open proxy.

## Local run

```powershell
Set-Location parser
go build -o ../worker/cbbl-parse ./cmd/cbbl-parse
go build -o ../worker/cbbl-highlight ./cmd/cbbl-highlight
go test ./...
```

```powershell
Set-Location worker
$env:DOC_ID = "faceit:<id>"
$env:DEMO_URL = "<presigned url>"
$env:CBBL_URL = "http://localhost:3000"
$env:WORKER_SECRET = "<same as cbbl>"
$env:ALLOWED_HOSTS = "<host of the url above>"
node src/parse-job.mjs
```
