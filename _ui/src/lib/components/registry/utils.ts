// Shared helpers for the registry feature surface.
//
// Lives alongside types.ts so the registry/ folder is fully
// self-contained — page-level Registries.svelte and any future
// extracted subcomponent (RepositoryList, PackageDetailPanel, …)
// import from here instead of redeclaring the same handful of
// pure functions.
//
// Pure functions only; no Svelte state or DOM access. The
// toast-driven copyToClipboard intentionally takes its toast
// callback as a parameter so this file can stay tree-shakeable
// and free of cross-cutting store imports.

import {
  Anchor,
  Cloud,
  Container,
  FileBox,
  Globe,
  Library,
  Package,
} from 'lucide-svelte';

import type { RegistryType } from './types';

// Registry kind discriminator. Mirror of service.RegistryKind* on
// the Go side. Lives here (not in types.ts) because the icon
// helper below is the primary consumer.
export type RegistryKind = 'local' | 'remote' | 'virtual';

/**
 * matchFilter tests whether `haystack` substring-matches the
 * (case-insensitive) `needle`. Empty `needle` matches everything
 * — that is the "no filter applied" state every list view uses.
 */
export function matchFilter(haystack: string, needle: string): boolean {
  if (!needle) return true;
  return haystack.toLowerCase().includes(needle.toLowerCase());
}

/**
 * artifactTypeLabel maps a long OCI/Docker media-type string to a
 * short human-readable badge label. Unknown types fall back to
 * "artifact" so the badge always renders something useful.
 *
 * The map is intentionally small — only the artifact families pika
 * actually surfaces in the Docker browser (Helm, cosign, notary,
 * in-toto, SBOMs, trivy, wasm).
 */
export function artifactTypeLabel(mediaType: string): string {
  const known: Record<string, string> = {
    'application/vnd.cncf.helm.config.v1+json': 'helm',
    'application/vnd.dev.cosign.simplesigning.v1+json': 'cosign',
    'application/vnd.cncf.notary.signature': 'notary',
    'application/vnd.in-toto+json': 'in-toto',
    'application/spdx+json': 'sbom-spdx',
    'application/vnd.cyclonedx+json': 'sbom-cyclonedx',
    'application/vnd.aquasec.trivy.config.v1+json': 'trivy',
    'application/vnd.wasm.config.v1+json': 'wasm',
  };
  return known[mediaType] ?? 'artifact';
}

/**
 * iconFor returns the Lucide icon component for a registry type.
 * Centralised so a new protocol's icon is a one-line change.
 */
export function iconFor(type: RegistryType) {
  switch (type) {
    case 'go': return FileBox;
    case 'npm': return Package;
    case 'docker': return Container;
    case 'helm': return Anchor;
    case 'maven': return Package;
    case 'pypi': return Package;
    case 'cargo': return Package;
  }
}

/**
 * kindIcon returns the Lucide icon component for a registry kind
 * (local / remote / virtual).
 */
export function kindIcon(kind: RegistryKind) {
  switch (kind) {
    case 'local': return Library;
    case 'remote': return Cloud;
    case 'virtual': return Globe;
  }
}

/**
 * endpointURL builds the absolute data-plane URL clients use to
 * pull from / push to a repository. Uses the browser's own origin
 * — correct for the typical "same host as the UI" deployment;
 * operators behind a custom DNS get exact-match URLs by browsing
 * from that host.
 */
export function endpointURL(basePath: string, ns: string, repo: string): string {
  const origin = typeof window !== 'undefined' ? window.location.origin : '';
  return `${origin}${basePath}/registries/${ns}/${repo}`;
}

/**
 * copyToClipboard writes `text` to the system clipboard and
 * surfaces the outcome through the provided `notify` callback.
 * The callback shape matches lib/store/toast's addToast so callers
 * can pass `(msg, kind) => addToast(msg, kind)` directly.
 *
 * Returns a promise that resolves to true on success, false on
 * failure — callers that need to chain extra UI state can await it.
 */
export async function copyToClipboard(
  text: string,
  notify: (msg: string, kind: 'success' | 'alert') => void,
): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    notify('Copied to clipboard', 'success');
    return true;
  } catch {
    notify('Clipboard write failed', 'alert');
    return false;
  }
}
