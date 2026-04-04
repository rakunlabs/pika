import axios from 'axios';

import type { RawMount } from '@/lib/types/config';

export interface AppInfo {
  name: string;
  version: string;
  commit?: string;
  date?: string;
  user?: string;
  auth_enabled?: boolean;
  setup_required?: boolean;
  raw_mounts?: RawMount[];
}

export interface UserInfo {
  id: string;
  username: string;
  disabled: boolean;
  created_at: string;
  updated_at: string;
}

function createAppStore() {
  let info = $state<AppInfo | null>(null);
  let authenticated = $state<boolean | null>(null); // null = unknown, true/false = resolved
  let users = $state<UserInfo[]>([]);

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

  async function loadUsers(): Promise<void> {
    try {
      const response = await axios.get('/api/v1/users');
      users = response.data || [];
    } catch {
      users = [];
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
    loadInfo,
    setup,
    login,
    logout,
    loadUsers,
    createUser,
    updateUser,
    deleteUser,
  };
}

export const appStore = createAppStore();
