import axios from 'axios';

export interface AppInfo {
  name: string;
  version: string;
  commit?: string;
  date?: string;
  user?: string;
}

function createAppStore() {
  let info = $state<AppInfo | null>(null);

  async function loadInfo(): Promise<void> {
    try {
      const response = await axios.get('/api/v1/info');
      info = response.data;
    } catch {
      info = { name: 'pika', version: 'unknown' };
    }
  }

  return {
    get info() { return info; },
    loadInfo
  };
}

export const appStore = createAppStore();
