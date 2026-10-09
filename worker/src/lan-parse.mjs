// Runs on GitHub Actions (public worker repo), dispatched by cbbl's LAN sync with a batch of Drive
// files grouped by map: download a map's public demo files from Drive, parse them together, POST the
// result to /api/ingest/lan. One map's files on disk at a time, deleted before the next. Demos are never stored.
import { execFile } from 'node:child_process';
import { createWriteStream } from 'node:fs';
import { readFile, rm, stat } from 'node:fs/promises';
import { pipeline } from 'node:stream/promises';
import { Readable } from 'node:stream';
import { promisify } from 'node:util';
import { eventPayload, safeError } from './log-safety.mjs';

const run = promisify(execFile);
const { CBBL_URL, WORKER_SECRET, GOOGLE_DRIVE_API_KEY } = process.env;
const { eventId: EVENT_ID, files: FILES } = eventPayload(); // event file, not env: see log-safety.mjs
if (!EVENT_ID || !Array.isArray(FILES) || !CBBL_URL || !WORKER_SECRET || !GOOGLE_DRIVE_API_KEY) throw new Error('missing env');

// The dispatch payload only ever names Drive file ids; the URL is built here, so this job can never
// be pointed at another host.
const files = FILES.filter((f) => /^[A-Za-z0-9_-]{10,100}$/.test(f?.fileId ?? ''));

async function report(fileId, body) {
  const res = await fetch(`${CBBL_URL}/api/ingest/lan`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${WORKER_SECRET}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ eventId: EVENT_ID, fileId, ...body }),
  });
  const text = await res.text();
  console.log(`  cbbl ${res.status} ${text.slice(0, 240)}`);
  return res.ok;
}

/**
 * Drive turning downloads away is not the demo's fault: cbbl queues it again. 'quota': the file
 * OWNER's download quota is used up (every file of theirs is refused, to anyone, for about a day), so
 * cbbl pauses that owner. 'busy': a short block ("automated queries", a rate limit, 429 or 5xx).
 * null: a real failure (a file no longer shared, say).
 */
async function driveRefusal(res) {
  if (res.status === 429 || res.status >= 500) return 'busy';
  if (res.status !== 403) return null;
  const text = (await res.text().catch(() => '')).slice(0, 4000);
  if (/downloadQuotaExceeded|quotaExceeded/i.test(text)) return 'quota';
  return /automated queries|rateLimitExceeded|userRateLimitExceeded/i.test(text) ? 'busy' : null;
}

// One map per group: its recording segments (FRAG: <map>_<id>.dem, then _1 …), in the order sent,
// parsed together in one cbbl-parse run and reported once, on the last file. Up to a few segments
// of ~300 MB each are on disk at once, deleted before the next map.
const groups = [];
for (const f of files) {
  const key = f.group ?? f.fileId;
  const last = groups.at(-1);
  if (last && last.key === key) last.files.push(f);
  else groups.push({ key, files: [f] });
}

let failures = 0;
let reported = 0;
for (const [gi, g] of groups.entries()) {
  const target = g.files.at(-1);
  const merged = g.files.slice(0, -1).map((f) => f.fileId);
  const paths = g.files.map((_, i) => `lan-${i}.dem`);
  console.log(g.files.map((f) => f.name ?? f.fileId).join(' + '));
  try {
    let refused = null, kind = null;
    for (const [i, { fileId }] of g.files.entries()) {
      const url = `https://www.googleapis.com/drive/v3/files/${fileId}?alt=media&key=${encodeURIComponent(GOOGLE_DRIVE_API_KEY)}`;
      const res = await fetch(url, { signal: AbortSignal.timeout(15 * 60_000) });
      if (!res.ok && (kind = await driveRefusal(res))) { refused = res.status; break; }
      if (!res.ok || !res.body) throw new Error(`Drive download ${res.status}`);
      await pipeline(Readable.fromWeb(res.body), createWriteStream(paths[i]));
      console.log(`  downloaded ${(await stat(paths[i])).size} bytes`);
    }
    if (refused !== null) {
      // Stop here rather than hammer Drive: this map and the rest of the batch go back in the queue.
      const why = kind === 'quota' ? `Drive download quota used up (${refused}); queued again` : `Drive refused the download (${refused}); queued again`;
      const rest = groups.slice(gi);
      console.error(`  ${why}: handing back ${rest.length} map(s)`);
      for (const r of rest) {
        await report(r.files.at(-1).fileId, { error: why, retry: true, quota: kind === 'quota', merged: r.files.slice(0, -1).map((f) => f.fileId) }).catch(() => {});
      }
      break;
    }

    await run('./cbbl-parse', [...paths.flatMap((x) => ['--in', x]), '--out', 'lan.json'], { maxBuffer: 64 << 20 });
    const map = JSON.parse(await readFile('lan.json', 'utf8'));
    const teams = (map.teams ?? []).map((t) => `${t.name || '?'} ${t.score}`).join(' vs ');
    console.log(`  parsed ${map.map}: ${teams}, ${map.rounds?.length} rounds, restarts ${map.restarts}, restores ${map.restores}, log v${map.logVersion}`);
    if (await report(target.fileId, { map, merged })) reported++;
    else failures++;
  } catch (e) {
    failures++;
    // Never echo the URL (it carries the API key) or the parser's self-check lines.
    const why = safeError(e);
    console.error(`  failed: ${why}`);
    await report(target.fileId, { error: why, merged }).catch(() => {});
  } finally {
    for (const x of paths) await rm(x, { force: true });
    await rm('lan.json', { force: true });
  }
}
console.log(`done: ${reported}/${groups.length} maps reported`);
if (failures) process.exit(1);
