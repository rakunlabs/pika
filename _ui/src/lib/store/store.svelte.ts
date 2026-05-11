import axios from 'axios';

import type { RawMount, Capability } from '@/lib/types/config';

export interface AppInfo {
  name: string;
  version: string;
  commit?: string;
  date?: string;
  capabilities?: Capability[];
  raw_mounts?: RawMount[];
  // Effective capability keys for the current user (e.g. ["files.read", "users.manage"]).
  // Set by the backend's /api/v1/info endpoint. Empty/undefined means no capabilities.
  permissions?: string[];
  // Superadmin shortcut: when true, the user implicitly has every known capability.
  is_superadmin?: boolean;
  // Username of the current authenticated user (server-side identity).
  user?: string;
}

export interface Identity {
  subject: string;
  name?: string;
  email?: string;
  email_verified?: boolean;
  roles?: string[];
  scopes?: string[];
  provider: string;
  claims?: Record<string, any>;
  issued_at: string;
}

export interface UserInfo {
  id: string;
  username: string;
  disabled: boolean;
  is_superadmin: boolean;
  active_sessions: number;
  created_at: string;
  updated_at: string;
}

export interface UserQuery {
  limit?: number;
  offset?: number;
  sort?: string;
  search?: string;
  // Filter to only users granted this permission bundle ID. Server resolves
  // the matching user IDs and applies an id IN (...) filter, so pagination
  // and sorting still work correctly.
  permissionId?: string;
}

export interface PermissionInfo {
  id: string;
  key: string;
  name: string;
  description: string;
  keys: string[];
  // Optional per-key path-glob restrictions. A key absent from this map (or
  // mapped to an empty array) is unrestricted: matches any path. A non-empty
  // array means the grant only applies to paths matching one of the
  // doublestar globs.
  key_patterns?: Record<string, string[]>;
  created_at: string;
}

export interface LoginStrategy {
  name: string;
  kind: string;
  label: string;
  url: string;
  fields?: any[];
  register?: { url: string; fields?: any[] };
}

export interface LoginInfo {
  title: string;
  subtitle?: string;
  icon?: string;
  version?: string;
  signup_first?: boolean;
  strategies: LoginStrategy[];
}

function createAppStore() {
  let info = $state<AppInfo | null>(null);
  let identity = $state<Identity | null>(null);
  let authenticated = $state<boolean | null>(null); // null = unknown, true/false = resolved
  let users = $state<UserInfo[]>([]);
  let usersTotal = $state(0);
  let lastUserQuery = $state<UserQuery>({});
  let permissions = $state<PermissionInfo[]>([]);
  let loginInfo = $state<LoginInfo | null>(null);

  function hasPermission(key: string): boolean {
    // Must be authenticated to have any capability.
    if (!identity) return false;
    // Superadmin shortcut — implicit grant of every known capability.
    if (info?.is_superadmin) return true;
    // Otherwise check the effective capability set the backend returned on /api/v1/info.
    return info?.permissions?.includes(key) ?? false;
  }

  // Convenience: returns true if the user has ANY of the supplied capability keys.
  function hasAnyPermission(...keys: string[]): boolean {
    return keys.some(k => hasPermission(k));
  }

  async function loadInfo(): Promise<void> {
    try {
      const response = await axios.get('/api/v1/info');
      info = response.data;
    } catch {
      info = { name: 'pika', version: 'unknown', capabilities: [], raw_mounts: [] };
    }
  }

  async function loadIdentity(): Promise<void> {
    try {
      // ada mounts /login/* at the root (not under /api/v1) because the
      // current ada version (v0.1.2) mis-handles non-root Base prefixes.
      const response = await axios.get('/login/me');
      // Guard against SPA fallback: if the catch-all folder handler served
      // index.html with 200, axios will treat it as success. Only accept a
      // real JSON identity object.
      if (!response.data || typeof response.data !== 'object' || !('subject' in response.data)) {
        identity = null;
        authenticated = false;
        return;
      }
      identity = response.data;
      authenticated = true;
    } catch (err: any) {
      if (err?.response?.status === 401) {
        identity = null;
        authenticated = false;
      } else {
        // Network error or other — treat as unauthenticated to avoid infinite spinner
        identity = null;
        authenticated = false;
      }
    }
  }

  async function loadLoginInfo(): Promise<void> {
    const response = await axios.get('/login/info');
    // Same SPA-fallback guard as loadIdentity: if /login/info isn't
    // routed to ada (e.g. it falls through to the folder handler and
    // axios receives the index.html string), don't propagate the bogus
    // shape — it would cause downstream `.strategies.find(...)` to
    // throw synchronously inside Login.svelte's $derived block.
    const data = response.data;
    if (!data || typeof data !== 'object' || !Array.isArray((data as any).strategies)) {
      throw new Error('Invalid /login/info response (expected JSON with strategies array)');
    }
    loginInfo = data as LoginInfo;
  }

  async function loginWith(url: string, body: Record<string, string>): Promise<void> {
    await axios.post(url, body, { headers: { Accept: 'application/json' } });
    await loadIdentity();
  }

  async function registerWith(url: string, body: Record<string, string>): Promise<void> {
    await axios.post(url, body, { headers: { Accept: 'application/json' } });
    await loadIdentity();
  }

  async function logout(): Promise<void> {
    try {
      await axios.post('/logout');
    } finally {
      identity = null;
      authenticated = false;
    }
  }

  function buildUserQueryParams(q: UserQuery): URLSearchParams {
    const params = new URLSearchParams();
    if (q.limit) params.set('_limit', String(q.limit));
    if (q.offset) params.set('_offset', String(q.offset));
    if (q.sort) params.set('_sort', q.sort);
    // Plain string — server applies ILIKE + %wildcard% wrap. UI no longer
    // encodes any SQL pattern syntax, so "%" or "_" in the search term
    // are no longer interpreted as wildcards (server-side transform skips
    // wrapping when the value already contains "%", preserving the
    // backward-compatible escape hatch for direct API callers).
    if (q.search) params.set('username', q.search);
    if (q.permissionId) params.set('permission_id', q.permissionId);
    return params;
  }

  async function loadUsers(q?: UserQuery): Promise<void> {
    if (q !== undefined) {
      lastUserQuery = q;
    }
    try {
      const params = buildUserQueryParams(lastUserQuery);
      const response = await axios.get('/api/v1/users', { params });
      users = response.data?.users || [];
      usersTotal = response.data?.total ?? 0;
    } catch {
      users = [];
      usersTotal = 0;
    }
  }

  async function createUser(username: string, password: string): Promise<UserInfo> {
    const response = await axios.post('/api/v1/users', { username, password });
    await loadUsers();
    return response.data;
  }

  async function updateUser(id: string, data: { username?: string; password?: string; disabled?: boolean }): Promise<void> {
    await axios.patch(`/api/v1/users/${id}`, data);
    await loadUsers();
  }

  async function deleteUser(id: string): Promise<void> {
    await axios.delete(`/api/v1/users/${id}`);
    await loadUsers();
  }

  async function kickUser(id: string): Promise<void> {
    await axios.post(`/api/v1/users-kick/${id}`);
    await loadUsers();
  }

  // Permission CRUD
  async function loadPermissions(): Promise<void> {
    try {
      const response = await axios.get('/api/v1/permissions');
      permissions = response.data || [];
    } catch {
      permissions = [];
    }
  }

  async function createPermission(
    key: string,
    name: string,
    description: string,
    keys: string[],
    keyPatterns?: Record<string, string[]>,
  ): Promise<PermissionInfo> {
    const body: Record<string, unknown> = { key, name, description, keys };
    if (keyPatterns && Object.keys(keyPatterns).length > 0) {
      body.key_patterns = keyPatterns;
    }
    const response = await axios.post('/api/v1/permissions', body);
    await loadPermissions();
    return response.data;
  }

  async function updatePermission(
    id: string,
    data: { key?: string; name?: string; description?: string; keys?: string[]; key_patterns?: Record<string, string[]> },
  ): Promise<void> {
    await axios.patch(`/api/v1/permissions/${id}`, data);
    await loadPermissions();
  }

  async function deletePermission(id: string): Promise<void> {
    await axios.delete(`/api/v1/permissions/${id}`);
    await loadPermissions();
  }

  // User permission assignment
  async function getUserPermissions(userId: string): Promise<PermissionInfo[]> {
    const response = await axios.get(`/api/v1/user-permissions/${userId}`);
    return response.data || [];
  }

  async function setUserPermissions(userId: string, permissionIds: string[]): Promise<void> {
    await axios.put(`/api/v1/user-permissions/${userId}`, { permission_ids: permissionIds });
  }

  // Set up axios interceptor for 401 responses
  axios.interceptors.response.use(
    (response) => response,
    (error) => {
      if (
        error?.response?.status === 401 &&
        !error.config?.url?.includes('/login/')
      ) {
        authenticated = false;
        identity = null;
      }
      return Promise.reject(error);
    }
  );

  return {
    get info() { return info; },
    get identity() { return identity; },
    get authenticated() { return authenticated; },
    get loginInfo() { return loginInfo; },
    get users() { return users; },
    get usersTotal() { return usersTotal; },
    get permissions() { return permissions; },
    hasPermission,
    hasAnyPermission,
    loadInfo,
    loadIdentity,
    loadLoginInfo,
    loginWith,
    registerWith,
    logout,
    loadUsers,
    createUser,
    updateUser,
    deleteUser,
    kickUser,
    loadPermissions,
    createPermission,
    updatePermission,
    deletePermission,
    getUserPermissions,
    setUserPermissions,
  };
}

export const appStore = createAppStore();
