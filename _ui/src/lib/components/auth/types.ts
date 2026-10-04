// Shared types and helpers for the Authentication settings editors.

export interface OAuth2Entry {
  name: string;
  display_name?: string;
  auth_url?: string;
  token_url?: string;
  userinfo_url?: string;
  jwks_url?: string;
  issuer_url?: string;
  client_id?: string;
  client_secret?: string;
  // client_secret_set is a read-only indicator from GET /settings: the
  // backend masks the real secret and only tells us whether one exists.
  client_secret_set?: boolean;
  // clear_client_secret is a write-only flag: when true on save, the
  // backend deliberately wipes the stored secret instead of keeping it.
  clear_client_secret?: boolean;
  scopes?: string[];
  disable_pkce?: boolean;
  password_flow?: boolean;
  // How client credentials are sent to the token endpoint:
  // "basic" (default — HTTP Basic header / client_secret_basic),
  // "post" (sent as request params / client_secret_post), or
  // "bearer" (Authorization: Bearer <secret>). Empty == basic.
  token_auth_method?: string;
  auto_create_user?: boolean;
  // Dotted claim paths roles are read from. Empty means the default
  // ["roles"]. Supports nesting + a "*" wildcard for Keycloak, e.g.
  // ["realm_access.roles", "resource_access.*.roles"].
  roles_claims?: string[];
}

export interface AuthSettings {
  ui?: {
      title?: string;
      subtitle?: string;
      icon?: string;
      // Note: version is intentionally absent. The login UI displays the
      // server version from /api/v1/info (build-time ldflags), not from
      // editable settings, so the operator can't desync the displayed
      // version from the actual binary.
      theme?: Record<string, string>;
      custom_css_url?: string;
  };
  cookie?: {
      name?: string;
      domain?: string;
      path?: string;
      secure?: boolean;
      disable_http_only?: boolean;
      same_site?: string;
  };
  issuer?: {
      access_ttl?: number;
      refresh_ttl?: number;
      disable_refresh_rotation?: boolean;
  };
  local?: {
      enabled: boolean;
      name?: string;
      login_form_collapsed?: boolean;
  };
  account_security_admin_only?: boolean;
  oauth2?: OAuth2Entry[];
  header?: {
      name?: string;
      user?: string;
      email?: string;
      display_name_header?: string;
      roles?: string;
      groups?: string;
      trusted_proxies?: string[];
  };
  // Passkey strategy config (WebAuthn). Per-user enrollment lives in
  // /api/v1/me/passkeys; this block only describes RP identity + UV
  // policy. challenge_ttl is a Go time.Duration (nanoseconds) on the
  // wire, normalised to seconds in the form.
  passkey?: {
      enabled: boolean;
      name?: string;
      label?: string;
      rp_id?: string;
      rp_display_name?: string;
      rp_origins?: string[];
      user_verification?: string;
      challenge_ttl?: number;
  };
  // Access tokens are accepted only via `Authorization: Bearer <key>`.
  // No tunable header, no fallback — the settings surface would imply
  // flexibility that isn't actually wanted.
  capabilities?: {
      superadmins?: string[];
      role_mapping?: Record<string, string[]>;
      scope_mapping?: Record<string, string[]>;
  };
  rate_limit?: {
      enabled?: boolean;
      // Durations are Go time.Duration (nanoseconds) on the wire.
      window?: number;
      ip_soft_threshold?: number;
      ip_hard_threshold?: number;
      user_soft_threshold?: number;
      user_hard_threshold?: number;
      backoff_base?: number;
      backoff_max?: number;
      trusted_proxy_cidrs?: string[];
  };
}

// ── Duration helpers ──
// The backend serializes time.Duration as nanoseconds (Go's default
// json.Marshal for time.Duration). The UI works in seconds for the user's
// sake; the helpers below convert at the boundaries.
export const NS_PER_SECOND = 1_000_000_000;
export function nsToSec(v: number | undefined): number | "" {
  if (v == null || v === 0) return "";
  return Math.round(v / NS_PER_SECOND);
}
export function secToNs(v: number | string): number {
  const n = typeof v === "string" ? Number(v) : v;
  if (!Number.isFinite(n) || n <= 0) return 0;
  return Math.round(n * NS_PER_SECOND);
}

/**
 * Edit-time representation of a role/scope → permission mapping: a key
 * (role or scope name as it appears in the identity) paired with a set of
 * pika Permission bundle keys granted when that role/scope is present.
 */
export type MappingRow = { key: string; permissions: string[] };

// Convert a Record<string, string[]> into edit-friendly row array.
// Stable order is alphabetical by key so the form doesn't jitter across
// reloads (JSON object iteration order is technically insertion-preserving
// but server-side encoding may not preserve it).
export function mapToRows(m: Record<string, string[]> | undefined): MappingRow[] {
  if (!m) return [];
  return Object.keys(m)
      .sort()
      .map((k) => ({ key: k, permissions: [...(m[k] ?? [])] }));
}

// Convert back to the wire format, dropping rows with empty keys and
// deduplicating the permission-key slices.
export function rowsToMap(
  rows: MappingRow[],
): Record<string, string[]> | undefined {
  const out: Record<string, string[]> = {};
  for (const row of rows) {
      const k = row.key.trim();
      if (!k) continue;
      const perms = Array.from(
          new Set(row.permissions.filter((c) => !!c)),
      );
      if (perms.length === 0) continue;
      out[k] = perms;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

export function hasOAuth2ManualEndpoints(entry: OAuth2Entry): boolean {
  return !!(entry.token_url && (entry.password_flow || entry.auth_url));
}
