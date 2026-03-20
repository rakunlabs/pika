<script lang="ts">
  import { configStore } from "@/lib/store/config.svelte";
  import FileTree from "@/lib/components/config/FileTree.svelte";
  import TabBar from "@/lib/components/config/TabBar.svelte";
  import Editor from "@/lib/components/config/Editor.svelte";
  import SettingsPanel from "@/lib/components/config/SettingsPanel.svelte";
  import RenderPreview from "@/lib/components/config/RenderPreview.svelte";
  import ResizablePanel from "@/lib/components/config/ResizablePanel.svelte";
  import { onMount } from "svelte";

  let showRenderPreview = $state(false);

  onMount(() => {
    // Open file from URL deep link (e.g., #/configurations?file=app/db&variant=prod)
    configStore.openFromURL();
  });

  function handleRenderClick() {
    showRenderPreview = true;
  }

  function closeRenderPreview() {
    showRenderPreview = false;
  }
</script>

<div class="flex h-full w-full overflow-hidden bg-slate-100 dark:bg-slate-900">
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
<RenderPreview 
  isOpen={showRenderPreview} 
  onClose={closeRenderPreview} 
/>
