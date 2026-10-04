<!--
  ConfirmDialog — host for promise-based confirmations created via
  `confirmDialog()` in lib/store/confirm.svelte.ts. Mount once (App.svelte).
-->
<script lang="ts">
  import { AlertTriangle } from "lucide-svelte";
  import Modal from "@/lib/components/Modal.svelte";
  import { confirmState, settleConfirm } from "@/lib/store/confirm.svelte";
  import { btnDanger, btnGhost, btnPrimary } from "@/lib/ui";

  const req = $derived(confirmState.current);
</script>

<Modal
  open={req !== null}
  onClose={() => settleConfirm(false)}
  labelledby="confirm-dialog-title"
  backdropClass="z-[200] p-4"
  panelClass="rounded-lg w-full max-w-md p-5 space-y-4"
>
  {#if req}
    <div class="flex items-start gap-3">
      {#if req.danger}
        <AlertTriangle
          size={18}
          class="text-vermilion-600 dark:text-vermilion-400 mt-0.5 shrink-0"
        />
      {/if}
      <div class="min-w-0">
        <h3
          id="confirm-dialog-title"
          class="text-sm font-semibold text-slate-800 dark:text-slate-100"
        >
          {req.title}
        </h3>
        {#if req.message}
          <p
            class="mt-1 text-sm text-slate-600 dark:text-slate-300 whitespace-pre-line break-words"
          >
            {req.message}
          </p>
        {/if}
      </div>
    </div>
    <div class="flex gap-2 justify-end">
      <button type="button" class={btnGhost} onclick={() => settleConfirm(false)}>
        {req.cancelLabel ?? "Cancel"}
      </button>
      <button
        type="button"
        data-autofocus
        class={req.danger ? btnDanger : btnPrimary}
        onclick={() => settleConfirm(true)}
      >
        {req.confirmLabel ?? "Confirm"}
      </button>
    </div>
  {/if}
</Modal>
