// This repo is public, so every Actions log is world-readable. The rules live in SECURITY.md; these
// are the helpers that keep the jobs inside them.
import { readFileSync } from 'node:fs';

/** Redact `value` from every later line of this job's log. Mask first, then use. */
export function mask(value) {
  const v = String(value ?? '').trim();
  if (v) console.log(`::add-mask::${v}`);
  return value;
}

/**
 * The triggering event (client_payload for repository_dispatch, inputs for workflow_dispatch), read
 * from the runner's event file. Never pass these through a step's `env:`: Actions prints a step's env
 * values in its log header, unmasked, before any script gets the chance to mask them.
 */
export function eventPayload() {
  const path = process.env.GITHUB_EVENT_PATH;
  if (!path) return {};
  const e = JSON.parse(readFileSync(path, 'utf8'));
  return e.client_payload ?? e.inputs ?? {};
}

// Our own binaries prefix their fatal and warning lines; `self-check:` lines name players and
// SteamIDs, so they (and anything unprefixed) stay out of the log.
const SAFE_STDERR = /^(cbbl-parse|cbbl-highlight|warning):/;

/**
 * One short line that is safe to print for any error. A failed execFile carries the child's whole
 * stderr in its message; only our prefixed lines survive. URL keys are redacted as a backstop.
 */
export function safeError(e) {
  const lines = String(e?.message ?? e).split('\n');
  const head = lines[0].startsWith('Command failed:') ? `exit ${e?.code ?? '?'}` : lines[0];
  const kept = [head, ...lines.slice(1).filter((l) => SAFE_STDERR.test(l))];
  return kept.join(' | ').replace(/([?&](?:key|sig|signature|token|X-Amz-[A-Za-z]+)=)[^&\s]+/gi, '$1…').slice(0, 400);
}

/** Run the job; an uncaught error would print its stack and every enumerable field (stderr included). */
export function main(fn) {
  fn().catch((e) => {
    console.error(`failed: ${safeError(e)}`);
    process.exit(1);
  });
}
