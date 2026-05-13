// HTTP layer for the personal vault. Thin wrapper over axios that
// makes the URL → body shape explicit so the store / pages don't
// have to re-derive endpoints. Mirrors internal/server/api/vault.go
// 1:1.

import axios from 'axios';

import type { VaultAccountView, VaultSetupPayload } from './crypto';

// Re-export so callers can `import type { ... } from '.../api'`
// without crossing module boundaries for the same shape.
export type { VaultAccountView, VaultSetupPayload };

export type VaultItemType =
  | 'login'
  | 'card'
  | 'identity'
  | 'secure_note'
  | 'ssh_key'
  | 'api_credential'
  | 'database'
  | 'server'
  | 'license'
  | 'tls_cert';

export interface VaultStatus {
  initialized: boolean;
  item_count: number;
}

export interface VaultItem {
  id: string;
  user_id: string;
  type: VaultItemType;
  /** base64-encoded XChaCha20-Poly1305 ciphertext of the JSON title */
  encrypted_title: string;
  /** base64-encoded ciphertext of the JSON-encoded tag array (empty when absent) */
  encrypted_tags?: string;
  /** base64-encoded ciphertext of the JSON-encoded hostname array */
  encrypted_hostnames?: string;
  /** base64-encoded ciphertext of the full item payload (fields + notes) */
  encrypted_payload: string;
  favorite?: boolean;
  archived?: boolean;
  deleted_at?: string;
  last_used_at?: string;
  created_at: string;
  updated_at: string;
  version: number;
}

export interface VaultItemVersion {
  item_id: string;
  version: number;
  encrypted_title: string;
  encrypted_payload: string;
  updated_at: string;
  author?: string;
}

export interface CreateVaultItemRequest {
  type: VaultItemType;
  encrypted_title: string;
  encrypted_tags?: string;
  encrypted_hostnames?: string;
  encrypted_payload: string;
  favorite?: boolean;
}

/**
 * UpdateVaultItemRequest patches an existing item. ExpectedVersion is
 * the optimistic-concurrency token; the byte fields use the
 * "empty = skip" convention rather than nullable pointers (matches
 * the Go side — see service.UpdateVaultItemRequest).
 */
export interface UpdateVaultItemRequest {
  expected_version: number;
  type?: VaultItemType;
  encrypted_title?: string;
  encrypted_tags?: string;
  encrypted_hostnames?: string;
  encrypted_payload?: string;
  favorite?: boolean;
  archived?: boolean;
}

/**
 * VaultListFilter only carries fields the server can evaluate without
 * touching encrypted content. Free-text search and tag filtering run
 * entirely in the SPA after the list is decrypted in-memory.
 */
export interface VaultListFilter {
  type?: VaultItemType;
  favorite?: boolean;
  archived?: 'include' | 'only';
  trash?: boolean;
}

// ─── Account ─────────────────────────────────────────────────────

export async function getStatus(): Promise<VaultStatus> {
  const res = await axios.get('/api/v1/me/vault/status');
  return res.data as VaultStatus;
}

export async function getAccount(): Promise<VaultAccountView> {
  const res = await axios.get('/api/v1/me/vault/account');
  return res.data as VaultAccountView;
}

export async function setup(payload: VaultSetupPayload): Promise<VaultAccountView> {
  const res = await axios.post('/api/v1/me/vault/setup', payload);
  return res.data as VaultAccountView;
}

export async function unlockCheck(secretKeyHash: string): Promise<void> {
  await axios.post('/api/v1/me/vault/unlock-check', { secret_key_hash: secretKeyHash });
}

export async function rotatePassword(payload: VaultSetupPayload): Promise<VaultAccountView> {
  const res = await axios.post('/api/v1/me/vault/rotate-password', payload);
  return res.data as VaultAccountView;
}

export async function regenerateKit(): Promise<string> {
  const res = await axios.post('/api/v1/me/vault/recovery-kit');
  return (res.data as { recovery_kit_id: string }).recovery_kit_id;
}

export async function setSessionLock(seconds: number): Promise<void> {
  await axios.put('/api/v1/me/vault/session-lock', { session_lock_seconds: seconds });
}

export async function resetVault(): Promise<number> {
  const res = await axios.delete('/api/v1/me/vault');
  return (res.data as { items_deleted: number }).items_deleted;
}

// ─── Items ───────────────────────────────────────────────────────

export async function listItems(filter: VaultListFilter = {}): Promise<VaultItem[]> {
  const params = new URLSearchParams();
  if (filter.type) params.set('type', filter.type);
  if (filter.favorite) params.set('favorite', '1');
  if (filter.archived === 'include') params.set('archived', 'include');
  if (filter.archived === 'only') params.set('archived', 'only');
  if (filter.trash) params.set('trash', '1');
  const res = await axios.get('/api/v1/me/vault/items', { params });
  return (res.data as VaultItem[]) ?? [];
}

export async function getItem(id: string): Promise<VaultItem> {
  const res = await axios.get(`/api/v1/me/vault/items/${encodeURIComponent(id)}`);
  return res.data as VaultItem;
}

export async function createItem(req: CreateVaultItemRequest): Promise<VaultItem> {
  const res = await axios.post('/api/v1/me/vault/items', req);
  return res.data as VaultItem;
}

export async function updateItem(id: string, req: UpdateVaultItemRequest): Promise<VaultItem> {
  const res = await axios.put(`/api/v1/me/vault/items/${encodeURIComponent(id)}`, req);
  return res.data as VaultItem;
}

export async function softDeleteItem(id: string): Promise<void> {
  await axios.delete(`/api/v1/me/vault/items/${encodeURIComponent(id)}`);
}

export async function purgeItem(id: string): Promise<void> {
  await axios.delete(`/api/v1/me/vault/items/${encodeURIComponent(id)}?purge=true`);
}

export async function restoreItem(id: string): Promise<VaultItem> {
  const res = await axios.post(`/api/v1/me/vault/items-restore/${encodeURIComponent(id)}`);
  return res.data as VaultItem;
}

export async function touchItem(id: string): Promise<void> {
  await axios.post(`/api/v1/me/vault/items-use/${encodeURIComponent(id)}`);
}

export async function listItemVersions(id: string): Promise<VaultItemVersion[]> {
  const res = await axios.get(`/api/v1/me/vault/items-versions/${encodeURIComponent(id)}`);
  return (res.data as VaultItemVersion[]) ?? [];
}
