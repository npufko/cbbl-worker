// Premier / Matchmaking discovery. Runs on a schedule in the worker repo, never on Vercel:
// it needs a logged-in Steam session for the Game Coordinator, and STEAM_REFRESH_TOKEN must never
// reach Vercel. Vercel Hobby also allows one cron a day, which is far too slow for this.
//
// Walk: cbbl says where each player's share-code cursor is -> GetNextMatchSharingCode gives the
// next code -> the GC turns it into a public replay URL -> download, parse, hand both halves to
// cbbl, which builds the match and advances the cursor.
import { execFile } from 'node:child_process';
import { createWriteStream } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { pipeline } from 'node:stream/promises';
import { Readable } from 'node:stream';
import { promisify } from 'node:util';
import SteamUser from 'steam-user';
import GlobalOffensive from 'globaloffensive';

const run = promisify(execFile);
const { CBBL_URL, WORKER_SECRET, STEAM_API_KEY, MATCH_AUTH_CODE, STEAM_REFRESH_TOKEN, VALVE_SEED_CODE } = process.env;
for (const [k, v] of Object.entries({ CBBL_URL, WORKER_SECRET, STEAM_API_KEY, MATCH_AUTH_CODE, STEAM_REFRESH_TOKEN })) {
  if (!v) throw new Error(`missing env ${k}`);
}
const MAX_PER_RUN = Number(process.env.MAX_MATCHES_PER_RUN ?? 3); // polite to Valve, and keeps a run short
const auth = { Authorization: `Bearer ${WORKER_SECRET}`, 'Content-Type': 'application/json' };

// Valve replays are plain http on replay<N>.valve.net. That exact pattern is allowed and nothing
// else is: a blanket http allowance would turn this job into an open proxy over cleartext.
function assertValveReplay(url) {
  const u = new URL(url);
  const ok = (u.protocol === 'http:' || u.protocol === 'https:') && /^replay\d+\.valve\.net$/.test(u.hostname);
  if (!ok) throw new Error(`refusing non-Valve replay host: ${u.protocol}//${u.hostname}`);
}

async function nextShareCode(steamId, knownCode) {
  const q = new URLSearchParams({ key: STEAM_API_KEY, steamid: steamId, steamidkey: MATCH_AUTH_CODE, knowncode: knownCode });
  const res = await fetch(`https://api.steampowered.com/ICSGOPlayers_730/GetNextMatchSharingCode/v1?${q}`, { signal: AbortSignal.timeout(10_000) });
  if (res.status === 202) return null; // caught up
  if (!res.ok) throw new Error(`GetNextMatchSharingCode ${res.status}`);
  const next = (await res.json())?.result?.nextcode;
  return next && next !== 'n/a' ? next : null;
}

const report = async (body) => {
  const res = await fetch(`${CBBL_URL}/api/ingest/valve`, { method: 'POST', headers: auth, body: JSON.stringify(body) });
  console.log(`  cbbl ${res.status} ${(await res.text()).slice(0, 200)}`);
  return res.ok;
};

// ---- Steam / GC session, opened once and reused for every code this run ----
const user = new SteamUser();
const cs = new GlobalOffensive(user);
const gcReady = new Promise((resolve, reject) => {
  user.on('error', reject);
  user.on('loggedOn', () => user.gamesPlayed([730]));
  cs.on('connectedToGC', resolve);
  setTimeout(() => reject(new Error('GC did not connect within 90s')), 90_000).unref();
});

/** requestGame is fire-and-forget; matchList is the answer. */
function requestMatch(shareCode) {
  return new Promise((resolve, reject) => {
    const t = setTimeout(() => { cs.off('matchList', onList); reject(new Error('GC timed out')); }, 30_000);
    const onList = (matches) => { clearTimeout(t); cs.off('matchList', onList); resolve(matches?.[0] ?? null); };
    cs.on('matchList', onList);
    cs.requestGame(shareCode);
  });
}

user.logOn({ refreshToken: STEAM_REFRESH_TOKEN });
await gcReady;
console.log('GC connected');

const { players } = await (await fetch(`${CBBL_URL}/api/ingest/valve`, { headers: auth })).json();
console.log(`${players.length} tracked player(s)`);

let ingested = 0;
for (const { steamId, lastCode } of players) {
  let cursor = lastCode ?? VALVE_SEED_CODE;
  if (!cursor) { console.log(`${steamId}: no cursor and no VALVE_SEED_CODE — skipping`); continue; }

  for (let n = 0; n < MAX_PER_RUN; n++) {
    const code = await nextShareCode(steamId, cursor).catch((e) => { console.error(`  ${e.message}`); return null; });
    if (!code) break;
    console.log(`${steamId}: ${code}`);
    cursor = code;

    try {
      const m = await requestMatch(code);
      const url = m?.roundstatsall?.at(-1)?.map;
      if (!m || !url) { await report({ steamId, shareCode: code, skip: 'GC returned no match or no replay URL' }); continue; }
      assertValveReplay(url);

      const res = await fetch(url, { signal: AbortSignal.timeout(180_000) });
      if (!res.ok) { await report({ steamId, shareCode: code, skip: `replay download ${res.status} (expired?)` }); continue; }
      await pipeline(Readable.fromWeb(res.body), createWriteStream('demo.bin'));

      await run('./cbbl-parse', ['--in', 'demo.bin', '--out', 'map.json'], { maxBuffer: 64 << 20 });
      const map = JSON.parse(await readFile('map.json', 'utf8'));
      console.log(`  parsed ${map.map}: ${map.rounds?.length} rounds, restarts ${map.restarts ?? '?'}`);
      if (await report({ steamId, shareCode: code, gc: m, map, demoUrl: url })) ingested++;
    } catch (e) {
      // Not retryable in practice: a replay Valve has deleted never comes back.
      await report({ steamId, shareCode: code, skip: String(e.message ?? e).slice(0, 280) });
    }
  }
}

console.log(`done: ${ingested} match(es) ingested`);
user.logOff();
process.exit(0);
