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
}

// File metadata
export interface FileMeta {
  description?: string;
  format?: FileFormat;
  inherit?: string; // External resource to inherit from
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
}

// Tree node for file tree UI
export interface TreeNode {
  name: string;
  path: string;
  type: 'folder' | 'file';
  expanded?: boolean;
  children?: TreeNode[];
  loaded?: boolean; // For lazy loading folders
}

// Open tab in editor
export interface Tab {
  id: string;           // Unique ID (file path)
  path: string;         // Full path
  name: string;         // Display name
  content: string;      // Current editor content
  originalContent: string; // For dirty detection
  format: FileFormat;
  version: number;
  versions: FileVersion[];
  meta: FileMeta;
  isDirty: boolean;
  size: number;         // Content size in bytes
  modifiedAt?: number;  // Timestamp
}

// Search result
export interface SearchResult {
  path: string;
  line: number;
  content: string;
  matchStart: number;
  matchEnd: number;
}

// External resource for inheritance
export interface ExternalResource {
  name: string;
  http?: {
    base_url?: string;
  };
  vault?: {
    base_path: string;
    address: string;
  };
}

// Settings from API
export interface Settings {
  external?: Record<string, ExternalResource>;
}

// API response types
export interface ApiError {
  message: string;
}
