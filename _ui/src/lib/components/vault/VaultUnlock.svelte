<script lang="ts">
  import {
    Lock,
    Loader2,
    KeyRound,
    AlertCircle,
    ShieldCheck,
  } from "lucide-svelte";
  import { onMount } from "svelte";
  import { vaultStore } from "@/lib/vault/store.svelte";
  import { parseSecretKey } from "@/lib/vault/crypto";
  import { addToast } from "@/lib/store/toast.svelte";

  interface Props {
    onUnlocked?: () => void;
  }
  let { onUnlocked }: Props = $props();

  // The form has two modes:
  //   trusted   — a localStorage trust blob exists for this account;
  //               only the master password is needed. The store
  //               unseals the Secret Key from the blob.
  //   untrusted — first time on this device (or trust was revoked);
  //               both fields are required, plus an opt-in checkbox
  //               to seed the trust blob on a successful unlock.
  let password = $state("");
  let secretKeyRaw = $state("");
  let trustChecked = $state(false);
  let busy = $state(false);
  let err = $state("");

  // Live character count of the entered Secret Key after applying
  // the SAME normalization the decoder does: uppercase, Crockford
  // typo correction (I/L → 1, O → 0), and strip anything outside
  // the alphabet (dashes, spaces, newlines, punctuation).
  //
  // A valid Secret Key encodes 32 bytes as base32 → exactly 52
  // characters. We show the live counter so the user catches a
  // short paste BEFORE clicking Unlock. Because this mirrors
  // parseSecretKey's filter exactly, a "52 / 52" reading guarantees
  // the decoder will accept the input (the only remaining failure
  // mode is a wrong password / wrong key against the wrapped vault).
  const SECRET_KEY_LENGTH = 52;
  const secretKeyCharCount = $derived(
    secretKeyRaw
      .toUpperCase()
      .replace(/[IL]/g, "1")
      .replace(/O/g, "0")
      .replace(/[^A-Z0-9]/g, "").length,
  );

  // Destructive-reset escape hatch. The user has lost their Secret
  // Key (or the master password) and wants to wipe the vault to
  // start over. Backed by DELETE /api/v1/me/vault — auth is the
  // login session alone, no vault credentials required (the user
  // wouldn't have them, that's the whole point).
  //
  // Two-step gate so an accidental click can't nuke the vault:
  //   showReset=false  → small "Lost your Secret Key?" link
  //   showReset=true   → confirmation panel with a typed-word gate
  let showReset = $state(false);
  let resetConfirm = $state("");
  let resetBusy = $state(false);
  let resetErr = $state("");

  async function doReset() {
    if (resetConfirm.trim().toUpperCase() !== "RESET") {
      resetErr = "Type RESET to confirm";
      return;
    }
    resetBusy = true;
    resetErr = "";
    try {
      await vaultStore.reset();
      addToast("Vault reset — you can create a new one now", "success", 3500);
      // The parent Vault.svelte switch will flip to the setup view
      // automatically because status.initialized is now false.
    } catch (e: any) {
      resetErr = e?.response?.data?.message ?? e?.message ?? "Reset failed";
    } finally {
      resetBusy = false;
    }
  }

  // `mode` is derived from the store, but we snapshot it locally so
  // it doesn't flip mid-submit (e.g. when the store auto-untrusts
  // on the 3rd failed attempt — we want the error to render in the
  // mode the user just submitted from).
  let mode = $state<"trusted" | "untrusted">("untrusted");

  function refreshMode() {
    mode = vaultStore.isDeviceTrusted() ? "trusted" : "untrusted";
  }

  onMount(async () => {
    if (!vaultStore.account) {
      await vaultStore.refreshAccount();
    }
    refreshMode();
  });

  // Manual "use Secret Key instead" path. Clears the blob so the
  // store falls through to the explicit-input path on submit.
  function useSecretKeyInstead() {
    vaultStore.untrustDevice();
    refreshMode();
    err = "";
    // Move focus to the secret-key textarea once it renders. The
    // password input keeps whatever the user already typed.
    setTimeout(() => {
      const el = document.getElementById(
        "unlock-sk",
      ) as HTMLTextAreaElement | null;
      el?.focus();
    }, 0);
  }

  async function submit(e: Event) {
    e.preventDefault();
    if (busy) return;
    err = "";
    busy = true;
    try {
      if (mode === "trusted") {
        // Store reads the trust blob, unseals with the master
        // password, and proceeds. Returns false on a wrong password
        // OR after the 3-strike auto-untrust fires.
        const ok = await vaultStore.unlock(password);
        if (!ok) {
          const stillTrusted = vaultStore.isDeviceTrusted();
          if (!stillTrusted) {
            // 3-strike untrust fired (or the stored blob was
            // server-rejected as stale). Flip to the full form and
            // tell the user.
            mode = "untrusted";
            err =
              "Too many failed attempts — device trust removed. Please enter your Secret Key.";
          } else {
            err = "Wrong master password";
          }
          return;
        }
      } else {
        let sk: Uint8Array;
        try {
          sk = parseSecretKey(secretKeyRaw);
        } catch (e: any) {
          err = e?.message ?? "Invalid Secret Key format";
          return;
        }
        const ok = await vaultStore.unlock(password, sk);
        if (!ok) {
          err = "Wrong password or Secret Key";
          return;
        }
        if (trustChecked) {
          try {
            await vaultStore.trustDevice(password);
            addToast("This device is now trusted", "success", 2500);
          } catch {
            // Non-fatal: unlock succeeded; just couldn't persist
            // the trust blob (quota, private mode, etc).
            addToast("Could not save device trust", "alert", 3000);
          }
        }
      }
      password = "";
      secretKeyRaw = "";
      trustChecked = false;
      addToast("Vault unlocked", "success", 2000);
      onUnlocked?.();
    } catch (e: any) {
      err = e?.response?.data?.message ?? e?.message ?? "Unlock failed";
    } finally {
      busy = false;
    }
  }
</script>

<div class="max-w-md mx-auto py-12 px-4">
  <!-- Card surface lives one elevation above the page background.
       The page (App.svelte:64) is `dark:bg-warm-900`, so a card
       that also uses `warm-900` blends right in — the previous
       version showed only the border. `warm-800` is one tick
       lighter, giving the card a visible "elevated" feel against
       the page in dark mode. -->
  <div
    class="bg-white dark:bg-warm-800 rounded-lg border border-slate-200 dark:border-warm-700 p-6 shadow-sm dark:shadow-none"
  >
    <div class="flex items-center gap-2 mb-2">
      <Lock size={18} class="text-accent-600 dark:text-accent-400" />
      <h2 class="text-lg font-semibold">Unlock your vault</h2>
    </div>
    {#if mode === "trusted"}
      <p
        class="text-sm text-slate-600 dark:text-slate-300 mb-2 flex items-center gap-1.5"
      >
        <ShieldCheck size={14} class="text-green-600 dark:text-green-500" />
        This device is trusted — only your master password is needed.
      </p>
    {:else}
      <p class="text-sm text-slate-600 dark:text-slate-300 mb-6">
        Enter your master password and Secret Key (from your Emergency Kit).
      </p>
    {/if}

    <form onsubmit={submit} class="space-y-4">
      <div>
        <label
          class="block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1"
          for="unlock-pw"
        >
          Master password
        </label>
        <!-- svelte-ignore a11y_autofocus -->
        <input
          id="unlock-pw"
          type="password"
          autofocus
          bind:value={password}
          required
          autocomplete="current-password"
          class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
        />
      </div>

      {#if mode === "untrusted"}
        <div>
          <label
            class="block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1"
            for="unlock-sk"
          >
            <span class="inline-flex items-center gap-1.5">
              <KeyRound size={12} /> Secret Key
            </span>
          </label>
          <textarea
            id="unlock-sk"
            bind:value={secretKeyRaw}
            rows="3"
            required
            spellcheck="false"
            autocomplete="off"
            placeholder="Paste your Secret Key here"
            class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
          ></textarea>
          <div class="mt-1 flex items-center justify-between gap-2 text-xs">
            <p class="text-slate-500 dark:text-slate-400">
              Dashes, spaces, and case are ignored.
            </p>
            {#if secretKeyRaw}
              <span
                class="font-mono tabular-nums {secretKeyCharCount ===
                SECRET_KEY_LENGTH
                  ? 'text-emerald-600 dark:text-emerald-400'
                  : secretKeyCharCount > SECRET_KEY_LENGTH
                    ? 'text-red-600 dark:text-red-400'
                    : 'text-amber-600 dark:text-amber-400'}"
                title="Counted after stripping dashes / spaces. Should be exactly {SECRET_KEY_LENGTH}."
              >
                {secretKeyCharCount} / {SECRET_KEY_LENGTH}
              </span>
            {/if}
          </div>
        </div>

        <label
          class="flex items-start gap-2 text-xs text-slate-600 dark:text-slate-300 cursor-pointer select-none"
        >
          <input
            type="checkbox"
            bind:checked={trustChecked}
            class="mt-0.5 cursor-pointer accent-accent-600"
          />
          <span>
            <span class="font-medium text-slate-700 dark:text-slate-200"
              >Trust this device.</span
            >
            Skip the Secret Key prompt on future unlocks from this browser. The Secret
            Key is stored locally, encrypted with your master password (Argon2id).
            Three wrong passwords automatically revoke trust.
          </span>
        </label>
      {/if}

      {#if err}
        <div
          class="flex items-start gap-2 text-sm text-red-700 dark:text-red-400"
        >
          <AlertCircle size={14} class="mt-0.5 shrink-0" />
          <span>{err}</span>
        </div>
      {/if}

      <button
        type="submit"
        disabled={busy}
        class="w-full flex items-center justify-center gap-2 px-4 py-2 text-sm rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
      >
        {#if busy}<Loader2 size={14} class="animate-spin" />{/if}
        {busy && mode === "trusted" ? "Unlocking…" : "Unlock"}
      </button>

      {#if mode === "trusted"}
        <button
          type="button"
          onclick={useSecretKeyInstead}
          class="w-full text-xs text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 underline cursor-pointer"
        >
          Use Secret Key instead (revoke trust on this device)
        </button>
      {/if}
    </form>

    <!-- Lost-Secret-Key escape hatch. Hidden behind a small link
         until clicked, then a typed-word confirmation gate before
         the destructive call. Backed by DELETE /api/v1/me/vault. -->
    <div class="mt-6 pt-4 border-t border-slate-200 dark:border-warm-700">
      {#if !showReset}
        <button
          type="button"
          onclick={() => {
            showReset = true;
            resetErr = "";
            resetConfirm = "";
          }}
          class="w-full text-xs text-slate-500 dark:text-slate-400 hover:text-red-600 dark:hover:text-red-400 underline cursor-pointer"
        >
          Lost your Secret Key or master password? Reset the vault…
        </button>
      {:else}
        <div class="space-y-3">
          <div
            class="flex items-start gap-2 text-xs text-red-700 dark:text-red-400"
          >
            <AlertCircle size={14} class="mt-0.5 shrink-0" />
            <span>
              <strong
                >This will permanently delete every item in your vault.</strong
              >
              The server keeps no plaintext copy and cannot recover it. After reset
              you'll be able to create a fresh vault with a new master password and
              a brand-new Secret Key.
            </span>
          </div>
          <div>
            <label
              class="block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1"
              for="reset-confirm"
            >
              Type <span class="font-mono text-red-600 dark:text-red-400"
                >RESET</span
              > to confirm
            </label>
            <input
              id="reset-confirm"
              type="text"
              bind:value={resetConfirm}
              autocomplete="off"
              spellcheck="false"
              class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-red-500"
            />
          </div>
          {#if resetErr}
            <div class="text-xs text-red-600 dark:text-red-400">{resetErr}</div>
          {/if}
          <div class="flex gap-2">
            <button
              type="button"
              onclick={doReset}
              disabled={resetBusy ||
                resetConfirm.trim().toUpperCase() !== "RESET"}
              class="flex-1 flex items-center justify-center gap-2 px-3 py-2 text-sm rounded bg-red-600 text-white font-medium hover:bg-red-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
            >
              {#if resetBusy}<Loader2 size={14} class="animate-spin" />{/if}
              Reset vault permanently
            </button>
            <button
              type="button"
              onclick={() => {
                showReset = false;
                resetErr = "";
                resetConfirm = "";
              }}
              disabled={resetBusy}
              class="px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-700 hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer"
            >
              Cancel
            </button>
          </div>
        </div>
      {/if}
    </div>
  </div>
</div>
