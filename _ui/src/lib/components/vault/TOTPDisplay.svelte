<script lang="ts">
  import { Copy, Check, AlertCircle } from 'lucide-svelte';
  import { computeTOTP, type TOTPField } from '@/lib/vault/totp';
  import { addToast } from '@/lib/store/toast.svelte';

  interface Props {
    field: TOTPField;
  }
  let { field }: Props = $props();

  let code = $state('');
  let period = $state(30);
  let remaining = $state(0);
  let err = $state('');
  let copied = $state(false);

  // Re-compute every second so the countdown bar animates and the
  // code rotates at the period boundary. We don't use $effect with a
  // setInterval inside because Svelte 5 effects are not torn down
  // automatically when the component unmounts.
  $effect(() => {
    let cancelled = false;
    function tick() {
      if (cancelled) return;
      try {
        const r = computeTOTP(field);
        code = r.code;
        period = r.period;
        remaining = r.remainingSeconds;
        err = '';
      } catch (e: any) {
        err = e?.message ?? 'Invalid TOTP secret';
      }
    }
    tick();
    const handle = setInterval(tick, 1000);
    return () => {
      cancelled = true;
      clearInterval(handle);
    };
  });

  async function copy() {
    if (!code) return;
    try {
      await navigator.clipboard.writeText(code);
      copied = true;
      setTimeout(() => (copied = false), 1500);
    } catch {
      addToast('Clipboard unavailable', 'warn');
    }
  }

  const pct = $derived(period > 0 ? (remaining / period) * 100 : 0);
  const formatted = $derived(code.length === 6 ? `${code.slice(0, 3)} ${code.slice(3)}` : code);
</script>

{#if err}
  <div class="flex items-center gap-2 text-xs text-red-700 dark:text-red-400">
    <AlertCircle size={12} />
    <span>{err}</span>
  </div>
{:else}
  <div class="flex items-center gap-3">
    <button
      type="button"
      onclick={copy}
      class="font-mono text-xl tracking-[0.2em] tabular-nums hover:bg-slate-100 dark:hover:bg-warm-800 px-2 py-1 rounded cursor-pointer flex items-center gap-2"
      title="Copy TOTP code"
    >
      {formatted}
      {#if copied}
        <Check size={14} class="text-emerald-600" />
      {:else}
        <Copy size={14} class="text-slate-400" />
      {/if}
    </button>
    <div class="relative w-8 h-8">
      <svg viewBox="0 0 36 36" class="w-8 h-8 -rotate-90">
        <circle cx="18" cy="18" r="14" fill="none" stroke="currentColor" stroke-width="3" class="text-slate-200 dark:text-warm-700" />
        <circle
          cx="18"
          cy="18"
          r="14"
          fill="none"
          stroke="currentColor"
          stroke-width="3"
          stroke-dasharray="87.96"
          stroke-dashoffset={87.96 * (1 - pct / 100)}
          class={remaining <= 5 ? 'text-red-500' : 'text-accent-600'}
        />
      </svg>
      <span class="absolute inset-0 flex items-center justify-center text-[10px] tabular-nums {remaining <= 5 ? 'text-red-500' : 'text-slate-500 dark:text-slate-400'}">
        {remaining}
      </span>
    </div>
  </div>
{/if}
