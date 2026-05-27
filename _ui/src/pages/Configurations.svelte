<script lang="ts">
    import { configStore } from "@/lib/store/config.svelte";
    import { appStore } from "@/lib/store/store.svelte";
    import FileTree from "@/lib/components/config/FileTree.svelte";
    import TabBar from "@/lib/components/config/TabBar.svelte";
    import Editor from "@/lib/components/config/Editor.svelte";
    import SettingsPanel from "@/lib/components/config/SettingsPanel.svelte";
    import RenderPreview from "@/lib/components/config/RenderPreview.svelte";
    import ResizablePanel from "@/lib/components/config/ResizablePanel.svelte";
    import { Blocks } from "lucide-svelte";

    let showRenderPreview = $state(false);
    let openedFromURL = $state(false);

    // Permission gate: /configurations is the default landing page (see
    // routes.ts) so a freshly-created user with no roles lands here even
    // though they can't see any mounts. Previously that showed an empty
    // FileTree + empty Editor — confusing because the page chrome implied
    // "there should be files here". When the read capability is absent
    // we render a branded welcome card instead, mirroring the navbar
    // gate at Navbar.svelte:20 so behaviour stays consistent: the nav
    // entry is hidden AND the page itself shows the friendlier state.
    const canRead = $derived(appStore.hasPermission("files.read"));
    const info = $derived(appStore.info);

    $effect(() => {
        // Open file from URL deep link (e.g., #/configurations?file=app/db&variant=prod)
        // only after the files.read capability is known. On a hard refresh
        // /login/me can resolve before /api/v1/info, making canRead false
        // during component init; this effect retries once canRead flips true.
        if (!canRead || openedFromURL) return;
        openedFromURL = true;
        configStore.openFromURL();
    });

    function handleRenderClick() {
        showRenderPreview = true;
    }

    function closeRenderPreview() {
        showRenderPreview = false;
    }
</script>

{#if canRead}
    <div
        class="flex h-full w-full overflow-hidden bg-slate-100 dark:bg-warm-900"
    >
        <!-- Left Panel: File Tree -->
        <ResizablePanel
            width={configStore.leftPanelWidth}
            minWidth={180}
            maxWidth={400}
            side="left"
            onResize={(w) => configStore.setLeftPanelWidth(w)}
        >
            <FileTree />
        </ResizablePanel>

        <!-- Center: Editor Area -->
        <div class="flex-1 flex flex-col min-w-0 overflow-hidden">
            <TabBar />
            <div class="flex-1 overflow-hidden">
                <Editor />
            </div>
        </div>

        <!-- Right Panel: Settings -->
        <ResizablePanel
            width={configStore.rightPanelWidth}
            minWidth={220}
            maxWidth={400}
            side="right"
            onResize={(w) => configStore.setRightPanelWidth(w)}
        >
            <SettingsPanel onRender={handleRenderClick} />
        </ResizablePanel>
    </div>

    <!-- Render Preview Modal -->
    <RenderPreview isOpen={showRenderPreview} onClose={closeRenderPreview} />
{:else}
    <!-- Welcome / no-permission landing. Visual recipe mirrors the
 navbar branding cluster (Navbar.svelte:64-77): same Blocks icon
 in the same accent red, same "pika" wordmark, same optional
 subtitle source. The version sits underneath in a muted mono
 font to match the build-metadata pill on the right side of the
 navbar (Navbar.svelte:109). Keeping the visual vocabulary
 identical means the empty state reads as "this is pika, you're
 in the right place" rather than "something is missing".

 No call-to-action button: the user genuinely cannot act from
 here, and a button that links to a page they also can't access
 would just bounce them back. The single line of helper copy
 below the version is enough — they know what to do next. -->
    <div
        class="flex h-full w-full items-center justify-center bg-slate-100 dark:bg-warm-900 px-4"
    >
        <div class="flex flex-col items-center max-w-sm">
            <!-- Brand cluster: logo on the left, wordmark on the right.
 When a subtitle is configured (auth settings → UI subtitle),
 the wordmark and subtitle stack vertically so the logo
 visually anchors a two-line text block — like a product
 logo with tagline. This matches the navbar's brand layout
 (Navbar.svelte:69-77) at a larger scale. Without a subtitle
 it's just logo + wordmark side by side, no inner stack. -->
            <div class="flex items-center gap-4">
                <Blocks size={64} color="#EF233C" strokeWidth={1.5} />
                <div class="flex flex-col items-start leading-tight">
                    <h1
                        class="text-5xl font-bold tracking-wide text-slate-800 dark:text-slate-100"
                    >
                        pika
                    </h1>
                    {#if info?.subtitle}
                        <p
                            class="mt-1 text-sm text-slate-500 dark:text-slate-400"
                        >
                            {info.subtitle}
                        </p>
                    {/if}
                </div>
            </div>
            {#if info?.version}
                <p
                    class="mt-3 text-xs font-mono text-slate-400 dark:text-slate-500 select-text"
                >
                    {info.version}
                </p>
            {/if}
            <p
                class="mt-8 text-sm text-slate-500 dark:text-slate-400 text-center"
            >
                Your account doesn't have access to any resources yet. Contact
                your administrator to request permissions.
            </p>
        </div>
    </div>
{/if}
