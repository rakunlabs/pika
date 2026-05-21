<script lang="ts">
  // Registries — top-level page for the artifact registry feature
  // (Go modules, NPM packages, Docker / OCI images).
  //
  // ── Scope of this page ────────────────────────────────────────────
  // Phase 1 surface: namespace + repository listing, type/kind
  // badges and per-repo artifact browser. Local repos additionally
  // show an upload guide (manual `curl PUT` for now; a form-based
  // uploader is queued for a later phase).
  //
  // ── Layout ────────────────────────────────────────────────────────
  //   ┌────────────┬────────────────┬────────────────────────────┐
  //   │ namespaces │ repositories   │ detail panel               │
  //   │ list       │ list (filter)  │ (modules, versions, CLI)   │
  //   └────────────┴────────────────┴────────────────────────────┘
  //
  // ── Endpoints used ────────────────────────────────────────────────
  //   GET  /api/v1/registries                          → namespace tree
  //   GET  /api/v1/registries/repos                    → flat list
  //   GET  /api/v1/registries/go/{ns}/{repo}/modules   → modules + versions

  import { onMount } from 'svelte';
  import { link } from 'svelte-spa-router';
  import { appStore } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { configStore } from '@/lib/store/config.svelte';
  import {
    Loader2,
    Copy, ChevronRight, FolderTree, Trash2, Plus, Pencil, X, RotateCw,
    Eye, EyeOff, Search,
    // These four are used directly in the template for per-protocol
    // section headers and empty states; iconFor() / kindIcon() in
    // registry/utils.ts cover the badge-icon callsites.
    Package, FileBox, Container, Anchor,
  } from 'lucide-svelte';
  import { basePath } from '@/lib/basepath';
  import { formatBytes } from '@/lib/format';
  import * as registryAPI from '@/lib/store/registry.svelte';
  import Modal from '@/lib/components/Modal.svelte';
  import PackageDetailPanel from '@/lib/components/registry/PackageDetailPanel.svelte';
  import type {
    CargoCrateEntry,
    DockerEntry,
    DockerTag,
    HelmChartListEntry,
    MavenArtifactEntry,
    ModuleEntry,
    Namespace,
    PackageEntry,
    ProbeResult,
    PyPIPackageEntry,
    RegistryStats,
    RegistryType,
    Repository,
    UpstreamAuth,
  } from '@/lib/components/registry/types';
  import {
    artifactTypeLabel,
    copyToClipboard as copyToClipboardUtil,
    endpointURL as endpointURLUtil,
    iconFor,
    kindIcon,
    matchFilter,
  } from '@/lib/components/registry/utils';

  let booted = $state(false);
  let namespaces = $state<Namespace[]>([]);
  let selectedNS = $state<string | null>(null);
  let selectedRepo = $state<Repository | null>(null);
  let modules = $state<ModuleEntry[]>([]);
  let packages = $state<PackageEntry[]>([]);
  let images = $state<DockerEntry[]>([]);
  let charts = $state<HelmChartListEntry[]>([]);
  let mavenArtifacts = $state<MavenArtifactEntry[]>([]);
  let pypiPackages = $state<PyPIPackageEntry[]>([]);
  let cargoCrates = $state<CargoCrateEntry[]>([]);
  let entriesLoading = $state(false);
  // For all three browsers — only one is shown at a time depending
  // on selectedRepo.type, so a single expanded-name key is enough.
  let expandedEntry = $state<string | null>(null);

  // PackageDetailPanel state. When `detailPackage` is non-null, the
  // panel renders as a right-side overlay; clicking a package row
  // sets this and clicking the close button on the panel clears it.
  // Kept at the page level (rather than per-browser) because the
  // panel is a singleton — only one package can be "open" at a time.
  let detailPackage = $state<{ name: string; type: RegistryType } | null>(null);
  function openDetail(name: string, type: RegistryType) {
    detailPackage = { name, type };
  }
  function closeDetail() {
    detailPackage = null;
  }

  async function refreshAfterArtifactDelete() {
    if (selectedNS && selectedRepo) {
      await loadEntries(selectedNS, selectedRepo);
      await loadStats(selectedNS, selectedRepo);
    }
  }

  // Per-list text filters. Each browser's list is filtered against
  // its respective state below; an empty filter shows everything.
  // The filter inputs render inside the browser headers (see template
  // below) and update these states in place.
  let nsFilter = $state('');
  let repoFilter = $state('');
  let entryFilter = $state('');

  const canAdmin = $derived(appStore.hasPermission('registry.admin'));
  const canDelete = $derived(appStore.hasPermission('registry.delete'));
  // Server-side feature toggle. When disabled, the data plane and
  // every /api/v1/registries/* endpoint return 404, so we render a
  // dedicated empty-state instead of letting load() spam "Failed to
  // load registries" toasts on each visit. Default true so users
  // on an older server build (no registry_enabled field) keep the
  // existing behaviour.
  const registryEnabled = $derived(appStore.info?.registry_enabled ?? true);

  async function load() {
    // Skip the fetch entirely when the feature is off — the API
    // would 404 and the user is already going to see the explicit
    // "feature disabled" panel below.
    if (!registryEnabled) {
      booted = true;
      return;
    }
    try {
      const body = await registryAPI.listRegistries();
      namespaces = body.namespaces ?? [];
      if (namespaces.length > 0 && !selectedNS) {
        selectedNS = namespaces[0].name;
      }
    } catch (err) {
      addToast(`Failed to load registries: ${err}`, 'alert');
    } finally {
      booted = true;
    }
  }

  async function loadEntries(ns: string, repo: Repository) {
    modules = [];
    packages = [];
    images = [];
    charts = [];
    mavenArtifacts = [];
    pypiPackages = [];
    cargoCrates = [];
    entriesLoading = true;
    try {
      // Dispatch on protocol — each API call has a different
      // response shape and a different state slot. The switch
      // keeps the call narrow and type-safe; the alternative
      // (a generic listEntries) would lose the per-protocol
      // typing the rest of the page depends on.
      switch (repo.type) {
        case 'go':
          modules = await registryAPI.listGoModules(ns, repo.name);
          break;
        case 'npm':
          packages = await registryAPI.listNPMPackages(ns, repo.name);
          break;
        case 'docker':
          images = await registryAPI.listDockerRepos(ns, repo.name);
          break;
        case 'helm':
          charts = await registryAPI.listHelmCharts(ns, repo.name);
          break;
        case 'maven':
          mavenArtifacts = await registryAPI.listMavenArtifacts(ns, repo.name);
          break;
        case 'pypi':
          pypiPackages = await registryAPI.listPyPIPackages(ns, repo.name);
          break;
        case 'cargo':
          cargoCrates = await registryAPI.listCargoCrates(ns, repo.name);
          break;
      }
    } catch (err) {
      addToast(`Failed to load entries: ${err}`, 'alert');
    } finally {
      entriesLoading = false;
    }
  }

  function selectRepo(repo: Repository) {
    selectedRepo = repo;
    expandedEntry = null;
    if (selectedNS) {
      loadEntries(selectedNS, repo);
      loadStats(selectedNS, repo);
    }
  }

  // Thin local wrappers — the page uses these names extensively in
  // the template, so we re-bind the utils versions to the original
  // local identifiers rather than touching dozens of callsites.
  const copyToClipboard = (text: string) => copyToClipboardUtil(text, addToast);

  // Per-repo on-disk statistics. Lazy-loaded the first time a repo
  // is selected, then re-fetched whenever the repo changes. Server
  // walks the storage on every call (no persistent counter), so we
  // keep the call rate low — refresh button instead of polling.
  let stats = $state<RegistryStats | null>(null);
  let statsLoading = $state(false);
  async function loadStats(ns: string, repo: Repository) {
    stats = null;
    if (repo.kind === 'virtual') {
      // Virtual repos delegate to members. The server returns {}
      // for them; surface a clean "n/a" rather than the empty
      // object so the UI doesn't render a card full of zeros.
      return;
    }
    statsLoading = true;
    try {
      stats = await registryAPI.getStats(repo.type, ns, repo.name);
    } catch (err) {
      // Non-fatal — the panel just hides the card.
      stats = null;
    } finally {
      statsLoading = false;
    }
  }

  // humanBytes is the format-with-zero variant used by the stats
  // card. Renamed from a local function to the shared formatBytes
  // helper but kept as an alias to avoid a template-wide rename.
  const humanBytes = formatBytes;
  const defaultGCMinAgeSeconds = 3600;
  const defaultAbandonedUploadMaxAgeSeconds = 86400;
  function effectiveGCMinAge(repo: Repository): number {
    return repo.policy?.retention?.gc_min_age_seconds || defaultGCMinAgeSeconds;
  }
  function effectiveAbandonedUploadMaxAge(repo: Repository): number {
    return repo.policy?.retention?.abandoned_upload_max_age_seconds || defaultAbandonedUploadMaxAgeSeconds;
  }
  function formatSeconds(seconds: number): string {
    if (seconds <= 0) return '0s';
    if (seconds % 86400 === 0) return `${seconds / 86400}d`;
    if (seconds % 3600 === 0) return `${seconds / 3600}h`;
    if (seconds % 60 === 0) return `${seconds / 60}m`;
    return `${seconds}s`;
  }

  // Cache purge for Remote repos. Mirrors the GC pattern: confirm,
  // POST, toast the stats, then reload the per-repo browser so any
  // freshly-dropped entries disappear immediately. Default scope is
  // "mutable" — operator can opt into a full purge through the
  // confirm prompt.
  let purgeRunning = $state(false);
  async function runCachePurge(ns: string, repo: Repository) {
    const wide = window.confirm(
      `Refresh upstream cache for ${ns}/${repo.name}?\n\n` +
      `OK = drop mutable pointers (next read re-fetches version lists / floating tags).\n` +
      `Cancel = abort.\n\n` +
      `Hold Shift while clicking OK to also drop cached artifacts (forces a full re-download).`
    );
    if (!wide) return;
    // window.confirm doesn't expose modifier state, so we use a
    // second prompt for the wide scope. Simple but explicit.
    const all = window.confirm(
      `Also drop cached artifacts (manifests / tarballs / blobs)?\n\n` +
      `OK = full purge (heavier, forces re-download on every pull).\n` +
      `Cancel = mutable-only purge (recommended).`
    );
    purgeRunning = true;
    try {
      const stats = await registryAPI.purgeCache(repo.type, ns, repo.name, all) as {
        purged_bytes?: number;
        purged_files?: number;
      };
      const bytes = stats.purged_bytes ?? 0;
      const human = bytes >= 1024 * 1024
        ? `${(bytes / (1024 * 1024)).toFixed(1)} MB`
        : `${(bytes / 1024).toFixed(0)} KB`;
      addToast(
        `Purged ${stats.purged_files} file${stats.purged_files === 1 ? '' : 's'} (${human}).`,
        'success'
      );
      if (selectedRepo && selectedNS) {
        await loadEntries(selectedNS, selectedRepo);
      }
    } catch (err) {
      addToast(`Cache purge failed: ${err}`, 'alert');
    } finally {
      purgeRunning = false;
    }
  }

  // GC for Docker Local repos. Confirms with the user, runs the
  // server-side sweep, surfaces the resulting stats via toast,
  // then reloads the image list so freed-up entries disappear.
  //
  // Pika never runs GC on a schedule: cleanup is operator-driven
  // here, with a side channel of cheap delete-time cascading in
  // the registry head. The estimate panel below this function
  // surfaces the reclaimable size BEFORE committing to a run.
  let gcRunning = $state(false);
  let gcEstimate = $state<registryAPI.GCStats | null>(null);
  let gcEstimateLoading = $state(false);
  let gcEstimateRepoKey = $state<string | null>(null); // "ns/repo" the estimate belongs to

  // refreshGCEstimate runs a dry-run mark-and-sweep and caches the
  // result for the currently-viewed Docker Local repo. Cheap to
  // re-trigger; we DO NOT auto-refresh on every selection because
  // the dry-run walk is still O(blob count).
  async function refreshGCEstimate(ns: string, repoName: string) {
    gcEstimate = null;
    gcEstimateLoading = true;
    gcEstimateRepoKey = `${ns}/${repoName}`;
    try {
      gcEstimate = await registryAPI.dockerGCEstimate(ns, repoName);
    } catch (err) {
      addToast(`Garbage estimate failed: ${err}`, 'alert');
    } finally {
      gcEstimateLoading = false;
    }
  }

  async function runDockerGC(ns: string, repoName: string) {
    const grace = selectedRepo && selectedRepo.name === repoName
      ? formatSeconds(effectiveGCMinAge(selectedRepo))
      : 'the policy grace window';
    if (!window.confirm(`Run garbage collection on ${ns}/${repoName}?\n\nThis deletes unreferenced blobs and manifests. Items newer than ${grace} are protected by the grace window.`)) {
      return;
    }
    gcRunning = true;
    try {
      const stats = await registryAPI.dockerGC(ns, repoName);
      const totalBytes = stats.swept_bytes + stats.abandoned_uploads_bytes;
      const msg = `Swept ${stats.swept_blobs} blob${stats.swept_blobs === 1 ? '' : 's'}, ` +
                  `${stats.swept_manifests} manifest${stats.swept_manifests === 1 ? '' : 's'}, ` +
                  `${stats.abandoned_uploads_removed} stale upload${stats.abandoned_uploads_removed === 1 ? '' : 's'} ` +
                  `(${formatBytes(totalBytes)} reclaimed).`;
      addToast(msg, 'success');
      // The estimate is now stale — refresh in the background so
      // the panel doesn't keep displaying yesterday's numbers.
      if (gcEstimateRepoKey === `${ns}/${repoName}`) {
        await refreshGCEstimate(ns, repoName);
      }
      if (selectedRepo && selectedNS) {
        await loadEntries(selectedNS, selectedRepo);
      }
    } catch (err) {
      addToast(`GC failed: ${err}`, 'alert');
    } finally {
      gcRunning = false;
    }
  }

  // endpointURL wraps the shared util so the template keeps its
  // 2-arg shape (basePath is bound here at the page level).
  const endpointURL = (ns: string, repo: string) => endpointURLUtil(basePath, ns, repo);

  function defaultBasePath(type: RegistryType): string {
    switch (type) {
      case 'go': return 'go/';
      case 'npm': return 'npm/';
      case 'helm': return 'charts/';
      case 'maven': return 'maven/';
      case 'pypi': return 'pypi/';
      case 'cargo': return 'cargo/';
      default: return 'docker/';
    }
  }

  function defaultCachePath(type: RegistryType): string {
    switch (type) {
      case 'go': return 'go-cache/';
      case 'npm': return 'npm-cache/';
      case 'helm': return 'charts-cache/';
      case 'maven': return 'maven-cache/';
      case 'pypi': return 'pypi-cache/';
      case 'cargo': return 'cargo-cache/';
      default: return 'docker-cache/';
    }
  }

  function defaultUpstreamURL(type: RegistryType): string {
    switch (type) {
      case 'go': return 'https://proxy.golang.org';
      case 'npm': return 'https://registry.npmjs.org';
      case 'helm': return 'https://charts.bitnami.com/bitnami';
      case 'maven': return 'https://repo1.maven.org/maven2';
      case 'pypi': return 'https://pypi.org';
      case 'cargo': return 'https://index.crates.io';
      default: return 'https://registry-1.docker.io';
    }
  }

  // ─── Admin actions: CRUD over namespaces + repositories ───
  //
  // The server takes a whole-tree PUT (action=set on the registry
  // block), so every mutation goes through the same recipe:
  //   1. Mutate the local `namespaces` array (creates a new tree).
  //   2. POST the whole tree via configStore.saveRegistrySettings.
  //   3. Re-load from the API to pick up server-canonicalized form.
  // We keep "disabled" intact across saves by reading it from
  // configStore.settings (the feature flag must NOT silently flip).

  // Modal state. `mode` controls which dialog is open; null = none.
  type Mode =
    | { kind: 'new-namespace' }
    | { kind: 'edit-namespace'; original: string }
    | { kind: 'new-repository'; namespace: string }
    | { kind: 'edit-repository'; namespace: string; original: string };
  let mode = $state<Mode | null>(null);
  let saving = $state(false);

  // ── Mode discriminator helpers ──
  //
  // Svelte 5's reactive `$derived.by` closures and template-side
  // ternaries on a 4-variant union don't always narrow cleanly in
  // svelte-check — the type checker can't follow `mode.kind ===
  // 'new-repository' || mode.kind === 'edit-repository'` to prove
  // that `.namespace` exists. These helpers do the narrowing in
  // one place so call sites can use a single accessor without
  // non-null assertions.
  function modeNamespace(m: Mode | null): string {
    if (!m) return '';
    if (m.kind === 'new-repository' || m.kind === 'edit-repository') return m.namespace;
    return '';
  }
  function modeOriginal(m: Mode | null): string {
    if (!m) return '';
    if (m.kind === 'edit-namespace' || m.kind === 'edit-repository') return m.original;
    return '';
  }
  function isRepoMode(m: Mode | null): m is
    | { kind: 'new-repository'; namespace: string }
    | { kind: 'edit-repository'; namespace: string; original: string } {
    return !!m && (m.kind === 'new-repository' || m.kind === 'edit-repository');
  }
  function isNamespaceMode(m: Mode | null): m is
    | { kind: 'new-namespace' }
    | { kind: 'edit-namespace'; original: string } {
    return !!m && (m.kind === 'new-namespace' || m.kind === 'edit-namespace');
  }

  // Draft buffers — separate from the live tree so cancel is cheap
  // (just clear `mode`).
  let nsDraft = $state<Namespace>({ name: '', description: '', repositories: [] });
  let repoDraft = $state<Repository>({
    name: '',
    description: '',
    type: 'go',
    kind: 'local',
    allow_push: true,
  });
  // Virtual-members editor uses a comma-separated string for the
  // simplest possible input. Round-tripped through .split/.join
  // when opening / saving.
  let virtualMembersText = $state('');
  // Floating-tags editor (Docker Remote only) — same comma-string
  // convention. Empty input means "use server default".
  let floatingTagsText = $state('');
  // CORS origins editor — comma-separated origins string. Same
  // pattern as virtualMembersText. The backend stores them as a
  // string slice; the UI keeps the editor flat for keyboard
  // efficiency (one input vs a chip array with per-chip buttons).
  let corsOriginsText = $state('');
  // Max upload size editor: number + unit dropdown. We persist the
  // bytes value in repoDraft.max_upload_size; the inputs below
  // expose a friendlier MB/GB unit picker. Zero (or empty) means
  // "type default" — preserved by omitting the field at save time.
  let maxUploadValue = $state(0);
  let maxUploadUnit = $state<'MB' | 'GB'>('MB');

  // Docker Local policy editor. Immutable tags are a comma-separated
  // glob list; retention numbers are persisted as seconds. Zero means
  // "server default" so existing repos keep the historical 1h/24h GC
  // behaviour until an operator opts into different defaults.
  let immutableTagsText = $state('');
  let gcMinAgeSeconds = $state(0);
  let abandonedUploadMaxAgeSeconds = $state(0);

  // Reveal toggles for the credential inputs. We render the fields
  // as type="password" by default — the value comes back plaintext
  // from the server (the seal layer transparently injects it) but
  // the browser masks it. Click 👁 to flip the input to type=text.
  // Reset to false each time the modal opens.
  let revealPassword = $state(false);
  let revealToken = $state(false);
  let revealHeaderValue = $state(false);

  // Upstream probe state. Lives at the page level (rather than
  // inside the modal subtree) so a long-running probe survives
  // briefly closing/reopening the modal — and so the result panel
  // can be cleared when the modal opens fresh.
  let probeResult = $state<ProbeResult | null>(null);
  let probeRunning = $state(false);
  async function runProbe() {
    if (!isRepoMode(mode)) return;
    const ns = mode.namespace;
    const repoName = (repoDraft.name ?? '').trim().toLowerCase();
    if (!repoName) {
      addToast('Save the repository once before testing the upstream.', 'alert');
      return;
    }
    probeRunning = true;
    probeResult = null;
    try {
      probeResult = await registryAPI.probeUpstream(repoDraft.type, ns, repoName);
    } catch (err) {
      probeResult = { ok: false, error: String(err) };
    } finally {
      probeRunning = false;
    }
  }

  // rawMounts feeds the mount dropdown for Local repos. Pulled from
  // /api/v1/info (already loaded by the app store at boot).
  const rawMounts = $derived(appStore.info?.raw_mounts ?? []);

  function openNewNamespace() {
    nsDraft = { name: '', description: '', repositories: [] };
    mode = { kind: 'new-namespace' };
  }

  function openEditNamespace(ns: Namespace) {
    nsDraft = { name: ns.name, description: ns.description ?? '', repositories: ns.repositories ?? [] };
    mode = { kind: 'edit-namespace', original: ns.name };
  }

  function openNewRepository(namespaceName: string) {
    repoDraft = {
      name: '',
      description: '',
      type: 'go',
      kind: 'local',
      allow_push: true,
    };
    virtualMembersText = '';
    floatingTagsText = '';
    corsOriginsText = '';
    maxUploadValue = 0;
    maxUploadUnit = 'MB';
    immutableTagsText = '';
    gcMinAgeSeconds = 0;
    abandonedUploadMaxAgeSeconds = 0;
    revealPassword = false;
    revealToken = false;
    revealHeaderValue = false;
    probeResult = null;
    mode = { kind: 'new-repository', namespace: namespaceName };
  }

  function openEditRepository(namespaceName: string, repo: Repository) {
    repoDraft = JSON.parse(JSON.stringify(repo)); // deep copy
    virtualMembersText = (repo.members ?? []).join(', ');
    floatingTagsText = (repo.floating_tags ?? []).join(', ');
    corsOriginsText = (repo.cors_origins ?? []).join(', ');
    immutableTagsText = (repo.policy?.immutable_tags ?? []).join(', ');
    gcMinAgeSeconds = repo.policy?.retention?.gc_min_age_seconds ?? 0;
    abandonedUploadMaxAgeSeconds = repo.policy?.retention?.abandoned_upload_max_age_seconds ?? 0;
    // Reconstruct the friendly value+unit pair from the stored
    // bytes. Round-trip prefers GB when the value is a clean
    // multiple, else MB. Sub-MB values (operator typed bytes
    // directly through a previous version) round up to 1 MB for
    // the display; saving overwrites with the new (value * unit).
    const bytes = repo.max_upload_size ?? 0;
    if (bytes <= 0) {
      maxUploadValue = 0;
      maxUploadUnit = 'MB';
    } else if (bytes >= 1024 * 1024 * 1024 && bytes % (1024 * 1024 * 1024) === 0) {
      maxUploadValue = bytes / (1024 * 1024 * 1024);
      maxUploadUnit = 'GB';
    } else {
      maxUploadValue = Math.max(1, Math.round(bytes / (1024 * 1024)));
      maxUploadUnit = 'MB';
    }
    revealPassword = false;
    revealToken = false;
    revealHeaderValue = false;
    mode = { kind: 'edit-repository', namespace: namespaceName, original: repo.name };
  }

  function cancelModal() {
    mode = null;
  }

  // commitTree posts the namespaces array via configStore. We always
  // include the existing `disabled` bit so saving from this page
  // never accidentally turns the feature off (or back on).
  async function commitTree(newNamespaces: Namespace[]) {
    saving = true;
    try {
      const disabled = configStore.settings?.registry?.disabled === true;
      await configStore.saveRegistrySettings({ disabled, namespaces: newNamespaces });
      namespaces = newNamespaces;
      addToast('Registry configuration saved.', 'success');
      mode = null;
      // Reload to pick up server-side defaults (e.g. default
      // MutableTTL filled in by validator). Best-effort — local
      // tree is the source of truth until reload completes.
      await load();
    } catch (err) {
      // saveRegistrySettings already surfaced a toast with the
      // server's error detail. Keep the dialog open so the user
      // can correct the input and retry.
      // eslint-disable-next-line no-console
      console.error('commitTree failed', err);
    } finally {
      saving = false;
    }
  }

  async function saveNamespace() {
    const m = mode;
    if (!isNamespaceMode(m)) return;
    const name = (nsDraft.name ?? '').trim().toLowerCase();
    if (!name) {
      addToast('Namespace name is required.', 'alert');
      return;
    }
    if (!/^[a-z0-9_-]+$/.test(name)) {
      addToast('Namespace name must match [a-z0-9_-]+.', 'alert');
      return;
    }
    const tree = JSON.parse(JSON.stringify(namespaces)) as Namespace[];
    if (m.kind === 'new-namespace') {
      if (tree.some((n) => n.name === name)) {
        addToast(`Namespace "${name}" already exists.`, 'alert');
        return;
      }
      tree.push({ name, description: nsDraft.description, repositories: [] });
    } else {
      const original = m.original;
      const idx = tree.findIndex((n) => n.name === original);
      if (idx === -1) {
        addToast('Original namespace not found — tree was edited elsewhere.', 'alert');
        return;
      }
      if (name !== original && tree.some((n) => n.name === name)) {
        addToast(`Namespace "${name}" already exists.`, 'alert');
        return;
      }
      tree[idx] = { ...tree[idx], name, description: nsDraft.description };
    }
    await commitTree(tree);
    if (mode === null) {
      selectedNS = name; // jump to the new/renamed namespace
    }
  }

  async function deleteNamespace(name: string) {
    const ns = namespaces.find((n) => n.name === name);
    const repoCount = ns?.repositories?.length ?? 0;
    const msg = repoCount > 0
      ? `Delete namespace "${name}" and its ${repoCount} repositor${repoCount === 1 ? 'y' : 'ies'}?\n\nThis removes the routing configuration. On-disk artifacts under the backing raw mount are NOT deleted.`
      : `Delete namespace "${name}"?`;
    if (!window.confirm(msg)) return;
    const tree = namespaces.filter((n) => n.name !== name);
    if (selectedNS === name) {
      selectedNS = tree.length > 0 ? tree[0].name : null;
      selectedRepo = null;
    }
    await commitTree(tree);
  }

  async function saveRepository() {
    const m = mode;
    if (!isRepoMode(m)) return;
    const name = (repoDraft.name ?? '').trim().toLowerCase();
    if (!name) {
      addToast('Repository name is required.', 'alert');
      return;
    }
    if (!/^[a-z0-9_-]+$/.test(name)) {
      addToast('Repository name must match [a-z0-9_-]+.', 'alert');
      return;
    }
    // Per-kind validation. Server re-validates; this is just UX.
    if (repoDraft.kind === 'local') {
      if (!repoDraft.mount) {
        addToast('Local repository requires a mount.', 'alert');
        return;
      }
      if (!repoDraft.base_path) {
        addToast('Local repository requires a base path.', 'alert');
        return;
      }
      if (repoDraft.type === 'docker') {
        const immutableTags = immutableTagsText.split(',').map((s) => s.trim()).filter(Boolean);
        if (immutableTags.some((s) => s.includes('/'))) {
          addToast('Immutable tag patterns must match tags, not paths.', 'alert');
          return;
        }
        if (Number(gcMinAgeSeconds) < 0 || Number(abandonedUploadMaxAgeSeconds) < 0) {
          addToast('Retention windows must be zero or positive seconds.', 'alert');
          return;
        }
      }
    } else if (repoDraft.kind === 'remote') {
      if (!repoDraft.url) {
        addToast('Remote repository requires an upstream URL.', 'alert');
        return;
      }
      if (!repoDraft.mount) {
        addToast('Remote repository requires a cache mount.', 'alert');
        return;
      }
      if (!repoDraft.base_path) {
        addToast('Remote repository requires a cache base path.', 'alert');
        return;
      }
    } else if (repoDraft.kind === 'virtual') {
      const members = virtualMembersText.split(',').map((s) => s.trim()).filter(Boolean);
      if (members.length === 0) {
        addToast('Virtual repository requires at least one member.', 'alert');
        return;
      }
      repoDraft.members = members;
    }

    const tree = JSON.parse(JSON.stringify(namespaces)) as Namespace[];
    const nsIdx = tree.findIndex((n) => n.name === m.namespace);
    if (nsIdx === -1) {
      addToast('Target namespace not found.', 'alert');
      return;
    }
    const ns = tree[nsIdx];
    ns.repositories = ns.repositories ?? [];

    // Build the row to persist — strip fields that don't apply to
    // this kind so the on-disk shape stays clean.
    const row: Repository = {
      name,
      description: repoDraft.description?.trim() || undefined,
      type: repoDraft.type,
      kind: repoDraft.kind,
    };
    if (repoDraft.kind === 'local') {
      row.mount = repoDraft.mount;
      row.base_path = repoDraft.base_path;
      row.allow_push = repoDraft.allow_push !== false;
      if (repoDraft.type === 'docker') {
        const immutableTags = immutableTagsText.split(',').map((s) => s.trim()).filter(Boolean);
        const gcMinAge = Math.floor(Number(gcMinAgeSeconds) || 0);
        const uploadMaxAge = Math.floor(Number(abandonedUploadMaxAgeSeconds) || 0);
        const retention: { gc_min_age_seconds?: number; abandoned_upload_max_age_seconds?: number } = {};
        if (gcMinAge > 0) retention.gc_min_age_seconds = gcMinAge;
        if (uploadMaxAge > 0) retention.abandoned_upload_max_age_seconds = uploadMaxAge;
        if (immutableTags.length > 0 || Object.keys(retention).length > 0) {
          row.policy = {};
          if (immutableTags.length > 0) row.policy.immutable_tags = immutableTags;
          if (Object.keys(retention).length > 0) row.policy.retention = retention;
        }
      }
    } else if (repoDraft.kind === 'remote') {
      row.url = repoDraft.url;
      row.mount = repoDraft.mount;
      row.base_path = repoDraft.base_path;
      if (repoDraft.mutable_ttl) row.mutable_ttl = repoDraft.mutable_ttl;
      if (repoDraft.insecure_skip_verify) row.insecure_skip_verify = true;
      // FloatingTags is Docker-only; the server silently ignores it
      // for Go / NPM but we keep the on-disk shape clean by not
      // writing it for other types. Empty input means "server
      // default" — preserved by omitting the field entirely.
      if (repoDraft.type === 'docker') {
        const ft = floatingTagsText.split(',').map((s) => s.trim()).filter(Boolean);
        if (ft.length > 0) row.floating_tags = ft;
      }
      // Auth: only include the type-relevant fields. The UI keeps the
      // editor minimal; raw:// and config:// refs are the recommended
      // way to supply secrets without persisting plaintext here.
      if (repoDraft.auth?.type) {
        row.auth = { type: repoDraft.auth.type };
        if (repoDraft.auth.type === 'basic') {
          row.auth.username = repoDraft.auth.username;
          row.auth.password = repoDraft.auth.password;
        } else if (repoDraft.auth.type === 'bearer') {
          row.auth.token = repoDraft.auth.token;
        } else if (repoDraft.auth.type === 'header') {
          row.auth.header = repoDraft.auth.header;
          row.auth.value = repoDraft.auth.value;
        }
      }
    } else if (repoDraft.kind === 'virtual') {
      row.members = repoDraft.members;
      if (repoDraft.default_local) row.default_local = repoDraft.default_local;
    }

    // Common per-repo overrides — applicable across kinds, so live
    // outside the kind-switch. Empty inputs are dropped to preserve
    // the canonical "field absent" shape over "field empty".
    const cors = corsOriginsText
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean);
    if (cors.length > 0) row.cors_origins = cors;
    if (maxUploadValue && maxUploadValue > 0) {
      const mult = maxUploadUnit === 'GB' ? 1024 * 1024 * 1024 : 1024 * 1024;
      row.max_upload_size = Math.floor(maxUploadValue) * mult;
    }

    if (m.kind === 'new-repository') {
      if (ns.repositories.some((r) => r.name === name)) {
        addToast(`Repository "${name}" already exists in this namespace.`, 'alert');
        return;
      }
      ns.repositories.push(row);
    } else {
      const original = m.original;
      const rIdx = ns.repositories.findIndex((r) => r.name === original);
      if (rIdx === -1) {
        addToast('Original repository not found.', 'alert');
        return;
      }
      if (name !== original && ns.repositories.some((r) => r.name === name)) {
        addToast(`Repository "${name}" already exists in this namespace.`, 'alert');
        return;
      }
      ns.repositories[rIdx] = row;
    }
    await commitTree(tree);
  }

  async function deleteRepository(namespaceName: string, repoName: string) {
    if (!window.confirm(`Delete repository "${namespaceName}/${repoName}"?\n\nThe routing configuration is removed. On-disk artifacts under the backing raw mount are NOT deleted.`)) {
      return;
    }
    const tree = JSON.parse(JSON.stringify(namespaces)) as Namespace[];
    const ns = tree.find((n) => n.name === namespaceName);
    if (!ns) return;
    ns.repositories = (ns.repositories ?? []).filter((r) => r.name !== repoName);
    if (selectedRepo?.name === repoName && selectedNS === namespaceName) {
      selectedRepo = null;
    }
    await commitTree(tree);
  }

  // Sibling-repo list for the virtual-members hint. Helps the user
  // type valid names without leaving the page.
  const siblingRepoNames = $derived.by(() => {
    const m = mode;
    if (!isRepoMode(m)) return [];
    const ns = namespaces.find((n) => n.name === m.namespace);
    return (ns?.repositories ?? [])
      .filter((r) => r.kind !== 'virtual')
      .filter((r) => m.kind !== 'edit-repository' || r.name !== m.original)
      .map((r) => r.name);
  });

  const selectedRepos = $derived.by(() => {
    if (!selectedNS) return [];
    const ns = namespaces.find((n) => n.name === selectedNS);
    const all = ns?.repositories ?? [];
    if (!repoFilter) return all;
    return all.filter((r) => matchFilter(r.name, repoFilter));
  });
  const selectedReposTotal = $derived.by(() => {
    if (!selectedNS) return 0;
    const ns = namespaces.find((n) => n.name === selectedNS);
    return ns?.repositories?.length ?? 0;
  });

  onMount(async () => {
    // Settings are needed so we can preserve the disabled flag
    // across saves. Independent of the registry tree load.
    if (canAdmin) {
      await configStore.loadSettings();
    }
    await load();
  });
</script>

<div class="flex flex-col h-full bg-warm-50 dark:bg-warm-950 text-warm-900 dark:text-warm-100">
  <!-- Header strip -->
  <header class="flex items-center justify-between px-4 py-2 border-b border-warm-200 dark:border-warm-800 bg-white dark:bg-warm-900">
    <div class="flex items-center gap-2">
      <Package size={18} class="text-accent-500" />
      <h1 class="text-base font-semibold">Registries</h1>
    </div>
    <div class="flex items-center gap-3">
      <div class="text-[11px] text-warm-500 dark:text-warm-400">
        Artifact hub — Go · NPM · Docker / OCI · Helm · Maven · PyPI · Cargo
      </div>
      {#if canAdmin}
        <button
          class="flex items-center gap-1 text-xs px-2 py-1 rounded border border-warm-300 dark:border-warm-700 hover:bg-warm-100 dark:hover:bg-warm-800"
          onclick={openNewNamespace}
          title="Create a new namespace"
        >
          <Plus size={12} />
          New namespace
        </button>
      {/if}
    </div>
  </header>

  {#if !booted}
    <div class="flex-1 flex items-center justify-center text-warm-500">
      <Loader2 size={20} class="animate-spin mr-2" />
      Loading registries…
    </div>
  {:else if !registryEnabled}
    <!--
      Feature is server-disabled. Operators reach this page through a
      bookmark or direct URL even after the navbar link is hidden;
      give them a single self-service hint instead of a stack of
      failed-to-load toasts.
    -->
    <div class="flex-1 flex items-center justify-center">
      <div class="max-w-md text-center px-4">
        <Package size={48} class="mx-auto text-warm-400 mb-4" />
        <h2 class="text-lg font-semibold mb-2">Registry feature is disabled</h2>
        <p class="text-sm text-warm-500 dark:text-warm-400 mb-4">
          An administrator has turned off the artifact registry for this deployment.
          Existing namespaces and repositories are preserved — re-enable the feature
          to make them serve again.
        </p>
        <a
          href="/settings"
          use:link
          class="inline-flex items-center gap-1.5 text-sm px-3 py-1.5 rounded border border-warm-300 dark:border-warm-700 hover:bg-warm-100 dark:hover:bg-warm-800"
        >
          Go to Settings → Features
        </a>
      </div>
    </div>
  {:else if namespaces.length === 0}
    <div class="flex-1 flex items-center justify-center">
      <div class="max-w-md text-center px-4">
        <Package size={48} class="mx-auto text-warm-400 mb-4" />
        <h2 class="text-lg font-semibold mb-2">No registries configured yet</h2>
        <p class="text-sm text-warm-500 dark:text-warm-400 mb-4">
          Pika can host Go modules, NPM packages, Docker / OCI images, Helm charts,
          Maven artifacts, PyPI packages and Cargo crates using
          your existing raw mounts as storage. Each namespace groups local,
          remote (upstream-proxied) and virtual (aggregated) repositories.
        </p>
        {#if canAdmin}
          <button
            class="inline-flex items-center gap-1.5 text-sm px-3 py-1.5 rounded border border-accent-300 dark:border-accent-700 bg-accent-50 dark:bg-accent-950/30 hover:bg-accent-100 dark:hover:bg-accent-900/40 text-accent-800 dark:text-accent-200"
            onclick={openNewNamespace}
          >
            <Plus size={14} />
            Create your first namespace
          </button>
        {:else}
          <p class="text-xs text-warm-500">
            Ask an administrator with the <span class="font-mono">registry.admin</span>
            capability to configure namespaces and repositories.
          </p>
        {/if}
      </div>
    </div>
  {:else}
    <div class="flex-1 flex overflow-hidden">
      <!-- Namespaces sidebar -->
      <aside class="w-48 border-r border-warm-200 dark:border-warm-800 bg-white dark:bg-warm-900 overflow-y-auto shrink-0">
        <div class="px-3 py-2 text-[10px] uppercase tracking-wide text-warm-500 flex items-center justify-between">
          <span>Namespaces</span>
          <span class="text-[9px] normal-case text-warm-400">{namespaces.length}</span>
        </div>
        {#if namespaces.length > 5}
          <div class="px-2 pb-2">
            <input
              type="text"
              placeholder="Filter…"
              bind:value={nsFilter}
              class="w-full text-[11px] px-2 py-1 bg-warm-50 dark:bg-warm-800 border border-warm-200 dark:border-warm-700 rounded focus:border-accent-500 outline-none"
            />
          </div>
        {/if}
        <ul>
          {#each namespaces.filter((n) => matchFilter(n.name, nsFilter)) as ns (ns.name)}
            <li class="group relative">
              <button
                class="w-full text-left px-3 py-2 hover:bg-warm-100 dark:hover:bg-warm-800 text-sm flex items-center justify-between"
                class:bg-warm-100={selectedNS === ns.name}
                class:dark:bg-warm-800={selectedNS === ns.name}
                onclick={() => { selectedNS = ns.name; selectedRepo = null; }}
              >
                <span class="font-medium truncate">{ns.name}</span>
                <span class="text-[10px] text-warm-500 shrink-0 ml-2">
                  {ns.repositories?.length ?? 0}
                </span>
              </button>
              {#if canAdmin}
                <div class="absolute right-1 top-1/2 -translate-y-1/2 flex gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
                  <button
                    class="p-1 rounded hover:bg-warm-200 dark:hover:bg-warm-700"
                    title="Edit namespace"
                    onclick={(e) => { e.stopPropagation(); openEditNamespace(ns); }}
                  >
                    <Pencil size={11} />
                  </button>
                  <button
                    class="p-1 rounded hover:bg-red-100 dark:hover:bg-red-900/40 text-red-600 dark:text-red-400"
                    title="Delete namespace"
                    onclick={(e) => { e.stopPropagation(); deleteNamespace(ns.name); }}
                  >
                    <Trash2 size={11} />
                  </button>
                </div>
              {/if}
            </li>
          {/each}
        </ul>
      </aside>

      <!-- Repositories panel -->
      <section class="w-72 border-r border-warm-200 dark:border-warm-800 bg-white dark:bg-warm-900 overflow-y-auto shrink-0">
        <div class="px-3 py-2 text-[10px] uppercase tracking-wide text-warm-500 flex items-center justify-between">
          <span>Repositories {#if selectedReposTotal > 0}<span class="normal-case text-warm-400">({selectedRepos.length}{repoFilter ? ' / ' + selectedReposTotal : ''})</span>{/if}</span>
          {#if canAdmin && selectedNS}
            <button
              class="flex items-center gap-1 text-[10px] normal-case tracking-normal px-1.5 py-0.5 rounded border border-warm-300 dark:border-warm-700 hover:bg-warm-100 dark:hover:bg-warm-800"
              onclick={() => openNewRepository(selectedNS!)}
              title="Add a new repository to {selectedNS}"
            >
              <Plus size={10} />
              Add
            </button>
          {/if}
        </div>
        {#if selectedReposTotal > 5}
          <div class="px-2 pb-2">
            <input
              type="text"
              placeholder="Filter repositories…"
              bind:value={repoFilter}
              class="w-full text-[11px] px-2 py-1 bg-warm-50 dark:bg-warm-800 border border-warm-200 dark:border-warm-700 rounded focus:border-accent-500 outline-none"
            />
          </div>
        {/if}
        {#if selectedRepos.length === 0}
          <div class="px-3 py-2 text-xs text-warm-500">
            Namespace has no repositories yet.
            {#if canAdmin && selectedNS}
              <button
                class="block mt-2 text-accent-600 dark:text-accent-400 hover:underline"
                onclick={() => openNewRepository(selectedNS!)}
              >
                + Add repository
              </button>
            {/if}
          </div>
        {:else}
          <ul>
            {#each selectedRepos as repo (repo.name)}
              {@const TypeIcon = iconFor(repo.type)}
              {@const KindIcon = kindIcon(repo.kind)}
              <li class="group relative">
                <button
                  class="w-full text-left px-3 py-2 hover:bg-warm-100 dark:hover:bg-warm-800 border-b border-warm-100 dark:border-warm-800/50"
                  class:bg-warm-100={selectedRepo?.name === repo.name}
                  class:dark:bg-warm-800={selectedRepo?.name === repo.name}
                  onclick={() => selectRepo(repo)}
                >
                  <div class="flex items-center gap-2">
                    <TypeIcon size={14} class="text-accent-500 shrink-0" />
                    <span class="text-sm font-medium truncate">{repo.name}</span>
                    <span class="ml-auto flex items-center gap-1 text-[9px] uppercase text-warm-500 shrink-0">
                      <KindIcon size={10} />
                      {repo.kind}
                    </span>
                  </div>
                  <div class="text-[10px] text-warm-500 mt-0.5 font-mono truncate">
                    {repo.type}
                    {#if repo.kind === 'local'}· {repo.mount}{/if}
                    {#if repo.kind === 'remote'}· {repo.url}{/if}
                  </div>
                </button>
                {#if canAdmin}
                  <div class="absolute right-1 top-1.5 flex gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
                    <button
                      class="p-1 rounded bg-white/80 dark:bg-warm-800/80 hover:bg-warm-200 dark:hover:bg-warm-700"
                      title="Edit repository"
                      onclick={(e) => { e.stopPropagation(); openEditRepository(selectedNS!, repo); }}
                    >
                      <Pencil size={11} />
                    </button>
                    <button
                      class="p-1 rounded bg-white/80 dark:bg-warm-800/80 hover:bg-red-100 dark:hover:bg-red-900/40 text-red-600 dark:text-red-400"
                      title="Delete repository"
                      onclick={(e) => { e.stopPropagation(); deleteRepository(selectedNS!, repo.name); }}
                    >
                      <Trash2 size={11} />
                    </button>
                  </div>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </section>

      <!-- Detail panel -->
      <main class="flex-1 overflow-y-auto bg-warm-50 dark:bg-warm-950">
        {#if !selectedRepo}
          <div class="h-full flex items-center justify-center text-sm text-warm-500">
            Select a repository to inspect its contents.
          </div>
        {:else}
          {@const repo = selectedRepo}
          {@const TypeIcon = iconFor(repo.type)}
          {@const KindIcon = kindIcon(repo.kind)}
          {@const endpoint = endpointURL(selectedNS ?? '', repo.name)}

          <div class="p-4 max-w-4xl">
            <!-- Repo header -->
            <div class="flex items-center gap-2 mb-3">
              <TypeIcon size={22} class="text-accent-500" />
              <h2 class="text-lg font-semibold">{repo.name}</h2>
              <span class="flex items-center gap-1 text-[10px] uppercase text-warm-500 px-1.5 py-0.5 rounded border border-warm-300 dark:border-warm-700">
                <KindIcon size={11} />
                {repo.kind}
              </span>
              <span class="text-[10px] uppercase text-warm-500 px-1.5 py-0.5 rounded border border-warm-300 dark:border-warm-700">
                {repo.type}
              </span>
              {#if (repo.kind === 'remote' || repo.kind === 'virtual') && canAdmin}
                <button
                  class="ml-auto flex items-center gap-1 text-xs px-2 py-1 rounded border border-warm-300 dark:border-warm-700 hover:bg-warm-100 dark:hover:bg-warm-800 disabled:opacity-50"
                  disabled={purgeRunning || repo.kind === 'virtual'}
                  onclick={() => runCachePurge(selectedNS ?? '', repo)}
                  title={repo.kind === 'virtual'
                    ? 'Virtual repos delegate to their members — purge each member individually'
                    : 'Drop cached upstream responses so the next read re-fetches'}
                >
                  {#if purgeRunning}
                    <Loader2 size={12} class="animate-spin" />
                  {:else}
                    <RotateCw size={12} />
                  {/if}
                  Refresh cache
                </button>
              {/if}
              <!--
                Docker Local cleanup moved out of the top-bar action
                row and into the dedicated "Garbage" card below the
                Statistics panel. The card surfaces an estimate
                first so the operator sees what they're about to
                delete; this avoids a click that does nothing on a
                clean registry.
              -->
              {#if repo.type === 'docker' && repo.kind === 'local' && canAdmin}
                <!-- intentional spacer: ml-auto on the next sibling needs the row -->
                <span class="ml-auto"></span>
              {/if}
            </div>

            {#if repo.description}
              <p class="text-xs text-warm-600 dark:text-warm-400 mb-3">
                {repo.description}
              </p>
            {/if}

            <!-- Endpoint snippet -->
            <div class="mb-4 border border-warm-200 dark:border-warm-800 rounded-md bg-white dark:bg-warm-900 p-3">
              <div class="text-[10px] uppercase tracking-wide text-warm-500 mb-1">
                Endpoint
              </div>
              <div class="flex items-center gap-2">
                <code class="flex-1 text-xs font-mono bg-warm-100 dark:bg-warm-800 px-2 py-1 rounded truncate">
                  {endpoint}
                </code>
                <button
                  class="p-1.5 rounded hover:bg-warm-100 dark:hover:bg-warm-800"
                  title="Copy endpoint URL"
                  onclick={() => copyToClipboard(endpoint)}
                >
                  <Copy size={14} />
                </button>
              </div>
            </div>

            <!-- Per-kind metadata strip -->
            <div class="mb-4 grid grid-cols-2 md:grid-cols-3 gap-2 text-xs">
              <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900">
                <div class="text-[10px] uppercase text-warm-500">Type / Kind</div>
                <div class="font-mono">{repo.type} · {repo.kind}</div>
              </div>
              {#if repo.kind === 'local'}
                <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900">
                  <div class="text-[10px] uppercase text-warm-500">Mount</div>
                  <div class="font-mono truncate">{repo.mount}</div>
                </div>
                <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900">
                  <div class="text-[10px] uppercase text-warm-500">Base path</div>
                  <div class="font-mono truncate">{repo.base_path || '/'}</div>
                </div>
                <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900">
                  <div class="text-[10px] uppercase text-warm-500">Push enabled</div>
                  <div class="font-mono">{repo.allow_push ? 'yes' : 'no'}</div>
                </div>
                {#if repo.type === 'docker'}
                  <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900">
                    <div class="text-[10px] uppercase text-warm-500">Immutable tags</div>
                    <div class="font-mono truncate">{repo.policy?.immutable_tags?.join(', ') || 'none'}</div>
                  </div>
                  <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900">
                    <div class="text-[10px] uppercase text-warm-500">GC grace</div>
                    <div class="font-mono">{formatSeconds(effectiveGCMinAge(repo))}</div>
                  </div>
                  <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900">
                    <div class="text-[10px] uppercase text-warm-500">Stale uploads</div>
                    <div class="font-mono">{formatSeconds(effectiveAbandonedUploadMaxAge(repo))}</div>
                  </div>
                {/if}
              {:else if repo.kind === 'remote'}
                <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900 col-span-2">
                  <div class="text-[10px] uppercase text-warm-500">Upstream</div>
                  <div class="font-mono truncate">{repo.url}</div>
                </div>
                <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900">
                  <div class="text-[10px] uppercase text-warm-500">Cache mount</div>
                  <div class="font-mono truncate">{repo.mount}</div>
                </div>
                <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900">
                  <div class="text-[10px] uppercase text-warm-500">Cache path</div>
                  <div class="font-mono truncate">{repo.base_path || '/'}</div>
                </div>
              {:else if repo.kind === 'virtual'}
                <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900 col-span-2">
                  <div class="text-[10px] uppercase text-warm-500">Members (in lookup order)</div>
                  <div class="font-mono truncate">
                    {repo.members?.join(', ') ?? '(none)'}
                  </div>
                </div>
              {/if}
            </div>

            <!-- Statistics card. Hidden for Virtual repos (no
                 backing store of their own — they delegate to
                 members). Re-fetched on each repo selection; the
                 server walks storage live so we don't poll. -->
            {#if repo.kind !== 'virtual'}
              <div class="mb-4 border border-warm-200 dark:border-warm-800 rounded-md bg-white dark:bg-warm-900 p-3">
                <div class="flex items-center justify-between mb-2">
                  <div class="text-[10px] uppercase tracking-wide text-warm-500">Statistics</div>
                  <button
                    class="text-[10px] flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-warm-100 dark:hover:bg-warm-800"
                    title="Re-fetch counts"
                    disabled={statsLoading}
                    onclick={() => selectedNS && loadStats(selectedNS, repo)}
                  >
                    {#if statsLoading}
                      <Loader2 size={10} class="animate-spin" />
                    {:else}
                      <RotateCw size={10} />
                    {/if}
                    refresh
                  </button>
                </div>
                {#if statsLoading && !stats}
                  <div class="text-xs text-warm-500">Loading…</div>
                {:else if !stats}
                  <div class="text-xs text-warm-500">Statistics unavailable.</div>
                {:else}
                  <div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-2 text-xs">
                    {#if repo.type === 'go'}
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Modules</div>
                        <div class="font-mono">{stats.module_count ?? 0}</div>
                      </div>
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Versions</div>
                        <div class="font-mono">{stats.version_count ?? 0}</div>
                      </div>
                    {:else if repo.type === 'npm'}
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Packages</div>
                        <div class="font-mono">{stats.package_count ?? 0}</div>
                      </div>
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Versions</div>
                        <div class="font-mono">{stats.version_count ?? 0}</div>
                      </div>
                    {:else if repo.type === 'maven'}
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Artifacts</div>
                        <div class="font-mono">{stats.package_count ?? 0}</div>
                      </div>
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Versions</div>
                        <div class="font-mono">{stats.version_count ?? 0}</div>
                      </div>
                    {:else if repo.type === 'pypi'}
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Packages</div>
                        <div class="font-mono">{stats.package_count ?? 0}</div>
                      </div>
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Versions</div>
                        <div class="font-mono">{stats.version_count ?? 0}</div>
                      </div>
                    {:else if repo.type === 'cargo'}
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Crates</div>
                        <div class="font-mono">{stats.package_count ?? 0}</div>
                      </div>
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Versions</div>
                        <div class="font-mono">{stats.version_count ?? 0}</div>
                      </div>
                    {:else if repo.type === 'docker'}
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Repos</div>
                        <div class="font-mono">{stats.repository_count ?? 0}</div>
                      </div>
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Tags</div>
                        <div class="font-mono">{stats.tag_count ?? 0}</div>
                      </div>
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Manifests</div>
                        <div class="font-mono">{stats.manifest_count ?? 0}</div>
                      </div>
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Blobs</div>
                        <div class="font-mono">{stats.blob_count ?? 0}</div>
                      </div>
                    {/if}
                    <div>
                      <div class="text-[10px] uppercase text-warm-500">Total size</div>
                      <div class="font-mono">{humanBytes(stats.total_bytes ?? 0)}</div>
                    </div>
                  </div>
                {/if}
              </div>
            {/if}

            <!-- Garbage card. Docker Local only — the cheap
                 delete-time cascade in the registry handler
                 already drops manifests + sidecars + orphan
                 referrer indexes anytime an image is deleted by
                 digest. Layer / config blobs are shareable, so
                 those wait for an explicit cleanup pass.
                 Operators trigger that pass here; the estimate
                 row tells them whether it's worth the click. -->
            {#if repo.type === 'docker' && repo.kind === 'local' && canAdmin}
              <div class="mb-4 border border-warm-200 dark:border-warm-800 rounded-md bg-white dark:bg-warm-900 p-3">
                <div class="flex items-center justify-between mb-2">
                  <div class="text-[10px] uppercase tracking-wide text-warm-500">Garbage</div>
                  <div class="flex items-center gap-2">
                    <button
                      class="text-[10px] flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-warm-100 dark:hover:bg-warm-800 disabled:opacity-50"
                      title="Dry-run mark-and-sweep: estimate reclaimable garbage without deleting anything"
                      disabled={gcEstimateLoading || gcRunning}
                      onclick={() => selectedNS && refreshGCEstimate(selectedNS, repo.name)}
                    >
                      {#if gcEstimateLoading}
                        <Loader2 size={10} class="animate-spin" />
                      {:else}
                        <RotateCw size={10} />
                      {/if}
                      estimate
                    </button>
                    <button
                      class="text-[10px] flex items-center gap-1 px-1.5 py-0.5 rounded border border-warm-300 dark:border-warm-700 hover:bg-warm-100 dark:hover:bg-warm-800 disabled:opacity-50"
                      title={`Run cleanup now: deletes unreferenced blobs, manifests and abandoned upload tmp files. Items modified in the last ${formatSeconds(effectiveGCMinAge(repo))} are protected.`}
                      disabled={gcRunning || gcEstimateLoading}
                      onclick={() => runDockerGC(selectedNS ?? '', repo.name)}
                    >
                      {#if gcRunning}
                        <Loader2 size={10} class="animate-spin" />
                      {:else}
                        <Trash2 size={10} />
                      {/if}
                      clean up
                    </button>
                  </div>
                </div>
                <div class="mb-2 text-[10px] text-warm-500">
                  Policy defaults: GC grace {formatSeconds(effectiveGCMinAge(repo))}, stale uploads {formatSeconds(effectiveAbandonedUploadMaxAge(repo))}.
                </div>
                {#if gcEstimateLoading && !gcEstimate}
                  <div class="text-xs text-warm-500">Calculating…</div>
                {:else if !gcEstimate || gcEstimateRepoKey !== `${selectedNS}/${repo.name}`}
                  <div class="text-xs text-warm-500">
                    Click <span class="font-medium">estimate</span> to see how much storage can be reclaimed.
                  </div>
                {:else}
                  {@const totalReclaimable = gcEstimate.swept_bytes + gcEstimate.abandoned_uploads_bytes}
                  {@const nothingToDo = gcEstimate.swept_blobs === 0
                    && gcEstimate.swept_manifests === 0
                    && gcEstimate.abandoned_uploads_removed === 0}
                  {#if nothingToDo}
                    <div class="text-xs text-warm-500">
                      Nothing reclaimable — registry is clean.
                    </div>
                  {:else}
                    <div class="grid grid-cols-2 sm:grid-cols-4 gap-2 text-xs">
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Reclaimable</div>
                        <div class="font-mono">{humanBytes(totalReclaimable)}</div>
                      </div>
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Blobs</div>
                        <div class="font-mono">{gcEstimate.swept_blobs}</div>
                      </div>
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Manifests</div>
                        <div class="font-mono">{gcEstimate.swept_manifests}</div>
                      </div>
                      <div>
                        <div class="text-[10px] uppercase text-warm-500">Stale uploads</div>
                        <div class="font-mono">{gcEstimate.abandoned_uploads_removed}</div>
                      </div>
                    </div>
                    {#if gcEstimate.skipped_young > 0}
                      <div class="mt-2 text-[10px] text-warm-500">
                        {gcEstimate.skipped_young} item{gcEstimate.skipped_young === 1 ? '' : 's'} protected by grace window (modified &lt; {formatSeconds(effectiveGCMinAge(repo))} ago).
                      </div>
                    {/if}
                  {/if}
                {/if}
              </div>
            {/if}

            <!-- Browser. Each protocol renders its cached/local package
                 index with the same click-through detail interaction. -->
            {#if repo.type === 'go' || repo.type === 'npm' || repo.type === 'docker' || repo.type === 'helm' || repo.type === 'maven' || repo.type === 'pypi' || repo.type === 'cargo'}
              {@const isGo = repo.type === 'go'}
              {@const isNpm = repo.type === 'npm'}
              {@const isDocker = repo.type === 'docker'}
              {@const isHelm = repo.type === 'helm'}
              {@const isMaven = repo.type === 'maven'}
              {@const isPyPI = repo.type === 'pypi'}
              {@const isCargo = repo.type === 'cargo'}
              {@const entryLabel = isGo ? 'Modules' : isNpm ? 'Packages' : isDocker ? 'Repositories' : isHelm ? 'Charts' : isMaven ? 'Artifacts' : isPyPI ? 'Packages' : 'Crates'}
              {@const entryCount = isGo ? modules.length : isNpm ? packages.length : isDocker ? images.length : isHelm ? charts.length : isMaven ? mavenArtifacts.length : isPyPI ? pypiPackages.length : cargoCrates.length}
              <div class="border border-warm-200 dark:border-warm-800 rounded-md bg-white dark:bg-warm-900">
                <div class="px-3 py-2 border-b border-warm-200 dark:border-warm-800 flex items-center gap-2">
                  <FolderTree size={14} class="text-accent-500" />
                  <span class="text-sm font-semibold">{entryLabel}</span>
                  {#if entriesLoading}
                    <Loader2 size={12} class="animate-spin text-warm-500" />
                  {:else if repo.kind !== 'virtual'}
                    <span class="text-[10px] text-warm-500">({entryCount})</span>
                  {/if}
                  {#if repo.kind !== 'virtual' && entryCount > 0}
                    <div class="ml-auto flex items-center gap-1">
                      <Search size={10} class="text-warm-500" />
                      <input
                        type="text"
                        placeholder="Filter…"
                        bind:value={entryFilter}
                        class="text-[11px] px-1.5 py-0.5 w-32 bg-warm-50 dark:bg-warm-800 border border-warm-200 dark:border-warm-700 rounded focus:border-accent-500 outline-none"
                      />
                    </div>
                  {/if}
                </div>
                {#if repo.kind === 'virtual'}
                  <div class="px-3 py-2 text-xs text-warm-500">
                    Virtual repositories aggregate at request time; entry
                    listings live on the underlying member repositories.
                  </div>
                {:else if entryCount === 0 && !entriesLoading}
                  <div class="px-3 py-4 text-xs text-warm-500 text-center">
                    {#if repo.kind === 'local'}
                      {isGo ? 'No modules' : isNpm ? 'No packages' : isDocker ? 'No images' : isHelm ? 'No charts' : isMaven ? 'No artifacts' : isPyPI ? 'No packages' : 'No crates'} uploaded yet.
                      {#if isGo && repo.allow_push}
                        <div class="mt-2 text-[11px] font-mono text-left bg-warm-100 dark:bg-warm-800 p-2 rounded">
                          # upload via curl{'\n'}
                          curl -XPUT -H "Authorization: Bearer $TOKEN" \{'\n'}
                          {'  '}-H "Content-Type: application/json" \{'\n'}
                          {'  '}--data '{`{"Version":"v1.0.0"}`}' \{'\n'}
                          {'  '}{endpoint}/&lt;module&gt;/@v/v1.0.0.info
                        </div>
                      {:else if isNpm && repo.allow_push}
                        <div class="mt-2 text-[11px] font-mono text-left bg-warm-100 dark:bg-warm-800 p-2 rounded">
                          # publish via npm{'\n'}
                          npm publish --registry={endpoint}/
                        </div>
                      {:else if isDocker && repo.allow_push}
                        <div class="mt-2 text-[11px] font-mono text-left bg-warm-100 dark:bg-warm-800 p-2 rounded">
                          # push via docker{'\n'}
                          docker tag &lt;img&gt; {endpoint.replace(/^https?:\/\//, '')}/v2/&lt;img&gt;:&lt;tag&gt;{'\n'}
                          docker push {endpoint.replace(/^https?:\/\//, '')}/v2/&lt;img&gt;:&lt;tag&gt;
                        </div>
                      {:else if isHelm && repo.allow_push}
                        <div class="mt-2 text-[11px] font-mono text-left bg-warm-100 dark:bg-warm-800 p-2 rounded">
                          # publish via curl (ChartMuseum-compatible){'\n'}
                          curl -XPOST -H "Authorization: Bearer $TOKEN" \{'\n'}
                          {'  '}--data-binary @mychart-1.0.0.tgz \{'\n'}
                          {'  '}{endpoint}/api/charts
                        </div>
                      {:else if isMaven && repo.allow_push}
                        <div class="mt-2 text-[11px] font-mono text-left bg-warm-100 dark:bg-warm-800 p-2 rounded">
                          # deploy via Maven settings.xml / pom.xml repository URL{`\n`}
                          {endpoint}/com/example/app/1.0.0/app-1.0.0.jar
                        </div>
                      {:else if isPyPI && repo.allow_push}
                        <div class="mt-2 text-[11px] font-mono text-left bg-warm-100 dark:bg-warm-800 p-2 rounded">
                          # publish via twine{`\n`}
                          twine upload --repository-url {endpoint}/ dist/*
                        </div>
                      {:else if isCargo && repo.allow_push}
                        <div class="mt-2 text-[11px] font-mono text-left bg-warm-100 dark:bg-warm-800 p-2 rounded">
                          # seed a crate archive via HTTP PUT{`\n`}
                          curl -XPUT --data-binary @crate.crate {endpoint}/api/v1/crates/&lt;crate&gt;/&lt;version&gt;/download
                        </div>
                      {/if}
                    {:else}
                      Nothing in cache yet — the first client request will
                      populate this list.
                    {/if}
                  </div>
                {:else if isGo}
                  <ul class="text-sm">
                    {#each modules.filter((m) => matchFilter(m.module, entryFilter)) as m (m.module)}
                      <li class="border-b border-warm-100 dark:border-warm-800/50 last:border-b-0">
                        <button
                          class="w-full text-left px-3 py-2 hover:bg-warm-50 dark:hover:bg-warm-800/50 flex items-center gap-2"
                          onclick={() => openDetail(m.module, 'go')}
                          title="View details"
                        >
                          <FileBox size={14} class="text-warm-500" />
                          <span class="font-mono text-xs">{m.module}</span>
                          <span class="ml-auto text-[10px] text-warm-500">
                            {m.versions.length} version{m.versions.length === 1 ? '' : 's'}
                          </span>
                          <ChevronRight size={12} class="text-warm-400" />
                        </button>
                      </li>
                    {/each}
                  </ul>
                {:else if isDocker}
                  <ul class="text-sm">
                    {#each images.filter((i) => matchFilter(i.name, entryFilter)) as img (img.name)}
                      {@const hasArtifacts = img.tags.some((t) => t.artifact_type)}
                      <li class="border-b border-warm-100 dark:border-warm-800/50 last:border-b-0">
                        <button
                          class="w-full text-left px-3 py-2 hover:bg-warm-50 dark:hover:bg-warm-800/50 flex items-center gap-2"
                          onclick={() => openDetail(img.name, 'docker')}
                          title="View details"
                        >
                          <Container size={14} class="text-warm-500" />
                          <span class="font-mono text-xs">{img.name}</span>
                          {#if hasArtifacts}
                            <span class="text-[9px] uppercase px-1 py-0.5 rounded bg-accent-500/10 text-accent-500 border border-accent-500/30">
                              OCI artifact
                            </span>
                          {/if}
                          <span class="ml-auto text-[10px] text-warm-500">
                            {img.tags.length} tag{img.tags.length === 1 ? '' : 's'}
                          </span>
                          <ChevronRight size={12} class="text-warm-400" />
                        </button>
                      </li>
                    {/each}
                  </ul>
                {:else if isNpm}
                  <ul class="text-sm">
                    {#each packages.filter((p) => matchFilter(p.name, entryFilter)) as p (p.name)}
                      <li class="border-b border-warm-100 dark:border-warm-800/50 last:border-b-0">
                        <button
                          class="w-full text-left px-3 py-2 hover:bg-warm-50 dark:hover:bg-warm-800/50 flex items-center gap-2"
                          onclick={() => openDetail(p.name, 'npm')}
                          title="View details"
                        >
                          <Package size={14} class="text-warm-500" />
                          <span class="font-mono text-xs">{p.name}</span>
                          {#if p.dist_tags?.latest}
                            <span class="text-[10px] text-accent-500 font-mono">@{p.dist_tags.latest}</span>
                          {/if}
                          <span class="ml-auto text-[10px] text-warm-500">
                            {p.versions.length} version{p.versions.length === 1 ? '' : 's'}
                          </span>
                          <ChevronRight size={12} class="text-warm-400" />
                        </button>
                      </li>
                    {/each}
                  </ul>
                {:else if isHelm}
                  <ul class="text-sm">
                    {#each charts.filter((ch) => matchFilter(ch.name, entryFilter)) as ch (ch.name)}
                      <li class="border-b border-warm-100 dark:border-warm-800/50 last:border-b-0">
                        <button
                          class="w-full text-left px-3 py-2 hover:bg-warm-50 dark:hover:bg-warm-800/50 flex items-center gap-2"
                          onclick={() => openDetail(ch.name, 'helm')}
                          title="View details"
                        >
                          <Anchor size={14} class="text-warm-500" />
                          <span class="font-mono text-xs">{ch.name}</span>
                          <span class="ml-auto text-[10px] text-warm-500">
                            {ch.versions.length} version{ch.versions.length === 1 ? '' : 's'}
                          </span>
                          <ChevronRight size={12} class="text-warm-400" />
                        </button>
                      </li>
                    {/each}
                  </ul>
                {:else if isMaven}
                  <ul class="text-sm">
                    {#each mavenArtifacts.filter((a) => matchFilter(`${a.group_id}:${a.artifact_id}`, entryFilter)) as art (`${art.group_id}:${art.artifact_id}`)}
                      <li class="border-b border-warm-100 dark:border-warm-800/50 last:border-b-0">
                        <button
                          class="w-full text-left px-3 py-2 hover:bg-warm-50 dark:hover:bg-warm-800/50 flex items-center gap-2"
                          onclick={() => openDetail(`${art.group_id}:${art.artifact_id}`, 'maven')}
                          title="View details"
                        >
                          <Package size={14} class="text-warm-500" />
                          <span class="font-mono text-xs">{art.group_id}:{art.artifact_id}</span>
                          <span class="ml-auto text-[10px] text-warm-500">
                            {art.versions.length} version{art.versions.length === 1 ? '' : 's'}
                          </span>
                          <ChevronRight size={12} class="text-warm-400" />
                        </button>
                      </li>
                    {/each}
                  </ul>
                {:else if isPyPI}
                  <ul class="text-sm">
                    {#each pypiPackages.filter((p) => matchFilter(p.name, entryFilter)) as p (p.name)}
                      <li class="border-b border-warm-100 dark:border-warm-800/50 last:border-b-0">
                        <button
                          class="w-full text-left px-3 py-2 hover:bg-warm-50 dark:hover:bg-warm-800/50 flex items-center gap-2"
                          onclick={() => openDetail(p.name, 'pypi')}
                          title="View details"
                        >
                          <Package size={14} class="text-warm-500" />
                          <span class="font-mono text-xs">{p.name}</span>
                          <span class="ml-auto text-[10px] text-warm-500">
                            {p.versions.length} version{p.versions.length === 1 ? '' : 's'}
                          </span>
                          <ChevronRight size={12} class="text-warm-400" />
                        </button>
                      </li>
                    {/each}
                  </ul>
                {:else if isCargo}
                  <ul class="text-sm">
                    {#each cargoCrates.filter((c) => matchFilter(c.name, entryFilter)) as cr (cr.name)}
                      <li class="border-b border-warm-100 dark:border-warm-800/50 last:border-b-0">
                        <button
                          class="w-full text-left px-3 py-2 hover:bg-warm-50 dark:hover:bg-warm-800/50 flex items-center gap-2"
                          onclick={() => openDetail(cr.name, 'cargo')}
                          title="View details"
                        >
                          <Package size={14} class="text-warm-500" />
                          <span class="font-mono text-xs">{cr.name}</span>
                          <span class="ml-auto text-[10px] text-warm-500">
                            {cr.versions.length} version{cr.versions.length === 1 ? '' : 's'}
                          </span>
                          <ChevronRight size={12} class="text-warm-400" />
                        </button>
                      </li>
                    {/each}
                  </ul>
                {/if}
              </div>
            {:else}
              <div class="border border-warm-200 dark:border-warm-800 rounded-md bg-white dark:bg-warm-900 px-3 py-4 text-xs text-warm-500 text-center">
                The browser for {repo.type} registries is coming in a follow-up
                phase. The endpoint above is already live.
              </div>
            {/if}
          </div>
        {/if}
      </main>
    </div>
  {/if}

  <!-- ─── Admin modal (new/edit namespace + repository) ─────────── -->
  <Modal open={!!mode} onClose={cancelModal} size="sm">
    {#snippet header()}
      {#if mode}
        <h2 class="text-base font-semibold">
          {#if mode.kind === 'new-namespace'}New namespace
          {:else if mode.kind === 'edit-namespace'}Edit namespace
          {:else if mode.kind === 'new-repository'}New repository in {modeNamespace(mode)}
          {:else}Edit repository in {modeNamespace(mode)}
          {/if}
        </h2>
        <button class="p-1 rounded hover:bg-warm-100 dark:hover:bg-warm-800" onclick={cancelModal} title="Cancel">
          <X size={16} />
        </button>
      {/if}
    {/snippet}

    {#if mode}
        <div class="p-4 space-y-3 text-sm">
          {#if mode.kind === 'new-namespace' || mode.kind === 'edit-namespace'}
            <!-- ── Namespace form ── -->
            <label class="block">
              <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                Name <span class="text-red-500">*</span>
              </span>
              <input
                type="text"
                class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                bind:value={nsDraft.name}
                placeholder="my-team"
                pattern="[a-z0-9_-]+"
              />
              <span class="block mt-1 text-[10px] text-warm-500">
                Lowercase alphanumerics, hyphen and underscore. Becomes the URL path segment.
              </span>
            </label>

            <label class="block">
              <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">Description</span>
              <input
                type="text"
                class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 text-xs"
                bind:value={nsDraft.description}
                placeholder="Optional"
              />
            </label>
          {:else}
            <!-- ── Repository form ── -->
            <label class="block">
              <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                Name <span class="text-red-500">*</span>
              </span>
              <input
                type="text"
                class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                bind:value={repoDraft.name}
                placeholder="proxy-cache"
                pattern="[a-z0-9_-]+"
              />
            </label>

            <label class="block">
              <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">Description</span>
              <input
                type="text"
                class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 text-xs"
                bind:value={repoDraft.description}
                placeholder="Optional"
              />
            </label>

            <div class="grid grid-cols-2 gap-3">
              <label class="block">
                <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                  Type <span class="text-red-500">*</span>
                </span>
                <select
                  class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 text-xs"
                  bind:value={repoDraft.type}
                >
                  <option value="go">Go modules</option>
                  <option value="npm">NPM packages</option>
                  <option value="docker">Docker / OCI</option>
                  <option value="helm">Helm charts</option>
                  <option value="maven">Maven artifacts</option>
                  <option value="pypi">PyPI packages</option>
                  <option value="cargo">Cargo crates</option>
                </select>
              </label>

              <label class="block">
                <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                  Kind <span class="text-red-500">*</span>
                </span>
                <select
                  class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 text-xs"
                  bind:value={repoDraft.kind}
                >
                  <option value="local">Local — stored on a raw mount</option>
                  <option value="remote">Remote — proxy + cache an upstream</option>
                  <option value="virtual">Virtual — aggregate sibling repos</option>
                </select>
              </label>
            </div>

            {#if repoDraft.kind === 'local'}
              <!-- ── Local: mount + base path ── -->
              <div class="rounded border border-warm-200 dark:border-warm-800 p-3 bg-warm-50/50 dark:bg-warm-950/30 space-y-3">
                <label class="block">
                  <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                    Storage mount <span class="text-red-500">*</span>
                  </span>
                  {#if rawMounts.length === 0}
                    <div class="text-[11px] text-amber-700 dark:text-amber-400">
                      No raw mounts configured. Add one under Settings → Raw mounts first.
                    </div>
                  {:else}
                    <select
                      class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                      bind:value={repoDraft.mount}
                    >
                      <option value="">— select a mount —</option>
                      {#each rawMounts as m (m.prefix)}
                        <option value={m.prefix}>{m.prefix}</option>
                      {/each}
                    </select>
                  {/if}
                </label>

                <label class="block">
                  <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                    Base path <span class="text-red-500">*</span>
                  </span>
                  <input
                    type="text"
                    class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                    bind:value={repoDraft.base_path}
                    placeholder={defaultBasePath(repoDraft.type)}
                  />
                  <span class="block mt-1 text-[10px] text-warm-500">
                    Path inside the mount where artifacts will be stored.
                  </span>
                </label>

                <label class="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    class="rounded border-warm-400 dark:border-warm-600"
                    checked={repoDraft.allow_push !== false}
                    onchange={(e) => repoDraft.allow_push = (e.currentTarget as HTMLInputElement).checked}
                  />
                  <span class="text-xs">Allow publish / push (otherwise read-only)</span>
                </label>

                {#if repoDraft.type === 'docker'}
                  <div class="rounded border border-warm-200 dark:border-warm-800 p-3 bg-white dark:bg-warm-900 space-y-3">
                    <div>
                      <div class="text-xs font-medium text-warm-700 dark:text-warm-300">Docker policy + retention</div>
                      <div class="text-[10px] text-warm-500">
                        Enforced by the local Docker handler. Remote and virtual repos keep policy on their member repos.
                      </div>
                    </div>

                    <label class="block">
                      <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                        Immutable tag patterns
                      </span>
                      <input
                        type="text"
                        class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                        bind:value={immutableTagsText}
                        placeholder="prod, release-*, v*"
                      />
                      <span class="block mt-1 text-[10px] text-warm-500 leading-relaxed">
                        Comma-separated shell-style tag patterns. Matching tags can be created once,
                        then cannot be moved to another digest or deleted. Do not include image paths.
                      </span>
                    </label>

                    <div class="grid grid-cols-2 gap-3">
                      <label class="block">
                        <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                          GC grace seconds
                        </span>
                        <input
                          type="number"
                          min="0"
                          step="1"
                          class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                          bind:value={gcMinAgeSeconds}
                          placeholder="3600"
                        />
                        <span class="block mt-1 text-[10px] text-warm-500">0 = default 1h</span>
                      </label>

                      <label class="block">
                        <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                          Stale upload seconds
                        </span>
                        <input
                          type="number"
                          min="0"
                          step="1"
                          class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                          bind:value={abandonedUploadMaxAgeSeconds}
                          placeholder="86400"
                        />
                        <span class="block mt-1 text-[10px] text-warm-500">0 = default 24h</span>
                      </label>
                    </div>
                  </div>
                {/if}
              </div>
            {:else if repoDraft.kind === 'remote'}
              <!-- ── Remote: upstream URL + cache storage + optional auth ── -->
              <div class="rounded border border-warm-200 dark:border-warm-800 p-3 bg-warm-50/50 dark:bg-warm-950/30 space-y-3">
                <label class="block">
                  <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                    Upstream URL <span class="text-red-500">*</span>
                  </span>
                  <input
                    type="url"
                    class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                    bind:value={repoDraft.url}
                    placeholder={defaultUpstreamURL(repoDraft.type)}
                  />
                </label>

                <div class="grid grid-cols-2 gap-3">
                  <label class="block">
                    <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                      Cache mount <span class="text-red-500">*</span>
                    </span>
                    {#if rawMounts.length === 0}
                      <div class="text-[11px] text-amber-700 dark:text-amber-400">
                        No raw mounts configured. Add one under Settings → Raw mounts first.
                      </div>
                    {:else}
                      <select
                        class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                        bind:value={repoDraft.mount}
                      >
                        <option value="">— select a mount —</option>
                        {#each rawMounts as m (m.prefix)}
                          <option value={m.prefix}>{m.prefix}</option>
                        {/each}
                      </select>
                    {/if}
                  </label>

                  <label class="block">
                    <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                      Cache base path <span class="text-red-500">*</span>
                    </span>
                    <input
                      type="text"
                      class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                      bind:value={repoDraft.base_path}
                      placeholder={defaultCachePath(repoDraft.type)}
                    />
                  </label>
                </div>

                <span class="block -mt-1 text-[10px] text-warm-500 leading-relaxed">
                  Pulled artifacts are stored in this raw mount path and served from cache
                  on later reads. Use a dedicated cache path per remote repository.
                </span>

                <!-- B8: connectivity probe button. Only enabled
                     when editing an existing repo (the probe uses
                     the persisted credentials, so the draft state
                     must already be saved). For new repos the
                     button is hidden — operators publish first,
                     then probe. -->
                {#if mode?.kind === 'edit-repository'}
                  <div class="flex items-center gap-2 text-xs">
                    <button
                      class="px-2 py-1 rounded border border-warm-300 dark:border-warm-700 hover:bg-warm-100 dark:hover:bg-warm-800 flex items-center gap-1 disabled:opacity-50"
                      onclick={runProbe}
                      disabled={probeRunning}
                    >
                      {#if probeRunning}<Loader2 size={10} class="animate-spin" />{:else}<RotateCw size={10} />{/if}
                      Test upstream
                    </button>
                    {#if probeResult}
                      {#if probeResult.ok}
                        <span class="text-green-600 dark:text-green-400">
                          ✓ {probeResult.status_code} · {probeResult.latency_ms} ms
                        </span>
                      {:else}
                        <span class="text-red-500" title={probeResult.error}>
                          ✗ {probeResult.status_code || 'error'}
                        </span>
                      {/if}
                    {/if}
                  </div>
                  {#if probeResult && probeResult.body_preview}
                    <details class="text-[10px] text-warm-500">
                      <summary class="cursor-pointer">Response preview</summary>
                      <pre class="mt-1 font-mono whitespace-pre-wrap break-all">{probeResult.body_preview}</pre>
                    </details>
                  {/if}
                {/if}

                <label class="block">
                  <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                    Mutable cache TTL
                  </span>
                  <input
                    type="text"
                    class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                    bind:value={repoDraft.mutable_ttl}
                    placeholder={repoDraft.type === 'docker' ? '1h' : '5m'}
                  />
                  <span class="block mt-1 text-[10px] text-warm-500 leading-relaxed">
                    {#if repoDraft.type === 'go'}
                      How long to cache <strong>mutable</strong> upstream responses — the version
                      list (<code class="font-mono">@v/list</code>) and latest pointer
                      (<code class="font-mono">@latest</code>). Within the TTL pika serves the
                      cached copy without hitting <code class="font-mono">proxy.golang.org</code>;
                      after it, the next request triggers a refresh. Immutable files
                      (<code class="font-mono">.info</code>, <code class="font-mono">.mod</code>,
                      <code class="font-mono">.zip</code>) are cached forever regardless.
                      Default: <code class="font-mono">5m</code>. Set <code class="font-mono">0s</code>
                      to disable the cache (always hit upstream) or a longer duration
                      (<code class="font-mono">1h</code>, <code class="font-mono">24h</code>)
                      to reduce upstream traffic at the cost of slower version-list freshness.
                    {:else if repoDraft.type === 'npm'}
                      How long to cache <strong>mutable</strong> packument responses (the
                      package metadata document that lists versions and dist-tags). Tarballs
                      themselves are content-addressed and cached forever. Default:
                      <code class="font-mono">5m</code>. Set <code class="font-mono">0s</code>
                      to always re-fetch, or a longer duration to reduce upstream load.
                    {:else if repoDraft.type === 'maven'}
                      How long to cache <strong>mutable</strong> Maven metadata files
                      (<code class="font-mono">maven-metadata.xml</code>). Versioned JAR/POM
                      artifacts are cached forever after first fetch. Default:
                      <code class="font-mono">5m</code>.
                    {:else if repoDraft.type === 'pypi'}
                      How long to cache <strong>mutable</strong> PEP 503 simple index pages.
                      Distribution files are cached forever after first download. Default:
                      <code class="font-mono">5m</code>.
                    {:else if repoDraft.type === 'cargo'}
                      How long to cache <strong>mutable</strong> sparse-index files. Downloaded
                      <code class="font-mono">.crate</code> archives are cached forever after
                      first fetch. Default: <code class="font-mono">5m</code>.
                    {:else}
                      How long to cache <strong>floating</strong> Docker tags (see the field
                      below). Blob layers and manifests-by-digest are immutable and cached
                      forever; tag → digest lookups for non-floating tags (semver, dated)
                      are also cached forever. This TTL <em>only</em> applies to tags listed
                      as "floating". Default: <code class="font-mono">5m</code>.
                    {/if}
                    <br />
                    Format: Go duration string — <code class="font-mono">5m</code>,
                    <code class="font-mono">1h</code>, <code class="font-mono">24h</code>,
                    <code class="font-mono">0s</code>.
                  </span>
                </label>

                {#if repoDraft.type === 'docker'}
                  <label class="block">
                    <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                      Floating tags
                    </span>
                    <input
                      type="text"
                      class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                      bind:value={floatingTagsText}
                      placeholder="latest, main, master, dev, develop, nightly, edge, stable, canary"
                    />
                    <span class="block mt-1 text-[10px] text-warm-500 leading-relaxed">
                      Comma-separated list of tag names treated as <strong>mutable</strong>.
                      Pika re-resolves these tags through upstream every TTL window. Tags
                      <strong>not</strong> in this list (e.g. <code class="font-mono">v1.2.3</code>,
                      <code class="font-mono">2024-05-19</code>) are cached forever after the
                      first successful resolve — once pika has the digest, it never asks
                      upstream again, even after a registry restart.
                      <br />
                      Leave empty to use the default list shown as placeholder. Use a single
                      <code class="font-mono">*</code> to make every tag floating (matches the
                      pre-classification behaviour, useful for upstreams that overwrite semver
                      tags). Matching is case-insensitive.
                    </span>
                  </label>
                {/if}

                <label class="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    class="rounded border-warm-400 dark:border-warm-600"
                    checked={repoDraft.insecure_skip_verify === true}
                    onchange={(e) => repoDraft.insecure_skip_verify = (e.currentTarget as HTMLInputElement).checked}
                  />
                  <span class="text-xs">Skip TLS verify (self-signed upstream only)</span>
                </label>

                <label class="block">
                  <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                    Upstream auth (optional)
                  </span>
                  <select
                    class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 text-xs"
                    value={repoDraft.auth?.type ?? ''}
                    onchange={(e) => {
                      const v = (e.currentTarget as HTMLSelectElement).value;
                      if (!v) repoDraft.auth = undefined;
                      else repoDraft.auth = { ...(repoDraft.auth ?? {}), type: v as 'basic' | 'bearer' | 'header' };
                    }}
                  >
                    <option value="">None</option>
                    <option value="basic">HTTP basic (username + password)</option>
                    <option value="bearer">Bearer token</option>
                    <option value="header">Custom header</option>
                  </select>
                </label>

                {#if repoDraft.auth?.type === 'basic'}
                  <div class="grid grid-cols-2 gap-3">
                    <input
                      type="text"
                      class="px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                      placeholder="username"
                      bind:value={repoDraft.auth.username}
                    />
                    <!--
                      Password field. Rendered as type=password by
                      default so casual screen-watching doesn't leak
                      the value; click 👁 to flip the input type to
                      text. The on-disk value is sealed via the
                      secret layer; the server transparently injects
                      plaintext back into the form on edit.
                    -->
                    <div class="relative">
                      <input
                        type={revealPassword ? 'text' : 'password'}
                        class="w-full px-2 py-1 pr-7 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                        placeholder="password, raw://mount/file#/key, or config://file#/path/to/key"
                        bind:value={repoDraft.auth.password}
                      />
                      <button
                        type="button"
                        class="absolute right-1 top-1/2 -translate-y-1/2 p-1 rounded hover:bg-warm-100 dark:hover:bg-warm-700"
                        onclick={() => (revealPassword = !revealPassword)}
                        title={revealPassword ? 'Hide password' : 'Show password'}
                      >
                        {#if revealPassword}<EyeOff size={11} />{:else}<Eye size={11} />{/if}
                      </button>
                    </div>
                  </div>
                {:else if repoDraft.auth?.type === 'bearer'}
                  <div class="relative">
                    <input
                      type={revealToken ? 'text' : 'password'}
                      class="w-full px-2 py-1 pr-7 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                      placeholder="token, raw://mount/file#/key, or config://file#/path/to/key"
                      bind:value={repoDraft.auth.token}
                    />
                    <button
                      type="button"
                      class="absolute right-1 top-1/2 -translate-y-1/2 p-1 rounded hover:bg-warm-100 dark:hover:bg-warm-700"
                      onclick={() => (revealToken = !revealToken)}
                      title={revealToken ? 'Hide token' : 'Show token'}
                    >
                      {#if revealToken}<EyeOff size={11} />{:else}<Eye size={11} />{/if}
                    </button>
                  </div>
                {:else if repoDraft.auth?.type === 'header'}
                  <div class="grid grid-cols-2 gap-3">
                    <input
                      type="text"
                      class="px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                      placeholder="X-Header-Name"
                      bind:value={repoDraft.auth.header}
                    />
                    <div class="relative">
                      <input
                        type={revealHeaderValue ? 'text' : 'password'}
                        class="w-full px-2 py-1 pr-7 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                        placeholder="value, raw://mount/file#/key, or config://file#/path/to/key"
                        bind:value={repoDraft.auth.value}
                      />
                      <button
                        type="button"
                        class="absolute right-1 top-1/2 -translate-y-1/2 p-1 rounded hover:bg-warm-100 dark:hover:bg-warm-700"
                        onclick={() => (revealHeaderValue = !revealHeaderValue)}
                        title={revealHeaderValue ? 'Hide value' : 'Show value'}
                      >
                        {#if revealHeaderValue}<EyeOff size={11} />{:else}<Eye size={11} />{/if}
                      </button>
                    </div>
                  </div>
                {/if}

                <p class="text-[10px] text-warm-500 leading-relaxed">
                  Secret values can be inline plaintext or direct
                  <code class="font-mono">raw://mount/path</code> /
                  <code class="font-mono">config://key</code> references. For structured JSON/YAML/TOML
                  secrets, append a JSON Pointer like <code class="font-mono">#/registry/token</code>.
                  Plaintext values are sealed before persistence; references are resolved at request time.
                </p>
              </div>
            {:else}
              <!-- ── Virtual: member list + default-local hint ── -->
              <div class="rounded border border-warm-200 dark:border-warm-800 p-3 bg-warm-50/50 dark:bg-warm-950/30 space-y-3">
                <label class="block">
                  <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                    Members <span class="text-red-500">*</span>
                  </span>
                  <input
                    type="text"
                    class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                    bind:value={virtualMembersText}
                    placeholder="local-repo, proxy-cache"
                  />
                  <span class="block mt-1 text-[10px] text-warm-500">
                    Comma-separated sibling repository names (within {modeNamespace(mode)}). Lookup tries them in order; first match wins.
                  </span>
                  {#if siblingRepoNames.length > 0}
                    <span class="block mt-1 text-[10px] text-warm-500">
                      Available: <code class="font-mono">{siblingRepoNames.join(', ')}</code>
                    </span>
                  {/if}
                </label>

                <label class="block">
                  <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">Default local (hint)</span>
                  <input
                    type="text"
                    class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                    bind:value={repoDraft.default_local}
                    placeholder="Optional — which local member receives writes"
                  />
                </label>
              </div>
            {/if}

            <!-- ── B6: common per-repo overrides ────────────────────
                 cors_origins and max_upload_size apply to any kind
                 (local/remote/virtual). Live below the kind-specific
                 fields so the form reads top-to-bottom: identity →
                 kind selector → kind-specific fields → common
                 overrides. -->
            <details class="border-t border-warm-200 dark:border-warm-800 pt-3 mt-3">
              <summary class="cursor-pointer text-xs font-medium text-warm-600 dark:text-warm-400 hover:text-warm-900 dark:hover:text-warm-100">
                Advanced (CORS, upload size)
              </summary>
              <div class="space-y-3 mt-3">
                <label class="block">
                  <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                    CORS origins
                  </span>
                  <input
                    type="text"
                    class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                    bind:value={corsOriginsText}
                    placeholder="https://app.example.com, *"
                  />
                  <span class="block mt-1 text-[10px] text-warm-500">
                    Comma-separated origins allowed for browser-based clients.
                    Use <code class="font-mono">*</code> as a single entry for
                    permissive CORS. Empty disables CORS headers (server-default
                    behaviour).
                  </span>
                </label>

                <div>
                  <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                    Max upload size
                  </span>
                  <div class="flex items-center gap-2">
                    <input
                      type="number"
                      min="0"
                      step="1"
                      class="w-24 px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                      bind:value={maxUploadValue}
                      placeholder="0"
                    />
                    <select
                      class="px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 text-xs"
                      bind:value={maxUploadUnit}
                    >
                      <option value="MB">MB</option>
                      <option value="GB">GB</option>
                    </select>
                    <span class="text-[10px] text-warm-500">0 = type default</span>
                  </div>
                  <span class="block mt-1 text-[10px] text-warm-500">
                    Caps the publish / push body size. Server-side defaults: NPM
                    200 MB, Docker (per-blob) varies by upload session.
                  </span>
                </div>
              </div>
            </details>
          {/if}
        </div>
    {/if}

    {#snippet footer()}
      <button
        class="px-3 py-1 text-xs rounded border border-warm-300 dark:border-warm-700 hover:bg-warm-100 dark:hover:bg-warm-800"
        onclick={cancelModal}
        disabled={saving}
      >
        Cancel
      </button>
      <button
        class="px-3 py-1 text-xs rounded bg-accent-500 hover:bg-accent-600 text-white disabled:opacity-50 flex items-center gap-1"
        onclick={() => {
          if (mode?.kind === 'new-namespace' || mode?.kind === 'edit-namespace') saveNamespace();
          else if (mode?.kind === 'new-repository' || mode?.kind === 'edit-repository') saveRepository();
        }}
        disabled={saving}
      >
        {#if saving}<Loader2 size={12} class="animate-spin" />{/if}
        Save
      </button>
    {/snippet}
  </Modal>

  <!-- Side-panel package detail. Mounted at the page root so it
       overlays everything (namespace sidebar, repo list, detail
       column) instead of nesting inside the detail column where
       it would be clipped by the column's overflow rules. -->
  {#if detailPackage && selectedRepo && selectedNS}
    <PackageDetailPanel
      namespace={selectedNS}
      repoName={selectedRepo.name}
      repoType={detailPackage.type}
      packageName={detailPackage.name}
      endpoint={endpointURL(selectedNS, selectedRepo.name)}
      canDelete={canDelete}
      ondeleted={refreshAfterArtifactDelete}
      onclose={closeDetail}
    />
  {/if}
</div>
