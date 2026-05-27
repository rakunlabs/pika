<script lang="ts">
    import { configStore } from "@/lib/store/config.svelte";
    import { addToast } from "@/lib/store/toast.svelte";
    import { onMount } from "svelte";
    import { Plus, Trash2, Copy, Eye, EyeOff, Shield } from "lucide-svelte";
    import type { TokenScope, CreateTokenRequest } from "@/lib/types/config";

    // ── Token state ──
    let showCreateToken = $state(false);
    let newTokenName = $state("");
    let newTokenScopes = $state<TokenScope[]>([
        { path: "**", operations: ["read"] },
    ]);
    let newTokenExpiry = $state("");
    let createdTokenKey = $state<string | null>(null);
    let showKey = $state(false);

    const tokens = $derived(configStore.tokens);

    onMount(() => {
        configStore.loadTokens();
    });

    // ── Utilities ──
    function formatDate(dateStr: string): string {
        return new Date(dateStr).toLocaleDateString(undefined, {
            year: "numeric",
            month: "short",
            day: "numeric",
            hour: "2-digit",
            minute: "2-digit",
        });
    }

    // ── Token handlers ──
    function addScope() {
        newTokenScopes = [
            ...newTokenScopes,
            { path: "", operations: ["read"] },
        ];
    }

    function removeScope(index: number) {
        newTokenScopes = newTokenScopes.filter((_, i) => i !== index);
    }

    function toggleOperation(index: number, op: string) {
        const scope = newTokenScopes[index];
        if (scope.operations.includes(op)) {
            scope.operations = scope.operations.filter((o) => o !== op);
        } else {
            scope.operations = [...scope.operations, op];
        }
        newTokenScopes = [...newTokenScopes];
    }

    async function handleCreateToken() {
        if (!newTokenName.trim()) {
            addToast("Token name is required", "alert");
            return;
        }
        if (newTokenScopes.some((s) => !s.path.trim())) {
            addToast("All scope paths are required", "alert");
            return;
        }

        try {
            const req: CreateTokenRequest = {
                name: newTokenName.trim(),
                scopes: newTokenScopes,
            };
            if (newTokenExpiry) {
                req.expires_at = new Date(newTokenExpiry).toISOString();
            }

            const result = await configStore.createToken(req);
            createdTokenKey = result.raw_key;
            showCreateToken = false;
            newTokenName = "";
            newTokenScopes = [{ path: "**", operations: ["read"] }];
            newTokenExpiry = "";
            addToast("Token created successfully", "success");
        } catch (error) {
            console.error("Failed to create token:", error);
            addToast("Failed to create token", "alert");
        }
    }

    async function handleDeleteToken(id: string) {
        if (!confirm("Are you sure you want to delete this token?")) return;
        try {
            await configStore.deleteToken(id);
        } catch (error) {
            addToast("Failed to delete token", "alert");
        }
    }

    async function handleToggleToken(id: string, active: boolean) {
        try {
            await configStore.patchToken(id, { active: !active });
        } catch (error) {
            addToast("Failed to update token", "alert");
        }
    }

    async function copyTokenKey() {
        if (createdTokenKey) {
            await navigator.clipboard.writeText(createdTokenKey);
            addToast("Token copied to clipboard", "success");
        }
    }

    function dismissTokenKey() {
        createdTokenKey = null;
    }
</script>

<!-- Token Created Banner -->
{#if createdTokenKey}
    <div class="mb-6 p-4 bg-green-50 border border-green-200 rounded-lg">
        <p class="text-sm font-semibold text-green-800 mb-2">
            Token Created Successfully
        </p>
        <p class="text-xs text-green-700 mb-3">
            Copy this token now. It will not be shown again.
        </p>
        <div class="flex items-center gap-2">
            <code
                class="flex-1 px-3 py-2 bg-white dark:bg-warm-900 border border-green-200 rounded text-xs font-mono text-green-900 overflow-hidden text-ellipsis"
            >
                {showKey ? createdTokenKey : "••••••••••••••••••••••••••••••••"}
            </code>
            <button
                class="p-2 bg-white dark:bg-warm-900 border border-green-200 rounded hover:bg-green-100 transition-colors cursor-pointer"
                onclick={() => (showKey = !showKey)}
                title={showKey ? "Hide" : "Show"}
            >
                {#if showKey}<EyeOff size={14} />{:else}<Eye size={14} />{/if}
            </button>
            <button
                class="p-2 bg-white dark:bg-warm-900 border border-green-200 rounded hover:bg-green-100 transition-colors cursor-pointer"
                onclick={copyTokenKey}
                title="Copy"
            >
                <Copy size={14} />
            </button>
        </div>
        <button
            class="mt-3 px-3 py-1.5 text-xs text-green-700 bg-transparent border border-green-300 rounded hover:bg-green-100 transition-colors cursor-pointer"
            onclick={dismissTokenKey}
        >
            Dismiss
        </button>
    </div>
{/if}

<!-- Access Tokens Section -->
<div>
    <div class="flex items-center justify-between mb-4">
        <div>
            <h2
                class="text-lg font-semibold text-slate-800 dark:text-slate-100"
            >
                Access Tokens
            </h2>
            <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
                Tokens authenticate consumers accessing configs via the data API
            </p>
        </div>
        <button
            class="flex items-center gap-1.5 px-3 py-2 bg-accent-600 text-white text-sm font-medium rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
            onclick={() => (showCreateToken = true)}
        >
            <Plus size={14} />
            New Token
        </button>
    </div>

    <!-- Create Token Form -->
    {#if showCreateToken}
        <div
            class="mb-6 p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm"
        >
            <h3
                class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-4"
            >
                Create New Token
            </h3>

            <div class="mb-4">
                <label
                    for="token-name"
                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
                    >Name</label
                >
                <input
                    id="token-name"
                    type="text"
                    bind:value={newTokenName}
                    placeholder="e.g., production-reader"
                    class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                />
            </div>

            <div class="mb-4">
                <label
                    for="token-expiry"
                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
                    >Expires At (optional)</label
                >
                <input
                    id="token-expiry"
                    type="datetime-local"
                    bind:value={newTokenExpiry}
                    class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                />
            </div>

            <div class="mb-4">
                <div class="flex items-center justify-between mb-2">
                    <span
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400"
                        >Scopes</span
                    >
                    <button
                        class="flex items-center gap-1 px-2 py-1 text-xs text-accent-700 bg-accent-50 rounded hover:bg-accent-100 transition-colors cursor-pointer"
                        onclick={addScope}
                    >
                        <Plus size={12} /> Add Scope
                    </button>
                </div>

                {#each newTokenScopes as scope, i (i)}
                    <div
                        class="flex items-start gap-2 mb-2 p-3 bg-slate-50 dark:bg-warm-900 rounded-md border border-slate-100 dark:border-warm-700"
                    >
                        <div class="flex-1">
                            <input
                                type="text"
                                bind:value={scope.path}
                                placeholder="Path pattern (e.g., app/**, production/*)"
                                class="w-full px-2.5 py-1.5 text-xs font-mono border border-slate-200 dark:border-warm-700 rounded focus:outline-none focus:border-accent-500"
                            />
                            <div class="flex gap-2 mt-2">
                                {#each ["read", "write", "delete"] as op}
                                    <label
                                        class="flex items-center gap-1 text-xs text-slate-600 dark:text-slate-300 cursor-pointer"
                                    >
                                        <input
                                            type="checkbox"
                                            checked={scope.operations.includes(
                                                op,
                                            )}
                                            onchange={() =>
                                                toggleOperation(i, op)}
                                            class="rounded border-slate-300"
                                        />
                                        {op}
                                    </label>
                                {/each}
                            </div>
                        </div>
                        {#if newTokenScopes.length > 1}
                            <button
                                class="p-1 text-slate-400 dark:text-slate-500 hover:text-red-500 transition-colors cursor-pointer"
                                onclick={() => removeScope(i)}
                            >
                                <Trash2 size={14} />
                            </button>
                        {/if}
                    </div>
                {/each}
            </div>

            <div class="flex justify-end gap-2">
                <button
                    class="px-3 py-2 text-sm text-slate-600 dark:text-slate-300 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
                    onclick={() => (showCreateToken = false)}
                >
                    Cancel
                </button>
                <button
                    class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
                    onclick={handleCreateToken}
                >
                    Create Token
                </button>
            </div>
        </div>
    {/if}

    <!-- Token List -->
    {#if tokens.length === 0}
        <div
            class="text-center py-12 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg"
        >
            <Shield size={32} class="mx-auto text-slate-300 mb-3" />
            <p class="text-sm text-slate-500 dark:text-slate-400">
                No access tokens yet
            </p>
            <p class="text-xs text-slate-400 dark:text-slate-500 mt-1">
                Create a token to allow consumers to access configurations
            </p>
        </div>
    {:else}
        <div class="space-y-2">
            {#each tokens as token (token.id)}
                <div
                    class="flex items-center gap-4 p-4 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg hover:border-slate-300 transition-colors"
                >
                    <div class="flex-1 min-w-0">
                        <div class="flex items-center gap-2">
                            <span
                                class="text-sm font-medium text-slate-800 dark:text-slate-100"
                                >{token.name}</span
                            >
                            <span
                                class="px-1.5 py-0.5 text-[10px] font-medium rounded
 {token.active
                                    ? 'bg-green-100 text-green-700'
                                    : 'bg-slate-100 dark:bg-warm-900 text-slate-500 dark:text-slate-400'}"
                            >
                                {token.active ? "Active" : "Disabled"}
                            </span>
                        </div>
                        <div class="flex gap-3 mt-1 flex-wrap">
                            <span
                                class="text-xs text-slate-400 dark:text-slate-500"
                                >Created: {formatDate(token.created_at)}</span
                            >
                            {#if token.created_by}
                                <span
                                    class="text-xs text-slate-400 dark:text-slate-500"
                                    >by: <span
                                        class="text-slate-600 dark:text-slate-300"
                                        >{token.created_by}</span
                                    ></span
                                >
                            {/if}
                            {#if token.expires_at}
                                <span
                                    class="text-xs text-slate-400 dark:text-slate-500"
                                    >Expires: {formatDate(
                                        token.expires_at,
                                    )}</span
                                >
                            {/if}
                        </div>
                        <div class="flex flex-wrap gap-1.5 mt-2">
                            {#each token.scopes as scope}
                                <span
                                    class="px-2 py-0.5 text-[10px] font-mono bg-slate-100 dark:bg-warm-900 text-slate-600 dark:text-slate-300 rounded"
                                >
                                    {scope.operations.join(",")}:{scope.path}
                                </span>
                            {/each}
                        </div>
                    </div>
                    <div class="flex items-center gap-1 shrink-0">
                        <button
                            class="px-2.5 py-1.5 text-xs rounded transition-colors
 {token.active
                                ? 'text-amber-600 bg-amber-50 hover:bg-amber-100'
                                : 'text-green-600 bg-green-50 hover:bg-green-100'} cursor-pointer"
                            onclick={() =>
                                handleToggleToken(token.id, token.active)}
                        >
                            {token.active ? "Disable" : "Enable"}
                        </button>
                        <button
                            class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-red-500 hover:bg-red-50 rounded transition-colors cursor-pointer"
                            onclick={() => handleDeleteToken(token.id)}
                            title="Delete token"
                        >
                            <Trash2 size={14} />
                        </button>
                    </div>
                </div>
            {/each}
        </div>
    {/if}
</div>
