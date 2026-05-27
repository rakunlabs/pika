// Proxy store — manages the list of ProxyServer graphs, the static
// catalog (middleware + handler kinds the backend ships), and the
// live runner status. All persistence goes through /api/v1/proxy/*.
//
// State is held as $state runes so any consumer component re-renders
// when the list changes. The store exposes pure-async functions for
// mutations and lets callers await them — toasts and error display
// stay at the call site so each page chooses its own UX.

import axios from 'axios';
import { addToast } from './toast.svelte';
import type { ProxyServer, ProxyListener } from '@/lib/types/config';

// CatalogSpec mirrors proxy.NodeSpec on the backend. Loose typing
// because the schema field is opaque JSON the UI feeds into a
// generic JSON-Schema form renderer.
export interface CatalogSpec {
  // kind groups the entry in the palette: 'middleware' | 'handler'
  // | 'switch'. Older deployments may omit it (the backend only
  // started emitting kind alongside the switch refactor); we
  // tolerate the absence by treating it as 'middleware' or
  // 'handler' based on which array the entry lives in.
  kind?: 'middleware' | 'handler' | 'switch';
  protocol?: 'http' | 'tcp';
  subtype: string;
  label: string;
  description?: string;
  config_schema?: unknown;
}

export interface ProxyCatalog {
  middlewares: CatalogSpec[];
  handlers: CatalogSpec[];
  // switches is new — backend started shipping it with the switch
  // node. Always defined for current builds; treat undefined as
  // [] for forwards compat with older API shapes.
  switches?: CatalogSpec[];
  tcp_middlewares?: CatalogSpec[];
  tcp_handlers?: CatalogSpec[];
}

export interface ProxyInstanceStatus {
  id: string;
  listener_id?: string;
  running: boolean;
  addr?: string;
  hash?: string;
  started?: string;
  last_err?: string;
}

export interface ProxyListenerStatus {
  id: string;
  running: boolean;
  addr?: string;
  protocol?: 'http' | 'tcp';
  started?: string;
  last_err?: string;
  graph_ids?: string[];
}

export interface ProxyCompileError {
  node_id?: string;
  edge_id?: string;
  code: string;
  message: string;
}

// Mirrors api.proxyTestRequest / proxyTestResponse server-side.
export interface ProxyTestRequest {
  url: string;
  method: string;
  headers?: Record<string, string>;
  body?: string;
  timeout_ms?: number;
}

export interface ProxyTestResponse {
  status: number;
  status_text: string;
  headers: Record<string, string[]>;
  body: string;
  duration_ms: number;
  truncated?: boolean;
}

let servers = $state<ProxyServer[]>([]);
let listeners = $state<ProxyListener[]>([]);
let catalog = $state<ProxyCatalog | null>(null);
let status = $state<ProxyInstanceStatus[]>([]);
let listenersStatus = $state<ProxyListenerStatus[]>([]);
let loaded = $state(false);

async function load(): Promise<void> {
  // Use allSettled so a single failing endpoint doesn't leave the
  // store with everything else null — that pattern showed up as
  // a permanently-spinning "Loading…" palette when one of the
  // three calls 404'd or 403'd. Each slot falls back to a sane
  // default and the next interaction can retry.
  const [listRes, catRes, statusRes, lnRes, lnStatusRes] = await Promise.allSettled([
    axios.get<ProxyServer[]>('/api/v1/proxy'),
    axios.get<ProxyCatalog>('/api/v1/proxy/catalog'),
    axios.get<ProxyInstanceStatus[]>('/api/v1/proxy/status'),
    axios.get<ProxyListener[]>('/api/v1/proxy/listeners'),
    axios.get<ProxyListenerStatus[]>('/api/v1/proxy/listeners/status'),
  ]);
  if (listRes.status === 'fulfilled') {
    servers = listRes.value.data ?? [];
  }
  if (lnRes.status === 'fulfilled') {
    listeners = lnRes.value.data ?? [];
  }
  if (lnStatusRes.status === 'fulfilled') {
    listenersStatus = lnStatusRes.value.data ?? [];
  }
  if (catRes.status === 'fulfilled') {
    // Defensive normalize: a JSON body of `null` would otherwise
    // leave catalog == null and the palette would spin "Loading…"
    // forever. Same goes for a response missing one of the array
    // fields (e.g. an older server build).
    const raw = catRes.value.data as Partial<ProxyCatalog> | null | undefined;
    catalog = {
      middlewares: raw?.middlewares ?? [],
      handlers: raw?.handlers ?? [],
      // switches landed with the switch-node refactor; tolerate
      // older backends that haven't shipped it yet by defaulting
      // to an empty array instead of leaving the field undefined
      // (the palette tests for `catalog.switches ?? []` already).
      switches: raw?.switches ?? [],
      tcp_middlewares: raw?.tcp_middlewares ?? [],
      tcp_handlers: raw?.tcp_handlers ?? [],
    };
  } else {
    // Surface as an empty catalogue so the palette shows "no
    // node kinds available" rather than a forever spinner.
    catalog = { middlewares: [], handlers: [], switches: [], tcp_middlewares: [], tcp_handlers: [] };
  }
  if (statusRes.status === 'fulfilled') {
    status = statusRes.value.data ?? [];
  }
  loaded = true;
}

async function refreshStatus(): Promise<void> {
  try {
    const [s, ls] = await Promise.allSettled([
      axios.get<ProxyInstanceStatus[]>('/api/v1/proxy/status'),
      axios.get<ProxyListenerStatus[]>('/api/v1/proxy/listeners/status'),
    ]);
    if (s.status === 'fulfilled') status = s.value.data ?? [];
    if (ls.status === 'fulfilled') listenersStatus = ls.value.data ?? [];
  } catch {
    // Polling failures shouldn't toast; the UI shows a stale state
    // badge instead so transient blips don't spam the operator.
  }
}

async function createListener(ln: ProxyListener): Promise<ProxyListener> {
  try {
    const res = await axios.post<ProxyListener>('/api/v1/proxy/listeners', ln);
    listeners = [...listeners, res.data];
    addToast('Listener created.', 'success');
    return res.data;
  } catch (error: any) {
    surface(error, 'create listener');
    throw error;
  }
}

async function updateListener(ln: ProxyListener): Promise<ProxyListener> {
  try {
    const res = await axios.put<ProxyListener>(`/api/v1/proxy/listeners/${encodeURIComponent(ln.id)}`, ln);
    listeners = listeners.map(l => (l.id === ln.id ? res.data : l));
    addToast('Listener saved.', 'success');
    return res.data;
  } catch (error: any) {
    surface(error, 'save listener');
    throw error;
  }
}

async function removeListener(id: string): Promise<void> {
  try {
    await axios.delete(`/api/v1/proxy/listeners/${encodeURIComponent(id)}`);
    listeners = listeners.filter(l => l.id !== id);
    addToast('Listener deleted.', 'success');
  } catch (error: any) {
    surface(error, 'delete listener');
    throw error;
  }
}

async function create(srv: ProxyServer): Promise<ProxyServer> {
  try {
    const res = await axios.post<ProxyServer>('/api/v1/proxy', srv);
    servers = [...servers, res.data];
    addToast('Proxy server created.', 'success');
    return res.data;
  } catch (error: any) {
    surface(error, 'create');
    throw error;
  }
}

async function update(srv: ProxyServer): Promise<ProxyServer> {
  try {
    const res = await axios.put<ProxyServer>(`/api/v1/proxy/${encodeURIComponent(srv.id)}`, srv);
    servers = servers.map(s => (s.id === srv.id ? res.data : s));
    addToast('Proxy server saved.', 'success');
    return res.data;
  } catch (error: any) {
    surface(error, 'save');
    throw error;
  }
}

async function remove(id: string): Promise<void> {
  try {
    await axios.delete(`/api/v1/proxy/${encodeURIComponent(id)}`);
    servers = servers.filter(s => s.id !== id);
    addToast('Proxy server deleted.', 'success');
  } catch (error: any) {
    surface(error, 'delete');
    throw error;
  }
}

// validate posts a graph and returns either { ok: true, ... } or a
// CompileError with the offending node id. The UI keeps the error in
// local state so it can colour the broken node.
// runTest sends a single HTTP request through the server-side
// playground proxy so the SPA can test endpoints without bumping
// into CORS. Errors come back as a structured response with
// status:0 rather than rejecting, so the form can render them
// inline next to the success path.
async function runTest(req: ProxyTestRequest): Promise<ProxyTestResponse> {
  try {
    const res = await axios.post<ProxyTestResponse>('/api/v1/proxy/test', req);
    return res.data;
  } catch (error: any) {
    const msg = error.response?.data?.message
      || error.message
      || 'Test request failed';
    addToast(msg, 'alert');
    throw error;
  }
}

async function validate(srv: ProxyServer): Promise<{ ok: true; pipeline: unknown } | ProxyCompileError> {
  try {
    const res = await axios.post(`/api/v1/proxy/${encodeURIComponent(srv.id || 'new')}/validate`, srv);
    return res.data;
  } catch (error: any) {
    if (error.response?.status === 400 && error.response.data) {
      // Backend returns a CompileError as the body. Surface verbatim.
      return error.response.data as ProxyCompileError;
    }
    surface(error, 'validate');
    throw error;
  }
}

function surface(error: any, action: string) {
  const msg = error.response?.data?.message
    || error.response?.data
    || error.message
    || `Failed to ${action} proxy server`;
  addToast(typeof msg === 'string' ? msg : `Failed to ${action} proxy server`, 'alert');
}

export const proxyStore = {
  get servers() { return servers; },
  get listeners() { return listeners; },
  get catalog() { return catalog; },
  get status() { return status; },
  get listenersStatus() { return listenersStatus; },
  get loaded() { return loaded; },
  load,
  refreshStatus,
  create,
  update,
  remove,
  validate,
  runTest,
  createListener,
  updateListener,
  removeListener,
};
