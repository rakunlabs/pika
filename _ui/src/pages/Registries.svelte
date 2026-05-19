<script lang="ts">
  // Registries — top-level page for the artifact registry feature
  // (Go modules, NPM packages, Docker / OCI images).
  //
  // ── Scope of this page ────────────────────────────────────────────
  // Phase 1 surface: namespace + repository listing, type/kind
  // badges, per-Go-repo module browser with version list, "copy
  // GOPROXY" snippet for client config. Local repos additionally
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
  import { appStore } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { configStore } from '@/lib/store/config.svelte';
  import {
    Package, Container, FileBox, Library, Globe, Cloud, Loader2,
    Copy, ChevronRight, FolderTree, Trash2, Plus, Pencil, X,
  } from 'lucide-svelte';
  import { basePath } from '@/lib/basepath';

  // Settings shape (mirrors service.RegistrySettings). Kept local
  // rather than imported from @/lib/types/config so the page renders
  // even when the type file evolves — the runtime contract is the
  // /api/v1/registries response, not a TS type.
  type UpstreamAuth = {
    type?: 'basic' | 'bearer' | 'header';
    username?: string;
    password?: string;
    token?: string;
    header_name?: string;
    header_value?: string;
  };
  type Repository = {
    name: string;
    description?: string;
    type: 'go' | 'npm' | 'docker';
    kind: 'local' | 'remote' | 'virtual';
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
  };
  type Namespace = {
    name: string;
    description?: string;
    repositories?: Repository[];
  };

  // Module browser shape (mirrors api.goModuleEntry).
  type ModuleEntry = {
    module: string;
    versions: string[];
  };

  // NPM package shape (mirrors api.npmPackageEntry).
  type PackageEntry = {
    name: string;
    versions: string[];
    dist_tags: Record<string, string>;
  };

  // Docker repo shape (mirrors api.dockerRepoEntry +
  // dockerTagSummary).
  type DockerTag = {
    tag: string;
    digest?: string;
    artifact_type?: string;
    media_type?: string;
    size?: number;
  };
  type DockerEntry = {
    name: string;
    tags: DockerTag[];
  };

  let booted = $state(false);
  let namespaces = $state<Namespace[]>([]);
  let selectedNS = $state<string | null>(null);
  let selectedRepo = $state<Repository | null>(null);
  let modules = $state<ModuleEntry[]>([]);
  let packages = $state<PackageEntry[]>([]);
  let images = $state<DockerEntry[]>([]);
  let entriesLoading = $state(false);
  // For all three browsers — only one is shown at a time depending
  // on selectedRepo.type, so a single expanded-name key is enough.
  let expandedEntry = $state<string | null>(null);

  const canAdmin = $derived(appStore.hasPermission('registry.admin'));

  async function load() {
    try {
      const resp = await fetch(`${basePath}/api/v1/registries`, {
        credentials: 'same-origin',
      });
      if (!resp.ok) {
        throw new Error(`HTTP ${resp.status}`);
      }
      const body = await resp.json();
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
    entriesLoading = true;
    try {
      let url = '';
      if (repo.type === 'go') {
        url = `${basePath}/api/v1/registries/go/${encodeURIComponent(ns)}/${encodeURIComponent(repo.name)}/modules`;
      } else if (repo.type === 'npm') {
        url = `${basePath}/api/v1/registries/npm/${encodeURIComponent(ns)}/${encodeURIComponent(repo.name)}/packages`;
      } else if (repo.type === 'docker') {
        url = `${basePath}/api/v1/registries/docker/${encodeURIComponent(ns)}/${encodeURIComponent(repo.name)}/repos`;
      } else {
        return;
      }
      const resp = await fetch(url, { credentials: 'same-origin' });
      if (!resp.ok) {
        throw new Error(`HTTP ${resp.status}`);
      }
      const body = (await resp.json()) ?? [];
      if (repo.type === 'go') {
        modules = body;
      } else if (repo.type === 'npm') {
        packages = body;
      } else if (repo.type === 'docker') {
        images = body;
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
    }
  }

  // artifactTypeLabel maps the long media-type string to a short
  // human-readable label for the UI badge. Unknown types fall back
  // to "artifact".
  function artifactTypeLabel(mediaType: string): string {
    const known: Record<string, string> = {
      'application/vnd.cncf.helm.config.v1+json': 'helm',
      'application/vnd.dev.cosign.simplesigning.v1+json': 'cosign',
      'application/vnd.cncf.notary.signature': 'notary',
      'application/vnd.in-toto+json': 'in-toto',
      'application/spdx+json': 'sbom-spdx',
      'application/vnd.cyclonedx+json': 'sbom-cyclonedx',
      'application/vnd.aquasec.trivy.config.v1+json': 'trivy',
      'application/vnd.wasm.config.v1+json': 'wasm',
    };
    return known[mediaType] ?? 'artifact';
  }

  // formatSize returns a humanised byte count for the tag size
  // column ("1.4 MB", "823 B", ...).
  function formatSize(n: number): string {
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    let v = n;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i++;
    }
    return v.toFixed(v < 10 && i > 0 ? 1 : 0) + ' ' + units[i];
  }

  function iconFor(type: Repository['type']) {
    switch (type) {
      case 'go': return FileBox;
      case 'npm': return Package;
      case 'docker': return Container;
    }
  }

  function kindIcon(kind: Repository['kind']) {
    switch (kind) {
      case 'local': return Library;
      case 'remote': return Cloud;
      case 'virtual': return Globe;
    }
  }

  async function copyToClipboard(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      addToast('Copied to clipboard', 'success');
    } catch {
      addToast('Clipboard write failed', 'alert');
    }
  }

  // GC for Docker Local repos. Confirms with the user, runs the
  // server-side sweep, surfaces the resulting stats via toast,
  // then reloads the image list so freed-up entries disappear.
  let gcRunning = $state(false);
  async function runDockerGC(ns: string, repoName: string) {
    if (!window.confirm(`Run garbage collection on ${ns}/${repoName}?\n\nThis deletes unreferenced blobs and manifests. Anything pushed in the last hour is protected by the grace window.`)) {
      return;
    }
    gcRunning = true;
    try {
      const resp = await fetch(
        `${basePath}/api/v1/registries/docker/${encodeURIComponent(ns)}/${encodeURIComponent(repoName)}/gc`,
        {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ min_age_seconds: 3600 }),
        }
      );
      if (!resp.ok) {
        throw new Error(`HTTP ${resp.status}`);
      }
      const stats = await resp.json();
      const msg = `Swept ${stats.SweptBlobs} blob${stats.SweptBlobs === 1 ? '' : 's'}, ` +
                  `${stats.SweptManifests} manifest${stats.SweptManifests === 1 ? '' : 's'}. ` +
                  `Kept ${stats.MarkedBlobs} marked.`;
      addToast(msg, 'success');
      if (selectedRepo && selectedNS) {
        await loadEntries(selectedNS, selectedRepo);
      }
    } catch (err) {
      addToast(`GC failed: ${err}`, 'alert');
    } finally {
      gcRunning = false;
    }
  }

  // The endpoint URL the registry exposes to clients. The browser's
  // own origin is correct for the typical "same host as the UI"
  // deployment; operators behind a custom DNS get exact-match URLs
  // by browsing from that host.
  function endpointURL(ns: string, repo: string): string {
    const origin = window.location.origin;
    const bp = basePath;
    return `${origin}${bp}/registries/${ns}/${repo}`;
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
    mode = { kind: 'new-repository', namespace: namespaceName };
  }

  function openEditRepository(namespaceName: string, repo: Repository) {
    repoDraft = JSON.parse(JSON.stringify(repo)); // deep copy
    virtualMembersText = (repo.members ?? []).join(', ');
    floatingTagsText = (repo.floating_tags ?? []).join(', ');
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
    if (!mode || (mode.kind !== 'new-namespace' && mode.kind !== 'edit-namespace')) return;
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
    if (mode.kind === 'new-namespace') {
      if (tree.some((n) => n.name === name)) {
        addToast(`Namespace "${name}" already exists.`, 'alert');
        return;
      }
      tree.push({ name, description: nsDraft.description, repositories: [] });
    } else {
      const original = mode.original;
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
    if (!mode || (mode.kind !== 'new-repository' && mode.kind !== 'edit-repository')) return;
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
    } else if (repoDraft.kind === 'remote') {
      if (!repoDraft.url) {
        addToast('Remote repository requires an upstream URL.', 'alert');
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
    const nsIdx = tree.findIndex((n) => n.name === mode.namespace);
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
    } else if (repoDraft.kind === 'remote') {
      row.url = repoDraft.url;
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
      // editor minimal; "secret://path" refs are the recommended way
      // to supply secrets.
      if (repoDraft.auth?.type) {
        row.auth = { type: repoDraft.auth.type };
        if (repoDraft.auth.type === 'basic') {
          row.auth.username = repoDraft.auth.username;
          row.auth.password = repoDraft.auth.password;
        } else if (repoDraft.auth.type === 'bearer') {
          row.auth.token = repoDraft.auth.token;
        } else if (repoDraft.auth.type === 'header') {
          row.auth.header_name = repoDraft.auth.header_name;
          row.auth.header_value = repoDraft.auth.header_value;
        }
      }
    } else if (repoDraft.kind === 'virtual') {
      row.members = repoDraft.members;
      if (repoDraft.default_local) row.default_local = repoDraft.default_local;
    }

    if (mode.kind === 'new-repository') {
      if (ns.repositories.some((r) => r.name === name)) {
        addToast(`Repository "${name}" already exists in this namespace.`, 'alert');
        return;
      }
      ns.repositories.push(row);
    } else {
      const original = mode.original;
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
    if (!mode || (mode.kind !== 'new-repository' && mode.kind !== 'edit-repository')) return [];
    const ns = namespaces.find((n) => n.name === mode.namespace);
    return (ns?.repositories ?? [])
      .filter((r) => r.kind !== 'virtual')
      .filter((r) => mode!.kind !== 'edit-repository' || r.name !== mode!.original)
      .map((r) => r.name);
  });

  const selectedRepos = $derived.by(() => {
    if (!selectedNS) return [];
    const ns = namespaces.find((n) => n.name === selectedNS);
    return ns?.repositories ?? [];
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
        Artifact hub — Go modules · NPM · Docker / OCI
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
  {:else if namespaces.length === 0}
    <div class="flex-1 flex items-center justify-center">
      <div class="max-w-md text-center px-4">
        <Package size={48} class="mx-auto text-warm-400 mb-4" />
        <h2 class="text-lg font-semibold mb-2">No registries configured yet</h2>
        <p class="text-sm text-warm-500 dark:text-warm-400 mb-4">
          Pika can host Go modules, NPM packages and Docker / OCI images using
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
        <div class="px-3 py-2 text-[10px] uppercase tracking-wide text-warm-500">
          Namespaces
        </div>
        <ul>
          {#each namespaces as ns (ns.name)}
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
          <span>Repositories</span>
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
              {#if repo.type === 'docker' && repo.kind === 'local' && canAdmin}
                <button
                  class="ml-auto flex items-center gap-1 text-xs px-2 py-1 rounded border border-warm-300 dark:border-warm-700 hover:bg-warm-100 dark:hover:bg-warm-800 disabled:opacity-50"
                  disabled={gcRunning}
                  onclick={() => runDockerGC(selectedNS ?? '', repo.name)}
                  title="Mark-and-sweep garbage collect — deletes unreferenced blobs and manifests"
                >
                  {#if gcRunning}
                    <Loader2 size={12} class="animate-spin" />
                  {:else}
                    <Trash2 size={12} />
                  {/if}
                  Garbage collect
                </button>
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
              {#if repo.type === 'go'}
                <div class="mt-2 text-[11px] text-warm-500">
                  <span class="font-medium">Client setup:</span>
                  <code class="font-mono">GOPROXY={endpoint},direct</code>
                </div>
              {:else if repo.type === 'npm'}
                <div class="mt-2 text-[11px] text-warm-500">
                  <span class="font-medium">Client setup:</span>
                  <code class="font-mono">npm config set registry {endpoint}/</code>
                </div>
              {:else if repo.type === 'docker'}
                <div class="mt-2 text-[11px] text-warm-500">
                  <span class="font-medium">Client setup:</span>
                  <code class="font-mono">docker login {endpoint.replace(/^https?:\/\//, '').split('/')[0]}</code>
                  <br />
                  <code class="font-mono">docker push {endpoint.replace(/^https?:\/\//, '')}/v2/&lt;image&gt;:&lt;tag&gt;</code>
                </div>
              {/if}
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
              {:else if repo.kind === 'remote'}
                <div class="border border-warm-200 dark:border-warm-800 rounded p-2 bg-white dark:bg-warm-900 col-span-2">
                  <div class="text-[10px] uppercase text-warm-500">Upstream</div>
                  <div class="font-mono truncate">{repo.url}</div>
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

            <!-- Browser. Go shows modules; NPM shows packages;
                 Docker shows image repositories. All three share
                 the same expand-to-see-children interaction model. -->
            {#if repo.type === 'go' || repo.type === 'npm' || repo.type === 'docker'}
              {@const isGo = repo.type === 'go'}
              {@const isNpm = repo.type === 'npm'}
              {@const isDocker = repo.type === 'docker'}
              {@const entryLabel = isGo ? 'Modules' : isNpm ? 'Packages' : 'Repositories'}
              {@const entryCount = isGo ? modules.length : isNpm ? packages.length : images.length}
              <div class="border border-warm-200 dark:border-warm-800 rounded-md bg-white dark:bg-warm-900">
                <div class="px-3 py-2 border-b border-warm-200 dark:border-warm-800 flex items-center gap-2">
                  <FolderTree size={14} class="text-accent-500" />
                  <span class="text-sm font-semibold">{entryLabel}</span>
                  {#if entriesLoading}
                    <Loader2 size={12} class="animate-spin text-warm-500" />
                  {:else if repo.kind !== 'virtual'}
                    <span class="text-[10px] text-warm-500">({entryCount})</span>
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
                      {isGo ? 'No modules' : isNpm ? 'No packages' : 'No images'} uploaded yet.
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
                      {/if}
                    {:else}
                      Nothing in cache yet — the first client request will
                      populate this list.
                    {/if}
                  </div>
                {:else if isGo}
                  <ul class="text-sm">
                    {#each modules as m (m.module)}
                      <li class="border-b border-warm-100 dark:border-warm-800/50 last:border-b-0">
                        <button
                          class="w-full text-left px-3 py-2 hover:bg-warm-50 dark:hover:bg-warm-800/50 flex items-center gap-2"
                          onclick={() => expandedEntry = (expandedEntry === m.module ? null : m.module)}
                        >
                          <ChevronRight
                            size={14}
                            class="text-warm-500 transition-transform {expandedEntry === m.module ? 'rotate-90' : ''}"
                          />
                          <span class="font-mono text-xs">{m.module}</span>
                          <span class="ml-auto text-[10px] text-warm-500">
                            {m.versions.length} version{m.versions.length === 1 ? '' : 's'}
                          </span>
                        </button>
                        {#if expandedEntry === m.module}
                          <ul class="bg-warm-50 dark:bg-warm-950/40">
                            {#each m.versions as v}
                              <li class="px-9 py-1 text-[11px] font-mono text-warm-600 dark:text-warm-400">
                                {v}
                              </li>
                            {/each}
                          </ul>
                        {/if}
                      </li>
                    {/each}
                  </ul>
                {:else if isDocker}
                  <ul class="text-sm">
                    {#each images as img (img.name)}
                      {@const hasArtifacts = img.tags.some((t) => t.artifact_type)}
                      <li class="border-b border-warm-100 dark:border-warm-800/50 last:border-b-0">
                        <button
                          class="w-full text-left px-3 py-2 hover:bg-warm-50 dark:hover:bg-warm-800/50 flex items-center gap-2"
                          onclick={() => expandedEntry = (expandedEntry === img.name ? null : img.name)}
                        >
                          <ChevronRight
                            size={14}
                            class="text-warm-500 transition-transform {expandedEntry === img.name ? 'rotate-90' : ''}"
                          />
                          <span class="font-mono text-xs">{img.name}</span>
                          {#if hasArtifacts}
                            <span class="text-[9px] uppercase px-1 py-0.5 rounded bg-accent-500/10 text-accent-500 border border-accent-500/30">
                              OCI artifact
                            </span>
                          {/if}
                          <span class="ml-auto text-[10px] text-warm-500">
                            {img.tags.length} tag{img.tags.length === 1 ? '' : 's'}
                          </span>
                        </button>
                        {#if expandedEntry === img.name}
                          <ul class="bg-warm-50 dark:bg-warm-950/40">
                            {#each img.tags as t (t.tag)}
                              <li class="px-9 py-1.5 text-[11px] font-mono text-warm-600 dark:text-warm-400">
                                <div class="flex items-center gap-2">
                                  <span class="font-semibold text-warm-700 dark:text-warm-300">{t.tag}</span>
                                  {#if t.artifact_type}
                                    <span class="text-[9px] uppercase px-1 rounded bg-accent-500/10 text-accent-500">
                                      {artifactTypeLabel(t.artifact_type)}
                                    </span>
                                  {/if}
                                  {#if t.size}
                                    <span class="text-[9px] text-warm-500 ml-auto">{formatSize(t.size)}</span>
                                  {/if}
                                </div>
                                {#if t.digest}
                                  <div class="text-[10px] text-warm-500 truncate">{t.digest}</div>
                                {/if}
                              </li>
                            {/each}
                          </ul>
                        {/if}
                      </li>
                    {/each}
                  </ul>
                {:else}
                  <ul class="text-sm">
                    {#each packages as p (p.name)}
                      <li class="border-b border-warm-100 dark:border-warm-800/50 last:border-b-0">
                        <button
                          class="w-full text-left px-3 py-2 hover:bg-warm-50 dark:hover:bg-warm-800/50 flex items-center gap-2"
                          onclick={() => expandedEntry = (expandedEntry === p.name ? null : p.name)}
                        >
                          <ChevronRight
                            size={14}
                            class="text-warm-500 transition-transform {expandedEntry === p.name ? 'rotate-90' : ''}"
                          />
                          <span class="font-mono text-xs">{p.name}</span>
                          {#if p.dist_tags?.latest}
                            <span class="text-[10px] text-accent-500 font-mono">@{p.dist_tags.latest}</span>
                          {/if}
                          <span class="ml-auto text-[10px] text-warm-500">
                            {p.versions.length} version{p.versions.length === 1 ? '' : 's'}
                          </span>
                        </button>
                        {#if expandedEntry === p.name}
                          <div class="bg-warm-50 dark:bg-warm-950/40">
                            {#if Object.keys(p.dist_tags ?? {}).length > 0}
                              <div class="px-9 py-1 text-[10px] uppercase text-warm-500">dist-tags</div>
                              {#each Object.entries(p.dist_tags) as [tag, ver]}
                                <div class="px-9 py-0.5 text-[11px] font-mono text-warm-600 dark:text-warm-400">
                                  {tag}: {ver}
                                </div>
                              {/each}
                            {/if}
                            <div class="px-9 py-1 text-[10px] uppercase text-warm-500">versions</div>
                            {#each p.versions as v}
                              <div class="px-9 py-0.5 text-[11px] font-mono text-warm-600 dark:text-warm-400">
                                {v}
                              </div>
                            {/each}
                          </div>
                        {/if}
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
  {#if mode}
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      onclick={cancelModal}
    >
      <div
        class="bg-white dark:bg-warm-900 rounded-lg shadow-xl w-[min(90vw,560px)] max-h-[90vh] overflow-y-auto"
        onclick={(e) => e.stopPropagation()}
        role="document"
      >
        <header class="flex items-center justify-between px-4 py-3 border-b border-warm-200 dark:border-warm-800">
          <h2 class="text-base font-semibold">
            {#if mode.kind === 'new-namespace'}New namespace
            {:else if mode.kind === 'edit-namespace'}Edit namespace
            {:else if mode.kind === 'new-repository'}New repository in {mode.namespace}
            {:else}Edit repository in {mode.namespace}
            {/if}
          </h2>
          <button class="p-1 rounded hover:bg-warm-100 dark:hover:bg-warm-800" onclick={cancelModal} title="Cancel">
            <X size={16} />
          </button>
        </header>

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
                    placeholder={repoDraft.type === 'go' ? 'go/' : repoDraft.type === 'npm' ? 'npm/' : 'docker/'}
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
              </div>
            {:else if repoDraft.kind === 'remote'}
              <!-- ── Remote: upstream URL + optional auth ── -->
              <div class="rounded border border-warm-200 dark:border-warm-800 p-3 bg-warm-50/50 dark:bg-warm-950/30 space-y-3">
                <label class="block">
                  <span class="block text-xs font-medium text-warm-600 dark:text-warm-400 mb-1">
                    Upstream URL <span class="text-red-500">*</span>
                  </span>
                  <input
                    type="url"
                    class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                    bind:value={repoDraft.url}
                    placeholder={repoDraft.type === 'go' ? 'https://proxy.golang.org' : repoDraft.type === 'npm' ? 'https://registry.npmjs.org' : 'https://registry-1.docker.io'}
                  />
                </label>

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
                    <input
                      type="text"
                      class="px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                      placeholder="password or secret://path"
                      bind:value={repoDraft.auth.password}
                    />
                  </div>
                {:else if repoDraft.auth?.type === 'bearer'}
                  <input
                    type="text"
                    class="w-full px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                    placeholder="token or secret://path"
                    bind:value={repoDraft.auth.token}
                  />
                {:else if repoDraft.auth?.type === 'header'}
                  <div class="grid grid-cols-2 gap-3">
                    <input
                      type="text"
                      class="px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                      placeholder="X-Header-Name"
                      bind:value={repoDraft.auth.header_name}
                    />
                    <input
                      type="text"
                      class="px-2 py-1 rounded border border-warm-300 dark:border-warm-700 bg-white dark:bg-warm-800 font-mono text-xs"
                      placeholder="value or secret://path"
                      bind:value={repoDraft.auth.header_value}
                    />
                  </div>
                {/if}

                <p class="text-[10px] text-warm-500 leading-relaxed">
                  Secret values accept the <code class="font-mono">secret://mount/path</code>
                  reference scheme. The server resolves the secret at request time so plaintext
                  credentials never persist in settings.
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
                    Comma-separated sibling repository names (within {mode.kind === 'new-repository' ? mode.namespace : (mode.kind === 'edit-repository' ? mode.namespace : '')}). Lookup tries them in order; first match wins.
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
          {/if}
        </div>

        <footer class="flex items-center justify-end gap-2 px-4 py-3 border-t border-warm-200 dark:border-warm-800 bg-warm-50/50 dark:bg-warm-950/30">
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
        </footer>
      </div>
    </div>
  {/if}
</div>
