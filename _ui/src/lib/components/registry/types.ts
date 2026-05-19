// Registry TypeScript types — mirror of the Go wire contracts
// served by /api/v1/registries/*.
//
// Two families of types live here:
//
//   1. List shapes (Namespace, Repository, ModuleEntry, …) — the
//      content of GET /api/v1/registries and the per-type list
//      endpoints. Used by Registries.svelte and any extracted
//      list components.
//   2. Detail shapes (PackageDetail and friends) — the content of
//      GET /api/v1/registries/{type}/{ns}/{repo}/packages/{name}.
//      Used by PackageDetailPanel.svelte.
//
// One file rather than splitting per protocol keeps the import
// surface small and prevents drift: every new field added to the
// backend struct should land here in the same commit.

// ─── Registry type / kind discriminators ───

export type RegistryType = 'go' | 'npm' | 'docker' | 'helm';
export type RegistryKind = 'local' | 'remote' | 'virtual';

// ─── List shapes: settings tree ───

/** UpstreamAuth mirrors service.RegistryUpstreamAuth on the wire. */
export type UpstreamAuth = {
  type?: 'basic' | 'bearer' | 'header';
  username?: string;
  password?: string;
  token?: string;
  /** 'header' = HTTP header name, 'value' = its value. */
  header?: string;
  value?: string;
};

/** Repository mirrors service.RegistryRepository on the wire. */
export type Repository = {
  name: string;
  description?: string;
  type: RegistryType;
  kind: RegistryKind;
  // Local
  mount?: string;
  base_path?: string;
  allow_push?: boolean;
  // Remote
  url?: string;
  auth?: UpstreamAuth;
  mutable_ttl?: string;
  floating_tags?: string[];
  insecure_skip_verify?: boolean;
  // Virtual
  members?: string[];
  default_local?: string;
  // Common per-repo overrides — exposed in UI from B6 onwards.
  cors_origins?: string[];
  max_upload_size?: number; // bytes (0 = type default)
};

/** Namespace mirrors service.RegistryNamespace on the wire. */
export type Namespace = {
  name: string;
  description?: string;
  repositories?: Repository[];
};

// ─── List shapes: per-protocol entry browsers ───

/** ModuleEntry mirrors api.goModuleEntry. */
export type ModuleEntry = {
  module: string;
  versions: string[];
};

/** PackageEntry mirrors api.npmPackageEntry. */
export type PackageEntry = {
  name: string;
  versions: string[];
  dist_tags: Record<string, string>;
};

/** HelmChartListEntry mirrors api.helmChartEntry. */
export type HelmChartListEntry = {
  name: string;
  versions: string[];
};

/** DockerTag mirrors api.dockerTagSummary. */
export type DockerTag = {
  tag: string;
  digest?: string;
  artifact_type?: string;
  media_type?: string;
  /** Manifest JSON byte count. */
  manifest_size?: number;
  /** Sum of layer sizes — zero for non-image OCI artifacts. */
  image_size?: number;
};

/** DockerEntry mirrors api.dockerRepoEntry. */
export type DockerEntry = {
  name: string;
  tags: DockerTag[];
};

// ─── Stats + probe response shapes ───

/** RegistryStats mirrors api.registry.Stats. */
export type RegistryStats = {
  blob_count?: number;
  total_bytes?: number;
  module_count?: number;
  version_count?: number;
  package_count?: number;
  repository_count?: number;
  tag_count?: number;
  manifest_count?: number;
};

/** ProbeResult mirrors api.UpstreamHealth. */
export type ProbeResult = {
  ok: boolean;
  status_code?: number;
  latency_ms?: number;
  url?: string;
  error?: string;
  body_preview?: string;
};

// ─── Detail shapes ───

export type NPMVersionDetail = {
  version: string;
  published_at?: string;
  size?: number;
  integrity?: string;
  shasum?: string;
  tarball_url?: string;
  dependencies?: Record<string, string>;
  dev_dependencies?: Record<string, string>;
  peer_dependencies?: Record<string, string>;
  engines?: Record<string, string>;
  deprecated?: string;
};

export type NPMPackageDetail = {
  latest_version?: string;
  description?: string;
  homepage?: string;
  license?: string;
  repository?: Record<string, any>;
  bugs?: Record<string, any>;
  keywords?: string[];
  author?: Record<string, any>;
  maintainers?: any[];
  dist_tags?: Record<string, string>;
  has_readme?: boolean;
  versions?: NPMVersionDetail[];
};

export type GoVersionDetail = {
  version: string;
  published_at?: string;
  retracted?: boolean;
  retraction_rationale?: string;
  gomod_size?: number;
  zip_size?: number;
};

export type GoModuleDetail = {
  latest_version?: string;
  versions?: GoVersionDetail[];
};

export type DockerLayer = {
  digest: string;
  size?: number;
  media_type?: string;
};

export type DockerPlatform = {
  os?: string;
  architecture?: string;
  variant?: string;
  digest?: string;
  size?: number;
};

export type DockerTagDetail = {
  tag: string;
  digest?: string;
  media_type?: string;
  artifact_type?: string;
  manifest_size?: number;
  image_size?: number;
  config_digest?: string;
  layers?: DockerLayer[];
  platforms?: DockerPlatform[];
};

export type DockerRepoDetail = {
  tags?: DockerTagDetail[];
};

export type HelmVersionDetail = {
  version: string;
  app_version?: string;
  description?: string;
  created?: string;
  digest?: string;
  size?: number;
  urls?: string[];
};

export type HelmChartDetail = {
  latest_version?: string;
  description?: string;
  icon?: string;
  app_version?: string;
  keywords?: string[];
  maintainers?: any[];
  has_readme?: boolean;
  versions?: HelmVersionDetail[];
};

export type PackageDetail = {
  type: RegistryType;
  name: string;
  npm?: NPMPackageDetail;
  go?: GoModuleDetail;
  docker?: DockerRepoDetail;
  helm?: HelmChartDetail;
};

// Runtime helpers used to live here; they moved to lib/format.ts
// (formatSize, formatPublishedAt) and lib/components/registry/utils.ts
// (matchFilter, iconFor, kindIcon, copyToClipboard, …).
// Re-export the format helpers from here so existing imports keep
// working through the migration.
export { formatSize, formatPublishedAt } from '@/lib/format';
