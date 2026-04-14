<script lang="ts">
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { Eye, EyeOff, Lock, Download, Upload, Trash2, ShieldAlert } from "lucide-svelte";
  import axios from 'axios';

  // ── Backup state ──
  let backupAdminSecret = $state('');
  let showBackupAdminSecret = $state(false);
  let isExporting = $state(false);
  let isImporting = $state(false);
  let importMode = $state<'replace' | 'merge'>('merge');
  let importFile = $state<File | null>(null);
  let importFileName = $state('');
  let encryptionPassword = $state('');
  let showEncryptionPassword = $state(false);
  let importEncryptionPassword = $state('');
  let showImportEncryptionPassword = $state(false);
  let importFileIsEncrypted = $state(false);
  let showUnencryptedWarning = $state(false);

  // ── Backup handlers ──
  function handleExportBackup() {
    if (!backupAdminSecret.trim()) {
      addToast('Admin secret is required', 'alert');
      return;
    }

    // If no encryption password, show warning prompt
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

      const response = await axios.get('/api/v1/backup', {
        params,
        responseType: 'blob'
      });

      // Trigger browser download
      const blob = new Blob([response.data], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      const timestamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
      a.href = url;
      a.download = `pika-backup-${timestamp}.json`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);

      addToast(encryptionPassword.trim() ? 'Encrypted backup downloaded successfully' : 'Backup downloaded successfully', 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || error.response?.statusText || 'Export failed';
      // If response is a blob, try to read the error message
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

      // Detect if the file is encrypted
      try {
        const text = await input.files[0].text();
        const parsed = JSON.parse(text);
        importFileIsEncrypted = !!parsed.encrypted;
      } catch {
        importFileIsEncrypted = false;
      }
    }
  }

  async function handleImportBackup() {
    if (!backupAdminSecret.trim()) {
      addToast('Admin secret is required', 'alert');
      return;
    }
    if (!importFile) {
      addToast('Please select a backup file', 'alert');
      return;
    }
    if (importFileIsEncrypted && !importEncryptionPassword.trim()) {
      addToast('Encryption password is required for encrypted backups', 'alert');
      return;
    }

    const confirmMsg = importMode === 'replace'
      ? 'This will REPLACE all existing configurations with the backup data. This cannot be undone. Continue?'
      : 'This will MERGE the backup data into existing configurations. Existing items with matching keys will be overwritten. Continue?';

    if (!confirm(confirmMsg)) return;

    isImporting = true;
    try {
      const text = await importFile.text();
      let backupData: any;
      try {
        backupData = JSON.parse(text);
      } catch {
        addToast('Invalid backup file: not valid JSON', 'alert');
        return;
      }

      const body: Record<string, any> = {
        admin_secret: backupAdminSecret.trim(),
        mode: importMode,
        data: backupData
      };
      if (importEncryptionPassword.trim()) {
        body.encryption_password = importEncryptionPassword.trim();
      }

      await axios.post('/api/v1/backup', body);

      addToast('Backup imported successfully', 'success');
      importFile = null;
      importFileName = '';
      importFileIsEncrypted = false;
      importEncryptionPassword = '';

      // Refresh settings and tree
      configStore.loadSettings();
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Import failed';
      addToast(msg, 'alert');
    } finally {
      isImporting = false;
    }
  }
</script>

<div>
  <div class="mb-4">
    <h2 class="text-lg font-semibold text-slate-800">Backup & Restore</h2>
    <p class="text-sm text-slate-500 mt-0.5">Export all configurations as a backup file or restore from a previous backup</p>
  </div>

  <!-- Admin Secret (shared for both operations) -->
  <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
    <div class="mb-4">
      <label for="backup-admin-secret" class="block text-xs font-medium text-slate-500 mb-1.5">Admin Secret</label>
      <div class="relative">
        <input
          id="backup-admin-secret"
          type={showBackupAdminSecret ? 'text' : 'password'}
          bind:value={backupAdminSecret}
          placeholder="Enter your admin secret"
          class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
        />
        <button
          type="button"
          class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
          onclick={() => showBackupAdminSecret = !showBackupAdminSecret}
          title={showBackupAdminSecret ? 'Hide' : 'Show'}
        >
          {#if showBackupAdminSecret}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
        </button>
      </div>
      <p class="mt-1 text-[11px] text-slate-400">Required for both export and import operations</p>
    </div>
  </div>

  <!-- Export Section -->
  <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
    <h3 class="text-sm font-semibold text-slate-700 mb-2">Download Backup</h3>
    <p class="text-xs text-slate-500 mb-4">
      Export all configuration data (folders, files, file versions, and settings) as a JSON file.
      Users, tokens, and the admin secret hash are not included in the backup.
    </p>

    <!-- Encryption Password for Export -->
    <div class="mb-4">
      <label for="export-encryption-password" class="block text-xs font-medium text-slate-500 mb-1.5">Encryption Password (optional)</label>
      <div class="relative">
        <input
          id="export-encryption-password"
          type={showEncryptionPassword ? 'text' : 'password'}
          bind:value={encryptionPassword}
          placeholder="Enter a password to encrypt the backup"
          class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
        />
        <button
          type="button"
          class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
          onclick={() => showEncryptionPassword = !showEncryptionPassword}
          title={showEncryptionPassword ? 'Hide' : 'Show'}
        >
          {#if showEncryptionPassword}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
        </button>
      </div>
      <p class="mt-1 text-[11px] text-slate-400">If set, the backup file will be encrypted. You will need this password to import it later.</p>
    </div>

    <button
      class="flex items-center justify-center gap-2 w-full px-4 py-2.5 text-sm font-medium text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
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
      <div class="bg-white rounded-lg shadow-xl max-w-md w-full mx-4 p-6">
        <div class="flex items-start gap-3 mb-4">
          <div class="p-2 bg-amber-100 rounded-full shrink-0">
            <ShieldAlert size={20} class="text-amber-600" />
          </div>
          <div>
            <h3 class="text-sm font-semibold text-slate-800 mb-1">Export without encryption?</h3>
            <p class="text-xs text-slate-500 leading-relaxed">
              You are about to export a backup without encryption. The backup file will contain all your configuration data in plain text.
              Anyone who obtains this file will be able to read its contents.
            </p>
            <p class="text-xs text-slate-500 leading-relaxed mt-2">
              To encrypt the backup, cancel and enter an encryption password above.
            </p>
          </div>
        </div>
        <div class="flex justify-end gap-2">
          <button
            class="px-4 py-2 text-sm text-slate-600 bg-white border border-slate-200 rounded-md hover:bg-slate-50 transition-colors"
            onclick={cancelUnencryptedExport}
          >
            Cancel
          </button>
          <button
            class="px-4 py-2 text-sm font-medium text-white bg-amber-500 rounded-md hover:bg-amber-600 transition-colors"
            onclick={confirmUnencryptedExport}
          >
            Continue without encryption
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Import Section -->
  <div class="p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
    <h3 class="text-sm font-semibold text-slate-700 mb-2">Restore from Backup</h3>
    <p class="text-xs text-slate-500 mb-4">
      Upload a previously exported backup file to restore configurations.
    </p>

    <!-- File Input -->
    <div class="mb-4">
      <label for="backup-file" class="block text-xs font-medium text-slate-500 mb-1.5">Backup File</label>
      <div class="flex items-center gap-2">
        <label
          class="flex-1 flex items-center gap-2 px-3 py-2 text-sm border border-slate-200 rounded-md cursor-pointer hover:bg-slate-50 transition-colors"
        >
          <Upload size={14} class="text-slate-400 shrink-0" />
          <span class="text-slate-500 truncate">{importFileName || 'Choose a .json backup file...'}</span>
          <input
            id="backup-file"
            type="file"
            accept=".json,application/json"
            class="hidden"
            onchange={handleFileSelect}
          />
        </label>
        {#if importFile}
          <button
            class="p-2 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors"
            onclick={() => { importFile = null; importFileName = ''; importFileIsEncrypted = false; importEncryptionPassword = ''; }}
            title="Clear selection"
          >
            <Trash2 size={14} />
          </button>
        {/if}
      </div>
    </div>

    <!-- Encrypted file indicator & password -->
    {#if importFileIsEncrypted}
      <div class="mb-4 p-3 bg-blue-50 border border-blue-200 rounded-md">
        <div class="flex items-center gap-2 mb-2">
          <Lock size={14} class="text-blue-600 shrink-0" />
          <p class="text-xs font-medium text-blue-800 m-0">This backup file is encrypted</p>
        </div>
        <p class="text-[11px] text-blue-600 mb-3">An encryption password is required to import this backup.</p>
        <div class="relative">
          <input
            id="import-encryption-password"
            type={showImportEncryptionPassword ? 'text' : 'password'}
            bind:value={importEncryptionPassword}
            placeholder="Enter the encryption password"
            class="w-full px-3 py-2 pr-9 text-sm border border-blue-200 rounded-md bg-white focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
          />
          <button
            type="button"
            class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
            onclick={() => showImportEncryptionPassword = !showImportEncryptionPassword}
            title={showImportEncryptionPassword ? 'Hide' : 'Show'}
          >
            {#if showImportEncryptionPassword}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
          </button>
        </div>
      </div>
    {:else if importFile}
      <div class="mb-4">
        <label for="import-encryption-password-opt" class="block text-xs font-medium text-slate-500 mb-1.5">Encryption Password (optional)</label>
        <div class="relative">
          <input
            id="import-encryption-password-opt"
            type={showImportEncryptionPassword ? 'text' : 'password'}
            bind:value={importEncryptionPassword}
            placeholder="Enter password if the backup is encrypted"
            class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
          />
          <button
            type="button"
            class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
            onclick={() => showImportEncryptionPassword = !showImportEncryptionPassword}
            title={showImportEncryptionPassword ? 'Hide' : 'Show'}
          >
            {#if showImportEncryptionPassword}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
          </button>
        </div>
        <p class="mt-1 text-[11px] text-slate-400">Only needed if the backup was exported with encryption</p>
      </div>
    {/if}

    <!-- Mode Selection -->
    <div class="mb-4">
      <span class="block text-xs font-medium text-slate-500 mb-1.5">Import Mode</span>
      <div class="flex gap-4">
        <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
          <input type="radio" bind:group={importMode} value="merge" class="text-blue-500" />
          Merge
        </label>
        <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
          <input type="radio" bind:group={importMode} value="replace" class="text-blue-500" />
          Replace
        </label>
      </div>
      <p class="mt-1.5 text-[11px] text-slate-400">
        {#if importMode === 'merge'}
          Imports backup data on top of existing configurations. Items with matching keys will be overwritten.
        {:else}
          Removes all existing configurations and replaces them with the backup data. This cannot be undone.
        {/if}
      </p>
    </div>

    <!-- Warning for replace mode -->
    {#if importMode === 'replace'}
      <div class="mb-4 p-3 bg-red-50 border border-red-200 rounded-md">
        <p class="text-xs text-red-800 leading-relaxed m-0">
          Replace mode will delete all existing folders, files, and file versions before importing the backup data. This operation cannot be undone.
        </p>
      </div>
    {/if}

    <button
      class="flex items-center justify-center gap-2 w-full px-4 py-2.5 text-sm font-medium text-white rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed
        {importMode === 'replace' ? 'bg-red-600 hover:bg-red-700' : 'bg-blue-500 hover:bg-blue-600'}"
      onclick={handleImportBackup}
      disabled={isImporting || !backupAdminSecret.trim() || !importFile || (importFileIsEncrypted && !importEncryptionPassword.trim())}
    >
      <Upload size={14} />
      {isImporting ? 'Importing...' : importMode === 'replace' ? 'Replace & Import' : 'Merge & Import'}
    </button>
  </div>
</div>
