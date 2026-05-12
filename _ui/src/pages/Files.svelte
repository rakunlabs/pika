<script lang="ts">
 import RawFileTree from "@/lib/components/files/RawFileTree.svelte";
 import RawTabBar from "@/lib/components/files/RawTabBar.svelte";
 import RawViewer from "@/lib/components/files/RawViewer.svelte";
 import RawFileInfo from "@/lib/components/files/RawFileInfo.svelte";
 import ResizablePanel from "@/lib/components/config/ResizablePanel.svelte";
 import { filesStore } from "@/lib/store/files.svelte";
</script>

<div class="flex h-full w-full overflow-hidden bg-slate-100 dark:bg-warm-900">
 <!-- Left Panel: File Tree -->
 <ResizablePanel
 width={filesStore.leftPanelWidth}
 minWidth={180}
 maxWidth={400}
 side="left"
 onResize={(w) => filesStore.setLeftPanelWidth(w)}
 >
 <RawFileTree />
 </ResizablePanel>

 <!-- Center: Viewer Area -->
 <div class="flex-1 flex flex-col min-w-0 overflow-hidden">
 <RawTabBar />
 <div class="flex-1 overflow-hidden">
 <RawViewer />
 </div>
 </div>

 <!-- Right Panel: File Info -->
 <ResizablePanel
 width={filesStore.rightPanelWidth}
 minWidth={180}
 maxWidth={400}
 side="right"
 onResize={(w) => filesStore.setRightPanelWidth(w)}
 >
 <RawFileInfo />
 </ResizablePanel>
</div>
