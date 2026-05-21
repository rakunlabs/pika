<script lang="ts">
 // Test tab — server-side HTTP playground for proxy endpoints.
 //
 // Why server-side: every proxy listener lives on its own port,
 // CORS doesn't allow the SPA (served from pika's admin port) to
 // call them from the browser. We POST the request through
 // /api/v1/proxy/test which forwards it from the server and
 // returns the response shape verbatim.
 //
 // The form is auto-prefilled from the user's configured servers:
 // pick a server → pick one of its handler nodes → the URL field
 // is pre-populated with that listener's address + the handler's
 // mount path. Operator can still edit anything before sending.
 import type { ProxyServer } from '@/lib/types/config';
 import type { ProxyInstanceStatus, ProxyTestResponse } from '@/lib/store/proxy.svelte';
 import { proxyStore } from '@/lib/store/proxy.svelte';
 import { Send, Loader2 } from 'lucide-svelte';

 let {
  servers,
  status,
  initialServerId,
 }: {
  servers: ProxyServer[];
  status: ProxyInstanceStatus[];
  initialServerId: string | null;
 } = $props();

 // Form state.
 let selectedServerId = $state<string>('');
 let selectedHandlerId = $state<string>('');
 let method = $state('GET');
 let url = $state('');
 let headersText = $state('');
 let body = $state('');
 let timeoutMs = $state(10_000);

  let response = $state<ProxyTestResponse | null>(null);
  let sending = $state(false);

  const httpServers = $derived(servers.filter(s => (s.protocol ?? 'http') === 'http'));

 // Auto-pick the dashboard's selected server (or whichever the
 // user clicked "Test" on) when the tab mounts.
 $effect(() => {
   if (initialServerId && !selectedServerId && httpServers.some(s => s.id === initialServerId)) {
    selectedServerId = initialServerId;
   } else if (!selectedServerId && httpServers.length > 0) {
    selectedServerId = httpServers[0].id;
   }
  });

  const currentServer = $derived(httpServers.find(s => s.id === selectedServerId) ?? null);

 const handlerOptions = $derived.by(() => {
  const srv = currentServer;
  if (!srv) return [] as { id: string; label: string; path: string }[];
  return (srv.nodes ?? [])
   .filter(n => n.type === 'handler')
   .map(n => ({
    id: n.id,
    label: (n.subtype ?? 'handler'),
    path: ((n.config as any)?.path as string) || '/*',
   }));
 });

 // Build the URL whenever the server or handler selection changes.
 // Use status.addr when the listener is running (so the user can
 // copy a value that actually resolves); fall back to localhost+port
 // for the more common dev case.
 function rebuildUrl() {
  if (!currentServer) { url = ''; return; }
  const st = status.find(s => s.id === currentServer.id);
  const host = st?.addr ? st.addr.split(':')[0] || '127.0.0.1' : (currentServer.host || '127.0.0.1');
  const port = currentServer.port;
  const handler = handlerOptions.find((h: { id: string }) => h.id === selectedHandlerId);
  // The handler path may end with /* — strip it so the user gets
  // a hitable URL by default (they can append after).
  let path = handler?.path ?? '/';
  if (path.endsWith('/*')) path = path.slice(0, -1);
  if (!path.startsWith('/')) path = '/' + path;
  url = `http://${host}:${port}${path}`;
 }

 $effect(() => {
  // Reset the handler selection when the server changes and rebuild.
  const opts = handlerOptions;
  if (opts.length > 0 && !opts.some((h: { id: string }) => h.id === selectedHandlerId)) {
   selectedHandlerId = opts[0].id;
  }
  rebuildUrl();
 });

 function parseHeaders(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split(/\n+/)) {
   const colon = line.indexOf(':');
   if (colon < 0) continue;
   const k = line.slice(0, colon).trim();
   const v = line.slice(colon + 1).trim();
   if (k) out[k] = v;
  }
  return out;
 }

 async function send() {
  if (!url.trim()) return;
  sending = true;
  response = null;
  try {
   response = await proxyStore.runTest({
    url: url.trim(),
    method,
    headers: parseHeaders(headersText),
    body: body || undefined,
    timeout_ms: timeoutMs,
   });
  } catch {
   // toast in store
  } finally {
   sending = false;
  }
 }

 // Pretty-print JSON bodies if they parse; otherwise leave verbatim.
 const prettyBody = $derived.by(() => {
  if (!response) return '';
  const ct = (response.headers?.['Content-Type']?.[0] ?? '').toLowerCase();
  if (ct.includes('json')) {
   try { return JSON.stringify(JSON.parse(response.body), null, 2); } catch {}
  }
  return response.body;
 });

 // DESIGN inputs.
 const inputClass =
  'w-full px-3 py-2 text-sm rounded ' +
  'border border-slate-300 dark:border-warm-600 ' +
  'bg-white dark:bg-warm-900 ' +
  'text-slate-800 dark:text-slate-100 ' +
  'placeholder-slate-400 dark:placeholder-slate-500 ' +
  'focus:outline-none focus:ring-2 focus:ring-accent-500';
 const labelClass =
  'block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1';
</script>

<div class="flex-1 overflow-y-auto p-6">
 <div class="max-w-5xl mx-auto">
  <header class="mb-6">
   <h1 class="text-xl font-semibold text-slate-800 dark:text-slate-100 flex items-center gap-2">
    <Send size={18} class="text-accent-600 dark:text-accent-400" />
    Test request
   </h1>
   <p class="text-sm text-slate-500 dark:text-slate-400 mt-1">
    Send an HTTP request through the server-side playground. Pick one of your proxy listeners,
    edit the URL / headers / body, and inspect the response without bumping into CORS.
   </p>
  </header>

  <div class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-4 mb-4">
   <div class="grid md:grid-cols-2 gap-3 mb-3">
    <div>
     <label class={labelClass} for="srv-select">Proxy server</label>
     <select id="srv-select" class={inputClass} bind:value={selectedServerId}>
     {#each httpServers as srv}
        <option value={srv.id}>{srv.name || srv.id} :{srv.port}</option>
      {/each}
      {#if httpServers.length === 0}
       <option value="" disabled>(no HTTP proxy servers)</option>
      {/if}
     </select>
    </div>
    <div>
     <label class={labelClass} for="handler-select">Endpoint (handler)</label>
     <select id="handler-select" class={inputClass} bind:value={selectedHandlerId} onchange={rebuildUrl}>
      {#each handlerOptions as h}
       <option value={h.id}>{h.label} — {h.path}</option>
      {/each}
      {#if handlerOptions.length === 0}
       <option value="" disabled>(no handlers configured)</option>
      {/if}
     </select>
    </div>
   </div>

   <div class="grid md:grid-cols-[120px_1fr_120px] gap-3 mb-3">
    <div>
     <label class={labelClass} for="method">Method</label>
     <select id="method" class={inputClass} bind:value={method}>
      <option>GET</option>
      <option>HEAD</option>
      <option>POST</option>
      <option>PUT</option>
      <option>PATCH</option>
      <option>DELETE</option>
      <option>OPTIONS</option>
     </select>
    </div>
    <div>
     <label class={labelClass} for="url">URL</label>
     <input id="url" type="text" class={inputClass + ' font-mono'} bind:value={url} placeholder="http://127.0.0.1:9090/healthz" />
    </div>
    <div>
     <label class={labelClass} for="timeout">Timeout (ms)</label>
     <input id="timeout" type="number" class={inputClass} bind:value={timeoutMs} min="100" max="60000" />
    </div>
   </div>

   <div class="grid md:grid-cols-2 gap-3 mb-3">
    <div>
     <label class={labelClass} for="headers">Headers (one per line: "Key: value")</label>
     <textarea
      id="headers" class={inputClass + ' font-mono'}
      rows="5" spellcheck="false"
      placeholder={'Authorization: Bearer ...\nContent-Type: application/json'}
      bind:value={headersText}></textarea>
    </div>
    <div>
     <label class={labelClass} for="body">Body</label>
     <textarea
      id="body" class={inputClass + ' font-mono'}
      rows="5" spellcheck="false"
      placeholder={'{ "hello": "world" }'}
      bind:value={body}></textarea>
    </div>
   </div>

   <div class="flex justify-end">
    <button
     type="button"
     class="px-3 py-1.5 text-xs rounded
            bg-accent-600 text-white font-medium hover:bg-accent-700
            disabled:opacity-40 disabled:cursor-not-allowed
            inline-flex items-center gap-1.5 cursor-pointer"
     disabled={sending || !url.trim()}
     onclick={send}
    >
     {#if sending}
      <Loader2 size={12} class="animate-spin" />
      Sending…
     {:else}
      <Send size={12} />
      Send request
     {/if}
    </button>
   </div>
  </div>

  {#if response}
   <div class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-4">
    <div class="flex items-center justify-between mb-3">
     <h2 class="text-sm font-semibold text-slate-800 dark:text-slate-100">Response</h2>
     <div class="flex items-center gap-3 text-xs">
      <span class={'px-2 py-0.5 rounded font-mono ' + (response.status === 0
       ? 'bg-vermilion-100 text-vermilion-800 dark:bg-vermilion-950/40 dark:text-vermilion-200'
       : (response.status >= 200 && response.status < 300
        ? 'bg-emerald-100 text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-200'
        : (response.status >= 400
         ? 'bg-amber-100 text-amber-800 dark:bg-amber-950/40 dark:text-amber-200'
         : 'bg-slate-100 text-slate-700 dark:bg-warm-900 dark:text-slate-300')))}>
       {response.status === 0 ? 'ERROR' : response.status_text}
      </span>
      <span class="text-slate-500 dark:text-slate-400 font-mono">{response.duration_ms} ms</span>
     </div>
    </div>

    {#if response.truncated}
     <div class="mb-3 p-2 rounded text-xs
                 bg-amber-50 dark:bg-amber-950/40
                 border border-amber-300 dark:border-amber-700
                 text-amber-900 dark:text-amber-200">
      Response body exceeded 1&nbsp;MiB cap — only the first megabyte is shown.
     </div>
    {/if}

    <div class="grid md:grid-cols-[200px_1fr] gap-4">
     <div>
      <h3 class="text-[10px] font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1">Headers</h3>
      <div class="text-xs font-mono space-y-1 max-h-72 overflow-y-auto">
       {#each Object.entries(response.headers ?? {}) as [k, vs]}
        <div>
         <span class="text-slate-500 dark:text-slate-400">{k}:</span>
         <span class="text-slate-800 dark:text-slate-100">{vs.join(', ')}</span>
        </div>
       {/each}
      </div>
     </div>
     <div>
      <h3 class="text-[10px] font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1">Body</h3>
      <pre class="text-xs font-mono p-3 rounded
                  bg-slate-50 dark:bg-warm-900
                  border border-slate-200 dark:border-warm-700
                  text-slate-800 dark:text-slate-100
                  whitespace-pre-wrap break-words
                  max-h-96 overflow-y-auto">{prettyBody}</pre>
     </div>
    </div>
   </div>
  {/if}
 </div>
</div>
