
export function formatDate(value: string): string {
  return value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) : '-';
}

// sha256Hex produces a stable hex digest for locally registered demo evidence so
// two excursions never share the same sensor summary. Falls back to a FNV-style
// hash when WebCrypto is unavailable.
export async function sha256Hex(input: string): Promise<string> {
  try {
    if (globalThis.crypto?.subtle) {
      const buffer = await globalThis.crypto.subtle.digest('SHA-256', new TextEncoder().encode(input));
      return Array.from(new Uint8Array(buffer)).map((byte) => byte.toString(16).padStart(2, '0')).join('');
    }
  } catch { /* fall through to the deterministic fallback */ }
  let hash = 0x811c9dc5;
  let output = '';
  for (let round = 0; round < 64; round += 1) {
    hash ^= input.charCodeAt(round % input.length) + round;
    hash = Math.imul(hash, 0x01000193) >>> 0;
    output += hash.toString(16).padStart(8, '0');
  }
  return output.slice(0, 64);
}
export function nextStatus(current: string, statuses: readonly string[]): string | null {
  const index = statuses.indexOf(current);
  return index >= 0 && index < statuses.length - 1 ? statuses[index + 1] : null;
}
export function statusTone(status: string): 'success' | 'warning' | 'danger' | 'neutral' {
  if (/approved|accepted|released|completed|signed|closed|pass|ready|online|cleared|succeeded/.test(status)) return 'success';
  if (/failed|rejected|critical|scrap|discard|revoked|urgent/.test(status)) return 'danger';
  if (/hold|warning|review|pending|restricted|limited|quarantine/.test(status)) return 'warning';
  return 'neutral';
}
