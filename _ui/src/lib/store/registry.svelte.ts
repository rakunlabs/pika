// Registry API client — typed wrappers around every
// /api/v1/registries/* endpoint the SPA hits.
//
// This file is intentionally narrow in scope: it owns the HTTP
// envelope (URL templating, basePath, credentials, error shape)
// and nothing else. State, toasts, and UI orchestration stay at
// the call sites so each component decides its own UX.
//
// Why a thin client instead of a full $state store like
// proxy.svelte.ts: the Registries page has rich, page-local
// state (selected ns, expanded entry, modal mode, draft) that
// doesn't belong in a global store. Pulling those into a shared
// store would force every consumer to subscribe to the world.
// The wrappers below remove the 9 hand-rolled fetch sites and
// give callers a consistent error shape; that's the win.

import { basePath } from '@/lib/basepath';
import type {
  DockerEntry,
  CargoCrateEntry,
  HelmChartListEntry,
  MavenArtifactEntry,
  ModuleEntry,
  Namespace,
  PackageDetail,
  PackageEntry,
  PyPIPackageEntry,
  ProbeResult,
  RegistryStats,
  RegistryType,
  Repository,
} from '@/lib/components/registry/types';

// ─── Envelope helpers ───

const enc = encodeURIComponent;
const pathTail = (value: string) => value.split('/').map(enc).join('/');
const fetchOpts: RequestInit = { credentials: 'same-origin' };

/**
 * RegistryAPIError carries the HTTP status and an optional server
 * message in a typed shape. Wrappers raise this so callers can
 * decide whether to toast / inline-render / silently fall back.
 */
export class RegistryAPIError extends Error {
  status: number;
  body?: string;
  constructor(status: number, message: string, body?: string) {
    super(message);
    this.name = 'RegistryAPIError';
    this.status = status;
    this.body = body;
  }
}

async function getJSON<T>(url: string): Promise<T> {
  const resp = await fetch(url, fetchOpts);
  if (!resp.ok) {
    const body = await resp.text().catch(() => '');
    throw new RegistryAPIError(resp.status, `HTTP ${resp.status}`, body);
  }
  return (await resp.json()) as T;
}

async function postJSON<T>(url: string, body?: unknown): Promise<T> {
  const init: RequestInit = {
    ...fetchOpts,
    method: 'POST',
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  };
  const resp = await fetch(url, init);
  if (!resp.ok) {
    const bodyText = await resp.text().catch(() => '');
    throw new RegistryAPIError(resp.status, `HTTP ${resp.status}`, bodyText);
  }
  return (await resp.json()) as T;
}

async function deleteNoContent(url: string): Promise<void> {
  const resp = await fetch(url, { ...fetchOpts, method: 'DELETE' });
  if (!resp.ok) {
    const body = await resp.text().catch(() => '');
    throw new RegistryAPIError(resp.status, `HTTP ${resp.status}`, body);
  }
}

async function getText(url: string): Promise<string> {
  const resp = await fetch(url, fetchOpts);
  if (!resp.ok) {
    throw new RegistryAPIError(resp.status, `HTTP ${resp.status}`);
  }
  return resp.text();
}

// ─── Read endpoints ───

/** GET /api/v1/registries → namespace tree. */
export async function listRegistries(): Promise<{ namespaces: Namespace[] }> {
  return getJSON(`${basePath}/api/v1/registries`);
}

/**
 * listEntries dispatches to the per-protocol list endpoint and
 * returns the protocol-shaped payload. Caller switches on
 * repo.type to narrow the result.
 */
export async function listGoModules(ns: string, repo: string): Promise<ModuleEntry[]> {
  return getJSON(`${basePath}/api/v1/registries/go/${enc(ns)}/${enc(repo)}/modules`);
}
export async function listNPMPackages(ns: string, repo: string): Promise<PackageEntry[]> {
  return getJSON(`${basePath}/api/v1/registries/npm/${enc(ns)}/${enc(repo)}/packages`);
}
export async function listDockerRepos(ns: string, repo: string): Promise<DockerEntry[]> {
  return getJSON(`${basePath}/api/v1/registries/docker/${enc(ns)}/${enc(repo)}/repos`);
}
export async function listHelmCharts(ns: string, repo: string): Promise<HelmChartListEntry[]> {
  return getJSON(`${basePath}/api/v1/registries/helm/${enc(ns)}/${enc(repo)}/charts`);
}
export async function listMavenArtifacts(ns: string, repo: string): Promise<MavenArtifactEntry[]> {
  return getJSON(`${basePath}/api/v1/registries/maven/${enc(ns)}/${enc(repo)}/artifacts`);
}
export async function listPyPIPackages(ns: string, repo: string): Promise<PyPIPackageEntry[]> {
  return getJSON(`${basePath}/api/v1/registries/pypi/${enc(ns)}/${enc(repo)}/packages`);
}
export async function listCargoCrates(ns: string, repo: string): Promise<CargoCrateEntry[]> {
  return getJSON(`${basePath}/api/v1/registries/cargo/${enc(ns)}/${enc(repo)}/crates`);
}

/** GET /api/v1/registries/{type}/{ns}/{repo}/stats. */
export async function getStats(type: RegistryType, ns: string, repo: string): Promise<RegistryStats> {
  return getJSON(`${basePath}/api/v1/registries/${type}/${enc(ns)}/${enc(repo)}/stats`);
}

/** GET /api/v1/registries/{type}/{ns}/{repo}/packages/{name...}. */
export async function getPackageDetail(
  type: RegistryType,
  ns: string,
  repo: string,
  name: string,
): Promise<PackageDetail> {
  return getJSON(`${basePath}/api/v1/registries/${type}/${enc(ns)}/${enc(repo)}/packages/${pathTail(name)}`);
}

/** GET /api/v1/registries/npm/{ns}/{repo}/packages/{name}/readme — markdown. */
export async function getNPMReadme(ns: string, repo: string, name: string): Promise<string> {
  return getText(`${basePath}/api/v1/registries/npm/${enc(ns)}/${enc(repo)}/packages/${pathTail(name)}/readme`);
}

/** GET /api/v1/registries/go/{ns}/{repo}/modules/{name...}/versions/{version}/gomod — text. */
export async function getGoMod(
  ns: string,
  repo: string,
  module: string,
  version: string,
): Promise<string> {
  return getText(
    `${basePath}/api/v1/registries/go/${enc(ns)}/${enc(repo)}/modules/${pathTail(module)}/versions/${enc(version)}/gomod`,
  );
}

// ─── Admin endpoints ───

/** POST /api/v1/registries/{type}/{ns}/{repo}/test-upstream. */
export async function probeUpstream(
  type: RegistryType,
  ns: string,
  repo: string,
): Promise<ProbeResult> {
  return postJSON(`${basePath}/api/v1/registries/${type}/${enc(ns)}/${enc(repo)}/test-upstream`);
}

/** POST /api/v1/registries/{type}/{ns}/{repo}/purge. */
export async function purgeCache(
  type: RegistryType,
  ns: string,
  repo: string,
  all: boolean,
): Promise<{ purged_bytes?: number } & Record<string, unknown>> {
  return postJSON(
    `${basePath}/api/v1/registries/${type}/${enc(ns)}/${enc(repo)}/purge`,
    { all },
  );
}

export type DeleteArtifactSelector =
  | { version: string }
  | { tag: string }
  | { digest: string };

/** DELETE /api/v1/registries/{type}/{ns}/{repo}/packages/{name...}?version|tag|digest=... */
export async function deleteRegistryArtifact(
  type: RegistryType,
  ns: string,
  repo: string,
  name: string,
  selector: DeleteArtifactSelector,
): Promise<void> {
  const params = new URLSearchParams();
  if ('version' in selector) params.set('version', selector.version);
  if ('tag' in selector) params.set('tag', selector.tag);
  if ('digest' in selector) params.set('digest', selector.digest);
  const qs = params.toString();
  await deleteNoContent(
    `${basePath}/api/v1/registries/${type}/${enc(ns)}/${enc(repo)}/packages/${pathTail(name)}${qs ? '?' + qs : ''}`,
  );
}

/**
 * GCStats mirrors docker.GCStats on the server. Snake-case wire
 * shape; the UI is free to alias these locally for display.
 *
 * dry_run is true on responses from the estimate endpoint and on
 * any future runs that pass DryRun=true. The UI distinguishes
 * "estimated" from "reclaimed" by reading this flag rather than
 * by the endpoint URL.
 */
export interface GCStats {
  marked_blobs: number;
  swept_blobs: number;
  swept_bytes: number;
  swept_manifests: number;
  skipped_young: number;
  abandoned_uploads_removed: number;
  abandoned_uploads_bytes: number;
  dry_run: boolean;
  errors?: string[];
}

/** POST /api/v1/registries/docker/{ns}/{repo}/gc. */
export async function dockerGC(
  ns: string,
  repo: string,
  minAgeSeconds?: number,
  abandonedUploadMaxAgeSeconds?: number,
): Promise<GCStats> {
  const body: Record<string, number> = {};
  if (minAgeSeconds !== undefined) {
    body.min_age_seconds = minAgeSeconds;
  }
  if (abandonedUploadMaxAgeSeconds !== undefined) {
    body.abandoned_upload_max_age_seconds = abandonedUploadMaxAgeSeconds;
  }
  return postJSON(
    `${basePath}/api/v1/registries/docker/${enc(ns)}/${enc(repo)}/gc`,
    Object.keys(body).length > 0 ? body : undefined,
  );
}

/**
 * GET /api/v1/registries/docker/{ns}/{repo}/gc/estimate.
 *
 * Dry-run pass returning the same shape as dockerGC but with
 * dry_run=true. Used by the UI's "estimated garbage" panel so
 * operators can see reclaimable space before committing to the
 * cleanup.
 */
export async function dockerGCEstimate(
  ns: string,
  repo: string,
  minAgeSeconds?: number,
  abandonedUploadMaxAgeSeconds?: number,
): Promise<GCStats> {
  const params = new URLSearchParams();
  if (minAgeSeconds !== undefined) {
    params.set('min_age_seconds', String(minAgeSeconds));
  }
  if (abandonedUploadMaxAgeSeconds !== undefined) {
    params.set('abandoned_upload_max_age_seconds', String(abandonedUploadMaxAgeSeconds));
  }
  const qs = params.toString();
  const url = `${basePath}/api/v1/registries/docker/${enc(ns)}/${enc(repo)}/gc/estimate${qs ? '?' + qs : ''}`;
  return getJSON<GCStats>(url);
}

// ─── Helpers re-exported for legacy callers ───

/** Re-export so call sites don't have to import Repository for the type alone. */
export type { Repository };
