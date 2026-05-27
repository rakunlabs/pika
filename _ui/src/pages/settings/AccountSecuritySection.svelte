<script lang="ts">
  import axios from "axios";
  import { onMount } from "svelte";
  import {
    Key,
    Plus,
    Trash2,
    Edit3,
    Smartphone,
    Usb,
    Wifi,
    Globe,
    Check,
    X,
    ShieldCheck,
    ShieldAlert,
    Copy,
    RefreshCw,
    AlertTriangle,
  } from "lucide-svelte";
  import QRCode from "qrcode";

  import { addToast } from "@/lib/store/toast.svelte";
  import {
    isWebAuthnSupported,
    startRegistration,
    type ServerCreationOptions,
  } from "@/lib/webauthn";

  // PasskeyCredential mirrors service.PasskeyCredential (JSON shape).
  // PublicKey is server-side only ("-" json tag) so we never receive it.
  interface PasskeyCredential {
    id: string;
    user_id: string;
    credential_id: string;
    aaguid?: string;
    sign_count: number;
    transports?: string[];
    user_verified: boolean;
    backup_eligible: boolean;
    backup_state: boolean;
    attestation_type?: string;
    name: string;
    created_at: string;
    last_used_at?: string;
  }

  // TOTPStatus mirrors service.TOTPStatus. Returned by GET /me/totp;
  // the Enabled / PendingEnrollment pair tells the UI which of the
  // three states to render: not enrolled / pending finish / live.
  interface TOTPStatus {
    enabled: boolean;
    created_at?: string;
    last_used_at?: string;
    recovery_codes_left: number;
    pending_enrollment: boolean;
  }

  // TOTPEnrollment mirrors service.TOTPEnrollment. The OTPAuthURL is
  // the canonical authenticator URI; we render a QR from it client-
  // side via the qrcode package (small, no eval, works offline).
  interface TOTPEnrollment {
    secret_base32: string;
    otpauth_url: string;
    period: number;
    digits: number;
    algorithm: string;
  }

  let passkeys = $state<PasskeyCredential[]>([]);
  let loading = $state(true);
  let loadError = $state("");
  let webauthnSupported = $state(true);

  // Enrollment state.
  let enrolling = $state(false);
  let newName = $state("");
  // Optional hint that scopes the registration ceremony to one class
  // of authenticator. Empty string = let the browser show its chooser
  // (the default). The values match WebAuthn's authenticatorAttachment
  // enum and are forwarded verbatim to the server.
  let newAttachment = $state<"" | "platform" | "cross-platform">("");

  // Rename state.
  let renamingID = $state<string | null>(null);
  let renameDraft = $state("");

  // ── TOTP state ──
  let totpStatus = $state<TOTPStatus | null>(null);
  let totpLoading = $state(true);
  let totpLoadError = $state("");

  // Enrollment flow. enrollment holds the server-issued secret +
  // URL while the user is scanning the QR; qrDataURI is the PNG
  // we render in the page. Cleared on cancel / finish.
  let enrollment = $state<TOTPEnrollment | null>(null);
  let qrDataURI = $state("");
  let enrollmentCode = $state("");
  let enrollmentBusy = $state(false);

  // Recovery codes shown ONCE after successful enrollment or
  // regenerate. The user must save these — we keep them in
  // component state until they dismiss the modal so a refresh
  // doesn't lose them. We never refetch the codes from the server.
  let lastRecoveryCodes = $state<string[]>([]);

  // Disable + regenerate password gates. Re-auth via password
  // confirms the user is at the keyboard.
  let disablePassword = $state("");
  let disableBusy = $state(false);
  let showDisable = $state(false);

  let regenPassword = $state("");
  let regenBusy = $state(false);
  let showRegen = $state(false);

  onMount(async () => {
    webauthnSupported = isWebAuthnSupported();
    await Promise.all([loadPasskeys(), loadTOTPStatus()]);
  });

  // ── TOTP load + helpers ──

  async function loadTOTPStatus() {
    totpLoading = true;
    totpLoadError = "";
    try {
      const res = await axios.get<TOTPStatus>("/api/v1/me/totp");
      totpStatus = res.data;
    } catch (err: any) {
      totpStatus = null;
      totpLoadError =
        err?.response?.data?.message ??
        err?.message ??
        "Failed to load TOTP status";
    } finally {
      totpLoading = false;
    }
  }

  async function startTOTPEnrollment() {
    enrollmentBusy = true;
    try {
      const res = await axios.post<TOTPEnrollment>("/api/v1/me/totp/begin", {});
      enrollment = res.data;
      // Render the QR client-side. Margin 1 = quiet zone ≈ 4 modules,
      // size 240 fits the typical settings card without scrolling.
      qrDataURI = await QRCode.toDataURL(res.data.otpauth_url, {
        margin: 1,
        width: 240,
        errorCorrectionLevel: "M",
      });
      enrollmentCode = "";
      // The page-level state is now "pending"; refresh status so the
      // header reflects the in-progress state.
      await loadTOTPStatus();
    } catch (err: any) {
      const msg =
        err?.response?.data?.message ??
        err?.message ??
        "Could not start enrollment";
      addToast(msg, "alert");
    } finally {
      enrollmentBusy = false;
    }
  }

  function cancelEnrollment() {
    enrollment = null;
    qrDataURI = "";
    enrollmentCode = "";
  }

  async function finishTOTPEnrollment() {
    if (!enrollment) return;
    const code = enrollmentCode.trim();
    if (!/^\d{6}$/.test(code)) {
      addToast("Enter the 6-digit code from your authenticator app", "alert");
      return;
    }
    enrollmentBusy = true;
    try {
      const res = await axios.post<{ recovery_codes: string[] }>(
        "/api/v1/me/totp/finish",
        { code },
      );
      lastRecoveryCodes = res.data.recovery_codes ?? [];
      addToast("TOTP enabled — save your recovery codes!", "success");
      enrollment = null;
      qrDataURI = "";
      enrollmentCode = "";
      await loadTOTPStatus();
    } catch (err: any) {
      const msg =
        err?.response?.data?.message ?? err?.message ?? "Verification failed";
      addToast(msg, "alert");
    } finally {
      enrollmentBusy = false;
    }
  }

  async function disableTOTP() {
    if (!disablePassword) {
      addToast("Enter your password to confirm", "alert");
      return;
    }
    disableBusy = true;
    try {
      await axios.delete("/api/v1/me/totp", {
        data: { password: disablePassword },
      });
      addToast("TOTP disabled", "success");
      disablePassword = "";
      showDisable = false;
      await loadTOTPStatus();
    } catch (err: any) {
      const msg =
        err?.response?.data?.message ??
        err?.message ??
        "Failed to disable TOTP";
      addToast(msg, "alert");
    } finally {
      disableBusy = false;
    }
  }

  async function regenerateRecoveryCodes() {
    if (!regenPassword) {
      addToast("Enter your password to confirm", "alert");
      return;
    }
    regenBusy = true;
    try {
      const res = await axios.post<{ recovery_codes: string[] }>(
        "/api/v1/me/totp/recovery-codes",
        { password: regenPassword },
      );
      lastRecoveryCodes = res.data.recovery_codes ?? [];
      addToast("New recovery codes generated — save them now", "success");
      regenPassword = "";
      showRegen = false;
      await loadTOTPStatus();
    } catch (err: any) {
      const msg =
        err?.response?.data?.message ??
        err?.message ??
        "Failed to regenerate codes";
      addToast(msg, "alert");
    } finally {
      regenBusy = false;
    }
  }

  // Copy the secret string (for users whose camera won't scan) or
  // an arbitrary text blob (recovery codes). navigator.clipboard
  // requires a secure context (HTTPS or localhost) — fall back to a
  // textarea + execCommand on older / http deployments.
  async function copyToClipboard(text: string, label: string) {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
      } else {
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
      }
      addToast(`${label} copied`, "success");
    } catch {
      addToast(`Could not copy ${label.toLowerCase()}`, "alert");
    }
  }

  function dismissRecoveryCodes() {
    lastRecoveryCodes = [];
  }

  async function loadPasskeys() {
    loading = true;
    loadError = "";
    try {
      const res = await axios.get<PasskeyCredential[]>("/api/v1/me/passkeys");
      passkeys = Array.isArray(res.data) ? res.data : [];
    } catch (err: any) {
      // 503 means the deployment hasn't configured RPID — we surface
      // a friendly "feature off" placeholder rather than a noisy error.
      if (err?.response?.status === 503) {
        passkeys = [];
        loadError = "Passkey is not configured on this server.";
      } else {
        loadError =
          err?.response?.data?.message ??
          err?.message ??
          "Failed to load passkeys";
      }
    } finally {
      loading = false;
    }
  }

  async function handleEnroll() {
    if (!webauthnSupported) {
      addToast("Your browser does not support passkeys.", "alert");
      return;
    }
    enrolling = true;
    try {
      // Begin: server returns session_id + options. The options are in
      // the WebAuthn wire shape (base64url-encoded ArrayBuffers) and
      // need translation before they're handed to the browser API.
      //
      // The attachment field is optional and only sent when the user
      // explicitly picked a non-default value, so deployments that
      // don't care never see it round-trip through the wire.
      const beginBody: { name: string; attachment?: string } = {
        name: newName.trim(),
      };
      if (newAttachment) beginBody.attachment = newAttachment;
      const beginRes = await axios.post<{
        session_id: string;
        options: ServerCreationOptions;
      }>("/api/v1/me/passkeys/begin", beginBody);
      const { session_id, options } = beginRes.data;

      // Browser ceremony. Throws on user-cancel (NotAllowedError) and
      // on duplicate enrollment (InvalidStateError). Both surface as
      // a toast — the server has already discarded the challenge so
      // there's no clean-up to do.
      const response = await startRegistration(options);

      // Finish: server validates the assertion and persists the row.
      const finishRes = await axios.post<PasskeyCredential>(
        "/api/v1/me/passkeys/finish",
        { session_id, name: newName.trim(), response },
      );

      passkeys = [finishRes.data, ...passkeys];
      addToast(`Passkey "${finishRes.data.name}" added`, "success");
      newName = "";
      newAttachment = "";
    } catch (err: any) {
      const code = err?.name ?? "";
      const msg =
        err?.response?.data?.message ?? err?.message ?? "Enrollment failed";
      if (code === "NotAllowedError") {
        // User cancelled or timeout — silent is friendlier here.
        addToast("Enrollment cancelled", "info");
      } else if (code === "InvalidStateError") {
        addToast("That device is already enrolled", "alert");
      } else {
        addToast(`Enroll failed: ${msg}`, "alert");
      }
    } finally {
      enrolling = false;
    }
  }

  function startRename(p: PasskeyCredential) {
    renamingID = p.id;
    renameDraft = p.name;
  }

  function cancelRename() {
    renamingID = null;
    renameDraft = "";
  }

  async function saveRename(p: PasskeyCredential) {
    const name = renameDraft.trim();
    if (!name || name === p.name) {
      cancelRename();
      return;
    }
    try {
      const res = await axios.patch<PasskeyCredential>(
        `/api/v1/me/passkeys/${p.id}`,
        { name },
      );
      passkeys = passkeys.map((x) => (x.id === p.id ? res.data : x));
      addToast("Passkey renamed", "success");
    } catch (err: any) {
      const msg =
        err?.response?.data?.message ?? err?.message ?? "Rename failed";
      addToast(`Rename failed: ${msg}`, "alert");
    } finally {
      cancelRename();
    }
  }

  async function handleDelete(p: PasskeyCredential) {
    // Stronger confirmation when this is the user's last passkey: if
    // they don't also know their password (or that login path isn't
    // configured) they'll be locked out. We don't have a clean signal
    // from the server about which other login methods the user can
    // use, so we rely on the count alone — most pika deployments keep
    // password login alive, but warning out loud is cheap insurance.
    const isLastOne = passkeys.length === 1;
    const message = isLastOne
      ? `Delete passkey "${p.name}"?\n\nThis is your only passkey. After deleting, you'll need to sign in with another method (password, OAuth, etc.). If you don't have one configured, you may be locked out.`
      : `Delete passkey "${p.name}"? You won't be able to sign in with it again.`;
    if (!confirm(message)) return;
    try {
      await axios.delete(`/api/v1/me/passkeys/${p.id}`);
      passkeys = passkeys.filter((x) => x.id !== p.id);
      addToast(`Passkey "${p.name}" deleted`, "success");
    } catch (err: any) {
      const msg =
        err?.response?.data?.message ?? err?.message ?? "Delete failed";
      addToast(`Delete failed: ${msg}`, "alert");
    }
  }

  // Transport pretty-printer: shows a tiny icon next to the row so
  // users can tell at a glance "this is my phone" vs "this is my
  // hardware key".
  function transportIcon(transports?: string[]): typeof Smartphone {
    if (!transports || transports.length === 0) return Key;
    if (transports.includes("internal")) return Smartphone;
    if (transports.includes("usb")) return Usb;
    if (transports.includes("nfc") || transports.includes("ble")) return Wifi;
    if (transports.includes("hybrid")) return Globe;
    return Key;
  }

  function formatDate(s?: string): string {
    if (!s) return "";
    const d = new Date(s);
    if (isNaN(d.getTime())) return "";
    return d.toLocaleString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  }

  function timeAgo(s?: string): string {
    if (!s) return "never";
    const d = new Date(s);
    if (isNaN(d.getTime())) return "";
    const diff = (Date.now() - d.getTime()) / 1000;
    if (diff < 60) return "just now";
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
    if (diff < 604800) return `${Math.floor(diff / 86400)}d ago`;
    return formatDate(s);
  }
</script>

<div>
  <div class="mb-4 flex items-start justify-between gap-4">
    <div>
      <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">
        Account Security
      </h2>
      <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
        Manage how you sign in to pika. Passkeys use your device's biometric
        (Touch ID, Windows Hello) or a hardware security key instead of a
        password.
      </p>
    </div>
  </div>

  <!-- Passkeys section -->
  <div
    class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-5"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <Key size={16} class="text-accent-600 dark:text-accent-400" />
        <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-100">
          Passkeys
        </h3>
      </div>
      <span class="text-xs text-slate-400 dark:text-slate-500"
        >{passkeys.length} enrolled</span
      >
    </div>

    {#if !webauthnSupported}
      <div
        class="p-3 bg-amber-50 dark:bg-amber-900/30 border border-amber-200 dark:border-amber-800 rounded-md text-sm text-amber-800 dark:text-amber-200"
      >
        Your browser doesn't support passkeys. Try a recent version of Chrome,
        Edge, Firefox, or Safari.
      </div>
    {:else if loadError}
      <div
        class="p-3 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md text-sm text-slate-600 dark:text-slate-300"
      >
        {loadError}
      </div>
    {:else}
      <!-- Enroll form -->
      <form
        class="mb-4 space-y-2"
        onsubmit={(e) => {
          e.preventDefault();
          handleEnroll();
        }}
      >
        <div class="flex items-end gap-2">
          <div class="flex-1">
            <label
              for="passkey-name"
              class="block text-xs font-medium text-slate-600 dark:text-slate-300 mb-1"
            >
              New passkey name <span class="text-slate-400 font-normal"
                >(optional)</span
              >
            </label>
            <input
              id="passkey-name"
              type="text"
              bind:value={newName}
              placeholder="e.g. iPhone, YubiKey 5"
              maxlength="64"
              class="w-full px-3 py-1.5 border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-accent-500 focus:border-transparent"
            />
          </div>
          <button
            type="submit"
            disabled={enrolling}
            class="inline-flex items-center gap-1.5 px-3 py-1.5 bg-accent-600 hover:bg-accent-700 text-white text-sm font-medium rounded-md cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <Plus size={14} />
            {enrolling ? "Waiting for device..." : "Add passkey"}
          </button>
        </div>
        <!-- Authenticator-attachment hint. Most users want "Any device"
         and never touch this — collapsed inside a <details> so the
         common case stays uncluttered. The select is bound to a
         union-typed state var so a typo can't slip through. -->
        <details class="text-xs">
          <summary
            class="cursor-pointer text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 select-none"
          >
            Advanced: device preference
          </summary>
          <div class="mt-2 flex items-center gap-2">
            <label
              for="passkey-attachment"
              class="text-xs text-slate-600 dark:text-slate-300 shrink-0"
            >
              Authenticator:
            </label>
            <select
              id="passkey-attachment"
              bind:value={newAttachment}
              class="flex-1 px-2 py-1 text-xs border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-700 dark:text-slate-200 rounded-md focus:outline-none focus:ring-2 focus:ring-accent-500"
            >
              <option value="">Any device (recommended)</option>
              <option value="platform"
                >This device only (Touch ID, Windows Hello)</option
              >
              <option value="cross-platform"
                >Security key (USB / NFC / BLE)</option
              >
            </select>
          </div>
          <p
            class="mt-1 text-[10px] text-slate-400 dark:text-slate-500 leading-snug"
          >
            Pick "Any device" unless you specifically want to enroll a hardware
            key or restrict this passkey to the current device. Browsers honor
            this as a preference — if no matching authenticator is available the
            prompt still falls back to the chooser.
          </p>
        </details>
      </form>

      <!-- Passkey list -->
      {#if loading}
        <div
          class="text-center text-sm text-slate-400 dark:text-slate-500 py-6"
        >
          Loading...
        </div>
      {:else if passkeys.length === 0}
        <div
          class="text-center text-sm text-slate-500 dark:text-slate-400 py-6 border border-dashed border-slate-200 dark:border-warm-700 rounded-md"
        >
          No passkeys enrolled yet. Add one above.
        </div>
      {:else}
        <ul class="divide-y divide-slate-200 dark:divide-warm-700">
          {#each passkeys as p (p.id)}
            {@const Icon = transportIcon(p.transports)}
            <li class="py-3 flex items-center gap-3">
              <Icon
                size={18}
                class="text-slate-500 dark:text-slate-400 shrink-0"
              />
              <div class="flex-1 min-w-0">
                {#if renamingID === p.id}
                  <div class="flex items-center gap-1">
                    <input
                      type="text"
                      bind:value={renameDraft}
                      maxlength="64"
                      onkeydown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          saveRename(p);
                        } else if (e.key === "Escape") {
                          e.preventDefault();
                          cancelRename();
                        }
                      }}
                      class="flex-1 px-2 py-1 text-sm border border-accent-300 dark:border-accent-700 bg-white dark:bg-warm-900 rounded-md focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                    <button
                      type="button"
                      class="p-1 rounded text-accent-600 hover:text-accent-700 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
                      onclick={() => saveRename(p)}
                      title="Save"
                    >
                      <Check size={14} />
                    </button>
                    <button
                      type="button"
                      class="p-1 rounded text-slate-400 hover:text-slate-600 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
                      onclick={cancelRename}
                      title="Cancel"
                    >
                      <X size={14} />
                    </button>
                  </div>
                {:else}
                  <div
                    class="font-medium text-sm text-slate-800 dark:text-slate-100 truncate"
                  >
                    {p.name}
                  </div>
                  <div
                    class="text-xs text-slate-500 dark:text-slate-400 flex items-center gap-2 mt-0.5"
                  >
                    <span>Added {formatDate(p.created_at)}</span>
                    <span aria-hidden="true">·</span>
                    <span>Last used: {timeAgo(p.last_used_at)}</span>
                    {#if p.backup_state}
                      <span aria-hidden="true">·</span>
                      <span
                        class="text-accent-600 dark:text-accent-400"
                        title="Synced to cloud (e.g. iCloud Keychain)"
                        >synced</span
                      >
                    {/if}
                  </div>
                {/if}
              </div>
              {#if renamingID !== p.id}
                <div class="flex items-center gap-1">
                  <button
                    type="button"
                    class="p-1.5 rounded text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-white hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
                    onclick={() => startRename(p)}
                    title="Rename"
                  >
                    <Edit3 size={14} />
                  </button>
                  <button
                    type="button"
                    class="p-1.5 rounded text-slate-500 dark:text-slate-400 hover:text-vermilion-600 dark:hover:text-vermilion-400 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
                    onclick={() => handleDelete(p)}
                    title="Delete"
                  >
                    <Trash2 size={14} />
                  </button>
                </div>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    {/if}
  </div>

  <!-- ── TOTP / 2FA section ── -->
  <div
    class="mt-4 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-5"
  >
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        {#if totpStatus?.enabled}
          <ShieldCheck size={16} class="text-accent-600 dark:text-accent-400" />
        {:else}
          <ShieldAlert size={16} class="text-slate-400 dark:text-slate-500" />
        {/if}
        <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-100">
          Two-factor authentication (TOTP)
        </h3>
      </div>
      {#if totpStatus?.enabled}
        <span
          class="inline-flex items-center gap-1 px-2 py-0.5 bg-emerald-50 dark:bg-emerald-900/30 border border-emerald-200 dark:border-emerald-800 rounded text-[10px] font-medium text-emerald-700 dark:text-emerald-300 uppercase tracking-wide"
        >
          Enabled
        </span>
      {:else if totpStatus?.pending_enrollment}
        <span
          class="inline-flex items-center gap-1 px-2 py-0.5 bg-amber-50 dark:bg-amber-900/30 border border-amber-200 dark:border-amber-800 rounded text-[10px] font-medium text-amber-700 dark:text-amber-300 uppercase tracking-wide"
        >
          Pending
        </span>
      {/if}
    </div>

    <p class="text-xs text-slate-500 dark:text-slate-400 mb-4 leading-relaxed">
      Adds a 6-digit code from an authenticator app (Google Authenticator,
      1Password, Authy, Bitwarden, ...) as a second factor after your password.
      After it is enabled every login requires entering the current code from
      your app.
    </p>

    {#if totpLoading}
      <div class="text-center text-sm text-slate-400 dark:text-slate-500 py-6">
        Loading...
      </div>
    {:else if totpLoadError}
      <div
        class="p-3 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md text-sm text-slate-600 dark:text-slate-300"
      >
        {totpLoadError}
      </div>
    {:else if enrollment}
      <!-- Active enrollment: show QR + code input -->
      <div class="space-y-4">
        <div class="flex flex-col sm:flex-row gap-4 items-start">
          {#if qrDataURI}
            <img
              src={qrDataURI}
              alt="Scan this QR with your authenticator app"
              class="w-60 h-60 border border-slate-200 dark:border-warm-700 rounded-md bg-white p-2"
            />
          {/if}
          <div
            class="flex-1 min-w-0 text-sm text-slate-700 dark:text-slate-200 space-y-3"
          >
            <div>
              <div class="font-medium mb-1">
                1. Scan the QR with your authenticator app
              </div>
              <div class="text-xs text-slate-500 dark:text-slate-400">
                Or tap "Can't scan?" below to copy the secret and add the
                account manually.
              </div>
            </div>
            <details class="text-xs">
              <summary
                class="cursor-pointer text-slate-600 dark:text-slate-300 hover:text-slate-800 dark:hover:text-white"
              >
                Can't scan? Enter the secret manually
              </summary>
              <div
                class="mt-2 p-2 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md font-mono text-[11px] break-all flex items-center gap-1"
              >
                <span class="flex-1">{enrollment.secret_base32}</span>
                <button
                  type="button"
                  class="p-1 rounded text-slate-400 hover:text-slate-700 dark:hover:text-white hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
                  onclick={() =>
                    copyToClipboard(enrollment!.secret_base32, "Secret")}
                  title="Copy secret"
                >
                  <Copy size={12} />
                </button>
              </div>
              <div class="mt-1 text-[10px] text-slate-400 dark:text-slate-500">
                Algorithm: {enrollment.algorithm} · Digits: {enrollment.digits} ·
                Period: {enrollment.period}s
              </div>
            </details>
            <div>
              <div class="font-medium mb-1">
                2. Enter the 6-digit code your app shows
              </div>
              <div class="flex gap-2">
                <input
                  type="text"
                  inputmode="numeric"
                  maxlength="6"
                  autocomplete="one-time-code"
                  bind:value={enrollmentCode}
                  placeholder="123456"
                  onkeydown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      finishTOTPEnrollment();
                    }
                  }}
                  class="w-32 px-3 py-1.5 text-center font-mono text-lg border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 rounded-md focus:outline-none focus:ring-2 focus:ring-accent-500 tracking-widest"
                />
                <button
                  type="button"
                  disabled={enrollmentBusy}
                  onclick={finishTOTPEnrollment}
                  class="inline-flex items-center gap-1.5 px-3 py-1.5 bg-accent-600 hover:bg-accent-700 text-white text-sm font-medium rounded-md cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  <Check size={14} />
                  Verify &amp; enable
                </button>
                <button
                  type="button"
                  onclick={cancelEnrollment}
                  class="inline-flex items-center gap-1.5 px-3 py-1.5 bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 text-sm font-medium rounded-md cursor-pointer"
                >
                  Cancel
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    {:else if totpStatus?.enabled}
      <!-- Enabled: show metadata + disable + regenerate -->
      <div class="space-y-3">
        <div class="text-xs text-slate-500 dark:text-slate-400">
          Enabled {totpStatus.created_at
            ? new Date(totpStatus.created_at).toLocaleString()
            : ""}
          · Recovery codes left:
          <span
            class="font-medium {totpStatus.recovery_codes_left < 3
              ? 'text-vermilion-600 dark:text-vermilion-400'
              : 'text-slate-700 dark:text-slate-200'}"
            >{totpStatus.recovery_codes_left}</span
          >
        </div>

        {#if totpStatus.recovery_codes_left < 3 && totpStatus.recovery_codes_left > 0}
          <div
            class="p-2 bg-amber-50 dark:bg-amber-900/30 border border-amber-200 dark:border-amber-800 rounded-md text-xs text-amber-800 dark:text-amber-200 flex items-start gap-2"
          >
            <AlertTriangle size={14} class="shrink-0 mt-0.5" />
            <span
              >You have very few recovery codes left. Regenerate to get a fresh
              set.</span
            >
          </div>
        {/if}
        {#if totpStatus.recovery_codes_left === 0}
          <div
            class="p-2 bg-vermilion-50 dark:bg-vermilion-900/30 border border-vermilion-200 dark:border-vermilion-800 rounded-md text-xs text-vermilion-800 dark:text-vermilion-200 flex items-start gap-2"
          >
            <AlertTriangle size={14} class="shrink-0 mt-0.5" />
            <span
              >No recovery codes remaining — if you lose your authenticator you
              will be locked out. Regenerate now.</span
            >
          </div>
        {/if}

        <div class="flex flex-wrap gap-2">
          <button
            type="button"
            onclick={() => {
              showRegen = !showRegen;
              showDisable = false;
            }}
            class="inline-flex items-center gap-1.5 px-3 py-1.5 bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 text-sm font-medium rounded-md cursor-pointer"
          >
            <RefreshCw size={14} />
            Regenerate recovery codes
          </button>
          <button
            type="button"
            onclick={() => {
              showDisable = !showDisable;
              showRegen = false;
            }}
            class="inline-flex items-center gap-1.5 px-3 py-1.5 bg-vermilion-50 dark:bg-vermilion-900/30 hover:bg-vermilion-100 dark:hover:bg-vermilion-900/50 border border-vermilion-200 dark:border-vermilion-800 text-vermilion-700 dark:text-vermilion-300 text-sm font-medium rounded-md cursor-pointer"
          >
            <Trash2 size={14} />
            Disable TOTP
          </button>
        </div>

        {#if showRegen}
          <div
            class="p-3 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md space-y-2"
          >
            <p class="text-xs text-slate-600 dark:text-slate-300">
              Confirm your password to regenerate. The new set will invalidate
              every previous recovery code.
            </p>
            <div class="flex gap-2">
              <input
                type="password"
                bind:value={regenPassword}
                placeholder="Your password"
                autocomplete="current-password"
                onkeydown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    regenerateRecoveryCodes();
                  }
                }}
                class="flex-1 px-3 py-1.5 text-sm border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 rounded-md focus:outline-none focus:ring-2 focus:ring-accent-500"
              />
              <button
                type="button"
                disabled={regenBusy}
                onclick={regenerateRecoveryCodes}
                class="px-3 py-1.5 bg-accent-600 hover:bg-accent-700 text-white text-sm font-medium rounded-md cursor-pointer disabled:opacity-50"
              >
                Regenerate
              </button>
            </div>
          </div>
        {/if}

        {#if showDisable}
          <div
            class="p-3 bg-vermilion-50/50 dark:bg-vermilion-900/10 border border-vermilion-200 dark:border-vermilion-800 rounded-md space-y-2"
          >
            <p class="text-xs text-vermilion-700 dark:text-vermilion-300">
              Confirm your password to disable. After this you sign in with
              password only.
            </p>
            <div class="flex gap-2">
              <input
                type="password"
                bind:value={disablePassword}
                placeholder="Your password"
                autocomplete="current-password"
                onkeydown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    disableTOTP();
                  }
                }}
                class="flex-1 px-3 py-1.5 text-sm border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 rounded-md focus:outline-none focus:ring-2 focus:ring-vermilion-500"
              />
              <button
                type="button"
                disabled={disableBusy}
                onclick={disableTOTP}
                class="px-3 py-1.5 bg-vermilion-600 hover:bg-vermilion-700 text-white text-sm font-medium rounded-md cursor-pointer disabled:opacity-50"
              >
                Disable
              </button>
            </div>
          </div>
        {/if}
      </div>
    {:else}
      <!-- Not enrolled (and no pending): single "Enable" CTA -->
      <button
        type="button"
        disabled={enrollmentBusy}
        onclick={startTOTPEnrollment}
        class="inline-flex items-center gap-1.5 px-3 py-1.5 bg-accent-600 hover:bg-accent-700 text-white text-sm font-medium rounded-md cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
      >
        <Plus size={14} />
        Enable two-factor authentication
      </button>
      {#if totpStatus?.pending_enrollment}
        <p class="mt-2 text-[11px] text-amber-700 dark:text-amber-400">
          Previous enrollment was abandoned. Clicking the button above starts a
          fresh one — the old QR is no longer valid.
        </p>
      {/if}
    {/if}
  </div>

  <!-- Recovery codes modal: shown after enrollment / regen, dismissed by user. -->
  {#if lastRecoveryCodes.length > 0}
    <div
      class="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4"
    >
      <div
        class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-xl max-w-md w-full p-5 space-y-3"
      >
        <div class="flex items-start gap-2">
          <AlertTriangle
            size={18}
            class="text-amber-600 dark:text-amber-400 mt-0.5 shrink-0"
          />
          <div>
            <h3
              class="text-base font-semibold text-slate-800 dark:text-slate-100"
            >
              Save your recovery codes
            </h3>
            <p class="text-xs text-slate-500 dark:text-slate-400 mt-1">
              Each code can be used <strong>once</strong> if you lose access to
              your authenticator.
              <strong>Save them now</strong> — pika will never show them again.
            </p>
          </div>
        </div>

        <div
          class="p-3 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md font-mono text-xs grid grid-cols-2 gap-y-1 gap-x-3"
        >
          {#each lastRecoveryCodes as code}
            <div class="select-all">{code}</div>
          {/each}
        </div>

        <div class="flex gap-2 justify-end">
          <button
            type="button"
            onclick={() =>
              copyToClipboard(lastRecoveryCodes.join("\n"), "Recovery codes")}
            class="inline-flex items-center gap-1.5 px-3 py-1.5 bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 text-sm font-medium rounded-md cursor-pointer"
          >
            <Copy size={14} />
            Copy all
          </button>
          <button
            type="button"
            onclick={dismissRecoveryCodes}
            class="inline-flex items-center gap-1.5 px-3 py-1.5 bg-accent-600 hover:bg-accent-700 text-white text-sm font-medium rounded-md cursor-pointer"
          >
            I've saved them
          </button>
        </div>
      </div>
    </div>
  {/if}
</div>
