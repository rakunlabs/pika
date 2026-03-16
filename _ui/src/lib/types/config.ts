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
  variantKey?: string;  // e.g., "env=prod" for variant nodes
  parentPath?: string;  // parent file path for variants
  expanded?: boolean;
  children?: TreeNode[];
  loaded?: boolean; // For lazy loading folders
}

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

// External resource for inheritance
export interface ExternalResource {
  http?: {
    base_url?: string;
  };
  vault?: VaultConfig;
}

// Settings from API
export interface Settings {
  external?: Record<string, ExternalResource>;
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
