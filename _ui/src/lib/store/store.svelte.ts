import axios from 'axios';

export interface AppInfo {
  name: string;
  version: string;
  commit?: string;
  date?: string;
  user?: string;
  auth_enabled?: boolean;
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
        // Still try to get basic info without auth
        info = { name: 'pika', version: 'unknown', auth_enabled: true };
      } else {
        info = { name: 'pika', version: 'unknown' };
        authenticated = true; // No auth configured
      }
    }
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
    login,
    logout,
    loadUsers,
    createUser,
    updateUser,
    deleteUser,
  };
}

export const appStore = createAppStore();
