<script lang="ts">
  import { Sun, Moon, Monitor } from "lucide-svelte";
  import { prefsStore, type AppTheme } from "@/lib/store/prefs.svelte";

  // Theme switcher: cycles light → dark → system → light on click. The
  // current theme determines the icon and tooltip. The choice is purely
  // local — stored in localStorage by prefsStore.setAppTheme and never
  // synced to the server.
  //
  // Two visual variants:
  // - "light" (default): light surface, used on the login card.
  // - "dark" : dark surface, used inside the dark navbar.
  //
  // Variants only swap the static colors of the button itself — the
  // toggled `.dark` class on <html> still drives every other piece of
  // app chrome.

  interface Props {
    variant?: "light" | "dark";
    /** Extra Tailwind classes for layout (positioning, margin, etc.). */
    class?: string;
  }

  let { variant = "light", class: extraClass = "" }: Props = $props();

  const THEME_CYCLE: AppTheme[] = ["light", "dark", "system"];

  function cycleTheme() {
    const cur = prefsStore.app.theme;
    const idx = THEME_CYCLE.indexOf(cur);
    const next = THEME_CYCLE[(idx + 1) % THEME_CYCLE.length];
    prefsStore.setAppTheme(next);
  }

  const themeLabel = $derived(
    prefsStore.app.theme === "light"
      ? "Light theme"
      : prefsStore.app.theme === "dark"
        ? "Dark theme"
        : "System theme",
  );

  // Base shape is a small square with slightly rounded corners — the
  // user explicitly asked for a square button (not a circle).
  const baseClasses =
    "flex items-center justify-center w-7 h-7 rounded-md border cursor-pointer transition-colors";

  const variantClasses = $derived(
    variant === "dark"
      ? "bg-warm-700 border-warm-600 text-warm-200 hover:bg-warm-600 hover:text-white"
      : "bg-white dark:bg-warm-900 border-slate-200 dark:border-warm-500 text-slate-600 dark:text-warm-200 hover:bg-slate-50 dark:hover:bg-warm-600 hover:text-slate-800 dark:hover:text-white",
  );
</script>

<button
  type="button"
  onclick={cycleTheme}
  title={`${themeLabel} (click to change)`}
  aria-label={themeLabel}
  class="{baseClasses} {variantClasses} {extraClass} cursor-pointer"
>
  {#if prefsStore.app.theme === "light"}
    <Sun size={14} />
  {:else if prefsStore.app.theme === "dark"}
    <Moon size={14} />
  {:else}
    <Monitor size={14} />
  {/if}
</button>
