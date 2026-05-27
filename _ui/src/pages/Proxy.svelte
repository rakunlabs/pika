<script lang="ts">
 // /proxy — top-level page. Two main panes:
 //
 //   - Dashboard view (default): server overview cards + tabs for
 //     "Dashboard" and "Test". Lives at the top level so the
 //     operator lands on running-state, not on an empty editor.
 //   - Editor view: kaykay node graph for a single server, with
 //     the palette on the left and the config panel on the right.
 //
 // The left sidebar is shared across both views and toggles which
 // pane is in focus by setting `view`.
 //
 // Theme: every surface here uses the canonical pika tokens
 // (warm-* for dark, slate-* for light, accent-* for active
 // indicators). See DESIGN_SYSTEM.md.
 //
 // ── kaykay integration model ────────────────────────────────────
 //
 // kaykay's <Canvas> reads its `nodes`/`edges`/`config` props ONCE
 // at mount and then owns the state internally (see kaykay's
 // Canvas.svelte:46 — "we intentionally capture initial values
 // here. The flow manages its own reactive state internally").
 // Mutating local state arrays after mount has zero effect on the
 // rendered canvas. We previously did exactly that and it looked
 // like "drop is broken" / "delete doesn't work" — the operations
 // ran, the data just never reached kaykay.
 //
 // The correct pattern (the one used in at/_ui WorkflowEditor) is:
 //   1. Pass initial nodes/edges as the prop.
 //   2. Capture the canvas via bind:this and pull its FlowState
 //      with `canvasRef.getFlow()`.
 //   3. From that point on, ALL mutations go through FlowState:
 //      flow.addNode, flow.removeNode, flow.addEdge, flow.updateNodeData,
 //      flow.fromJSON, flow.toJSON.
 //   4. Read reactive state straight off flow.nodes / flow.edges
 //      inside $derived — these are $state-runed arrays so any
 //      consumer rerenders automatically.
 //
 // This page no longer maintains a parallel flowNodes/flowEdges
 // copy. Switching servers calls flow.fromJSON; saving calls
 // flow.toJSON. Position, edge, selection state lives only inside
 // kaykay.
 import { onMount, onDestroy } from 'svelte';
 import { appStore } from '@/lib/store/store.svelte';
 import { proxyStore } from '@/lib/store/proxy.svelte';
 import type { ProxyCompileError, CatalogSpec } from '@/lib/store/proxy.svelte';
 import type { ProxyServer, ProxyNode, ProxyEdge } from '@/lib/types/config';

 import { Canvas, Controls, Minimap, type FlowState } from 'kaykay';
 import type { FlowNode, FlowEdge, NodeTypes, FlowCallbacks, FlowConfig } from 'kaykay';

 import ProxySidebar from '@/lib/components/proxy/ProxySidebar.svelte';
 import ProxyPalette from '@/lib/components/proxy/ProxyPalette.svelte';
 import ProxyConfigPanel from '@/lib/components/proxy/ProxyConfigPanel.svelte';
 import ProxyDashboard from '@/lib/components/proxy/ProxyDashboard.svelte';
 import ProxyTest from '@/lib/components/proxy/ProxyTest.svelte';
 import ProxyListenersPanel from '@/lib/components/proxy/ProxyListenersPanel.svelte';
 import { PROXY_NODE_DRAG_MIME } from '@/lib/components/proxy/ProxyPalette.svelte';

 import ListenerNode from '@/lib/components/proxy/nodes/ListenerNode.svelte';
 import MiddlewareNode from '@/lib/components/proxy/nodes/MiddlewareNode.svelte';
 import SwitchNode from '@/lib/components/proxy/nodes/SwitchNode.svelte';
 import HandlerNode from '@/lib/components/proxy/nodes/HandlerNode.svelte';

 import { ShieldOff, Save, Play, LayoutDashboard, Send, Network } from 'lucide-svelte';

 // ── Capability + feature flag gates ─────────────────────────────

 // Read = can view dashboard + run test requests. Manage adds
 // mutation (CRUD on graphs). Splitting lets us hand a teammate
 // "see proxies, test them" without unlocking everything else.
 const canRead = $derived(appStore.hasPermission('proxy.read'));
 const canManage = $derived(appStore.hasPermission('proxy.manage'));
 const featureEnabled = $derived(appStore.info?.proxy_enabled ?? true);

  // ── Page-level state ─────────────────────────────────────────────
  type View = 'dashboard' | 'test' | 'editor';
  type ProxyProtocol = 'http' | 'tcp';
  type ProxyServerPreset = ProxyProtocol;
  type ProxyNodeKind = 'middleware' | 'switch' | 'handler';

 let booted = $state(false);
 let proxyLoadRequested = $state(false);
 let view = $state<View>('dashboard');
 // The tab strip on the dashboard pane. Kept separate from `view`
 // so the editor can return to whichever dashboard tab the user
 // was on before clicking into a server.
 let dashboardTab = $state<'dashboard' | 'test' | 'listeners'>('dashboard');

 let activeId = $state<string | null>(null);
 // testServerId is the server pre-selected when the user clicks
 // "Test" on a dashboard card; sticky between tab switches.
 let testServerId = $state<string | null>(null);

 let saving = $state(false);
 let lastValidation = $state<ProxyCompileError | null>(null);

 // kaykay canvas instance + the FlowState it exposes via getFlow().
 // canvasRef binds on mount; flow follows in onMount and via the
 // openEditor path so we never read getFlow() before it exists.
 //
 // Once flow is set, EVERYTHING graph-shaped (nodes, edges,
 // selection, positions, drag state) lives inside it. We hold
 // selectedNodeId locally only because the config panel sits
 // outside the canvas and needs a stable handle independent of
 // kaykay's selection set.
 let canvasRef = $state<{
  getFlow: () => FlowState;
  clientToCanvas: (x: number, y: number) => { x: number; y: number } | null;
  getContainer: () => HTMLDivElement | null;
 } | null>(null);
 let flow = $state<FlowState | null>(null);
 let selectedNodeId = $state<string | null>(null);

 // Forces the Canvas to remount with fresh initial nodes when we
 // switch servers. Kaykay's Canvas constructor reads nodes/edges
 // ONCE; re-keying the component is the cleanest way to hand it a
 // new graph without leaking the previous server's state.
 let canvasKey = $state(0);

 // Mutable copies of the active server's top-level fields. These
 // are not part of the graph so they stay as plain $state here.
 let activeName = $state('');
 let activePort = $state('');
 let activeHost = $state('');
 let activeEnabled = $state(false);
 // activeListenerID is the listener this graph is bound to. Empty
 // string means "legacy mode": the runner auto-synthesizes a
 // listener from activeHost:activePort. New graphs created from
 // the Listeners tab should set this directly.
 let activeListenerID = $state('');
 // activeHostMatch is a comma-separated editor surface for the
 // list of HTTP Host patterns this graph claims on its listener.
 // Empty = catch-all.
 let activeHostMatch = $state('');

 // Initial nodes/edges handed to <Canvas> at mount. Refreshed
 // alongside canvasKey when the active server changes.
 let initialNodes = $state<FlowNode[]>([]);
 let initialEdges = $state<FlowEdge[]>([]);

  const activeServer = $derived<ProxyServer | null>(
   activeId ? (proxyStore.servers.find(s => s.id === activeId) ?? null) : null,
  );
  const activeProtocol = $derived<ProxyProtocol>((activeServer?.protocol ?? 'http') as ProxyProtocol);

 // Re-bind every reactive store read through a $derived so the
 // dependency graph stays explicit. Without the wrapper, passing
 // `proxyStore.catalog` straight to a child prop in some Svelte 5
 // setups loses the cross-module subscription on the first render
 // (the palette would then stay on "Loading…" until something
 // else triggered a parent re-render — exactly the bug the
 // operator reported: navigate away, come back, palette works).
 const proxyCatalog = $derived(proxyStore.catalog);

 // Belt-and-braces: when the editor opens and the catalog is
 // missing (cold load lost the race, the network blip ate the
 // first request, or proxy.read was granted after boot), retry
 // the store load so the palette eventually populates instead of
 // sitting on a forever spinner. The store's load() is a single
 // allSettled batch — calling it twice is cheap.
 $effect(() => {
  if (view === 'editor' && proxyCatalog === null && booted) {
   void proxyStore.load();
  }
 });

 async function loadProxyPage() {
  try {
   await proxyStore.load();
   const current = activeId ? proxyStore.servers.find(s => s.id === activeId) : null;
   const next = current ?? proxyStore.servers[0] ?? null;
   activeId = next?.id ?? null;
   syncFromServer(next);
  } catch {/* toast in store */}
  finally { booted = true; }
 }

 // Permission data comes from /api/v1/info, which can arrive after
 // /login/me on a hard refresh. Do not mark the page as permanently
 // loaded just because canRead was false on the first effect run;
 // when the permission flips true, fire the initial load exactly once.
 $effect(() => {
  if (!canRead || !featureEnabled) {
   booted = true;
   return;
  }
  if (proxyLoadRequested) return;
  proxyLoadRequested = true;
  booted = false;
  void loadProxyPage();
 });

 // Drag-over visual feedback. Toggled in the wrapper's ondragover /
 // ondragleave / ondrop handlers so the canvas shows an accent
 // ring while the user is hovering a valid palette item over it.
 let draggingOver = $state(false);

 // ── Conversions ──────────────────────────────────────────────────

 function toFlowNode(n: ProxyNode, serverProtocol: ProxyProtocol = 'http'): FlowNode {
  const protocol = (n.protocol ?? serverProtocol) as ProxyProtocol;
  return {
   id: n.id,
   type: n.type,
   position: { x: n.position.x, y: n.position.y },
   data: {
    type: n.type,
    protocol,
    subtype: n.subtype ?? '',
    config: n.config ?? {},
    // Surfaced at the top of data so SwitchNode.svelte can read
    // the current rule list directly via NodeProps<>. Mirrors
    // what updateSelectedNode does on every config change.
    rules: (n.config as any)?.rules,
   } as Record<string, unknown>,
  };
 }
 function toProxyNode(fn: { id: string; type: string; position: { x: number; y: number }; data: Record<string, unknown> }): ProxyNode {
  const d = fn.data as Record<string, unknown>;
  return {
   id: fn.id,
   type: d.type as ProxyNode['type'],
   protocol: (d.protocol as ProxyProtocol | undefined),
   subtype: (d.subtype as string) || undefined,
   position: { x: fn.position.x, y: fn.position.y },
   config: (d.config as Record<string, unknown>) ?? {},
  };
 }
 function toFlowEdge(e: ProxyEdge): FlowEdge {
  return {
   id: e.id,
   source: e.source,
   source_handle: e.source_handle || 'out',
   target: e.target,
   target_handle: e.target_handle || 'in',
  };
 }
 function toProxyEdge(e: FlowEdge): ProxyEdge {
  return {
   id: e.id,
   source: e.source,
   source_handle: e.source_handle,
   target: e.target,
   target_handle: e.target_handle,
  };
 }

 // Load a server into the editor. If the canvas is already mounted
 // we use flow.fromJSON for an in-place swap (cheaper, preserves
 // viewport pan/zoom would be nice but kaykay resets them anyway).
 // If not, we stash into initialNodes/initialEdges and bump the
 // remount key so the next render gives kaykay the right seed.
 function syncFromServer(srv: ProxyServer | null) {
  if (!srv) {
   initialNodes = [];
   initialEdges = [];
   selectedNodeId = null;
   activeName = '';
   activePort = '';
   activeHost = '';
   activeEnabled = false;
   activeListenerID = '';
   activeHostMatch = '';
   if (flow) flow.fromJSON({ nodes: [], edges: [] });
   return;
  }
  const serverProtocol = (srv.protocol ?? 'http') as ProxyProtocol;
  const nodes = (srv.nodes ?? []).map(n => toFlowNode(n, serverProtocol));
  const edges = (srv.edges ?? []).map(toFlowEdge);
  initialNodes = nodes;
  initialEdges = edges;
  selectedNodeId = null;
  lastValidation = null;
  activeName = srv.name;
  activePort = srv.port ?? '';
  activeHost = srv.host ?? '';
  activeEnabled = srv.enabled;
  activeListenerID = srv.listener_id ?? '';
  activeHostMatch = (srv.host_match ?? []).join(', ');
  if (flow) {
   flow.fromJSON({ nodes, edges });
  } else {
   // No canvas yet — bumping the key forces a remount once the
   // editor view shows, and the constructor will pick up the
   // initialNodes/initialEdges we just assigned.
   canvasKey++;
  }
 }

 // Snapshot the current graph back into ProxyServer shape. Pulls
 // straight from kaykay so positions reflect any drags the user
 // did, edges include connections kaykay added through its draft
 // connection UI, etc.
 function snapshot(): ProxyServer | null {
  if (!activeServer) return null;
  const json = flow ? flow.toJSON() : { nodes: [], edges: [] };
  const hostMatch = activeHostMatch
   .split(',')
   .map(s => s.trim())
   .filter(Boolean);
  return {
   ...activeServer,
   protocol: activeProtocol,
   name: activeName,
   port: activePort,
   host: activeHost || undefined,
   enabled: activeEnabled,
   listener_id: activeListenerID || undefined,
   host_match: hostMatch.length > 0 ? hostMatch : undefined,
   nodes: json.nodes.map((n) => toProxyNode(n as any)),
   edges: json.edges.map((e) => toProxyEdge(e as FlowEdge)),
  };
 }

 // ── Derived state for the config panel ───────────────────────────

 // Pulled live from kaykay's reactive nodes array. When the user
 // edits a field in the config panel we round-trip through
 // flow.updateNodeData below; the result flows back into this
 // derived value automatically.
 const selectedNode = $derived.by<ProxyNode | null>(() => {
  if (!flow || !selectedNodeId) return null;
  const n = flow.nodes.find((x) => x.id === selectedNodeId);
  return n ? toProxyNode(n as any) : null;
 });

 const selectedSpec = $derived.by<CatalogSpec | null>(() => {
  const node = selectedNode;
  const cat = proxyStore.catalog;
  if (!node || !cat) return null;
  const protocol = node.protocol ?? activeProtocol;
  if (node.type === 'middleware') {
   const bucket = protocol === 'tcp' ? (cat.tcp_middlewares ?? []) : cat.middlewares;
   return bucket.find(m => m.subtype === node.subtype) ?? null;
  }
  if (node.type === 'handler') {
   const bucket = protocol === 'tcp' ? (cat.tcp_handlers ?? []) : cat.handlers;
   return bucket.find(h => h.subtype === node.subtype) ?? null;
  }
  if (node.type === 'switch') {
   return (cat.switches ?? []).find(s => s.subtype === (node.subtype || 'switch')) ?? null;
  }
  return null;
 });

 // ── Actions ───────────────────────────────────────────────────────

 async function addServer(preset: ProxyServerPreset = 'http') {
  const id = (crypto as any).randomUUID?.() ?? Math.random().toString(36).slice(2);
  // Seed every new server with the smallest graph that compiles:
  // HTTP gets listener → healthz; TCP gets listener → tcp-forward.
  // CDN is an HTTP handler in the editor palette, not its own server type.
  const isTCP = preset === 'tcp';
  const protocol: ProxyProtocol = isTCP ? 'tcp' : 'http';
  const srv: ProxyServer = {
   id,
   name: isTCP ? 'New TCP proxy' : 'New proxy',
   enabled: false,
   protocol,
   port: isTCP ? '9091' : '9090',
   nodes: isTCP
    ? [
     { id: 'listener', type: 'listener', protocol: 'tcp', position: { x: 80, y: 120 }, config: {} },
     { id: 'tcp-forward', type: 'handler', protocol: 'tcp', subtype: 'tcp-forward', position: { x: 380, y: 120 }, config: { network: 'tcp', address: '127.0.0.1:80' } },
    ]
    : [
     { id: 'listener', type: 'listener', protocol: 'http', position: { x: 80, y: 120 }, config: {} },
     { id: 'healthz', type: 'handler', protocol: 'http', subtype: 'healthz', position: { x: 380, y: 120 }, config: {} },
    ],
   edges: [isTCP
    ? { id: 'e-listener-tcp-forward', source: 'listener', target: 'tcp-forward' }
    : { id: 'e-listener-healthz', source: 'listener', target: 'healthz' }],
  };
  try {
   const created = await proxyStore.create(srv);
   activeId = created.id;
   syncFromServer(created);
   view = 'editor';
  } catch {/* toast in store */}
 }

 async function deleteServer(id: string) {
  if (!confirm('Delete this proxy server?')) return;
  await proxyStore.remove(id);
  if (activeId === id) {
   const next = proxyStore.servers[0];
   activeId = next?.id ?? null;
   syncFromServer(next ?? null);
   if (!next) view = 'dashboard';
  }
 }

 async function toggleEnabled(id: string, enabled: boolean) {
  const srv = proxyStore.servers.find(s => s.id === id);
  if (!srv) return;
  try {
   await proxyStore.update({ ...srv, enabled });
   if (id === activeId) activeEnabled = enabled;
  } catch {/* toast in store */}
 }

 function openEditor(id: string) {
  activeId = id;
  syncFromServer(proxyStore.servers.find(s => s.id === id) ?? null);
  view = 'editor';
 }

 function openDashboard() {
  view = 'dashboard';
  dashboardTab = 'dashboard';
 }

 function openTest(id?: string) {
  if (id) testServerId = id;
  view = 'dashboard';
  dashboardTab = 'test';
 }

 function nextNodePosition(): { x: number; y: number } {
  // Drop spawn position somewhere sensible inside the visible
  // canvas. We pick the average of existing node positions plus a
  // small jitter so consecutive adds don't perfectly overlap.
  const nodes = flow?.nodes ?? [];
  if (nodes.length === 0) return { x: 240, y: 200 };
  let ax = 0, ay = 0;
  for (const n of nodes) { ax += n.position.x; ay += n.position.y; }
  ax /= nodes.length; ay /= nodes.length;
  return { x: ax + 60 + Math.random() * 40, y: ay + 40 + Math.random() * 40 };
 }

 // defaultConfigFor seeds a freshly-dropped node with the minimal
 // config its kind needs. Switch starts with an empty rules list
 // (operator adds the first rule from the config panel); other
 // kinds inherit the empty object — their forms fill in defaults.
 function defaultConfigFor(kind: ProxyNodeKind, subtype?: string): Record<string, unknown> {
  if (kind === 'switch') return { rules: [] };
  if (subtype === 'tcp-forward') return { network: 'tcp', address: '127.0.0.1:80' };
  if (subtype === 'cdn') return { namespace: 'default', repository: 'npm', strip_prefix: '/npm' };
  return {};
 }

  function spawnNode(kind: ProxyNodeKind, subtype: string | undefined, protocol: ProxyProtocol, position: { x: number; y: number }) {
   if (!activeServer || !canManage || !flow) return;
   if (protocol !== activeProtocol) return;
   const id = `${kind}-${Date.now().toString(36)}`;
   const node: ProxyNode = {
    id,
    type: kind,
    protocol,
    subtype,
    position,
   config: defaultConfigFor(kind, subtype),
  };
  flow.addNode(toFlowNode(node) as FlowNode);
  selectedNodeId = id;
 }

  function addNodeOnCanvas(kind: ProxyNodeKind, subtype: string | undefined, protocol: ProxyProtocol) {
   spawnNode(kind, subtype, protocol, nextNodePosition());
  }

 // Canvas drop handling — standard React Flow / SvelteFlow pattern.
 //
 // The palette sets a small JSON payload on dataTransfer at
 // dragstart; the wrapper here calls preventDefault() in dragover
 // to mark itself a valid drop target, then reads the payload in
 // drop and converts the pointer's client coordinates into canvas
 // coordinates using the Canvas's clientToCanvas helper.
 //
 // The wrapper sits OUTSIDE the kaykay Canvas root element so the
 // canvas's own pointer/mouse handlers don't interact with the
 // HTML5 drag/drop layer (the two event families are independent
 // anyway — kaykay uses pointer*, drag uses drag*).
 let dropZone = $state<HTMLDivElement | null>(null);

 function onCanvasDragOver(e: DragEvent) {
  // preventDefault MUST be called unconditionally in dragover for
  // the drop event to fire at all. Gating on mime types here
  // fights some browsers' permission model (the mime check
  // happens in drop instead).
  e.preventDefault();
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
  if (canManage) draggingOver = true;
 }

 function onCanvasDragLeave(e: DragEvent) {
  // A leave fires when the pointer crosses into a child element
  // too, so we filter on relatedTarget — if the new target is
  // still inside the dropzone we ignore the event.
  const next = e.relatedTarget as Node | null;
  if (next && dropZone && dropZone.contains(next)) return;
  draggingOver = false;
 }

 function onCanvasDrop(e: DragEvent) {
  e.preventDefault();
  draggingOver = false;
  // Pull the JSON payload the palette put into dataTransfer. We
  // try the custom mime first, then text/plain fallback — same
  // order the palette writes them in. Drops from outside the
  // palette (browser tabs, files, etc.) silently no-op because
  // they carry neither mime type.
  const raw = e.dataTransfer?.getData(PROXY_NODE_DRAG_MIME)
           || e.dataTransfer?.getData('text/plain')
           || '';
  if (!raw) return;
  let payload: { kind?: string; subtype?: string; protocol?: ProxyProtocol };
  try { payload = JSON.parse(raw); } catch { return; }
  if (payload.kind !== 'middleware' && payload.kind !== 'switch' && payload.kind !== 'handler') return;

  // Canvas-local coordinates account for pan + zoom (the canvas
  // applies its own transform on the nodes layer). Fall back to
  // wrapper-relative coords if the canvas isn't mounted yet —
  // shouldn't happen in practice but defensive nonetheless.
  let pos = canvasRef?.clientToCanvas(e.clientX, e.clientY) ?? null;
  if (!pos && dropZone) {
   const r = dropZone.getBoundingClientRect();
   pos = { x: e.clientX - r.left, y: e.clientY - r.top };
  }
  spawnNode(payload.kind, payload.subtype || undefined, payload.protocol ?? 'http', pos ?? { x: 0, y: 0 });
 }

 function updateSelectedNode(next: ProxyNode) {
  if (!flow) return;
  // Push the entire data envelope so subtype changes (rare but
  // possible via raw JSON editor) take effect alongside config.
  // We also surface rules onto data so SwitchNode's body can
  // re-render the per-rule handles reactively; without this the
  // node card only sees the initial rule list on mount.
  flow.updateNodeData(next.id, {
   type: next.type,
   protocol: next.protocol ?? activeProtocol,
   subtype: next.subtype ?? '',
   config: next.config ?? {},
   rules: (next.config as any)?.rules,
  });
 }
 function deleteSelectedNode() {
  if (!flow || !selectedNodeId) return;
  // kaykay's removeNode also clears any connected edges and
  // updates its own selection set — we just have to drop our
  // local pointer.
  flow.removeNode(selectedNodeId);
  selectedNodeId = null;
 }

 // Called by ProxyConfigPanel (forwarded from SwitchForm) when a
 // switch rule is removed. The rule's source_handle is the rule's
 // ID; any edge originating from the active switch on that handle
 // becomes a dangling reference, so we delete the matching edges
 // up front. Without this the backend would reject the saved
 // graph with "node unreachable" / "branch missing" on the next
 // Save.
 function onSwitchRuleRemoved(ruleId: string) {
  if (!flow || !selectedNodeId) return;
  const switchId = selectedNodeId;
  for (const edge of flow.edges.slice()) {
   if (edge.source === switchId && edge.source_handle === ruleId) {
    flow.removeEdge(edge.id);
   }
  }
 }

 async function saveActive() {
  const snap = snapshot();
  if (!snap) return;
  saving = true;
  try {
   await proxyStore.update(snap);
   lastValidation = null;
  } catch {/* toast */}
  finally { saving = false; }
 }

 async function validateActive() {
  const snap = snapshot();
  if (!snap || !flow) return;
  const res = await proxyStore.validate(snap);
  if (res && (res as any).ok) {
   lastValidation = null;
   // Clear hasError flag from every node.
   for (const n of flow.nodes) {
    if ((n.data as any)?.hasError) {
     flow.updateNodeData(n.id, { hasError: false });
    }
   }
  } else {
   lastValidation = res as ProxyCompileError;
   const errId = lastValidation.node_id;
   for (const n of flow.nodes) {
    const shouldFlag = errId && n.id === errId;
    const hasFlag = !!(n.data as any)?.hasError;
    if (shouldFlag !== hasFlag) {
     flow.updateNodeData(n.id, { hasError: !!shouldFlag });
    }
   }
  }
 }

 // ── kaykay wiring ────────────────────────────────────────────────

 const nodeTypes: NodeTypes = {
  listener: ListenerNode as any,
  middleware: MiddlewareNode as any,
  switch: SwitchNode as any,
  handler: HandlerNode as any,
 };

 // Initial config — captured at Canvas mount. Runtime changes
 // (e.g. canManage flipping) are propagated by writing into the
 // live flow.config below via an $effect, so it's correct that
 // this only reads canManage's initial value.
 // svelte-ignore state_referenced_locally
 const initialConfig: FlowConfig = {
  min_zoom: 0.5,
  max_zoom: 2,
  snap_to_grid: false,
  allow_delete: canManage,
  default_edge_type: 'bezier',
  locked: !canManage,
 };

 // Keep lock state in sync with the live permission. We can't
 // re-pass `config` because kaykay only reads props once at mount,
 // but flow.config is reactive $state so writing into it works.
 $effect(() => {
  if (!flow) return;
  flow.config.locked = !canManage;
  flow.config.allow_delete = canManage;
 });

 // Callbacks fire AFTER kaykay has already mutated its own state
 // (e.g. addEdge pushes onto flow.edges then calls on_connect).
 // We only use them for cross-cutting concerns — selection
 // mirroring into selectedNodeId, and clearing the config panel
 // when the user deletes the active node.
 const callbacks: FlowCallbacks = {
  on_node_click: (id) => { selectedNodeId = id; },
  on_selection_change: (nodeIds) => {
   if (nodeIds.length === 1) {
    selectedNodeId = nodeIds[0];
   } else if (nodeIds.length === 0 && selectedNodeId !== null) {
    // Clicking empty canvas — close the panel too.
    selectedNodeId = null;
   }
  },
  on_delete: (nodeIds) => {
   if (selectedNodeId && nodeIds.includes(selectedNodeId)) selectedNodeId = null;
  },
 };

 // Watch the document's dark-mode class so we can hand kaykay the
 // matching theme class string. Without this kaykay defaults to
 // light mode no matter what pika's shell shows.
 let canvasThemeClass = $state('kaykay-light');
 function updateCanvasTheme() {
  if (typeof document === 'undefined') return;
  canvasThemeClass = document.documentElement.classList.contains('dark')
   ? 'kaykay-dark'
   : 'kaykay-light';
 }

 // Pull the FlowState out of the canvas as soon as it mounts.
 // Triggered whenever canvasRef flips from null to non-null
 // (initial mount, or after a canvasKey bump for server switch).
 $effect(() => {
  if (canvasRef && !flow) {
   flow = canvasRef.getFlow();
  }
 });

 // When the canvas is remounted (server switch), canvasRef goes
 // through null briefly; clearing flow alongside it keeps the two
 // in sync so we never call methods on a stale FlowState.
 $effect(() => {
  if (!canvasRef) flow = null;
 });

 // ── Lifecycle ────────────────────────────────────────────────────

 let pollHandle: ReturnType<typeof setInterval> | null = null;
 let themeObserver: MutationObserver | null = null;

 onMount(() => {
  pollHandle = setInterval(() => {
   if (canRead && featureEnabled) void proxyStore.refreshStatus();
  }, 5000);
  updateCanvasTheme();
  if (typeof document !== 'undefined') {
   themeObserver = new MutationObserver(updateCanvasTheme);
   themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
  }
 });

 onDestroy(() => {
  if (pollHandle) clearInterval(pollHandle);
  themeObserver?.disconnect();
 });
</script>

<svelte:head><title>Proxy · pika</title></svelte:head>

<div class="flex flex-col h-full overflow-hidden bg-slate-100 dark:bg-warm-900">
 {#if !booted}
  <div class="flex-1 grid place-content-center text-slate-500 dark:text-slate-400 text-sm">Loading…</div>
 {:else if !featureEnabled}
  <div class="max-w-md mx-auto py-12 px-4 text-center">
   <ShieldOff size={32} class="mx-auto text-slate-400 dark:text-slate-500 mb-3" />
   <h2 class="text-lg font-semibold mb-2 text-slate-800 dark:text-slate-100">Proxy disabled</h2>
   <p class="text-sm text-slate-600 dark:text-slate-300">
    The proxy feature is disabled for this deployment. An administrator can enable it under
    <code class="font-mono">Settings → Features</code>.
   </p>
  </div>
 {:else if !canRead}
  <div class="max-w-md mx-auto py-12 px-4 text-center">
   <ShieldOff size={32} class="mx-auto text-slate-400 dark:text-slate-500 mb-3" />
   <h2 class="text-lg font-semibold mb-2 text-slate-800 dark:text-slate-100">Permission required</h2>
   <p class="text-sm text-slate-600 dark:text-slate-300">
    You need the <code class="font-mono">proxy.read</code> capability to view proxy servers.
   </p>
  </div>
 {:else}
  <div class="flex flex-1 min-h-0">
   <ProxySidebar
    servers={proxyStore.servers}
    status={proxyStore.status}
    {activeId}
    view={view === 'editor' ? 'editor' : 'dashboard'}
    {canManage}
    onSelectDashboard={openDashboard}
    onSelect={openEditor}
    onDelete={deleteServer}
   />

    {#if view === 'editor' && activeServer}
     <!-- Editor view: palette + canvas + config panel -->
      <ProxyPalette
       catalog={proxyCatalog}
       protocol={activeProtocol}
       {canManage}
       onAddNode={addNodeOnCanvas}
      />

     <div class="flex-1 flex flex-col min-w-0 bg-slate-50 dark:bg-warm-900">
      <!-- Toolbar. Navigation back to the dashboard happens through
           the sidebar's "Dashboard" entry, so the toolbar stays
           focused on actions for the current server. -->
      <div class="flex items-center gap-2 px-3 py-2 border-b border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-800">
       <input
       type="text"
       class="grow min-w-0 px-3 py-1.5 text-sm rounded
              border border-slate-300 dark:border-warm-600
              bg-white dark:bg-warm-900
              text-slate-800 dark:text-slate-100
              placeholder-slate-400 dark:placeholder-slate-500
              focus:outline-none focus:ring-2 focus:ring-accent-500"
       bind:value={activeName}
       disabled={!canManage}
       placeholder="Server name"
       />
       <span class={'shrink-0 text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded font-mono ' + (activeProtocol === 'tcp'
        ? 'bg-violet-100 text-violet-700 dark:bg-violet-950/40 dark:text-violet-300'
        : 'bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300')}>
        {activeProtocol}
       </span>
       <label class="flex items-center gap-1.5 text-xs text-slate-500 dark:text-slate-400">
        Port
       <input
        type="text"
        class="w-20 px-2 py-1.5 text-sm rounded
               border border-slate-300 dark:border-warm-600
               bg-white dark:bg-warm-900
               text-slate-800 dark:text-slate-100
               focus:outline-none focus:ring-2 focus:ring-accent-500 font-mono"
        bind:value={activePort}
        disabled={!canManage}
        placeholder="9090"
       />
      </label>
      <label class="flex items-center gap-1.5 text-xs text-slate-500 dark:text-slate-400">
       Host
       <input
        type="text"
        class="w-28 px-2 py-1.5 text-sm rounded
               border border-slate-300 dark:border-warm-600
               bg-white dark:bg-warm-900
               text-slate-800 dark:text-slate-100
               focus:outline-none focus:ring-2 focus:ring-accent-500 font-mono"
        bind:value={activeHost}
        disabled={!canManage}
        placeholder="(default)"
       />
      </label>
      <label class="flex items-center gap-1.5 text-xs text-slate-500 dark:text-slate-400">
       Listener
       <select
        class="px-2 py-1.5 text-sm rounded
               border border-slate-300 dark:border-warm-600
               bg-white dark:bg-warm-900
               text-slate-800 dark:text-slate-100
               focus:outline-none focus:ring-2 focus:ring-accent-500"
        bind:value={activeListenerID}
        disabled={!canManage}
       >
        <option value="">(legacy host:port)</option>
        {#each proxyStore.listeners.filter(l => (l.protocol ?? 'http') === activeProtocol) as ln (ln.id)}
         <option value={ln.id}>{ln.name || ln.id} · {ln.host || '0.0.0.0'}:{ln.port}</option>
        {/each}
       </select>
      </label>
      {#if activeListenerID && activeProtocol === 'http'}
       <label class="flex items-center gap-1.5 text-xs text-slate-500 dark:text-slate-400">
        Host match
        <input
         type="text"
         class="w-44 px-2 py-1.5 text-sm rounded
                border border-slate-300 dark:border-warm-600
                bg-white dark:bg-warm-900
                text-slate-800 dark:text-slate-100
                focus:outline-none focus:ring-2 focus:ring-accent-500 font-mono"
         bind:value={activeHostMatch}
         disabled={!canManage}
         placeholder="example.com, *.api.io (empty = *)"
        />
       </label>
      {/if}
      <label class="flex items-center gap-1.5 text-xs text-slate-700 dark:text-slate-200 cursor-pointer">
       <input
        type="checkbox"
        class="h-3.5 w-3.5 rounded border-slate-300 dark:border-warm-600 text-accent-600 focus:ring-accent-500 cursor-pointer"
        bind:checked={activeEnabled}
        disabled={!canManage}
       />
       enabled
      </label>
      <button
       type="button"
       class="px-3 py-1.5 text-xs rounded
              bg-slate-100 dark:bg-warm-700
              hover:bg-slate-200 dark:hover:bg-warm-600
              text-slate-700 dark:text-slate-200
              inline-flex items-center gap-1.5 cursor-pointer"
       onclick={validateActive}
      >
       <Play size={12} /> Validate
      </button>
      {#if canManage}
       <button
        type="button"
        class="px-3 py-1.5 text-xs rounded
               bg-accent-600 text-white font-medium hover:bg-accent-700
               disabled:opacity-40 disabled:cursor-not-allowed
               inline-flex items-center gap-1.5 cursor-pointer"
        onclick={saveActive}
        disabled={saving}
       >
        <Save size={12} /> {saving ? 'Saving…' : 'Save'}
       </button>
      {/if}
     </div>

     {#if lastValidation}
      <div class="px-4 py-2 text-xs font-mono
                  bg-vermilion-50 dark:bg-vermilion-950/40
                  border-b border-vermilion-300 dark:border-vermilion-800
                  text-vermilion-900 dark:text-vermilion-200">
       <strong>{lastValidation.code}:</strong> {lastValidation.message}
       {#if lastValidation.node_id}<span class="opacity-70"> · node {lastValidation.node_id}</span>{/if}
      </div>
     {/if}

      <!-- Drag listeners live on the wrapper around the canvas.
           HTML5 dragover/drop bubble up from any descendant
           (including kaykay's nodes), so one pair of handlers
           catches every drop position. The kaykay Canvas itself
           handles only pointer* events for panning / node drag,
           which are independent from drag/drop and don't fight.

           role + tabindex are required to keep svelte-check's
           a11y pass happy on a div that owns interaction events. -->
      <div
       class="flex-1 min-h-0 relative {draggingOver ? 'ring-2 ring-inset ring-accent-500 dark:ring-accent-400' : ''}"
       bind:this={dropZone}
       ondragover={onCanvasDragOver}
       ondragleave={onCanvasDragLeave}
       ondrop={onCanvasDrop}
       role="region"
       aria-label="Proxy graph canvas"
      >
       {#key canvasKey}
        <Canvas
         bind:this={canvasRef}
         nodes={initialNodes}
         edges={initialEdges}
         {nodeTypes}
         config={initialConfig}
         {callbacks}
         class={canvasThemeClass}
        >
         {#snippet controls()}
          <Controls position="bottom-left" />
          <Minimap width={160} height={100} />
         {/snippet}
        </Canvas>
       {/key}
      </div>
    </div>

    <ProxyConfigPanel
     node={selectedNode}
     spec={selectedSpec}
     {canManage}
     onChange={updateSelectedNode}
     onDelete={deleteSelectedNode}
     {onSwitchRuleRemoved}
    />
   {:else}
    <!-- Dashboard view: tabs + content -->
    <div class="flex-1 flex flex-col min-w-0">
     <div class="px-6 pt-4 border-b border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-800">
      <nav class="flex gap-1" aria-label="Proxy tabs">
       <button
        type="button"
        class={'inline-flex items-center gap-1.5 px-3 py-2 text-sm rounded-t border-b-2 cursor-pointer ' + (dashboardTab === 'dashboard'
         ? 'border-accent-500 text-accent-700 dark:text-accent-300'
         : 'border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200')}
        onclick={() => dashboardTab = 'dashboard'}
       >
        <LayoutDashboard size={14} /> Dashboard
       </button>
       <button
        type="button"
        class={'inline-flex items-center gap-1.5 px-3 py-2 text-sm rounded-t border-b-2 cursor-pointer ' + (dashboardTab === 'listeners'
         ? 'border-accent-500 text-accent-700 dark:text-accent-300'
         : 'border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200')}
        onclick={() => dashboardTab = 'listeners'}
       >
        <Network size={14} /> Listeners
       </button>
       <button
        type="button"
        class={'inline-flex items-center gap-1.5 px-3 py-2 text-sm rounded-t border-b-2 cursor-pointer ' + (dashboardTab === 'test'
         ? 'border-accent-500 text-accent-700 dark:text-accent-300'
         : 'border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200')}
        onclick={() => dashboardTab = 'test'}
       >
        <Send size={14} /> Test
       </button>
      </nav>
     </div>

     {#if dashboardTab === 'dashboard'}
      <ProxyDashboard
       servers={proxyStore.servers}
       status={proxyStore.status}
        {canManage}
        onOpenEditor={openEditor}
        onAdd={addServer}
        onToggleEnabled={toggleEnabled}
        onTestServer={openTest}
       />
     {:else if dashboardTab === 'listeners'}
      <ProxyListenersPanel
       listeners={proxyStore.listeners}
       status={proxyStore.listenersStatus}
       servers={proxyStore.servers}
       {canManage}
      />
     {:else}
      <ProxyTest
       servers={proxyStore.servers}
       status={proxyStore.status}
       initialServerId={testServerId}
      />
     {/if}
    </div>
   {/if}
  </div>
 {/if}
</div>
