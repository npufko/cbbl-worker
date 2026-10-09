// Runs on GitHub Actions (public worker repo) while a LAN event is live: calls cbbl's LAN sync every
// few minutes, which lists the event's Drive folder, dispatches new demos to lan-parse and settles
// finished series. GitHub's cron is best-effort (lan-poll's */10 fired 22 times in 10 days), so this
// one job keeps the cadence instead, and re-dispatches itself before Actions' 6 h job limit.
// Started by cbbl (repository_dispatch lan-live) when it finds a live event and no loop checking in.
// Logs are public: only counts and statuses are printed (SECURITY.md).
import { safeError, main } from './log-safety.mjs';

const { CBBL_URL, WORKER_SECRET, GH_TOKEN, GITHUB_REPOSITORY, GITHUB_REF_NAME } = process.env;
const GITHUB_API = process.env.GITHUB_API_URL ?? 'https://api.github.com'; // set by Actions
if (!CBBL_URL || !WORKER_SECRET) throw new Error('missing env');

const EVERY_MS = Number(process.env.LAN_LIVE_EVERY_MS ?? 180_000); // 3 min between syncs
const RUN_FOR_MS = Number(process.env.LAN_LIVE_RUN_FOR_MS ?? 5.5 * 3_600_000); // then hand over to a new run
const IDLE_CALLS = 2; // nothing live this many calls in a row: stop (cbbl starts a new loop when needed)
const MAX_FAILURES = 15; // ~45 min of cbbl not answering: stop; lan-poll's sync restarts the loop
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function sync() {
  const res = await fetch(`${CBBL_URL}/api/ingest/lan/sync`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${WORKER_SECRET}`, 'x-cbbl-loop': 'lan-live' },
    signal: AbortSignal.timeout(90_000),
  });
  if (!res.ok) throw new Error(`cbbl ${res.status}`);
  const { events = [] } = await res.json();
  return events;
}

/** Start the next run of this workflow (GITHUB_TOKEN may start workflow_dispatch runs). */
async function handOver() {
  if (!GH_TOKEN || !GITHUB_REPOSITORY) throw new Error('cannot re-dispatch: no token');
  const res = await fetch(`${GITHUB_API}/repos/${GITHUB_REPOSITORY}/actions/workflows/lan-live.yml/dispatches`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${GH_TOKEN}`, Accept: 'application/vnd.github+json', 'X-GitHub-Api-Version': '2022-11-28' },
    body: JSON.stringify({ ref: GITHUB_REF_NAME || 'main' }),
  });
  console.log(`handed over to a new run: GitHub ${res.status}`);
}

main(async () => {
  const until = Date.now() + RUN_FOR_MS;
  let idle = 0, failures = 0, calls = 0;
  while (Date.now() < until) {
    const started = Date.now();
    try {
      const events = await sync();
      calls++;
      failures = 0;
      const live = events.filter((e) => e.status === 'live');
      const sum = (k) => events.reduce((n, e) => n + (Number(e[k]) || 0), 0);
      console.log(`${new Date().toISOString().slice(11, 19)} sync ${calls}: ${live.length} live · ${sum('newDemos')} new demos · ${sum('dispatched')} dispatched · ${sum('promoted')} promoted · ${sum('waiting')} waiting · ${sum('review')} review${events.some((e) => e.held) ? ' · held' : ''}`);
      idle = events.length === 0 ? idle + 1 : 0;
      if (idle >= IDLE_CALLS) { console.log('nothing live: stopping'); return; }
    } catch (e) {
      failures++;
      console.log(`sync failed (${failures} in a row): ${safeError(e)}`);
      if (failures >= MAX_FAILURES) throw new Error('cbbl did not answer for too long');
    }
    await sleep(Math.max(0, EVERY_MS - (Date.now() - started)));
  }
  await handOver();
});
