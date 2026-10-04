// Clipboard helper for vault secrets. Copied values are cleared from the
// clipboard after a delay. We don't read the clipboard back first
// (readText triggers a permission prompt in several browsers), so a value
// the user copied elsewhere within the window is cleared too.

const CLEAR_AFTER_MS = 30_000;

let clearTimer: ReturnType<typeof setTimeout> | null = null;

async function clearNow(): Promise<void> {
  clearTimer = null;
  try {
    await navigator.clipboard.writeText('');
  } catch {
    // Clipboard unavailable (e.g. document not focused) — nothing to do.
  }
}

/**
 * Copy a secret to the clipboard and schedule it to be cleared.
 * Throws when the clipboard API is unavailable, like writeText.
 */
export async function copySecret(value: string, clearAfterMs = CLEAR_AFTER_MS): Promise<void> {
  await navigator.clipboard.writeText(value);
  if (clearTimer) clearTimeout(clearTimer);
  clearTimer = setTimeout(() => void clearNow(), clearAfterMs);
}

/** Clear a pending vault secret from the clipboard right away (e.g. on lock). */
export async function clearCopiedSecret(): Promise<void> {
  if (!clearTimer) return;
  clearTimeout(clearTimer);
  await clearNow();
}
