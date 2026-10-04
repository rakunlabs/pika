// Settings slice of the config store: the settings document itself plus
// the per-section save helpers (vault, TLS, MCP, hooks, event log,
// public endpoints). Re-exported via configStore in config.svelte.ts.

import type {
  AuditSettings,
  EventLogSettings,
  Hook,
  MCPSettings,
  PublicEndpoint,
  PublicEndpointStatus,
  PublicEndpointTestResult,
  RequestCheck,
  RequestRuleTestResult,
  ServerTLSSettings,
  Settings,
  VaultFilesSettings,
  VaultSettings,
} from '@/lib/types/config';
import { addToast } from '@/lib/store/toast.svelte';
import { appStore } from '@/lib/store/store.svelte';
import { apiServerMessage } from '@/lib/api/client';
import axios from 'axios';

export function createSettingsStore() {
  let settings = $state<Settings | null>(null);

  async function fetchSettings(): Promise<Settings> {
    try {
      const response = await axios.get<Settings>('/api/v1/settings');
      return response.data;
    } catch {
      return { external: {} };
    }
  }

  // Settings operations
  async function loadSettings(): Promise<void> {
    settings = await fetchSettings();
  }

  async function saveSettings(updatedSettings: Settings): Promise<void> {
    try {
      const body: Record<string, unknown> = {
        action: 'set',
        external: updatedSettings.external || {}
      };
      await axios.post('/api/v1/settings', body);
      settings = updatedSettings;
      addToast('Settings saved', 'success');
    } catch (error) {
      console.error('Failed to save settings:', error);
      const msg = apiServerMessage(error, 'Failed to save settings');
      addToast(msg, 'alert');
      throw error;
    }
  }

  // saveVaultSettings flips the deployment-level personal-vault
  // feature flag. The server stores the value in the Settings row;
  // the next /api/v1/info response reflects the new state and the
  // SPA's vault link disappears (or reappears) accordingly.
  async function saveVaultSettings(
    patch: VaultSettings,
    successMessage?: string,
  ): Promise<void> {
    // The server replaces the whole vault object, so merge with what
    // is stored to avoid clobbering the other flag.
    const next: VaultSettings = { ...(settings?.vault ?? {}), ...patch };
    try {
      await axios.post('/api/v1/settings', {
        action: 'set',
        vault: next,
      });
      if (settings) {
        settings = { ...settings, vault: next };
      } else {
        settings = { vault: next };
      }
      // Refresh /api/v1/info so the navbar / route gate
      // (appStore.info.vault_enabled) updates immediately.
      await appStore.loadInfo();
      addToast(
        successMessage ??
          (next.disabled
            ? 'Personal vault disabled for this deployment.'
            : 'Personal vault enabled for this deployment.'),
        'success',
      );
    } catch (error) {
      const msg = apiServerMessage(error, 'Failed to save vault settings');
      addToast(msg, 'alert');
      throw error;
    }
  }

  // saveVaultFilesSettings stores the vault file storage backend. The
  // response never echoes the S3 secret, so reload the masked view.
  async function saveVaultFilesSettings(patch: VaultFilesSettings): Promise<void> {
    try {
      await axios.post('/api/v1/settings', { action: 'set', vault_files: patch });
      settings = await fetchSettings();
      addToast('Vault storage settings saved.', 'success');
    } catch (error) {
      addToast(apiServerMessage(error, 'Failed to save vault storage settings'), 'alert');
      throw error;
    }
  }

  async function testVaultFilesSettings(
    cfg: VaultFilesSettings,
  ): Promise<{ ok: boolean; message?: string }> {
    const response = await axios.post<{ ok: boolean; message?: string }>(
      '/api/v1/settings-vault-files/test',
      cfg,
    );
    return response.data;
  }

  async function saveServerTLSSettings(
    patch: ServerTLSSettings,
  ): Promise<void> {
    try {
      await axios.post('/api/v1/settings', {
        action: 'set',
        server_tls: patch,
      });
      if (settings) {
        settings = { ...settings, server_tls: patch };
      } else {
        settings = { server_tls: patch };
      }
      addToast('HTTPS settings saved', 'success');
    } catch (error) {
      const msg = apiServerMessage(error, 'Failed to save HTTPS settings');
      addToast(msg, 'alert');
      throw error;
    }
  }

  // saveAuditSettings stores the audit retention override. An empty
  // retention clears the override so the config value applies again.
  async function saveAuditSettings(patch: AuditSettings): Promise<void> {
    try {
      await axios.post('/api/v1/settings', { action: 'set', audit: patch });
      const audit = patch.retention ? patch : undefined;
      settings = { ...settings, audit };
      addToast('Audit settings saved', 'success');
    } catch (error) {
      addToast(apiServerMessage(error, 'Failed to save audit settings'), 'alert');
      throw error;
    }
  }

  async function saveMCPSettings(patch: MCPSettings): Promise<void> {
    try {
      await axios.post('/api/v1/settings', { action: 'set', mcp: patch });
      settings = { ...settings, mcp: patch };
      addToast('MCP settings saved', 'success');
    } catch (error) {
      addToast(apiServerMessage(error, 'Failed to save MCP settings'), 'alert');
      throw error;
    }
  }

  async function saveHooks(hooks: Hook[]): Promise<void> {
    try {
      await axios.post('/api/v1/settings', {
        action: 'set',
        hooks: hooks
      });
      if (settings) {
        settings = { ...settings, hooks: hooks };
      } else {
        settings = { hooks: hooks };
      }
      addToast('Hooks saved', 'success');
    } catch (error) {
      const msg = apiServerMessage(error, 'Failed to save hooks');
      addToast(msg, 'alert');
      throw error;
    }
  }

  async function saveEventLogSettings(patch: EventLogSettings): Promise<void> {
    try {
      await axios.post('/api/v1/settings', {
        action: 'set',
        event_log: patch,
      });
      if (settings) {
        settings = { ...settings, event_log: patch };
      } else {
        settings = { event_log: patch };
      }
      addToast(
        patch.disabled
          ? 'Event logging disabled.'
          : 'Event logging enabled.',
        'success',
      );
    } catch (error) {
      const msg = apiServerMessage(error, 'Failed to save event logging setting');
      addToast(msg, 'alert');
      throw error;
    }
  }

  // savePublicEndpoints persists the desired list of extra listener
  // compatibility / custom-modifier endpoints. Full-replace
  // semantics: the backend takes the submitted array verbatim and
  // reconciles its live listeners against it (Hooks-style).
  async function savePublicEndpoints(
    endpoints: PublicEndpoint[],
  ): Promise<void> {
    try {
      await axios.post('/api/v1/settings', {
        action: 'set',
        public_endpoints: endpoints,
      });
      if (settings) {
        settings = { ...settings, public_endpoints: endpoints };
      } else {
        settings = { public_endpoints: endpoints };
      }
      addToast('Endpoints saved', 'success');
    } catch (error) {
      const msg = apiServerMessage(error, 'Failed to save endpoints');
      addToast(msg, 'alert');
      throw error;
    }
  }

  // listPublicEndpointStatus polls the diagnostic surface so the UI
  // can show "running / disabled / bind-failed" badges next to each
  // entry. Returns [] on any error so the UI keeps rendering.
  async function listPublicEndpointStatus(): Promise<PublicEndpointStatus[]> {
    try {
      const response = await axios.get<PublicEndpointStatus[]>('/api/v1/public-endpoints/status');
      return response.data || [];
    } catch {
      return [];
    }
  }

  // testPublicEndpoint runs a synthetic probe against an endpoint
  // through the live handler chain. The optional `headers` map is
  // forwarded onto the synthetic request so operators can exercise
  // both the auth chain and any request-check rules in one shot.
  async function testPublicEndpoint(
    id: string,
    req: {
      key: string;
      variant?: string;
      version?: string;
      raw?: boolean;
      format?: string;
      headers?: Record<string, string>;
    },
  ): Promise<PublicEndpointTestResult> {
    const response = await axios.post<PublicEndpointTestResult>(`/api/v1/public-endpoints/${id}/test`, req);
    return response.data;
  }

  // testPublicEndpointRules dry-runs a draft request_check block
  // through the backend's real Go evaluator. The endpoint does not
  // need to be saved first, which keeps the modal edit loop safe.
  async function testPublicEndpointRules(req: {
    request_check: RequestCheck;
    method?: string;
    path: string;
    headers?: Record<string, string>;
  }): Promise<RequestRuleTestResult> {
    const response = await axios.post<RequestRuleTestResult>('/api/v1/public-endpoints/test-rules', req);
    return response.data;
  }

  return {
    get settings() { return settings; },
    loadSettings,
    saveSettings,
    saveVaultSettings,
    saveVaultFilesSettings,
    testVaultFilesSettings,
    saveServerTLSSettings,
    saveAuditSettings,
    saveMCPSettings,
    saveHooks,
    saveEventLogSettings,
    savePublicEndpoints,
    listPublicEndpointStatus,
    testPublicEndpoint,
    testPublicEndpointRules,
  };
}

export type SettingsStore = ReturnType<typeof createSettingsStore>;
