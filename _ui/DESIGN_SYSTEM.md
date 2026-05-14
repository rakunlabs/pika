# Pika UI Design System

> **For agents and contributors:** before adding any UI, read this. The rules
> below already match what the rest of the SPA does — if your component
> diverges, the diff between yours and the existing pages will look broken in
> at least one theme.

## 1. Color tokens

All colors live in `_ui/src/style/global.css` under the `@theme` block. Use the
Tailwind utility class for the token; never hard-code hex values in
component files.

**Each custom palette (`brand`, `accent`, `vermilion`, `cool`, `warm`)
defines the full `50, 100, 200, …, 900, 950` scale.** Tailwind v4 silently
drops any utility that references a token not present in `@theme` — for
example `dark:bg-accent-950/30` produces no CSS rule if `--color-accent-950`
isn't defined, so the element falls back to whatever color was set in the
non-dark class (typically `bg-white` → looks fine in light mode, broken in
dark). **If you add a new shade to a palette, add it to every palette
that's already at parity. If you reference a `*-950` class, verify the
token exists.**

| Token | Hue | Purpose |
|---|---|---|
| `brand-*` | Navy `#1d3557` | Primary actions, focus rings (we don't reach for it often — most actions use `accent-*`). |
| `accent-*` | Teal `#0e9594` | Selected / active highlight. Primary buttons, focus rings, "current" indicators, link color. |
| `vermilion-*` | Red `#ef233c` | Destructive call-to-action only. Reset, delete, "type RESET to confirm". |
| `cool-*` | Cool grey | Info pills, secondary surfaces (rare). |
| `warm-*` | Warm grey | **The dark-mode surface scale.** Every dark background is `warm-{50…950}`. The 950 step (`#0e0e0e`) was added late — older copies of this doc said the scale stops at 900. If a class like `dark:bg-warm-950` doesn't render, verify `_ui/src/style/global.css` defines `--color-warm-950`; Tailwind v4 silently drops utilities that reference undefined tokens. |
| `slate-*` | Tailwind default | The **light-mode** surface + text scale. |
| `emerald-*` / `amber-*` | Tailwind default | Success / warning callouts. |

### Rules

- **Never** use `slate-*` for a dark-mode background. Dark mode is warm-* only.
- **Never** use `gray-*` (the cooler Tailwind default). We use `slate-*` for light, `warm-*` for dark.
- `accent-600` is the dark-enough teal for light-mode text + bg. `accent-400` is the dark-mode pair.

---

## 2. Surface elevation tiers

Every dark surface MUST sit at a different `warm-*` step than the surface
behind it. If two adjacent surfaces share the same tone, the inner one
disappears into the outer one — that's exactly the "vault is broken in dark
mode" report you may have seen.

| Tier | Light | Dark | When to use |
|---|---|---|---|
| **Page (tier 0)** | `bg-slate-100` | `bg-warm-900` | The application background. Set on the app shell in `App.svelte`. Do not re-apply on inner panels. |
| **Card / panel (tier 1)** | `bg-white` | `bg-warm-800` | The default surface for settings sections, modals, login card, vault setup/unlock card. **Always one step lighter than page in dark mode** so the card reads as elevated. |
| **Nested muted (tier 1.5)** | `bg-slate-50` | `bg-warm-900` | Info boxes, code samples, stat panels INSIDE a tier-1 card. Same value as the page in dark mode is intentional — it reads as "inset" relative to the card. |
| **Input / control (tier 2)** | `bg-white` | `bg-warm-900` | `<input>`, `<textarea>`, `<select>`. Surface should be visibly recessed inside a tier-1 card. |
| **App-page surface (tier 0 alt)** | `bg-white` | `bg-warm-950` | Full-bleed app pages that aren't "cards", like the vault item-editor body or the item-list sidebar. These represent the application's own dark canvas; the surrounding page goes invisible behind them. `warm-950` is the deepest tone in the scale, defined in `global.css`. |

### Common mistake → fix

| Symptom | Cause | Fix |
|---|---|---|
| Card "doesn't show up" in dark mode | Card bg = page bg (`dark:bg-warm-900` on both) | Card → `dark:bg-warm-800` |
| Input is invisible black-on-near-black | Browser default `color` on `<input>` (form elements don't inherit `color`) | Already fixed globally in `global.css` (`input, textarea, select { color: inherit }`). New inputs still need explicit `text-slate-800 dark:text-slate-100` if you override `color`. |
| Two cards next to each other "merge" | Same tier, no border, no spacing | Use `border border-slate-200 dark:border-warm-700` on both, or separate with `gap-*`. |

---

## 3. The canonical form input

Every `<input>`, `<textarea>`, `<select>` in a vault / settings context should
match this class string:

```html
class="w-full px-3 py-2 text-sm rounded
       border border-slate-300 dark:border-warm-600
       bg-white dark:bg-warm-900
       text-slate-800 dark:text-slate-100
       placeholder-slate-400 dark:placeholder-slate-500
       focus:outline-none focus:ring-2 focus:ring-accent-500"
```

Breakdown:
- `border-slate-300 dark:border-warm-600` — the lighter `warm-600` (vs `warm-700`) is intentional; against a `warm-800` card with a `warm-900` input, the slightly lighter border gives the input enough definition to read.
- `text-slate-800 dark:text-slate-100` — explicit because some browsers (notably Safari with `-webkit-text-fill-color`) ignore inherited color on form inputs.
- `placeholder-slate-400 dark:placeholder-slate-500` — never default; default placeholder is too low contrast.
- `focus:ring-accent-500` — selection teal, NOT brand navy. We reserve navy for nothing in particular and use accent for everything interactive.

For destructive variants (red focus + border), swap:
- `border-red-300 dark:border-red-700`
- `focus:ring-red-500`

---

## 4. The canonical button

Three flavors. Pick by intent, not by look.

### Primary / call-to-action
```html
class="px-3 py-1.5 text-xs rounded
       bg-accent-600 text-white font-medium
       hover:bg-accent-700
       disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
```

### Secondary / neutral
```html
class="px-3 py-1.5 text-xs rounded
       bg-slate-100 dark:bg-warm-800
       hover:bg-slate-200 dark:hover:bg-warm-700
       text-slate-700 dark:text-slate-200
       cursor-pointer"
```

### Ghost (toolbar icon button)
```html
class="p-1.5 rounded
       hover:bg-slate-100 dark:hover:bg-warm-700
       text-slate-500 dark:text-slate-400
       cursor-pointer"
```

### Destructive
```html
class="px-3 py-1.5 text-xs rounded
       bg-vermilion-600 text-white font-medium
       hover:bg-vermilion-700
       disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
```

### Active nav-item (soft selection)

Used for the sidebar in Settings, the selected-row in vault item list,
folder pickers, and any other "this entry is current" indicator that
isn't a high-emphasis filled-accent button. The formula is the
**canonical "selected nav entry" pattern** across the app — keep them
identical so cross-page navigation feels uniform.

```html
class="bg-accent-50 text-accent-700 border-accent-200
       dark:bg-accent-900/40 dark:text-accent-300 dark:border-accent-700"
```

Inactive sibling rows:

```html
class="text-slate-600 dark:text-warm-200
       hover:bg-slate-100 dark:hover:bg-warm-700
       hover:text-slate-800 dark:hover:text-white"
```

Notes:
- The dark active surface is `accent-900/40` (40% opacity teal) so it sits as a wash on top of the underlying `warm-800` card, not a flat tile. The 40% is deliberate — `/30` looked anemic next to settings.
- **Avoid `accent-950/30`** for soft selections. We have a `--color-accent-950` token now, but the settings family standardized on `accent-900/40` first; staying with one variant means one place to tune if the wash ever needs adjusting.
- **Hover bg in dark mode** is `warm-700`, NEVER `warm-800` (that's the same as the card behind, so hover looks dead).
- **Icon-only buttons** still need `aria-label` and a `title`. Both. The `title` is hover help; `aria-label` is the screen reader name.
- `cursor-pointer` is explicit because Tailwind's preflight resets `<button>` to default cursor.

---

## 5. Typography & semantic text colors

| Class | Light | Dark | Use for |
|---|---|---|---|
| (none — inherits) | `slate-800` | `slate-100` | Body text. The app shell sets this once in `App.svelte`. |
| `text-slate-700 dark:text-slate-200` | medium contrast | medium contrast | Emphasized body, definition values (`<dd>`). |
| `text-slate-500 dark:text-slate-400` | muted | muted | Help text, captions, `<dt>` labels, "since X" timestamps. |
| `text-slate-400 dark:text-slate-500` | very muted | very muted | Placeholder-ish text, "no items" empty states. |
| `text-accent-600 dark:text-accent-400` | teal | light teal | Links, primary-icon glyphs in headings, "current" emphasis. |
| `text-vermilion-600 dark:text-vermilion-400` | red | light red | Destructive text + icons (`Delete`, `Reset`, "X removed"). |
| `text-emerald-600 dark:text-emerald-400` | green | light green | Success state ("Copied", "Saved"). |
| `text-amber-600 dark:text-amber-400` | amber | light amber | Warnings ("Archived", "Changed"). |

**Always pair a `text-*` with its `dark:text-*` counterpart.** A bare
`text-slate-500` will be invisible on the dark page. The two exceptions:
- Inside a tier-1 card where contrast against `warm-800` is fine — even then, prefer the pair.
- Inside a destructive panel (`bg-red-50 dark:bg-red-950/40`) where you already use red text variants.

---

## 6. Iconography

We use [Lucide](https://lucide.dev) via `lucide-svelte`. Standard sizes:

| Context | Size |
|---|---|
| Inline next to body text | 12–14 |
| Toolbar / action button | 14–16 |
| Hero zone (large header icon) | 22–28 |
| Empty-state illustration | 28–32 |

Color tracking with text-* utility:
```html
<Star size={14} class="text-amber-500" />
<Lock size={16} class="text-accent-600 dark:text-accent-400" />
```

When using an icon as the only content of a button, the button needs:
1. `aria-label="..."` for screen readers
2. `title="..."` for hover help
3. A larger hit area than the visible icon (`p-1.5` minimum)

---

## 7. Cards & panels — anatomy

```svelte
<div class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-4">
  <h3 class="text-sm font-semibold mb-1 flex items-center gap-1.5">
    <SomeIcon size={14} class="text-accent-600 dark:text-accent-400" />
    Title
  </h3>
  <p class="text-xs text-slate-500 dark:text-slate-400 mb-3">
    One-line explanation.
  </p>
  <!-- body... -->
</div>
```

Spacing scale: `space-y-6` between major sections of a page, `space-y-4`
between groups inside a card, `space-y-2` between rows. Padding: `p-4` for
compact cards, `p-6` for full-width hero-feeling cards (login, vault setup).

---

## 8. Info / warning / destructive callouts

Standard callout pattern, swap the color stem (`blue`, `amber`, `red`, `emerald`):

```svelte
<div class="bg-blue-50 dark:bg-blue-950/30
            border border-blue-300 dark:border-blue-700
            rounded p-3 text-sm flex gap-2">
  <AlertTriangle size={16} class="text-blue-700 dark:text-blue-300 shrink-0 mt-0.5" />
  <div class="text-blue-900 dark:text-blue-200">
    ...
  </div>
</div>
```

For dark-mode opacity on the panel: use `/30` for soft info, `/40` for
warnings. **Never `/20`** — at 20% the tinted panel disappears against
`warm-900` and the warning loses all weight.

---

## 9. Forms — recommended structure

```svelte
<form onsubmit={onSubmit} class="space-y-3">
  <!-- Labeled field -->
  <div>
    <label class="block text-xs font-medium uppercase tracking-wide
                  text-slate-500 dark:text-slate-400 mb-1" for="field-id">
      Field label
    </label>
    <input
      id="field-id"
      type="text"
      bind:value={value}
      class="w-full px-3 py-2 text-sm rounded
             border border-slate-300 dark:border-warm-600
             bg-white dark:bg-warm-900
             text-slate-800 dark:text-slate-100
             placeholder-slate-400 dark:placeholder-slate-500
             focus:outline-none focus:ring-2 focus:ring-accent-500" />
    <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
      Optional helper text.
    </p>
  </div>

  <!-- Action row -->
  <div class="flex gap-2 justify-end">
    <button type="button" onclick={onCancel}
      class="px-3 py-1.5 text-xs rounded
             hover:bg-slate-100 dark:hover:bg-warm-700
             text-slate-700 dark:text-slate-200 cursor-pointer">
      Cancel
    </button>
    <button type="submit" disabled={busy}
      class="px-3 py-1.5 text-xs rounded
             bg-accent-600 text-white font-medium hover:bg-accent-700
             disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer">
      Save
    </button>
  </div>
</form>
```

The Cancel button is **ghost** (left), the Primary is **filled accent** (right).
Always in that order, right-aligned.

---

## 10. Empty states

Every "no content" surface should communicate **what would be here** and
**how to get there**. Generic "No items" is forbidden.

```svelte
<div class="flex flex-col items-center justify-center py-12 px-6 text-center
            text-slate-400 dark:text-slate-500">
  <KeyRound size={28} class="mb-3 opacity-40" />
  <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">
    Your vault is empty
  </div>
  <div class="text-xs mb-4">
    Add your first password, key, or note — everything is encrypted in your
    browser before it reaches the server.
  </div>
  <button onclick={onNew}
    class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded
           bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer">
    <Plus size={12} /> Create your first item
  </button>
</div>
```

---

## 11. Vault-specific patterns

These rules cover ItemList / ItemEditor / VaultSetup / VaultUnlock /
EmergencyKit / NewItemDialog.

### Item type → color stripe

Every vault item has a `type`. The item-list row and the item-editor hero use
a thin accent stripe / icon tile to make types visually distinct without
the user reading the label.

| Type | Stem | Icon |
|---|---|---|
| `login` | `accent` (teal) | `KeyRound` |
| `card` | `vermilion` (red) | `CreditCard` |
| `identity` | `sky` | `UserSquare2` |
| `secure_note` | `amber` | `FileText` |
| `ssh_key` | `purple` | `Terminal` |
| `api_credential` | `indigo` | `Plug` |
| `database` | `cyan` | `Database` |
| `server` | `slate` | `Server` |
| `license` | `emerald` | `FileBadge` |
| `tls_cert` | `rose` | `ShieldCheck` |

Implementation: a single `itemTypeStem(type)` helper returns the stem
string; the row applies it as `bg-${stem}-100 dark:bg-${stem}-950/40
text-${stem}-700 dark:text-${stem}-300` on the icon tile and as a
left-border stripe on the editor hero.

### Item editor body

- **Left-aligned**, NOT centered. The previous `mx-auto` was rolled back per user feedback ("reads as a centered card, disconnects from the list"). Use `max-w-3xl` cap without `mx-auto`.
- **Scrollbar lives on the outer `overflow-y-auto`** wrapping the content column, so it sits on the panel's right edge regardless of inner width.
- **View vs. Edit mode**: read mode is the default; explicit `Edit` button enters edit mode. Save reverts to view mode automatically.

### Field cards (read mode)

- Border + rounded card per field
- Header strip with **label** (caps) + **type pill** + action icons
- Body has the value (mono for keys, tracked-letter masked, etc.)
- Sensitive fields render as `••••••` by default with an eye toggle

### Secure note rendering

Notes for `type === 'secure_note'` items are rendered through the in-tree
markdown parser (`_ui/src/lib/vault/markdown.ts`). Other types render plain
whitespace-preserved text. The markdown parser is zero-dep and escape-safe;
don't add `marked` or similar without a hard reason.

### Folder grouping

Item list shows folders as collapsible accordion groups. Each group is a
`<button>` header that toggles a `collapsed` Set persisted to
`localStorage["pika.vault.collapsed.${user_id}"]`. The header order is real
folders alphabetically, then `(No folder)` last.

### History timeline

Item editor has a single "History" button (not per field). When opened, it
fetches `listItemVersions` and decrypts each snapshot's payload in the
browser. Each row shows the **value at that snapshot** (not before/after);
sensitive values are masked by default with a per-snapshot eye toggle.

---

## 12. Dark mode debugging checklist

If something looks broken in dark mode:

1. **Does the surface have a `dark:bg-warm-*` pair?** If not, add it.
2. **Is the surface the same warm-* step as its parent?** Move it up or down one step.
3. **Does the text have a `dark:text-*` pair?** Defaults inherit, but `text-slate-500` etc. don't darken.
4. **Are inputs showing black-on-near-black?** Already fixed in `global.css`; if the input has a hard-coded `color` class, replace with `text-slate-800 dark:text-slate-100`.
5. **Is a tinted overlay too subtle?** Bump `/20` → `/30` or `/40`.
6. **Did you use `gray-*` or `zinc-*`?** Replace with `slate-*` for light and `warm-*` for dark.

If a fix would need more than five class changes, the structural problem is
that the surface elevation tiers are wrong — re-read §2.
