import axios from 'axios';

import type { RawMount, Capability } from '@/lib/types/config';

export interface AppInfo {
  name: string;
  version: string;
  commit?: string;
  date?: string;
  capabilities?: Capability[];
  raw_mounts?: RawMount[];
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
}

export interface PermissionInfo {
  id: string;
  key: string;
  name: string;
  description: string;
  keys: string[];
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

  function hasPermission(_key: string): boolean {
    // With the new model, the backend authorizes every request; the UI
    // optimistically shows admin nav and relies on 403 feedback.
    return !!identity;
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
    loginInfo = response.data;
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
    if (q.search) params.set('username[like]', `%${q.search}%`);
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

  async function createPermission(key: string, name: string, description: string, keys: string[]): Promise<PermissionInfo> {
    const response = await axios.post('/api/v1/permissions', { key, name, description, keys });
    await loadPermissions();
    return response.data;
  }

  async function updatePermission(id: string, data: { key?: string; name?: string; description?: string; keys?: string[] }): Promise<void> {
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
