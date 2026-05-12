<script lang="ts">
 import { addToast } from "@/lib/store/toast.svelte";
 import { Eye, EyeOff, RotateCw } from "lucide-svelte";
 import axios from 'axios';

 // ── Rotation state ──
 let rotationAdminSecret = $state('');
 let rotationNewKey = $state('');
 let isRotating = $state(false);
 let showRotationAdminSecret = $state(false);
 let showNewKey = $state(false);

 async function handleRotateKey() {
 if (!rotationAdminSecret.trim()) {
 addToast('Admin secret is required', 'alert');
 return;
 }
 if (!rotationNewKey.trim()) {
 addToast('New encryption key is required', 'alert');
 return;
 }

 isRotating = true;
 try {
 await axios.post('/api/v1/rotate', {
 admin_secret: rotationAdminSecret.trim(),
 new_key: rotationNewKey.trim()
 });
 addToast('Key rotation completed successfully', 'success');
 rotationAdminSecret = '';
 rotationNewKey = '';
 } catch (error: any) {
 const msg = error.response?.data?.message || 'Key rotation failed';
 addToast(msg, 'alert');
 } finally {
 isRotating = false;
 }
 }
</script>

<div>
 <div class="mb-4">
 <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">Key Rotation</h2>
 <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">Rotate the encryption key used to protect stored configurations</p>
 </div>

 <div class="p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
 <div class="mb-5 p-3 bg-amber-50 border border-amber-200 rounded-md">
 <p class="text-xs text-amber-800 leading-relaxed m-0">
 Key rotation will re-encrypt all stored data with the new key. This operation may take time depending on the amount of data.
 After rotation, update the <code class="px-1 py-0.5 bg-amber-100 rounded text-[11px]">PIKA_SECRET_ENCRYPTION_KEY</code> environment variable to the new key.
 </p>
 </div>

 <div class="mb-4">
 <label for="rotation-admin-secret" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Admin Secret</label>
 <div class="relative">
 <input
 id="rotation-admin-secret"
 type={showRotationAdminSecret ? 'text' : 'password'}
 bind:value={rotationAdminSecret}
 placeholder="Enter your admin secret"
 class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <button
 type="button"
 class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 dark:text-slate-500 bg-transparent border-none cursor-pointer hover:text-slate-600 dark:text-slate-300 transition-colors"
 onclick={() => showRotationAdminSecret = !showRotationAdminSecret}
 title={showRotationAdminSecret ? 'Hide' : 'Show'}
 >
 {#if showRotationAdminSecret}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
 </button>
 </div>
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">The admin secret configured in the Security tab</p>
 </div>

 <div class="mb-4">
 <label for="rotation-new-key" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">New Encryption Key</label>
 <div class="relative">
 <input
 id="rotation-new-key"
 type={showNewKey ? 'text' : 'password'}
 bind:value={rotationNewKey}
 placeholder="Enter new encryption key"
 class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <button
 type="button"
 class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 dark:text-slate-500 bg-transparent border-none cursor-pointer hover:text-slate-600 dark:text-slate-300 transition-colors"
 onclick={() => showNewKey = !showNewKey}
 title={showNewKey ? 'Hide' : 'Show'}
 >
 {#if showNewKey}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
 </button>
 </div>
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Any string — will be hashed (SHA-256) to derive the encryption key. After rotation, update the <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded text-[11px]">PIKA_SECRET_ENCRYPTION_KEY</code> environment variable.</p>
 </div>

  <button
  class="flex items-center justify-center gap-2 w-full px-4 py-2.5 text-sm font-medium text-white rounded-md cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed
  {isRotating ? 'bg-amber-500' : 'bg-vermilion-500 hover:bg-vermilion-600'}"
  onclick={handleRotateKey}
  disabled={isRotating}
  >
 <RotateCw size={14} class={isRotating ? 'animate-spin' : ''} />
 {isRotating ? 'Rotating...' : 'Rotate Encryption Key'}
 </button>
 </div>
</div>
