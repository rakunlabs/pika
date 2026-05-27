<script lang="ts">
    import {
        removeToast,
        storeToast,
        type ToastType,
    } from "@/lib/store/toast.svelte";
    import {
        X,
        CheckCircle,
        Info,
        AlertTriangle,
        AlertCircle,
    } from "lucide-svelte";
    import { onDestroy } from "svelte";

    // Track animation frame for progress bars
    let frameId: number | null = null;
    let now = $state(Date.now());

    function tick() {
        now = Date.now();
        frameId = requestAnimationFrame(tick);
    }

    $effect(() => {
        if (storeToast.length > 0 && frameId === null) {
            frameId = requestAnimationFrame(tick);
        } else if (storeToast.length === 0 && frameId !== null) {
            cancelAnimationFrame(frameId);
            frameId = null;
        }
    });

    onDestroy(() => {
        if (frameId !== null) cancelAnimationFrame(frameId);
    });

    function getProgress(toast: {
        createdAt: number;
        duration: number;
    }): number {
        if (toast.duration <= 0) return 100;
        const elapsed = now - toast.createdAt;
        return Math.max(0, 100 - (elapsed / toast.duration) * 100);
    }

    const iconMap: Record<ToastType, typeof Info> = {
        info: Info,
        success: CheckCircle,
        warn: AlertTriangle,
        alert: AlertCircle,
    };

    const config: Record<
        ToastType,
        {
            bg: string;
            border: string;
            text: string;
            icon: string;
            progress: string;
        }
    > = {
        info: {
            bg: "bg-slate-800",
            border: "border-slate-700",
            text: "text-slate-100",
            // Info uses the cool palette so it stays visually distinct from
            // the brand red used for primary actions and the red used for
            // alerts. Brand red on an info toast would muddle the semantics.
            icon: "text-cool-300",
            progress: "bg-cool-300",
        },
        success: {
            bg: "bg-slate-800",
            border: "border-slate-700",
            text: "text-slate-100",
            icon: "text-emerald-400",
            progress: "bg-emerald-400",
        },
        warn: {
            bg: "bg-slate-800",
            border: "border-slate-700",
            text: "text-slate-100",
            icon: "text-amber-400",
            progress: "bg-amber-400",
        },
        alert: {
            bg: "bg-slate-800",
            border: "border-slate-700",
            text: "text-slate-100",
            icon: "text-red-400",
            progress: "bg-red-400",
        },
    };

    function slideIn(node: HTMLElement, { duration }: { duration: number }) {
        return {
            duration,
            css: (t: number) => {
                const eased = 1 - Math.pow(1 - t, 3); // ease-out cubic
                return `
 transform: translateX(${(1 - eased) * 120}%);
 opacity: ${eased};
 `;
            },
        };
    }

    function slideOut(node: HTMLElement, { duration }: { duration: number }) {
        return {
            duration,
            css: (t: number) => {
                const eased = 1 - Math.pow(1 - t, 3);
                return `
 transform: translateX(${(1 - eased) * 120}%);
 opacity: ${eased};
 max-height: ${eased * 80}px;
 margin-bottom: ${eased * 8}px;
 overflow: hidden;
 `;
            },
        };
    }
</script>

<div
    class="fixed bottom-3 right-3 z-[200] flex flex-col-reverse gap-2 w-80 pointer-events-none"
>
    {#each storeToast as toast (toast.id)}
        {@const style = config[toast.type]}
        {@const Icon = iconMap[toast.type]}
        {@const progress = getProgress(toast)}
        <div
            class="{style.bg} {style.text} border {style.border} rounded-lg shadow-lg shadow-black/20 pointer-events-auto overflow-hidden"
            in:slideIn={{ duration: 250 }}
            out:slideOut={{ duration: 200 }}
        >
            <div class="flex items-start gap-2.5 px-3 py-2.5">
                <span class="{style.icon} shrink-0 mt-0.5">
                    <Icon size={16} strokeWidth={2} />
                </span>
                <p class="flex-1 text-[13px] leading-snug m-0 break-words">
                    {toast.message}
                </p>
                <button
                    class="shrink-0 p-0.5 text-slate-500 dark:text-slate-400 bg-transparent border-none rounded cursor-pointer transition-colors hover:text-slate-200 hover:bg-slate-700"
                    onclick={() => removeToast(toast.id)}
                    aria-label="Dismiss"
                >
                    <X size={14} />
                </button>
            </div>
            {#if toast.duration > 0}
                <div class="h-[2px] w-full bg-slate-700/50">
                    <div
                        class="{style.progress} h-full transition-none"
                        style="width: {progress}%"
                    ></div>
                </div>
            {/if}
        </div>
    {/each}
</div>
