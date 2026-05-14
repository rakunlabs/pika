<script lang="ts">
  import { Dices, Copy, Check, RotateCw } from 'lucide-svelte';
  import { generatePassword, generatePassphrase, estimateStrength } from '@/lib/vault/generator';
  import { addToast } from '@/lib/store/toast.svelte';

  interface Props {
    onApply?: (value: string) => void;
  }
  let { onApply }: Props = $props();

  // Mode tab.
  let mode = $state<'password' | 'passphrase'>('password');

  // Password options.
  let length = $state(20);
  let useLower = $state(true);
  let useUpper = $state(true);
  let useDigits = $state(true);
  let useSymbols = $state(true);
  let excludeAmbiguous = $state(true);

  // Passphrase options.
  let wordCount = $state(5);
  let separator = $state('-');
  let capitalize = $state(false);
  let appendNumber = $state(false);

  let value = $state('');
  let copied = $state(false);

  function regenerate() {
    if (mode === 'password') {
      value = generatePassword({ length, lower: useLower, upper: useUpper, digits: useDigits, symbols: useSymbols, excludeAmbiguous });
    } else {
      value = generatePassphrase({ words: wordCount, separator, capitalize, appendNumber });
    }
  }

  // Auto-regenerate on option change. Cheap (≤ 1ms) so no debouncing.
  $effect(() => {
    void length; void useLower; void useUpper; void useDigits; void useSymbols; void excludeAmbiguous;
    void wordCount; void separator; void capitalize; void appendNumber;
    void mode;
    regenerate();
  });

  const strength = $derived(estimateStrength(value));
  const strengthColors: Record<string, string> = {
    terrible: 'bg-red-500',
    weak: 'bg-orange-500',
    fair: 'bg-yellow-500',
    strong: 'bg-emerald-500',
    very_strong: 'bg-emerald-600',
  };

  async function copyToClipboard() {
    if (!value) return;
    try {
      await navigator.clipboard.writeText(value);
      copied = true;
      setTimeout(() => (copied = false), 1500);
    } catch {
      addToast('Clipboard unavailable', 'warn');
    }
  }

  function apply() {
    if (!value) return;
    onApply?.(value);
  }
</script>

<div class="space-y-3">
  <!-- Mode tabs -->
  <div class="flex gap-1 text-xs">
    <button
      type="button"
      onclick={() => (mode = 'password')}
      class="px-3 py-1.5 rounded {mode === 'password' ? 'bg-accent-600 text-white' : 'bg-slate-100 dark:bg-warm-800 text-slate-700 dark:text-slate-300 hover:bg-slate-200 dark:hover:bg-warm-700'} cursor-pointer"
    >Password</button>
    <button
      type="button"
      onclick={() => (mode = 'passphrase')}
      class="px-3 py-1.5 rounded {mode === 'passphrase' ? 'bg-accent-600 text-white' : 'bg-slate-100 dark:bg-warm-800 text-slate-700 dark:text-slate-300 hover:bg-slate-200 dark:hover:bg-warm-700'} cursor-pointer"
    >Passphrase</button>
  </div>

  <!-- Output -->
  <div class="flex items-center gap-2">
    <input
      type="text"
      readonly
      value={value}
      class="flex-1 px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-slate-50 dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none"
    />
    <button
      type="button"
      onclick={copyToClipboard}
      class="p-2 rounded border border-slate-300 dark:border-warm-700 hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer"
      title="Copy"
    >
      {#if copied}<Check size={16} class="text-emerald-600" />{:else}<Copy size={16} />{/if}
    </button>
    <button
      type="button"
      onclick={regenerate}
      class="p-2 rounded border border-slate-300 dark:border-warm-700 hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer"
      title="Regenerate"
    >
      <RotateCw size={16} />
    </button>
  </div>

  <!-- Strength -->
  <div class="flex items-center gap-2">
    <div class="flex-1 h-1 bg-slate-200 dark:bg-warm-800 rounded overflow-hidden">
      <div class="h-full transition-all {strengthColors[strength.label]}" style="width: {(strength.score + 1) * 20}%"></div>
    </div>
    <span class="text-[10px] uppercase tracking-wide text-slate-500 dark:text-slate-400">{strength.label.replace('_', ' ')}</span>
  </div>

  <!-- Options -->
  {#if mode === 'password'}
    <div class="space-y-2 text-sm">
      <label class="flex items-center justify-between gap-3">
        <span>Length</span>
        <input type="number" min="8" max="128" bind:value={length} class="w-20 px-2 py-1 rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100" />
      </label>
      <label class="flex items-center gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={useLower} /> Lowercase letters
      </label>
      <label class="flex items-center gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={useUpper} /> Uppercase letters
      </label>
      <label class="flex items-center gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={useDigits} /> Digits
      </label>
      <label class="flex items-center gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={useSymbols} /> Symbols
      </label>
      <label class="flex items-center gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={excludeAmbiguous} /> Exclude look-alike characters (I, l, O, 0…)
      </label>
    </div>
  {:else}
    <div class="space-y-2 text-sm">
      <label class="flex items-center justify-between gap-3">
        <span>Words</span>
        <input type="number" min="3" max="16" bind:value={wordCount} class="w-20 px-2 py-1 rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100" />
      </label>
      <label class="flex items-center justify-between gap-3">
        <span>Separator</span>
        <input type="text" maxlength="3" bind:value={separator} class="w-20 px-2 py-1 rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono" />
      </label>
      <label class="flex items-center gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={capitalize} /> Capitalize each word
      </label>
      <label class="flex items-center gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={appendNumber} /> Append a number
      </label>
    </div>
  {/if}

  {#if onApply}
    <button
      type="button"
      onclick={apply}
      class="w-full flex items-center justify-center gap-1.5 px-3 py-2 text-sm rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 cursor-pointer disabled:cursor-not-allowed"
      disabled={!value}
    >
      <Dices size={14} /> Use this value
    </button>
  {/if}
</div>
