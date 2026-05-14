<script lang="ts">
  import { Printer, Download, Copy, Check, FileDown, ShieldAlert } from 'lucide-svelte';
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

  // Track whether the user has actually exported the Secret Key in
  // any form. We require at least one of copy / print / pdf /
  // download before allowing the "I have saved" checkbox to flip —
  // otherwise users muscle-memory through the acknowledgement
  // without ever having a copy of the key, then can't unlock on
  // the next device.
  let didExport = $state(false);

  const issuedAt = new Date().toLocaleString(undefined, {
    year: 'numeric', month: 'long', day: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });

  // The Print path runs the browser's native print dialog. Modern
  // browsers offer "Save as PDF" in that dialog on every desktop OS,
  // which gives the user a true PDF without any client-side PDF
  // library bloating the bundle. The kit-print section (further
  // down) is hidden on screen and exposed only when printing.
  function printKit() {
    didExport = true;
    window.print();
  }

  // Save-as-PDF is just the print path with an explicit hint. We
  // keep a distinct button label because users searching for "PDF"
  // will not click "Print" — even though under the hood it is the
  // same dialog.
  function savePdf() {
    didExport = true;
    addToast('In the print dialog, choose "Save as PDF" as the destination', 'success', 4500);
    // Give the toast a moment to render before the modal print
    // dialog takes focus away.
    setTimeout(() => window.print(), 250);
  }

  // Standalone HTML the user can stash on a USB drive. Same content
  // the print path produces, but self-contained — opens in any
  // browser, can be re-printed later, survives offline use.
  function downloadHtml() {
    const html = renderStandaloneHtml();
    const blob = new Blob([html], { type: 'text/html' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `pika-emergency-kit-${slugify(username)}.html`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
    didExport = true;
  }

  async function copyKey() {
    try {
      await navigator.clipboard.writeText(secretKey);
      copied = true;
      didExport = true;
      setTimeout(() => { copied = false; }, 2000);
      addToast('Secret Key copied to clipboard', 'success');
    } catch {
      addToast('Clipboard unavailable; print, save as PDF, or download instead', 'warn');
    }
  }

  function escapeHTML(s: string): string {
    return s.replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c] ?? c));
  }

  function slugify(s: string): string {
    return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'user';
  }

  // Self-contained HTML for the Download button. Mirrors the
  // on-print layout so a printed kit and a downloaded-then-printed
  // kit look identical.
  function renderStandaloneHtml(): string {
    return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Pika Emergency Kit — ${escapeHTML(username)}</title>
<style>
  :root {
    --ink: #0f172a;
    --muted: #475569;
    --line: #cbd5e1;
    --accent: #EF233C;
    --accent-soft: #fef0f2;
    --warn-bg: #fef3c7;
    --warn-bd: #f59e0b;
    --warn-ink: #78350f;
  }
  * { box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
    color: var(--ink);
    margin: 0;
    padding: 2.5rem 2rem;
    background: #ffffff;
    /* Force the brand red and pill backgrounds to print on
       Chrome/Edge; otherwise the downloaded-then-printed kit would
       come out colorless. */
    -webkit-print-color-adjust: exact;
    print-color-adjust: exact;
  }
  .sheet { max-width: 720px; margin: 0 auto; }
  .brand {
    display: flex; align-items: center; gap: 0.75rem;
    padding-bottom: 1rem;
    border-bottom: 2px solid var(--ink);
    margin-bottom: 1.5rem;
  }
  /* Brand mark is the same Lucide "Blocks" icon used in the app's
     Navbar (Navbar.svelte:61) — kept inline as raw SVG so the
     standalone HTML doesn't need any external asset. Stroke color
     matches the in-app brand color #EF233C. */
  .brand-mark {
    width: 44px; height: 44px;
    display: flex; align-items: center; justify-content: center;
    color: var(--accent);
  }
  .brand-mark svg { width: 100%; height: 100%; }
  .brand-text { line-height: 1.1; }
  .brand-title { font-size: 1.35rem; font-weight: 700; letter-spacing: -0.01em; }
  .brand-sub { font-size: 0.85rem; color: var(--muted); margin-top: 0.15rem; }
  .lede {
    font-size: 0.95rem; line-height: 1.55; color: var(--muted);
    margin: 0 0 1.5rem;
  }
  .field { margin: 1.25rem 0; }
  .label {
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--muted);
    margin-bottom: 0.4rem;
    display: flex; align-items: baseline; gap: 0.5rem;
  }
  .label .pill {
    display: inline-block;
    padding: 1px 6px;
    border-radius: 999px;
    background: var(--accent-soft);
    color: var(--accent);
    font-size: 0.6rem;
    font-weight: 600;
    letter-spacing: 0.05em;
  }
  .value {
    font-family: "JetBrains Mono", "Fira Code", "SF Mono", Menlo, Consolas, monospace;
    font-size: 1.05rem;
    padding: 0.85rem 1rem;
    background: #f8fafc;
    border: 1px solid var(--line);
    border-radius: 8px;
    word-break: break-all;
    line-height: 1.5;
  }
  .value.secret { font-size: 1.15rem; letter-spacing: 0.02em; }
  .handwrite {
    height: 2.5rem;
    border: 1px dashed var(--line);
    border-radius: 8px;
    background:
      repeating-linear-gradient(
        to bottom,
        transparent 0,
        transparent 1.2rem,
        #e2e8f0 1.2rem,
        #e2e8f0 calc(1.2rem + 1px)
      );
  }
  .warn {
    background: var(--warn-bg);
    border: 1px solid var(--warn-bd);
    color: var(--warn-ink);
    padding: 1rem 1.1rem;
    border-radius: 8px;
    margin: 1.5rem 0 0.5rem;
    font-size: 0.85rem;
    line-height: 1.5;
  }
  .warn strong { color: var(--warn-ink); }
  .footer {
    margin-top: 2.25rem;
    padding-top: 1rem;
    border-top: 1px solid var(--line);
    display: flex; justify-content: space-between;
    color: var(--muted); font-size: 0.75rem;
  }
  @media print {
    body { padding: 0; }
    .sheet { padding: 1.5rem; }
  }
</style>
</head>
<body>
  <div class="sheet">
    <div class="brand">
      <div class="brand-mark">
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M10 22V7a1 1 0 0 0-1-1H4a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-5a1 1 0 0 0-1-1H2"/>
          <rect x="14" y="2" width="8" height="8" rx="1"/>
        </svg>
      </div>
      <div class="brand-text">
        <div class="brand-title">Pika Emergency Kit</div>
        <div class="brand-sub">Recovery sheet — keep offline, keep safe</div>
      </div>
    </div>

    <p class="lede">
      Anyone with <strong>both</strong> the Secret Key below <strong>and</strong> your master password
      can read your vault. Store this sheet somewhere physical and private: a fireproof safe, a
      bank deposit box, a sealed envelope at home. Do <strong>not</strong> store it together with
      a written copy of your master password.
    </p>

    <div class="field">
      <div class="label">Username</div>
      <div class="value">${escapeHTML(username)}</div>
    </div>

    <div class="field">
      <div class="label">Secret Key <span class="pill">required to unlock on new devices</span></div>
      <div class="value secret">${escapeHTML(secretKey)}</div>
    </div>

    ${kitID ? `<div class="field">
      <div class="label">Kit ID</div>
      <div class="value">${escapeHTML(kitID)}</div>
    </div>` : ''}

    <div class="field">
      <div class="label">Master password <span class="pill">write below in pen — never digitally</span></div>
      <div class="handwrite"></div>
    </div>

    <div class="warn">
      <strong>If you lose both</strong> the master password and this Secret Key,
      your vault cannot be recovered — not by you, not by an administrator. The server
      stores only encrypted ciphertext.
    </div>

    <div class="footer">
      <span>Issued ${escapeHTML(issuedAt)}</span>
      <span>pika · personal vault</span>
    </div>
  </div>
</body>
</html>`;
  }
</script>

<!-- Screen view: a compact, action-driven panel. The print path
     uses the .kit-print section further down, which is invisible on
     screen and unconstrained on paper. -->
<div class="space-y-4 print:hidden">
  <div class="bg-amber-50 dark:bg-amber-950/30 border border-amber-300 dark:border-amber-700 rounded p-3 text-sm flex gap-2">
    <ShieldAlert size={18} class="text-amber-700 dark:text-amber-300 shrink-0 mt-0.5" />
    <div>
      <p class="font-semibold text-amber-900 dark:text-amber-200 mb-1">
        Save this Secret Key now. It is shown only once.
      </p>
      <p class="text-amber-800 dark:text-amber-200">
        You will need both your master password <em>and</em> this Secret Key to unlock your vault on
        another device. If you lose both, the vault cannot be recovered.
      </p>
    </div>
  </div>

  <div>
    <div class="text-xs uppercase tracking-wider text-slate-500 dark:text-slate-400 mb-1">Username</div>
    <div class="font-mono text-sm bg-slate-100 dark:bg-warm-800 px-3 py-2 rounded border border-slate-200 dark:border-warm-700">
      {username}
    </div>
  </div>

  <div>
    <div class="text-xs uppercase tracking-wider text-slate-500 dark:text-slate-400 mb-1">Secret Key</div>
    <div class="font-mono text-base bg-slate-100 dark:bg-warm-800 px-3 py-2 rounded border border-slate-200 dark:border-warm-700 break-all select-all tracking-wider">
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

  <div class="pt-2">
    <div class="text-xs uppercase tracking-wider text-slate-500 dark:text-slate-400 mb-2">
      Save your Secret Key — pick at least one
    </div>
    <div class="grid grid-cols-2 sm:grid-cols-4 gap-2">
      <!-- Save as PDF is the recommended action: it produces a
           true PDF via the browser's print → "Save as PDF"
           destination, which every modern browser supports without
           any JS PDF library in the bundle. -->
      <button
        onclick={savePdf}
        class="flex items-center justify-center gap-1.5 px-3 py-2 text-sm rounded bg-accent-600 text-white font-medium hover:bg-accent-700 border border-accent-600 cursor-pointer"
        title="Save as PDF — opens the print dialog, choose 'Save as PDF' as the destination"
      >
        <FileDown size={14} /> PDF
      </button>
      <button
        onclick={downloadHtml}
        class="flex items-center justify-center gap-1.5 px-3 py-2 text-sm rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 border border-slate-200 dark:border-warm-700 cursor-pointer"
        title="Download as a self-contained HTML file you can store offline"
      >
        <Download size={14} /> HTML
      </button>
      <button
        onclick={printKit}
        class="flex items-center justify-center gap-1.5 px-3 py-2 text-sm rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 border border-slate-200 dark:border-warm-700 cursor-pointer"
      >
        <Printer size={14} /> Print
      </button>
      <button
        onclick={copyKey}
        class="flex items-center justify-center gap-1.5 px-3 py-2 text-sm rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 border border-slate-200 dark:border-warm-700 cursor-pointer"
      >
        {#if copied}<Check size={14} class="text-emerald-600" /> Copied{:else}<Copy size={14} /> Copy key{/if}
      </button>
    </div>
    <p class="mt-2 text-[11px] text-slate-500 dark:text-slate-400">
      <strong>PDF</strong> uses your browser's print dialog — select "Save as PDF" as the destination.
    </p>
  </div>

  {#if onAcknowledge}
    <!-- Two-step gate: the user must actually export the key in some
         form before they can even check the acknowledgement box, and
         the box must be checked before Continue activates. -->
    {#if !didExport}
      <div class="text-xs text-slate-500 dark:text-slate-400 italic">
        Save, download, print, or copy the Secret Key above to enable the confirmation below.
      </div>
    {/if}
    <label class="flex items-start gap-2 text-sm {didExport ? 'cursor-pointer' : 'cursor-not-allowed opacity-50'}">
      <input
        type="checkbox"
        bind:checked={acknowledged}
        disabled={!didExport}
        class="mt-0.5 {didExport ? 'cursor-pointer' : 'cursor-not-allowed'}"
      />
      <span>I have saved my Secret Key in a safe place. I understand the vault cannot be recovered without it.</span>
    </label>
    <button
      onclick={onAcknowledge}
      disabled={!acknowledged || !didExport}
      class="px-4 py-2 text-sm rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
    >
      Continue to vault
    </button>
  {/if}
</div>

<!-- Print-only section. Hidden on screen, full-page on paper or
     "Save as PDF". A page-level @media print rule (below) hides
     every other element on the page so the PDF contains ONLY the
     kit, regardless of which app frame the user printed from. -->
<div class="kit-print hidden print:block">
  <div class="kit-sheet">
    <header class="kit-brand">
      <div class="kit-mark" aria-hidden="true">
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M10 22V7a1 1 0 0 0-1-1H4a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-5a1 1 0 0 0-1-1H2"/>
          <rect x="14" y="2" width="8" height="8" rx="1"/>
        </svg>
      </div>
      <div class="kit-brand-text">
        <div class="kit-title">Pika Emergency Kit</div>
        <div class="kit-sub">Recovery sheet — keep offline, keep safe</div>
      </div>
    </header>

    <p class="kit-lede">
      Anyone with <strong>both</strong> the Secret Key below <strong>and</strong> your master password
      can read your vault. Store this sheet somewhere physical and private: a fireproof safe,
      a bank deposit box, a sealed envelope at home. Do <strong>not</strong> store it together
      with a written copy of your master password.
    </p>

    <div class="kit-field">
      <div class="kit-label">Username</div>
      <div class="kit-value">{username}</div>
    </div>

    <div class="kit-field">
      <div class="kit-label">
        Secret Key
        <span class="kit-pill">required to unlock on new devices</span>
      </div>
      <div class="kit-value kit-secret">{secretKey}</div>
    </div>

    {#if kitID}
      <div class="kit-field">
        <div class="kit-label">Kit ID</div>
        <div class="kit-value">{kitID}</div>
      </div>
    {/if}

    <div class="kit-field">
      <div class="kit-label">
        Master password
        <span class="kit-pill">write below in pen — never digitally</span>
      </div>
      <div class="kit-handwrite"></div>
    </div>

    <div class="kit-warn">
      <strong>If you lose both</strong> the master password and this Secret Key,
      your vault cannot be recovered — not by you, not by an administrator.
      The server stores only encrypted ciphertext.
    </div>

    <footer class="kit-footer">
      <span>Issued {issuedAt}</span>
      <span>pika · personal vault</span>
    </footer>
  </div>
</div>

<style>
  /* Print rules:
     - hide every element on the page by default
     - reveal only the kit-print branch (and its descendants)
     The .print:hidden / .print:block Tailwind utilities above
     already toggle the on-screen views; this block makes sure the
     surrounding app chrome (navbar, sidebar, modal frame) disappear
     from the printed sheet too. */
  :global(body.kit-printing) { background: white !important; }

  @media print {
    :global(body) { background: white !important; }
    /* The :global(*) selector is intentional: the print path nukes
       every sibling of the kit sheet so the PDF is just the kit. */
    :global(body *) { visibility: hidden !important; }
    .kit-print, .kit-print * { visibility: visible !important; }
    .kit-print {
      position: absolute;
      inset: 0;
      padding: 1.25rem 1.5rem;
      background: white;
      color: #0f172a;
    }
  }

  /* Kit sheet styles — used by the @media print branch above. They
     stay in this component (not Tailwind) so they survive even if
     the dark-mode classes leak into print.

     print-color-adjust:exact is required for Chrome/Edge to print
     the brand red, the pill background, and the warn-box yellow;
     without it, those would render as white-on-white in the PDF
     and the document would look broken. */
  .kit-sheet {
    max-width: 720px;
    margin: 0 auto;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
    color: #0f172a;
    -webkit-print-color-adjust: exact;
    print-color-adjust: exact;
  }
  .kit-brand {
    display: flex; align-items: center; gap: 0.75rem;
    padding-bottom: 1rem;
    border-bottom: 2px solid #0f172a;
    margin-bottom: 1.5rem;
  }
  /* Brand mark matches the in-app Navbar: Lucide "Blocks" stroked
     in the brand red (#EF233C). Pure SVG so it scales crisp at any
     PDF resolution. */
  .kit-mark {
    width: 44px; height: 44px;
    display: flex; align-items: center; justify-content: center;
    color: #EF233C;
  }
  .kit-mark svg { width: 100%; height: 100%; }
  .kit-brand-text { line-height: 1.1; }
  .kit-title { font-size: 1.35rem; font-weight: 700; letter-spacing: -0.01em; }
  .kit-sub { font-size: 0.85rem; color: #475569; margin-top: 0.15rem; }
  .kit-lede {
    font-size: 0.95rem; line-height: 1.55; color: #475569;
    margin: 0 0 1.5rem;
  }
  .kit-field { margin: 1.25rem 0; }
  .kit-label {
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: #475569;
    margin-bottom: 0.4rem;
    display: flex; align-items: baseline; gap: 0.5rem;
  }
  .kit-pill {
    display: inline-block;
    padding: 1px 6px;
    border-radius: 999px;
    background: #fef0f2;
    color: #EF233C;
    font-size: 0.6rem;
    font-weight: 600;
    letter-spacing: 0.05em;
  }
  .kit-value {
    font-family: "JetBrains Mono", "Fira Code", "SF Mono", Menlo, Consolas, monospace;
    font-size: 1.05rem;
    padding: 0.85rem 1rem;
    background: #f8fafc;
    border: 1px solid #cbd5e1;
    border-radius: 8px;
    word-break: break-all;
    line-height: 1.5;
  }
  .kit-secret { font-size: 1.15rem; letter-spacing: 0.02em; }
  .kit-handwrite {
    height: 2.5rem;
    border: 1px dashed #cbd5e1;
    border-radius: 8px;
    background:
      repeating-linear-gradient(
        to bottom,
        transparent 0,
        transparent 1.2rem,
        #e2e8f0 1.2rem,
        #e2e8f0 calc(1.2rem + 1px)
      );
  }
  .kit-warn {
    background: #fef3c7;
    border: 1px solid #f59e0b;
    color: #78350f;
    padding: 1rem 1.1rem;
    border-radius: 8px;
    margin: 1.5rem 0 0.5rem;
    font-size: 0.85rem;
    line-height: 1.5;
  }
  .kit-footer {
    margin-top: 2.25rem;
    padding-top: 1rem;
    border-top: 1px solid #cbd5e1;
    display: flex; justify-content: space-between;
    color: #475569; font-size: 0.75rem;
  }
</style>
