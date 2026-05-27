<!--
  Modal — generic dialog wrapper with the four behaviours every
  modal in the app should share, baked in once:

    1. Backdrop click closes the modal (via the `backdropClose`
       action that handles the drag-leak case — see
       lib/actions/backdropClose.ts).
    2. Escape key closes the modal.
    3. role="dialog" + aria-modal="true" for screen readers.
    4. A focusable scroll container with tabindex so keyboard
       navigation enters the modal cleanly.

  Usage:
      <Modal open={isOpen} onClose={() => isOpen = false}>
        {#snippet header()}<h2>Title</h2>{/snippet}
        <div>...modal body...</div>
        {#snippet footer()}<button>Save</button>{/snippet}
      </Modal>

  The default slot is the body. `header` and `footer` are
  optional named snippets — leave them out for a plain body.

  Size: pass one of 'sm' | 'md' | 'lg' | 'xl' | 'full' to control
  the inner panel width. Defaults to 'md'.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import type { Snippet } from "svelte";
  import { backdropClose } from "@/lib/actions/backdropClose";

  type Size = "sm" | "md" | "lg" | "xl" | "full";

  type Props = {
    /** Whether the modal is open. Caller controls visibility. */
    open: boolean;
    /** Called when backdrop is clicked or Escape is pressed. */
    onClose: () => void;
    /** Inner panel width preset. Defaults to 'md'. */
    size?: Size;
    /** Optional ARIA label for the dialog (used when no visible header). */
    ariaLabel?: string;
    /** Snippet for the header strip — typically an h2 + close button. */
    header?: Snippet;
    /** Default slot for the body content. */
    children?: Snippet;
    /** Snippet for the footer strip — typically action buttons. */
    footer?: Snippet;
  };

  let {
    open,
    onClose,
    size = "md",
    ariaLabel,
    header,
    children,
    footer,
  }: Props = $props();

  // Tailwind width classes per size preset. Kept here so call
  // sites don't have to redeclare them; adjust once for global
  // consistency.
  const sizeClass: Record<Size, string> = {
    sm: "max-w-md",
    md: "max-w-2xl",
    lg: "max-w-4xl",
    xl: "max-w-6xl",
    full: "max-w-full mx-4",
  };

  // Escape-to-close. Bound to window for the duration the modal is
  // open so a keypress with focus anywhere on the page still
  // triggers the dismiss.
  onMount(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && open) {
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
</script>

{#if open}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 dark:bg-black/60"
    use:backdropClose={onClose}
    role="dialog"
    aria-modal="true"
    aria-label={ariaLabel}
    tabindex="-1"
  >
    <div
      class="relative bg-white dark:bg-warm-900 rounded shadow-xl border border-warm-200 dark:border-warm-800 w-full {sizeClass[
        size
      ]} max-h-[90vh] flex flex-col"
      role="document"
    >
      {#if header}
        <header
          class="flex items-center justify-between px-4 py-3 border-b border-warm-200 dark:border-warm-800 shrink-0"
        >
          {@render header()}
        </header>
      {/if}

      <div class="flex-1 overflow-y-auto">
        {@render children?.()}
      </div>

      {#if footer}
        <footer
          class="flex items-center justify-end gap-2 px-4 py-3 border-t border-warm-200 dark:border-warm-800 shrink-0"
        >
          {@render footer()}
        </footer>
      {/if}
    </div>
  </div>
{/if}
