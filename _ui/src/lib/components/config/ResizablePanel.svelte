<script lang="ts">
    interface Props {
        width: number;
        minWidth?: number;
        maxWidth?: number;
        side?: "left" | "right";
        onResize?: (width: number) => void;
        children?: import("svelte").Snippet;
    }

    let {
        width,
        minWidth = 150,
        maxWidth = 500,
        side = "left",
        onResize,
        children,
    }: Props = $props();

    let isDragging = $state(false);
    let startX = 0;
    let startWidth = 0;

    function handleMouseDown(e: MouseEvent) {
        isDragging = true;
        startX = e.clientX;
        startWidth = width;
        document.addEventListener("mousemove", handleMouseMove);
        document.addEventListener("mouseup", handleMouseUp);
        document.body.style.cursor = "col-resize";
        document.body.style.userSelect = "none";
    }

    function handleMouseMove(e: MouseEvent) {
        if (!isDragging) return;

        const delta = side === "left" ? e.clientX - startX : startX - e.clientX;

        const newWidth = Math.max(
            minWidth,
            Math.min(maxWidth, startWidth + delta),
        );

        if (onResize) {
            onResize(newWidth);
        }
    }

    function handleMouseUp() {
        isDragging = false;
        document.removeEventListener("mousemove", handleMouseMove);
        document.removeEventListener("mouseup", handleMouseUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
    }
</script>

<div class="relative flex flex-row h-full shrink-0" style="width: {width}px;">
    <div class="flex-1 overflow-hidden h-full">
        {#if children}
            {@render children()}
        {/if}
    </div>

    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
        class="absolute top-0 bottom-0 w-1 bg-transparent cursor-col-resize z-10 transition-colors duration-150 hover:bg-vermilion-500 {side ===
        'left'
            ? 'right-0'
            : 'left-0'}"
        class:bg-vermilion-500={isDragging}
        onmousedown={handleMouseDown}
        role="separator"
        aria-orientation="vertical"
        aria-valuenow={width}
        aria-valuemin={minWidth}
        aria-valuemax={maxWidth}
        tabindex="0"
    ></div>
</div>
