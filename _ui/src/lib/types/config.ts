// Configuration file format types
export type FileFormat = 'json' | 'yaml' | 'toml' | 'raw';

// File status from backend
export type FileStatusType = 'CREATED' | 'DELETED';

// Version status info
export interface FileStatus {
  status: FileStatusType;
  timestamp: number;
  author: string;
}

// Version info
export interface FileVersion {
  version: number;
  status: FileStatus[];
  constraint?: string; // semver constraint, e.g., ">= 0.2.5"
}

// A single inheritance entry
export interface InheritEntry {
  // Legacy raw-mount inheritance entries can still appear in saved tabs and
  // preview state; the backend no longer creates new ones after the kutu split.
  mount?: string;
  source?: string;    // Internal file path (for internal inheritance)
  resource?: string;  // External resource name from settings (for external inheritance)
  path?: string;      // Resource-specific path (e.g., Vault secret path, HTTP endpoint path)
  paths?: string[];   // Fields to pull from the source (dot-notation, wildcards)
  inject?: string;    // Where to place in the config (dot-notation, empty = root)
  // Decoder hint for non-JSON external payloads. When the provider
  // returns {"value": "<raw-string>"} (Consul / etcd / GCP / HTTP fallback
  // for unparseable bodies), this tells the backend to parse that string
  // as the named format before merging. Empty / omitted = current
  // behaviour (no special handling). Internal sources use their own
  // meta.format and ignore this field.
  format?: 'json' | 'yaml' | 'toml';
}

// File metadata
export interface FileMeta {
  description?: string;
  format?: FileFormat;
  inherits?: InheritEntry[];
  // When true, the backend runs this file through mugo/Go templates
  // before format parsing and inheritance resolution. Omitted = false.
  go_template?: boolean;
}

// File data from API
export interface FileData {
  meta: FileMeta;
  data: string; // Base64 encoded or raw string
}

// Folder data from API
export interface FolderData {
  folders: string[];
  files: string[];
  variants?: Record<string, string[]>; // file name -> variant keys
}

// Tree node for file tree UI
export interface TreeNode {
  name: string;
  path: string;
  type: 'folder' | 'file' | 'variant';
  variantKey?: string;  // e.g., "prod" for variant nodes
  parentPath?: string;  // parent file path for variants
  expanded?: boolean;
  children?: TreeNode[];
  loaded?: boolean; // For lazy loading folders
}

// View mode for editor display
export type ViewMode = 'text' | 'hex';

// Open tab in editor
export interface Tab {
  id: string;           // Unique ID (file path or file@variant)
  path: string;         // File path
  name: string;         // Display name
  variantKey?: string;  // Variant key if this tab is a variant
  content: string;      // Current editor content
  originalContent: string; // For dirty detection
  format: FileFormat;
  version: number;
  versions: FileVersion[];
  latestVersion: number; // Latest known version (for optimistic concurrency)
  meta: FileMeta;
  isDirty: boolean;
  size: number;         // Content size in bytes
  modifiedAt?: number;  // Timestamp
  rawData?: string;     // Base64 of the raw bytes (always populated from API/import)
  originalRawData?: string; // Original rawData for dirty detection on raw format
  viewMode: ViewMode;   // Current view mode: 'text' or 'hex'
}

// Search result from backend (path-only for safety; no file contents leak)
export interface SearchResult {
  path: string;
  type: 'name' | 'content';
}

// SearchMode picks what the backend walks:
//  - 'all'  : path matches AND file content matches (default; reads every file)
//  - 'name' : path matches only, no file contents are ever read (faster, safer)
export type SearchMode = 'all' | 'name';

// Vault AppRole authentication
export interface VaultAppRole {
  role_id: string;
  secret_id: string;
  app_role_base_path?: string; // defaults to "approle"
}

// Vault external resource configuration
export interface VaultConfig {
  address: string;
  mount: string;         // KV secrets engine mount (e.g., "secret")
  token?: string;
  app_role?: VaultAppRole;
}

// Kubernetes external resource configuration.
// Auth selection (priority order): kubeconfig_content > kubeconfig (path) > in-cluster.
export interface KubernetesConfig {
  kubeconfig?: string;          // path to kubeconfig file on the pika server
  kubeconfig_content?: string;  // full kubeconfig YAML, pasted directly
}

// Consul external resource configuration
export interface ConsulConfig {
  address: string;
  token?: string;
}

// etcd external resource configuration
export interface EtcdConfig {
  address: string;
  username?: string;
  password?: string;
}

// AWS external resource configuration (Secrets Manager or SSM)
export interface AWSConfig {
  region: string;
  access_key: string;
  secret_key: string;
  service: string;  // "secretsmanager" or "ssm"
}

// GCP Secret Manager external resource configuration
export interface GCPConfig {
  service_account_json: string;
}

// GCP Parameter Manager external resource configuration. Location
// scopes the API to a regional endpoint (e.g. "global", "us-central1")
// and defaults to "global" on the server when omitted.
export interface GCPParameterConfig {
  service_account_json: string;
  location?: string;
}

// Azure Key Vault external resource configuration
export interface AzureConfig {
  vault_url: string;
  tenant_id: string;
  client_id: string;
  client_secret: string;
}

// External resource for inheritance
export interface ExternalResource {
  http?: {
    base_url?: string;
    // Custom headers applied to every request. Backed by ok.Config.Header
    // (map[string][]string) so a single key can have multiple values.
    header?: Record<string, string[]>;
  };
  vault?: VaultConfig;
  kubernetes?: KubernetesConfig;
  consul?: ConsulConfig;
  etcd?: EtcdConfig;
  aws?: AWSConfig;
  gcp?: GCPConfig;
  gcp_parameter?: GCPParameterConfig;
  azure?: AzureConfig;
}

// Mirrors external.Capabilities — what the browser UI can do with a
// given backend. Returned by /api/v1/external/resources for every
// configured resource. The SPA hides/disables buttons based on these
// flags so AWS doesn't show a "Save" button it would never accept.
export interface ExternalCapabilities {
  can_read: boolean;
  can_list: boolean;
  can_write: boolean;
  can_delete: boolean;
  can_versions: boolean;
}

// Summary record for the External browser left pane. Has no secret
// fields; the SPA can render it without settings.manage having to
// surface the full ExternalResource (which carries credentials).
export interface ExternalResourceSummary {
  name: string;
  kind: string;
  capabilities: ExternalCapabilities;
}

// A single entry returned by Provider.Read. Mirrors external.Entry.
// data is the structured key/value payload the SPA renders as a
// table; raw carries the verbatim bytes for "view as text/JSON"; and
// content_type is informational.
export interface ExternalEntry {
  data?: Record<string, unknown>;
  raw?: string; // base64-encoded by Go's json marshaller for []byte
  content_type?: string;
  version?: string;
}

// One Vault KV v2 version. id is the integer version as a string —
// callers pass it back to /version unmodified.
export interface ExternalVersion {
  id: string;
  created_at?: string;
  deleted?: boolean;
  destroyed?: boolean;
}

// HTTP webhook target configuration
export interface HTTPTarget {
  url: string;
  method?: string;         // default: "POST"
  headers?: Record<string, string>;
  timeout?: string;        // e.g., "10s", default: "30s"
}

// Kafka SASL/PLAIN authentication
export interface KafkaSASLPlain {
  enabled?: boolean;
  user?: string;
  pass?: string;
}

// Kafka SASL/SCRAM authentication
export interface KafkaSASLSCRAM {
  enabled?: boolean;
  algorithm?: string;   // "SCRAM-SHA-256" or "SCRAM-SHA-512"
  user?: string;
  pass?: string;
  is_token?: boolean;
}

// Kafka SASL mechanism (plain or scram)
export interface KafkaSASLEntry {
  plain?: KafkaSASLPlain;
  scram?: KafkaSASLSCRAM;
}

// Kafka TLS configuration
// Each field supports: file path, inline PEM text, or reference
// (raw://mount/path, config://key). Structured refs may append
// #/json/pointer selectors.
export interface KafkaTLS {
  enabled?: boolean;
  cert_file?: string;    // path to client cert file
  cert_pem?: string;     // inline PEM or raw://... or config://...
  key_file?: string;     // path to client key file
  key_pem?: string;      // inline PEM or raw://... or config://...
  ca_file?: string;      // path to CA cert file
  ca_pem?: string;       // inline PEM or raw://... or config://...
}

// Kafka security (TLS + SASL)
export interface KafkaSecurity {
  tls?: KafkaTLS;
  sasl?: KafkaSASLEntry[];
}

// Kafka producer target configuration
export interface KafkaTarget {
  brokers: string[];
  topic: string;
  key_template?: string;   // Go template for Kafka message key
  auto_topic_creation?: boolean; // enable broker-side auto topic creation
  security?: KafkaSecurity;
}

// A single push destination for hook events
// Redis TLS configuration
export interface RedisTLS {
  enabled?: boolean;
  cert_file?: string;
  key_file?: string;
  ca_file?: string;
}

// Redis Pub/Sub target for hooks (standalone or cluster)
export interface RedisTarget {
  address?: string;       // single address for standalone mode
  addresses?: string[];   // multiple addresses for cluster mode
  password?: string;
  db?: number;            // only used in standalone mode
  channel: string;
  tls?: RedisTLS;
}

// NATS target for hooks
export interface NATSTarget {
  url: string;
  subject: string;
  token?: string;
  username?: string;
  password?: string;
}

// Local slog logging target for hooks
export interface LogTarget {
  level?: 'debug' | 'info' | 'warn' | 'error';
  message?: string;                 // Go text/template rendered against the Event
  fields?: Record<string, string>;  // key -> Go text/template value
}

export interface HookTarget {
  type: string;            // "http", "kafka", "redis", "nats", or "log"
  http?: HTTPTarget;
  kafka?: KafkaTarget;
  redis?: RedisTarget;
  nats?: NATSTarget;
  log?: LogTarget;
  body_template?: string;  // Go text/template for custom payload
}

// Filter to restrict which events a hook receives
export interface HookFilter {
  mounts?: string[];       // restrict to specific mount prefixes
  path_pattern?: string;   // glob pattern for matching file paths
}

// Hook definition — an event hook with filters and targets
export interface Hook {
  name: string;
  enabled: boolean;
  events: string[];        // e.g., ["file.created", "file.deleted", "*"]
  filter?: HookFilter;
  targets: HookTarget[];
}

// Built-in server-side structured event logging. Missing settings mean enabled.
export interface EventLogSettings {
  disabled?: boolean;
}

// Forward-auth settings — delegates authentication to an external service.
// The middleware is hot-swapped via an ada.Slot at runtime.
export interface ForwardAuthSettings {
  enabled: boolean;
  address: string;
  auth_response_headers?: string[];
  auth_response_headers_regex?: string;
  auth_request_headers?: string[];
  trust_forward_header?: boolean;
  insecure_skip_verify?: boolean;
  timeout?: string;          // Go duration string, e.g. "10s"
  redirect_url?: string;
  redirect_code?: number;
  redirect_status_codes?: number[];
  request_method?: string;
}

// External permissions settings — enables forward-auth permission enforcement.
// The groups header (default X-Groups) is read from each request and mapped
// to pika capability keys via the Mapping. Superadmins is an allowlist of
// usernames that bypass all permission checks.
export interface ExternalPermissionsSettings {
  enabled: boolean;
  groups_header?: string;         // default: "X-Groups"
  groups_separator?: string;      // default: ","
  mapping?: Record<string, string[]>;
  superadmins?: string[];
}

// Settings from API
export interface Settings {
  external?: Record<string, ExternalResource>;
  event_log?: EventLogSettings;
  hooks?: Hook[];
  external_permissions?: ExternalPermissionsSettings;
  forward_auth?: ForwardAuthSettings;
  user_sync?: UserSyncSettings;
  vault?: VaultSettings;
  server_tls?: ServerTLSSettings;
  public_endpoints?: PublicEndpoint[];
}

export interface ServerTLSSettings {
  https_disabled?: boolean;
  plain_http_enabled?: boolean;
}

// PublicEndpoint mirrors service.PublicEndpoint on the backend.
// One entry = one TCP listener on the operator's chosen host:port
// that exposes pika config data directly, through Consul KV compatibility,
// from an External resource, or through a user-authored Go-template modifier.
export interface PublicEndpoint {
  id: string;
  name: string;
  enabled: boolean;
  listen_host: string;
  listen_port: number;
  base_path: string;
  mode: 'static' | 'consul' | 'external' | 'custom';
  static?: StaticCompat;
  consul?: ConsulCompat;
  external?: ExternalCompat;
  custom?: CustomCompat;
  auth: EndpointAuth;
  tls?: EndpointTLS;
  request_check?: RequestCheck;
  created_at?: string;
  updated_at?: string;
}

// RequestCheck is the optional per-endpoint request inspector /
// modifier stage that runs after auth and before the shim. It is
// an ordered list of declarative rules — no templates, just point
// and click. See _docs/reference/compat.md for the full semantics.
export interface RequestCheck {
  rules?: RequestRule[];
}

// RequestRule is one row in the operator's rule list. Each rule
// has a When matcher (AND-combined predicates) and one or more
// actions. `then` is the legacy single-action form; `actions` is
// the ordered multi-action form and takes precedence when present.
// Rules are evaluated top-to-bottom; allow/block short-circuit,
// set_*/del_* modify the request and let evaluation continue.
export interface RequestRule {
  name?: string;
  enabled: boolean;
  when: RequestMatch;
  then: RequestAction;
  actions?: RequestAction[];
}

// RequestMatch — AND-combined predicates. All non-empty fields
// must match. An empty match block matches every request.
export interface RequestMatch {
  method?: string;
  path_equals?: string;
  path_prefix?: string;
  header_equals?: { name: string; value: string };
  header_present?: string;
  header_absent?: string;
  query_equals?: { name: string; value: string };
  query_present?: string;
  query_absent?: string;
}

// RequestAction — what to do when a rule matches.
export type RequestActionType =
  | "allow"
  | "block"
  | "set_header"
  | "del_header"
  | "set_query"
  | "del_query"
  | "set_path"
  | "replace_path";

export interface RequestAction {
  type: RequestActionType;
  status?: number;        // block: defaults to 403
  body?: string;          // block
  content_type?: string;  // block: defaults to application/json
  name?: string;          // set_/del_ header/query
  pattern?: string;       // replace_path regex
  value?: string;         // set_header/set_query/set_path/replace_path replacement
  capture_transforms?: CaptureTransform[]; // replace_path capture string replacements
}

export interface CaptureTransform {
  capture: string;         // capture number ("1") or name ("tail")
  find: string;            // literal text to replace inside that capture
  value: string;           // replacement text
}

export interface RequestRuleTestSnapshot {
  method: string;
  path: string;
  raw_query?: string;
  headers?: Record<string, string>;
}

export interface RequestRuleBlockResult {
  status: number;
  body: string;
  content_type: string;
}

export interface RequestRuleActionTrace {
  action_index: number;
  type: RequestActionType;
  before_path?: string;
  after_path?: string;
  before_query?: string;
  after_query?: string;
  header_name?: string;
  header_before?: string;
  header_after?: string;
  query_name?: string;
  query_before?: string;
  query_after?: string;
  terminal?: boolean;
  block?: RequestRuleBlockResult;
}

export interface RequestRuleTrace {
  rule_index: number;
  rule_name?: string;
  actions: RequestRuleActionTrace[];
}

export interface RequestRuleTestResult {
  initial: RequestRuleTestSnapshot;
  final: RequestRuleTestSnapshot;
  terminal: 'allow' | 'block' | 'default_allow';
  matched_rules: RequestRuleTrace[];
  block?: RequestRuleBlockResult;
}

// StaticCompat — the plain /data-style shim has no configurable knobs today
// (base path lives on the parent); this empty marker tells the UI
// "static mode is selected".
export type StaticCompat = Record<string, never>;

// ConsulCompat — the Consul KV shim has no configurable knobs today
// (base path lives on the parent); this empty marker tells the UI
// "consul mode is selected".
export type ConsulCompat = Record<string, never>;

// ExternalCompat points an endpoint at one configured External resource.
// The endpoint path tail becomes the provider-specific external path.
export interface ExternalCompat {
  resource: string;
}

// CustomCompat is the user-authored Go-template modifier
// configuration. body_template is a text/template source with a
// curated FuncMap (see internal/server/publicendpoint/custom.go).
export interface CustomCompat {
  body_template: string;
  content_type?: string;
  status_on_missing?: number;
  allow_format_override?: boolean;
}

// EndpointAuth picks how the public listener authenticates incoming
// requests. The three modes are mutually exclusive.
export interface EndpointAuth {
  mode: 'none' | 'bearer_token' | 'static_token';
  // static_tokens is a sealed-at-rest list of accepted tokens. The
  // backend never sends already-stored values back through GET, so
  // the UI treats an empty list on read as "tokens preserved".
  static_tokens?: string[];
  header_name?: string;
}

export interface EndpointTLS {
  enabled?: boolean;
  allow_http?: boolean;
}

// EndpointStatus is the diagnostic row returned by
// GET /api/v1/public-endpoints/status. Reflects what the manager
// has currently bound (or failed to bind) against the persisted
// configuration.
export interface PublicEndpointStatus {
  id: string;
  name: string;
  enabled: boolean;
  listen_host: string;
  listen_port: number;
  base_path: string;
  mode: 'static' | 'consul' | 'external' | 'custom';
  tls_enabled?: boolean;
  allow_http?: boolean;
  running: boolean;
  bound_addr?: string;
  last_error?: string;
  started_at?: string;
}

// PublicEndpointTestResult is the body of POST /api/v1/public-endpoints/{id}/test.
export interface PublicEndpointTestResult {
  status: number;
  headers: Record<string, string>;
  body: string;
}

export interface TLSCertificateStatus {
  loaded: boolean;
  cert_file: string;
  key_file: string;
  subject?: string;
  issuer?: string;
  dns_names?: string[];
  ip_addresses?: string[];
  not_before?: string;
  not_after?: string;
  days_remaining: number;
  fingerprint_sha256?: string;
  self_signed: boolean;
}

export interface TLSServerStatus {
  process_enabled: boolean;
  https_enabled: boolean;
  plain_http_enabled: boolean;
  certificate: TLSCertificateStatus;
}

// Runtime cluster connection status from /api/v1/cluster/status.
// This is not persisted in Settings; it reflects the current alan/bw view.
export interface ClusterStatus {
  enabled: boolean;
  role: 'standalone' | 'leader' | 'leader_unhealthy' | 'follower';
  is_leader: boolean;
  leader_healthy: boolean;
  leader_addr?: string;
  local_addr?: string;
  peer_count: number;
  online_nodes: number;
  expected_replicas: number;
  quorum_nodes_required: number;
  has_quorum: boolean;
  version: number;
  config: ClusterConfigStatus;
  nodes: ClusterNode[];
}

export interface ClusterConfigStatus {
  dns_addr?: string;
  bind_addr?: string;
  port?: number;
  replicas?: number;
  security_enabled: boolean;
  lock_key: string;
  prefix: string;
  refresh_interval?: string;
  heartbeat_interval?: string;
  heartbeat_timeout?: string;
  sync_interval?: string;
  forward_timeout?: string;
}

export interface ClusterNode {
  id: string;
  label: string;
  address?: string;
  role: 'standalone' | 'leader' | 'follower' | 'peer';
  self: boolean;
  leader: boolean;
  connected: boolean;
}

// VaultSettings is the admin-level feature flag for the personal
// vault. Disabled=true hides /vault from the SPA navigation and
// turns every /api/v1/me/vault/* endpoint into a 404. Existing
// vault data is preserved — flipping the flag back to false makes
// it accessible again without any migration.
export interface VaultSettings {
  disabled?: boolean;
}

// User-sync settings: top-level array of sources (LDAP, future SCIM, etc.).
// Each source owns its provisioned users via user_identities.provider = source.id
// and its synced permissions via user_permissions.source = source.id.
export interface UserSyncSettings {
  sources?: SyncSource[];
}

export interface SyncSource {
  id: string;            // stable, becomes user_identities.provider
  name: string;          // human label
  type: 'ldap';          // future: 'scim' etc.
  enabled: boolean;
  ldap?: LDAPSyncSpec;
  schedule: SyncSchedule;
  on_missing?: 'disable' | 'ignore'; // default 'disable'
}

export interface SyncSchedule {
  mode: 'manual' | 'interval';
  interval_minutes?: number;
}

export interface LDAPSyncSpec {
  address: string;
  tls?: boolean;
  insecure_skip?: boolean;
  bind_dn: string;
  bind_password?: string;
  user_base_dn: string;
  user_filter?: string;
  page_size?: number;
  group_searches?: LDAPGroupSearchSpec[];
  attributes: LDAPAttributeMap;
  // LDAP group value (e.g. full DN as it appears in memberOf) → list of pika permission IDs.
  group_permissions?: Record<string, string[]>;
}

export interface LDAPGroupSearchSpec {
  base_dn: string;
  filter?: string;
  attributes?: string[];
  name_attribute?: string;
  member_attribute?: string;
  member_uid_attribute?: string;
}

export interface LDAPAttributeMap {
  username: string;
  subject?: string;
  email?: string;
  display_name?: string;
  given_name?: string;
  surname?: string;
  groups?: string;
}

// /api/v1/user-sync/status response entry
export interface SyncSourceStatus {
  id: string;
  name: string;
  enabled: boolean;
  schedule_human?: string;
  last?: SyncReport;
}

export interface SyncReport {
  source_id: string;
  started_at: string;
  finished_at: string;
  found: number;
  created: number;
  updated: number;
  disabled: number;
  perms_applied: number;
  errors?: string[];
}

// Capability descriptor returned by /api/v1/info
export interface Capability {
  key: string;
  name: string;
  description: string;
}

// API response types
export interface ApiError {
  message: string;
}

// Token scope
export interface TokenScope {
  path: string;        // Glob pattern: "app/*", "production/**"
  operations: string[]; // ["read", "write", "delete"]
}

// Token info (public, no hash)
export interface TokenInfo {
  id: string;
  name: string;
  scopes: TokenScope[];
  created_at: string;
  created_by: string;
  expires_at?: string;
  active: boolean;
}

// Create token request
export interface CreateTokenRequest {
  name: string;
  scopes: TokenScope[];
  expires_at?: string;
}

// Create token response (includes raw key shown once)
export interface CreateTokenResponse extends TokenInfo {
  raw_key: string;
}

// Patch token request
export interface PatchTokenRequest {
  name?: string;
  scopes?: TokenScope[];
  active?: boolean;
  expires_at?: string;
}
