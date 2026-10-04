import type { UserQuery } from '@/lib/store/store.svelte';

/**
 * Users-tab query state (search / sort / pagination / permission filter).
 * Lives in the Users page rather than in UsersTab so it survives switching
 * between the Users and Permissions tabs, and so the Permissions tab can
 * jump to a pre-filtered user list.
 */
export class UserQueryState {
  searchText = $state('');
  sortField = $state('username');
  sortDir = $state<'asc' | 'desc'>('asc');
  pageSize = $state(20);
  currentPage = $state(1);
  // Permission filter — when set, only users granted this permission bundle
  // are returned. Server resolves the matching user IDs and applies an
  // id IN (...) filter so pagination/sort still work.
  filterPermissionId = $state('');

  build(): UserQuery {
    return {
      limit: this.pageSize,
      offset: (this.currentPage - 1) * this.pageSize,
      sort: this.sortDir === 'desc' ? `-${this.sortField}` : this.sortField,
      search: this.searchText || undefined,
      permissionId: this.filterPermissionId || undefined,
    };
  }
}

export interface KnownCapability {
  key: string;
  name: string;
  description: string;
}

// Strip empties + return undefined when no patterns remain, so we don't
// send `key_patterns: {}` in request bodies.
export function cleanPatterns(
  map: Record<string, string[]>,
  allowedKeys: string[],
): Record<string, string[]> | undefined {
  const out: Record<string, string[]> = {};
  for (const k of Object.keys(map)) {
    if (!allowedKeys.includes(k)) continue;
    const trimmed = (map[k] ?? []).map((s) => s.trim()).filter(Boolean);
    if (trimmed.length > 0) out[k] = trimmed;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

// Compares two pattern maps (after cleaning) for content equality.
export function patternsEqual(
  a: Record<string, string[]> | undefined,
  b: Record<string, string[]> | undefined,
): boolean {
  const ka = a ? Object.keys(a).sort() : [];
  const kb = b ? Object.keys(b).sort() : [];
  if (ka.length !== kb.length) return false;
  for (let i = 0; i < ka.length; i++) {
    if (ka[i] !== kb[i]) return false;
    const av = [...(a?.[ka[i]] ?? [])].sort();
    const bv = [...(b?.[kb[i]] ?? [])].sort();
    if (av.length !== bv.length) return false;
    for (let j = 0; j < av.length; j++) if (av[j] !== bv[j]) return false;
  }
  return true;
}
