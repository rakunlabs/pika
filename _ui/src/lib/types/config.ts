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
  source?: string;    // Internal file path (for internal inheritance)
  resource?: string;  // External resource name from settings (for external inheritance)
  mount?: string;     // Raw mount prefix (for raw mount inheritance)
  path?: string;      // Resource-specific path (e.g., Vault secret path, HTTP endpoint path, or file path within mount)
  paths?: string[];   // Fields to pull from the source (dot-notation, wildcards)
  inject?: string;    // Where to place in the config (dot-notation, empty = root)
  // Decoder hint for non-JSON external/mount payloads. When the provider
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

// S3 configuration for raw mounts
export interface S3ConfigEntry {
  bucket: string;
  region?: string;
  endpoint?: string;
  access_key?: string;
  secret_key?: string;
  path_style?: boolean;
  prefix?: string;
  secure?: boolean;
}

// FTP configuration for raw mounts
export interface FTPConfigEntry {
  host: string;
  username?: string;
  password?: string;
  tls?: boolean;
  base_path?: string;
}

// SFTP configuration for raw mounts
export interface SFTPConfigEntry {
  host: string;
  username?: string;
  password?: string;
  private_key?: string;
  base_path?: string;
}

// WebDAV configuration for raw mounts
export interface WebDAVConfigEntry {
  url: string;
  username?: string;
  password?: string;
  base_path?: string;
}

// Vercel Blob configuration for raw mounts
export interface VercelBlobConfigEntry {
  token: string;
  store_id?: string;
  prefix?: string;
}

// Raw mount entry stored in settings
export interface RawMountEntry {
  prefix: string;
  type?: string;              // "local" (default), "s3", "ftp", "sftp", "webdav", "vercel-blob"
  path?: string;              // for type=local
  s3?: S3ConfigEntry;         // for type=s3
  ftp?: FTPConfigEntry;       // for type=ftp
  sftp?: SFTPConfigEntry;     // for type=sftp
  webdav?: WebDAVConfigEntry; // for type=webdav
  vercelBlob?: VercelBlobConfigEntry; // for type=vercel-blob
}

// FTP user entry stored in settings
export interface FTPUserEntry {
  username: string;
  password?: string;
  shares?: string[];          // allowed share names; empty = all
  authorized_keys?: string;   // SSH public keys (OpenSSH authorized_keys format, one per line)
  read_only: boolean;
}

// FTP share entry stored in settings
export interface FTPShareEntry {
  name: string;
  paths: string[];     // e.g., ["configs", "assets/images", "backup/2024"]
  read_only: boolean;
  root?: boolean;      // mount at "/" instead of "/name/"
}

// FTP server settings stored in DB
export interface FTPServeSettings {
  enabled: boolean;
  port?: number;
  host?: string;
  public_ip?: string;
  passive_ports?: string;
  tls_cert_file?: string;   // path to PEM certificate file
  tls_key_file?: string;    // path to PEM private key file
  tls_cert_pem?: string;    // PEM certificate content (paste directly)
  tls_key_pem?: string;     // PEM private key content (paste directly)
  tls_required?: number;    // 0=disabled, 1=explicit FTPS (AUTH TLS), 2=implicit FTPS
}

// SFTP server settings stored in DB
export interface SFTPServeSettings {
  enabled: boolean;
  port?: number;
  host?: string;
  host_key_path?: string;
  host_key_pem?: string;    // PEM host key content (paste directly)
}

// TFTP server settings stored in DB
export interface TFTPServeSettings {
  enabled: boolean;
  port?: number;
  host?: string;
}

// WebDAV server settings stored in DB
export interface WebDAVServeSettings {
  enabled: boolean;
  port?: number;
  host?: string;
  prefix?: string;
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
// Each field supports: file path, inline PEM text, or reference (raw://mount/path, config://key)
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

// Proxy server graph entry — one user-built listener with its
// kaykay node/edge graph and a denormalized pipeline summary.
export interface ProxyServer {
  id: string;
  name: string;
  enabled: boolean;
  host?: string;
  port: string;
  nodes: ProxyNode[];
  edges: ProxyEdge[];
  // pipeline is read-only from the UI; the backend regenerates it
  // on every save. Carried so the live status panel can compare
  // the row's hash with the runner's hash.
  pipeline?: { hash?: string; listen_host?: string; listen_port?: string };
}

export interface ProxyNode {
  id: string;
  // Node types match the backend graph.go constants. 'router' was
  // retired in favour of 'switch' — the latter supports host/IP/
  // path/method/header/query rules with a dedicated default branch.
  type: 'listener' | 'middleware' | 'switch' | 'handler';
  subtype?: string;
  position: { x: number; y: number };
  // Opaque per-node config matching the backend schema for the
  // (type, subtype) pair. Catalog endpoint ships the schemas.
  // For switch nodes this carries the SwitchConfig (see backend
  // switch.go) — { rules: [{ id, label?, host?, cidrs?, path?,
  //                          methods?, headers?, query? }, ...] }.
  config?: Record<string, unknown>;
}

export interface ProxyEdge {
  id: string;
  source: string;
  source_handle?: string;
  target: string;
  target_handle?: string;
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
  raw_mounts?: RawMountEntry[];
  ftp_shares?: FTPShareEntry[];
  ftp_users?: FTPUserEntry[];
  ftp_serve?: FTPServeSettings;
  sftp_serve?: SFTPServeSettings;
  tftp_serve?: TFTPServeSettings;
  webdav_serve?: WebDAVServeSettings;
  hooks?: Hook[];
  proxy_servers?: ProxyServer[];
  external_permissions?: ExternalPermissionsSettings;
  forward_auth?: ForwardAuthSettings;
  user_sync?: UserSyncSettings;
  vault?: VaultSettings;
  proxy?: ProxySettings;
  registry?: RegistrySettings;
}

// ProxySettings is the deployment-wide feature flag for the
// user-built Proxy Servers. Default disabled=false → enabled.
export interface ProxySettings {
  disabled: boolean;
}

// RegistrySettings is the deployment-wide artifact-registry config:
// feature flag + namespace/repository tree. Operators edit this from
// the Registries page (tree) and Settings → Features (flag). When
// posted with action=set the server replaces the entire registry
// block, so callers must include the full namespaces array even
// when only flipping the disabled bit (Registries.svelte does this
// by re-loading first).
export interface RegistrySettings {
  disabled?: boolean;
  namespaces?: RegistryNamespace[];
}

// RegistryNamespace = tenant. Name is the URL path segment; must
// match [a-z0-9_-]+ and be unique across the deployment.
export interface RegistryNamespace {
  name: string;
  description?: string;
  repositories?: RegistryRepository[];
}

// RegistryRepository — one repo inside a namespace. The shape is
// the union of all three kinds (local|remote|virtual); the server
// validator rejects rows that mix fields across kinds.
export interface RegistryRepository {
  name: string;
  description?: string;
  type: 'go' | 'npm' | 'docker';
  kind: 'local' | 'remote' | 'virtual';
  // Local fields
  mount?: string;
  base_path?: string;
  allow_push?: boolean;
  // Remote fields
  url?: string;
  auth?: RegistryUpstreamAuth;
  mutable_ttl?: string;
  // Docker-only: tags treated as mutable (TTL-bounded). Tags not
  // in this list are cached forever after the first resolve. Empty
  // ⇒ default list. ["*"] ⇒ every tag is floating.
  floating_tags?: string[];
  insecure_skip_verify?: boolean;
  // Virtual fields
  members?: string[];
  default_local?: string;
  // Common overrides
  cors_origins?: string[];
  max_upload_size?: number;
}

// RegistryUpstreamAuth — credentials for a Remote repository's
// upstream. Username/Password supports HTTP basic; Token supports
// bearer auth (npm "_authToken", Docker registry token).
// All secret-valued fields accept "secret://path" references that
// the server resolves at runtime.
export interface RegistryUpstreamAuth {
  type?: 'basic' | 'bearer' | 'header';
  username?: string;
  password?: string;
  token?: string;
  header_name?: string;
  header_value?: string;
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
  attributes: LDAPAttributeMap;
  // LDAP group value (e.g. full DN as it appears in memberOf) → list of pika permission IDs.
  group_permissions?: Record<string, string[]>;
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

// Raw mount info from /api/v1/info
export interface RawMount {
  prefix: string;
  type: string;    // "local", "s3", "ftp"
  writable: boolean;
}

// Directory entry from raw file listing API
export interface RawDirEntry {
  name: string;
  is_dir: boolean;
  size: number;
}

// Tree node for raw file tree UI
export interface RawTreeNode {
  name: string;
  path: string;       // e.g., "configs/subdir/file.txt"
  mount: string;      // mount prefix e.g., "configs"
  type: 'mount' | 'folder' | 'file';
  expanded?: boolean;
  children?: RawTreeNode[];
  loaded?: boolean;
  size?: number;
  writable?: boolean; // mount supports write operations
  mountType?: string; // "local", "s3", "ftp"
}

// View state for raw file viewer
export type RawViewerMode = 'text' | 'image' | 'binary-placeholder' | 'binary-loading' | 'hex' | 'pdf' | 'video' | 'audio';

// Open tab in raw file browser
export interface RawTab {
  id: string;          // Unique ID: "mount/path"
  mount: string;       // Mount prefix
  path: string;        // Relative path within mount
  name: string;        // Display name (file name)
  size: number;        // File size in bytes
  contentType: string; // MIME type from response
  viewerMode: RawViewerMode;
  textContent?: string;    // Text content (for text/code files)
  rawUrl?: string;         // URL for image/pdf viewing
  hexData?: string;        // Base64 data for hex viewer
  loaded: boolean;         // Whether content has been fetched
  forceHex?: boolean;      // User clicked "Open Anyway" on binary placeholder
  truncated?: boolean;     // Text content was truncated due to size limit
  tooLargeForHex?: boolean; // File is too large even for hex viewer
}
