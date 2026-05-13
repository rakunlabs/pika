<script lang="ts">
  import { Printer, Download, Copy, Check } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';

  interface Props {
    username: string;
    secretKey: string; // formatted, dashed
    kitID?: string;
    onAcknowledge?: () => void;
  }
  let { username, secretKey, kitID = '', onAcknowledge }: Props = $props();

  let acknowledged = $state(false);
  let copied = $state(false);

  // The kit prints a complete recovery sheet: username + Secret Key +
  // kit id, plus the instructions block. The browser's print dialog
  // is the path to a real PDF — we don't bundle a PDF generator.
  function printKit() {
    window.print();
  }

  // "Download" is a plain HTML file rendered through a blob URL. Same
  // content the print path uses, just standalone — useful when the
  // user wants to keep an offline copy on a USB drive.
  function downloadKit() {
    const html = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Pika Emergency Kit</title>
  <style>
    body { font-family: system-ui, -apple-system, sans-serif; max-width: 600px; margin: 2rem auto; padding: 1rem; color: #111; }
    h1 { font-size: 1.5rem; border-bottom: 2px solid #111; padding-bottom: 0.5rem; }
    .field { margin: 1.5rem 0; }
    .label { font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; color: #444; }
    .value { font-family: 'JetBrains Mono', 'Fira Code', ui-monospace, monospace; font-size: 1.25rem; padding: 0.5rem; background: #f5f5f5; border: 1px solid #ddd; word-break: break-all; }
    .warn { background: #fff8e1; border: 1px solid #f5b800; padding: 0.75rem; margin: 1.5rem 0; font-size: 0.875rem; }
  </style>
</head>
<body>
  <h1>Pika Emergency Kit</h1>
  <p>Print this page and store it somewhere safe (a fireproof safe, a deposit box). Anyone with both the Secret Key below AND your master password can read your vault — keep them separate.</p>
  <div class="field">
    <div class="label">Username</div>
    <div class="value">${escapeHTML(username)}</div>
  </div>
  <div class="field">
    <div class="label">Secret Key</div>
    <div class="value">${escapeHTML(secretKey)}</div>
  </div>
  ${kitID ? `<div class="field"><div class="label">Kit ID</div><div class="value">${escapeHTML(kitID)}</div></div>` : ''}
  <div class="warn">
    <strong>Master password</strong>: not printed here. You memorize it.<br>
    If you lose <em>both</em> the master password and this Secret Key, your vault cannot be recovered.
  </div>
</body>
</html>`;
    const blob = new Blob([html], { type: 'text/html' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `pika-emergency-kit-${username}.html`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }

  async function copyKey() {
    try {
      await navigator.clipboard.writeText(secretKey);
      copied = true;
      setTimeout(() => (copied = false), 2000);
      addToast('Secret Key copied to clipboard', 'success');
    } catch {
      addToast('Clipboard unavailable; print or download instead', 'warn');
    }
  }

  function escapeHTML(s: string): string {
    return s.replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c] ?? c));
  }
</script>

<div class="space-y-4">
  <div class="bg-amber-50 dark:bg-amber-950/30 border border-amber-300 dark:border-amber-700 rounded p-3 text-sm">
    <p class="font-semibold text-amber-900 dark:text-amber-200 mb-1">
      Save this Secret Key now. It is shown only once.
    </p>
    <p class="text-amber-800 dark:text-amber-200">
      You will need both your master password <em>and</em> this Secret Key to unlock your vault on another device.
      If you lose both, the vault cannot be recovered.
    </p>
  </div>

  <div>
    <div class="text-xs uppercase tracking-wider text-slate-500 dark:text-slate-400 mb-1">Username</div>
    <div class="font-mono text-sm bg-slate-100 dark:bg-warm-800 px-3 py-2 rounded border border-slate-200 dark:border-warm-700">
      {username}
    </div>
  </div>

  <div>
    <div class="text-xs uppercase tracking-wider text-slate-500 dark:text-slate-400 mb-1">Secret Key</div>
    <div class="font-mono text-base bg-slate-100 dark:bg-warm-800 px-3 py-2 rounded border border-slate-200 dark:border-warm-700 break-all select-all">
      {secretKey}
    </div>
  </div>

  {#if kitID}
    <div>
      <div class="text-xs uppercase tracking-wider text-slate-500 dark:text-slate-400 mb-1">Kit ID</div>
      <div class="font-mono text-xs bg-slate-100 dark:bg-warm-800 px-3 py-2 rounded border border-slate-200 dark:border-warm-700 break-all">
        {kitID}
      </div>
    </div>
  {/if}

  <div class="flex flex-wrap gap-2">
    <button
      onclick={copyKey}
      class="flex items-center gap-1.5 px-3 py-2 text-sm rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 border border-slate-200 dark:border-warm-700 cursor-pointer"
    >
      {#if copied}<Check size={14} class="text-emerald-600" /> Copied{:else}<Copy size={14} /> Copy key{/if}
    </button>
    <button
      onclick={printKit}
      class="flex items-center gap-1.5 px-3 py-2 text-sm rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 border border-slate-200 dark:border-warm-700 cursor-pointer"
    >
      <Printer size={14} /> Print
    </button>
    <button
      onclick={downloadKit}
      class="flex items-center gap-1.5 px-3 py-2 text-sm rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 border border-slate-200 dark:border-warm-700 cursor-pointer"
    >
      <Download size={14} /> Download HTML
    </button>
  </div>

  {#if onAcknowledge}
    <label class="flex items-start gap-2 text-sm cursor-pointer">
      <input
        type="checkbox"
        bind:checked={acknowledged}
        class="mt-0.5 cursor-pointer"
      />
      <span>I have saved my Secret Key in a safe place. I understand the vault cannot be recovered without it.</span>
    </label>
    <button
      onclick={onAcknowledge}
      disabled={!acknowledged}
      class="px-4 py-2 text-sm rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
    >
      Continue to vault
    </button>
  {/if}
</div>
