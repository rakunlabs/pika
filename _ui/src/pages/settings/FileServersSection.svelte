<script lang="ts">
 import { configStore } from "@/lib/store/config.svelte";
 import { addToast } from "@/lib/store/toast.svelte";
 import { onMount } from "svelte";
 import { RotateCw } from "lucide-svelte";
 import type { FTPServeSettings, SFTPServeSettings, TFTPServeSettings, WebDAVServeSettings } from "@/lib/types/config";
 import axios from 'axios';

 // ── File server state ──
 let ftpServeEnabled = $state(false);
 let ftpServePort = $state(2121);
 let ftpServeHost = $state('');
 let ftpServePublicIP = $state('');
 let ftpServePassivePorts = $state('30000-30100');
 let ftpServeTLSCertFile = $state('');
 let ftpServeTLSKeyFile = $state('');
 let ftpServeTLSCertPEM = $state('');
 let ftpServeTLSKeyPEM = $state('');
 let ftpServeTLSRequired = $state(0);
 let ftpServeTLSInputMode = $state<'path' | 'paste'>('path');
 let sftpServeEnabled = $state(false);
 let sftpServePort = $state(2222);
 let sftpServeHost = $state('');
 let sftpServeHostKeyPath = $state('');
 let sftpServeHostKeyPEM = $state('');
 let sftpServeKeyInputMode = $state<'path' | 'paste'>('path');
 let tftpServeEnabled = $state(false);
 let tftpServePort = $state(69);
 let tftpServeHost = $state('');
 let webdavServeEnabled = $state(false);
 let webdavServePort = $state(9119);
 let webdavServeHost = $state('');
 let webdavServePrefix = $state('/');
 let isSavingServers = $state(false);
 let isGeneratingTLS = $state(false);
 let isGeneratingSSHKey = $state(false);

 function loadServeSettings() {
 const s = configStore.settings;
 ftpServeEnabled = s?.ftp_serve?.enabled ?? false;
 ftpServePort = s?.ftp_serve?.port || 2121;
 ftpServeHost = s?.ftp_serve?.host ?? '';
 ftpServePublicIP = s?.ftp_serve?.public_ip ?? '';
 ftpServePassivePorts = s?.ftp_serve?.passive_ports || '30000-30100';
 ftpServeTLSCertFile = s?.ftp_serve?.tls_cert_file ?? '';
 ftpServeTLSKeyFile = s?.ftp_serve?.tls_key_file ?? '';
 ftpServeTLSCertPEM = s?.ftp_serve?.tls_cert_pem ?? '';
 ftpServeTLSKeyPEM = s?.ftp_serve?.tls_key_pem ?? '';
 ftpServeTLSRequired = s?.ftp_serve?.tls_required ?? 0;
 // Auto-select input mode based on which fields have data
 ftpServeTLSInputMode = (ftpServeTLSCertPEM || ftpServeTLSKeyPEM) ? 'paste' : 'path';
 sftpServeEnabled = s?.sftp_serve?.enabled ?? false;
 sftpServePort = s?.sftp_serve?.port || 2222;
 sftpServeHost = s?.sftp_serve?.host ?? '';
 sftpServeHostKeyPath = s?.sftp_serve?.host_key_path ?? '';
 sftpServeHostKeyPEM = s?.sftp_serve?.host_key_pem ?? '';
 // Auto-select input mode based on which fields have data
 sftpServeKeyInputMode = sftpServeHostKeyPEM ? 'paste' : 'path';
 tftpServeEnabled = s?.tftp_serve?.enabled ?? false;
 tftpServePort = s?.tftp_serve?.port || 69;
 tftpServeHost = s?.tftp_serve?.host ?? '';
 webdavServeEnabled = s?.webdav_serve?.enabled ?? false;
 webdavServePort = s?.webdav_serve?.port || 9119;
 webdavServeHost = s?.webdav_serve?.host ?? '';
 webdavServePrefix = s?.webdav_serve?.prefix || '/';
 }

 async function handleGenerateTLS() {
 isGeneratingTLS = true;
 try {
 const resp = await axios.post('/api/v1/tls-generate', {});
 ftpServeTLSCertPEM = resp.data.cert_pem;
 ftpServeTLSKeyPEM = resp.data.key_pem;
 ftpServeTLSInputMode = 'paste';
 addToast('Self-signed TLS certificate generated', 'success');
 } catch (error: any) {
 const msg = error.response?.data?.message || 'Failed to generate TLS certificate';
 addToast(msg, 'alert');
 } finally {
 isGeneratingTLS = false;
 }
 }

 async function handleGenerateSSHKey() {
 isGeneratingSSHKey = true;
 try {
 const resp = await axios.post('/api/v1/ssh-keygen', {});
 sftpServeHostKeyPEM = resp.data.key_pem;
 sftpServeKeyInputMode = 'paste';
 addToast('Ed25519 host key generated', 'success');
 } catch (error: any) {
 const msg = error.response?.data?.message || 'Failed to generate SSH key';
 addToast(msg, 'alert');
 } finally {
 isGeneratingSSHKey = false;
 }
 }

 async function handleSaveServers() {
 isSavingServers = true;
 try {
 // ── PEM content validation ──
 if (ftpServeEnabled && ftpServeTLSRequired > 0 && ftpServeTLSInputMode === 'paste') {
 if (ftpServeTLSCertPEM && !ftpServeTLSCertPEM.includes('BEGIN CERTIFICATE')) {
 const hasPublicKey = ftpServeTLSCertPEM.includes('PUBLIC KEY');
 addToast(
 hasPublicKey
 ? 'TLS certificate field contains a public key, not a certificate. Paste the X.509 certificate (cert.pem) instead.'
 : 'TLS certificate field does not contain a valid PEM certificate. Expected -----BEGIN CERTIFICATE-----.',
 'alert'
 );
 return;
 }
 if (ftpServeTLSKeyPEM && !ftpServeTLSKeyPEM.includes('PRIVATE KEY')) {
 addToast('TLS key field does not contain a private key. Expected -----BEGIN PRIVATE KEY----- (or RSA/EC PRIVATE KEY).', 'alert');
 return;
 }
 }
 if (sftpServeEnabled && sftpServeKeyInputMode === 'paste' && sftpServeHostKeyPEM) {
 if (!sftpServeHostKeyPEM.includes('PRIVATE KEY')) {
 const hasPublicKey = sftpServeHostKeyPEM.includes('PUBLIC KEY');
 addToast(
 hasPublicKey
 ? 'SFTP host key field contains a public key. Paste the private key instead.'
 : 'SFTP host key field does not contain a private key. Expected -----BEGIN OPENSSH PRIVATE KEY----- or -----BEGIN PRIVATE KEY-----.',
 'alert'
 );
 return;
 }
 }

 const s = configStore.settings;
 const patch: {
 ftp_serve?: FTPServeSettings;
 sftp_serve?: SFTPServeSettings;
 tftp_serve?: TFTPServeSettings;
 webdav_serve?: WebDAVServeSettings;
 } = {};

 const ftpServe: FTPServeSettings = {
 enabled: ftpServeEnabled,
 port: ftpServePort,
 host: ftpServeHost || undefined,
 public_ip: ftpServePublicIP || undefined,
 passive_ports: ftpServePassivePorts || undefined,
 tls_cert_file: ftpServeTLSCertFile || undefined,
 tls_key_file: ftpServeTLSKeyFile || undefined,
 tls_cert_pem: ftpServeTLSCertPEM || undefined,
 tls_key_pem: ftpServeTLSKeyPEM || undefined,
 tls_required: ftpServeTLSRequired || undefined,
 };
 if (JSON.stringify(ftpServe) !== JSON.stringify(s?.ftp_serve ?? {})) {
 patch.ftp_serve = ftpServe;
 }

 const sftpServe: SFTPServeSettings = {
 enabled: sftpServeEnabled,
 port: sftpServePort,
 host: sftpServeHost || undefined,
 host_key_path: sftpServeHostKeyPath || undefined,
 host_key_pem: sftpServeHostKeyPEM || undefined,
 };
 if (JSON.stringify(sftpServe) !== JSON.stringify(s?.sftp_serve ?? {})) {
 patch.sftp_serve = sftpServe;
 }

 const tftpServe: TFTPServeSettings = {
 enabled: tftpServeEnabled,
 port: tftpServePort,
 host: tftpServeHost || undefined,
 };
 if (JSON.stringify(tftpServe) !== JSON.stringify(s?.tftp_serve ?? {})) {
 patch.tftp_serve = tftpServe;
 }

 const webdavServe: WebDAVServeSettings = {
 enabled: webdavServeEnabled,
 port: webdavServePort,
 host: webdavServeHost || undefined,
 prefix: webdavServePrefix || undefined,
 };
 if (JSON.stringify(webdavServe) !== JSON.stringify(s?.webdav_serve ?? {})) {
 patch.webdav_serve = webdavServe;
 }

 if (Object.keys(patch).length === 0) {
 addToast('No changes detected.', 'info');
 return;
 }

 await configStore.saveServeSettings(patch);
 } catch {
 // toast already shown by store
 } finally {
 isSavingServers = false;
 }
 }

 onMount(() => {
 loadServeSettings();
 });
</script>

<div>
 <div class="mb-6">
 <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">File Servers</h2>
 <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">Configure built-in FTP, SFTP, TFTP, and WebDAV servers.</p>
 </div>

 <!-- FTP Server -->
 <div class="mb-6 p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
 <div class="flex items-center justify-between mb-4">
 <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200">FTP Server</h3>
 <label class="flex items-center gap-2 cursor-pointer">
 <input type="checkbox" bind:checked={ftpServeEnabled}
 class="w-4 h-4 rounded border-slate-300 text-accent-700 focus:ring-accent-500" />
 <span class="text-xs font-medium text-slate-600 dark:text-slate-300">Enabled</span>
 </label>
 </div>

 {#if ftpServeEnabled}
 <div class="grid grid-cols-2 gap-4">
 <div>
 <label for="ftp-port" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Port</label>
 <input id="ftp-port" type="number" bind:value={ftpServePort} placeholder="2121"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="ftp-host" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Host</label>
 <input id="ftp-host" type="text" bind:value={ftpServeHost} placeholder="0.0.0.0 (all interfaces)"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="ftp-public-ip" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Public IP</label>
 <input id="ftp-public-ip" type="text" bind:value={ftpServePublicIP} placeholder="(for passive mode)"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="ftp-passive-ports" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Passive Ports</label>
 <input id="ftp-passive-ports" type="text" bind:value={ftpServePassivePorts} placeholder="30000-30100"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div class="col-span-2">
 <label for="ftp-tls-mode" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">TLS Mode</label>
 <select id="ftp-tls-mode"
 value={ftpServeTLSRequired}
 onchange={(e) => { ftpServeTLSRequired = Number((e.target as HTMLSelectElement).value); }}
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 bg-white dark:bg-warm-900">
 <option value={0}>Disabled (plain FTP)</option>
 <option value={1}>Explicit FTPS (AUTH TLS)</option>
 <option value={2}>Implicit FTPS</option>
 </select>
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">
 Explicit FTPS: clients connect in plain text and upgrade via AUTH TLS command.
 Implicit FTPS: entire connection is TLS from the start (typically port 990).
 </p>
 </div>
 {#if ftpServeTLSRequired > 0}
 <div class="col-span-2">
 <div class="flex items-center gap-3 mb-3">
 <span class="text-xs font-medium text-slate-500 dark:text-slate-400">TLS Certificate & Key</span>
 <div class="flex items-center border border-slate-200 dark:border-warm-700 rounded-md overflow-hidden">
 <button
 type="button"
 class="px-2.5 py-1 text-[11px] font-medium transition-colors {ftpServeTLSInputMode === 'path' ? 'bg-slate-800 text-white' : 'bg-white dark:bg-warm-900 text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200'} cursor-pointer"
 onclick={() => { ftpServeTLSInputMode = 'path'; }}
 >File Path</button>
 <button
 type="button"
 class="px-2.5 py-1 text-[11px] font-medium transition-colors {ftpServeTLSInputMode === 'paste' ? 'bg-slate-800 text-white' : 'bg-white dark:bg-warm-900 text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200'} cursor-pointer"
 onclick={() => { ftpServeTLSInputMode = 'paste'; }}
 >Paste PEM</button>
 </div>
 </div>

 {#if ftpServeTLSInputMode === 'path'}
 <div class="grid grid-cols-2 gap-4">
 <div>
 <label for="ftp-tls-cert" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">TLS Certificate Path</label>
 <input id="ftp-tls-cert" type="text" bind:value={ftpServeTLSCertFile} placeholder="/path/to/cert.pem"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="ftp-tls-key" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">TLS Key Path</label>
 <input id="ftp-tls-key" type="text" bind:value={ftpServeTLSKeyFile} placeholder="/path/to/key.pem"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 {:else}
 <div class="flex justify-end mb-2">
 <button
 class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-accent-700 bg-accent-50 border border-accent-200 rounded-md hover:bg-accent-100 transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
 onclick={handleGenerateTLS}
 disabled={isGeneratingTLS}
 >
 <RotateCw size={12} class={isGeneratingTLS ? 'animate-spin' : ''} />
 {isGeneratingTLS ? 'Generating...' : 'Generate Self-Signed'}
 </button>
 </div>
 <div class="grid grid-cols-2 gap-4">
 <div>
 <label for="ftp-tls-cert-pem" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">TLS Certificate (PEM)</label>
 <textarea id="ftp-tls-cert-pem" bind:value={ftpServeTLSCertPEM} placeholder="-----BEGIN CERTIFICATE-----&#10;MIIBxTCCAWugAwIBAgIU...&#10;-----END CERTIFICATE-----" rows="6"
 class="w-full px-3 py-2 text-xs font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 resize-y"></textarea>
 {#if ftpServeTLSCertPEM && !ftpServeTLSCertPEM.includes('BEGIN CERTIFICATE')}
 <p class="mt-1 text-[11px] text-red-600">
 This does not look like a certificate. Expected a PEM block starting with <code class="font-mono">-----BEGIN CERTIFICATE-----</code>.
 {#if ftpServeTLSCertPEM.includes('PUBLIC KEY')}You may have pasted a public key by mistake.{/if}
 </p>
 {/if}
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Paste the X.509 certificate (the contents of your <code class="font-mono">cert.pem</code>). This is not the public key.</p>
 </div>
 <div>
 <label for="ftp-tls-key-pem" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">TLS Private Key (PEM)</label>
 <textarea id="ftp-tls-key-pem" bind:value={ftpServeTLSKeyPEM} placeholder="-----BEGIN PRIVATE KEY-----&#10;MIGHAgEAMBMGByqGSM49...&#10;-----END PRIVATE KEY-----" rows="6"
 class="w-full px-3 py-2 text-xs font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 resize-y"></textarea>
 {#if ftpServeTLSKeyPEM && !ftpServeTLSKeyPEM.includes('PRIVATE KEY')}
 <p class="mt-1 text-[11px] text-red-600">
 This does not look like a private key. Expected a PEM block starting with <code class="font-mono">-----BEGIN PRIVATE KEY-----</code> (or <code class="font-mono">RSA PRIVATE KEY</code> / <code class="font-mono">EC PRIVATE KEY</code>).
 </p>
 {/if}
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Paste the private key (the contents of your <code class="font-mono">key.pem</code>). Do not paste the public key.</p>
 </div>
 </div>
 {/if}

 <p class="mt-2 text-[11px] text-slate-400 dark:text-slate-500">
 {#if ftpServeTLSInputMode === 'path'}
 PEM-encoded TLS certificate and private key files on the server filesystem.
 Generate a self-signed pair with:
 <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded text-[10px] font-mono">openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -keyout key.pem -out cert.pem -days 3650 -nodes</code>
 {:else}
 Paste the PEM content directly, or click <strong>Generate Self-Signed</strong> to create a new ECDSA P-256 certificate (valid 10 years). Both certificate and key are stored in the database.
 {/if}
 </p>
 </div>
 {/if}
 </div>
 {/if}
 </div>

 <!-- SFTP Server -->
 <div class="mb-6 p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
 <div class="flex items-center justify-between mb-4">
 <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200">SFTP Server</h3>
 <label class="flex items-center gap-2 cursor-pointer">
 <input type="checkbox" bind:checked={sftpServeEnabled}
 class="w-4 h-4 rounded border-slate-300 text-accent-700 focus:ring-accent-500" />
 <span class="text-xs font-medium text-slate-600 dark:text-slate-300">Enabled</span>
 </label>
 </div>

 {#if sftpServeEnabled}
 <div class="grid grid-cols-2 gap-4">
 <div>
 <label for="sftp-port" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Port</label>
 <input id="sftp-port" type="number" bind:value={sftpServePort} placeholder="2222"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="sftp-host" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Host</label>
 <input id="sftp-host" type="text" bind:value={sftpServeHost} placeholder="0.0.0.0 (all interfaces)"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div class="col-span-2">
 <div class="flex items-center gap-3 mb-3">
 <span class="text-xs font-medium text-slate-500 dark:text-slate-400">Host Key</span>
 <div class="flex items-center border border-slate-200 dark:border-warm-700 rounded-md overflow-hidden">
 <button
 type="button"
 class="px-2.5 py-1 text-[11px] font-medium transition-colors {sftpServeKeyInputMode === 'path' ? 'bg-slate-800 text-white' : 'bg-white dark:bg-warm-900 text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200'} cursor-pointer"
 onclick={() => { sftpServeKeyInputMode = 'path'; }}
 >File Path</button>
 <button
 type="button"
 class="px-2.5 py-1 text-[11px] font-medium transition-colors {sftpServeKeyInputMode === 'paste' ? 'bg-slate-800 text-white' : 'bg-white dark:bg-warm-900 text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200'} cursor-pointer"
 onclick={() => { sftpServeKeyInputMode = 'paste'; }}
 >Paste PEM</button>
 </div>
 </div>

 {#if sftpServeKeyInputMode === 'path'}
 <label for="sftp-host-key" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Host Key Path</label>
 <input id="sftp-host-key" type="text" bind:value={sftpServeHostKeyPath} placeholder="(auto-generated if empty)"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 {:else}
 <div class="flex items-center justify-between mb-1.5">
 <label for="sftp-host-key-pem" class="block text-xs font-medium text-slate-500 dark:text-slate-400">Host Key (PEM)</label>
 <button
 class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-accent-700 bg-accent-50 border border-accent-200 rounded-md hover:bg-accent-100 transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
 onclick={handleGenerateSSHKey}
 disabled={isGeneratingSSHKey}
 >
 <RotateCw size={12} class={isGeneratingSSHKey ? 'animate-spin' : ''} />
 {isGeneratingSSHKey ? 'Generating...' : 'Generate Ed25519 Key'}
 </button>
 </div>
 <textarea id="sftp-host-key-pem" bind:value={sftpServeHostKeyPEM} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;b3BlbnNzaC1rZXktdjEAAAA...&#10;-----END OPENSSH PRIVATE KEY-----" rows="6"
 class="w-full px-3 py-2 text-xs font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 resize-y"></textarea>
 {#if sftpServeHostKeyPEM && !sftpServeHostKeyPEM.includes('PRIVATE KEY')}
 <p class="mt-1 text-[11px] text-red-600">
 This does not look like a private key. Expected a PEM block starting with <code class="font-mono">-----BEGIN OPENSSH PRIVATE KEY-----</code> or <code class="font-mono">-----BEGIN PRIVATE KEY-----</code>.
 {#if sftpServeHostKeyPEM.includes('PUBLIC KEY')}You may have pasted a public key by mistake. Paste the private key instead.{/if}
 </p>
 {/if}
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">Paste the SSH private key content directly, or click <strong>Generate Ed25519 Key</strong> to create one. Stored in the database.</p>
 {/if}

 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">
 {#if sftpServeKeyInputMode === 'path'}
 Path to the server's SSH private key file (PEM format). This key identifies the server to connecting clients.
 Leave empty to auto-generate an Ed25519 key. The generated key is automatically saved to the database and reused across restarts.
 {:else}
 The host key must be a private key (e.g. <code class="font-mono">BEGIN OPENSSH PRIVATE KEY</code> or <code class="font-mono">BEGIN PRIVATE KEY</code>), not a public key.
 {/if}
 Supported key types: Ed25519, RSA, ECDSA.
 Generate one with: <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded text-[10px] font-mono">ssh-keygen -t ed25519 -f /path/to/host_key -N ""</code>.
 </p>
 </div>
 </div>
 {/if}
 </div>

 <!-- TFTP Server -->
 <div class="mb-6 p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
 <div class="flex items-center justify-between mb-4">
 <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200">TFTP Server</h3>
 <label class="flex items-center gap-2 cursor-pointer">
 <input type="checkbox" bind:checked={tftpServeEnabled}
 class="w-4 h-4 rounded border-slate-300 text-accent-700 focus:ring-accent-500" />
 <span class="text-xs font-medium text-slate-600 dark:text-slate-300">Enabled</span>
 </label>
 </div>

 {#if tftpServeEnabled}
 <div class="grid grid-cols-2 gap-4">
 <div>
 <label for="tftp-port" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Port</label>
 <input id="tftp-port" type="number" bind:value={tftpServePort} placeholder="69"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="tftp-host" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Host</label>
 <input id="tftp-host" type="text" bind:value={tftpServeHost} placeholder="0.0.0.0 (all interfaces)"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 {/if}
 </div>

 <!-- WebDAV Server -->
 <div class="mb-6 p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
 <div class="flex items-center justify-between mb-4">
 <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200">WebDAV Server</h3>
 <label class="flex items-center gap-2 cursor-pointer">
 <input type="checkbox" bind:checked={webdavServeEnabled}
 class="w-4 h-4 rounded border-slate-300 text-accent-700 focus:ring-accent-500" />
 <span class="text-xs font-medium text-slate-600 dark:text-slate-300">Enabled</span>
 </label>
 </div>

 {#if webdavServeEnabled}
 <div class="grid grid-cols-2 gap-4">
 <div>
 <label for="webdav-serve-port" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Port</label>
 <input id="webdav-serve-port" type="number" bind:value={webdavServePort} placeholder="9119"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="webdav-serve-host" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">Host</label>
 <input id="webdav-serve-host" type="text" bind:value={webdavServeHost} placeholder="0.0.0.0 (all interfaces)"
 class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <label for="webdav-serve-prefix" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">URL Prefix</label>
 <input id="webdav-serve-prefix" type="text" bind:value={webdavServePrefix} placeholder="/"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">URL path prefix for WebDAV requests (default: /)</p>
 </div>
 </div>
 <p class="mt-3 text-[11px] text-slate-400 dark:text-slate-500">
 WebDAV clients authenticate using the same user credentials as the other built-in file servers (HTTP Basic Auth). Shares and access control are shared across all file servers.
 Connect with any WebDAV client (macOS Finder, Windows Explorer, Cyberduck, etc.).
 </p>
 {/if}
 </div>

 <!-- Save button -->
 <div class="flex justify-end">
 <button
 class="px-4 py-2 bg-accent-600 text-white text-sm font-medium rounded-md hover:bg-accent-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
 onclick={handleSaveServers}
 disabled={isSavingServers}
 >
 {isSavingServers ? 'Saving...' : 'Save Server Settings'}
 </button>
 </div>
</div>
