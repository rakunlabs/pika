<script lang="ts">
    import { onMount } from "svelte";

    interface Props {
        data: string; // Base64 encoded data
    }

    let { data }: Props = $props();

    const ROW_HEIGHT = 20; // px per row
    const BYTES_PER_ROW = 16;
    const BUFFER_ROWS = 10; // extra rows above/below viewport

    let scrollContainer: HTMLDivElement | undefined = $state();
    let containerHeight = $state(0);
    let scrollTop = $state(0);

    // Decode base64 to Uint8Array
    const bytes = $derived.by(() => {
        try {
            const binaryStr = atob(data);
            return Uint8Array.from(binaryStr, (c) => c.charCodeAt(0));
        } catch {
            return new Uint8Array(0);
        }
    });

    const totalRows = $derived(
        Math.max(1, Math.ceil(bytes.length / BYTES_PER_ROW)),
    );
    const totalHeight = $derived(totalRows * ROW_HEIGHT);

    // Virtual scroll: which rows to render
    const visibleStart = $derived(
        Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - BUFFER_ROWS),
    );
    const visibleEnd = $derived(
        Math.min(
            totalRows,
            Math.ceil((scrollTop + containerHeight) / ROW_HEIGHT) + BUFFER_ROWS,
        ),
    );

    // Build visible rows
    const visibleRows = $derived.by(() => {
        const result: { index: number; line: string }[] = [];

        for (let i = visibleStart; i < visibleEnd; i++) {
            const offset = i * BYTES_PER_ROW;
            const chunk = bytes.slice(offset, offset + BYTES_PER_ROW);

            // Offset
            const offsetStr = offset.toString(16).padStart(8, "0");

            // Hex bytes: two groups of 8
            let hexLeft = "";
            let hexRight = "";
            let ascii = "";

            for (let j = 0; j < BYTES_PER_ROW; j++) {
                const hex =
                    j < chunk.length
                        ? chunk[j].toString(16).padStart(2, "0")
                        : " ";
                if (j < 8) {
                    hexLeft += (j > 0 ? " " : "") + hex;
                } else {
                    hexRight += (j > 8 ? " " : "") + hex;
                }

                if (j < chunk.length) {
                    const b = chunk[j];
                    ascii +=
                        b >= 0x20 && b <= 0x7e ? String.fromCharCode(b) : ".";
                }
            }

            result.push({
                index: i,
                line: `${offsetStr} ${hexLeft} ${hexRight} |${ascii}|`,
            });
        }

        if (result.length === 0) {
            result.push({ index: 0, line: "00000000 ||" });
        }

        return result;
    });

    function formatSize(n: number): string {
        if (n < 1024) return `${n} bytes`;
        if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
        return `${(n / (1024 * 1024)).toFixed(1)} MB`;
    }

    function handleScroll() {
        if (scrollContainer) {
            scrollTop = scrollContainer.scrollTop;
        }
    }

    onMount(() => {
        if (scrollContainer) {
            containerHeight = scrollContainer.clientHeight;
            const observer = new ResizeObserver((entries) => {
                for (const entry of entries) {
                    containerHeight = entry.contentRect.height;
                }
            });
            observer.observe(scrollContainer);
            return () => observer.disconnect();
        }
    });
</script>

<div class="flex flex-col h-full bg-[#1e1e1e] font-mono text-[13px]">
    <!-- Header -->
    <div
        class="px-4 py-1.5 bg-[#252526] border-b border-[#3c3c3c] text-[10px] text-gray-500 dark:text-slate-400 select-none shrink-0 whitespace-pre"
    >
        Offset 00 01 02 03 04 05 06 07 08 09 0A 0B 0C 0D 0E 0F Decoded text
    </div>

    <!-- Virtual scroll container -->
    <div
        class="flex-1 min-h-0 overflow-auto"
        bind:this={scrollContainer}
        onscroll={handleScroll}
    >
        <div style="height: {totalHeight}px; position: relative;">
            {#each visibleRows as row (row.index)}
                <div
                    class="absolute left-0 right-0 px-4 leading-5 text-[#d4d4d4] hover:bg-[#2a2d2e] whitespace-pre"
                    style="top: {row.index *
                        ROW_HEIGHT}px; height: {ROW_HEIGHT}px;"
                >
                    {row.line}
                </div>
            {/each}
        </div>
    </div>

    <!-- Footer -->
    <div
        class="flex items-center justify-between px-4 py-1 bg-[#252526] border-t border-[#3c3c3c] text-[10px] text-gray-500 dark:text-slate-400 shrink-0"
    >
        <span>{formatSize(bytes.length)}</span>
        <span>{totalRows.toLocaleString()} rows</span>
    </div>
</div>
