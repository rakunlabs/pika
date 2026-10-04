<!--
  Modal — generic dialog wrapper with the behaviours every modal in
  the app should share, baked in once:

    1. Backdrop click closes the modal (via the `backdropClose`
       action that handles the drag-leak case — see
       lib/actions/backdropClose.ts). Disable with
       `closeOnBackdrop={false}` for destructive confirmations.
    2. Escape key closes the modal.
    3. role="dialog" + aria-modal="true" + aria-labelledby /
       aria-label for screen readers.
    4. Focus moves into the dialog on open (first element marked
       `data-autofocus`, else the first focusable element, else the
       panel itself), Tab / Shift+Tab are trapped inside, and focus
       is restored to the previously focused element on close.

  Usage:
      <Modal open={isOpen} onClose={() => isOpen = false} labelledby="my-title">
        {#snippet header()}<h2 id="my-title">Title</h2>{/snippet}
        <div>...modal body...</div>
        {#snippet footer()}<button>Save</button>{/snippet}
      </Modal>

  The default slot is the body. `header` and `footer` are optional
  named snippets — leave them out for a plain body.

  Size: pass one of 'sm' | 'md' | 'lg' | 'xl' | 'full' to control
  the inner panel width. Defaults to 'md'. `panelClass` replaces the
  default panel layout classes (surface colors are still applied) for
  call sites that need a custom layout.
-->
<script lang="ts" module>
  // Stack of open modal ids so only the topmost one reacts to Escape /
  // Tab when dialogs are nested (e.g. a ConfirmDialog over an editor).
  const openStack: symbol[] = [];
</script>

<script lang="ts">
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
    /** Optional ARIA label for the dialog (used when no visible title). */
    ariaLabel?: string;
    /** id of the element that labels the dialog (usually the title). */
    labelledby?: string;
    /** Close when the backdrop is clicked. Defaults to true. */
    closeOnBackdrop?: boolean;
    /** Close on Escape. Defaults to true. */
    closeOnEscape?: boolean;
    /** Replaces the default panel layout classes. */
    panelClass?: string;
    /** Extra classes for the backdrop (e.g. z-index override). */
    backdropClass?: string;
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
    labelledby,
    closeOnBackdrop = true,
    closeOnEscape = true,
    panelClass,
    backdropClass = "z-50 p-4",
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

  const FOCUSABLE =
    'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"]), [contenteditable="true"]';

  let panel = $state<HTMLDivElement | null>(null);

  function focusables(): HTMLElement[] {
    if (!panel) return [];
    return Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
      (el) => el.offsetParent !== null || el === document.activeElement,
    );
  }

  // Initial focus + restore on close.
  $effect(() => {
    if (!open || !panel) return;
    const previous = document.activeElement as HTMLElement | null;
    const target =
      panel.querySelector<HTMLElement>("[data-autofocus]") ??
      focusables()[0] ??
      panel;
    // Defer so child components finish mounting.
    queueMicrotask(() => {
      if (panel && !panel.contains(document.activeElement)) target.focus();
    });
    return () => {
      if (previous && document.contains(previous)) previous.focus();
    };
  });

  // Escape-to-close and Tab trap. Bound to window while open so a
  // keypress with focus anywhere on the page still triggers dismiss.
  $effect(() => {
    if (!open) return;
    const id = Symbol("modal");
    openStack.push(id);
    const onKey = (e: KeyboardEvent) => {
      if (openStack[openStack.length - 1] !== id) return;
      if (e.key === "Escape" && closeOnEscape) {
        e.preventDefault();
        e.stopPropagation();
        onClose();
        return;
      }
      if (e.key !== "Tab" || !panel) return;
      const items = focusables();
      if (items.length === 0) {
        e.preventDefault();
        panel.focus();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const active = document.activeElement;
      if (e.shiftKey && (active === first || !panel.contains(active))) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && (active === last || !panel.contains(active))) {
        e.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      const idx = openStack.indexOf(id);
      if (idx >= 0) openStack.splice(idx, 1);
    };
  });

  const noop = () => {};
</script>

{#if open}
  <div
    class="fixed inset-0 flex items-center justify-center bg-black/40 dark:bg-black/60 {backdropClass}"
    use:backdropClose={closeOnBackdrop ? onClose : noop}
    role="presentation"
  >
    <div
      bind:this={panel}
      class="relative bg-white dark:bg-warm-800 shadow-xl border border-slate-200 dark:border-warm-700 outline-none {panelClass ??
        `rounded-lg w-full ${sizeClass[size]} max-h-[90vh] flex flex-col`}"
      role="dialog"
      aria-modal="true"
      aria-label={labelledby ? undefined : ariaLabel}
      aria-labelledby={labelledby}
      tabindex="-1"
    >
      {#if header}
        <header
          class="flex items-center justify-between px-4 py-3 border-b border-slate-200 dark:border-warm-700 shrink-0"
        >
          {@render header()}
        </header>
      {/if}

      {#if header || footer}
        <div class="flex-1 overflow-y-auto">
          {@render children?.()}
        </div>
      {:else}
        {@render children?.()}
      {/if}

      {#if footer}
        <footer
          class="flex items-center justify-end gap-2 px-4 py-3 border-t border-slate-200 dark:border-warm-700 shrink-0"
        >
          {@render footer()}
        </footer>
      {/if}
    </div>
  </div>
{/if}
