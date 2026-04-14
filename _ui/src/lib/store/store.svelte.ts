import axios from 'axios';

import type { RawMount, Capability } from '@/lib/types/config';

export interface AppInfo {
  name: string;
  version: string;
  commit?: string;
  date?: string;
  user?: string;
  // auth_enabled is true when permission checks are enforced — that is,
  // under built-in auth OR under forward auth with ExternalPermissions.Enabled.
  auth_enabled?: boolean;
  // builtin_auth is true only when session-based built-in auth is active.
  // The Users/Permissions management pages require this.
  builtin_auth?: boolean;
  // forward_auth_enabled is true when the forward-auth Slot is active.
  forward_auth_enabled?: boolean;
  // forward_auth_redirect_url is the external SSO login URL (if configured).
  forward_auth_redirect_url?: string;
  is_superadmin?: boolean;
  permissions?: string[];
  // capabilities is the canonical list of capability keys the server knows
  // about, with display names and descriptions. Used by the UI to render
  // permission-bundle editors and the external-permissions mapping form.
  capabilities?: Capability[];
  setup_required?: boolean;
  raw_mounts?: RawMount[];
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

function createAppStore() {
  let info = $state<AppInfo | null>(null);
  let authenticated = $state<boolean | null>(null); // null = unknown, true/false = resolved
  let users = $state<UserInfo[]>([]);
  let usersTotal = $state(0);
  let lastUserQuery = $state<UserQuery>({});
  let permissions = $state<PermissionInfo[]>([]);

  function hasPermission(key: string): boolean {
    if (!info?.auth_enabled) return true;
    if (info?.is_superadmin) return true;
    return info?.permissions?.includes(key) ?? false;
  }

  async function loadInfo(): Promise<void> {
    try {
      const response = await axios.get('/api/v1/info');
      info = response.data;
      authenticated = true;
    } catch (err: any) {
      if (err?.response?.status === 401) {
        authenticated = false;
        // Check if this is a fresh install needing setup
        info = { name: 'pika', version: 'unknown', auth_enabled: true };
        try {
          const setupRes = await axios.get('/api/v1/auth/setup');
          if (setupRes.data?.required) {
            info = { ...info, setup_required: true };
          }
        } catch {
          // Setup endpoint not available, just show login
        }
      } else {
        info = { name: 'pika', version: 'unknown' };
        authenticated = true; // No auth configured
      }
    }
  }

  async function setup(username: string, password: string): Promise<void> {
    await axios.post('/api/v1/auth/setup', { username, password });
    authenticated = true;
    // Reload info to get full server details now that we're authenticated
    await loadInfo();
  }

  async function login(username: string, password: string): Promise<void> {
    const response = await axios.post('/api/v1/auth/login', { username, password });
    authenticated = true;
    // Reload info to get user details
    await loadInfo();
  }

  async function logout(): Promise<void> {
    try {
      await axios.post('/api/v1/auth/logout');
    } catch {
      // Ignore errors
    }
    authenticated = false;
    info = { name: 'pika', version: 'unknown', auth_enabled: true };
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
        info?.auth_enabled &&
        !error.config?.url?.includes('/auth/')
      ) {
        authenticated = false;
      }
      return Promise.reject(error);
    }
  );

  return {
    get info() { return info; },
    get authenticated() { return authenticated; },
    get users() { return users; },
    get usersTotal() { return usersTotal; },
    get permissions() { return permissions; },
    hasPermission,
    loadInfo,
    setup,
    login,
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
