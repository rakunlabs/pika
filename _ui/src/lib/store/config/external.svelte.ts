// External-resource slice of the config store: resource CRUD (stored in
// settings.external) plus the /api/v1/external/* browser endpoints.
// Re-exported via configStore in config.svelte.ts.

import type {
  ExternalEntry,
  ExternalResource,
  ExternalResourceSummary,
  ExternalVersion,
} from '@/lib/types/config';
import { addToast } from '@/lib/store/toast.svelte';
import { apiBlobErrorMessage, apiErrorMessage } from '@/lib/api/client';
import axios from 'axios';
import type { SettingsStore } from './settings.svelte';

export function createExternalStore(store: SettingsStore) {
  // List the child paths under a prefix. This THROWS on failure instead
  // of returning an empty array: a Vault 403 (policy doesn't cover
  // <mount>/metadata/), an expired AppRole lease or an upstream timeout
  // used to be indistinguishable from "this prefix is genuinely empty",
  // so the browser silently rendered an empty tree and the operator had
  // no idea anything went wrong. Callers own the error presentation.
  async function listExternalPaths(resourceName: string, prefix: string = ''): Promise<string[]> {
    try {
      const response = await axios.get<string[]>(`/api/v1/external/${encodeURIComponent(resourceName)}/paths`, {
        params: prefix ? { prefix } : undefined
      });
      return response.data || [];
    } catch (error) {
      throw new Error(apiErrorMessage(error, 'Failed to list external paths'),
      );
    }
  }

  // Search within a single external resource. Mode 'name' is a cheap
  // BFS over List() results matching by substring; 'all' additionally
  // Read()s each leaf and greps the value. Errors return an empty
  // result rather than throwing so the UI can simply show "no hits"
  // — failed search shouldn't kick the user back to a tree view.
  async function searchExternal(
    resourceName: string,
    query: string,
    mode: 'name' | 'all' = 'name',
    limit: number = 200,
  ): Promise<Array<{ path: string; type: 'name' | 'content'; snippet?: string }>> {
    if (!query.trim()) return [];
    try {
      const response = await axios.get(
        `/api/v1/external/${encodeURIComponent(resourceName)}/search`,
        { params: { q: query, mode, limit } }
      );
      return response.data || [];
    } catch {
      return [];
    }
  }

  // Live-probe an external resource using its configured credentials.
  // Backend always returns 200 with {ok,message,sample}; an axios reject
  // here only means the request itself never reached the handler (auth
  // redirect, network, etc.) — surface it as a failed test result so the
  // SPA can render a single error path.
  async function testExternal(
    resourceName: string
  ): Promise<{ ok: boolean; message?: string; sample?: string[] }> {
    try {
      const response = await axios.post<{ ok: boolean; message?: string; sample?: string[] }>(`/api/v1/external/${encodeURIComponent(resourceName)}/test`);
      return response.data || { ok: false, message: 'Empty response' };
    } catch (error) {
      const msg = apiErrorMessage(error, 'Test failed');
      return { ok: false, message: msg };
    }
  }

  // Bulk-export one external resource as a zip archive. The backend
  // walks the whole key space and streams the archive; the response is
  // a Blob, so error bodies arrive as Blobs too and have to be decoded
  // before they can be shown.
  //
  // Server-side this route needs settings.manage (not external.read) —
  // one archive carries every secret the backend holds. Only Consul and
  // Vault resources are exportable; anything else 400s.
  //
  // Returns the archive plus the server-chosen filename so the caller
  // owns the anchor-click download step.
  async function exportExternalResource(
    name: string,
    prefix?: string,
  ): Promise<{ blob: Blob; filename: string }> {
    try {
      const response = await axios.get(
        `/api/v1/external/${encodeURIComponent(name)}/export`,
        { params: prefix ? { prefix } : undefined, responseType: 'blob' }
      );

      let filename = '';
      const cd = response.headers['content-disposition'] || '';
      const m = cd.match(/filename="([^"]+)"/);
      if (m) filename = m[1];
      if (!filename) {
        const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
        filename = `pika-external-${name}-${ts}.zip`;
      }

      return {
        blob: new Blob([response.data], { type: 'application/zip' }),
        filename,
      };
    } catch (error) {
      throw new Error(await apiBlobErrorMessage(error, apiErrorMessage(error, 'Export failed')));
    }
  }

  // Replace or insert a single external resource entry while preserving the
  // rest of settings.external. Centralised here so the new External page,
  // the existing Settings section, and any future caller all hit the same
  // read-modify-write path (the backend stores `external` as a full map).
  async function saveExternalResource(
    name: string,
    resource: ExternalResource
  ): Promise<void> {
    const currentExternal = { ...(store.settings?.external || {}) };
    currentExternal[name] = resource;
    await store.saveSettings({ ...(store.settings || {}), external: currentExternal });
  }

  // Rename keeps the value, drops the old key. Used by the External page's
  // edit mode when the user changes the resource name.
  async function renameExternalResource(oldName: string, newName: string): Promise<void> {
    if (oldName === newName) return;
    const currentExternal = { ...(store.settings?.external || {}) };
    if (!(oldName in currentExternal)) return;
    if (newName in currentExternal) {
      addToast(`Resource "${newName}" already exists`, 'alert');
      throw new Error('duplicate external resource name');
    }
    currentExternal[newName] = currentExternal[oldName];
    delete currentExternal[oldName];
    await store.saveSettings({ ...(store.settings || {}), external: currentExternal });
  }

  async function removeExternalResource(name: string): Promise<void> {
    const currentExternal = { ...(store.settings?.external || {}) };
    if (!(name in currentExternal)) return;
    delete currentExternal[name];
    await store.saveSettings({ ...(store.settings || {}), external: currentExternal });
  }

  // ── External resource browser ───────────────────────────────────────
  // The new External page consumes these. They go through the
  // /api/v1/external/* surface (separate from /api/v1/settings) so
  // a future split could narrow the capability gate without dragging
  // the rest of settings.manage along.

  async function listExternalResourceSummaries(): Promise<ExternalResourceSummary[]> {
    try {
      const res = await axios.get<ExternalResourceSummary[]>('/api/v1/external/resources');
      return res.data || [];
    } catch {
      return [];
    }
  }

  async function readExternalEntry(
    resource: string,
    path: string,
  ): Promise<ExternalEntry> {
    const res = await axios.post(
      `/api/v1/external/${encodeURIComponent(resource)}/read`,
      { path },
    );
    return res.data;
  }

  async function writeExternalEntry(
    resource: string,
    path: string,
    data: Record<string, unknown>,
  ): Promise<void> {
    await axios.post(
      `/api/v1/external/${encodeURIComponent(resource)}/write`,
      { path, data },
    );
  }

  async function deleteExternalEntry(resource: string, path: string): Promise<void> {
    await axios.post(
      `/api/v1/external/${encodeURIComponent(resource)}/delete`,
      { path },
    );
  }

  async function listExternalVersions(
    resource: string,
    path: string,
  ): Promise<ExternalVersion[]> {
    try {
      const res = await axios.post(
        `/api/v1/external/${encodeURIComponent(resource)}/versions`,
        { path },
      );
      return res.data || [];
    } catch {
      // Versions are best-effort: a backend that doesn't support them
      // returns 400 (ErrNotSupported translation). Treat as "no
      // versions" — the SPA renders single-version mode.
      return [];
    }
  }

  async function readExternalVersion(
    resource: string,
    path: string,
    version: string,
  ): Promise<ExternalEntry> {
    const res = await axios.post(
      `/api/v1/external/${encodeURIComponent(resource)}/version`,
      { path, version },
    );
    return res.data;
  }

  return {
    listExternalPaths,
    searchExternal,
    testExternal,
    exportExternalResource,
    saveExternalResource,
    renameExternalResource,
    removeExternalResource,
    listExternalResourceSummaries,
    readExternalEntry,
    writeExternalEntry,
    deleteExternalEntry,
    listExternalVersions,
    readExternalVersion,
  };
}
