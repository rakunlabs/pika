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
  path?: string;      // Resource-specific path (e.g., Vault secret path, HTTP endpoint path)
  paths?: string[];   // Fields to pull from the source (dot-notation, wildcards)
  inject?: string;    // Where to place in the config (dot-notation, empty = root)
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

// Search result from backend
export interface SearchResult {
  path: string;
  type: 'name' | 'content';
  line?: number;
  snippet?: string;
}

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

// Kubernetes external resource configuration
export interface KubernetesConfig {
  kubeconfig?: string;  // path to kubeconfig file (empty = in-cluster)
}

// External resource for inheritance
export interface ExternalResource {
  http?: {
    base_url?: string;
  };
  vault?: VaultConfig;
  kubernetes?: KubernetesConfig;
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

// Raw mount entry stored in settings
export interface RawMountEntry {
  prefix: string;
  type?: string;           // "local" (default), "s3", "ftp", "sftp"
  path?: string;           // for type=local
  s3?: S3ConfigEntry;      // for type=s3
  ftp?: FTPConfigEntry;    // for type=ftp
  sftp?: SFTPConfigEntry;  // for type=sftp
}

// FTP user entry stored in settings
export interface FTPUserEntry {
  username: string;
  password: string;
  shares?: string[];   // allowed share names; empty = all
  read_only: boolean;
}

// FTP share entry stored in settings
export interface FTPShareEntry {
  name: string;
  paths: string[];     // e.g., ["configs", "assets/images", "backup/2024"]
  read_only: boolean;
}

// FTP server settings stored in DB
export interface FTPServeSettings {
  enabled: boolean;
  port?: number;
  host?: string;
  public_ip?: string;
  passive_ports?: string;
}

// SFTP server settings stored in DB
export interface SFTPServeSettings {
  enabled: boolean;
  port?: number;
  host?: string;
  host_key_path?: string;
}

// TFTP server settings stored in DB
export interface TFTPServeSettings {
  enabled: boolean;
  port?: number;
  host?: string;
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
