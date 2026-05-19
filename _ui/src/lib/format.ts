// Top-level formatting helpers shared across the UI.
//
// Lives at lib/ root (not under a feature folder) because at least
// five separate sites used to carry their own copy of formatSize:
//
//   - src/pages/Registries.svelte (formatSize + humanBytes — same fn, two names)
//   - src/lib/components/registry/types.ts
//   - src/lib/components/config/SettingsPanel.svelte
//   - src/lib/components/config/HexViewer.svelte
//   - src/lib/store/files.svelte.ts
//
// Centralising removes the drift risk and keeps behaviour
// uniform: "0 B" for zero, three decimals only when v < 10 and
// the unit isn't bytes, KB/MB/GB/TB scale at 1024.

/**
 * formatSize returns a humanised byte count ("1.4 MB", "823 B",
 * "0 B"). Accepts undefined / null for convenience at call sites
 * where the upstream payload may omit the field entirely; those
 * cases render as an empty string so the cell stays blank rather
 * than showing "0 B" or "NaN".
 *
 * Use formatBytes() when you specifically want "0 B" for a zero
 * input (file viewers, raw mount stats) rather than the empty
 * string.
 */
export function formatSize(n: number | undefined | null): string {
  if (n === undefined || n === null || n === 0 || Number.isNaN(n)) return '';
  return formatBytesNonZero(n);
}

/**
 * formatBytes is the same scale but always returns a renderable
 * string — "0 B" for zero, never empty. Use this in viewers /
 * panels that need to show a numeric size even when empty.
 */
export function formatBytes(n: number | undefined | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '0 B';
  if (n === 0) return '0 B';
  return formatBytesNonZero(n);
}

function formatBytesNonZero(n: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  // Show one decimal only when the value is in the low single
  // digits (e.g. "1.4 MB" but "147 MB"); never decimals for bytes
  // because fractional bytes don't exist.
  return v.toFixed(v < 10 && i > 0 ? 1 : 0) + ' ' + units[i];
}

/**
 * formatPublishedAt collapses an RFC3339 timestamp into a short
 * "YYYY-MM-DD" format for table cells. Returns '' for empty /
 * invalid inputs so the cell stays blank rather than rendering
 * "Invalid Date".
 */
export function formatPublishedAt(s: string | undefined | null): string {
  if (!s) return '';
  const d = new Date(s);
  if (isNaN(d.getTime())) return '';
  return d.toISOString().slice(0, 10);
}
