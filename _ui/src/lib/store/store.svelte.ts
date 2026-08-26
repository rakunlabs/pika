import axios from 'axios';

import type { Capability } from '@/lib/types/config';
import { withoutBasePath } from '@/lib/basepath';
import { prefsStore } from '@/lib/store/prefs.svelte';

export interface AppInfo {
  name: string;
  version: string;
  commit?: string;
  date?: string;
  // Editable branding subtitle from auth settings; mirrors the value
  // /login/info exposes so the post-login navbar can show the same
  // string the login card shows. Empty/undefined when unset.
  subtitle?: string;
  capabilities?: Capability[];
  // Effective capability keys for the current user (e.g. ["files.read", "users.manage"]).
  // Set by the backend's /api/v1/info endpoint. Empty/undefined means no capabilities.
  permissions?: string[];
  // Superadmin shortcut: when true, the user implicitly has every known capability.
  is_superadmin?: boolean;
  // Username of the current authenticated user (server-side identity).
  user?: string;
  // VaultEnabled mirrors svc.VaultCoord() != nil on the server. When
  // false, the SPA hides the /vault link entirely — the routes themselves
  // 503 in that case, so a stray bookmark just fails closed.
  vault_enabled?: boolean;
  // ManagedTLSEnabled is false when process config disables Pika's
  // built-in HTTPS listener, e.g. Kubernetes Gateway deployments where
  // public TLS is terminated before reaching the pod. In that mode the
  // Certificates settings section is hidden to avoid presenting no-op
  // runtime toggles.
  managed_tls_enabled?: boolean;
  // Login presentation policy. The local strategy remains usable when the
  // form is collapsed; Login.svelte exposes a compact reveal control.
  local_login_name?: string;
  local_login_form_collapsed?: boolean;
  // Effective server-side Account Security access for the current caller.
  account_security_available?: boolean;
  // VaultItemTypes is the server's known item-type vocabulary, used by
  // the new-item picker. Empty when vault is disabled.
  vault_item_types?: string[];
  // EncryptionConfigInvalid is true when the server was started with a
  // non-empty `encryption.password` config value that didn't match the
  // on-disk verifier. The server stays locked in that case; the
  // UnlockScreen surfaces a warning so the operator knows their config
  // file (or PIKA_ENCRYPTION_PASSWORD) needs fixing.
  encryption_config_invalid?: boolean;
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
  email?: string;
  display_name?: string;
  // external is true for users authenticated by an external IdP
  // (OAuth2/Header) rather than a local password.
  external?: boolean;
  disabled: boolean;
  is_superadmin: boolean;
  active_sessions: number;
  // has_totp is true when the user has an Enabled TOTP enrollment.
  // Pending-but-not-confirmed enrollments don't count — only the
  // live second factor flips this flag. The admin UI uses it to
  // show a 2FA badge and conditionally render the Reset action.
  has_totp: boolean;
  // denied_capabilities is the per-user deny overlay (capability keys
  // subtracted from the resolved set regardless of grant source).
  denied_capabilities?: string[];
  created_at: string;
  updated_at: string;
}

// CapSource records where one granted capability key came from.
export interface CapSource {
  capability: string;
  kind: 'superadmin' | 'db_bundle' | 'role' | 'scope';
  bundle?: string;
  role?: string;
  scope?: string;
}

// EffectiveReport is the resolved capability view for a target user.
export interface EffectiveReport {
  username: string;
  user_id: string;
  online: boolean;
  superadmin: boolean;
  superadmin_reason?: 'allowlist' | 'column';
  roles: string[];
  scopes: string[];
  capabilities: string[];
  patterns?: Record<string, string[]>;
  sources: CapSource[];
  denied: string[];
}

// SessionView is an admin-safe projection of one active session. The raw
// session ID is never exposed — `handle` is a hash used for revocation.
export interface SessionView {
  handle: string;
  provider?: string;
  subject?: string;
  current: boolean;
  created_at: string;
  expires_at: string;
}

// UserIdentity is a linked external credential (OAuth2/Header).
export interface UserIdentity {
  id: string;
  user_id: string;
  provider: string;
  subject: string;
  email?: string;
  display_name?: string;
  created_at: string;
  last_login_at?: string;
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
      info = { name: 'pika', version: 'unknown', capabilities: [] };
    }
  }

  async function loadIdentity(): Promise<void> {
    try {
      // /login/* lives beside /api/v1; axios.baseURL applies server.base_path.
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

  // TOTPChallenge is the step-up response shape the MFA-wrapped login
  // strategy writes on phase 1. The caller renders a TOTP form and
  // calls finishMFA() with the session id + the 6-digit code.
  //
  // expires_in is the configured pending TTL in seconds — the UI
  // shows a countdown so the user knows they have ~5 minutes to enter
  // the code before the challenge is discarded and they have to
  // re-enter their password.
  // eslint-disable-next-line no-unused-vars
  interface TOTPChallenge {
    phase: 'totp_required';
    totp_session_id: string;
    strategy: string;
    expires_in: number;
  }

  async function loginWith(url: string, body: Record<string, string>): Promise<TOTPChallenge | null> {
    const res = await axios.post(withoutBasePath(url), body, { headers: { Accept: 'application/json' } });
    // The MFA decorator wraps phase-1 success: when the user is TOTP-
    // enrolled the response body is the step-up challenge instead of
    // ada's standard {strategy, redirect_path}. Detect by the
    // `phase` field — a regular success response never carries it.
    const data = res?.data;
    if (data && typeof data === 'object' && data.phase === 'totp_required') {
      return data as TOTPChallenge;
    }
    // Refresh identity (/login/me), app info (/api/v1/info) and the
    // user's UI preferences in parallel.
    //
    // allSettled (not all): we don't want a single side-channel failure
    // (e.g. /api/v1/me/preferences returning 401 because the backend
    // hasn't been redeployed yet) to surface as a thrown error to the
    // Login.svelte caller and leave the UI on the login screen even
    // though the cookie was set. loadIdentity is the source of truth
    // for "are we authenticated"; the other two are best-effort.
    await Promise.allSettled([loadIdentity(), loadInfo(), prefsStore.loadPreferences()]);
    return null;
  }

  // finishMFA submits the TOTP code (or a recovery code) for a
  // pending step-up challenge. Posts to the SAME url the password
  // POST went to — the MFA strategy dispatches phase-2 by inspecting
  // the body shape (presence of totp_session_id + code).
  //
  // On success the server mints the real session cookie and we
  // refresh identity exactly like loginWith does. On failure the
  // axios error surfaces with the standard {error, message} body.
  async function finishMFA(url: string, totpSessionID: string, code: string): Promise<void> {
    await axios.post(
      withoutBasePath(url),
      { totp_session_id: totpSessionID, code },
      { headers: { Accept: 'application/json' } }
    );
    await Promise.allSettled([loadIdentity(), loadInfo(), prefsStore.loadPreferences()]);
  }

  async function registerWith(url: string, body: Record<string, string>): Promise<void> {
    await axios.post(withoutBasePath(url), body, { headers: { Accept: 'application/json' } });
    // On first signup the backend auto-logs the user in. Mirror loginWith
    // — see loginWith for the rationale behind allSettled.
    await Promise.allSettled([loadIdentity(), loadInfo(), prefsStore.loadPreferences()]);
  }

  // finishExternalLogin runs the post-login fan-out for flows where the
  // session cookie is set out-of-band — i.e. the OAuth2 popup callback, which
  // mints the cookie server-side and closes itself. By the time we call this
  // the cookie is already present; we just refresh identity, app info and
  // preferences so App.svelte's reactive gate flips the login screen for the
  // app. Mirrors loginWith's tail — see loginWith for the allSettled rationale.
  async function finishExternalLogin(): Promise<void> {
    await Promise.allSettled([loadIdentity(), loadInfo(), prefsStore.loadPreferences()]);
  }

  async function logout(): Promise<void> {
    try {
      await axios.post('/logout');
    } finally {
      identity = null;
      authenticated = false;
      // Reset info back to the anonymous capability set and drop the
      // previous user's preferences so a shared device doesn't leak
      // either across logins.
      prefsStore.resetLocal();
      await loadInfo();
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

  // resetUserTOTP wipes a target user's TOTP enrollment from the
  // admin side — used when a user has lost both their authenticator
  // and their recovery codes. Idempotent on the server: calling it
  // on a user without TOTP returns 204 too.
  //
  // Requires CapUsersManage; failures (403, 404) surface as axios
  // errors the caller can map to a toast.
  async function resetUserTOTP(id: string): Promise<void> {
    await axios.delete(`/api/v1/users-totp/${id}`);
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

  // Effective-permission introspection + per-user session/deny control.

  async function getUserEffectivePermissions(userId: string): Promise<EffectiveReport> {
    const response = await axios.get(`/api/v1/users-effective/${userId}`);
    return response.data;
  }

  async function getUserIdentities(userId: string): Promise<UserIdentity[]> {
    const response = await axios.get(`/api/v1/users-identities/${userId}`);
    return response.data?.identities || [];
  }

  async function listUserSessions(userId: string): Promise<SessionView[]> {
    const response = await axios.get(`/api/v1/users-sessions/${userId}`);
    return response.data?.sessions || [];
  }

  async function revokeUserSession(userId: string, handle: string): Promise<void> {
    await axios.delete(`/api/v1/users-sessions/${userId}/${handle}`);
  }

  async function setUserDeniedPermissions(userId: string, capabilityKeys: string[]): Promise<void> {
    await axios.put(`/api/v1/users-denied/${userId}`, { capability_keys: capabilityKeys });
  }

  // Set up axios interceptor for 401 responses.
  //
  // A 401 from a regular API call means the session expired (or the
  // user was never authenticated to begin with) — flip the global gate
  // so App.svelte swaps in the Login component.
  //
  // Exceptions — these endpoints are intentionally probed before/around
  // a session exists and a 401 from them is not evidence that the user
  // got logged out:
  //   /login/*                — auth dance itself (login, signup, /me)
  //   /api/v1/info            — capability bootstrap; works anonymous
  //   /api/v1/me/preferences  — fetched right after /login/password as
  //                             part of the post-login fan-out. If the
  //                             backend doesn't (yet) expose it, or the
  //                             session cookie isn't visible to this
  //                             request for any reason, treating that
  //                             401 as a logout would bounce the user
  //                             straight back to the login screen even
  //                             though they just authenticated
  //                             successfully.
  const SKIP_401_PATHS = ['/login/', '/api/v1/info', '/api/v1/me/preferences'];
  axios.interceptors.response.use(
    (response) => response,
    (error) => {
      const url: string = error?.config?.url ?? '';
      const skip = SKIP_401_PATHS.some((p) => url.includes(p));
      if (error?.response?.status === 401 && !skip) {
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
    finishMFA,
    registerWith,
    finishExternalLogin,
    logout,
    loadUsers,
    createUser,
    updateUser,
    deleteUser,
    kickUser,
    resetUserTOTP,
    loadPermissions,
    createPermission,
    updatePermission,
    deletePermission,
    getUserPermissions,
    setUserPermissions,
    getUserEffectivePermissions,
    getUserIdentities,
    listUserSessions,
    revokeUserSession,
    setUserDeniedPermissions,
  };
}

export const appStore = createAppStore();
