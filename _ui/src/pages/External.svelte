<script lang="ts">
  // External Resource Browser
  // ─────────────────────────────────────────────────────────────────
  // 3-pane layout for inspecting configured external backends without
  // leaving Pika:
  //   Left   : configured resource list (name + kind badge + caps).
  //   Middle : path tree of the selected resource (List endpoint).
  //   Right  : content viewer/editor for the selected path, with
  //            version history when the backend exposes one.
  //
  // Why this exists separately from Settings → External Resources:
  // the Settings section owns the *configuration* of resources
  // (add/edit/test/delete credentials). This page owns *use* — once
  // a Vault is configured, you should be able to read/write its
  // secrets here just like you'd inspect a file in the
  // Configurations page. The two surfaces never overlap.
  //
  // Capability gating is driven entirely by what the backend reports
  // via ExternalCapabilities. AWS/GCP/Azure report read+list only,
  // so this page hides Save and Delete for them automatically. Vault
  // KV v2 reports versions=true and gets a version selector strip.
  //
  // The server is locked? Reads of secret-bearing paths still work
  // (the seal layer only protects writes to settings.external, not
  // live backend traffic). The encryption-key banner from the prior
  // External page is no longer needed here because we don't write
  // settings — write-to-backend goes straight to Vault/etc.

  import { link } from "svelte-spa-router";
  import { configStore } from "@/lib/store/config.svelte";
  import { appStore } from "@/lib/store/store.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import ExternalValueEditor from "@/lib/components/external/ExternalValueEditor.svelte";
  import ResizablePanel from "@/lib/components/config/ResizablePanel.svelte";
  import {
    Globe,
    ShieldOff,
    Loader2,
    Search,
    Settings as SettingsIcon,
    Folder,
    FolderOpen,
    FileText,
    RefreshCw,
    Save,
    Trash2,
    History,
    Plus,
    X,
    ChevronRight,
    ChevronDown,
    AlertCircle,
  } from "lucide-svelte";
  import type {
    ExternalResourceSummary,
    ExternalEntry,
    ExternalVersion,
  } from "@/lib/types/config";
  import jsYaml from "js-yaml";

  // ── Page state ────────────────────────────────────────────────────
  let booted = $state(false);
  let resourcesLoadRequested = $state(false);
  let resources = $state<ExternalResourceSummary[]>([]);
  let resourceFilter = $state("");

  // Selected resource (left pane). All middle/right pane state is
  // scoped per-resource — switching resources clears everything below.
  let selectedResource = $state<string | null>(null);

  // Sidebar widths (session-only, mirroring the Configurations page).
  // Both panes are mouse-resizable via the ResizablePanel drag handle.
  // Defaults are intentionally compact — the resources list only needs
  // room for a name + kind badge, so it starts narrow and grows on demand.
  let resourcesWidth = $state(200);
  let pathsWidth = $state(260);

  const currentResource = $derived(
    selectedResource
      ? (resources.find((r) => r.name === selectedResource) ?? null)
      : null,
  );

  // Wrapper backends store a single opaque value per key (Consul,
  // etcd, HTTP, GCP Secret Manager fallback). Their Write contract
  // accepts `{value: "<string>"}` and stores the raw string verbatim.
  // For these we collapse the edit form down to ONE editor — a
  // key/value table here is pure cargo-culting, the key is always
  // "value" and the user has nothing to name. Vault and Kubernetes,
  // which natively store key/value maps, keep the multi-row editor.
  const wrapperKinds = new Set(["consul", "etcd", "http", "gcp"]);
  const isWrapperBackend = $derived(
    currentResource ? wrapperKinds.has(currentResource.kind) : false,
  );

  // ── Path tree (middle pane) ───────────────────────────────────────
  // We hold a flat map of "prefix → children" so a deep tree can be
  // built lazily: List(prefix) is only called when a folder is
  // expanded. Trees per-backend vary in semantics (Vault folders end
  // with "/", K8s paths are exactly three segments), so we tolerate
  // both shapes.
  let pathTree = $state<Record<string, string[] | undefined>>({});
  let pathTreeLoading = $state<Record<string, boolean>>({});
  let expanded = $state<Record<string, boolean>>({ "": true });
  let selectedPath = $state<string | null>(null);
  let deepLinkHydrationStarted = $state(false);
  let deepLinkHydrated = $state(false);
  let applyingDeepLink = $state(false);

  // ── Content viewer (right pane) ───────────────────────────────────
  let entry = $state<ExternalEntry | null>(null);
  let entryLoading = $state(false);
  let entryError = $state<string | null>(null);

  // Edit state. Every backend now edits through ONE full-document
  // editor (`singleValueDraft`), regardless of kind:
  //   • Wrapper backends (Consul/etcd/HTTP/GCP) store the raw string
  //     verbatim under the synthetic `value` key.
  //   • Structured backends (Vault/Kubernetes) edit the WHOLE secret
  //     as a single JSON (or YAML) document — saveEntry() parses it
  //     back to the key/value map the backend stores. js-yaml reads
  //     JSON too, so a YAML edit is transparently converted to the
  //     JSON map Vault persists.
  let editing = $state(false);
  let saving = $state(false);
  let singleValueDraft = $state("");

  // Current syntax-validation error reported by the active editor (or
  // null). Bound from ExternalValueEditor; drives the save-time confirm
  // gate the way the Configurations editor lints before writing.
  let editorLintError = $state<string | null>(null);

  // Configurations-style save-time lint gate. For wrapper backends a
  // syntax error is advisory (the raw string is still storable), so the
  // first Save warns and a second Save within the timeout confirms.
  let pendingSaveConfirm = $state(false);
  let pendingSaveTimer: ReturnType<typeof setTimeout> | undefined;

  // ── Search state ─────────────────────────────────────────────────
  // Search runs against the currently selected resource. We keep the
  // input separate from the live "search has run" state so the user
  // can clear the input without immediately losing their results
  // (which can be slow to re-fetch in content mode). `searchMode`
  // mirrors the Configuration page's two-mode UI: 'name' is cheap
  // and instant, 'all' walks the tree and reads each leaf.
  let searchInput = $state("");
  let searchMode = $state<"name" | "all">("name");
  let searchResults = $state<
    Array<{ path: string; type: "name" | "content"; snippet?: string }>
  >([]);
  let searching = $state(false);
  let searchActive = $state(false);

  // Versions (only meaningful when capabilities.can_versions === true)
  let versions = $state<ExternalVersion[]>([]);
  let activeVersion = $state<string>(""); // '' = latest

  // New-entry inline composer
  let composing = $state(false);
  let newPath = $state("");

  // Delete confirmation
  let confirmDeletePath = $state<string | null>(null);
  let deleting = $state(false);

  // Browsing external resources requires external.read; writing entries
  // additionally requires external.write. The backend enforces both
  // independently; UI hides write affordances when canWrite is false.
  const canManage = $derived(appStore.hasPermission("external.read"));
  const canWrite = $derived(appStore.hasPermission("external.write"));

  // Resources filtered by the search box (matches name or kind).
  const visibleResources = $derived.by(() => {
    if (!resourceFilter.trim()) return resources;
    const q = resourceFilter.trim().toLowerCase();
    return resources.filter(
      (r) =>
        r.name.toLowerCase().includes(q) || r.kind.toLowerCase().includes(q),
    );
  });

  async function loadResourceList() {
    resources = await configStore.listExternalResourceSummaries();
    booted = true;
  }

  // Same boot race as other permission-gated pages: canManage can be
  // false until /api/v1/info finishes. Only set the one-shot loaded
  // flag after the permission is actually true and the fetch starts.
  $effect(() => {
    if (!canManage) {
      booted = true;
      return;
    }
    if (resourcesLoadRequested) return;
    resourcesLoadRequested = true;
    booted = false;
    void loadResourceList();
  });

  // ── Resource selection ────────────────────────────────────────────
  async function selectResource(name: string) {
    if (selectedResource === name) return;
    selectedResource = name;
    // Reset everything below.
    pathTree = {};
    pathTreeLoading = {};
    expanded = { "": true };
    selectedPath = null;
    entry = null;
    entryError = null;
    editing = false;
    singleValueDraft = "";
    pendingSaveConfirm = false;
    // Reset search so picking a new resource doesn't leave stale
    // results from the previous one in the panel.
    searchInput = "";
    searchResults = [];
    searchActive = false;
    versions = [];
    activeVersion = "";
    composing = false;
    newPath = "";
    confirmDeletePath = null;

    await loadChildren("");
  }

  function readDeepLink(): { resource: string | null; path: string | null } {
    if (typeof window === "undefined") return { resource: null, path: null };
    const queryStart = window.location.hash.indexOf("?");
    if (queryStart === -1) return { resource: null, path: null };
    const params = new URLSearchParams(window.location.hash.slice(queryStart + 1));
    return {
      resource: params.get("resource"),
      path: params.get("path"),
    };
  }

  function updateDeepLink() {
    if (typeof window === "undefined") return;
    const params = new URLSearchParams();
    if (selectedResource) params.set("resource", selectedResource);
    if (selectedResource && selectedPath) params.set("path", selectedPath);
    const nextHash = params.toString()
      ? `/external?${params.toString()}`
      : "/external";
    if (window.location.hash.slice(1) === nextHash) return;
    window.history.replaceState(
      null,
      "",
      `${window.location.pathname}${window.location.search}#${nextHash}`,
    );
  }

  async function expandAncestors(path: string) {
    const parts = path.split("/").filter(Boolean);
    let prefix = "";
    for (const part of parts.slice(0, -1)) {
      prefix += `${part}/`;
      expanded = { ...expanded, [prefix]: true };
      await loadChildren(prefix);
    }
  }

  async function hydrateDeepLink() {
    applyingDeepLink = true;
    try {
      const { resource, path } = readDeepLink();
      if (!resource || !resources.some((r) => r.name === resource)) return;
      await selectResource(resource);
      if (path) {
        await expandAncestors(path);
        await openPath(path);
      }
    } finally {
      applyingDeepLink = false;
      deepLinkHydrated = true;
    }
  }

  $effect(() => {
    if (!canManage || !booted || deepLinkHydrationStarted) return;
    deepLinkHydrationStarted = true;
    void hydrateDeepLink();
  });

  $effect(() => {
    if (!deepLinkHydrated || applyingDeepLink) return;
    updateDeepLink();
  });

  // Lazy-load children under a prefix. Cached in pathTree; concurrent
  // calls deduped by pathTreeLoading.
  async function loadChildren(prefix: string) {
    if (!selectedResource) return;
    if (pathTreeLoading[prefix]) return;
    if (pathTree[prefix] !== undefined) return;
    pathTreeLoading = { ...pathTreeLoading, [prefix]: true };
    try {
      const children = await configStore.listExternalPaths(
        selectedResource,
        prefix,
      );
      pathTree = { ...pathTree, [prefix]: sortChildren(prefix, children || []) };
    } finally {
      pathTreeLoading = { ...pathTreeLoading, [prefix]: false };
    }
  }

  // Order a prefix's children folders-first, then files, each group
  // sorted alphabetically (numeric-aware, case-insensitive). isFolder
  // needs the full path, so we recombine prefix + child for the test.
  function sortChildren(prefix: string, children: string[]): string[] {
    return [...children].sort((a, b) => {
      const aFolder = isFolder(prefix + a);
      const bFolder = isFolder(prefix + b);
      if (aFolder !== bFolder) return aFolder ? -1 : 1;
      return a.localeCompare(b, undefined, {
        numeric: true,
        sensitivity: "base",
      });
    });
  }

  // A node is a folder if its name ends with "/" (Vault/Consul/etcd
  // convention) OR if the backend is Kubernetes and the path has
  // fewer than 3 segments (Kubernetes uses namespace/type/name —
  // anything shorter is a folder).
  function isFolder(fullPath: string): boolean {
    if (fullPath.endsWith("/")) return true;
    if (currentResource?.kind === "kubernetes") {
      const parts = fullPath.split("/").filter(Boolean);
      return parts.length < 3;
    }
    return false;
  }

  async function toggleNode(fullPath: string) {
    if (isFolder(fullPath)) {
      const wasExpanded = expanded[fullPath];
      expanded = { ...expanded, [fullPath]: !wasExpanded };
      if (!wasExpanded) {
        await loadChildren(fullPath);
      }
    } else {
      await openPath(fullPath);
    }
  }

  // ── Content load ──────────────────────────────────────────────────
  async function openPath(path: string) {
    if (!selectedResource) return;
    selectedPath = path;
    editing = false;
    singleValueDraft = "";
    pendingSaveConfirm = false;
    entryError = null;
    activeVersion = "";
    versions = [];

    await Promise.all([loadEntry(path, ""), loadVersionsIfSupported(path)]);
  }

  async function loadEntry(path: string, version: string) {
    if (!selectedResource) return;
    entryLoading = true;
    entryError = null;
    try {
      let result: ExternalEntry;
      if (version) {
        result = await configStore.readExternalVersion(
          selectedResource,
          path,
          version,
        );
      } else {
        result = await configStore.readExternalEntry(selectedResource, path);
      }
      entry = result;
      activeVersion = version;
    } catch (err: any) {
      entry = null;
      entryError =
        err?.response?.data?.message || err?.message || "Failed to read entry";
    } finally {
      entryLoading = false;
    }
  }

  async function loadVersionsIfSupported(path: string) {
    if (!selectedResource || !currentResource?.capabilities.can_versions)
      return;
    versions = await configStore.listExternalVersions(selectedResource, path);
  }

  // ── Edit / Save ───────────────────────────────────────────────────
  function startEdit() {
    if (isWrapperBackend) {
      // Wrapper backends: there's only ever a single `value` field.
      // Seed the single-editor draft from it (or empty when the entry
      // hasn't loaded yet). Stringify objects so the editor shows
      // the JSON form the user can re-parse on save.
      const raw = entry?.data?.value;
      singleValueDraft =
        raw === undefined
          ? ""
          : typeof raw === "string"
            ? raw
            : JSON.stringify(raw, null, 2);
    } else {
      // Structured backend (Vault/K8s): edit the WHOLE secret as one
      // JSON document. This matches the read view (a single JSON pane)
      // and how the secret actually lives in the backend, instead of
      // fragmenting it into a row-per-key form.
      singleValueDraft = entry?.data ? JSON.stringify(entry.data, null, 2) : "{}";
    }
    pendingSaveConfirm = false;
    editing = true;
  }

  function cancelEdit() {
    editing = false;
    singleValueDraft = "";
    pendingSaveConfirm = false;
  }

  // Reset the pending save-confirm whenever the draft changes — any edit
  // invalidates a prior "press Save again" prompt (mirrors the
  // Configurations editor's handleChange).
  function onDraftChange(v: string) {
    singleValueDraft = v;
    if (pendingSaveConfirm) {
      pendingSaveConfirm = false;
      clearTimeout(pendingSaveTimer);
    }
  }

  // Build the write payload for the active backend, applying the
  // save-time lint gate. Returns the data object to write, or undefined
  // when the save must be aborted (a toast/confirm was already raised).
  // Shared by saveEntry() and createEntry() so both honour the same
  // validation contract the server expects (consul.go:275, vault.go).
  function buildSaveData(): Record<string, unknown> | undefined {
    if (isWrapperBackend) {
      // Wrapper backend: ship the raw string verbatim under "value".
      // A syntax error in the chosen format is advisory (the string is
      // still storable), so we warn once and let a second Save confirm —
      // the Configurations editor's "press Save again" behaviour.
      if (editorLintError && !pendingSaveConfirm) {
        pendingSaveConfirm = true;
        addToast(
          "Value has syntax errors — press Save again to confirm",
          "warn",
        );
        clearTimeout(pendingSaveTimer);
        pendingSaveTimer = setTimeout(() => {
          pendingSaveConfirm = false;
        }, 5000);
        return undefined;
      }
      pendingSaveConfirm = false;
      clearTimeout(pendingSaveTimer);
      return { value: singleValueDraft };
    }
    // Structured backend (Vault/K8s): the whole secret is one document.
    // Parse it back to an object — js-yaml reads JSON too, so a YAML edit
    // is transparently converted to the JSON map the backend stores.
    // Invalid input can't be serialized into a structured write, so it
    // hard-blocks here rather than falling through to a "save anyway".
    const parsed = parseStructuredDraft(singleValueDraft);
    if (parsed === null) {
      addToast("Invalid JSON/YAML — the secret must be an object", "alert");
      return undefined;
    }
    return parsed;
  }

  // Parse a structured-backend draft (JSON or YAML) into the key/value
  // map the backend stores. Returns null when the text doesn't parse to
  // a plain object (arrays and scalars aren't valid secret maps).
  function parseStructuredDraft(draft: string): Record<string, unknown> | null {
    const trimmed = draft.trim();
    if (!trimmed) return {};
    let parsed: unknown;
    try {
      parsed = jsYaml.load(trimmed);
    } catch {
      return null;
    }
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      return null;
    }
    return parsed as Record<string, unknown>;
  }

  async function saveEntry() {
    if (!selectedResource || !selectedPath) return;
    if (!canWrite) {
      addToast("Missing external.write permission", "alert");
      return;
    }
    const data = buildSaveData();
    if (data === undefined) return;

    saving = true;
    try {
      await configStore.writeExternalEntry(
        selectedResource,
        selectedPath,
        data,
      );
      addToast("Saved", "success");
      editing = false;
      // Re-read so we see exactly what the backend stored (e.g.
      // Vault might have minted a new version number).
      await Promise.all([
        loadEntry(selectedPath, ""),
        loadVersionsIfSupported(selectedPath),
      ]);
    } catch (err: any) {
      const msg = err?.response?.data?.message || err?.message || "Save failed";
      addToast(msg, "alert");
    } finally {
      saving = false;
    }
  }

  // ── Delete ────────────────────────────────────────────────────────
  async function performDelete(path: string) {
    if (!selectedResource) return;
    if (!canWrite) {
      addToast("Missing external.write permission", "alert");
      return;
    }
    deleting = true;
    try {
      await configStore.deleteExternalEntry(selectedResource, path);
      addToast("Deleted", "success");
      // Refresh parent prefix so the tree updates.
      const parent = path.includes("/")
        ? path.slice(0, path.lastIndexOf("/") + 1)
        : "";
      pathTree = { ...pathTree, [parent]: undefined };
      await loadChildren(parent);
      if (selectedPath === path) {
        selectedPath = null;
        entry = null;
      }
      confirmDeletePath = null;
    } catch (err: any) {
      const msg =
        err?.response?.data?.message || err?.message || "Delete failed";
      addToast(msg, "alert");
    } finally {
      deleting = false;
    }
  }

  // ── Search ────────────────────────────────────────────────────────
  // Triggered by the search box in the tree panel. We don't debounce:
  // 'name' mode is fast enough to feel instant, and 'all' mode is too
  // expensive to fire on every keystroke — the user explicitly hits
  // Enter (or clicks the search button) when they're ready.
  async function runSearch() {
    if (!selectedResource) return;
    const q = searchInput.trim();
    if (!q) {
      searchResults = [];
      searchActive = false;
      return;
    }
    searching = true;
    searchActive = true;
    try {
      searchResults = await configStore.searchExternal(
        selectedResource,
        q,
        searchMode,
      );
    } finally {
      searching = false;
    }
  }

  function clearSearch() {
    searchInput = "";
    searchResults = [];
    searchActive = false;
  }

  // Open a search hit. Same as toggleNode for leaves, but skips the
  // expansion path entirely — search results are flat, the user
  // doesn't need to walk through the folder hierarchy.
  async function openSearchHit(path: string) {
    // If the hit is a folder name, expand to it; otherwise open the
    // entry directly. Folder names come back without trailing "/"
    // (we strip it in the service layer) so the only signal is "did
    // List() ever return this with a slash?". Cheapest test: try
    // listing it; if it returns paths, treat as folder.
    const children = await configStore.listExternalPaths(
      selectedResource!,
      path + "/",
    );
    if (children.length > 0) {
      // Folder. Expand to it in the tree, but stay in search mode so
      // the user can keep iterating on results.
      const folderPath = path + "/";
      pathTree = { ...pathTree, [folderPath]: children };
      expanded = { ...expanded, [folderPath]: true };
    } else {
      // Leaf. Open it in the viewer.
      await openPath(path);
    }
  }

  // ── New entry composer ────────────────────────────────────────────
  function startCompose() {
    composing = true;
    newPath = "";
    // Seed the single-editor draft for the active backend: wrapper
    // backends start blank (raw value); structured backends start with
    // an empty JSON object so the editor lints as JSON from the first
    // keystroke.
    singleValueDraft = isWrapperBackend ? "" : "{}";
    pendingSaveConfirm = false;
    selectedPath = null;
    entry = null;
  }

  function cancelCompose() {
    composing = false;
    newPath = "";
    singleValueDraft = "";
    pendingSaveConfirm = false;
  }

  async function createEntry() {
    if (!selectedResource) return;
    if (!canWrite) {
      addToast("Missing external.write permission", "alert");
      return;
    }
    const path = newPath.trim();
    if (!path) {
      addToast("Path is required", "alert");
      return;
    }
    // Reuse the shared payload builder so create and save honour the
    // same parse/lint contract (see buildSaveData).
    const data = buildSaveData();
    if (data === undefined) return;
    saving = true;
    try {
      await configStore.writeExternalEntry(selectedResource, path, data);
      addToast("Created", "success");
      composing = false;
      // Refresh root or parent prefix and open the new entry.
      const parent = path.includes("/")
        ? path.slice(0, path.lastIndexOf("/") + 1)
        : "";
      pathTree = { ...pathTree, [parent]: undefined };
      await loadChildren(parent);
      await openPath(path);
    } catch (err: any) {
      const msg =
        err?.response?.data?.message || err?.message || "Create failed";
      addToast(msg, "alert");
    } finally {
      saving = false;
    }
  }

  // ── Refresh whole tree ────────────────────────────────────────────
  async function refreshTree() {
    if (!selectedResource) return;
    const wasExpanded = { ...expanded };
    pathTree = {};
    pathTreeLoading = {};
    expanded = { "": true };
    await loadChildren("");
    // Re-expand previously open folders. This is best-effort — if a
    // folder no longer exists upstream, it simply stays collapsed.
    for (const path of Object.keys(wasExpanded)) {
      if (path && wasExpanded[path]) {
        await loadChildren(path);
        expanded = { ...expanded, [path]: true };
      }
    }
  }

  // ── Helpers ───────────────────────────────────────────────────────
  function kindBadgeColor(kind: string): string {
    if (kind.startsWith("aws"))
      return "bg-orange-100 text-orange-700 dark:bg-orange-950/40 dark:text-orange-300";
    if (kind === "vault")
      return "bg-amber-100 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300";
    if (kind === "kubernetes")
      return "bg-sky-100 text-sky-700 dark:bg-sky-950/40 dark:text-sky-300";
    if (kind === "consul")
      return "bg-pink-100 text-pink-700 dark:bg-pink-950/40 dark:text-pink-300";
    if (kind === "etcd")
      return "bg-teal-100 text-teal-700 dark:bg-teal-950/40 dark:text-teal-300";
    if (kind === "gcp")
      return "bg-green-100 text-green-700 dark:bg-green-950/40 dark:text-green-300";
    if (kind === "gcp-parameter")
      return "bg-emerald-100 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300";
    if (kind === "azure")
      return "bg-blue-100 text-blue-700 dark:bg-blue-950/40 dark:text-blue-300";
    if (kind === "http")
      return "bg-accent-100 text-brand-700 dark:bg-accent-950/40 dark:text-accent-200";
    return "bg-slate-200 text-slate-700 dark:bg-slate-800 dark:text-slate-300";
  }

  // Display name for a tree node (strip the parent prefix so each row
  // is short, but keep the trailing "/" for folders so it's visible).
  function displayName(parent: string, child: string): string {
    return child;
  }
</script>

<svelte:head>
  <title>External · pika</title>
</svelte:head>

<div class="flex flex-col h-full overflow-hidden bg-slate-100 dark:bg-warm-900">
  {#if !booted}
    <div class="flex-1 flex items-center justify-center">
      <Loader2 size={20} class="animate-spin text-slate-400" />
    </div>
  {:else if !canManage}
    <div class="max-w-md mx-auto py-12 px-4 text-center">
      <ShieldOff size={32} class="mx-auto text-slate-400 mb-3" />
      <h2 class="text-lg font-semibold mb-2">Permission required</h2>
      <p class="text-sm text-slate-600 dark:text-slate-300">
        You need the <code
          class="px-1 py-0.5 bg-slate-200 dark:bg-warm-800 rounded text-xs"
          >external.read</code
        > capability to browse external resources.
      </p>
    </div>
  {:else}
    <div class="flex-1 flex overflow-hidden">
      <!-- ── Left: Resource List ─────────────────────────────── -->
      <ResizablePanel
        width={resourcesWidth}
        minWidth={160}
        maxWidth={360}
        side="left"
        onResize={(w) => (resourcesWidth = w)}
      >
        <aside
          class="h-full w-full flex flex-col border-r border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-950"
        >
        <div
          class="px-3 py-3 border-b border-slate-200 dark:border-warm-700 shrink-0"
        >
          <div class="flex items-center justify-between mb-2">
            <h1
              class="text-xs font-semibold text-slate-800 dark:text-slate-100 uppercase tracking-wide"
            >
              Resources
            </h1>
            <a
              href="/settings"
              use:link
              class="flex items-center gap-1 text-[10px] text-slate-500 dark:text-slate-400 hover:text-accent-600 transition-colors no-underline"
              title="Configure resources in Settings"
            >
              <SettingsIcon size={11} /> Manage
            </a>
          </div>
          <div class="relative">
            <Search
              size={11}
              class="absolute left-2 top-1/2 -translate-y-1/2 text-slate-400"
            />
            <input
              type="text"
              bind:value={resourceFilter}
              placeholder="Filter…"
              class="w-full pl-6 pr-2 py-1 text-xs border border-slate-200 dark:border-warm-700 rounded bg-slate-50 dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-1 focus:ring-accent-500/10"
            />
          </div>
        </div>
        <div class="flex-1 overflow-y-auto">
          {#if visibleResources.length === 0}
            <div class="px-4 py-8 text-center">
              <Globe
                size={20}
                class="mx-auto text-slate-300 dark:text-slate-600 mb-2"
              />
              <p class="text-[11px] text-slate-500 dark:text-slate-400">
                {resourceFilter ? "No matches" : "No resources configured"}
              </p>
              {#if !resourceFilter}
                <a
                  href="/settings"
                  use:link
                  class="inline-block mt-2 text-[11px] text-accent-600 hover:underline no-underline"
                >
                  Add one in Settings →
                </a>
              {/if}
            </div>
          {:else}
            <ul class="py-1">
              {#each visibleResources as r (r.name)}
                {@const isActive = r.name === selectedResource}
                <li>
                  <button
                    class="w-full text-left px-3 py-1.5 flex flex-col gap-0.5 transition-colors cursor-pointer
                           {isActive
                      ? 'bg-accent-50 dark:bg-accent-950/30 border-l-2 border-accent-600'
                      : 'border-l-2 border-transparent hover:bg-slate-50 dark:hover:bg-warm-900'}"
                    onclick={() => selectResource(r.name)}
                  >
                    <div class="flex items-center gap-1.5">
                      <span
                        class="text-xs font-medium text-slate-800 dark:text-slate-100 truncate flex-1"
                        >{r.name}</span
                      >
                      <span
                        class="px-1 py-0.5 text-[9px] font-medium rounded uppercase {kindBadgeColor(
                          r.kind,
                        )}"
                      >
                        {r.kind}
                      </span>
                    </div>
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
        </aside>
      </ResizablePanel>

      <!-- ── Middle: Path Tree ───────────────────────────────── -->
      <ResizablePanel
        width={pathsWidth}
        minWidth={180}
        maxWidth={420}
        side="left"
        onResize={(w) => (pathsWidth = w)}
      >
        <aside
          class="h-full w-full flex flex-col border-r border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-950"
        >
        {#if !selectedResource}
          <div class="flex-1 flex items-center justify-center text-center px-4">
            <p class="text-xs text-slate-400 dark:text-slate-500">
              Select a resource on the left
            </p>
          </div>
        {:else}
          <div
            class="px-3 py-2 border-b border-slate-200 dark:border-warm-700 shrink-0 flex items-center justify-between"
          >
            <span
              class="text-xs font-semibold text-slate-800 dark:text-slate-100 truncate"
              >Paths</span
            >
            <div class="flex items-center gap-1">
              {#if canWrite && currentResource?.capabilities.can_write}
                <button
                  class="p-1 text-slate-500 dark:text-slate-400 hover:text-accent-600 hover:bg-accent-50 dark:hover:bg-accent-950/30 rounded transition-colors cursor-pointer"
                  onclick={startCompose}
                  title="New entry"
                >
                  <Plus size={12} />
                </button>
              {/if}
              <button
                class="p-1 text-slate-500 dark:text-slate-400 hover:text-accent-600 hover:bg-accent-50 dark:hover:bg-accent-950/30 rounded transition-colors cursor-pointer"
                onclick={refreshTree}
                title="Refresh"
              >
                <RefreshCw size={12} />
              </button>
            </div>
          </div>

          <!-- Search bar. Mirrors Configuration's FileTree pattern
               (FileTree.svelte:135-153): input + explicit search
               button + mode toggle. Backends that don't support List()
               (HTTP) get search hidden — there's nothing to walk.

               Why an explicit button alongside Enter: 'all' mode
               (content grep) can take a few seconds on large trees,
               so the button gives the user a deliberate "go now"
               affordance instead of relying on the keyboard. Enter
               still works as a power-user shortcut. -->
          {#if currentResource?.capabilities.can_list}
            <div
              class="px-3 py-2 border-b border-slate-200 dark:border-warm-700 shrink-0 space-y-1.5"
            >
              <div class="flex items-center gap-1.5">
                <div class="relative flex-1">
                  <Search
                    size={11}
                    class="absolute left-2 top-1/2 -translate-y-1/2 text-slate-400 pointer-events-none"
                  />
                  <input
                    type="text"
                    bind:value={searchInput}
                    onkeydown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault();
                        runSearch();
                      } else if (e.key === "Escape") {
                        clearSearch();
                      }
                    }}
                    placeholder={searchMode === "name"
                      ? "Search paths…"
                      : "Search paths + values…"}
                    class="w-full pl-7 pr-7 py-1 text-[11px] font-mono border border-slate-200 dark:border-warm-700 rounded bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 focus:outline-none focus:border-accent-500 focus:ring-1 focus:ring-accent-500/30"
                  />
                  {#if searchInput}
                    <button
                      class="absolute right-1 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer"
                      onclick={clearSearch}
                      title="Clear search"
                      aria-label="Clear search"
                    >
                      <X size={11} />
                    </button>
                  {/if}
                </div>
                <!-- Search action button. Disabled when there's
                     nothing to search for or when a query is already
                     in flight. Stays compact to keep the input field
                     wide enough to read. -->
                <button
                  class="px-2 py-1 text-[11px] font-medium text-white bg-accent-600 rounded hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer transition-colors shrink-0"
                  onclick={runSearch}
                  disabled={searching || !searchInput.trim()}
                  title="Search (Enter)"
                  aria-label="Search"
                >
                  {#if searching}
                    <Loader2 size={11} class="animate-spin" />
                  {:else}
                    <Search size={11} />
                  {/if}
                </button>
              </div>
              <div class="flex items-center justify-between text-[10px]">
                <!-- Mode toggle. Same chip-style affordance as
                     Configuration; clicking flips and re-runs the
                     query so the user sees the new result set
                     immediately rather than having to hit Enter. -->
                <button
                  class="px-1.5 py-0.5 rounded font-mono cursor-pointer transition-colors
                         {searchMode === 'all'
                    ? 'bg-accent-100 dark:bg-accent-950/40 text-accent-700 dark:text-accent-300 border border-accent-200 dark:border-accent-900'
                    : 'bg-slate-100 dark:bg-warm-800 text-slate-500 dark:text-slate-400 border border-slate-200 dark:border-warm-700'}"
                  onclick={() => {
                    searchMode = searchMode === "name" ? "all" : "name";
                    if (searchActive) runSearch();
                  }}
                  title={searchMode === "name"
                    ? "Searching path names only. Click to also search values."
                    : "Searching paths AND values. Click for names only."}
                  aria-pressed={searchMode === "all"}
                >
                  {searchMode === "all" ? "+contents" : "names only"}
                </button>
                {#if searchActive && !searching}
                  <span class="text-slate-400">
                    {searchResults.length}
                    {searchResults.length === 1 ? "hit" : "hits"}
                  </span>
                {/if}
              </div>
            </div>
          {/if}

          <div class="flex-1 overflow-y-auto py-1">
            {#if !currentResource?.capabilities.can_list}
              <div
                class="px-4 py-6 text-center text-[11px] text-slate-500 dark:text-slate-400"
              >
                This backend doesn't expose a path listing.<br />
                Use the New button to enter a path directly.
              </div>
            {:else if searchActive}
              <!-- Search results take over the tree area while active.
                   The tree state isn't touched — clearing the search
                   restores exactly what was open before. -->
              {#if searchResults.length === 0 && !searching}
                <div
                  class="px-4 py-6 text-center text-[11px] text-slate-500 dark:text-slate-400"
                >
                  No matches for <code
                    class="font-mono text-slate-700 dark:text-slate-300"
                    >{searchInput}</code
                  >
                  {#if searchMode === "name"}
                    <div class="mt-2 text-[10px]">
                      Try <button
                        class="underline cursor-pointer"
                        onclick={() => {
                          searchMode = "all";
                          runSearch();
                        }}>+contents</button
                      > mode.
                    </div>
                  {/if}
                </div>
              {:else}
                <!-- Results are intentionally name-only — even content
                     hits show just the path. The snippet is hidden so
                     the user can scan a long result list without their
                     eyes being drawn to the value previews; clicking a
                     row loads the entry in the editor pane where the
                     content can be read in full. The hit type is still
                     surfaced as a small pill so the operator knows
                     *why* a path matched (was it the path itself or
                     something inside the value?). -->
                <ul class="text-[11px]">
                  {#each searchResults as hit (hit.path + ":" + hit.type)}
                    <li>
                      <button
                        class="w-full text-left px-3 py-1 flex items-center gap-1 hover:bg-slate-50 dark:hover:bg-warm-900 cursor-pointer
                               {selectedPath === hit.path
                          ? 'bg-accent-50 dark:bg-accent-950/30 text-accent-700 dark:text-accent-300'
                          : 'text-slate-700 dark:text-slate-200'}"
                        onclick={() => openSearchHit(hit.path)}
                        title={hit.snippet ?? hit.path}
                      >
                        {#if hit.type === "content"}
                          <FileText size={10} class="text-slate-400 shrink-0" />
                        {:else}
                          <Search size={10} class="text-slate-400 shrink-0" />
                        {/if}
                        <span class="font-mono truncate flex-1">{hit.path}</span
                        >
                        {#if hit.type === "content"}
                          <!-- "in value" pill — only on content hits.
                               Name hits don't need disambiguation
                               since "path matched the path" is the
                               default mental model. -->
                          <span
                            class="px-1 py-px text-[9px] font-medium rounded bg-amber-100 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300 shrink-0"
                          >
                            in value
                          </span>
                        {/if}
                      </button>
                    </li>
                  {/each}
                </ul>
              {/if}
            {:else}
              {#snippet tree(prefix: string, depth: number)}
                {@const children = pathTree[prefix]}
                {@const isLoading = pathTreeLoading[prefix]}
                {#if isLoading && children === undefined}
                  <div
                    class="px-3 py-1 text-[10px] text-slate-400"
                    style="padding-left: {12 + depth * 12}px"
                  >
                    <Loader2 size={10} class="inline animate-spin" /> Loading…
                  </div>
                {:else if children !== undefined && children.length === 0 && depth > 0}
                  <div
                    class="px-3 py-1 text-[10px] text-slate-400"
                    style="padding-left: {12 + depth * 12}px"
                  >
                    (empty)
                  </div>
                {:else if children !== undefined}
                  {#each children as child (child)}
                    {@const fullPath = prefix + child}
                    {@const isF = isFolder(fullPath)}
                    {@const isOpen = expanded[fullPath] === true}
                    {@const isSel = selectedPath === fullPath}
                    <div>
                      <!-- Row uses div+role rather than <button> so we
                           can nest a real Delete button inside without
                           tripping HTML's "no button inside button"
                           rule. Keyboard handling: Enter/Space activate
                           the row. -->
                      <div
                        role="button"
                        tabindex="0"
                        class="w-full text-left flex items-center gap-1 py-1 hover:bg-slate-50 dark:hover:bg-warm-900 transition-colors cursor-pointer group
                               {isSel
                          ? 'bg-accent-50 dark:bg-accent-950/30 text-accent-700 dark:text-accent-300'
                          : 'text-slate-700 dark:text-slate-200'}"
                        style="padding-left: {8 + depth * 12}px"
                        onclick={() => toggleNode(fullPath)}
                        onkeydown={(e) => {
                          if (e.key === "Enter" || e.key === " ") {
                            e.preventDefault();
                            toggleNode(fullPath);
                          }
                        }}
                      >
                        {#if isF}
                          {#if isOpen}
                            <ChevronDown
                              size={10}
                              class="text-slate-400 shrink-0"
                            />
                            <FolderOpen
                              size={11}
                              class="text-amber-500 shrink-0"
                            />
                          {:else}
                            <ChevronRight
                              size={10}
                              class="text-slate-400 shrink-0"
                            />
                            <Folder size={11} class="text-amber-500 shrink-0" />
                          {/if}
                        {:else}
                          <span class="w-[10px] shrink-0"></span>
                          <FileText size={11} class="text-slate-400 shrink-0" />
                        {/if}
                        <span class="text-[11px] font-mono truncate flex-1"
                          >{displayName(prefix, child)}</span
                        >
                        {#if !isF && canWrite && currentResource?.capabilities.can_delete}
                          <button
                            class="opacity-0 group-hover:opacity-100 p-0.5 text-slate-400 hover:text-red-500 transition-all cursor-pointer mr-1"
                            onclick={(e) => {
                              e.stopPropagation();
                              confirmDeletePath = fullPath;
                            }}
                            title="Delete"
                          >
                            <Trash2 size={10} />
                          </button>
                        {/if}
                      </div>
                      {#if isF && isOpen}
                        {@render tree(fullPath, depth + 1)}
                      {/if}
                    </div>
                  {/each}
                {/if}
              {/snippet}
              {@render tree("", 0)}
            {/if}
          </div>
        {/if}
        </aside>
      </ResizablePanel>

      <!-- ── Right: Content viewer / editor ──────────────────── -->
      <section
        class="flex-1 overflow-hidden flex flex-col bg-white dark:bg-warm-950"
      >
        {#if !selectedResource}
          <div
            class="flex-1 flex flex-col items-center justify-center text-center px-6"
          >
            <div
              class="w-16 h-16 rounded-full bg-slate-100 dark:bg-warm-900 flex items-center justify-center mb-4"
            >
              <Globe size={28} class="text-slate-400 opacity-70" />
            </div>
            <div
              class="text-sm font-medium text-slate-700 dark:text-slate-200 mb-1"
            >
              External Resource Browser
            </div>
            <div class="text-xs text-slate-500 dark:text-slate-400 max-w-sm">
              Configured external backends appear on the left. Pick one to
              browse its contents — read secrets, edit values, or remove entries
              without leaving Pika.
            </div>
          </div>
        {:else if composing}
          <!-- New-entry composer -->
          <div
            class="px-5 py-3 border-b border-slate-200 dark:border-warm-700 shrink-0 flex items-center gap-2"
          >
            <Plus size={14} class="text-accent-600" />
            <span
              class="text-sm font-semibold text-slate-800 dark:text-slate-100"
              >New entry</span
            >
            <div class="flex-1"></div>
            <button
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer disabled:opacity-50"
              onclick={createEntry}
              disabled={saving}
            >
              {#if saving}<Loader2 size={12} class="animate-spin" />{:else}<Save
                  size={12}
                />{/if} Create
            </button>
            <button
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-slate-700 dark:text-slate-200 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:hover:bg-warm-800 transition-colors cursor-pointer"
              onclick={cancelCompose}
            >
              <X size={12} /> Cancel
            </button>
          </div>
          <div class="flex-1 min-h-0 flex flex-col">
            <div class="px-5 pt-4 pb-3 shrink-0">
              <label
                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
                for="new-path">Path</label
              >
              <input
                id="new-path"
                type="text"
                bind:value={newPath}
                placeholder={currentResource?.kind === "kubernetes"
                  ? "default/secret/my-secret"
                  : currentResource?.kind === "vault"
                    ? "myapp/db"
                    : "key/path"}
                class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
              />
            </div>
            {@render valueEditor()}
          </div>
        {:else if !selectedPath}
          <div
            class="flex-1 flex flex-col items-center justify-center text-center px-6"
          >
            <FileText
              size={32}
              class="text-slate-300 dark:text-slate-600 mb-3"
            />
            <p class="text-sm text-slate-600 dark:text-slate-300 mb-1">
              Select a path on the left
            </p>
            <p class="text-xs text-slate-500 dark:text-slate-400 max-w-sm">
              Or click <strong>+</strong> to create a new entry.
            </p>
          </div>
        {:else}
          <!-- Header bar: path + actions -->
          <div
            class="px-5 py-2 border-b border-slate-200 dark:border-warm-700 shrink-0"
          >
            <div class="flex items-center gap-2">
              <FileText size={14} class="text-slate-400 shrink-0" />
              <span
                class="text-sm font-mono text-slate-800 dark:text-slate-100 truncate flex-1"
                >{selectedPath}</span
              >
              <div class="flex items-center gap-1 shrink-0">
                {#if !editing}
                  {#if canWrite && currentResource?.capabilities.can_write}
                    <button
                      class="flex items-center gap-1 px-2 py-1 text-[11px] font-medium text-white bg-accent-600 rounded hover:bg-accent-700 transition-colors cursor-pointer"
                      onclick={startEdit}
                      disabled={!entry}
                    >
                      Edit
                    </button>
                  {/if}
                  <button
                    class="p-1.5 text-slate-500 dark:text-slate-400 hover:text-accent-600 hover:bg-accent-50 dark:hover:bg-accent-950/30 rounded transition-colors cursor-pointer"
                    onclick={() => loadEntry(selectedPath!, activeVersion)}
                    title="Reload"
                  >
                    <RefreshCw size={12} />
                  </button>
                  {#if canWrite && currentResource?.capabilities.can_delete}
                    {#if confirmDeletePath === selectedPath}
                      <span
                        class="text-[11px] text-slate-600 dark:text-slate-300"
                        >Delete?</span
                      >
                      <button
                        class="px-2 py-1 text-[11px] font-medium text-white bg-red-600 rounded hover:bg-red-700 cursor-pointer disabled:opacity-50"
                        onclick={() => performDelete(selectedPath!)}
                        disabled={deleting}
                      >
                        Yes
                      </button>
                      <button
                        class="px-2 py-1 text-[11px] font-medium text-slate-700 dark:text-slate-200 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded hover:bg-slate-50 dark:hover:bg-warm-800 cursor-pointer"
                        onclick={() => (confirmDeletePath = null)}
                      >
                        No
                      </button>
                    {:else}
                      <button
                        class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-950/30 rounded transition-colors cursor-pointer"
                        onclick={() => (confirmDeletePath = selectedPath)}
                        title="Delete"
                      >
                        <Trash2 size={12} />
                      </button>
                    {/if}
                  {/if}
                {:else}
                  <button
                    class="flex items-center gap-1 px-2 py-1 text-[11px] font-medium text-white bg-accent-600 rounded hover:bg-accent-700 transition-colors cursor-pointer disabled:opacity-50"
                    onclick={saveEntry}
                    disabled={saving}
                  >
                    {#if saving}<Loader2
                        size={10}
                        class="animate-spin"
                      />{:else}<Save size={10} />{/if}
                    Save
                  </button>
                  <button
                    class="flex items-center gap-1 px-2 py-1 text-[11px] font-medium text-slate-700 dark:text-slate-200 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded hover:bg-slate-50 dark:hover:bg-warm-800 cursor-pointer"
                    onclick={cancelEdit}
                  >
                    Cancel
                  </button>
                {/if}
              </div>
            </div>
          </div>

          <!-- Version selector strip (Vault KV v2 only) -->
          {#if currentResource?.capabilities.can_versions && versions.length > 0}
            <div
              class="px-5 py-1.5 border-b border-slate-200 dark:border-warm-700 shrink-0 flex items-center gap-2 bg-slate-50 dark:bg-warm-900/50"
            >
              <History size={11} class="text-slate-400" />
              <span
                class="text-[10px] uppercase tracking-wide text-slate-500 dark:text-slate-400 mr-1"
                >Versions</span
              >
              <div class="flex items-center gap-1 overflow-x-auto">
                <button
                  class="px-2 py-0.5 text-[10px] font-mono rounded transition-colors cursor-pointer shrink-0
                         {activeVersion === ''
                    ? 'bg-accent-600 text-white'
                    : 'bg-white dark:bg-warm-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-warm-700 hover:bg-slate-50 dark:hover:bg-warm-800'}"
                  onclick={() => loadEntry(selectedPath!, "")}
                >
                  latest
                </button>
                {#each versions as v (v.id)}
                  <button
                    class="px-2 py-0.5 text-[10px] font-mono rounded transition-colors cursor-pointer shrink-0
                           {activeVersion === v.id
                      ? 'bg-accent-600 text-white'
                      : v.destroyed
                        ? 'bg-red-50 dark:bg-red-950/30 text-red-700 dark:text-red-300 border border-red-200 dark:border-red-900'
                        : v.deleted
                          ? 'bg-amber-50 dark:bg-amber-950/30 text-amber-700 dark:text-amber-300 border border-amber-200 dark:border-amber-900'
                          : 'bg-white dark:bg-warm-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-warm-700 hover:bg-slate-50 dark:hover:bg-warm-800'}"
                    onclick={() => loadEntry(selectedPath!, v.id)}
                    title={v.destroyed
                      ? "destroyed"
                      : v.deleted
                        ? "deleted"
                        : v.created_at || ""}
                    disabled={v.destroyed}
                  >
                    <!-- Version labels: Vault returns numeric IDs ("1",
                         "2") so we prefix "v" for the familiar v1/v2
                         look. Parameter Manager versions are user-named
                         strings ("prod", "v1", "rev3"), so we render
                         those verbatim — prefixing would produce
                         "vprod" / "vv1" which is awkward. The detection
                         is "pure digits → prefix, otherwise raw".

                         The parens around the regex literal aren't
                         optional: Svelte's template parser treats `{/`
                         as the start of a closing block tag (`{/if}`,
                         `{/each}`), so a bare regex right after `{`
                         fails to parse. -->
                    {(/^\d+$/).test(v.id) ? `v${v.id}` : v.id}
                  </button>
                {/each}
              </div>
            </div>
          {/if}

          <!-- Body. Padding is intentionally NOT applied here: the
               entry viewer wants to render its editor edge-to-edge
               for an IDE-style "full pane" feel (no surrounding gutter
               around the code surface). Each non-viewer branch adds
               its own padding so loading messages and error cards
               still get breathing room. -->
          <div class="flex-1 min-h-0 flex flex-col">
            {#if entryLoading}
              <div class="flex-1 flex items-center justify-center">
                <Loader2 size={20} class="animate-spin text-slate-400" />
              </div>
            {:else if entryError}
              <div
                class="m-5 flex items-start gap-2 p-3 bg-red-50 dark:bg-red-950/30 border border-red-200 dark:border-red-900 rounded-md"
              >
                <AlertCircle
                  size={14}
                  class="text-red-600 dark:text-red-400 mt-0.5 shrink-0"
                />
                <div class="flex-1 min-w-0">
                  <div
                    class="text-xs font-medium text-red-800 dark:text-red-200"
                  >
                    Failed to read
                  </div>
                  <div
                    class="text-[11px] text-red-700 dark:text-red-300 mt-0.5 font-mono"
                  >
                    {entryError}
                  </div>
                </div>
              </div>
            {:else if editing}
              <!-- Every backend edits through one full-bleed document
                   editor. Wrapper backends (Consul/etcd/HTTP/GCP) store
                   the raw string; structured backends (Vault/K8s) edit
                   the whole secret as JSON/YAML. Both get format
                   controls, beautify, and inline linting. -->
              {@render valueEditor()}
            {:else if entry}
              {@render entryViewer(entry)}
            {/if}
          </div>
        {/if}
      </section>
    </div>
  {/if}
</div>

<!-- ── Snippets ─────────────────────────────────────────────────── -->
{#snippet entryViewer(e: ExternalEntry)}
  <!-- Synthetic-wrapper detection.

       Several backends in pika (Consul, etcd, HTTP, GCP Secret Manager
       fallback path, GCP Parameter Manager UNFORMATTED fallback) wrap
       a single opaque string value in `{value: "<string>"}` because
       the Provider contract returns a structured map. The "value" key
       there carries no information — it's purely an artefact of the
       wire shape — so surfacing it as a column header is at best
       noise and at worst confusing ("why does my Consul key say
       'value:' on top of itself?").

       Heuristic: exactly one entry AND that entry's key is "value".
       This is the exact shape every wrapper-producing backend emits.
       Real maps from Vault / Kubernetes can also contain a "value"
       key, but they always have at least one more key alongside it —
       single-entry "value" is reliable. -->
  {@const dataEntries = e.data ? Object.entries(e.data) : []}
  {@const isWrapper = dataEntries.length === 1 && dataEntries[0][0] === "value"}

  <!-- Full-bleed pane layout: no outer padding, no gap, no rounded
       corner on the editor wrapper — the editor IS the pane. This is
       deliberately the opposite of a "card inside a section" look:
       we want the user to feel like they're inside a code surface,
       the same way Configuration's editor is full-bleed under its
       toolbar.

       Single-editor case: that editor occupies the entire body.
       Multi-editor case: editors split the column equally (flex-1),
       separated by a 1px dark divider — matches VS Code's split view
       seam. Each editor still has its own toolbar + body, so the
       seam reads naturally as "two adjacent code panes". -->
  <div class="flex-1 min-h-0 flex flex-col">
    {#if dataEntries.length === 0}
      <div class="m-5 text-xs text-slate-400 dark:text-slate-500 italic">
        No data
      </div>
    {:else if isWrapper}
      <!-- Wrapper backends (Consul/etcd/HTTP/GCP fallback) carry a
           single opaque string under the synthetic "value" key. Show
           that raw string in one editor, with no "value" header (it
           would just be noise — see the synthetic-wrapper note above). -->
      <div class="flex-1 min-h-0 flex flex-col">
        <ExternalValueEditor
          value={typeof dataEntries[0][1] === "string"
            ? dataEntries[0][1]
            : JSON.stringify(dataEntries[0][1], null, 2)}
          readonly={true}
        />
      </div>
    {:else}
      <!-- Structured secret (Vault, Kubernetes): render the WHOLE map
           as one flat JSON document in a single editor. We used to open
           one editor per key, which fragmented a single secret into N
           stacked code panes — hard to scan, copy, and reason about.
           A single JSON view matches how the secret actually lives in
           the backend and lets the user copy/format it in one shot. -->
      <div class="flex-1 min-h-0 flex flex-col">
        <ExternalValueEditor
          value={JSON.stringify(e.data, null, 2)}
          lang="json"
          readonly={true}
        />
      </div>
    {/if}
    {#if e.content_type}
      <!-- Footer. Lives outside the editor area so it doesn't
           interrupt the full-bleed code surface; gets its own thin
           border + dark background to match the chrome above. -->
      <div
        class="px-3 py-1.5 text-[10px] text-gray-400 bg-[#252526] border-t border-[#3c3c3c] shrink-0"
      >
        Content-Type: <code class="font-mono">{e.content_type}</code>
        {#if e.version}· version <code class="font-mono">{e.version}</code>{/if}
      </div>
    {/if}
  </div>
{/snippet}

{#snippet valueEditor()}
  <!-- Unified edit surface for every backend. ONE editor, full-bleed,
       with format selector + beautify + inline linting in the toolbar.

       Wrapper backends (Consul/etcd/HTTP/GCP): the chosen format only
       affects highlighting/linting — the backend stores the raw string
       verbatim (consul.go:275 detects the `{value: <string>}` shape and
       bypasses JSON marshaling), so format selection is a UX hint and a
       syntax error is advisory (gated by the save-time confirm).

       Structured backends (Vault/K8s): the editor holds the WHOLE secret
       as one JSON/YAML document. saveEntry() parses it back to the
       key/value map the backend stores; because js-yaml reads JSON too,
       a YAML edit is converted to the JSON map Vault persists.

       We drive value via `onchange` (not `bind:value`) because
       AppCodeMirror relays edits one-way through its onchange callback —
       a bare bind wouldn't capture typing. `editorLintError` flows back
       up via the bindable so the save flow can gate on syntax errors. -->
  <div class="flex-1 min-h-0 flex flex-col">
    <ExternalValueEditor
      value={singleValueDraft}
      onchange={onDraftChange}
      bind:lintError={editorLintError}
      showFormatControls={true}
      placeholder={isWrapperBackend
        ? "Enter value (plain text, JSON, YAML, …)"
        : "Edit the whole secret as JSON or YAML — stored as JSON"}
    />
  </div>
{/snippet}
