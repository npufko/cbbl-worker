# Security and logging policy: cbbl-worker

This repository is **public**, and so is every Actions log it produces. Anyone can open a run and read
its output, without signing in, for as long as the log is retained. Treat every `console.log`, every
`echo` and every step header as a post on a public page.

## Rule 1: never pass a runtime value through `env:`

At the top of each step's log, Actions prints the step's `env:` block. It masks `secrets.*` as `***`
but prints everything else as plain text, **before your script runs**, so a later `::add-mask::`
cannot hide it.

| Source | Via `env:`? |
|---|---|
| `secrets.*` | Yes (printed as `***`) |
| `vars.*` that are not sensitive (e.g. `DEMO_URL_HOSTS`) | Yes |
| `github.event.client_payload.*` (repository_dispatch) | **No.** Read it with `eventPayload()` |
| `inputs.*` / `github.event.inputs.*` (workflow_dispatch) | **No.** Read it with `eventPayload()` |
| Anything that identifies a player (SteamIDs, share codes) | **No.** Store it as a secret, or read it from the payload and mask it |

`eventPayload()` in `src/log-safety.mjs` reads the runner's event file (`GITHUB_EVENT_PATH`), so the
value never shows up in the step header. Never write a payload or input value into a `run:` script
with `${{ }}` either: that prints it, and it opens a script-injection hole.

## Rule 2: mask first, then use

Use `mask(value)` on anything sensitive the job learns at runtime, **before** the value can reach
any log line. That includes values from the event payload, from cbbl's responses and from Steam/GC.
Actions then redacts the exact string wherever it appears later in the job, including inside
response bodies that echo it back.

Masking matches exact strings only. A value that has been URL-encoded, base64-encoded, sliced or
reformatted is **not** redacted. Mask the form you print, or don't print it.

## Rule 3: what may and may not be logged

**Never log:**
- Secrets or anything built from them: `WORKER_SECRET`, `STEAM_REFRESH_TOKEN`, `STEAM_API_KEY`,
  `MATCH_AUTH_CODE`, `GOOGLE_DRIVE_API_KEY`, `DISCORD_MATCHES_WEBHOOK`, `CBBL_URL`, and any URL or
  header that contains one of them.
- Bearer URLs: FACEIT presigned demo links (`X-Amz-Signature`), Valve replay URLs, Drive URLs with `key=`.
- Share codes in full (`short()` gives a `CSGO-xxxxx…xxxxx` preview), match reservation data, and the
  GC `matchList` payload.
- SteamIDs and player names from Premier/MM/FACEIT demos. Log `player N` instead. LAN team names are
  public event data and may be logged.
- Raw child-process stderr. The parser's `self-check:` lines name players and SteamIDs.
- Whole cbbl response bodies from endpoints that are not listed below.
- Full error objects or stacks. An uncaught error prints every field it carries, stderr included.

**May log:**
- Counts, sizes, durations, HTTP status codes, round and score summaries, map names, tickrates,
  parser version and restart counts.
- FACEIT match IDs (`docId`), LAN event IDs, and Drive file IDs and names from public event folders.
- cbbl responses from `/api/ingest/lan`, `/api/ingest/lan/sync` and `/api/ingest/valve` (counts, IDs,
  and a masked cursor), truncated as they already are.

## Rule 4: errors go through `safeError()`

Every `catch` that prints something, and every job entry point (`main()`), uses `safeError(e)`. It
keeps the first line of the message and the parser's own `cbbl-parse:` / `cbbl-highlight:` /
`warning:` lines, drops everything else from stderr, and redacts `key=`, `sig=`, `token=` and
`X-Amz-*` query values. If you need full parser diagnostics, send them to cbbl (a private
endpoint) or reproduce the problem locally. Don't print them here.

## Rule 5: triggers and permissions

- Only `repository_dispatch`, `schedule` and `workflow_dispatch`. **Never** `pull_request_target`,
  and no `pull_request` trigger on any job that uses a secret. Forks must never run with secrets.
- Every workflow declares `permissions:` and grants the minimum it needs (`contents: read` or `{}`).
- The worker re-checks every URL it fetches against an allowlist (`ALLOWED_HOSTS`,
  `assertValveReplay`, Drive URLs built from validated file IDs only). It must never act as an open
  fetch proxy.
- Dependencies are installed with `npm ci` from the committed `package-lock.json`. If you change
  `package.json`, regenerate the lockfile outside the monorepo workspace
  (`npm install --package-lock-only` in a standalone copy).

## Repository settings (manual, one-time)

- **Settings → Actions → General → Artifact and log retention: 1 day.** This limits how long any
  mistake stays public.
- **Settings → Actions → General → Fork pull request workflows**: require approval for all outside
  collaborators.
- Secrets hold everything sensitive, including `TRACKED_STEAM_IDS`. Variables (`vars.*`) are only for
  values that are safe to print.

## If something leaks

1. Delete the run's logs (open the run → ⋯ → *Delete all logs*), or delete the run.
2. Rotate the credential right away. Deleting the log does not undo a leak: public logs may already
   have been read or scraped. For `WORKER_SECRET`, update both the Vercel env and this repo's secret.
3. Fix the code path in the cbbl monorepo (`worker/`), then sync it here.

## Reporting

Report suspected vulnerabilities privately through GitHub (Security tab → *Report a vulnerability*).
Do not open a public issue.
