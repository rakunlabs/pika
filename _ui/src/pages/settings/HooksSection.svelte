<script lang="ts">
    import { confirmDialog } from "@/lib/store/confirm.svelte";
    import { configStore } from "@/lib/store/config.svelte";
    import { addToast } from "@/lib/store/toast.svelte";
    import { onMount } from "svelte";
    import { Plus, Trash2, Eye, EyeOff, Webhook, Pencil } from "lucide-svelte";
    import type { Hook, HookTarget } from "@/lib/types/config";
    import HookTargetForm from "@/lib/components/hooks/HookTargetForm.svelte";
    import { targetTypeBadge } from "@/lib/components/hooks/shared";

    // ── Hooks state ──
    let hooks = $state<Hook[]>([]);
    let showAddHook = $state(false);
    let editingHookIndex = $state<number | null>(null);
    let isSavingHooks = $state(false);
    let eventLogEnabled = $state(true);
    let isSavingEventLog = $state(false);
    // Hook form fields
    let hookName = $state("");
    let hookEnabled = $state(true);
    let hookEvents = $state<string[]>([]);
    let hookEventInput = $state("");
    let hookFilterMounts = $state<string[]>([]);
    let hookFilterMountInput = $state("");
    let hookFilterPathPattern = $state("");
    let hookTargets = $state<HookTarget[]>([]);
    // Target form (inline within hook form). `editingTargetIndex` is
    // null when adding a new target.
    let showAddTarget = $state(false);
    let editingTargetIndex = $state<number | null>(null);
    // Bumped every time the target form opens so it remounts with fresh state.
    let targetFormKey = $state(0);

    // Known event types for quick-add
    const HOOK_EVENT_TYPES = [
        "file.created",
        "file.updated",
        "file.deleted",
        "dir.created",
        "file.renamed",
        "file.copied",
        "config.created",
        "config.deleted",
        "config.updated",
        "*",
    ];

    // Initialize hooks from configStore on mount
    onMount(() => {
        void (async () => {
            if (!configStore.settings) {
                await configStore.loadSettings();
            }
            hooks = [...(configStore.settings?.hooks || [])];
            eventLogEnabled =
                configStore.settings?.event_log?.disabled !== true;
        })();
    });

    async function saveEventLogToggle(enabled: boolean) {
        const previous = eventLogEnabled;
        eventLogEnabled = enabled;
        isSavingEventLog = true;
        try {
            await configStore.saveEventLogSettings({ disabled: !enabled });
        } catch {
            eventLogEnabled = previous;
        } finally {
            isSavingEventLog = false;
        }
    }

    // ── Hook handlers ──
    function resetHookForm() {
        hookName = "";
        hookEnabled = true;
        hookEvents = [];
        hookEventInput = "";
        hookFilterMounts = [];
        hookFilterMountInput = "";
        hookFilterPathPattern = "";
        hookTargets = [];
        editingHookIndex = null;
        showAddTarget = false;
        resetTargetForm();
    }

    function resetTargetForm() {
        editingTargetIndex = null;
        targetFormKey++;
    }

    function handleEditHook(index: number) {
        const h = hooks[index];
        hookName = h.name;
        hookEnabled = h.enabled;
        hookEvents = [...h.events];
        hookFilterMounts = [...(h.filter?.mounts || [])];
        hookFilterMountInput = "";
        hookFilterPathPattern = h.filter?.path_pattern || "";
        hookTargets = h.targets.map((t: HookTarget) => ({ ...t }));
        editingHookIndex = index;
        showAddHook = true;
        showAddTarget = false;
        resetTargetForm();
    }

    function addHookEvent() {
        const e = hookEventInput.trim();
        if (!e) return;
        if (hookEvents.includes(e)) {
            addToast("Event already added", "alert");
            return;
        }
        hookEvents = [...hookEvents, e];
        hookEventInput = "";
    }

    function removeHookEvent(index: number) {
        hookEvents = hookEvents.filter((_, i) => i !== index);
    }

    function addHookFilterMount() {
        const m = hookFilterMountInput.trim();
        if (!m) return;
        if (hookFilterMounts.includes(m)) {
            addToast("Mount already added", "alert");
            return;
        }
        hookFilterMounts = [...hookFilterMounts, m];
        hookFilterMountInput = "";
    }

    function removeHookFilterMount(index: number) {
        hookFilterMounts = hookFilterMounts.filter((_, i) => i !== index);
    }

    function handleSaveTarget(target: HookTarget) {
        if (editingTargetIndex !== null) {
            hookTargets = hookTargets.map((t, i) =>
                i === editingTargetIndex ? target : t,
            );
        } else {
            hookTargets = [...hookTargets, target];
        }
        showAddTarget = false;
        resetTargetForm();
    }

    function handleEditTarget(index: number) {
        resetTargetForm();
        editingTargetIndex = index;
        showAddTarget = true;
    }

    function removeTarget(index: number) {
        hookTargets = hookTargets.filter((_, i) => i !== index);
    }

    async function handleAddHook() {
        const name = hookName.trim();
        if (!name) {
            addToast("Hook name is required", "alert");
            return;
        }
        if (hookEvents.length === 0) {
            addToast("At least one event type is required", "alert");
            return;
        }
        if (hookTargets.length === 0) {
            addToast("At least one target is required", "alert");
            return;
        }
        if (hooks.some((h, i) => h.name === name && i !== editingHookIndex)) {
            addToast(`A hook named "${name}" already exists`, "alert");
            return;
        }

        const entry: Hook = {
            name,
            enabled: hookEnabled,
            events: [...hookEvents],
            filter:
                hookFilterMounts.length > 0 || hookFilterPathPattern
                    ? {
                          mounts:
                              hookFilterMounts.length > 0
                                  ? [...hookFilterMounts]
                                  : undefined,
                          path_pattern: hookFilterPathPattern || undefined,
                      }
                    : undefined,
            targets: hookTargets.map((t) => ({ ...t })),
        };

        let updated: Hook[];
        if (editingHookIndex !== null) {
            updated = hooks.map((h, i) => (i === editingHookIndex ? entry : h));
        } else {
            updated = [...hooks, entry];
        }

        isSavingHooks = true;
        try {
            await configStore.saveHooks(updated);
            hooks = updated;
            showAddHook = false;
            resetHookForm();
        } catch {
            // toast already shown
        } finally {
            isSavingHooks = false;
        }
    }

    async function handleRemoveHook(index: number) {
        const h = hooks[index];
        if (
            !(await confirmDialog({
                title: `Remove hook "${h.name}"?`,
                confirmLabel: "Remove",
                danger: true,
            }))
        )
            return;

        const updated = hooks.filter((_, i) => i !== index);
        isSavingHooks = true;
        try {
            await configStore.saveHooks(updated);
            hooks = updated;
        } catch {
            // toast already shown
        } finally {
            isSavingHooks = false;
        }
    }

    async function handleToggleHook(index: number) {
        const updated = hooks.map((h, i) =>
            i === index ? { ...h, enabled: !h.enabled } : h,
        );
        isSavingHooks = true;
        try {
            await configStore.saveHooks(updated);
            hooks = updated;
        } catch {
            // toast already shown
        } finally {
            isSavingHooks = false;
        }
    }
</script>

<div>
    <div class="flex items-center justify-between mb-4">
        <div>
            <h2
                class="text-lg font-semibold text-slate-800 dark:text-slate-100"
            >
                Hooks
            </h2>
            <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
                Trigger HTTP webhooks or Kafka messages when files are created,
                updated, or deleted.
            </p>
        </div>
        <button
            class="flex items-center gap-1.5 px-3 py-2 bg-accent-600 text-white text-sm font-medium rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
            onclick={() => {
                showAddHook = true;
                resetHookForm();
            }}
        >
            <Plus size={14} />
            Add Hook
        </button>
    </div>

    <div
        class="mb-4 p-4 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm"
    >
        <label class="flex items-start gap-3 cursor-pointer">
            <input
                type="checkbox"
                class="mt-0.5 h-4 w-4 rounded border-slate-300 dark:border-warm-600 text-accent-600 focus:ring-accent-500 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed dark:text-accent-400"
                checked={eventLogEnabled}
                disabled={isSavingEventLog}
                onchange={(e) =>
                    saveEventLogToggle(
                        (e.currentTarget as HTMLInputElement).checked,
                    )}
            />
            <span class="flex-1">
                <span
                    class="block text-sm font-medium text-slate-800 dark:text-slate-100"
                >
                    Write emitted events to server logs
                </span>
                <span
                    class="block text-xs text-slate-500 dark:text-slate-400 mt-0.5"
                >
                    Enabled by default. Pika writes one structured <code
                        class="font-mono text-[11px]">pika event emitted</code
                    > log line per event so operators can use their own log tooling
                    instead of storing events in the database.
                </span>
            </span>
        </label>
        <p class="mt-3 text-[11px] text-slate-400 dark:text-slate-500">
            This only controls the built-in event log line. Configured hook
            targets, including custom <code class="font-mono">log</code> targets,
            continue to receive matching events.
        </p>
    </div>

    <!-- Add/Edit Hook Form -->
    {#if showAddHook}
        <div
            class="mb-6 p-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm"
        >
            <h3
                class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-4"
            >
                {editingHookIndex !== null ? "Edit Hook" : "Add Hook"}
            </h3>

            <!-- Hook Name -->
            <div class="mb-4">
                <label
                    for="hook-name"
                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
                    >Hook Name</label
                >
                <input
                    id="hook-name"
                    type="text"
                    bind:value={hookName}
                    placeholder="e.g., upload-notifier"
                    class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                />
            </div>

            <!-- Enabled -->
            <div class="mb-4">
                <label
                    class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
                >
                    <input
                        type="checkbox"
                        bind:checked={hookEnabled}
                        class="rounded border-slate-300 dark:border-warm-600"
                    />
                    Enabled
                </label>
            </div>

            <!-- Event Types -->
            <div class="mb-4">
                <label
                    for="hook-event-input"
                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
                    >Event Types</label
                >
                <div class="flex gap-2 mb-2">
                    <input
                        id="hook-event-input"
                        type="text"
                        bind:value={hookEventInput}
                        placeholder="e.g., file.created"
                        onkeydown={(e) => {
                            if (e.key === "Enter") {
                                e.preventDefault();
                                addHookEvent();
                            }
                        }}
                        class="flex-1 px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                    <button
                        class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
                        onclick={addHookEvent}
                    >
                        Add
                    </button>
                </div>
                <!-- Quick-add buttons -->
                <div class="flex flex-wrap gap-1 mb-2">
                    {#each HOOK_EVENT_TYPES as evtType}
                        {#if !hookEvents.includes(evtType)}
                            <button
                                class="px-2 py-0.5 text-[10px] font-mono rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 text-slate-500 dark:text-slate-400 hover:bg-accent-50 dark:hover:bg-accent-900/40 hover:text-accent-700 dark:hover:text-accent-300 hover:border-accent-200 dark:hover:border-accent-700 transition-colors cursor-pointer"
                                onclick={() => {
                                    hookEvents = [...hookEvents, evtType];
                                }}>{evtType}</button
                            >
                        {/if}
                    {/each}
                </div>
                {#if hookEvents.length > 0}
                    <div class="flex flex-wrap gap-1.5">
                        {#each hookEvents as ev, i}
                            <span
                                class="inline-flex items-center gap-1 px-2 py-1 bg-accent-50 dark:bg-accent-900/40 border border-accent-200 dark:border-accent-700 rounded text-xs font-mono text-accent-700 dark:text-accent-300"
                            >
                                {ev}
                                <button
                                    class="flex items-center justify-center w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-accent-400 hover:text-vermilion-500 transition-colors"
                                    onclick={() => removeHookEvent(i)}
                                    title="Remove"
                                    aria-label="Remove">&times;</button
                                >
                            </span>
                        {/each}
                    </div>
                {/if}
            </div>

            <!-- Filters -->
            <div
                class="mb-4 p-4 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
                <p
                    class="text-xs font-semibold text-slate-500 dark:text-slate-400 mb-3 uppercase tracking-wider"
                >
                    Filters (optional)
                </p>

                <!-- Mount filter -->
                <div class="mb-3">
                    <label
                        for="hook-mount-filter"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
                        >Mount Prefixes</label
                    >
                    <div class="flex gap-2">
                        <input
                            id="hook-mount-filter"
                            type="text"
                            bind:value={hookFilterMountInput}
                            placeholder="e.g., uploads"
                            onkeydown={(e) => {
                                if (e.key === "Enter") {
                                    e.preventDefault();
                                    addHookFilterMount();
                                }
                            }}
                            class="flex-1 px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                        />
                        <button
                            class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
                            onclick={addHookFilterMount}>Add</button
                        >
                    </div>
                    <p
                        class="mt-1 text-[11px] text-slate-400 dark:text-slate-500"
                    >
                        Only fire for events on these mounts. Leave empty for
                        all mounts.
                    </p>
                    {#if hookFilterMounts.length > 0}
                        <div class="mt-2 flex flex-wrap gap-1.5">
                            {#each hookFilterMounts as m, i}
                                <span
                                    class="inline-flex items-center gap-1 px-2 py-1 bg-accent-50 dark:bg-accent-900/40 border border-accent-200 dark:border-accent-700 rounded text-xs font-mono text-accent-700 dark:text-accent-300"
                                >
                                    {m}
                                    <button
                                        class="flex items-center justify-center w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-accent-400 hover:text-vermilion-500 transition-colors"
                                        onclick={() => removeHookFilterMount(i)}
                                        title="Remove"
                                    aria-label="Remove">&times;</button
                                    >
                                </span>
                            {/each}
                        </div>
                    {/if}
                </div>

                <!-- Path pattern -->
                <div>
                    <label
                        for="hook-path-pattern"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
                        >Path Pattern</label
                    >
                    <input
                        id="hook-path-pattern"
                        type="text"
                        bind:value={hookFilterPathPattern}
                        placeholder="e.g., *.pdf"
                        class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                    <p
                        class="mt-1 text-[11px] text-slate-400 dark:text-slate-500"
                    >
                        Glob pattern to match file paths. Leave empty for all
                        paths.
                    </p>
                </div>
            </div>

            <!-- Targets -->
            <div class="mb-4">
                <div class="flex items-center justify-between mb-2">
                    <p
                        class="text-xs font-semibold text-slate-500 dark:text-slate-400 uppercase tracking-wider"
                    >
                        Targets
                    </p>
                    <button
                        class="flex items-center gap-1 px-2 py-1 text-xs text-accent-700 bg-accent-50 border border-accent-200 rounded hover:bg-accent-100 transition-colors cursor-pointer dark:text-accent-300 dark:bg-accent-950/30 dark:border-accent-700 dark:hover:bg-accent-900/40"
                        onclick={() => {
                            showAddTarget = true;
                            resetTargetForm();
                        }}
                    >
                        <Plus size={12} />
                        Add Target
                    </button>
                </div>

                <!-- Target list -->
                {#if hookTargets.length > 0}
                    <div class="space-y-2 mb-3">
                        {#each hookTargets as t, i}
                            <div
                                class="flex items-center gap-3 p-3 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-md"
                            >
                                <div class="flex-1 min-w-0">
                                    <div class="flex items-center gap-2">
                                        <span
                                            class="px-1.5 py-0.5 text-[10px] font-medium rounded {targetTypeBadge[t.type] ?? targetTypeBadge.log}"
                                            >{t.type.toUpperCase()}</span
                                        >
                                        {#if t.type === "http" && t.http}
                                            <span
                                                class="text-xs text-slate-600 dark:text-slate-300 font-mono truncate"
                                                >{t.http.method || "POST"}
                                                {t.http.url}</span
                                            >
                                        {:else if t.type === "kafka" && t.kafka}
                                            <span
                                                class="text-xs text-slate-600 dark:text-slate-300 font-mono truncate"
                                                >{t.kafka.topic} ({t.kafka.brokers.join(
                                                    ", ",
                                                )})</span
                                            >
                                        {:else if t.type === "redis" && t.redis}
                                            <span
                                                class="text-xs text-slate-600 dark:text-slate-300 font-mono truncate"
                                                >{t.redis.channel} ({t.redis
                                                    .addresses?.length
                                                    ? t.redis.addresses.join(
                                                          ", ",
                                                      )
                                                    : t.redis.address})</span
                                            >
                                        {:else if t.type === "nats" && t.nats}
                                            <span
                                                class="text-xs text-slate-600 dark:text-slate-300 font-mono truncate"
                                                >{t.nats.subject} ({t.nats
                                                    .url})</span
                                            >
                                        {:else if t.type === "log" && t.log}
                                            <span
                                                class="text-xs text-slate-600 dark:text-slate-300 font-mono truncate"
                                                >{(
                                                    t.log.level || "info"
                                                ).toUpperCase()}{t.log.message
                                                    ? `: ${t.log.message}`
                                                    : ""}</span
                                            >
                                        {/if}
                                    </div>
                                    {#if t.body_template}
                                        <p
                                            class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500 truncate"
                                        >
                                            Template: {t.body_template}
                                        </p>
                                    {/if}
                                </div>
                                <div class="flex items-center gap-1 shrink-0">
                                    <button
                                        class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-accent-600 hover:bg-accent-50 dark:hover:bg-accent-900/30 rounded transition-colors cursor-pointer"
                                        onclick={() => handleEditTarget(i)}
                                        title="Edit"
                                        aria-label="Edit target"
                                    >
                                        <Pencil size={14} />
                                    </button>
                                    <button
                                        class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-vermilion-500 hover:bg-vermilion-50 dark:hover:bg-vermilion-900/40 rounded transition-colors cursor-pointer"
                                        onclick={() => removeTarget(i)}
                                        title="Remove"
                                        aria-label="Remove target"
                                    >
                                        <Trash2 size={14} />
                                    </button>
                                </div>
                            </div>
                        {/each}
                    </div>
                {:else if !showAddTarget}
                    <p class="text-xs text-slate-400 dark:text-slate-500 mb-3">
                        No targets added yet. Add at least one HTTP or Kafka
                        target.
                    </p>
                {/if}

                <!-- Add/Edit Target Form -->
                {#if showAddTarget}
                    {#key targetFormKey}
                        <HookTargetForm
                            initial={editingTargetIndex !== null
                                ? hookTargets[editingTargetIndex]
                                : undefined}
                            onSave={handleSaveTarget}
                            onCancel={() => {
                                showAddTarget = false;
                                resetTargetForm();
                            }}
                        />
                    {/key}
                {/if}
            </div>

            <!-- Save / Cancel -->
            <div class="flex justify-end gap-2">
                <button
                    class="px-3 py-2 text-sm text-slate-600 dark:text-slate-300 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 transition-colors cursor-pointer"
                    onclick={() => {
                        showAddHook = false;
                        resetHookForm();
                    }}>Cancel</button
                >
                <button
                    class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                    onclick={handleAddHook}
                    disabled={isSavingHooks}
                    >{isSavingHooks
                        ? "Saving..."
                        : editingHookIndex !== null
                          ? "Save Changes"
                          : "Add Hook"}</button
                >
            </div>
        </div>
    {/if}

    <!-- Hook List -->
    {#if hooks.length === 0 && !showAddHook}
        <div
            class="text-center py-12 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg"
        >
            <Webhook size={32} class="mx-auto text-slate-300 dark:text-slate-600 mb-3" />
            <p class="text-sm text-slate-500 dark:text-slate-400">
                No hooks configured
            </p>
            <p class="text-xs text-slate-400 dark:text-slate-500 mt-1">
                Add a hook to trigger notifications when files change
            </p>
        </div>
    {:else if !showAddHook}
        <div class="space-y-2">
            {#each hooks as h, i (h.name)}
                <div
                    class="p-4 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg hover:border-slate-300 dark:hover:border-warm-600 transition-colors"
                >
                    <div class="flex items-center gap-4">
                        <div class="flex-1 min-w-0">
                            <div class="flex items-center gap-2">
                                <span
                                    class="text-sm font-medium text-slate-800 dark:text-slate-100"
                                    >{h.name}</span
                                >
                                <span
                                    class="px-1.5 py-0.5 text-[10px] font-medium rounded {h.enabled
                                        ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
                                        : 'bg-slate-100 dark:bg-warm-700 text-slate-500 dark:text-slate-400'}"
                                >
                                    {h.enabled ? "Active" : "Disabled"}
                                </span>
                            </div>
                            <div class="mt-1 flex flex-wrap gap-1">
                                {#each h.events as ev}
                                    <span
                                        class="px-1.5 py-0.5 text-[10px] font-mono rounded bg-accent-50 text-accent-700 border border-accent-100 dark:bg-accent-900/40 dark:text-accent-300 dark:border-accent-700"
                                        >{ev}</span
                                    >
                                {/each}
                            </div>
                            {#if h.filter?.mounts?.length || h.filter?.path_pattern}
                                <div
                                    class="mt-1 flex flex-wrap gap-1 items-center"
                                >
                                    <span
                                        class="text-[10px] text-slate-400 dark:text-slate-500"
                                        >Filters:</span
                                    >
                                    {#if h.filter?.mounts}
                                        {#each h.filter.mounts as m}
                                            <span
                                                class="px-1.5 py-0.5 text-[10px] font-mono rounded bg-amber-50 text-amber-600 border border-amber-100 dark:bg-amber-950/40 dark:text-amber-300 dark:border-amber-700"
                                                >{m}</span
                                            >
                                        {/each}
                                    {/if}
                                    {#if h.filter?.path_pattern}
                                        <span
                                            class="px-1.5 py-0.5 text-[10px] font-mono rounded bg-violet-50 text-violet-600 border border-violet-100 dark:bg-violet-950/40 dark:text-violet-300 dark:border-violet-700"
                                            >{h.filter.path_pattern}</span
                                        >
                                    {/if}
                                </div>
                            {/if}
                            <div class="mt-1 flex flex-wrap gap-1">
                                {#each h.targets as t}
                                    <span
                                        class="px-1.5 py-0.5 text-[10px] font-medium rounded {targetTypeBadge[t.type] ?? targetTypeBadge.log}"
                                    >
                                        {t.type === "http" && t.http
                                            ? t.http.url
                                            : t.type === "kafka" && t.kafka
                                              ? t.kafka.topic
                                              : t.type === "redis" && t.redis
                                                ? t.redis.channel
                                                : t.type === "nats" && t.nats
                                                  ? t.nats.subject
                                                  : t.type === "log" && t.log
                                                    ? `log:${t.log.level || "info"}`
                                                    : t.type}
                                    </span>
                                {/each}
                            </div>
                        </div>
                        <div class="flex items-center gap-1 shrink-0">
                            <button
                                class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-emerald-500 hover:bg-emerald-50 dark:hover:bg-emerald-900/40 rounded transition-colors cursor-pointer"
                                onclick={() => handleToggleHook(i)}
                                title={h.enabled ? "Disable" : "Enable"}
                                aria-label={h.enabled ? `Disable hook ${h.name}` : `Enable hook ${h.name}`}
                            >
                                {#if h.enabled}
                                    <Eye size={14} />
                                {:else}
                                    <EyeOff size={14} />
                                {/if}
                            </button>
                            <button
                                class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-accent-600 hover:bg-accent-50 dark:hover:bg-accent-900/30 rounded transition-colors cursor-pointer"
                                onclick={() => handleEditHook(i)}
                                title="Edit hook"
                                aria-label="Edit hook {h.name}"
                            >
                                <Pencil size={14} />
                            </button>
                            <button
                                class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-vermilion-500 hover:bg-vermilion-50 dark:hover:bg-vermilion-900/40 rounded transition-colors cursor-pointer"
                                onclick={() => handleRemoveHook(i)}
                                title="Remove hook"
                                aria-label="Remove hook {h.name}"
                            >
                                <Trash2 size={14} />
                            </button>
                        </div>
                    </div>
                </div>
            {/each}
        </div>
    {/if}
</div>
