<script lang="ts">
 import { configStore } from "@/lib/store/config.svelte";
 import { addToast } from "@/lib/store/toast.svelte";
 import { appStore } from "@/lib/store/store.svelte";
 import { onMount } from "svelte";
 import { Plus, Trash2, Eye, EyeOff, Share2 } from "lucide-svelte";
 import type { FTPShareEntry, FTPUserEntry } from "@/lib/types/config";

 // ── FTP Shares state ──
 let ftpShares = $state<FTPShareEntry[]>([]);
 let showAddShare = $state(false);
 let newShareName = $state('');
 let newSharePaths = $state<string[]>([]);
 let newSharePathInput = $state('');
 let newShareReadOnly = $state(false);
 let newShareRoot = $state(false);
 let editingShareIndex = $state<number | null>(null);
 let isSavingShares = $state(false);

 // FTP/SFTP Users state
 let ftpUsers = $state<FTPUserEntry[]>([]);
 let showAddUser = $state(false);
 let newUserUsername = $state('');
 let newUserPassword = $state('');
 let newUserShares = $state<string[]>([]);
 let newUserShareInput = $state('');
 let newUserReadOnly = $state(false);
 let newUserAuthorizedKeys = $state('');
 let editingUserIndex = $state<number | null>(null);
 let isSavingUsers = $state(false);
 let showUserPassword = $state(false);

 // SFTP port for connection instructions display
 let sftpServePort = $derived(configStore.settings?.sftp_serve?.port || 2222);

 // Available mounts from app store
 const availableMounts = $derived(appStore.info?.raw_mounts ?? []);

 // Initialize from configStore on mount
 onMount(() => {
 ftpShares = [...(configStore.settings?.ftp_shares || [])];
 ftpUsers = [...(configStore.settings?.ftp_users || [])];
 });

 // ── FTP share handlers ──
 function resetShareForm() {
 newShareName = '';
 newSharePaths = [];
 newSharePathInput = '';
 newShareReadOnly = false;
 newShareRoot = false;
 editingShareIndex = null;
 }

 function handleEditShare(index: number) {
 const share = ftpShares[index];
 newShareName = share.name;
 newSharePaths = [...share.paths];
 newSharePathInput = '';
 newShareReadOnly = share.read_only;
 newShareRoot = share.root ?? false;
 editingShareIndex = index;
 showAddShare = true;
 }

 function addSharePath() {
 const p = newSharePathInput.trim().replace(/^\/+/, '');
 if (!p) return;
 if (newSharePaths.includes(p)) {
 addToast('Path already added', 'alert');
 return;
 }
 newSharePaths = [...newSharePaths, p];
 newSharePathInput = '';
 }

 function removeSharePath(index: number) {
 newSharePaths = newSharePaths.filter((_, i) => i !== index);
 }

 async function handleAddShare() {
 const name = newShareName.trim();
 if (!name) {
 addToast('Share name is required', 'alert');
 return;
 }
 if (newSharePaths.length === 0) {
 addToast('At least one path is required', 'alert');
 return;
 }
 if (ftpShares.some((s, i) => s.name === name && i !== editingShareIndex)) {
 addToast(`A share named "${name}" already exists`, 'alert');
 return;
 }
 // Only one share can be root
 if (newShareRoot && ftpShares.some((s, i) => s.root && i !== editingShareIndex)) {
 addToast('Another share is already mounted at root. Only one root share is allowed.', 'alert');
 return;
 }

 const entry: FTPShareEntry = {
 name,
 paths: [...newSharePaths],
 read_only: newShareReadOnly,
 root: newShareRoot || undefined,
 };

 let updated: FTPShareEntry[];
 if (editingShareIndex !== null) {
 updated = ftpShares.map((s, i) => i === editingShareIndex ? entry : s);
 } else {
 updated = [...ftpShares, entry];
 }

 isSavingShares = true;
 try {
 await configStore.saveFTPShares(updated);
 ftpShares = updated;
 showAddShare = false;
 resetShareForm();
 } catch {
 // toast already shown
 } finally {
 isSavingShares = false;
 }
 }

 async function handleRemoveShare(index: number) {
 const share = ftpShares[index];
 if (!confirm(`Remove share "${share.name}"?`)) return;

 const updated = ftpShares.filter((_, i) => i !== index);
 isSavingShares = true;
 try {
 await configStore.saveFTPShares(updated);
 ftpShares = updated;
 } catch {
 // toast already shown
 } finally {
 isSavingShares = false;
 }
 }

 // ── FTP/SFTP user handlers ──
 function resetUserForm() {
 newUserUsername = '';
 newUserPassword = '';
 newUserShares = [];
 newUserShareInput = '';
 newUserReadOnly = false;
 newUserAuthorizedKeys = '';
 editingUserIndex = null;
 showUserPassword = false;
 }

 function handleEditUser(index: number) {
 const user = ftpUsers[index];
 newUserUsername = user.username;
 newUserPassword = user.password || '';
 newUserShares = [...(user.shares || [])];
 newUserShareInput = '';
 newUserReadOnly = user.read_only;
 newUserAuthorizedKeys = user.authorized_keys || '';
 editingUserIndex = index;
 showAddUser = true;
 }

 function addUserShare() {
 const s = newUserShareInput.trim();
 if (!s) return;
 if (newUserShares.includes(s)) {
 addToast('Share already added', 'alert');
 return;
 }
 newUserShares = [...newUserShares, s];
 newUserShareInput = '';
 }

 function removeUserShare(index: number) {
 newUserShares = newUserShares.filter((_, i) => i !== index);
 }

 async function handleAddUser() {
 const username = newUserUsername.trim();
 if (!username) {
 addToast('Username is required', 'alert');
 return;
 }
 const hasKeys = newUserAuthorizedKeys.trim().length > 0;
 if (!newUserPassword && !hasKeys) {
 addToast('Password or authorized keys required', 'alert');
 return;
 }
 if (ftpUsers.some((u, i) => u.username === username && i !== editingUserIndex)) {
 addToast(`User "${username}" already exists`, 'alert');
 return;
 }

 const entry: FTPUserEntry = {
 username,
 password: newUserPassword || undefined,
 shares: newUserShares.length > 0 ? [...newUserShares] : undefined,
 authorized_keys: hasKeys ? newUserAuthorizedKeys.trim() : undefined,
 read_only: newUserReadOnly,
 };

 let updated: FTPUserEntry[];
 if (editingUserIndex !== null) {
 updated = ftpUsers.map((u, i) => i === editingUserIndex ? entry : u);
 } else {
 updated = [...ftpUsers, entry];
 }

 isSavingUsers = true;
 try {
 await configStore.saveFTPUsers(updated);
 ftpUsers = updated;
 showAddUser = false;
 resetUserForm();
 } catch {
 // toast already shown
 } finally {
 isSavingUsers = false;
 }
 }

 async function handleRemoveUser(index: number) {
 const user = ftpUsers[index];
 if (!confirm(`Remove user "${user.username}"?`)) return;

 const updated = ftpUsers.filter((_, i) => i !== index);
 isSavingUsers = true;
 try {
 await configStore.saveFTPUsers(updated);
 ftpUsers = updated;
 } catch {
 // toast already shown
 } finally {
 isSavingUsers = false;
 }
 }

 async function generateKeypair() {
 try {
 const keyPair = await crypto.subtle.generateKey('Ed25519' as any, true, ['sign', 'verify']);

 // Export keys from Web Crypto
 const privDer = new Uint8Array(await crypto.subtle.exportKey('pkcs8', keyPair.privateKey));
 const rawPub = new Uint8Array(await crypto.subtle.exportKey('raw', keyPair.publicKey));

 // Extract 32-byte seed from PKCS#8 DER (Ed25519 PKCS8 is always 48 bytes, seed at offset 16)
 const seed = privDer.slice(16, 48);

 // Build OpenSSH private key format (unencrypted)
 const enc = new TextEncoder();
 const kt = enc.encode('ssh-ed25519');
 const comment = enc.encode('generated-by-pika');

 // Helper: uint32 big-endian
 const u32 = (n: number) => { const b = new Uint8Array(4); new DataView(b.buffer).setUint32(0, n); return b; };
 // Helper: length-prefixed string/bytes
 const sshStr = (d: Uint8Array) => { const r = new Uint8Array(4 + d.length); r.set(u32(d.length)); r.set(d, 4); return r; };
 // Helper: concat multiple Uint8Arrays
 const concat = (...arrs: Uint8Array[]) => { const r = new Uint8Array(arrs.reduce((s, a) => s + a.length, 0)); let o = 0; for (const a of arrs) { r.set(a, o); o += a.length; } return r; };

 // Public key blob (SSH wire format)
 const pubBlob = concat(sshStr(kt), sshStr(rawPub));

 // Private key blob (64 bytes = seed || pubkey for Ed25519)
 const privKeyData = concat(seed, rawPub);

 // Random check integers (must match)
 const checkBytes = crypto.getRandomValues(new Uint8Array(4));

 // Assemble private section (before padding)
 let privSection = concat(checkBytes, checkBytes, sshStr(kt), sshStr(rawPub), sshStr(privKeyData), sshStr(comment));

 // Pad to block size (8 for cipher "none")
 const padLen = 8 - (privSection.length % 8);
 if (padLen < 8) {
 const padding = new Uint8Array(padLen);
 for (let i = 0; i < padLen; i++) padding[i] = i + 1;
 privSection = concat(privSection, padding);
 }

 // Assemble full key
 const magic = enc.encode('openssh-key-v1\0');
 const none = enc.encode('none');
 const fullKey = concat(
 magic,
 sshStr(none), // cipher
 sshStr(none), // kdf
 sshStr(new Uint8Array(0)), // kdf options
 u32(1), // number of keys
 sshStr(pubBlob), // public key
 sshStr(privSection), // private section
 );

 // Encode as PEM
 const b64 = btoa(String.fromCharCode(...fullKey));
 const privPem = '-----BEGIN OPENSSH PRIVATE KEY-----\n' +
 b64.match(/.{1,70}/g)!.join('\n') +
 '\n-----END OPENSSH PRIVATE KEY-----\n';

 // Build OpenSSH public key line
 const pubLine = 'ssh-ed25519 ' + btoa(String.fromCharCode(...pubBlob)) + ' generated-by-pika';

 // Append public key to authorized_keys
 const existing = newUserAuthorizedKeys.trim();
 newUserAuthorizedKeys = existing ? existing + '\n' + pubLine : pubLine;

 // Download private key file
 const filename = (newUserUsername.trim() || 'pika') + '_id_ed25519';
 const blob = new Blob([privPem], { type: 'application/x-pem-file' });
 const url = URL.createObjectURL(blob);
 const a = document.createElement('a');
 a.href = url;
 a.download = filename;
 a.click();
 URL.revokeObjectURL(url);

 addToast('Keypair generated. Private key downloaded — keep it safe.', 'success');
 } catch (err: any) {
 addToast('Keypair generation failed: ' + (err?.message || 'Ed25519 may not be supported in this browser'), 'alert');
 }
 }
</script>

<div>
 <div class="flex items-center justify-between mb-4">
 <div>
 <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">Shares</h2>
 <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">Share folders from your raw mounts via the built-in file servers (FTP, SFTP, TFTP, WebDAV). External clients can connect and browse/download files.</p>
 </div>
 <button
 class="flex items-center gap-1.5 px-3 py-2 bg-accent-600 text-white text-sm font-medium rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
 onclick={() => { showAddShare = true; resetShareForm(); }}
 >
 <Plus size={14} />
 Add Share
 </button>
 </div>

 {#if availableMounts.length === 0}
 <div class="mb-4 p-3 bg-amber-50 border border-amber-200 rounded-md">
 <p class="text-xs text-amber-800">No raw mounts configured. Add a raw mount first before creating shares.</p>
 </div>
 {/if}

 <!-- Add/Edit Share Form -->
 {#if showAddShare}
 <div class="mb-6 p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
 <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-4">{editingShareIndex !== null ? 'Edit Share' : 'Add Share'}</h3>

 <div class="mb-4">
 <label for="share-name" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Share Name</label>
 <input id="share-name" type="text" bind:value={newShareName} placeholder="e.g., project-files"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">This becomes the top-level folder name visible to connecting clients</p>
 </div>

 <div class="mb-4">
 <label for="share-path-input" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Paths</label>
 <div class="flex gap-2">
 <input id="share-path-input" type="text" bind:value={newSharePathInput}
 placeholder="mount/folder (e.g., configs/app)"
 onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addSharePath(); } }}
 class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <button
 class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
 onclick={addSharePath}
 >
 Add
 </button>
 </div>
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">
 Format: <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded text-[10px]">mount_prefix</code> or
 <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded text-[10px]">mount_prefix/sub/folder</code>.
 Multiple paths are merged into a single share.
 {#if availableMounts.length > 0}
 Available mounts: {availableMounts.map(m => m.prefix).join(', ')}
 {/if}
 </p>

 {#if newSharePaths.length > 0}
 <div class="mt-2 flex flex-wrap gap-1.5">
 {#each newSharePaths as p, i}
 <span class="inline-flex items-center gap-1 px-2 py-1 bg-accent-50 border border-accent-200 rounded text-xs font-mono text-brand-700">
 {p}
 <button
 class="flex items-center justify-center w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-brand-400 hover:text-red-500 transition-colors"
 onclick={() => removeSharePath(i)}
 title="Remove"
 >&times;</button>
 </span>
 {/each}
 </div>
 {/if}
 </div>

 <div class="mb-4">
 <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="checkbox" bind:checked={newShareReadOnly} class="rounded border-slate-300" />
 Read-only (clients cannot upload or delete)
 </label>
 </div>

 <div class="mb-4">
 <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="checkbox" bind:checked={newShareRoot} class="rounded border-slate-300" />
 Mount at root
 </label>
 <p class="mt-1 ml-5 text-[11px] text-slate-400 dark:text-slate-500">Serve this share's contents directly at <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded text-[10px]">/</code> instead of <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded text-[10px]">/{'{name}'}/</code>. Only one share can be root. Other shares will be hidden while a root share is active.</p>
 </div>

 <div class="flex justify-end gap-2">
 <button
 class="px-3 py-2 text-sm text-slate-600 dark:text-slate-300 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => { showAddShare = false; resetShareForm(); }}
 >
 Cancel
 </button>
 <button
 class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
 onclick={handleAddShare}
 disabled={isSavingShares}
 >
 {isSavingShares ? 'Saving...' : editingShareIndex !== null ? 'Save Changes' : 'Add Share'}
 </button>
 </div>
 </div>
 {/if}

 <!-- Share List -->
 {#if ftpShares.length === 0 && !showAddShare}
 <div class="text-center py-12 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg">
 <Share2 size={32} class="mx-auto text-slate-300 mb-3" />
 <p class="text-sm text-slate-500 dark:text-slate-400">No shares configured</p>
 <p class="text-xs text-slate-400 dark:text-slate-500 mt-1">Add a share to expose folders via the built-in file servers</p>
 </div>
 {:else}
 <div class="space-y-2">
 {#each ftpShares as share, i (share.name)}
 <div class="flex items-center gap-4 p-4 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg hover:border-slate-300 transition-colors">
 <div class="flex-1 min-w-0">
 <div class="flex items-center gap-2">
 <span class="text-sm font-medium text-slate-800 dark:text-slate-100">{share.name}</span>
 {#if share.root}
 <span class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-accent-100 text-brand-700">
 Root
 </span>
 {/if}
 {#if share.read_only}
 <span class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-amber-100 text-amber-700">
 Read-only
 </span>
 {:else}
 <span class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-emerald-100 text-emerald-700">
 Read+Write
 </span>
 {/if}
 <span class="text-[10px] text-slate-400 dark:text-slate-500">{share.root ? '→ /' : `→ /${share.name}/`}</span>
 </div>
 <div class="mt-1 flex flex-wrap gap-1">
 {#each share.paths as p}
 <span class="px-1.5 py-0.5 text-[10px] font-mono rounded bg-slate-100 dark:bg-warm-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-warm-700">{p}</span>
 {/each}
 </div>
 </div>
 <div class="flex items-center gap-1 shrink-0">
 <button
 class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-accent-600 hover:bg-accent-50 dark:hover:bg-accent-900/30 rounded transition-colors cursor-pointer"
 onclick={() => handleEditShare(i)}
 title="Edit share"
 >
 <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/><path d="m15 5 4 4"/></svg>
 </button>
 <button
 class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-red-500 hover:bg-red-50 rounded transition-colors cursor-pointer"
 onclick={() => handleRemoveShare(i)}
 title="Remove share"
 >
 <Trash2 size={14} />
 </button>
 </div>
 </div>
 {/each}
 </div>
 {/if}

 <!-- Users Section -->
 <div class="mt-8">
 <div class="flex items-center justify-between mb-4">
 <div>
 <h3 class="text-base font-semibold text-slate-800 dark:text-slate-100">Users</h3>
 <p class="text-xs text-slate-500 dark:text-slate-400 mt-0.5">Manage user accounts for the built-in file servers (FTP, SFTP, WebDAV). The same users apply to all servers.</p>
 </div>
 <button
 class="flex items-center gap-1.5 px-3 py-2 bg-accent-600 text-white text-sm font-medium rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
 onclick={() => { showAddUser = true; resetUserForm(); }}
 >
 <Plus size={14} />
 Add User
 </button>
 </div>

 {#if showAddUser}
 <div class="mb-6 p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
 <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-4">{editingUserIndex !== null ? 'Edit User' : 'Add User'}</h3>

 <div class="grid grid-cols-2 gap-3 mb-4">
 <div>
 <label for="ftp-user-name" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Username</label>
 <input id="ftp-user-name" type="text" bind:value={newUserUsername} placeholder="admin"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="ftp-user-pass" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Password</label>
 <div class="relative">
 <input id="ftp-user-pass" type={showUserPassword ? 'text' : 'password'} bind:value={newUserPassword} placeholder="Password"
 class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <button
 type="button"
 class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 dark:text-slate-500 bg-transparent border-none cursor-pointer hover:text-slate-600 dark:text-slate-300 transition-colors"
 onclick={() => showUserPassword = !showUserPassword}
 title={showUserPassword ? 'Hide' : 'Show'}
 >
 {#if showUserPassword}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
 </button>
 </div>
 </div>
 </div>

 <div class="mb-4">
 <div class="flex items-center justify-between mb-1.5">
 <label for="ftp-user-authorized-keys" class="block text-xs font-medium text-slate-500 dark:text-slate-400">Authorized Keys (optional)</label>
 <button
 type="button"
 class="flex items-center gap-1 px-2 py-1 text-[11px] font-medium text-slate-600 dark:text-slate-300 bg-slate-100 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded hover:bg-slate-200 dark:hover:bg-warm-700 transition-colors cursor-pointer"
 onclick={generateKeypair}
 title="Generate an Ed25519 keypair, auto-fill the public key, and download the private key"
 >
 <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4"/></svg>
 Generate Keypair
 </button>
 </div>
 <textarea id="ftp-user-authorized-keys" bind:value={newUserAuthorizedKeys}
 placeholder="ssh-ed25519 AAAA... user@host"
 rows="3"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 resize-y"></textarea>
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">
 SSH public keys for SFTP key-based authentication (OpenSSH <code class="px-0.5 bg-slate-100 dark:bg-warm-900 rounded">authorized_keys</code> format, one key per line).
 When set, password becomes optional.
 </p>
 {#if newUserAuthorizedKeys.trim()}
 <div class="mt-2 p-2.5 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded text-[11px] text-slate-500 dark:text-slate-400 space-y-1.5">
 <p class="font-medium text-slate-600 dark:text-slate-300">Connection instructions</p>
 <p>
 <span class="font-medium">Command line:</span>
 <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded font-mono text-[10px]">sftp -P {sftpServePort || 2222} -i /path/to/{newUserUsername.trim() || 'user'}_id_ed25519 {newUserUsername.trim() || 'user'}@your-host</code>
 </p>
 <p>
 <span class="font-medium">FileZilla:</span>
 Edit &rarr; Settings &rarr; SFTP &rarr; Add key file. Then connect with Host / Port / Username.
 </p>
 <p>
 <span class="font-medium">WinSCP:</span>
 Session &rarr; Advanced &rarr; SSH &rarr; Authentication &rarr; Private key file.
 </p>
 <p class="text-slate-400 dark:text-slate-500">Keep the private key file secure. Never share it.</p>
 </div>
 {/if}
 </div>

 <div class="mb-4">
 <label for="ftp-user-shares" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Allowed Shares (optional)</label>
 <div class="flex gap-2">
 <input id="ftp-user-shares" type="text" bind:value={newUserShareInput}
 placeholder="Share name"
 onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addUserShare(); } }}
 class="flex-1 px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <button
 class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
 onclick={addUserShare}
 >
 Add
 </button>
 </div>
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Leave empty to allow access to all shares. Otherwise, type share names to restrict access.</p>

 {#if newUserShares.length > 0}
 <div class="mt-2 flex flex-wrap gap-1.5">
 {#each newUserShares as s, i}
 <span class="inline-flex items-center gap-1 px-2 py-1 bg-accent-50 border border-accent-200 rounded text-xs text-brand-700">
 {s}
 <button class="w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-brand-400 hover:text-red-500"
 onclick={() => removeUserShare(i)}>&times;</button>
 </span>
 {/each}
 </div>
 {/if}
 </div>

 <div class="mb-4">
 <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="checkbox" bind:checked={newUserReadOnly} class="rounded border-slate-300" />
 Read-only (user cannot upload or delete, regardless of share settings)
 </label>
 </div>

 <div class="flex justify-end gap-2">
 <button class="px-3 py-2 text-sm text-slate-600 dark:text-slate-300 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => { showAddUser = false; resetUserForm(); }}>Cancel</button>
 <button class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
 onclick={handleAddUser} disabled={isSavingUsers}>
 {isSavingUsers ? 'Saving...' : editingUserIndex !== null ? 'Save Changes' : 'Add User'}
 </button>
 </div>
 </div>
 {/if}

 {#if ftpUsers.length === 0 && !showAddUser}
 <div class="text-center py-8 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg">
 <p class="text-sm text-slate-500 dark:text-slate-400">No users configured</p>
 <p class="text-xs text-slate-400 dark:text-slate-500 mt-1">Add a user to enable file-server access. Users can also be set in the config file.</p>
 </div>
 {:else}
 <div class="space-y-2">
 {#each ftpUsers as user, i (user.username)}
 <div class="flex items-center gap-4 p-4 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg hover:border-slate-300 transition-colors">
 <div class="flex-1 min-w-0">
 <div class="flex items-center gap-2">
 <span class="text-sm font-medium text-slate-800 dark:text-slate-100">{user.username}</span>
 {#if user.read_only}
 <span class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-amber-100 text-amber-700">Read-only</span>
 {:else}
 <span class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-emerald-100 text-emerald-700">Read+Write</span>
 {/if}
 {#if user.authorized_keys}
 <span class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-violet-100 text-violet-700">SSH Key</span>
 {/if}
 </div>
 <div class="mt-1">
 {#if user.shares && user.shares.length > 0}
 <span class="text-[11px] text-slate-400 dark:text-slate-500 mr-1">Shares:</span>
 {#each user.shares as s}
 <span class="px-1 py-0.5 text-[10px] rounded bg-slate-100 dark:bg-warm-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-warm-700 mr-1">{s}</span>
 {/each}
 {:else}
 <span class="text-[11px] text-slate-400 dark:text-slate-500">All shares</span>
 {/if}
 </div>
 </div>
 <div class="flex items-center gap-1 shrink-0">
 <button class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-accent-600 hover:bg-accent-50 dark:hover:bg-accent-900/30 rounded transition-colors cursor-pointer"
 onclick={() => handleEditUser(i)} title="Edit user">
 <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/><path d="m15 5 4 4"/></svg>
 </button>
 <button class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-red-500 hover:bg-red-50 rounded transition-colors cursor-pointer"
 onclick={() => handleRemoveUser(i)} title="Remove user">
 <Trash2 size={14} />
 </button>
 </div>
 </div>
 {/each}
 </div>
 {/if}
 </div>


</div>
