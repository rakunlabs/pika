<script lang="ts">
 import { configStore } from "@/lib/store/config.svelte";
 import { addToast } from "@/lib/store/toast.svelte";
 import { Eye, EyeOff, Lock, Download, Upload, Trash2, ShieldAlert, RefreshCw } from "lucide-svelte";
 import axios from 'axios';

 // ── Backup mode selection ──
 // The bw-backed backup format ships in three flavors:
 // • full — snapshot of the entire current DB (default).
 // • since=X — incremental: entries newer than version X.
 // • until=X — point-in-time: entries up to version X (inclusive).
 // Since and until are mutually exclusive at the API; the UI radio
 // group enforces that.
 type BackupMode = 'full' | 'since' | 'until';

 // ── Backup state ──
 let backupAdminSecret = $state('');
 let showBackupAdminSecret = $state(false);
 let isExporting = $state(false);
 let isImporting = $state(false);
 let importFile = $state<File | null>(null);
 let importFileName = $state('');
 let encryptionPassword = $state('');
 let showEncryptionPassword = $state(false);
 let importEncryptionPassword = $state('');
 let showImportEncryptionPassword = $state(false);
 let importFileIsEncrypted = $state(false);
 let importFileDBVersion = $state<number | null>(null);
 let showUnencryptedWarning = $state(false);

 // Wipe-and-restore mode. When true, the server runs DropAll BEFORE
 // applying the backup — keys present in the running DB but absent
 // from the backup are removed. The backup is validated (magic +
 // decryption test) before any wipe runs, so a bad file doesn't
 // destroy production data. We also gate this behind a confirmation
 // modal here for a second human checkpoint.
 let wipeBeforeRestore = $state(false);
 let showWipeConfirm = $state(false);

 // Versioning controls
 let backupMode = $state<BackupMode>('full');
 let sinceVersion = $state('');
 let untilVersion = $state('');

 // Current server-side DB version. Loaded after the user enters the
 // admin secret + clicks Refresh. We don't auto-fetch at mount because
 // the endpoint requires the secret — and the secret is also gating
 // this whole panel.
 let currentDBVersion = $state<number | null>(null);
 let isLoadingVersion = $state(false);

 // ── pikabw header layout (mirrored from internal/service/backup.go) ──
 // Bytes:
 // 0..7 magic "PIKABW\x00\x01"
 // 8 flags bit 0 = encrypted
 // 9..16 payload size (BE u64) — read but not surfaced in the UI
 // 17..24 db_version (BE u64)
 // We only parse the flag byte and db_version client-side; the rest is
 // server territory. If the magic doesn't match we treat the file as
 // not-a-pika-backup and disable the import button.
 const PIKABW_MAGIC = new Uint8Array([0x50, 0x49, 0x4B, 0x41, 0x42, 0x57, 0x00, 0x01]);
 const HEADER_SIZE = 25;

 async function refreshVersion() {
 if (!backupAdminSecret.trim()) {
 addToast('Enter the admin secret first', 'alert');
 return;
 }
 isLoadingVersion = true;
 try {
 const { data } = await axios.get('/api/v1/backup/info', {
 params: { admin_secret: backupAdminSecret.trim() }
 });
 currentDBVersion = data.db_version ?? 0;
 } catch (error: any) {
 const msg = error.response?.data?.message || error.response?.statusText || 'Failed to read version';
 addToast(msg, 'alert');
 } finally {
 isLoadingVersion = false;
 }
 }

 // ── Backup handlers ──
 function handleExportBackup() {
 if (!backupAdminSecret.trim()) {
 addToast('Admin secret is required', 'alert');
 return;
 }

 if (backupMode === 'since' && !sinceVersion.trim()) {
 addToast('Provide a "since" version', 'alert');
 return;
 }
 if (backupMode === 'until' && !untilVersion.trim()) {
 addToast('Provide an "until" version', 'alert');
 return;
 }

 if (!encryptionPassword.trim()) {
 showUnencryptedWarning = true;
 return;
 }

 doExportBackup();
 }

 function confirmUnencryptedExport() {
 showUnencryptedWarning = false;
 doExportBackup();
 }

 function cancelUnencryptedExport() {
 showUnencryptedWarning = false;
 }

 async function doExportBackup() {
 isExporting = true;
 try {
 const params: Record<string, string> = { admin_secret: backupAdminSecret.trim() };
 if (encryptionPassword.trim()) {
 params.encryption_password = encryptionPassword.trim();
 }
 if (backupMode === 'since' && sinceVersion.trim()) {
 params.since = sinceVersion.trim();
 } else if (backupMode === 'until' && untilVersion.trim()) {
 params.until = untilVersion.trim();
 }

 const response = await axios.get('/api/v1/backup', {
 params,
 responseType: 'blob'
 });

 // Trust the server's filename (Content-Disposition) when present;
 // fall back to a sensible default. The server already encodes the
 // captured DB version into the filename.
 let filename = '';
 const cd = response.headers['content-disposition'] || '';
 const m = cd.match(/filename="([^"]+)"/);
 if (m) filename = m[1];
 if (!filename) {
 const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
 filename = `pika-backup-${ts}.pikabw`;
 }

 const blob = new Blob([response.data], { type: 'application/octet-stream' });
 const url = URL.createObjectURL(blob);
 const a = document.createElement('a');
 a.href = url;
 a.download = filename;
 document.body.appendChild(a);
 a.click();
 document.body.removeChild(a);
 URL.revokeObjectURL(url);

 const summary = encryptionPassword.trim() ? 'Encrypted backup downloaded' : 'Backup downloaded';
 addToast(summary, 'success');

 // Refresh the displayed DB version — exporting doesn't change it,
 // but if the user just woke the panel up this is a free win.
 refreshVersion();
 } catch (error: any) {
 const msg = error.response?.data?.message || error.response?.statusText || 'Export failed';
 if (error.response?.data instanceof Blob) {
 try {
 const text = await error.response.data.text();
 const parsed = JSON.parse(text);
 addToast(parsed.message || 'Export failed', 'alert');
 return;
 } catch {}
 }
 addToast(msg, 'alert');
 } finally {
 isExporting = false;
 }
 }

 async function handleFileSelect(event: Event) {
 const input = event.target as HTMLInputElement;
 if (input.files && input.files.length > 0) {
 importFile = input.files[0];
 importFileName = input.files[0].name;

 // Parse the .pikabw header so we can disable the button cleanly
 // when the user picked a non-pika file, and surface the encrypted
 // flag + embedded db_version.
 try {
 const headerBuf = await input.files[0].slice(0, HEADER_SIZE).arrayBuffer();
 const hdr = new Uint8Array(headerBuf);
 if (hdr.length < HEADER_SIZE) {
 throw new Error('header too short');
 }
 for (let i = 0; i < PIKABW_MAGIC.length; i++) {
 if (hdr[i] !== PIKABW_MAGIC[i]) {
 throw new Error('not a pika backup file');
 }
 }
 importFileIsEncrypted = (hdr[8] & 0x01) === 0x01;
 const dv = new DataView(headerBuf, 17, 8);
 // Browsers older than 2024 may lack getBigUint64; guard it.
 let v: number;
 if (typeof dv.getBigUint64 === 'function') {
 v = Number(dv.getBigUint64(0, false));
 } else {
 const hi = dv.getUint32(0, false);
 const lo = dv.getUint32(4, false);
 v = hi * 0x100000000 + lo;
 }
 importFileDBVersion = v;
 } catch (err: any) {
 importFileIsEncrypted = false;
 importFileDBVersion = null;
 addToast(`Selected file is not a valid pika backup: ${err.message}`, 'alert');
 }
 }
 }

 function handleImportBackup() {
 if (!backupAdminSecret.trim()) {
 addToast('Admin secret is required', 'alert');
 return;
 }
 if (!importFile) {
 addToast('Please select a .pikabw backup file', 'alert');
 return;
 }
 if (importFileIsEncrypted && !importEncryptionPassword.trim()) {
 addToast('Encryption password is required for encrypted backups', 'alert');
 return;
 }

 // Wipe path is destructive — open a dedicated confirmation modal so
 // the user has to explicitly acknowledge the consequences. The
 // upsert path uses a softer browser confirm.
 if (wipeBeforeRestore) {
 showWipeConfirm = true;
 return;
 }

 if (!confirm('Restore will overwrite any keys present in the backup. Existing keys not in the backup will be left in place. Continue?')) {
 return;
 }
 doImportBackup();
 }

 function confirmWipeRestore() {
 showWipeConfirm = false;
 doImportBackup();
 }

 function cancelWipeRestore() {
 showWipeConfirm = false;
 }

 async function doImportBackup() {
 if (!importFile) return;

 isImporting = true;
 try {
 // Stream the file straight to the server. Auth + password ride on
 // headers so the body stays a clean octet-stream the API can pass
 // to Service.Restore without buffering.
 const headers: Record<string, string> = {
 'Content-Type': 'application/octet-stream',
 'X-Admin-Secret': backupAdminSecret.trim(),
 };
 if (importEncryptionPassword.trim()) {
 headers['X-Encryption-Password'] = importEncryptionPassword.trim();
 }
 if (wipeBeforeRestore) {
 headers['X-Pika-Wipe'] = 'true';
 }

 // axios needs the raw bytes here; using the File object directly
 // would set multipart form-data on most browsers.
 const arrayBuffer = await importFile.arrayBuffer();
 await axios.post('/api/v1/backup', arrayBuffer, { headers });

 addToast(wipeBeforeRestore ? 'Database wiped and backup restored' : 'Backup restored successfully', 'success');
 importFile = null;
 importFileName = '';
 importFileIsEncrypted = false;
 importFileDBVersion = null;
 importEncryptionPassword = '';
 wipeBeforeRestore = false;

 // Refresh settings, tree, and the displayed DB version.
 configStore.loadSettings();
 refreshVersion();
 } catch (error: any) {
 const msg = error.response?.data?.message || 'Restore failed';
 addToast(msg, 'alert');
 } finally {
 isImporting = false;
 }
 }
</script>

<div>
 <div class="mb-4">
 <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">Backup & Restore</h2>
 <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
 Export the entire database as a <code class="text-[11px] bg-slate-100 dark:bg-warm-900 px-1 py-0.5 rounded">.pikabw</code> stream, or restore from a previous one.
 Backups capture every key (configs, users, tokens, settings).
 </p>
 </div>

 <!-- Admin Secret + DB version (shared for both operations) -->
 <div class="mb-6 p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
 <div class="mb-4">
 <label for="backup-admin-secret" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Admin Secret</label>
 <div class="relative">
 <input
 id="backup-admin-secret"
 type={showBackupAdminSecret ? 'text' : 'password'}
 bind:value={backupAdminSecret}
 placeholder="Enter your admin secret"
 class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <button
 type="button"
 class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 dark:text-slate-500 bg-transparent border-none cursor-pointer hover:text-slate-600 dark:text-slate-300 transition-colors"
 onclick={() => showBackupAdminSecret = !showBackupAdminSecret}
 title={showBackupAdminSecret ? 'Hide' : 'Show'}
 >
 {#if showBackupAdminSecret}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
 </button>
 </div>
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Required for all backup operations</p>
 </div>

 <div class="flex items-center justify-between gap-3 px-3 py-2 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md">
 <div class="text-xs text-slate-500 dark:text-slate-400">
 <span class="font-medium text-slate-700 dark:text-slate-200">Current DB version:</span>
 {#if currentDBVersion === null}
 <span class="text-slate-400 dark:text-slate-500">unknown</span>
 {:else}
 <span class="font-mono text-slate-700 dark:text-slate-200">{currentDBVersion}</span>
 {/if}
 </div>
 <button
 type="button"
 class="flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium text-slate-600 dark:text-slate-300 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded hover:bg-slate-50 dark:bg-warm-900 transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
 onclick={refreshVersion}
 disabled={isLoadingVersion || !backupAdminSecret.trim()}
 title="Read current DB version from the server"
 >
 <RefreshCw size={12} class={isLoadingVersion ? 'animate-spin' : ''} />
 Refresh
 </button>
 </div>
 </div>

 <!-- Export Section -->
 <div class="mb-6 p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
 <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-2">Download Backup</h3>
 <p class="text-xs text-slate-500 dark:text-slate-400 mb-4">
 Pika streams a binary <code class="text-[11px] bg-slate-100 dark:bg-warm-900 px-1 py-0.5 rounded">.pikabw</code> file. Plain by default; encrypted with XChaCha20-Poly1305 when an encryption password is supplied.
 </p>

 <!-- Backup mode -->
 <div class="mb-4">
 <span class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Backup Type</span>
 <div class="space-y-2">
 <label class="flex items-start gap-2 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="radio" bind:group={backupMode} value="full" class="mt-0.5 text-accent-600" />
 <span>
 <span class="font-medium text-slate-700 dark:text-slate-200">Full</span>
 <span class="block text-[11px] text-slate-400 dark:text-slate-500">Snapshot of the entire current database.</span>
 </span>
 </label>
 <label class="flex items-start gap-2 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="radio" bind:group={backupMode} value="since" class="mt-0.5 text-accent-600" />
 <span>
 <span class="font-medium text-slate-700 dark:text-slate-200">Incremental (since)</span>
 <span class="block text-[11px] text-slate-400 dark:text-slate-500">Only entries newer than the supplied version. Useful for chained incremental backups.</span>
 </span>
 </label>
 <label class="flex items-start gap-2 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="radio" bind:group={backupMode} value="until" class="mt-0.5 text-accent-600" />
 <span>
 <span class="font-medium text-slate-700 dark:text-slate-200">Point-in-time (until)</span>
 <span class="block text-[11px] text-slate-400 dark:text-slate-500">Snapshot containing only entries with version ≤ the supplied value.</span>
 </span>
 </label>
 </div>

 {#if backupMode === 'since'}
 <div class="mt-3">
 <label for="since-version" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Since version</label>
 <input
 id="since-version"
 type="number"
 min="0"
 bind:value={sinceVersion}
 placeholder="e.g. 1234"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Entries newer than this version will be included. Use the value from a previous full backup to chain.</p>
 </div>
 {:else if backupMode === 'until'}
 <div class="mt-3">
 <label for="until-version" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Until version</label>
 <input
 id="until-version"
 type="number"
 min="0"
 bind:value={untilVersion}
 placeholder="e.g. 1234"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Restoring this archive returns the database to its state at that version.</p>
 </div>
 {/if}
 </div>

 <!-- Encryption Password for Export -->
 <div class="mb-4">
 <label for="export-encryption-password" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Encryption Password (optional)</label>
 <div class="relative">
 <input
 id="export-encryption-password"
 type={showEncryptionPassword ? 'text' : 'password'}
 bind:value={encryptionPassword}
 placeholder="Enter a password to encrypt the backup"
 class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <button
 type="button"
 class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 dark:text-slate-500 bg-transparent border-none cursor-pointer hover:text-slate-600 dark:text-slate-300 transition-colors"
 onclick={() => showEncryptionPassword = !showEncryptionPassword}
 title={showEncryptionPassword ? 'Hide' : 'Show'}
 >
 {#if showEncryptionPassword}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
 </button>
 </div>
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">If set, the backup payload is encrypted (ChaCha20-Poly1305). The same password is required at restore time.</p>
 </div>

  <button
  class="flex items-center justify-center gap-2 w-full px-4 py-2.5 text-sm font-medium text-white bg-vermilion-500 rounded-md hover:bg-vermilion-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
  onclick={handleExportBackup}
  disabled={isExporting || !backupAdminSecret.trim()}
  >
  <Download size={14} />
  {isExporting ? 'Exporting...' : encryptionPassword.trim() ? 'Download Encrypted Backup' : 'Download Backup'}
  </button>
 </div>

 <!-- Unencrypted Export Warning Modal -->
 {#if showUnencryptedWarning}
 <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
 <div class="bg-white dark:bg-warm-900 rounded-lg shadow-xl max-w-md w-full mx-4 p-6">
 <div class="flex items-start gap-3 mb-4">
 <div class="p-2 bg-amber-100 rounded-full shrink-0">
 <ShieldAlert size={20} class="text-amber-600" />
 </div>
 <div>
 <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-100 mb-1">Export without encryption?</h3>
 <p class="text-xs text-slate-500 dark:text-slate-400 leading-relaxed">
 The backup will contain every config, user, token, and setting in plain bytes. Anyone who obtains the file can read its contents and forge a working pika instance.
 </p>
 <p class="text-xs text-slate-500 dark:text-slate-400 leading-relaxed mt-2">
 To encrypt the backup, cancel and enter an encryption password above.
 </p>
 </div>
 </div>
 <div class="flex justify-end gap-2">
 <button
 class="px-4 py-2 text-sm text-slate-600 dark:text-slate-300 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={cancelUnencryptedExport}
 >
 Cancel
 </button>
 <button
 class="px-4 py-2 text-sm font-medium text-white bg-amber-500 rounded-md hover:bg-amber-600 transition-colors cursor-pointer"
 onclick={confirmUnencryptedExport}
 >
 Continue without encryption
 </button>
 </div>
 </div>
 </div>
 {/if}

 <!-- Import Section -->
 <div class="p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
 <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-2">Restore from Backup</h3>
 <p class="text-xs text-slate-500 dark:text-slate-400 mb-4">
 Upload a previously exported <code class="text-[11px] bg-slate-100 dark:bg-warm-900 px-1 py-0.5 rounded">.pikabw</code> file. Restore is an upsert — keys present in the backup overwrite any matching keys in the running DB. Keys absent from the backup are preserved.
 </p>

 <!-- File Input -->
 <div class="mb-4">
 <label for="backup-file" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Backup File</label>
 <div class="flex items-center gap-2">
 <label
 class="flex-1 flex items-center gap-2 px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md cursor-pointer hover:bg-slate-50 dark:bg-warm-900 transition-colors"
 >
 <Upload size={14} class="text-slate-400 dark:text-slate-500 shrink-0" />
 <span class="text-slate-500 dark:text-slate-400 truncate">{importFileName || 'Choose a .pikabw backup file...'}</span>
 <input
 id="backup-file"
 type="file"
 accept=".pikabw,application/octet-stream"
 class="hidden"
 onchange={handleFileSelect}
 />
 </label>
 {#if importFile}
 <button
 class="p-2 text-slate-400 dark:text-slate-500 hover:text-red-500 hover:bg-red-50 rounded transition-colors cursor-pointer"
 onclick={() => { importFile = null; importFileName = ''; importFileIsEncrypted = false; importFileDBVersion = null; importEncryptionPassword = ''; }}
 title="Clear selection"
 >
 <Trash2 size={14} />
 </button>
 {/if}
 </div>

 {#if importFile && importFileDBVersion !== null}
 <p class="mt-2 text-[11px] text-slate-500 dark:text-slate-400">
 <span class="font-medium text-slate-700 dark:text-slate-200">Backup DB version:</span>
 <span class="font-mono">{importFileDBVersion}</span>
 {#if currentDBVersion !== null}
 <span class="text-slate-400 dark:text-slate-500">(current: <span class="font-mono">{currentDBVersion}</span>)</span>
 {/if}
 </p>
 {/if}
 </div>

 <!-- Encrypted file indicator & password -->
 {#if importFileIsEncrypted}
 <div class="mb-4 p-3 bg-accent-50 border border-accent-200 rounded-md">
 <div class="flex items-center gap-2 mb-2">
 <Lock size={14} class="text-accent-700 shrink-0" />
 <p class="text-xs font-medium text-brand-800 m-0">This backup is encrypted</p>
 </div>
 <p class="text-[11px] text-accent-700 mb-3">An encryption password is required to restore.</p>
 <div class="relative">
 <input
 id="import-encryption-password"
 type={showImportEncryptionPassword ? 'text' : 'password'}
 bind:value={importEncryptionPassword}
 placeholder="Enter the encryption password"
 class="w-full px-3 py-2 pr-9 text-sm border border-accent-200 rounded-md bg-white dark:bg-warm-900 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <button
 type="button"
 class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 dark:text-slate-500 bg-transparent border-none cursor-pointer hover:text-slate-600 dark:text-slate-300 transition-colors"
 onclick={() => showImportEncryptionPassword = !showImportEncryptionPassword}
 title={showImportEncryptionPassword ? 'Hide' : 'Show'}
 >
 {#if showImportEncryptionPassword}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
 </button>
 </div>
 </div>
 {/if}

 <!-- Wipe-and-restore opt-in. The default Restore is an upsert
 (only keys present in the backup are touched). Wiping first
 makes the running DB exactly match the backup, including
 dropping any keys that aren't in it. The server validates
 the backup BEFORE wiping, so a bad file can't destroy data —
 but the operation is still irreversible, so we hide it
 behind an explicit checkbox + confirm modal. -->
 <div class="mb-4 p-3 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md">
 <label class="flex items-start gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer">
 <input
 type="checkbox"
 bind:checked={wipeBeforeRestore}
 class="mt-0.5 accent-red-500"
 />
 <span>
 <span class="font-medium">Wipe existing data first</span>
 <span class="block text-[11px] text-slate-500 dark:text-slate-400 mt-0.5 leading-relaxed">
 Drops every key in the running database before applying the
 backup. The result matches the backup exactly. Use this to
 roll back to a snapshot — otherwise leave unchecked for an
 additive restore.
 </span>
 </span>
 </label>
 </div>

  <button
  class="flex items-center justify-center gap-2 w-full px-4 py-2.5 text-sm font-medium text-white rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed
  {wipeBeforeRestore ? 'bg-red-600 hover:bg-red-700' : 'bg-vermilion-500 hover:bg-vermilion-600'} cursor-pointer"
  onclick={handleImportBackup}
 disabled={isImporting || !backupAdminSecret.trim() || !importFile || importFileDBVersion === null || (importFileIsEncrypted && !importEncryptionPassword.trim())}
 >
 <Upload size={14} />
 {isImporting ? 'Restoring...' : wipeBeforeRestore ? 'Wipe & Restore' : 'Restore'}
 </button>
 </div>

 <!-- Wipe-and-restore confirmation modal. The lighter "merge" path
 uses a browser confirm(); this destructive path gets a real
 modal so it can't be dismissed by muscle memory. -->
 {#if showWipeConfirm}
 <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
 <div class="bg-white dark:bg-warm-900 rounded-lg shadow-xl max-w-md w-full mx-4 p-6">
 <div class="flex items-start gap-3 mb-4">
 <div class="p-2 bg-red-100 rounded-full shrink-0">
 <ShieldAlert size={20} class="text-red-600" />
 </div>
 <div>
 <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-100 mb-1">Wipe and restore?</h3>
 <p class="text-xs text-slate-500 dark:text-slate-400 leading-relaxed">
 Every key in the running database will be dropped before
 the backup is applied. This includes data the backup does
 not contain — configs, users, tokens, and settings that
 were added after the backup was taken.
 </p>
 <p class="text-xs text-slate-500 dark:text-slate-400 leading-relaxed mt-2">
 The backup file is validated before any wipe runs, so a
 bad file aborts the operation cleanly. Once the wipe
 starts, however, it is irreversible.
 </p>
 {#if importFileDBVersion !== null && currentDBVersion !== null && importFileDBVersion < currentDBVersion}
 <p class="text-xs text-amber-700 leading-relaxed mt-2 p-2 bg-amber-50 border border-amber-200 rounded">
 Note: this backup was taken at version
 <span class="font-mono">{importFileDBVersion}</span>,
 which is older than the current
 <span class="font-mono">{currentDBVersion}</span>.
 You're rolling back to an earlier snapshot.
 </p>
 {/if}
 </div>
 </div>
 <div class="flex justify-end gap-2">
 <button
 class="px-4 py-2 text-sm text-slate-600 dark:text-slate-300 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={cancelWipeRestore}
 >
 Cancel
 </button>
 <button
 class="px-4 py-2 text-sm font-medium text-white bg-red-600 rounded-md hover:bg-red-700 transition-colors cursor-pointer"
 onclick={confirmWipeRestore}
 >
 Wipe & Restore
 </button>
 </div>
 </div>
 </div>
 {/if}
</div>
