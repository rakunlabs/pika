<script lang="ts">
 // Palette — left panel inside the editor view. Rows are grouped
 // into collapsable categories (Switches / Handlers / Middlewares)
 // so a long catalogue (15+ middlewares) does not force the
 // operator to scroll past everything to find the one they want.
 //
 // Drag-and-drop uses the standard "React Flow" pattern that also
 // shows up across every SvelteFlow / kaykay example: each row is
 // a plain <div draggable="true"> that puts a small JSON payload
 // into dataTransfer at dragstart, and the page's drop zone reads
 // it back at drop time. No module-shared state, no event-target
 // gymnastics — just the native DnD API the browser already
 // understands.
 //
 // Click-to-add stays available as a keyboard / accessibility
 // fallback (drag is not reachable by screen readers).
 import type { CatalogSpec } from '@/lib/store/proxy.svelte';
 import { Layers, Boxes, Split, ChevronRight, Cable, Route, Database } from 'lucide-svelte';
 import { stripeClassFor } from './palette-categories';

 type NodeKind = 'middleware' | 'switch' | 'handler';
 type NodeProtocol = 'http' | 'tcp';

 interface PaletteRow {
  kind: NodeKind;
  protocol: NodeProtocol;
  subtype?: string;
  label: string;
  description?: string;
 }

 interface PaletteGroup {
   key: string;            // also doubles as the localStorage key suffix
  title: string;          // section header text
  icon: typeof Layers;    // icon used in the header AND per row
  iconClass: string;      // colour token applied to every row icon
  rows: PaletteRow[];
 }

  let {
   catalog,
   protocol = 'http',
   canManage,
   onAddNode,
  }: {
   catalog: {
    middlewares: CatalogSpec[];
    handlers: CatalogSpec[];
    switches?: CatalogSpec[];
    tcp_middlewares?: CatalogSpec[];
    tcp_handlers?: CatalogSpec[];
   } | null;
   protocol?: NodeProtocol;
   canManage: boolean;
   onAddNode: (kind: NodeKind, subtype: string | undefined, protocol: NodeProtocol) => void;
  } = $props();

 // Group catalogue entries by kind. Switches first because they're
 // the routing primitive, then handlers, then middlewares. Empty
 // categories are filtered out so we never render a section header
 // with nothing under it.
  const groups = $derived.by<PaletteGroup[]>(() => {
   const out: PaletteGroup[] = [];

   const resourceSubtypes = new Set(['data', 'raw', 'registry', 'cdn']);

   if (protocol === 'tcp') {
    const tcpHandlerRows = (catalog?.tcp_handlers ?? []).map<PaletteRow>((h) => ({
     kind: 'handler', protocol: 'tcp', subtype: h.subtype, label: h.label, description: h.description,
    }));
    if (tcpHandlerRows.length) out.push({
     key: 'tcp-handler', title: 'TCP Handlers', icon: Route,
     iconClass: 'text-emerald-600 dark:text-emerald-400', rows: tcpHandlerRows,
    });

    const tcpMWRows = (catalog?.tcp_middlewares ?? []).map<PaletteRow>((m) => ({
     kind: 'middleware', protocol: 'tcp', subtype: m.subtype, label: m.label, description: m.description,
    }));
    if (tcpMWRows.length) out.push({
     key: 'tcp-middleware', title: 'TCP Middlewares', icon: Cable,
     iconClass: 'text-violet-600 dark:text-violet-400', rows: tcpMWRows,
    });

    return out;
   }

   const switchRows = (catalog?.switches ?? []).map<PaletteRow>((s) => ({
    kind: 'switch', protocol: 'http', subtype: s.subtype, label: s.label, description: s.description,
   }));
  if (switchRows.length) out.push({
   key: 'switch', title: 'Switches', icon: Split,
   iconClass: 'text-indigo-600 dark:text-indigo-400', rows: switchRows,
  });

  const resourceRows = (catalog?.handlers ?? [])
   .filter((h) => resourceSubtypes.has(h.subtype))
   .map<PaletteRow>((h) => ({
    kind: 'handler', protocol: 'http', subtype: h.subtype, label: h.label, description: h.description,
   }));
  if (resourceRows.length) out.push({
   key: 'http-resource', title: 'Resources', icon: Database,
   iconClass: 'text-amber-600 dark:text-amber-400', rows: resourceRows,
  });

  const handlerRows = (catalog?.handlers ?? [])
   .filter((h) => !resourceSubtypes.has(h.subtype))
   .map<PaletteRow>((h) => ({
   kind: 'handler', protocol: 'http', subtype: h.subtype, label: h.label, description: h.description,
  }));
  if (handlerRows.length) out.push({
   key: 'http-handler', title: 'HTTP Handlers', icon: Boxes,
   iconClass: 'text-emerald-600 dark:text-emerald-400', rows: handlerRows,
  });

  const mwRows = (catalog?.middlewares ?? []).map<PaletteRow>((m) => ({
   kind: 'middleware', protocol: 'http', subtype: m.subtype, label: m.label, description: m.description,
  }));
   if (mwRows.length) out.push({
    key: 'http-middleware', title: 'HTTP Middlewares', icon: Layers,
    iconClass: 'text-accent-600 dark:text-accent-400', rows: mwRows,
   });

   return out;
  });

 // Collapse state per category, persisted in localStorage so the
 // operator's last-opened set survives a page reload. Default is
 // "everything expanded" the first time — discoverability over
 // tidiness.
 const STORAGE_KEY = 'pika.proxy.palette.collapsed';

 function loadCollapsed(): Record<string, boolean> {
  if (typeof localStorage === 'undefined') return {};
  try {
   const raw = localStorage.getItem(STORAGE_KEY);
   if (!raw) return {};
   const parsed = JSON.parse(raw);
   return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
   return {};
  }
 }
 let collapsed = $state<Record<string, boolean>>(loadCollapsed());

 function toggle(key: string) {
  collapsed = { ...collapsed, [key]: !collapsed[key] };
  if (typeof localStorage !== 'undefined') {
   try { localStorage.setItem(STORAGE_KEY, JSON.stringify(collapsed)); } catch { /* quota */ }
  }
 }

 // Mime type the drop zone recognises. A custom type stops the
 // browser from treating accidental drops on other elements as
 // a generic text drop.
 const DRAG_MIME = 'application/x-pika-proxy-node';

 function onDragStart(e: DragEvent, row: PaletteRow) {
  if (!canManage || !e.dataTransfer) return;
   const payload = JSON.stringify({ kind: row.kind, subtype: row.subtype ?? '', protocol: row.protocol });
  e.dataTransfer.setData(DRAG_MIME, payload);
  // text/plain is a belt-and-braces fallback: Firefox refuses to
  // start the drag if dataTransfer is "empty" of well-known types,
  // and some browsers strip custom mime types in cross-origin
  // contexts (we're same-origin here but it costs nothing).
  e.dataTransfer.setData('text/plain', payload);
  e.dataTransfer.effectAllowed = 'copy';
 }

 function onClickAdd(row: PaletteRow) {
  if (!canManage) return;
   onAddNode(row.kind, row.subtype, row.protocol);
 }

 const totalRows = $derived(groups.reduce((n, g) => n + g.rows.length, 0));
</script>

<aside class="w-56 shrink-0 border-r border-slate-200 dark:border-warm-700
              bg-white dark:bg-warm-800 overflow-y-auto">
 <div class="p-3 border-b border-slate-200 dark:border-warm-700">
   <h3 class="text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">{protocol.toUpperCase()} Nodes</h3>
  {#if canManage}
   <p class="text-[11px] text-slate-400 dark:text-slate-500 mt-1">Drag onto the canvas or click to add.</p>
  {:else}
   <p class="text-[11px] text-slate-400 dark:text-slate-500 mt-1">Read-only</p>
  {/if}
 </div>

 {#if !catalog}
  <div class="px-3 py-3 text-xs text-slate-400 dark:text-slate-500">Loading…</div>
 {:else if totalRows === 0}
  <div class="px-3 py-3 text-xs text-slate-400 dark:text-slate-500">
   No node kinds registered. Check the server build.
  </div>
 {:else}
  <div class="p-2 space-y-2">
   {#each groups as group (group.key)}
    <section class="group-section">
     <button
      type="button"
      class="group-head"
      aria-expanded={!collapsed[group.key]}
      onclick={() => toggle(group.key)}
     >
      <ChevronRight
       size={11}
       class={'chev shrink-0 ' + (collapsed[group.key] ? '' : 'rot')}
      />
      <group.icon size={12} class={group.iconClass + ' shrink-0'} />
      <span class="grow text-left">{group.title}</span>
      <span class="count">{group.rows.length}</span>
     </button>

     {#if !collapsed[group.key]}
      <ul class="space-y-0.5 mt-1">
       {#each group.rows as row (row.kind + ':' + (row.subtype ?? ''))}
        <li>
         <div
           class={'row ' + stripeClassFor(row.kind, row.subtype, row.protocol)}
          class:disabled={!canManage}
          role="button"
          tabindex={canManage ? 0 : -1}
          draggable={canManage ? 'true' : 'false'}
          ondragstart={(e) => onDragStart(e, row)}
          onclick={() => onClickAdd(row)}
          onkeydown={(e) => {
           if (!canManage) return;
           if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            onClickAdd(row);
           }
          }}
          title={row.description ?? row.label}
         >
          <group.icon size={13} class={group.iconClass + ' shrink-0'} />
          <span class="grow text-left truncate">{row.label}</span>
         </div>
        </li>
       {/each}
      </ul>
     {/if}
    </section>
   {/each}
  </div>
 {/if}
</aside>

<style>
 .group-head {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 4px 6px;
  border-radius: 4px;
  background: transparent;
  color: rgb(71 85 105);
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  cursor: pointer;
  border: none;
 }
 .group-head:hover {
  background: rgba(0, 0, 0, 0.04);
  color: rgb(15 23 42);
 }
 :global(.dark) .group-head {
  color: rgb(148 163 184);
 }
 :global(.dark) .group-head:hover {
  background: rgba(255, 255, 255, 0.05);
  color: rgb(226 232 240);
 }
 .group-head .count {
  font-size: 10px;
  opacity: 0.6;
  font-family: ui-monospace, SFMono-Regular, monospace;
  text-transform: none;
  letter-spacing: 0;
 }
 /* lucide-svelte renders its own SVG so the class lands on a
    child Svelte component — we need :global to pierce scoping. */
 :global(.chev) {
  transition: transform 120ms ease;
 }
 :global(.chev.rot) {
  transform: rotate(90deg);
 }

 .row {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 5px 8px;
  /* 3px coloured stripe on the left; colour token comes from the
     dynamic stripeClassFor() class (border-{rose,sky,...}-500). */
  border-left-width: 3px;
  border-left-style: solid;
  margin-left: 6px;
  border-top-right-radius: 4px;
  border-bottom-right-radius: 4px;
  padding-left: 8px;
  background: transparent;
  color: inherit;
  cursor: grab;
  text-align: left;
  font-size: 12px;
  user-select: none;
  transition: background-color 100ms ease, border-color 100ms ease;
 }
 .row:hover:not(.disabled) { background: rgba(0,0,0,0.04); }
 :global(.dark) .row:hover:not(.disabled) { background: rgba(255,255,255,0.05); }
 .row.disabled { cursor: not-allowed; opacity: 0.5; }
 .row:active:not(.disabled) { cursor: grabbing; }
 .row:focus-visible { outline: 2px solid #0e9594; outline-offset: 1px; }
</style>

<script lang="ts" module>
 // Exported so the page-side drop zone can read the same mime type
 // string. Keeping the constant next to the producer avoids drift.
 export const PROXY_NODE_DRAG_MIME = 'application/x-pika-proxy-node';
</script>
