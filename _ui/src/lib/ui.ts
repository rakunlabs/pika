/**
 * Shared Tailwind class strings for the canonical controls described in
 * `_ui/DESIGN_SYSTEM.md`. Import these instead of re-typing the class
 * soup so a single edit here keeps every form/button in sync.
 */

/** §3 canonical form input (text, password, number, textarea). */
export const inputClass =
  'w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500';

/** Compact variant of the canonical input for dense nested forms. */
export const inputSmClass =
  'w-full px-2 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500';

/**
 * Compact monospace input for side panels whose own surface is already
 * `warm-900` (config SettingsPanel, InheritDialog): the field moves one
 * step *up* to `warm-800` so it stays distinct from the panel (§2, §12.2).
 */
export const inputPanelClass =
  'w-full px-2 py-1.5 text-xs font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500';

/** Small bordered card used inside side panels (no padding). */
export const panelCardClass =
  'bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded';

/** §3 canonical select — same tokens as the input. */
export const selectClass =
  'w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500';

/** §9 uppercase field label. */
export const labelClass =
  'block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1';

/** §4 primary / call-to-action button. */
export const btnPrimary =
  'px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer';

/** §4 secondary / neutral button. */
export const btnSecondary =
  'px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer';

/** §9 ghost button (Cancel in dialogs). */
export const btnGhost =
  'px-3 py-1.5 text-xs rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer';

/** §4 destructive button. */
export const btnDanger =
  'px-3 py-1.5 text-xs rounded bg-vermilion-600 text-white font-medium hover:bg-vermilion-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer';

/** §4 ghost toolbar icon button. */
export const btnIcon =
  'p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-500 dark:text-slate-400 cursor-pointer';

/** §7 card / panel (tier 1). */
export const cardClass =
  'bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-4';
