<script lang="ts">
 // DynamicForm renders one FormDef's fields against a plain JSON
 // value object and emits the updated object via onChange whenever
 // any field changes. Field kinds map to canonical pika inputs
 // (DESIGN_SYSTEM §3 / §9) so every form on this page looks like
 // every other form across the app.
 //
 // The page can still fall back to a JSON textarea for unknown
 // subtypes (handled by the parent), but anything in the catalogue
 // gets a proper labelled form here.
 import type { FormDef, FieldDef } from './schema';

 let {
  form,
  value,
  onChange,
 }: {
  form: FormDef;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
 } = $props();

 // ── Per-kind helpers ────────────────────────────────────────────

 // string-list serializes to/from "a, b, c"; we keep the raw text in
 // local state so commas and trailing spaces don't trip on each
 // keystroke as the user types.
 function listToText(v: unknown): string {
  if (Array.isArray(v)) return v.join(', ');
  return '';
 }
 function textToList(s: string): string[] {
  return s.split(',').map(x => x.trim()).filter(Boolean);
 }

 // kv-map serializes to "K=V\nK=V" — multi-line because URL header
 // values often contain spaces and commas, which would make a CSV
 // form unreadable.
 function mapToText(v: unknown): string {
  if (v && typeof v === 'object' && !Array.isArray(v)) {
   return Object.entries(v as Record<string, string>)
    .map(([k, val]) => `${k}=${val}`)
    .join('\n');
  }
  return '';
 }
 function textToMap(s: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of s.split(/\n+/)) {
   const eq = line.indexOf('=');
   if (eq < 0) continue;
   const k = line.slice(0, eq).trim();
   const v = line.slice(eq + 1).trim();
   if (k) out[k] = v;
  }
  return out;
 }

 // basic-auth "users" is a list of htpasswd-style "username:hash"
 // strings. Each line is one entry; we keep the raw "username:hash"
 // form on the wire so the backend can hand it straight to
 // CheckSecret (bcrypt / apr1 / SHA / crypt).
 function usersToText(v: unknown): string {
  if (Array.isArray(v)) return (v as string[]).join('\n');
  return '';
 }
 function textToUsers(s: string): string[] {
  const out: string[] = [];
  for (const line of s.split(/\n+/)) {
   const trimmed = line.trim();
   if (trimmed) out.push(trimmed);
  }
  return out;
 }

 function update(key: string, next: unknown) {
  // Drop falsey values for keys with no default so save payloads
  // stay compact (the backend treats missing fields as defaults).
  const out = { ...value };
  if (next === '' || next === undefined || next === null) {
   delete out[key];
  } else {
   out[key] = next;
  }
  onChange(out);
 }

 function onString(field: FieldDef, raw: string) {
  update(field.key, raw);
 }
 function onNumber(field: FieldDef, raw: string) {
  if (raw === '') { update(field.key, undefined); return; }
  const n = Number(raw);
  if (!Number.isFinite(n)) return;
  update(field.key, n);
 }
 function onBool(field: FieldDef, raw: boolean) {
  // Persist explicit false only when the default is true; otherwise
  // drop to keep payloads small (default is false).
  if (raw === field.default) {
   update(field.key, undefined);
  } else {
   update(field.key, raw);
  }
 }
 function onList(field: FieldDef, raw: string) {
  const list = textToList(raw);
  update(field.key, list.length ? list : undefined);
 }
 function onMap(field: FieldDef, raw: string) {
  const m = textToMap(raw);
  update(field.key, Object.keys(m).length ? m : undefined);
 }
 function onSelect(field: FieldDef, raw: string) {
  if (raw === '') { update(field.key, undefined); return; }
  // The redirect status options need number coercion; everything else
  // is fine as a string. Detect by checking if the field's options
  // are all numeric strings.
  const isNumeric = field.options?.every(o => /^\d+$/.test(o.value));
  update(field.key, isNumeric ? Number(raw) : raw);
 }

 // Canonical input class string from DESIGN_SYSTEM §3.
 const inputClass =
  'w-full px-3 py-2 text-sm rounded ' +
  'border border-slate-300 dark:border-warm-600 ' +
  'bg-white dark:bg-warm-900 ' +
  'text-slate-800 dark:text-slate-100 ' +
  'placeholder-slate-400 dark:placeholder-slate-500 ' +
  'focus:outline-none focus:ring-2 focus:ring-accent-500';

 const labelClass =
  'block text-xs font-medium uppercase tracking-wide ' +
  'text-slate-500 dark:text-slate-400 mb-1';
</script>

{#if form.intro}
 <p class="text-xs text-slate-500 dark:text-slate-400 mb-4">{form.intro}</p>
{/if}

{#if form.fields.length === 0}
 <p class="text-xs text-slate-400 dark:text-slate-500 italic">No configurable fields.</p>
{:else}
 <div class="space-y-3">
  {#each form.fields as field (field.key)}
   <div>
    {#if field.kind !== 'boolean'}
     <label class={labelClass} for={`f-${field.key}`}>
      {field.label}
      {#if field.required}<span class="text-vermilion-600 dark:text-vermilion-400">*</span>{/if}
     </label>
    {/if}

    {#if field.kind === 'string'}
     <input
      id={`f-${field.key}`}
      type="text"
      class={inputClass}
      placeholder={field.placeholder ?? ''}
      value={(value[field.key] as string | undefined) ?? ''}
      oninput={(e) => onString(field, (e.currentTarget as HTMLInputElement).value)}
     />

    {:else if field.kind === 'text'}
     {#if field.key === 'users'}
      <textarea
       id={`f-${field.key}`}
       class={inputClass + ' font-mono'}
       rows="4"
       spellcheck="false"
       placeholder={field.placeholder ?? ''}
       value={usersToText(value[field.key])}
       oninput={(e) => update(field.key, textToUsers((e.currentTarget as HTMLTextAreaElement).value))}
      ></textarea>
     {:else}
      <textarea
       id={`f-${field.key}`}
       class={inputClass + ' font-mono'}
       rows="4"
       spellcheck="false"
       placeholder={field.placeholder ?? ''}
       value={(value[field.key] as string | undefined) ?? ''}
       oninput={(e) => onString(field, (e.currentTarget as HTMLTextAreaElement).value)}
      ></textarea>
     {/if}

    {:else if field.kind === 'number'}
     <input
      id={`f-${field.key}`}
      type="number"
      class={inputClass}
      placeholder={field.placeholder ?? ''}
      value={(value[field.key] as number | undefined)?.toString() ?? ''}
      oninput={(e) => onNumber(field, (e.currentTarget as HTMLInputElement).value)}
     />

    {:else if field.kind === 'boolean'}
     <label class="flex items-start gap-2 cursor-pointer" for={`f-${field.key}`}>
      <input
       id={`f-${field.key}`}
       type="checkbox"
       class="mt-0.5 h-4 w-4 rounded border-slate-300 dark:border-warm-600 text-accent-600 focus:ring-accent-500 cursor-pointer"
       checked={(value[field.key] as boolean | undefined) ?? (field.default as boolean | undefined) ?? false}
       onchange={(e) => onBool(field, (e.currentTarget as HTMLInputElement).checked)}
      />
      <span class="text-sm text-slate-700 dark:text-slate-200">{field.label}</span>
     </label>

    {:else if field.kind === 'string-list'}
     <input
      id={`f-${field.key}`}
      type="text"
      class={inputClass}
      placeholder={field.placeholder ?? 'a, b, c'}
      value={listToText(value[field.key])}
      oninput={(e) => onList(field, (e.currentTarget as HTMLInputElement).value)}
     />

    {:else if field.kind === 'select'}
     <select
      id={`f-${field.key}`}
      class={inputClass}
      value={(value[field.key] as string | number | undefined)?.toString() ?? ''}
      onchange={(e) => onSelect(field, (e.currentTarget as HTMLSelectElement).value)}
     >
      {#each field.options ?? [] as opt}
       <option value={opt.value}>{opt.label}</option>
      {/each}
     </select>

    {:else if field.kind === 'kv-map'}
     <textarea
      id={`f-${field.key}`}
      class={inputClass + ' font-mono'}
      rows="3"
      spellcheck="false"
      placeholder={'X-Header=value\nX-Other=other'}
      value={mapToText(value[field.key])}
      oninput={(e) => onMap(field, (e.currentTarget as HTMLTextAreaElement).value)}
     ></textarea>
    {/if}

    {#if field.help}
     <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">{field.help}</p>
    {/if}
   </div>
  {/each}
 </div>
{/if}
