<script lang="ts">
 import axios from 'axios';
 import { onMount } from 'svelte';
 import { Key, Plus, Trash2, Edit3, Smartphone, Usb, Wifi, Globe, Check, X } from 'lucide-svelte';

 import { addToast } from '@/lib/store/toast.svelte';
 import {
 isWebAuthnSupported,
 startRegistration,
 type ServerCreationOptions,
 } from '@/lib/webauthn';

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

 let passkeys = $state<PasskeyCredential[]>([]);
 let loading = $state(true);
 let loadError = $state('');
 let webauthnSupported = $state(true);

 // Enrollment state.
 let enrolling = $state(false);
 let newName = $state('');

 // Rename state.
 let renamingID = $state<string | null>(null);
 let renameDraft = $state('');

 onMount(async () => {
  webauthnSupported = isWebAuthnSupported();
  await loadPasskeys();
 });

 async function loadPasskeys() {
  loading = true;
  loadError = '';
  try {
   const res = await axios.get<PasskeyCredential[]>('/api/v1/me/passkeys');
   passkeys = Array.isArray(res.data) ? res.data : [];
  } catch (err: any) {
   // 503 means the deployment hasn't configured RPID — we surface
   // a friendly "feature off" placeholder rather than a noisy error.
   if (err?.response?.status === 503) {
    passkeys = [];
    loadError = 'Passkey is not configured on this server.';
   } else {
    loadError = err?.response?.data?.message ?? err?.message ?? 'Failed to load passkeys';
   }
  } finally {
   loading = false;
  }
 }

 async function handleEnroll() {
  if (!webauthnSupported) {
   addToast('Your browser does not support passkeys.', 'alert');
   return;
  }
  enrolling = true;
  try {
   // Begin: server returns session_id + options. The options are in
   // the WebAuthn wire shape (base64url-encoded ArrayBuffers) and
   // need translation before they're handed to the browser API.
   const beginRes = await axios.post<{ session_id: string; options: ServerCreationOptions }>(
    '/api/v1/me/passkeys/begin',
    { name: newName.trim() }
   );
   const { session_id, options } = beginRes.data;

   // Browser ceremony. Throws on user-cancel (NotAllowedError) and
   // on duplicate enrollment (InvalidStateError). Both surface as
   // a toast — the server has already discarded the challenge so
   // there's no clean-up to do.
   const response = await startRegistration(options);

   // Finish: server validates the assertion and persists the row.
   const finishRes = await axios.post<PasskeyCredential>(
    '/api/v1/me/passkeys/finish',
    { session_id, name: newName.trim(), response }
   );

   passkeys = [finishRes.data, ...passkeys];
   addToast(`Passkey "${finishRes.data.name}" added`, 'success');
   newName = '';
  } catch (err: any) {
   const code = err?.name ?? '';
   const msg = err?.response?.data?.message ?? err?.message ?? 'Enrollment failed';
   if (code === 'NotAllowedError') {
    // User cancelled or timeout — silent is friendlier here.
    addToast('Enrollment cancelled', 'info');
   } else if (code === 'InvalidStateError') {
    addToast('That device is already enrolled', 'alert');
   } else {
    addToast(`Enroll failed: ${msg}`, 'alert');
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
  renameDraft = '';
 }

 async function saveRename(p: PasskeyCredential) {
  const name = renameDraft.trim();
  if (!name || name === p.name) {
   cancelRename();
   return;
  }
  try {
   const res = await axios.patch<PasskeyCredential>(`/api/v1/me/passkeys/${p.id}`, { name });
   passkeys = passkeys.map(x => x.id === p.id ? res.data : x);
   addToast('Passkey renamed', 'success');
  } catch (err: any) {
   const msg = err?.response?.data?.message ?? err?.message ?? 'Rename failed';
   addToast(`Rename failed: ${msg}`, 'alert');
  } finally {
   cancelRename();
  }
 }

 async function handleDelete(p: PasskeyCredential) {
  if (!confirm(`Delete passkey "${p.name}"? You won't be able to sign in with it again.`)) return;
  try {
   await axios.delete(`/api/v1/me/passkeys/${p.id}`);
   passkeys = passkeys.filter(x => x.id !== p.id);
   addToast(`Passkey "${p.name}" deleted`, 'success');
  } catch (err: any) {
   const msg = err?.response?.data?.message ?? err?.message ?? 'Delete failed';
   addToast(`Delete failed: ${msg}`, 'alert');
  }
 }

 // Transport pretty-printer: shows a tiny icon next to the row so
 // users can tell at a glance "this is my phone" vs "this is my
 // hardware key".
 function transportIcon(transports?: string[]): typeof Smartphone {
  if (!transports || transports.length === 0) return Key;
  if (transports.includes('internal')) return Smartphone;
  if (transports.includes('usb')) return Usb;
  if (transports.includes('nfc') || transports.includes('ble')) return Wifi;
  if (transports.includes('hybrid')) return Globe;
  return Key;
 }

 function formatDate(s?: string): string {
  if (!s) return '';
  const d = new Date(s);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleString(undefined, {
   year: 'numeric', month: 'short', day: 'numeric',
   hour: '2-digit', minute: '2-digit',
  });
 }

 function timeAgo(s?: string): string {
  if (!s) return 'never';
  const d = new Date(s);
  if (isNaN(d.getTime())) return '';
  const diff = (Date.now() - d.getTime()) / 1000;
  if (diff < 60) return 'just now';
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  if (diff < 604800) return `${Math.floor(diff / 86400)}d ago`;
  return formatDate(s);
 }
</script>

<div>
 <div class="mb-4 flex items-start justify-between gap-4">
  <div>
   <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">Account Security</h2>
   <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
    Manage how you sign in to pika. Passkeys use your device's biometric (Touch ID, Windows Hello)
    or a hardware security key instead of a password.
   </p>
  </div>
 </div>

 <!-- Passkeys section -->
 <div class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-5">
  <div class="flex items-center justify-between mb-3">
   <div class="flex items-center gap-2">
    <Key size={16} class="text-accent-600 dark:text-accent-400" />
    <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-100">Passkeys</h3>
   </div>
   <span class="text-xs text-slate-400 dark:text-slate-500">{passkeys.length} enrolled</span>
  </div>

  {#if !webauthnSupported}
   <div class="p-3 bg-amber-50 dark:bg-amber-900/30 border border-amber-200 dark:border-amber-800 rounded-md text-sm text-amber-800 dark:text-amber-200">
    Your browser doesn't support passkeys. Try a recent version of Chrome, Edge, Firefox, or Safari.
   </div>
  {:else if loadError}
   <div class="p-3 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md text-sm text-slate-600 dark:text-slate-300">
    {loadError}
   </div>
  {:else}
   <!-- Enroll form -->
   <form
    class="flex items-end gap-2 mb-4"
    onsubmit={(e) => { e.preventDefault(); handleEnroll(); }}
   >
    <div class="flex-1">
     <label for="passkey-name" class="block text-xs font-medium text-slate-600 dark:text-slate-300 mb-1">
      New passkey name <span class="text-slate-400 font-normal">(optional)</span>
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
     {enrolling ? 'Waiting for device...' : 'Add passkey'}
    </button>
   </form>

   <!-- Passkey list -->
   {#if loading}
    <div class="text-center text-sm text-slate-400 dark:text-slate-500 py-6">Loading...</div>
   {:else if passkeys.length === 0}
    <div class="text-center text-sm text-slate-500 dark:text-slate-400 py-6 border border-dashed border-slate-200 dark:border-warm-700 rounded-md">
     No passkeys enrolled yet. Add one above.
    </div>
   {:else}
    <ul class="divide-y divide-slate-200 dark:divide-warm-700">
     {#each passkeys as p (p.id)}
      {@const Icon = transportIcon(p.transports)}
      <li class="py-3 flex items-center gap-3">
       <Icon size={18} class="text-slate-500 dark:text-slate-400 shrink-0" />
       <div class="flex-1 min-w-0">
        {#if renamingID === p.id}
         <div class="flex items-center gap-1">
          <input
           type="text"
           bind:value={renameDraft}
           maxlength="64"
           onkeydown={(e) => {
            if (e.key === 'Enter') { e.preventDefault(); saveRename(p); }
            else if (e.key === 'Escape') { e.preventDefault(); cancelRename(); }
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
         <div class="font-medium text-sm text-slate-800 dark:text-slate-100 truncate">{p.name}</div>
         <div class="text-xs text-slate-500 dark:text-slate-400 flex items-center gap-2 mt-0.5">
          <span>Added {formatDate(p.created_at)}</span>
          <span aria-hidden="true">·</span>
          <span>Last used: {timeAgo(p.last_used_at)}</span>
          {#if p.backup_state}
           <span aria-hidden="true">·</span>
           <span class="text-accent-600 dark:text-accent-400" title="Synced to cloud (e.g. iCloud Keychain)">synced</span>
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
</div>
