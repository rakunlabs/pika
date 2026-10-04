// HTTP layer for the personal-vault file browser. Mirrors
// internal/server/api/vault_files.go. File content is stored
// unencrypted on the server-side storage backend (local disk or S3).

import axios from 'axios';
import { withBasePath } from '@/lib/basepath';

export interface VaultFile {
  id: string;
  user_id: string;
  /** "" = root */
  parent_id: string;
  name: string;
  is_dir: boolean;
  size: number;
  content_type?: string;
  backend?: string;
  created_at: string;
  updated_at: string;
}

export interface VaultFilesInfo {
  enabled: boolean;
  backend?: string;
  files: VaultFile[];
}

export async function listFiles(): Promise<VaultFilesInfo> {
  const res = await axios.get('/api/v1/me/vault/files');
  const data = res.data as VaultFilesInfo;
  return { ...data, files: data.files ?? [] };
}

export async function createFolder(parentId: string, name: string): Promise<VaultFile> {
  const res = await axios.post('/api/v1/me/vault/files-folder', { parent_id: parentId, name });
  return res.data as VaultFile;
}

export interface UploadOptions {
  parentId: string;
  /** Relative folder path ("a/b") created below parentId. */
  relDir?: string;
  replace?: boolean;
  signal?: AbortSignal;
  onProgress?: (loaded: number, total: number) => void;
}

export async function uploadFile(file: File, opts: UploadOptions): Promise<VaultFile> {
  const params = new URLSearchParams({ name: file.name });
  if (opts.parentId) params.set('parent_id', opts.parentId);
  if (opts.relDir) params.set('path', opts.relDir);
  if (opts.replace) params.set('replace', 'true');
  const res = await axios.put(`/api/v1/me/vault/files-upload?${params}`, file, {
    headers: { 'Content-Type': file.type || 'application/octet-stream' },
    signal: opts.signal,
    // Uploads have no size limit; never time out mid-transfer.
    timeout: 0,
    onUploadProgress: (e) => opts.onProgress?.(e.loaded, e.total ?? file.size),
  });
  return res.data as VaultFile;
}

export async function updateFile(
  id: string,
  patch: { name?: string; parent_id?: string },
): Promise<VaultFile> {
  const res = await axios.patch(`/api/v1/me/vault/files/${encodeURIComponent(id)}`, patch);
  return res.data as VaultFile;
}

export async function deleteFile(id: string): Promise<number> {
  const res = await axios.delete(`/api/v1/me/vault/files/${encodeURIComponent(id)}`);
  return (res.data as { deleted: number }).deleted;
}

/** Same-origin URL for previews (<img>, <video>, ...). The session
 *  cookie authenticates the request. */
export function contentUrl(id: string, download = false): string {
  const base = withBasePath(`/api/v1/me/vault/files-content/${encodeURIComponent(id)}`);
  return download ? `${base}?download=1` : base;
}

/** Zip download of a folder; without an id, of every file. */
export function zipUrl(folderId = ''): string {
  return withBasePath(
    folderId ? `/api/v1/me/vault/files-zip/${encodeURIComponent(folderId)}` : '/api/v1/me/vault/files-zip',
  );
}

/**
 * Fetches raw bytes. With `maxBytes`, only the first maxBytes are
 * requested (HTTP Range), so huge files can still be inspected.
 */
export async function readBytes(
  id: string,
  opts: { signal?: AbortSignal; maxBytes?: number } = {},
): Promise<Uint8Array> {
  const headers: Record<string, string> = {};
  if (opts.maxBytes) headers.Range = `bytes=0-${opts.maxBytes - 1}`;
  const res = await axios.get(`/api/v1/me/vault/files-content/${encodeURIComponent(id)}`, {
    responseType: 'arraybuffer',
    headers,
    signal: opts.signal,
    timeout: 0,
  });
  const buf = new Uint8Array(res.data as ArrayBuffer);
  return opts.maxBytes && buf.length > opts.maxBytes ? buf.slice(0, opts.maxBytes) : buf;
}

/** Fetches a file's content as text. */
export async function readText(id: string, signal?: AbortSignal): Promise<string> {
  const res = await axios.get(`/api/v1/me/vault/files-content/${encodeURIComponent(id)}`, {
    responseType: 'text',
    transformResponse: (d) => d,
    signal,
    timeout: 0,
  });
  return res.data as string;
}

/**
 * Overwrites a file's content. `ifUpdatedAt` is the updated_at the
 * editor loaded; the server answers 409 when the file changed since.
 */
export async function writeText(
  id: string,
  text: string,
  opts: { ifUpdatedAt?: string; contentType?: string } = {},
): Promise<VaultFile> {
  const params = new URLSearchParams();
  if (opts.ifUpdatedAt) params.set('if_updated_at', opts.ifUpdatedAt);
  const q = params.toString();
  const blob = new Blob([text], { type: opts.contentType || 'text/plain;charset=utf-8' });
  const res = await axios.put(
    `/api/v1/me/vault/files-content/${encodeURIComponent(id)}${q ? `?${q}` : ''}`,
    blob,
    { headers: { 'Content-Type': blob.type }, timeout: 0 },
  );
  return res.data as VaultFile;
}

/** Creates a new text file with the given content in parentId. */
export async function createTextFile(parentId: string, name: string, text = ''): Promise<VaultFile> {
  const file = new File([text], name, { type: textContentType(name) });
  return uploadFile(file, { parentId });
}

/** Best-effort content type for a text file name. */
export function textContentType(name: string): string {
  const ext = name.includes('.') ? name.slice(name.lastIndexOf('.') + 1).toLowerCase() : '';
  switch (ext) {
    case 'md':
    case 'markdown':
      return 'text/markdown;charset=utf-8';
    case 'json':
      return 'application/json';
    case 'yaml':
    case 'yml':
      return 'application/yaml';
    case 'html':
    case 'htm':
      return 'text/html;charset=utf-8';
    case 'csv':
      return 'text/csv;charset=utf-8';
    default:
      return 'text/plain;charset=utf-8';
  }
}
