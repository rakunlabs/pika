<script lang="ts">
  // PackageDetailPanel — slide-in side panel that renders the full
  // detail document for one package / module / image / chart.
  //
  // Mounted as an overlay on top of the Registries page; the parent
  // owns the open/closed state via the `open` prop. Closing the
  // panel does not destroy the component — the parent simply hides
  // it — so the user can flip back to the previous selection
  // without re-fetching.
  //
  // Data contract: a single GET against
  //   /api/v1/registries/{type}/{ns}/{repo}/packages/{name}
  // returns the polymorphic PackageDetail document defined in
  // ./types.ts. The panel branches on detail.type to render the
  // appropriate sub-view.
  //
  // README rendering uses @comark/svelte; the markdown bytes come
  // from a secondary fetch against
  //   /api/v1/registries/npm/{ns}/{repo}/packages/{name}/readme
  // only when the user opens the README tab. We don't preload the
  // README because most users won't open the tab and tarball
  // extraction is non-trivial work on the server.

  import { onMount } from 'svelte';
  import { Comark } from '@comark/svelte';
  import {
    X, Loader2, Copy, FileText, Layers, Box, Tag, AlertTriangle,
    FileCode, Globe, Package, Trash2,
  } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import * as registryAPI from '@/lib/store/registry.svelte';
  import type { PackageDetail, RegistryType, NPMVersionDetail } from './types';
  import { formatSize, formatPublishedAt } from '@/lib/format';

  type Props = {
    namespace: string;
    repoName: string;
    repoType: RegistryType;
    packageName: string;
    // Public registry endpoint base for client-side install snippets
    // ("npm config set registry …", "go get …", "docker pull …").
    endpoint: string;
    canDelete?: boolean;
    ondeleted?: () => void | Promise<void>;
    onclose: () => void;
  };
  let { namespace, repoName, repoType, packageName, endpoint, canDelete = false, ondeleted, onclose }: Props = $props();

  type Tab = 'overview' | 'versions' | 'readme' | 'install' | 'layers';
  let activeTab = $state<Tab>('overview');

  let detail = $state<PackageDetail | null>(null);
  let loading = $state(false);
  let loadError = $state<string | null>(null);

  let readme = $state<string | null>(null);
  let readmeLoading = $state(false);
  let readmeError = $state<string | null>(null);

  // GoMod source for the currently selected version (Go only).
  let goModSource = $state<string | null>(null);
  let goModLoading = $state(false);
  let goModVersion = $state<string | null>(null);

  // Tag/version focus for the "layers" tab (Docker) and the
  // go.mod viewer (Go). Defaults to latest on first load.
  let focusedVersion = $state<string | null>(null);
  let deletingRef = $state<string | null>(null);

  async function loadDetail() {
    loading = true;
    loadError = null;
    detail = null;
    readme = null;
    goModSource = null;
    goModVersion = null;
    try {
      detail = await registryAPI.getPackageDetail(repoType, namespace, repoName, packageName);
      // Pick a sensible default focus version.
      if (detail) {
        if (detail.go?.latest_version) {
          focusedVersion = detail.go.latest_version;
        } else if (detail.npm?.latest_version) {
          focusedVersion = detail.npm.latest_version;
        } else if (detail.docker?.tags?.length) {
          focusedVersion = detail.docker.tags[0].tag;
        } else if (detail.maven?.latest_version) {
          focusedVersion = detail.maven.latest_version;
        } else if (detail.pypi?.latest_version) {
          focusedVersion = detail.pypi.latest_version;
        } else if (detail.cargo?.latest_version) {
          focusedVersion = detail.cargo.latest_version;
        }
      }
    } catch (err) {
      loadError = String(err);
    } finally {
      loading = false;
    }
  }

  async function loadReadme() {
    if (readme !== null) return; // already loaded
    readmeLoading = true;
    readmeError = null;
    try {
      readme = await registryAPI.getNPMReadme(namespace, repoName, packageName);
    } catch (err) {
      // The READMEs are best-effort — 404 means the package
      // doesn't ship one, treat it as a non-error empty state.
      if (err instanceof registryAPI.RegistryAPIError && err.status === 404) {
        readme = '';
      } else {
        readmeError = String(err);
      }
    } finally {
      readmeLoading = false;
    }
  }

  async function loadGoMod(version: string) {
    if (goModVersion === version && goModSource !== null) return;
    goModLoading = true;
    goModVersion = version;
    try {
      goModSource = await registryAPI.getGoMod(namespace, repoName, packageName, version);
    } catch (err) {
      if (err instanceof registryAPI.RegistryAPIError) {
        goModSource = `// failed to fetch go.mod: HTTP ${err.status}`;
      } else {
        goModSource = `// error: ${err}`;
      }
    } finally {
      goModLoading = false;
    }
  }

  $effect(() => {
    // Reload whenever the selection changes. The deps reference
    // ensures the effect re-runs across rapid clicks in the list.
    packageName;
    repoName;
    namespace;
    repoType;
    activeTab = 'overview';
    loadDetail();
  });

  $effect(() => {
    // Lazy-load README the first time the user opens that tab.
    if (activeTab === 'readme' && detail?.type === 'npm') {
      loadReadme();
    }
    if (activeTab === 'install' && detail?.type === 'go' && focusedVersion) {
      loadGoMod(focusedVersion);
    }
  });

  async function copyToClipboard(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      addToast('Copied to clipboard', 'success');
    } catch {
      addToast('Clipboard write failed', 'alert');
    }
  }

  async function deleteFocusedArtifact(ref: string) {
    if (!detail || !canDelete) return;
    const subject = detail.type === 'docker' ? `${detail.name}:${ref}` : `${detail.name}@${ref}`;
    if (!window.confirm(`Delete ${subject} from ${namespace}/${repoName}?\n\nThis removes the registry artifact reference and cannot be undone.`)) {
      return;
    }
    deletingRef = ref;
    try {
      const selector = detail.type === 'docker' ? { tag: ref } : { version: ref };
      await registryAPI.deleteRegistryArtifact(detail.type, namespace, repoName, detail.name, selector);
      addToast(`Deleted ${subject}`, 'success');
      await loadDetail();
      await ondeleted?.();
    } catch (err) {
      addToast(`Delete failed: ${err}`, 'alert');
    } finally {
      deletingRef = null;
    }
  }

  // Per-protocol install snippet for the currently focused version.
  function installSnippet(): string {
    if (!detail) return '';
    const ver = focusedVersion ?? '';
    switch (detail.type) {
      case 'npm':
        return ver ? `npm install ${detail.name}@${ver}` : `npm install ${detail.name}`;
      case 'go':
        return ver ? `go get ${detail.name}@${ver}` : `go get ${detail.name}`;
      case 'docker': {
        const host = endpoint.replace(/^https?:\/\//, '');
        return `docker pull ${host}/v2/${detail.name}:${ver || 'latest'}`;
      }
      case 'helm':
        return ver
          ? `helm install ${detail.name} oci://${endpoint.replace(/^https?:\/\//, '')}/${detail.name} --version ${ver}`
          : `helm install ${detail.name} oci://${endpoint.replace(/^https?:\/\//, '')}/${detail.name}`;
      case 'maven': {
        const [groupId, artifactId] = detail.name.split(':');
        return `<dependency>\n  <groupId>${groupId ?? ''}</groupId>\n  <artifactId>${artifactId ?? detail.name}</artifactId>\n  <version>${ver || 'VERSION'}</version>\n</dependency>`;
      }
      case 'pypi':
        return ver
          ? `pip install --index-url ${endpoint}/simple/ ${detail.name}==${ver}`
          : `pip install --index-url ${endpoint}/simple/ ${detail.name}`;
      case 'cargo':
        return `# .cargo/config.toml\n[registries.${repoName}]\nindex = "sparse+${endpoint}/"\n\n# Cargo.toml\n${detail.name} = { version = "${ver || 'VERSION'}", registry = "${repoName}" }`;
    }
    return '';
  }

  // Determine which tabs apply to this protocol.
  const availableTabs = $derived<Tab[]>((() => {
    if (!detail) return ['overview'];
    const base: Tab[] = ['overview', 'versions', 'install'];
    if (detail.type === 'npm' && detail.npm?.has_readme) base.push('readme');
    if (detail.type === 'helm' && detail.helm?.has_readme) base.push('readme');
    if (detail.type === 'docker') base.push('layers');
    return base;
  })());

  function tabLabel(t: Tab): string {
    switch (t) {
      case 'overview': return 'Overview';
      case 'versions': return 'Versions';
      case 'readme': return 'README';
      case 'install': return 'Install';
      case 'layers': return 'Layers';
    }
  }

  function tabIcon(t: Tab) {
    switch (t) {
      case 'overview': return Box;
      case 'versions': return Tag;
      case 'readme': return FileText;
      case 'install': return FileCode;
      case 'layers': return Layers;
    }
  }

  // Focused tag detail for the layers tab (Docker only).
  const focusedTag = $derived(
    detail?.docker?.tags?.find((t) => t.tag === focusedVersion) ?? null
  );

  // Wire Escape to close — keydown lives on the window so the user
  // doesn't have to click into the drawer first. Svelte 5 effect
  // cleanup runs on unmount.
  $effect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onclose();
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });
</script>

<!--
  Backdrop + right-side drawer. Clicking the backdrop closes the
  panel; pressing Escape also closes (wired via keydown handler).
-->
<div
  class="fixed inset-0 z-40 bg-black/40 backdrop-blur-sm"
  onclick={onclose}
  role="presentation"
></div>
<aside
  class="fixed right-0 top-0 bottom-0 z-50 w-full max-w-3xl bg-white dark:bg-warm-950 border-l border-warm-200 dark:border-warm-800 shadow-2xl flex flex-col"
  aria-label="Package detail"
>
  <!-- Header -->
  <header class="px-4 py-3 border-b border-warm-200 dark:border-warm-800 flex items-center gap-2">
    <Package size={18} class="text-accent-500" />
    <div class="flex-1 min-w-0">
      <div class="text-sm font-mono truncate">{packageName}</div>
      <div class="text-[10px] uppercase text-warm-500">
        {repoType} · {namespace}/{repoName}
      </div>
    </div>
    <button
      class="p-1.5 rounded hover:bg-warm-100 dark:hover:bg-warm-800"
      onclick={onclose}
      title="Close"
      aria-label="Close detail panel"
    >
      <X size={16} />
    </button>
  </header>

  <!-- Tabs -->
  {#if detail && !loading}
    <nav class="flex border-b border-warm-200 dark:border-warm-800 bg-warm-50 dark:bg-warm-900">
      {#each availableTabs as t}
        {@const Icon = tabIcon(t)}
        <button
          class="px-3 py-2 text-xs flex items-center gap-1.5 border-b-2 transition-colors {activeTab === t ? 'border-accent-500 text-accent-500' : 'border-transparent text-warm-600 dark:text-warm-400 hover:text-warm-900 dark:hover:text-warm-100'}"
          onclick={() => activeTab = t}
        >
          <Icon size={12} />
          {tabLabel(t)}
        </button>
      {/each}
    </nav>
  {/if}

  <!-- Body -->
  <div class="flex-1 overflow-y-auto">
    {#if loading}
      <div class="flex items-center justify-center h-32 text-warm-500">
        <Loader2 size={20} class="animate-spin" />
      </div>
    {:else if loadError}
      <div class="p-4 text-sm text-red-500 flex items-start gap-2">
        <AlertTriangle size={16} class="mt-0.5 shrink-0" />
        <div>
          <div class="font-semibold">Failed to load package detail</div>
          <div class="text-xs mt-1 font-mono">{loadError}</div>
        </div>
      </div>
    {:else if detail}
      {#if activeTab === 'overview'}
        <!-- ── Overview ── -->
        <div class="p-4 space-y-4">
          {#if detail.type === 'npm' && detail.npm}
            {@const n = detail.npm}
            {#if n.description}
              <p class="text-sm text-warm-700 dark:text-warm-300">{n.description}</p>
            {/if}
            <dl class="grid grid-cols-2 gap-3 text-xs">
              {#if n.latest_version}
                <div>
                  <dt class="text-warm-500 uppercase text-[10px]">Latest</dt>
                  <dd class="font-mono">{n.latest_version}</dd>
                </div>
              {/if}
              {#if n.license}
                <div>
                  <dt class="text-warm-500 uppercase text-[10px]">License</dt>
                  <dd class="font-mono">{n.license}</dd>
                </div>
              {/if}
              {#if n.homepage}
                <div class="col-span-2">
                  <dt class="text-warm-500 uppercase text-[10px]">Homepage</dt>
                  <dd><a href={n.homepage} class="font-mono text-accent-500 hover:underline" target="_blank" rel="noopener">{n.homepage}</a></dd>
                </div>
              {/if}
              {#if n.repository?.url}
                <div class="col-span-2">
                  <dt class="text-warm-500 uppercase text-[10px]">Repository</dt>
                  <dd class="font-mono text-xs break-all">{n.repository.url}</dd>
                </div>
              {/if}
              {#if n.bugs?.url}
                <div class="col-span-2">
                  <dt class="text-warm-500 uppercase text-[10px]">Bugs</dt>
                  <dd class="font-mono text-xs break-all">{n.bugs.url}</dd>
                </div>
              {/if}
            </dl>
            {#if n.keywords?.length}
              <div>
                <div class="text-warm-500 uppercase text-[10px] mb-1">Keywords</div>
                <div class="flex flex-wrap gap-1">
                  {#each n.keywords as kw}
                    <span class="text-[10px] px-1.5 py-0.5 rounded bg-warm-100 dark:bg-warm-800 font-mono">{kw}</span>
                  {/each}
                </div>
              </div>
            {/if}
            {#if n.dist_tags && Object.keys(n.dist_tags).length > 0}
              <div>
                <div class="text-warm-500 uppercase text-[10px] mb-1">Dist-tags</div>
                <div class="space-y-0.5">
                  {#each Object.entries(n.dist_tags) as [tag, ver]}
                    <div class="text-xs font-mono">{tag}: <span class="text-accent-500">{ver}</span></div>
                  {/each}
                </div>
              </div>
            {/if}
          {:else if detail.type === 'go' && detail.go}
            <dl class="grid grid-cols-2 gap-3 text-xs">
              <div>
                <dt class="text-warm-500 uppercase text-[10px]">Latest</dt>
                <dd class="font-mono">{detail.go.latest_version}</dd>
              </div>
              <div>
                <dt class="text-warm-500 uppercase text-[10px]">Versions</dt>
                <dd class="font-mono">{detail.go.versions?.length ?? 0}</dd>
              </div>
            </dl>
          {:else if detail.type === 'docker' && detail.docker}
            <dl class="text-xs">
              <div>
                <dt class="text-warm-500 uppercase text-[10px]">Tags</dt>
                <dd class="font-mono">{detail.docker.tags?.length ?? 0}</dd>
              </div>
            </dl>
          {:else if detail.type === 'helm' && detail.helm}
            {@const h = detail.helm}
            {#if h.description}
              <p class="text-sm text-warm-700 dark:text-warm-300">{h.description}</p>
            {/if}
            <dl class="grid grid-cols-2 gap-3 text-xs">
              {#if h.latest_version}
                <div>
                  <dt class="text-warm-500 uppercase text-[10px]">Latest</dt>
                  <dd class="font-mono">{h.latest_version}</dd>
                </div>
              {/if}
              {#if h.app_version}
                <div>
                  <dt class="text-warm-500 uppercase text-[10px]">App version</dt>
                  <dd class="font-mono">{h.app_version}</dd>
                </div>
              {/if}
            </dl>
            {#if h.keywords?.length}
              <div>
                <div class="text-warm-500 uppercase text-[10px] mb-1">Keywords</div>
                <div class="flex flex-wrap gap-1">
                  {#each h.keywords as kw}
                    <span class="text-[10px] px-1.5 py-0.5 rounded bg-warm-100 dark:bg-warm-800 font-mono">{kw}</span>
                  {/each}
                </div>
              </div>
            {/if}
          {:else if detail.type === 'maven' && detail.maven}
            <dl class="grid grid-cols-2 gap-3 text-xs">
              <div>
                <dt class="text-warm-500 uppercase text-[10px]">Group</dt>
                <dd class="font-mono">{detail.maven.group_id}</dd>
              </div>
              <div>
                <dt class="text-warm-500 uppercase text-[10px]">Artifact</dt>
                <dd class="font-mono">{detail.maven.artifact_id}</dd>
              </div>
              <div>
                <dt class="text-warm-500 uppercase text-[10px]">Latest</dt>
                <dd class="font-mono">{detail.maven.latest_version}</dd>
              </div>
              <div>
                <dt class="text-warm-500 uppercase text-[10px]">Versions</dt>
                <dd class="font-mono">{detail.maven.versions?.length ?? 0}</dd>
              </div>
            </dl>
          {:else if detail.type === 'pypi' && detail.pypi}
            <dl class="grid grid-cols-2 gap-3 text-xs">
              <div>
                <dt class="text-warm-500 uppercase text-[10px]">Latest</dt>
                <dd class="font-mono">{detail.pypi.latest_version}</dd>
              </div>
              <div>
                <dt class="text-warm-500 uppercase text-[10px]">Versions</dt>
                <dd class="font-mono">{detail.pypi.versions?.length ?? 0}</dd>
              </div>
            </dl>
          {:else if detail.type === 'cargo' && detail.cargo}
            <dl class="grid grid-cols-2 gap-3 text-xs">
              <div>
                <dt class="text-warm-500 uppercase text-[10px]">Latest</dt>
                <dd class="font-mono">{detail.cargo.latest_version}</dd>
              </div>
              <div>
                <dt class="text-warm-500 uppercase text-[10px]">Versions</dt>
                <dd class="font-mono">{detail.cargo.versions?.length ?? 0}</dd>
              </div>
            </dl>
          {/if}
        </div>
      {:else if activeTab === 'versions'}
        <!-- ── Versions table ── -->
        <div class="p-4">
          {#if detail.type === 'npm' && detail.npm?.versions}
            <table class="w-full text-xs">
              <thead class="text-left text-[10px] uppercase text-warm-500 border-b border-warm-200 dark:border-warm-800">
                <tr>
                  <th class="py-1.5 pr-2">Version</th>
                  <th class="py-1.5 pr-2">Published</th>
                  <th class="py-1.5 pr-2">Size</th>
                  <th class="py-1.5">Status</th>
                  {#if canDelete}<th class="py-1.5 text-right">Actions</th>{/if}
                </tr>
              </thead>
              <tbody>
                {#each detail.npm.versions as v}
                  <tr class="border-b border-warm-100 dark:border-warm-800/50">
                    <td class="py-1.5 pr-2 font-mono">
                      <button
                        class="hover:text-accent-500 {focusedVersion === v.version ? 'text-accent-500' : ''}"
                        onclick={() => focusedVersion = v.version}
                      >{v.version}</button>
                    </td>
                    <td class="py-1.5 pr-2 text-warm-500">{formatPublishedAt(v.published_at)}</td>
                    <td class="py-1.5 pr-2 text-warm-500">{formatSize(v.size)}</td>
                    <td class="py-1.5">
                      {#if v.deprecated}
                        <span class="text-[10px] px-1 py-0.5 rounded bg-red-500/10 text-red-500 line-through" title={v.deprecated}>
                          deprecated
                        </span>
                      {/if}
                    </td>
                    {#if canDelete}
                      <td class="py-1.5 text-right">
                        <button class="p-1 rounded hover:bg-red-100 dark:hover:bg-red-900/40 text-red-600 dark:text-red-400 disabled:opacity-50" title="Delete version" aria-label="Delete version" disabled={deletingRef === v.version} onclick={() => deleteFocusedArtifact(v.version)}>
                          {#if deletingRef === v.version}<Loader2 size={12} class="animate-spin" />{:else}<Trash2 size={12} />{/if}
                        </button>
                      </td>
                    {/if}
                  </tr>
                {/each}
              </tbody>
            </table>
          {:else if detail.type === 'go' && detail.go?.versions}
            <table class="w-full text-xs">
              <thead class="text-left text-[10px] uppercase text-warm-500 border-b border-warm-200 dark:border-warm-800">
                <tr>
                  <th class="py-1.5 pr-2">Version</th>
                  <th class="py-1.5 pr-2">Published</th>
                  <th class="py-1.5 pr-2">go.mod</th>
                  <th class="py-1.5 pr-2">.zip</th>
                  <th class="py-1.5">Status</th>
                  {#if canDelete}<th class="py-1.5 text-right">Actions</th>{/if}
                </tr>
              </thead>
              <tbody>
                {#each detail.go.versions as v}
                  <tr class="border-b border-warm-100 dark:border-warm-800/50">
                    <td class="py-1.5 pr-2 font-mono {v.retracted ? 'line-through text-warm-500' : ''}">
                      <button
                        class="hover:text-accent-500 {focusedVersion === v.version ? 'text-accent-500' : ''}"
                        onclick={() => focusedVersion = v.version}
                      >{v.version}</button>
                    </td>
                    <td class="py-1.5 pr-2 text-warm-500">{formatPublishedAt(v.published_at)}</td>
                    <td class="py-1.5 pr-2 text-warm-500">{formatSize(v.gomod_size)}</td>
                    <td class="py-1.5 pr-2 text-warm-500">{formatSize(v.zip_size)}</td>
                    <td class="py-1.5">
                      {#if v.retracted}
                        <span class="text-[10px] px-1 py-0.5 rounded bg-red-500/10 text-red-500" title={v.retraction_rationale || 'retracted'}>
                          retracted
                        </span>
                      {/if}
                    </td>
                    {#if canDelete}
                      <td class="py-1.5 text-right">
                        <button class="p-1 rounded hover:bg-red-100 dark:hover:bg-red-900/40 text-red-600 dark:text-red-400 disabled:opacity-50" title="Delete version" aria-label="Delete version" disabled={deletingRef === v.version} onclick={() => deleteFocusedArtifact(v.version)}>
                          {#if deletingRef === v.version}<Loader2 size={12} class="animate-spin" />{:else}<Trash2 size={12} />{/if}
                        </button>
                      </td>
                    {/if}
                  </tr>
                {/each}
              </tbody>
            </table>
          {:else if detail.type === 'docker' && detail.docker?.tags}
            <table class="w-full text-xs">
              <thead class="text-left text-[10px] uppercase text-warm-500 border-b border-warm-200 dark:border-warm-800">
                <tr>
                  <th class="py-1.5 pr-2">Tag</th>
                  <th class="py-1.5 pr-2">Image size</th>
                  <th class="py-1.5 pr-2">Layers</th>
                  <th class="py-1.5">Digest</th>
                  {#if canDelete}<th class="py-1.5 text-right">Actions</th>{/if}
                </tr>
              </thead>
              <tbody>
                {#each detail.docker.tags as t}
                  <tr class="border-b border-warm-100 dark:border-warm-800/50">
                    <td class="py-1.5 pr-2 font-mono">
                      <button
                        class="hover:text-accent-500 {focusedVersion === t.tag ? 'text-accent-500' : ''}"
                        onclick={() => focusedVersion = t.tag}
                      >{t.tag}</button>
                    </td>
                    <td class="py-1.5 pr-2 text-warm-500">{formatSize(t.image_size)}</td>
                    <td class="py-1.5 pr-2 text-warm-500">{t.layers?.length ?? 0}</td>
                    <td class="py-1.5 font-mono text-[10px] text-warm-500 truncate max-w-xs">{t.digest ?? ''}</td>
                    {#if canDelete}
                      <td class="py-1.5 text-right">
                        <button class="p-1 rounded hover:bg-red-100 dark:hover:bg-red-900/40 text-red-600 dark:text-red-400 disabled:opacity-50" title="Delete tag" aria-label="Delete tag" disabled={deletingRef === t.tag} onclick={() => deleteFocusedArtifact(t.tag)}>
                          {#if deletingRef === t.tag}<Loader2 size={12} class="animate-spin" />{:else}<Trash2 size={12} />{/if}
                        </button>
                      </td>
                    {/if}
                  </tr>
                {/each}
              </tbody>
            </table>
          {:else if detail.type === 'helm' && detail.helm?.versions}
            <table class="w-full text-xs">
              <thead class="text-left text-[10px] uppercase text-warm-500 border-b border-warm-200 dark:border-warm-800">
                <tr>
                  <th class="py-1.5 pr-2">Version</th>
                  <th class="py-1.5 pr-2">App version</th>
                  <th class="py-1.5 pr-2">Created</th>
                  <th class="py-1.5">Size</th>
                  {#if canDelete}<th class="py-1.5 text-right">Actions</th>{/if}
                </tr>
              </thead>
              <tbody>
                {#each detail.helm.versions as v}
                  <tr class="border-b border-warm-100 dark:border-warm-800/50">
                    <td class="py-1.5 pr-2 font-mono">
                      <button
                        class="hover:text-accent-500 {focusedVersion === v.version ? 'text-accent-500' : ''}"
                        onclick={() => focusedVersion = v.version}
                      >{v.version}</button>
                    </td>
                    <td class="py-1.5 pr-2 text-warm-500">{v.app_version ?? ''}</td>
                    <td class="py-1.5 pr-2 text-warm-500">{formatPublishedAt(v.created)}</td>
                    <td class="py-1.5 text-warm-500">{formatSize(v.size)}</td>
                    {#if canDelete}
                      <td class="py-1.5 text-right">
                        <button class="p-1 rounded hover:bg-red-100 dark:hover:bg-red-900/40 text-red-600 dark:text-red-400 disabled:opacity-50" title="Delete version" aria-label="Delete version" disabled={deletingRef === v.version} onclick={() => deleteFocusedArtifact(v.version)}>
                          {#if deletingRef === v.version}<Loader2 size={12} class="animate-spin" />{:else}<Trash2 size={12} />{/if}
                        </button>
                      </td>
                    {/if}
                  </tr>
                {/each}
              </tbody>
            </table>
          {:else if detail.type === 'maven' && detail.maven?.versions}
            <table class="w-full text-xs">
              <thead class="text-left text-[10px] uppercase text-warm-500 border-b border-warm-200 dark:border-warm-800">
                <tr>
                  <th class="py-1.5 pr-2">Version</th>
                  <th class="py-1.5 pr-2">JAR</th>
                  <th class="py-1.5">POM</th>
                  {#if canDelete}<th class="py-1.5 text-right">Actions</th>{/if}
                </tr>
              </thead>
              <tbody>
                {#each detail.maven.versions as v}
                  <tr class="border-b border-warm-100 dark:border-warm-800/50">
                    <td class="py-1.5 pr-2 font-mono">
                      <button class="hover:text-accent-500 {focusedVersion === v.version ? 'text-accent-500' : ''}" onclick={() => focusedVersion = v.version}>{v.version}</button>
                    </td>
                    <td class="py-1.5 pr-2 text-warm-500">{formatSize(v.jar_size)}</td>
                    <td class="py-1.5 text-warm-500">{formatSize(v.pom_size)}</td>
                    {#if canDelete}
                      <td class="py-1.5 text-right">
                        <button class="p-1 rounded hover:bg-red-100 dark:hover:bg-red-900/40 text-red-600 dark:text-red-400 disabled:opacity-50" title="Delete version" aria-label="Delete version" disabled={deletingRef === v.version} onclick={() => deleteFocusedArtifact(v.version)}>
                          {#if deletingRef === v.version}<Loader2 size={12} class="animate-spin" />{:else}<Trash2 size={12} />{/if}
                        </button>
                      </td>
                    {/if}
                  </tr>
                {/each}
              </tbody>
            </table>
          {:else if detail.type === 'pypi' && detail.pypi?.versions}
            <table class="w-full text-xs">
              <thead class="text-left text-[10px] uppercase text-warm-500 border-b border-warm-200 dark:border-warm-800">
                <tr>
                  <th class="py-1.5 pr-2">Version</th>
                  <th class="py-1.5 pr-2">Files</th>
                  <th class="py-1.5">Size</th>
                  {#if canDelete}<th class="py-1.5 text-right">Actions</th>{/if}
                </tr>
              </thead>
              <tbody>
                {#each detail.pypi.versions as v}
                  <tr class="border-b border-warm-100 dark:border-warm-800/50">
                    <td class="py-1.5 pr-2 font-mono">
                      <button class="hover:text-accent-500 {focusedVersion === v.version ? 'text-accent-500' : ''}" onclick={() => focusedVersion = v.version}>{v.version}</button>
                    </td>
                    <td class="py-1.5 pr-2 text-warm-500">{v.files?.length ?? 0}</td>
                    <td class="py-1.5 text-warm-500">{formatSize(v.file_size)}</td>
                    {#if canDelete}
                      <td class="py-1.5 text-right">
                        <button class="p-1 rounded hover:bg-red-100 dark:hover:bg-red-900/40 text-red-600 dark:text-red-400 disabled:opacity-50" title="Delete version files" aria-label="Delete version files" disabled={deletingRef === v.version} onclick={() => deleteFocusedArtifact(v.version)}>
                          {#if deletingRef === v.version}<Loader2 size={12} class="animate-spin" />{:else}<Trash2 size={12} />{/if}
                        </button>
                      </td>
                    {/if}
                  </tr>
                {/each}
              </tbody>
            </table>
          {:else if detail.type === 'cargo' && detail.cargo?.versions}
            <table class="w-full text-xs">
              <thead class="text-left text-[10px] uppercase text-warm-500 border-b border-warm-200 dark:border-warm-800">
                <tr>
                  <th class="py-1.5 pr-2">Version</th>
                  <th class="py-1.5 pr-2">Size</th>
                  <th class="py-1.5">Status</th>
                  {#if canDelete}<th class="py-1.5 text-right">Actions</th>{/if}
                </tr>
              </thead>
              <tbody>
                {#each detail.cargo.versions as v}
                  <tr class="border-b border-warm-100 dark:border-warm-800/50">
                    <td class="py-1.5 pr-2 font-mono">
                      <button class="hover:text-accent-500 {focusedVersion === v.version ? 'text-accent-500' : ''}" onclick={() => focusedVersion = v.version}>{v.version}</button>
                    </td>
                    <td class="py-1.5 pr-2 text-warm-500">{formatSize(v.size)}</td>
                    <td class="py-1.5">
                      {#if v.yanked}<span class="text-[10px] px-1 py-0.5 rounded bg-red-500/10 text-red-500">yanked</span>{/if}
                    </td>
                    {#if canDelete}
                      <td class="py-1.5 text-right">
                        <button class="p-1 rounded hover:bg-red-100 dark:hover:bg-red-900/40 text-red-600 dark:text-red-400 disabled:opacity-50" title="Delete version" aria-label="Delete version" disabled={deletingRef === v.version} onclick={() => deleteFocusedArtifact(v.version)}>
                          {#if deletingRef === v.version}<Loader2 size={12} class="animate-spin" />{:else}<Trash2 size={12} />{/if}
                        </button>
                      </td>
                    {/if}
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        </div>
      {:else if activeTab === 'install'}
        <!-- ── Install snippet + per-version metadata ── -->
        <div class="p-4 space-y-4">
          <div class="flex items-center justify-between">
            <div class="text-xs text-warm-500">
              Selected version: <span class="font-mono text-warm-900 dark:text-warm-100">{focusedVersion ?? 'none'}</span>
            </div>
          </div>
          <div class="border border-warm-200 dark:border-warm-800 rounded bg-warm-50 dark:bg-warm-900">
            <div class="flex items-center justify-between px-3 py-2 border-b border-warm-200 dark:border-warm-800">
              <span class="text-[10px] uppercase text-warm-500">Install command</span>
              <button
                class="text-[10px] px-2 py-0.5 rounded bg-warm-200 dark:bg-warm-800 hover:bg-warm-300 dark:hover:bg-warm-700 flex items-center gap-1"
                onclick={() => copyToClipboard(installSnippet())}
              >
                <Copy size={10} /> Copy
              </button>
            </div>
            <pre class="px-3 py-2 text-xs font-mono whitespace-pre-wrap break-all">{installSnippet()}</pre>
          </div>

          {#if detail.type === 'npm' && focusedVersion}
            {@const v = detail.npm?.versions?.find((x: NPMVersionDetail) => x.version === focusedVersion)}
            {#if v}
              {#if v.dependencies && Object.keys(v.dependencies).length > 0}
                <div>
                  <div class="text-[10px] uppercase text-warm-500 mb-1">Dependencies</div>
                  <div class="font-mono text-xs space-y-0.5">
                    {#each Object.entries(v.dependencies) as [name, spec]}
                      <div>{name}: <span class="text-warm-500">{spec}</span></div>
                    {/each}
                  </div>
                </div>
              {/if}
              {#if v.dev_dependencies && Object.keys(v.dev_dependencies).length > 0}
                <div>
                  <div class="text-[10px] uppercase text-warm-500 mb-1">Dev dependencies</div>
                  <div class="font-mono text-xs space-y-0.5">
                    {#each Object.entries(v.dev_dependencies) as [name, spec]}
                      <div>{name}: <span class="text-warm-500">{spec}</span></div>
                    {/each}
                  </div>
                </div>
              {/if}
              {#if v.peer_dependencies && Object.keys(v.peer_dependencies).length > 0}
                <div>
                  <div class="text-[10px] uppercase text-warm-500 mb-1">Peer dependencies</div>
                  <div class="font-mono text-xs space-y-0.5">
                    {#each Object.entries(v.peer_dependencies) as [name, spec]}
                      <div>{name}: <span class="text-warm-500">{spec}</span></div>
                    {/each}
                  </div>
                </div>
              {/if}
              {#if v.integrity}
                <div>
                  <div class="text-[10px] uppercase text-warm-500 mb-1">Integrity</div>
                  <div class="font-mono text-[10px] break-all text-warm-700 dark:text-warm-300">{v.integrity}</div>
                </div>
              {/if}
            {/if}
          {:else if detail.type === 'go' && focusedVersion}
            <div class="border border-warm-200 dark:border-warm-800 rounded bg-warm-50 dark:bg-warm-900">
              <div class="px-3 py-2 border-b border-warm-200 dark:border-warm-800 text-[10px] uppercase text-warm-500">
                go.mod
              </div>
              {#if goModLoading}
                <div class="px-3 py-4 flex items-center justify-center text-warm-500">
                  <Loader2 size={14} class="animate-spin" />
                </div>
              {:else if goModSource !== null}
                <pre class="px-3 py-2 text-[11px] font-mono whitespace-pre-wrap break-all max-h-64 overflow-y-auto">{goModSource}</pre>
              {/if}
            </div>
          {/if}
        </div>
      {:else if activeTab === 'readme'}
        <!-- ── README markdown ── -->
        <div class="p-4">
          {#if readmeLoading}
            <div class="flex items-center justify-center py-8 text-warm-500">
              <Loader2 size={20} class="animate-spin" />
            </div>
          {:else if readmeError}
            <div class="text-sm text-red-500">Failed to load README: {readmeError}</div>
          {:else if readme === ''}
            <div class="text-sm text-warm-500 text-center py-8">No README provided.</div>
          {:else if readme !== null}
            <div class="prose prose-sm dark:prose-invert max-w-none">
              <Comark markdown={readme} />
            </div>
          {/if}
        </div>
      {:else if activeTab === 'layers' && detail.type === 'docker'}
        <!-- ── Docker layer breakdown ── -->
        <div class="p-4 space-y-3">
          {#if focusedTag}
            <div class="text-xs">
              <span class="text-warm-500">Tag:</span>
              <span class="font-mono">{focusedTag.tag}</span>
              {#if focusedTag.artifact_type}
                <span class="ml-2 text-[10px] px-1 py-0.5 rounded bg-accent-500/10 text-accent-500">
                  {focusedTag.artifact_type}
                </span>
              {/if}
            </div>
            {#if focusedTag.config_digest}
              <div class="text-[10px] text-warm-500">
                Config: <span class="font-mono">{focusedTag.config_digest}</span>
              </div>
            {/if}
            {#if focusedTag.platforms && focusedTag.platforms.length > 0}
              <div>
                <div class="text-[10px] uppercase text-warm-500 mb-1">Platforms (multi-arch)</div>
                <table class="w-full text-[11px]">
                  <thead class="text-left text-warm-500">
                    <tr>
                      <th class="pr-2 py-0.5">OS/Arch</th>
                      <th class="pr-2 py-0.5">Digest</th>
                      <th class="py-0.5">Size</th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each focusedTag.platforms as p}
                      <tr class="border-t border-warm-100 dark:border-warm-800/50">
                        <td class="pr-2 py-1 font-mono">{p.os}/{p.architecture}{p.variant ? '/' + p.variant : ''}</td>
                        <td class="pr-2 py-1 font-mono text-[10px] text-warm-500 truncate max-w-[200px]">{p.digest}</td>
                        <td class="py-1 text-warm-500">{formatSize(p.size)}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}
            {#if focusedTag.layers && focusedTag.layers.length > 0}
              <div>
                <div class="text-[10px] uppercase text-warm-500 mb-1">Layers</div>
                <table class="w-full text-[11px]">
                  <thead class="text-left text-warm-500">
                    <tr>
                      <th class="pr-2 py-0.5">Digest</th>
                      <th class="pr-2 py-0.5">Size</th>
                      <th class="py-0.5">Media type</th>
                    </tr>
                  </thead>
                  <tbody>
                    {#each focusedTag.layers as l}
                      <tr class="border-t border-warm-100 dark:border-warm-800/50">
                        <td class="pr-2 py-1 font-mono text-[10px] text-warm-700 dark:text-warm-300 truncate max-w-[260px]">{l.digest}</td>
                        <td class="pr-2 py-1 text-warm-500">{formatSize(l.size)}</td>
                        <td class="py-1 font-mono text-[10px] text-warm-500 truncate max-w-[200px]">{l.media_type}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {:else if !focusedTag.platforms?.length}
              <div class="text-warm-500 text-xs">No layers reported.</div>
            {/if}
          {:else}
            <div class="text-warm-500 text-xs">Select a tag in the Versions tab.</div>
          {/if}
        </div>
      {/if}
    {/if}
  </div>
</aside>
