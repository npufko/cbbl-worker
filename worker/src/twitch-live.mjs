// Runs on GitHub Actions (public worker repo) every few minutes: the LIVE seal on member profiles.
// Asks cbbl for the members' Twitch handles, asks Twitch which of them are streaming, and posts the
// answer back (cbbl/apps/web/app/api/ingest/live). Only the worker talks to Twitch (cbbl rule 1);
// Vercel Hobby's cron runs once a day, far too slow for this.
// Twitch: an app access token (client credentials, no user login), then Get Streams with up to 100
// user_login per call. Free; a handful of members is one call per run.
// Logs are public: handles are masked as soon as they arrive, and only counts are printed (SECURITY.md).
import { mask, main } from './log-safety.mjs';

const { CBBL_URL, WORKER_SECRET, TWITCH_CLIENT_ID, TWITCH_CLIENT_SECRET } = process.env;
for (const [k, v] of Object.entries({ CBBL_URL, WORKER_SECRET })) {
  if (!v) throw new Error(`missing env ${k}`);
}
// Until the Twitch app's secrets are added, skip cleanly: a failing run every 5 minutes would only be noise.
if (!TWITCH_CLIENT_ID || !TWITCH_CLIENT_SECRET) {
  console.log('no Twitch app secrets yet: skipping');
  process.exit(0);
}
const cbbl = { Authorization: `Bearer ${WORKER_SECRET}`, 'Content-Type': 'application/json' };

async function twitchToken() {
  const q = new URLSearchParams({ client_id: TWITCH_CLIENT_ID, client_secret: TWITCH_CLIENT_SECRET, grant_type: 'client_credentials' });
  const res = await fetch(`https://id.twitch.tv/oauth2/token?${q}`, { method: 'POST', signal: AbortSignal.timeout(20_000) });
  if (!res.ok) throw new Error(`twitch token ${res.status}`);
  return mask((await res.json()).access_token);
}

main(async () => {
  const res = await fetch(`${CBBL_URL}/api/ingest/live`, { headers: cbbl, signal: AbortSignal.timeout(30_000) });
  // 404: the cbbl deploy does not have the route yet (it ships with the profiles release). Not an error.
  if (res.status === 404) { console.log('cbbl has no live route yet: skipping'); return; }
  if (!res.ok) throw new Error(`cbbl ${res.status}`);
  const handles = ((await res.json()).handles ?? []).map((h) => mask(String(h)));
  const live = [];
  if (handles.length) {
    const token = await twitchToken();
    for (let i = 0; i < handles.length; i += 100) {
      const q = new URLSearchParams(handles.slice(i, i + 100).map((h) => ['user_login', h]));
      const r = await fetch(`https://api.twitch.tv/helix/streams?${q}`, {
        headers: { 'Client-Id': TWITCH_CLIENT_ID, Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(20_000),
      });
      if (!r.ok) throw new Error(`twitch streams ${r.status}`);
      for (const s of (await r.json()).data ?? []) if (s.type === 'live') live.push({ login: s.user_login, startedAt: s.started_at });
    }
  }
  // Always report, even an empty list: that is how a finished stream loses its seal.
  const post = await fetch(`${CBBL_URL}/api/ingest/live`, { method: 'POST', headers: cbbl, body: JSON.stringify({ live }), signal: AbortSignal.timeout(30_000) });
  if (!post.ok) throw new Error(`cbbl report ${post.status}`);
  const { changed } = await post.json();
  console.log(`${handles.length} handles · ${live.length} live · ${changed} changed`);
});
