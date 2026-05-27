<script lang="ts">
    import { appStore } from "@/lib/store/store.svelte";
    import { ExternalLink, Copy, Check } from "lucide-svelte";
    import { addToast } from "@/lib/store/toast.svelte";

    // Canonical pika repo on GitHub. Used to build commit/release links.
    // Kept as a local constant rather than read from build config because the
    // value is fixed for this project and inlining keeps the About page
    // self-contained.
    const REPO_URL = "https://github.com/rakunlabs/pika";

    const info = $derived(appStore.info);

    // ldflags use a placeholder of "-" when the build was not stamped (e.g.
    // `go run` without -ldflags). Treat that as "unknown" for display.
    const hasCommit = $derived(!!info?.commit && info.commit !== "-");
    const hasDate = $derived(!!info?.date && info.date !== "-");
    const hasVersion = $derived(!!info?.version && info.version !== "v0.0.0");

    // Commit URLs work with short hashes on GitHub.
    const commitUrl = $derived(
        hasCommit ? `${REPO_URL}/commit/${info?.commit}` : "",
    );
    // Release URL only meaningful when version looks like a real tag.
    const versionUrl = $derived(
        hasVersion ? `${REPO_URL}/releases/tag/${info?.version}` : "",
    );

    // Pika stamps build date as "YYYY-MM-DD_HH:MM:SS" (UTC). Render that as a
    // friendlier "YYYY-MM-DD HH:MM:SS UTC" without trying to localize — the
    // value is already a build artifact, not a user-relative timestamp.
    const formattedDate = $derived.by(() => {
        if (!hasDate) return "";
        return (info?.date ?? "").replace("_", " ") + " UTC";
    });

    let copied = $state<string | null>(null);

    async function copyToClipboard(value: string, label: string) {
        try {
            await navigator.clipboard.writeText(value);
            copied = label;
            addToast(`${label} copied to clipboard`, "success");
            setTimeout(() => {
                if (copied === label) copied = null;
            }, 2000);
        } catch {
            addToast("Failed to copy to clipboard", "alert");
        }
    }
</script>

<div>
    <div class="mb-4">
        <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">
            About
        </h2>
        <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
            Build and source information for this pika instance
        </p>
    </div>

    <div
        class="p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm"
    >
        <dl class="divide-y divide-slate-100">
            <!-- Name -->
            <div class="flex items-baseline gap-4 py-2.5 first:pt-0">
                <dt
                    class="w-32 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
                >
                    Name
                </dt>
                <dd class="flex-1 text-sm text-slate-800 dark:text-slate-100">
                    {info?.name ?? "pika"}
                </dd>
            </div>

            <!-- Version -->
            <div class="flex items-baseline gap-4 py-2.5">
                <dt
                    class="w-32 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
                >
                    Version
                </dt>
                <dd
                    class="flex-1 text-sm text-slate-800 dark:text-slate-100 flex items-center gap-2"
                >
                    <span class="font-mono">{info?.version ?? "unknown"}</span>
                    {#if hasVersion}
                        <a
                            href={versionUrl}
                            target="_blank"
                            rel="noopener noreferrer"
                            class="inline-flex items-center gap-1 text-xs text-accent-600 hover:text-accent-700 dark:text-accent-300 dark:hover:text-accent-200 cursor-pointer"
                            title="View release on GitHub"
                        >
                            <ExternalLink size={12} />
                            release
                        </a>
                    {/if}
                </dd>
            </div>

            <!-- Commit -->
            <div class="flex items-baseline gap-4 py-2.5">
                <dt
                    class="w-32 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
                >
                    Commit
                </dt>
                <dd
                    class="flex-1 text-sm text-slate-800 dark:text-slate-100 flex items-center gap-2"
                >
                    {#if hasCommit}
                        <a
                            href={commitUrl}
                            target="_blank"
                            rel="noopener noreferrer"
                            class="font-mono text-accent-600 hover:text-accent-700 dark:text-accent-300 dark:hover:text-accent-200 hover:underline cursor-pointer"
                            title="View commit on GitHub"
                        >
                            {info?.commit}
                        </a>
                        <button
                            type="button"
                            class="inline-flex items-center text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200 cursor-pointer"
                            onclick={() =>
                                copyToClipboard(info?.commit ?? "", "Commit")}
                            title="Copy commit hash"
                        >
                            {#if copied === "Commit"}
                                <Check size={12} class="text-green-600" />
                            {:else}
                                <Copy size={12} />
                            {/if}
                        </button>
                    {:else}
                        <span class="text-slate-400 dark:text-slate-500 italic"
                            >unknown</span
                        >
                    {/if}
                </dd>
            </div>

            <!-- Build date -->
            <div class="flex items-baseline gap-4 py-2.5">
                <dt
                    class="w-32 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
                >
                    Build Date
                </dt>
                <dd class="flex-1 text-sm text-slate-800 dark:text-slate-100">
                    {#if hasDate}
                        <span class="font-mono">{formattedDate}</span>
                    {:else}
                        <span class="text-slate-400 dark:text-slate-500 italic"
                            >unknown</span
                        >
                    {/if}
                </dd>
            </div>

            <!-- Repository -->
            <div class="flex items-baseline gap-4 py-2.5">
                <dt
                    class="w-32 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
                >
                    Repository
                </dt>
                <dd
                    class="flex-1 text-sm text-slate-800 dark:text-slate-100 flex items-center gap-2"
                >
                    <a
                        href={REPO_URL}
                        target="_blank"
                        rel="noopener noreferrer"
                        class="text-accent-600 hover:text-accent-700 dark:text-accent-300 dark:hover:text-accent-200 hover:underline cursor-pointer break-all"
                    >
                        {REPO_URL}
                    </a>
                    <a
                        href={REPO_URL}
                        target="_blank"
                        rel="noopener noreferrer"
                        class="inline-flex items-center text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200 cursor-pointer"
                        title="Open in new tab"
                    >
                        <ExternalLink size={12} />
                    </a>
                </dd>
            </div>
        </dl>
    </div>
</div>
