/**
 * Promise-based replacement for the native `window.confirm()`.
 *
 *   if (!(await confirmDialog({ title: 'Delete token?', danger: true }))) return;
 *
 * A single <ConfirmDialog /> host mounted in App.svelte renders the
 * pending request. Only one dialog is shown at a time; opening a new
 * one while another is pending resolves the old one as `false`.
 */

export interface ConfirmOptions {
  title: string;
  message?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
}

interface PendingConfirm extends ConfirmOptions {
  resolve: (ok: boolean) => void;
}

let pending = $state<PendingConfirm | null>(null);

export const confirmState = {
  get current(): PendingConfirm | null {
    return pending;
  },
};

export function confirmDialog(opts: ConfirmOptions | string): Promise<boolean> {
  const options: ConfirmOptions = typeof opts === 'string' ? { title: opts } : opts;
  pending?.resolve(false);
  return new Promise<boolean>((resolve) => {
    pending = { ...options, resolve };
  });
}

export function settleConfirm(ok: boolean): void {
  const p = pending;
  pending = null;
  p?.resolve(ok);
}
