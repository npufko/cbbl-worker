// Runs on GitHub Actions (public worker repo): download demo from a presigned/public URL, parse, POST results.
// Demos stream to the runner's disk and are deleted with the runner — never stored anywhere else.
import { execFile } from 'node:child_process';
import { createWriteStream } from 'node:fs';
import { readFile, stat } from 'node:fs/promises';
import { pipeline } from 'node:stream/promises';
import { Readable } from 'node:stream';
import { promisify } from 'node:util';
import { postClip } from './discord-clip.mjs';

const run = promisify(execFile);
const { DOC_ID, DEMO_URL, DEMO_NAME, CBBL_URL, WORKER_SECRET, ALLOWED_HOSTS, DISCORD_MATCHES_WEBHOOK, TRACKED_STEAM_IDS } = process.env;
if (!DOC_ID || !DEMO_URL || !CBBL_URL || !WORKER_SECRET) throw new Error('missing env');

// Defence in depth: the site already allowlists hosts, but the worker re-checks before fetching anything.
const host = new URL(DEMO_URL).hostname;
const allowed = (ALLOWED_HOSTS ?? '').split(',').map((h) => h.trim()).filter(Boolean);
if (!allowed.some((h) => host === h || host.endsWith(`.${h}`))) throw new Error(`host not allowed: ${host}`);

// FACEIT presigned links are S3-style and short-lived (observed X-Amz-Expires=299). Starting the download
// before expiry is enough; an expired link exits cleanly and the extension re-mints on its next poll.
const q = new URL(DEMO_URL).searchParams;
const signedAt = q.get('X-Amz-Date'), ttl = Number(q.get('X-Amz-Expires'));
if (signedAt && ttl) {
  const t = Date.UTC(+signedAt.slice(0, 4), +signedAt.slice(4, 6) - 1, +signedAt.slice(6, 8), +signedAt.slice(9, 11), +signedAt.slice(11, 13), +signedAt.slice(13, 15));
  const left = Math.round((t + ttl * 1000 - Date.now()) / 1000);
  console.log(`link expires in ${left}s`);
  if (left <= 5) { console.error('presigned link expired before the runner started; will be re-requested'); process.exit(3); }
}

const res = await fetch(DEMO_URL, { signal: AbortSignal.timeout(10 * 60_000) });
if (!res.ok || !res.body) throw new Error(`download ${res.status}`);
await pipeline(Readable.fromWeb(res.body), createWriteStream('demo.bin'));
console.log(`downloaded ${(await stat('demo.bin')).size} bytes`);

await run('./cbbl-parse', ['--in', 'demo.bin', '--out', 'map.json'], { maxBuffer: 64 << 20 });
const map = JSON.parse(await readFile('map.json', 'utf8'));

// Round-count mismatches against the FACEIT score are the one cross-check failure that needs the
// demo to explain it (knife round? a trailing post-match round?). Print enough to tell them apart.
{
  const r = map.rounds ?? [];
  const tally = r.reduce((acc, x) => ((acc[x.winner ?? '?'] = (acc[x.winner ?? '?'] ?? 0) + 1), acc), {});
  const brief = (x) => `n=${x.n} ${x.winner ?? '?'} reason=${x.reason} kills=${(x.kills ?? []).length} equip=[${x.equip ?? ''}]`;
  console.log(`parsed ${map.map}: ${r.length} rounds, winners ${JSON.stringify(tally)}, tickrate ${map.tickrate}, restarts ${map.restarts ?? '?'}`);
  for (const x of r.slice(0, 2)) console.log(`  first  ${brief(x)}`);
  for (const x of r.slice(-2)) console.log(`  last   ${brief(x)}`);
}

const post = await fetch(`${CBBL_URL}/api/ingest/parsed`, {
  method: 'POST',
  headers: { Authorization: `Bearer ${WORKER_SECRET}`, 'Content-Type': 'application/json' },
  // `demo` names which of the match's demos this was: a Bo3 has one per map.
  body: JSON.stringify({ docId: DOC_ID, maps: [map], demo: DEMO_NAME || new URL(DEMO_URL).pathname.split('/').pop() }),
});
console.log(`cbbl responded ${post.status}`);
if (!post.ok) process.exit(1);
const { firstParse } = await post.json().catch(() => ({ firstParse: false }));

// ---- Play of the match: best highlight among tracked players (falls back to the match's best) ----
if (firstParse && DISCORD_MATCHES_WEBHOOK) {
  try {
    const { stdout } = await run('./cbbl-highlight', ['--in', 'demo.bin', '--list'], { maxBuffer: 16 << 20 });
    const all = JSON.parse(stdout);
    const tracked = new Set((TRACKED_STEAM_IDS ?? '').split(',').map((x) => x.trim()).filter(Boolean));
    const pick = all.find((h) => tracked.has(h.steamId)) ?? all[0];
    if (pick) {
      await run('./cbbl-highlight', ['--in', 'demo.bin', '--steam', pick.steamId, '--out', 'highlight.mp4']);
      const secs = Math.round((pick.toTick - pick.fromTick) / (map.tickrate || 64));
      await postClip(DISCORD_MATCHES_WEBHOOK, 'highlight.mp4', `Play of the match: **${pick.name}**, ${pick.label}, round ${pick.round} (${secs}s, 2D replay)`);
      console.log(`posted highlight: ${pick.name} ${pick.label}`);
    }
  } catch (e) {
    // A clip is a bonus: never fail the parse job because of it.
    console.error(`highlight skipped: ${e.message}`);
  }
}
