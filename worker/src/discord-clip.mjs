// Upload a rendered highlight to a Discord channel webhook (multipart). Webhooks are free and need no bot.
// Discord's default upload limit is 10 MB; cbbl clips are typically 0.2–1 MB.
import { readFile } from 'node:fs/promises';

export async function postClip(webhookUrl, filePath, content, f = fetch) {
  const bytes = await readFile(filePath);
  if (bytes.length > 9.5 * 1024 * 1024) throw new Error(`clip too large for Discord: ${bytes.length} bytes`);
  const form = new FormData();
  form.append('payload_json', JSON.stringify({ content, allowed_mentions: { parse: [] } }));
  form.append('files[0]', new Blob([bytes], { type: 'video/mp4' }), 'highlight.mp4');
  const res = await f(`${webhookUrl}?wait=true`, { method: 'POST', body: form, signal: AbortSignal.timeout(30_000) });
  if (!res.ok) throw new Error(`Discord upload ${res.status}: ${await res.text()}`);
  return (await res.json()).id;
}
