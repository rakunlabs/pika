<script lang="ts">
  // AccessFields edits external.Access — the per-resource operation limits
  // that apply to every caller, sessions included. It is deliberately one
  // component shared by the Settings add-form and the resource editor: the
  // rules are backend-agnostic, only the wording changes per kind.
  //
  // Wire semantics are tri-state (undefined / true / false) but the operator
  // only ever sees "allowed or not", so we emit `false` for a denied
  // operation and drop the key entirely when it's allowed. That keeps
  // untouched resources byte-identical on save.

  import type { ExternalAccess } from "@/lib/types/config";

  let {
    access = $bindable(),
    kind,
    readonly = false,
  }: {
    access: ExternalAccess | undefined;
    // Backend kind, used to label the rows with the right noun and to
    // explain the create/update split only where it is enforceable.
    kind: string;
    readonly?: boolean;
  } = $props();

  const id = $props.id();

  // GitLab is the backend that can tell a new variable from an existing one
  // (it probes the exact environment scope before writing). Everything else
  // must have create and update set the same way, so we say so up front
  // instead of letting the server reject the write later.
  const splitsWrites = $derived(kind === "gitlab");
  const noun = $derived(kind === "gitlab" ? "variable" : "entry");

  type Op = keyof ExternalAccess;

  const rows = $derived<Array<{ op: Op; label: string; help: string }>>([
    {
      op: "read",
      label: `Read ${noun} values`,
      help: "Also covers config inheritance and version history. Off makes the resource write-only.",
    },
    {
      op: "list",
      label: `List ${noun}s`,
      help: "Off hides the inventory; callers can still work with names they already know.",
    },
    {
      op: "create",
      label: `Add new ${noun}s`,
      help: splitsWrites
        ? `Off keeps the resource to the ${noun}s that already exist.`
        : `This backend cannot tell new ${noun}s from existing ones — keep add and change the same.`,
    },
    {
      op: "update",
      label: `Change existing ${noun}s`,
      help: splitsWrites
        ? `Off makes existing ${noun}s read-only while new ones can still be added.`
        : `This backend cannot tell new ${noun}s from existing ones — keep add and change the same.`,
    },
    {
      op: "delete",
      label: `Delete ${noun}s`,
      help: "Off rejects deletes before they reach the backend.",
    },
  ]);

  function allowed(op: Op): boolean {
    return access?.[op] !== false;
  }

  function setAllowed(op: Op, value: boolean) {
    const next: ExternalAccess = { ...(access ?? {}) };
    if (value) {
      delete next[op];
    } else {
      next[op] = false;
    }
    // Keep the wire shape minimal: an all-allowed record is the same as
    // having no access block at all.
    access = Object.keys(next).length > 0 ? next : undefined;
  }

  const restricted = $derived(rows.some((row) => !allowed(row.op)));
</script>

<div class="mb-4 rounded border border-slate-200 dark:border-warm-700 p-3">
  <div class="flex items-center justify-between gap-2">
    <h4 class="text-xs font-semibold text-slate-700 dark:text-slate-200">
      Resource permissions
    </h4>
    {#if restricted}
      <span
        class="px-2 py-0.5 text-[11px] rounded bg-amber-50 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300"
      >
        Restricted
      </span>
    {/if}
  </div>
  <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
    Applies to every caller, including superadmins and API tokens. Turning an
    operation off is a ceiling: user capabilities and token scopes can only
    narrow it further.
  </p>

  <div class="mt-3 space-y-3">
    {#each rows as row (row.op)}
      <div>
        <label
          for={`${id}-${row.op}`}
          class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200"
        >
          <input
            id={`${id}-${row.op}`}
            type="checkbox"
            role="switch"
            checked={allowed(row.op)}
            disabled={readonly}
            aria-describedby={`${id}-${row.op}-help`}
            onchange={(event) =>
              setAllowed(row.op, event.currentTarget.checked)}
            class="accent-accent-600 focus:outline-none focus:ring-2 focus:ring-accent-500 disabled:cursor-not-allowed"
          />
          {row.label}
        </label>
        <p
          id={`${id}-${row.op}-help`}
          class="mt-1 ml-6 text-xs text-slate-500 dark:text-slate-400"
        >
          {row.help}
        </p>
      </div>
    {/each}
  </div>

  {#if !splitsWrites && allowed("create") !== allowed("update")}
    <p
      class="mt-3 text-xs text-amber-600 dark:text-amber-400"
      role="alert"
    >
      This backend cannot separate adding from changing. Writes will be
      rejected until both switches match.
    </p>
  {/if}
</div>
