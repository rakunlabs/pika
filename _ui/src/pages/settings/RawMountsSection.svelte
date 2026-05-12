<script lang="ts">
 import { configStore } from "@/lib/store/config.svelte";
 import { addToast } from "@/lib/store/toast.svelte";
 import { appStore } from "@/lib/store/store.svelte";
 import { onMount } from "svelte";
 import { Plus, Trash2, FolderOpen } from "lucide-svelte";
 import type { RawMountEntry } from "@/lib/types/config";

 // ── Raw mounts state ──
 let rawMounts = $state<RawMountEntry[]>([]);
 let showAddMount = $state(false);
 let newMountPrefix = $state('');
 let newMountType = $state<'local' | 's3' | 'ftp' | 'sftp' | 'webdav' | 'vercel-blob'>('local');
 let newMountPath = $state('');
 // S3 fields
 let newS3Bucket = $state('');
 let newS3Region = $state('us-east-1');
 let newS3Endpoint = $state('');
 let newS3AccessKey = $state('');
 let newS3SecretKey = $state('');
 let newS3PathStyle = $state(false);
 let newS3Prefix = $state('');
 let newS3Secure = $state(true);
 // FTP fields
 let newFtpHost = $state('');
 let newFtpUsername = $state('');
 let newFtpPassword = $state('');
 let newFtpTLS = $state(false);
 let newFtpBasePath = $state('');
 // SFTP fields
 let newSftpHost = $state('');
 let newSftpUsername = $state('');
 let newSftpPassword = $state('');
 let newSftpPrivateKey = $state('');
 let newSftpBasePath = $state('');
 // WebDAV fields
 let newWebdavUrl = $state('');
 let newWebdavUsername = $state('');
 let newWebdavPassword = $state('');
 let newWebdavBasePath = $state('');
 // Vercel Blob fields
 let newVercelBlobToken = $state('');
 let newVercelBlobStoreId = $state('');
 let newVercelBlobPrefix = $state('');
 // Edit state
 let editingIndex = $state<number | null>(null);
 let isSavingMounts = $state(false);

 // Initialize rawMounts from configStore on mount
 onMount(() => {
 rawMounts = [...(configStore.settings?.raw_mounts || [])];
 });

 // ── Raw mount handlers ──
 function resetMountForm() {
 newMountPrefix = '';
 newMountType = 'local';
 newMountPath = '';
 newS3Bucket = '';
 newS3Region = 'us-east-1';
 newS3Endpoint = '';
 newS3AccessKey = '';
 newS3SecretKey = '';
 newS3PathStyle = false;
 newS3Prefix = '';
 newS3Secure = true;
 newFtpHost = '';
 newFtpUsername = '';
 newFtpPassword = '';
 newFtpTLS = false;
 newFtpBasePath = '';
 newSftpHost = '';
 newSftpUsername = '';
 newSftpPassword = '';
 newSftpPrivateKey = '';
 newSftpBasePath = '';
 newWebdavUrl = '';
 newWebdavUsername = '';
 newWebdavPassword = '';
 newWebdavBasePath = '';
 newVercelBlobToken = '';
 newVercelBlobStoreId = '';
 newVercelBlobPrefix = '';
 editingIndex = null;
 }

 function loadMountIntoForm(mount: RawMountEntry) {
 newMountPrefix = mount.prefix;
 newMountType = (mount.type || 'local') as typeof newMountType;
 newMountPath = mount.path || '';
 newS3Bucket = mount.s3?.bucket || '';
 newS3Region = mount.s3?.region || 'us-east-1';
 newS3Endpoint = mount.s3?.endpoint || '';
 newS3AccessKey = mount.s3?.access_key || '';
 newS3SecretKey = mount.s3?.secret_key || '';
 newS3PathStyle = mount.s3?.path_style || false;
 newS3Prefix = mount.s3?.prefix || '';
 newS3Secure = mount.s3?.secure ?? true;
 newFtpHost = mount.ftp?.host || '';
 newFtpUsername = mount.ftp?.username || '';
 newFtpPassword = mount.ftp?.password || '';
 newFtpTLS = mount.ftp?.tls || false;
 newFtpBasePath = mount.ftp?.base_path || '';
 newSftpHost = mount.sftp?.host || '';
 newSftpUsername = mount.sftp?.username || '';
 newSftpPassword = mount.sftp?.password || '';
 newSftpPrivateKey = mount.sftp?.private_key || '';
 newSftpBasePath = mount.sftp?.base_path || '';
 newWebdavUrl = mount.webdav?.url || '';
 newWebdavUsername = mount.webdav?.username || '';
 newWebdavPassword = mount.webdav?.password || '';
 newWebdavBasePath = mount.webdav?.base_path || '';
 newVercelBlobToken = mount.vercelBlob?.token || '';
 newVercelBlobStoreId = mount.vercelBlob?.store_id || '';
 newVercelBlobPrefix = mount.vercelBlob?.prefix || '';
 }

 function handleEditMount(index: number) {
 const mount = rawMounts[index];
 loadMountIntoForm(mount);
 editingIndex = index;
 showAddMount = true;
 }

 async function handleAddMount() {
 const prefix = newMountPrefix.trim();

 if (!prefix) {
 addToast('Prefix is required', 'alert');
 return;
 }
 // Check for duplicate prefix (skip the one being edited)
 if (rawMounts.some((m, i) => m.prefix === prefix && i !== editingIndex)) {
 addToast(`A mount with prefix "${prefix}" already exists`, 'alert');
 return;
 }

 const entry: RawMountEntry = { prefix, type: newMountType };

 if (newMountType === 'local') {
 if (!newMountPath.trim()) {
 addToast('Directory path is required', 'alert');
 return;
 }
 entry.path = newMountPath.trim();
 } else if (newMountType === 's3') {
 if (!newS3Bucket.trim()) {
 addToast('S3 bucket is required', 'alert');
 return;
 }
 entry.s3 = {
 bucket: newS3Bucket.trim(),
 region: newS3Region.trim() || 'us-east-1',
 endpoint: newS3Endpoint.trim() || undefined,
 access_key: newS3AccessKey.trim() || undefined,
 secret_key: newS3SecretKey.trim() || undefined,
 path_style: newS3PathStyle || undefined,
 prefix: newS3Prefix.trim() || undefined,
 secure: newS3Secure,
 };
 } else if (newMountType === 'ftp') {
 if (!newFtpHost.trim()) {
 addToast('FTP host is required', 'alert');
 return;
 }
 entry.ftp = {
 host: newFtpHost.trim(),
 username: newFtpUsername.trim() || undefined,
 password: newFtpPassword.trim() || undefined,
 tls: newFtpTLS || undefined,
 base_path: newFtpBasePath.trim() || undefined,
 };
 } else if (newMountType === 'sftp') {
 if (!newSftpHost.trim()) {
 addToast('SFTP host is required', 'alert');
 return;
 }
 entry.sftp = {
 host: newSftpHost.trim(),
 username: newSftpUsername.trim() || undefined,
 password: newSftpPassword.trim() || undefined,
 private_key: newSftpPrivateKey.trim() || undefined,
 base_path: newSftpBasePath.trim() || undefined,
 };
 } else if (newMountType === 'webdav') {
 if (!newWebdavUrl.trim()) {
 addToast('WebDAV URL is required', 'alert');
 return;
 }
 entry.webdav = {
 url: newWebdavUrl.trim(),
 username: newWebdavUsername.trim() || undefined,
 password: newWebdavPassword.trim() || undefined,
 base_path: newWebdavBasePath.trim() || undefined,
 };
 } else if (newMountType === 'vercel-blob') {
 if (!newVercelBlobToken.trim()) {
 addToast('Vercel Blob token is required', 'alert');
 return;
 }
 entry.vercelBlob = {
 token: newVercelBlobToken.trim(),
 store_id: newVercelBlobStoreId.trim() || undefined,
 prefix: newVercelBlobPrefix.trim() || undefined,
 };
 }

 let updated: RawMountEntry[];
 if (editingIndex !== null) {
 // Replace the existing entry
 updated = rawMounts.map((m, i) => i === editingIndex ? entry : m);
 } else {
 updated = [...rawMounts, entry];
 }
 isSavingMounts = true;
 try {
 await configStore.saveRawMounts(updated);
 rawMounts = updated;
 showAddMount = false;
 resetMountForm();
 await appStore.loadInfo();
 } catch {
 // Error toast already shown by store
 } finally {
 isSavingMounts = false;
 }
 }

 async function handleRemoveMount(index: number) {
 const mount = rawMounts[index];
 if (!confirm(`Remove raw mount "${mount.prefix}" (${mount.path})?`)) return;

 const updated = rawMounts.filter((_, i) => i !== index);
 isSavingMounts = true;
 try {
 await configStore.saveRawMounts(updated);
 rawMounts = updated;
 // Reload app info so navbar updates
 await appStore.loadInfo();
 } catch {
 // Error toast already shown by store
 } finally {
 isSavingMounts = false;
 }
 }
</script>

<div>
 <div class="flex items-center justify-between mb-4">
 <div>
 <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">Raw Filesystem Mounts</h2>
 <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">Serve files from local directories at <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded text-[11px]">/raw/{'{prefix}'}/...</code></p>
 </div>
 <button
 class="flex items-center gap-1.5 px-3 py-2 bg-accent-600 text-white text-sm font-medium rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
 onclick={() => showAddMount = true}
 >
 <Plus size={14} />
 Add Mount
 </button>
 </div>

 <!-- Add Mount Form -->
 {#if showAddMount}
 <div class="mb-6 p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
 <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-4">{editingIndex !== null ? 'Edit Raw Mount' : 'Add Raw Mount'}</h3>

 <div class="mb-4">
 <label for="mount-prefix" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Prefix</label>
 <input id="mount-prefix" type="text" bind:value={newMountPrefix} placeholder="e.g., configs"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">URL prefix — files will be served at <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded text-[10px]">/raw/{newMountPrefix || 'prefix'}/...</code></p>
 </div>

 <div class="mb-4">
 <span class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Backend Type</span>
 <div class="flex gap-3">
 <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="radio" bind:group={newMountType} value="local" class="text-accent-600" /> Local Directory
 </label>
 <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="radio" bind:group={newMountType} value="s3" class="text-accent-600" /> S3 Compatible
 </label>
 <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="radio" bind:group={newMountType} value="ftp" class="text-accent-600" /> FTP / FTPS
 </label>
 <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="radio" bind:group={newMountType} value="sftp" class="text-accent-600" /> SFTP (SSH)
 </label>
 <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="radio" bind:group={newMountType} value="webdav" class="text-accent-600" /> WebDAV
 </label>
 <label class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="radio" bind:group={newMountType} value="vercel-blob" class="text-accent-600" /> Vercel Blob
 </label>
 </div>
 </div>

 {#if newMountType === 'local'}
 <div class="mb-4">
 <label for="mount-path" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Directory Path</label>
 <input id="mount-path" type="text" bind:value={newMountPath} placeholder="/opt/configs"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Absolute path to a directory on the server's filesystem (also works with FUSE mounts)</p>
 </div>

 {:else if newMountType === 's3'}
 <div class="grid grid-cols-2 gap-3 mb-4">
 <div>
 <label for="s3-bucket" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Bucket</label>
 <input id="s3-bucket" type="text" bind:value={newS3Bucket} placeholder="my-bucket"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="s3-region" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Region</label>
 <input id="s3-region" type="text" bind:value={newS3Region} placeholder="us-east-1"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div class="mb-4">
 <label for="s3-endpoint" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Endpoint (optional)</label>
 <input id="s3-endpoint" type="text" bind:value={newS3Endpoint} placeholder="s3.amazonaws.com or minio.local:9000"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Leave empty for AWS S3. Set for MinIO, Cloudflare R2, etc.</p>
 </div>
 <div class="grid grid-cols-2 gap-3 mb-4">
 <div>
 <label for="s3-access-key" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Access Key</label>
 <input id="s3-access-key" type="text" bind:value={newS3AccessKey} placeholder="AKIA..."
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="s3-secret-key" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Secret Key</label>
 <input id="s3-secret-key" type="password" bind:value={newS3SecretKey} placeholder="Secret key"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div class="mb-4">
 <label for="s3-prefix" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Key Prefix (optional)</label>
 <input id="s3-prefix" type="text" bind:value={newS3Prefix} placeholder="configs/"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Only serve keys under this prefix within the bucket</p>
 </div>
 <div class="flex gap-4 mb-4">
 <label class="flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="checkbox" bind:checked={newS3PathStyle} class="rounded border-slate-300" />
 Path-style access (MinIO)
 </label>
 <label class="flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="checkbox" bind:checked={newS3Secure} class="rounded border-slate-300" />
 Use HTTPS
 </label>
 </div>

 {:else if newMountType === 'ftp'}
 <div class="grid grid-cols-2 gap-3 mb-4">
 <div>
 <label for="ftp-host" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Host</label>
 <input id="ftp-host" type="text" bind:value={newFtpHost} placeholder="ftp.example.com:21"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="ftp-basepath" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Base Path (optional)</label>
 <input id="ftp-basepath" type="text" bind:value={newFtpBasePath} placeholder="/data"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div class="grid grid-cols-2 gap-3 mb-4">
 <div>
 <label for="ftp-username" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Username</label>
 <input id="ftp-username" type="text" bind:value={newFtpUsername} placeholder="admin"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="ftp-password" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Password</label>
 <input id="ftp-password" type="password" bind:value={newFtpPassword} placeholder="Password"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div class="mb-4">
 <label class="flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="checkbox" bind:checked={newFtpTLS} class="rounded border-slate-300" />
 Use FTPS (explicit TLS)
 </label>
 </div>
 {:else if newMountType === 'sftp'}
 <div class="grid grid-cols-2 gap-3 mb-4">
 <div>
 <label for="sftp-host" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Host</label>
 <input id="sftp-host" type="text" bind:value={newSftpHost} placeholder="ssh.example.com:22"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="sftp-basepath" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Base Path (optional)</label>
 <input id="sftp-basepath" type="text" bind:value={newSftpBasePath} placeholder="/data"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div class="grid grid-cols-2 gap-3 mb-4">
 <div>
 <label for="sftp-username" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Username</label>
 <input id="sftp-username" type="text" bind:value={newSftpUsername} placeholder="admin"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="sftp-password" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Password</label>
 <input id="sftp-password" type="password" bind:value={newSftpPassword} placeholder="Password"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div class="mb-4">
 <label for="sftp-key" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Private Key (optional, PEM format)</label>
 <textarea id="sftp-key" bind:value={newSftpPrivateKey} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;..."
 rows="4"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 resize-y" ></textarea>
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Used instead of password authentication. Paste the full PEM-encoded key.</p>
 </div>
 {:else if newMountType === 'webdav'}
 <div class="mb-4">
 <label for="webdav-url" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">URL</label>
 <input id="webdav-url" type="text" bind:value={newWebdavUrl} placeholder="https://example.com/dav/"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Full WebDAV endpoint URL</p>
 </div>
 <div class="grid grid-cols-2 gap-3 mb-4">
 <div>
 <label for="webdav-username" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Username</label>
 <input id="webdav-username" type="text" bind:value={newWebdavUsername} placeholder="admin"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="webdav-password" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Password</label>
 <input id="webdav-password" type="password" bind:value={newWebdavPassword} placeholder="Password"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div class="mb-4">
 <label for="webdav-basepath" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Base Path (optional)</label>
 <input id="webdav-basepath" type="text" bind:value={newWebdavBasePath} placeholder="/data"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Sub-path within the WebDAV root to use as mount root</p>
 </div>
 {:else if newMountType === 'vercel-blob'}
 <div class="mb-4">
 <label for="vercel-blob-token" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Token</label>
 <input id="vercel-blob-token" type="password" bind:value={newVercelBlobToken} placeholder="vercel_blob_rw_..."
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">BLOB_READ_WRITE_TOKEN from your Vercel project settings</p>
 </div>
 <div class="grid grid-cols-2 gap-3 mb-4">
 <div>
 <label for="vercel-blob-store-id" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Store ID (optional)</label>
 <input id="vercel-blob-store-id" type="text" bind:value={newVercelBlobStoreId} placeholder="store_abc123"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="vercel-blob-prefix" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Key Prefix (optional)</label>
 <input id="vercel-blob-prefix" type="text" bind:value={newVercelBlobPrefix} placeholder="configs/"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 {/if}

 <div class="flex justify-end gap-2">
 <button
 class="px-3 py-2 text-sm text-slate-600 dark:text-slate-300 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => { showAddMount = false; resetMountForm(); }}
 >
 Cancel
 </button>
 <button
 class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
 onclick={handleAddMount}
 disabled={isSavingMounts}
 >
 {isSavingMounts ? 'Saving...' : editingIndex !== null ? 'Save Changes' : 'Add Mount'}
 </button>
 </div>
 </div>
 {/if}

 <!-- Mount List -->
 {#if rawMounts.length === 0}
 <div class="text-center py-12 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg">
 <FolderOpen size={32} class="mx-auto text-slate-300 mb-3" />
 <p class="text-sm text-slate-500 dark:text-slate-400">No raw mounts configured</p>
 <p class="text-xs text-slate-400 dark:text-slate-500 mt-1">Add a mount to serve files from a local directory, S3 bucket, FTP server, or WebDAV server</p>
 </div>
 {:else}
 <div class="space-y-2">
 {#each rawMounts as mount, i (mount.prefix)}
 {@const mType = mount.type || 'local'}
 <div class="flex items-center gap-4 p-4 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg hover:border-slate-300 transition-colors">
 <div class="flex-1 min-w-0">
 <div class="flex items-center gap-2">
 <span class="text-sm font-medium text-slate-800 dark:text-slate-100">{mount.prefix}</span>
 <span class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-emerald-100 text-emerald-700">
 /raw/{mount.prefix}
 </span>
 <span class="px-1.5 py-0.5 text-[10px] font-medium rounded
 {mType === 's3' ? 'bg-orange-100 text-orange-700' : mType === 'ftp' ? 'bg-purple-100 text-purple-700' : mType === 'sftp' ? 'bg-teal-100 text-teal-700' : mType === 'webdav' ? 'bg-indigo-100 text-indigo-700' : mType === 'vercel-blob' ? 'bg-sky-100 text-sky-700' : 'bg-accent-100 text-brand-700'}">
 {mType === 's3' ? 'S3' : mType === 'ftp' ? 'FTP' : mType === 'sftp' ? 'SFTP' : mType === 'webdav' ? 'WebDAV' : mType === 'vercel-blob' ? 'Vercel Blob' : 'Local'}
 </span>
 </div>
 <div class="mt-1">
 {#if mType === 'local'}
 <span class="text-xs font-mono text-slate-400 dark:text-slate-500">{mount.path}</span>
 {:else if mType === 's3'}
 <span class="text-xs font-mono text-slate-400 dark:text-slate-500">
 {mount.s3?.endpoint ? mount.s3.endpoint + '/' : ''}{mount.s3?.bucket}{mount.s3?.prefix ? '/' + mount.s3.prefix : ''}
 </span>
 {:else if mType === 'ftp'}
 <span class="text-xs font-mono text-slate-400 dark:text-slate-500">
 {mount.ftp?.host}{mount.ftp?.base_path || ''}
 </span>
 {:else if mType === 'sftp'}
 <span class="text-xs font-mono text-slate-400 dark:text-slate-500">
 {mount.sftp?.username ? mount.sftp.username + '@' : ''}{mount.sftp?.host}{mount.sftp?.base_path || ''}
 </span>
 {:else if mType === 'webdav'}
 <span class="text-xs font-mono text-slate-400 dark:text-slate-500">
 {mount.webdav?.url}{mount.webdav?.base_path || ''}
 </span>
 {:else if mType === 'vercel-blob'}
 <span class="text-xs font-mono text-slate-400 dark:text-slate-500">
 vercel-blob{mount.vercelBlob?.store_id ? '/' + mount.vercelBlob.store_id : ''}{mount.vercelBlob?.prefix ? '/' + mount.vercelBlob.prefix : ''}
 </span>
 {/if}
 </div>
 </div>
 <div class="flex items-center gap-1 shrink-0">
 <button
 class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-accent-600 hover:bg-accent-50 dark:hover:bg-accent-900/30 rounded transition-colors cursor-pointer"
 onclick={() => handleEditMount(i)}
 title="Edit mount"
 >
 <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/><path d="m15 5 4 4"/></svg>
 </button>
 <button
 class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-red-500 hover:bg-red-50 rounded transition-colors cursor-pointer"
 onclick={() => handleRemoveMount(i)}
 title="Remove mount"
 >
 <Trash2 size={14} />
 </button>
 </div>
 </div>
 {/each}
 </div>
 {/if}
</div>
