<script lang="ts">
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { onMount } from "svelte";
  import { Plus, Trash2, Copy, Eye, EyeOff, Shield, Globe, Key, RotateCw, Lock, Download, Upload, HardDrive, ShieldAlert, FolderOpen, Share2, Server } from "lucide-svelte";
  import type { TokenScope, CreateTokenRequest, ExternalResource, RawMountEntry, FTPShareEntry, FTPUserEntry, FTPServeSettings, SFTPServeSettings, TFTPServeSettings } from "@/lib/types/config";
  import { appStore } from "@/lib/store/store.svelte";
  import axios from 'axios';

  // ── Tab state ──
  let activeSection = $state<'tokens' | 'external' | 'raw_mounts' | 'ftp_shares' | 'file_servers' | 'rotation' | 'security' | 'backup'>('tokens');

  // ── Raw mounts state ──
  let rawMounts = $state<RawMountEntry[]>([]);
  let showAddMount = $state(false);
  let newMountPrefix = $state('');
  let newMountType = $state<'local' | 's3' | 'ftp' | 'sftp'>('local');
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
  // Edit state
  let editingIndex = $state<number | null>(null);
  let isSavingMounts = $state(false);

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
  let isSavingServers = $state(false);

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

  // ── Rotation state ──
  let rotationAdminSecret = $state('');
  let rotationNewKey = $state('');
  let isRotating = $state(false);
  let showRotationAdminSecret = $state(false);
  let showNewKey = $state(false);

  // ── Admin secret state ──
  let adminSecretConfigured = $state(false);
  let currentAdminSecret = $state('');
  let newAdminSecret = $state('');
  let confirmAdminSecret = $state('');
  let isSavingAdminSecret = $state(false);
  let showCurrentAdminSecret = $state(false);
  let showNewAdminSecret = $state(false);
  let showConfirmAdminSecret = $state(false);

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

  // ── Token state ──
  let showCreateToken = $state(false);
  let newTokenName = $state('');
  let newTokenScopes = $state<TokenScope[]>([{ path: '**', operations: ['read'] }]);
  let newTokenExpiry = $state('');
  let createdTokenKey = $state<string | null>(null);
  let showKey = $state(false);

  // ── External resource state ──
  let showAddExternal = $state(false);
  let newExtName = $state('');
  let newExtType = $state<'http' | 'vault' | 'kubernetes'>('http');
  let newExtHttpUrl = $state('');
  let newExtVaultAddr = $state('');
  let newExtVaultMount = $state('secret');
  let newExtVaultRoleId = $state('');
  let newExtVaultSecretId = $state('');
  let newExtVaultAppRolePath = $state('approle');
  let newExtK8sKubeconfig = $state('');

  const tokens = $derived(configStore.tokens);
  const settings = $derived(configStore.settings);
  const externalResources = $derived(
    settings?.external ? Object.entries(settings.external) : []
  );

  onMount(() => {
    configStore.loadSettings().then(() => {
      // Initialize raw mounts from loaded settings
      rawMounts = [...(configStore.settings?.raw_mounts || [])];
    });
    configStore.loadTokens();
    loadAdminSecretStatus();
  });

  async function loadAdminSecretStatus() {
    const status = await configStore.fetchAdminSecretStatus();
    adminSecretConfigured = status.configured;
  }

  // ── Token handlers ──
  function addScope() {
    newTokenScopes = [...newTokenScopes, { path: '', operations: ['read'] }];
  }

  function removeScope(index: number) {
    newTokenScopes = newTokenScopes.filter((_, i) => i !== index);
  }

  function toggleOperation(index: number, op: string) {
    const scope = newTokenScopes[index];
    if (scope.operations.includes(op)) {
      scope.operations = scope.operations.filter(o => o !== op);
    } else {
      scope.operations = [...scope.operations, op];
    }
    newTokenScopes = [...newTokenScopes];
  }

  async function handleCreateToken() {
    if (!newTokenName.trim()) {
      addToast('Token name is required', 'alert');
      return;
    }
    if (newTokenScopes.some(s => !s.path.trim())) {
      addToast('All scope paths are required', 'alert');
      return;
    }

    try {
      const req: CreateTokenRequest = {
        name: newTokenName.trim(),
        scopes: newTokenScopes,
      };
      if (newTokenExpiry) {
        req.expires_at = new Date(newTokenExpiry).toISOString();
      }

      const result = await configStore.createToken(req);
      createdTokenKey = result.raw_key;
      showCreateToken = false;
      newTokenName = '';
      newTokenScopes = [{ path: '**', operations: ['read'] }];
      newTokenExpiry = '';
      addToast('Token created successfully', 'success');
    } catch (error) {
      console.error('Failed to create token:', error);
      addToast('Failed to create token', 'alert');
    }
  }

  async function handleDeleteToken(id: string) {
    if (!confirm('Are you sure you want to delete this token?')) return;
    try {
      await configStore.deleteToken(id);
    } catch (error) {
      addToast('Failed to delete token', 'alert');
    }
  }

  async function handleToggleToken(id: string, active: boolean) {
    try {
      await configStore.patchToken(id, { active: !active });
    } catch (error) {
      addToast('Failed to update token', 'alert');
    }
  }

  async function copyTokenKey() {
    if (createdTokenKey) {
      await navigator.clipboard.writeText(createdTokenKey);
      addToast('Token copied to clipboard', 'success');
    }
  }

  function dismissTokenKey() {
    createdTokenKey = null;
  }

  // ── External resource handlers ──
  async function handleAddExternal() {
    if (!newExtName.trim()) {
      addToast('Resource name is required', 'alert');
      return;
    }

    const resource: ExternalResource = {} as ExternalResource;
    if (newExtType === 'http') {
      if (!newExtHttpUrl.trim()) {
        addToast('HTTP URL is required', 'alert');
        return;
      }
      resource.http = { base_url: newExtHttpUrl.trim() };
    } else if (newExtType === 'vault') {
      if (!newExtVaultAddr.trim() || !newExtVaultMount.trim()) {
        addToast('Vault address and mount are required', 'alert');
        return;
      }
      if (!newExtVaultRoleId.trim() || !newExtVaultSecretId.trim()) {
        addToast('AppRole Role ID and Secret ID are required', 'alert');
        return;
      }
      resource.vault = {
        address: newExtVaultAddr.trim(),
        mount: newExtVaultMount.trim(),
        app_role: {
          role_id: newExtVaultRoleId.trim(),
          secret_id: newExtVaultSecretId.trim(),
          app_role_base_path: newExtVaultAppRolePath.trim() || 'approle'
        }
      };
    } else if (newExtType === 'kubernetes') {
      resource.kubernetes = {
        kubeconfig: newExtK8sKubeconfig.trim() || undefined
      };
    }

    try {
      const currentExternal = settings?.external || {};
      await configStore.saveSettings({
        external: { ...currentExternal, [newExtName.trim()]: resource }
      });
      showAddExternal = false;
      newExtName = '';
      newExtHttpUrl = '';
      newExtVaultAddr = '';
      newExtVaultMount = 'secret';
      newExtVaultRoleId = '';
      newExtVaultSecretId = '';
      newExtVaultAppRolePath = 'approle';
      newExtK8sKubeconfig = '';
    } catch (error) {
      addToast('Failed to add external resource', 'alert');
    }
  }

  async function handleRemoveExternal(name: string) {
    if (!confirm(`Remove external resource "${name}"?`)) return;
    try {
      const currentExternal = { ...(settings?.external || {}) };
      delete currentExternal[name];
      await configStore.saveSettings({ external: currentExternal });
    } catch (error) {
      addToast('Failed to remove external resource', 'alert');
    }
  }

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
    if (!confirm(`Remove FTP share "${share.name}"?`)) return;

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

  const availableMounts = $derived(appStore.info?.raw_mounts ?? []);

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
    if (!confirm(`Remove FTP/SFTP user "${user.username}"?`)) return;

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
        sshStr(none),          // cipher
        sshStr(none),          // kdf
        sshStr(new Uint8Array(0)), // kdf options
        u32(1),                // number of keys
        sshStr(pubBlob),       // public key
        sshStr(privSection),   // private section
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

  async function handleSetAdminSecret() {
    if (!newAdminSecret.trim()) {
      addToast('New secret is required', 'alert');
      return;
    }
    if (newAdminSecret !== confirmAdminSecret) {
      addToast('Secrets do not match', 'alert');
      return;
    }
    if (adminSecretConfigured && !currentAdminSecret.trim()) {
      addToast('Current secret is required', 'alert');
      return;
    }

    isSavingAdminSecret = true;
    try {
      await configStore.setAdminSecret(currentAdminSecret.trim(), newAdminSecret.trim());
      addToast(adminSecretConfigured ? 'Admin secret updated' : 'Admin secret set', 'success');
      adminSecretConfigured = true;
      currentAdminSecret = '';
      newAdminSecret = '';
      confirmAdminSecret = '';
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to set admin secret';
      addToast(msg, 'alert');
    } finally {
      isSavingAdminSecret = false;
    }
  }

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

  function formatDate(dateStr: string): string {
    return new Date(dateStr).toLocaleDateString(undefined, {
      year: 'numeric', month: 'short', day: 'numeric',
      hour: '2-digit', minute: '2-digit'
    });
  }
</script>

<div class="flex h-full overflow-hidden">
  <!-- Left Sidebar -->
  <div class="w-52 shrink-0 bg-slate-50 border-r border-slate-200 overflow-y-auto">
    <div class="px-3 py-3">
      <span class="text-[10px] font-semibold text-slate-400 uppercase tracking-wider px-2">Settings</span>
    </div>
    <nav class="flex flex-col gap-0.5 px-2 pb-4">
      <button
        class="flex items-center gap-2.5 w-full px-3 py-2 text-[13px] font-medium rounded-md cursor-pointer transition-colors text-left
          {activeSection === 'tokens' ? 'bg-blue-50 text-blue-700 border border-blue-200' : 'bg-transparent text-slate-600 border border-transparent hover:bg-slate-100 hover:text-slate-800'}"
        onclick={() => activeSection = 'tokens'}
      >
        <Key size={15} class="shrink-0" />
        Access Tokens
      </button>
      <button
        class="flex items-center gap-2.5 w-full px-3 py-2 text-[13px] font-medium rounded-md cursor-pointer transition-colors text-left
          {activeSection === 'external' ? 'bg-blue-50 text-blue-700 border border-blue-200' : 'bg-transparent text-slate-600 border border-transparent hover:bg-slate-100 hover:text-slate-800'}"
        onclick={() => activeSection = 'external'}
      >
        <Globe size={15} class="shrink-0" />
        External Resources
      </button>
      <button
        class="flex items-center gap-2.5 w-full px-3 py-2 text-[13px] font-medium rounded-md cursor-pointer transition-colors text-left
          {activeSection === 'raw_mounts' ? 'bg-blue-50 text-blue-700 border border-blue-200' : 'bg-transparent text-slate-600 border border-transparent hover:bg-slate-100 hover:text-slate-800'}"
        onclick={() => { activeSection = 'raw_mounts'; rawMounts = [...(configStore.settings?.raw_mounts || [])]; }}
      >
        <FolderOpen size={15} class="shrink-0" />
        Raw Mounts
      </button>
      <button
        class="flex items-center gap-2.5 w-full px-3 py-2 text-[13px] font-medium rounded-md cursor-pointer transition-colors text-left
          {activeSection === 'ftp_shares' ? 'bg-blue-50 text-blue-700 border border-blue-200' : 'bg-transparent text-slate-600 border border-transparent hover:bg-slate-100 hover:text-slate-800'}"
        onclick={() => { activeSection = 'ftp_shares'; ftpShares = [...(configStore.settings?.ftp_shares || [])]; ftpUsers = [...(configStore.settings?.ftp_users || [])]; }}
      >
        <Share2 size={15} class="shrink-0" />
        File Sharing
      </button>
      <button
        class="flex items-center gap-2.5 w-full px-3 py-2 text-[13px] font-medium rounded-md cursor-pointer transition-colors text-left
          {activeSection === 'file_servers' ? 'bg-blue-50 text-blue-700 border border-blue-200' : 'bg-transparent text-slate-600 border border-transparent hover:bg-slate-100 hover:text-slate-800'}"
        onclick={() => { activeSection = 'file_servers'; loadServeSettings(); }}
      >
        <Server size={15} class="shrink-0" />
        File Servers
      </button>
      <button
        class="flex items-center gap-2.5 w-full px-3 py-2 text-[13px] font-medium rounded-md cursor-pointer transition-colors text-left
          {activeSection === 'rotation' ? 'bg-blue-50 text-blue-700 border border-blue-200' : 'bg-transparent text-slate-600 border border-transparent hover:bg-slate-100 hover:text-slate-800'}"
        onclick={() => activeSection = 'rotation'}
      >
        <RotateCw size={15} class="shrink-0" />
        Key Rotation
      </button>
      <button
        class="flex items-center gap-2.5 w-full px-3 py-2 text-[13px] font-medium rounded-md cursor-pointer transition-colors text-left
          {activeSection === 'security' ? 'bg-blue-50 text-blue-700 border border-blue-200' : 'bg-transparent text-slate-600 border border-transparent hover:bg-slate-100 hover:text-slate-800'}"
        onclick={() => activeSection = 'security'}
      >
        <Lock size={15} class="shrink-0" />
        Security
      </button>
      <button
        class="flex items-center gap-2.5 w-full px-3 py-2 text-[13px] font-medium rounded-md cursor-pointer transition-colors text-left
          {activeSection === 'backup' ? 'bg-blue-50 text-blue-700 border border-blue-200' : 'bg-transparent text-slate-600 border border-transparent hover:bg-slate-100 hover:text-slate-800'}"
        onclick={() => activeSection = 'backup'}
      >
        <HardDrive size={15} class="shrink-0" />
        Backup
      </button>
    </nav>
  </div>

  <!-- Right Content Area -->
  <div class="flex-1 overflow-y-auto">
  <div class="max-w-3xl p-6">

  <!-- ══════════════════════════════════════════ -->
  <!-- Token Created Banner -->
  <!-- ══════════════════════════════════════════ -->
  {#if createdTokenKey}
    <div class="mb-6 p-4 bg-green-50 border border-green-200 rounded-lg">
      <p class="text-sm font-semibold text-green-800 mb-2">Token Created Successfully</p>
      <p class="text-xs text-green-700 mb-3">Copy this token now. It will not be shown again.</p>
      <div class="flex items-center gap-2">
        <code class="flex-1 px-3 py-2 bg-white border border-green-200 rounded text-xs font-mono text-green-900 overflow-hidden text-ellipsis">
          {showKey ? createdTokenKey : '••••••••••••••••••••••••••••••••'}
        </code>
        <button
          class="p-2 bg-white border border-green-200 rounded hover:bg-green-100 transition-colors"
          onclick={() => showKey = !showKey}
          title={showKey ? 'Hide' : 'Show'}
        >
          {#if showKey}<EyeOff size={14} />{:else}<Eye size={14} />{/if}
        </button>
        <button
          class="p-2 bg-white border border-green-200 rounded hover:bg-green-100 transition-colors"
          onclick={copyTokenKey}
          title="Copy"
        >
          <Copy size={14} />
        </button>
      </div>
      <button
        class="mt-3 px-3 py-1.5 text-xs text-green-700 bg-transparent border border-green-300 rounded hover:bg-green-100 transition-colors"
        onclick={dismissTokenKey}
      >
        Dismiss
      </button>
    </div>
  {/if}

  <!-- ══════════════════════════════════════════ -->
  <!-- Access Tokens Section -->
  <!-- ══════════════════════════════════════════ -->
  {#if activeSection === 'tokens'}
    <div>
      <div class="flex items-center justify-between mb-4">
        <div>
          <h2 class="text-lg font-semibold text-slate-800">Access Tokens</h2>
          <p class="text-sm text-slate-500 mt-0.5">Tokens authenticate consumers accessing configs via the data API</p>
        </div>
        <button
          class="flex items-center gap-1.5 px-3 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 transition-colors"
          onclick={() => showCreateToken = true}
        >
          <Plus size={14} />
          New Token
        </button>
      </div>

      <!-- Create Token Form -->
      {#if showCreateToken}
        <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
          <h3 class="text-sm font-semibold text-slate-700 mb-4">Create New Token</h3>

          <div class="mb-4">
            <label for="token-name" class="block text-xs font-medium text-slate-500 mb-1.5">Name</label>
            <input
              id="token-name"
              type="text"
              bind:value={newTokenName}
              placeholder="e.g., production-reader"
              class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
          </div>

          <div class="mb-4">
            <label for="token-expiry" class="block text-xs font-medium text-slate-500 mb-1.5">Expires At (optional)</label>
            <input
              id="token-expiry"
              type="datetime-local"
              bind:value={newTokenExpiry}
              class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
          </div>

          <div class="mb-4">
            <div class="flex items-center justify-between mb-2">
              <span class="block text-xs font-medium text-slate-500">Scopes</span>
              <button
                class="flex items-center gap-1 px-2 py-1 text-xs text-blue-600 bg-blue-50 rounded hover:bg-blue-100 transition-colors"
                onclick={addScope}
              >
                <Plus size={12} /> Add Scope
              </button>
            </div>

            {#each newTokenScopes as scope, i (i)}
              <div class="flex items-start gap-2 mb-2 p-3 bg-slate-50 rounded-md border border-slate-100">
                <div class="flex-1">
                  <input
                    type="text"
                    bind:value={scope.path}
                    placeholder="Path pattern (e.g., app/**, production/*)"
                    class="w-full px-2.5 py-1.5 text-xs font-mono border border-slate-200 rounded focus:outline-none focus:border-blue-500"
                  />
                  <div class="flex gap-2 mt-2">
                    {#each ['read', 'write', 'delete'] as op}
                      <label class="flex items-center gap-1 text-xs text-slate-600 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={scope.operations.includes(op)}
                          onchange={() => toggleOperation(i, op)}
                          class="rounded border-slate-300"
                        />
                        {op}
                      </label>
                    {/each}
                  </div>
                </div>
                {#if newTokenScopes.length > 1}
                  <button
                    class="p-1 text-slate-400 hover:text-red-500 transition-colors"
                    onclick={() => removeScope(i)}
                  >
                    <Trash2 size={14} />
                  </button>
                {/if}
              </div>
            {/each}
          </div>

          <div class="flex justify-end gap-2">
            <button
              class="px-3 py-2 text-sm text-slate-600 bg-white border border-slate-200 rounded-md hover:bg-slate-50 transition-colors"
              onclick={() => showCreateToken = false}
            >
              Cancel
            </button>
            <button
              class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
              onclick={handleCreateToken}
            >
              Create Token
            </button>
          </div>
        </div>
      {/if}

      <!-- Token List -->
      {#if tokens.length === 0}
        <div class="text-center py-12 bg-white border border-slate-200 rounded-lg">
          <Shield size={32} class="mx-auto text-slate-300 mb-3" />
          <p class="text-sm text-slate-500">No access tokens yet</p>
          <p class="text-xs text-slate-400 mt-1">Create a token to allow consumers to access configurations</p>
        </div>
      {:else}
        <div class="space-y-2">
          {#each tokens as token (token.id)}
            <div class="flex items-center gap-4 p-4 bg-white border border-slate-200 rounded-lg hover:border-slate-300 transition-colors">
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2">
                  <span class="text-sm font-medium text-slate-800">{token.name}</span>
                  <span class="px-1.5 py-0.5 text-[10px] font-medium rounded
                    {token.active ? 'bg-green-100 text-green-700' : 'bg-slate-100 text-slate-500'}">
                    {token.active ? 'Active' : 'Disabled'}
                  </span>
                </div>
                <div class="flex gap-3 mt-1 flex-wrap">
                  <span class="text-xs text-slate-400">Created: {formatDate(token.created_at)}</span>
                  {#if token.created_by}
                    <span class="text-xs text-slate-400">by: <span class="text-slate-600">{token.created_by}</span></span>
                  {/if}
                  {#if token.expires_at}
                    <span class="text-xs text-slate-400">Expires: {formatDate(token.expires_at)}</span>
                  {/if}
                </div>
                <div class="flex flex-wrap gap-1.5 mt-2">
                  {#each token.scopes as scope}
                    <span class="px-2 py-0.5 text-[10px] font-mono bg-slate-100 text-slate-600 rounded">
                      {scope.operations.join(',')}:{scope.path}
                    </span>
                  {/each}
                </div>
              </div>
              <div class="flex items-center gap-1 shrink-0">
                <button
                  class="px-2.5 py-1.5 text-xs rounded transition-colors
                    {token.active ? 'text-amber-600 bg-amber-50 hover:bg-amber-100' : 'text-green-600 bg-green-50 hover:bg-green-100'}"
                  onclick={() => handleToggleToken(token.id, token.active)}
                >
                  {token.active ? 'Disable' : 'Enable'}
                </button>
                <button
                  class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors"
                  onclick={() => handleDeleteToken(token.id)}
                  title="Delete token"
                >
                  <Trash2 size={14} />
                </button>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- ══════════════════════════════════════════ -->
  <!-- External Resources Section -->
  <!-- ══════════════════════════════════════════ -->
  {#if activeSection === 'external'}
    <div>
      <div class="flex items-center justify-between mb-4">
        <div>
          <h2 class="text-lg font-semibold text-slate-800">External Resources</h2>
          <p class="text-sm text-slate-500 mt-0.5">Configure external sources for configuration inheritance</p>
        </div>
        <button
          class="flex items-center gap-1.5 px-3 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 transition-colors"
          onclick={() => showAddExternal = true}
        >
          <Plus size={14} />
          Add Resource
        </button>
      </div>

      <!-- Add External Form -->
      {#if showAddExternal}
        <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
          <h3 class="text-sm font-semibold text-slate-700 mb-4">Add External Resource</h3>

          <div class="mb-4">
            <label for="ext-name" class="block text-xs font-medium text-slate-500 mb-1.5">Resource Name</label>
            <input
              id="ext-name"
              type="text"
              bind:value={newExtName}
              placeholder="e.g., shared-config"
              class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
          </div>

          <div class="mb-4">
            <span class="block text-xs font-medium text-slate-500 mb-1.5">Type</span>
            <div class="flex gap-3">
              <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
                <input type="radio" bind:group={newExtType} value="http" class="text-blue-500" />
                HTTP
              </label>
              <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
                <input type="radio" bind:group={newExtType} value="vault" class="text-blue-500" />
                Vault
              </label>
              <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
                <input type="radio" bind:group={newExtType} value="kubernetes" class="text-blue-500" />
                Kubernetes
              </label>
            </div>
          </div>

          {#if newExtType === 'http'}
            <div class="mb-4">
              <label for="ext-url" class="block text-xs font-medium text-slate-500 mb-1.5">Base URL</label>
              <input
                id="ext-url"
                type="url"
                bind:value={newExtHttpUrl}
                placeholder="https://config-server.example.com/api/config"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
            </div>
          {:else if newExtType === 'vault'}
            <div class="mb-4">
              <label for="ext-vault-addr" class="block text-xs font-medium text-slate-500 mb-1.5">Vault Address</label>
              <input
                id="ext-vault-addr"
                type="url"
                bind:value={newExtVaultAddr}
                placeholder="https://vault.example.com"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
            </div>
            <div class="mb-4">
              <label for="ext-vault-mount" class="block text-xs font-medium text-slate-500 mb-1.5">Mount</label>
              <input
                id="ext-vault-mount"
                type="text"
                bind:value={newExtVaultMount}
                placeholder="secret"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
              <p class="mt-1 text-[11px] text-slate-400">KV secrets engine mount path. Secret paths are specified per-inheritance entry.</p>
            </div>

            <div class="mb-3 pt-2 border-t border-slate-100">
              <p class="text-xs font-medium text-slate-500 mb-2">AppRole Authentication</p>
            </div>

            <div class="mb-4">
              <label for="ext-vault-role-id" class="block text-xs font-medium text-slate-500 mb-1.5">Role ID</label>
              <input
                id="ext-vault-role-id"
                type="text"
                bind:value={newExtVaultRoleId}
                placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
                class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
            </div>
            <div class="mb-4">
              <label for="ext-vault-secret-id" class="block text-xs font-medium text-slate-500 mb-1.5">Secret ID</label>
              <input
                id="ext-vault-secret-id"
                type="password"
                bind:value={newExtVaultSecretId}
                placeholder="Secret ID"
                class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
            </div>
            <div class="mb-4">
              <label for="ext-vault-approle-path" class="block text-xs font-medium text-slate-500 mb-1.5">AppRole Mount Path</label>
              <input
                id="ext-vault-approle-path"
                type="text"
                bind:value={newExtVaultAppRolePath}
                placeholder="approle"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
              <p class="mt-1 text-[11px] text-slate-400">Usually "approle" unless using a custom mount</p>
            </div>
          {:else if newExtType === 'kubernetes'}
            <div class="mb-4">
              <label for="ext-k8s-kubeconfig" class="block text-xs font-medium text-slate-500 mb-1.5">Kubeconfig Path (optional)</label>
              <input
                id="ext-k8s-kubeconfig"
                type="text"
                bind:value={newExtK8sKubeconfig}
                placeholder="/path/to/kubeconfig"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
              <p class="mt-1 text-[11px] text-slate-400">Leave empty to use in-cluster config (service account token). Path format: <code class="px-1 py-0.5 bg-slate-100 rounded text-[10px]">namespace/secret/name</code> or <code class="px-1 py-0.5 bg-slate-100 rounded text-[10px]">namespace/configmap/name</code></p>
            </div>
          {/if}

          <div class="flex justify-end gap-2">
            <button
              class="px-3 py-2 text-sm text-slate-600 bg-white border border-slate-200 rounded-md hover:bg-slate-50 transition-colors"
              onclick={() => showAddExternal = false}
            >
              Cancel
            </button>
            <button
              class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
              onclick={handleAddExternal}
            >
              Add Resource
            </button>
          </div>
        </div>
      {/if}

      <!-- Resource List -->
      {#if externalResources.length === 0}
        <div class="text-center py-12 bg-white border border-slate-200 rounded-lg">
          <Globe size={32} class="mx-auto text-slate-300 mb-3" />
          <p class="text-sm text-slate-500">No external resources configured</p>
          <p class="text-xs text-slate-400 mt-1">Add external sources for configuration inheritance</p>
        </div>
      {:else}
        <div class="space-y-2">
          {#each externalResources as [name, resource] (name)}
            <div class="flex items-center gap-4 p-4 bg-white border border-slate-200 rounded-lg hover:border-slate-300 transition-colors">
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2">
                  <span class="text-sm font-medium text-slate-800">{name}</span>
                  <span class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-blue-100 text-blue-700">
                    {resource.http ? 'HTTP' : resource.vault ? 'Vault' : 'Kubernetes'}
                  </span>
                </div>
                <div class="mt-1 space-y-0.5">
                  {#if resource.http}
                    <span class="text-xs font-mono text-slate-400">{resource.http.base_url}</span>
                  {:else if resource.vault}
                    <div class="text-xs font-mono text-slate-400">{resource.vault.address}</div>
                    <div class="text-xs text-slate-400">
                      Mount: <span class="font-mono">{resource.vault.mount}</span>
                    </div>
                    {#if resource.vault.app_role}
                      <div class="flex items-center gap-1.5 text-[10px] text-slate-400">
                        <span class="px-1 py-0.5 bg-slate-100 rounded text-slate-500">AppRole</span>
                        <span class="font-mono">{resource.vault.app_role.app_role_base_path || 'approle'}</span>
                        <span class="text-slate-300">|</span>
                        <span>Role: {resource.vault.app_role.role_id.slice(0, 8)}...</span>
                      </div>
                    {/if}
                  {:else if resource.kubernetes}
                    <div class="text-xs text-slate-400">
                      {resource.kubernetes.kubeconfig
                        ? `Kubeconfig: ${resource.kubernetes.kubeconfig}`
                        : 'In-cluster (service account)'}
                    </div>
                    <div class="text-[10px] text-slate-400">
                      Path format: <code class="px-1 py-0.5 bg-slate-100 rounded">namespace/secret/name</code> or <code class="px-1 py-0.5 bg-slate-100 rounded">namespace/configmap/name</code>
                    </div>
                  {/if}
                </div>
              </div>
              <button
                class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors shrink-0"
                onclick={() => handleRemoveExternal(name)}
                title="Remove resource"
              >
                <Trash2 size={14} />
              </button>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- ══════════════════════════════════════════ -->
  <!-- Raw Mounts Section -->
  <!-- ══════════════════════════════════════════ -->
  {#if activeSection === 'raw_mounts'}
    <div>
      <div class="flex items-center justify-between mb-4">
        <div>
          <h2 class="text-lg font-semibold text-slate-800">Raw Filesystem Mounts</h2>
          <p class="text-sm text-slate-500 mt-0.5">Serve files from local directories at <code class="px-1 py-0.5 bg-slate-100 rounded text-[11px]">/raw/&#123;prefix&#125;/...</code></p>
        </div>
        <button
          class="flex items-center gap-1.5 px-3 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 transition-colors"
          onclick={() => showAddMount = true}
        >
          <Plus size={14} />
          Add Mount
        </button>
      </div>

      <!-- Add Mount Form -->
      {#if showAddMount}
        <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
          <h3 class="text-sm font-semibold text-slate-700 mb-4">{editingIndex !== null ? 'Edit Raw Mount' : 'Add Raw Mount'}</h3>

          <div class="mb-4">
            <label for="mount-prefix" class="block text-xs font-medium text-slate-500 mb-1.5">Prefix</label>
            <input id="mount-prefix" type="text" bind:value={newMountPrefix} placeholder="e.g., configs"
              class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
            <p class="mt-1 text-[11px] text-slate-400">URL prefix — files will be served at <code class="px-1 py-0.5 bg-slate-100 rounded text-[10px]">/raw/{newMountPrefix || 'prefix'}/...</code></p>
          </div>

          <div class="mb-4">
            <span class="block text-xs font-medium text-slate-500 mb-1.5">Backend Type</span>
            <div class="flex gap-3">
              <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
                <input type="radio" bind:group={newMountType} value="local" class="text-blue-500" /> Local Directory
              </label>
              <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
                <input type="radio" bind:group={newMountType} value="s3" class="text-blue-500" /> S3 Compatible
              </label>
              <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
                <input type="radio" bind:group={newMountType} value="ftp" class="text-blue-500" /> FTP / FTPS
              </label>
              <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
                <input type="radio" bind:group={newMountType} value="sftp" class="text-blue-500" /> SFTP (SSH)
              </label>
            </div>
          </div>

          {#if newMountType === 'local'}
            <div class="mb-4">
              <label for="mount-path" class="block text-xs font-medium text-slate-500 mb-1.5">Directory Path</label>
              <input id="mount-path" type="text" bind:value={newMountPath} placeholder="/opt/configs"
                class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              <p class="mt-1 text-[11px] text-slate-400">Absolute path to a directory on the server's filesystem (also works with FUSE mounts)</p>
            </div>

          {:else if newMountType === 's3'}
            <div class="grid grid-cols-2 gap-3 mb-4">
              <div>
                <label for="s3-bucket" class="block text-xs font-medium text-slate-500 mb-1.5">Bucket</label>
                <input id="s3-bucket" type="text" bind:value={newS3Bucket} placeholder="my-bucket"
                  class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
              <div>
                <label for="s3-region" class="block text-xs font-medium text-slate-500 mb-1.5">Region</label>
                <input id="s3-region" type="text" bind:value={newS3Region} placeholder="us-east-1"
                  class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
            </div>
            <div class="mb-4">
              <label for="s3-endpoint" class="block text-xs font-medium text-slate-500 mb-1.5">Endpoint (optional)</label>
              <input id="s3-endpoint" type="text" bind:value={newS3Endpoint} placeholder="s3.amazonaws.com or minio.local:9000"
                class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              <p class="mt-1 text-[11px] text-slate-400">Leave empty for AWS S3. Set for MinIO, Cloudflare R2, etc.</p>
            </div>
            <div class="grid grid-cols-2 gap-3 mb-4">
              <div>
                <label for="s3-access-key" class="block text-xs font-medium text-slate-500 mb-1.5">Access Key</label>
                <input id="s3-access-key" type="text" bind:value={newS3AccessKey} placeholder="AKIA..."
                  class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
              <div>
                <label for="s3-secret-key" class="block text-xs font-medium text-slate-500 mb-1.5">Secret Key</label>
                <input id="s3-secret-key" type="password" bind:value={newS3SecretKey} placeholder="Secret key"
                  class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
            </div>
            <div class="mb-4">
              <label for="s3-prefix" class="block text-xs font-medium text-slate-500 mb-1.5">Key Prefix (optional)</label>
              <input id="s3-prefix" type="text" bind:value={newS3Prefix} placeholder="configs/"
                class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              <p class="mt-1 text-[11px] text-slate-400">Only serve keys under this prefix within the bucket</p>
            </div>
            <div class="flex gap-4 mb-4">
              <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
                <input type="checkbox" bind:checked={newS3PathStyle} class="rounded border-slate-300" />
                Path-style access (MinIO)
              </label>
              <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
                <input type="checkbox" bind:checked={newS3Secure} class="rounded border-slate-300" />
                Use HTTPS
              </label>
            </div>

          {:else if newMountType === 'ftp'}
            <div class="grid grid-cols-2 gap-3 mb-4">
              <div>
                <label for="ftp-host" class="block text-xs font-medium text-slate-500 mb-1.5">Host</label>
                <input id="ftp-host" type="text" bind:value={newFtpHost} placeholder="ftp.example.com:21"
                  class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
              <div>
                <label for="ftp-basepath" class="block text-xs font-medium text-slate-500 mb-1.5">Base Path (optional)</label>
                <input id="ftp-basepath" type="text" bind:value={newFtpBasePath} placeholder="/data"
                  class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
            </div>
            <div class="grid grid-cols-2 gap-3 mb-4">
              <div>
                <label for="ftp-username" class="block text-xs font-medium text-slate-500 mb-1.5">Username</label>
                <input id="ftp-username" type="text" bind:value={newFtpUsername} placeholder="admin"
                  class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
              <div>
                <label for="ftp-password" class="block text-xs font-medium text-slate-500 mb-1.5">Password</label>
                <input id="ftp-password" type="password" bind:value={newFtpPassword} placeholder="Password"
                  class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
            </div>
            <div class="mb-4">
              <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
                <input type="checkbox" bind:checked={newFtpTLS} class="rounded border-slate-300" />
                Use FTPS (explicit TLS)
              </label>
            </div>
          {:else if newMountType === 'sftp'}
            <div class="grid grid-cols-2 gap-3 mb-4">
              <div>
                <label for="sftp-host" class="block text-xs font-medium text-slate-500 mb-1.5">Host</label>
                <input id="sftp-host" type="text" bind:value={newSftpHost} placeholder="ssh.example.com:22"
                  class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
              <div>
                <label for="sftp-basepath" class="block text-xs font-medium text-slate-500 mb-1.5">Base Path (optional)</label>
                <input id="sftp-basepath" type="text" bind:value={newSftpBasePath} placeholder="/data"
                  class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
            </div>
            <div class="grid grid-cols-2 gap-3 mb-4">
              <div>
                <label for="sftp-username" class="block text-xs font-medium text-slate-500 mb-1.5">Username</label>
                <input id="sftp-username" type="text" bind:value={newSftpUsername} placeholder="admin"
                  class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
              <div>
                <label for="sftp-password" class="block text-xs font-medium text-slate-500 mb-1.5">Password</label>
                <input id="sftp-password" type="password" bind:value={newSftpPassword} placeholder="Password"
                  class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
            </div>
            <div class="mb-4">
              <label for="sftp-key" class="block text-xs font-medium text-slate-500 mb-1.5">Private Key (optional, PEM format)</label>
              <textarea id="sftp-key" bind:value={newSftpPrivateKey} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;..."
                rows="4"
                class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10 resize-y" ></textarea>
              <p class="mt-1 text-[11px] text-slate-400">Used instead of password authentication. Paste the full PEM-encoded key.</p>
            </div>
          {/if}

          <div class="flex justify-end gap-2">
            <button
              class="px-3 py-2 text-sm text-slate-600 bg-white border border-slate-200 rounded-md hover:bg-slate-50 transition-colors"
              onclick={() => { showAddMount = false; resetMountForm(); }}
            >
              Cancel
            </button>
            <button
              class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
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
        <div class="text-center py-12 bg-white border border-slate-200 rounded-lg">
          <FolderOpen size={32} class="mx-auto text-slate-300 mb-3" />
          <p class="text-sm text-slate-500">No raw mounts configured</p>
          <p class="text-xs text-slate-400 mt-1">Add a mount to serve files from a local directory, S3 bucket, or FTP server</p>
        </div>
      {:else}
        <div class="space-y-2">
          {#each rawMounts as mount, i (mount.prefix)}
            {@const mType = mount.type || 'local'}
            <div class="flex items-center gap-4 p-4 bg-white border border-slate-200 rounded-lg hover:border-slate-300 transition-colors">
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2">
                  <span class="text-sm font-medium text-slate-800">{mount.prefix}</span>
                  <span class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-emerald-100 text-emerald-700">
                    /raw/{mount.prefix}
                  </span>
                  <span class="px-1.5 py-0.5 text-[10px] font-medium rounded
                    {mType === 's3' ? 'bg-orange-100 text-orange-700' : mType === 'ftp' ? 'bg-purple-100 text-purple-700' : mType === 'sftp' ? 'bg-teal-100 text-teal-700' : 'bg-blue-100 text-blue-700'}">
                    {mType === 's3' ? 'S3' : mType === 'ftp' ? 'FTP' : mType === 'sftp' ? 'SFTP' : 'Local'}
                  </span>
                </div>
                <div class="mt-1">
                  {#if mType === 'local'}
                    <span class="text-xs font-mono text-slate-400">{mount.path}</span>
                  {:else if mType === 's3'}
                    <span class="text-xs font-mono text-slate-400">
                      {mount.s3?.endpoint ? mount.s3.endpoint + '/' : ''}{mount.s3?.bucket}{mount.s3?.prefix ? '/' + mount.s3.prefix : ''}
                    </span>
                  {:else if mType === 'ftp'}
                    <span class="text-xs font-mono text-slate-400">
                      {mount.ftp?.host}{mount.ftp?.base_path || ''}
                    </span>
                  {:else if mType === 'sftp'}
                    <span class="text-xs font-mono text-slate-400">
                      {mount.sftp?.username ? mount.sftp.username + '@' : ''}{mount.sftp?.host}{mount.sftp?.base_path || ''}
                    </span>
                  {/if}
                </div>
              </div>
              <div class="flex items-center gap-1 shrink-0">
                <button
                  class="p-1.5 text-slate-400 hover:text-blue-500 hover:bg-blue-50 rounded transition-colors"
                  onclick={() => handleEditMount(i)}
                  title="Edit mount"
                >
                  <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/><path d="m15 5 4 4"/></svg>
                </button>
                <button
                  class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors"
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
  {/if}

  <!-- ══════════════════════════════════════════ -->
  <!-- FTP Shares Section -->
  <!-- ══════════════════════════════════════════ -->
  {#if activeSection === 'ftp_shares'}
    <div>
      <div class="flex items-center justify-between mb-4">
        <div>
          <h2 class="text-lg font-semibold text-slate-800">FTP Shares</h2>
          <p class="text-sm text-slate-500 mt-0.5">Share folders from your raw mounts via the built-in FTP server. External clients can connect and browse/download files.</p>
        </div>
        <button
          class="flex items-center gap-1.5 px-3 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 transition-colors"
          onclick={() => { showAddShare = true; resetShareForm(); }}
        >
          <Plus size={14} />
          Add Share
        </button>
      </div>

      {#if availableMounts.length === 0}
        <div class="mb-4 p-3 bg-amber-50 border border-amber-200 rounded-md">
          <p class="text-xs text-amber-800">No raw mounts configured. Add a raw mount first before creating FTP shares.</p>
        </div>
      {/if}

      <!-- Add/Edit Share Form -->
      {#if showAddShare}
        <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
          <h3 class="text-sm font-semibold text-slate-700 mb-4">{editingShareIndex !== null ? 'Edit Share' : 'Add Share'}</h3>

          <div class="mb-4">
            <label for="share-name" class="block text-xs font-medium text-slate-500 mb-1.5">Share Name</label>
            <input id="share-name" type="text" bind:value={newShareName} placeholder="e.g., project-files"
              class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
            <p class="mt-1 text-[11px] text-slate-400">This becomes the top-level folder name visible to FTP clients</p>
          </div>

          <div class="mb-4">
            <label for="share-path-input" class="block text-xs font-medium text-slate-500 mb-1.5">Paths</label>
            <div class="flex gap-2">
              <input id="share-path-input" type="text" bind:value={newSharePathInput}
                placeholder="mount/folder (e.g., configs/app)"
                onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addSharePath(); } }}
                class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              <button
                class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
                onclick={addSharePath}
              >
                Add
              </button>
            </div>
            <p class="mt-1 text-[11px] text-slate-400">
              Format: <code class="px-1 py-0.5 bg-slate-100 rounded text-[10px]">mount_prefix</code> or
              <code class="px-1 py-0.5 bg-slate-100 rounded text-[10px]">mount_prefix/sub/folder</code>.
              Multiple paths are merged into a single share.
              {#if availableMounts.length > 0}
                Available mounts: {availableMounts.map(m => m.prefix).join(', ')}
              {/if}
            </p>

            {#if newSharePaths.length > 0}
              <div class="mt-2 flex flex-wrap gap-1.5">
                {#each newSharePaths as p, i}
                  <span class="inline-flex items-center gap-1 px-2 py-1 bg-blue-50 border border-blue-200 rounded text-xs font-mono text-blue-700">
                    {p}
                    <button
                      class="flex items-center justify-center w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-blue-400 hover:text-red-500 transition-colors"
                      onclick={() => removeSharePath(i)}
                      title="Remove"
                    >&times;</button>
                  </span>
                {/each}
              </div>
            {/if}
          </div>

          <div class="mb-4">
            <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
              <input type="checkbox" bind:checked={newShareReadOnly} class="rounded border-slate-300" />
              Read-only (clients cannot upload or delete)
            </label>
          </div>

          <div class="mb-4">
            <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
              <input type="checkbox" bind:checked={newShareRoot} class="rounded border-slate-300" />
              Mount at root
            </label>
            <p class="mt-1 ml-5 text-[11px] text-slate-400">Serve this share's contents directly at <code class="px-1 py-0.5 bg-slate-100 rounded text-[10px]">/</code> instead of <code class="px-1 py-0.5 bg-slate-100 rounded text-[10px]">/{'{name}'}/</code>. Only one share can be root. Other shares will be hidden while a root share is active.</p>
          </div>

          <div class="flex justify-end gap-2">
            <button
              class="px-3 py-2 text-sm text-slate-600 bg-white border border-slate-200 rounded-md hover:bg-slate-50 transition-colors"
              onclick={() => { showAddShare = false; resetShareForm(); }}
            >
              Cancel
            </button>
            <button
              class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
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
        <div class="text-center py-12 bg-white border border-slate-200 rounded-lg">
          <Share2 size={32} class="mx-auto text-slate-300 mb-3" />
          <p class="text-sm text-slate-500">No FTP shares configured</p>
          <p class="text-xs text-slate-400 mt-1">Add a share to expose folders via the built-in FTP server</p>
        </div>
      {:else}
        <div class="space-y-2">
          {#each ftpShares as share, i (share.name)}
            <div class="flex items-center gap-4 p-4 bg-white border border-slate-200 rounded-lg hover:border-slate-300 transition-colors">
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2">
                   <span class="text-sm font-medium text-slate-800">{share.name}</span>
                   {#if share.root}
                     <span class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-blue-100 text-blue-700">
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
                  <span class="text-[10px] text-slate-400">{share.root ? '→ ftp://.../' : `→ ftp://.../${share.name}/`}</span>
                </div>
                <div class="mt-1 flex flex-wrap gap-1">
                  {#each share.paths as p}
                    <span class="px-1.5 py-0.5 text-[10px] font-mono rounded bg-slate-100 text-slate-600 border border-slate-200">{p}</span>
                  {/each}
                </div>
              </div>
              <div class="flex items-center gap-1 shrink-0">
                <button
                  class="p-1.5 text-slate-400 hover:text-blue-500 hover:bg-blue-50 rounded transition-colors"
                  onclick={() => handleEditShare(i)}
                  title="Edit share"
                >
                  <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/><path d="m15 5 4 4"/></svg>
                </button>
                <button
                  class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors"
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
            <h3 class="text-base font-semibold text-slate-800">Users</h3>
            <p class="text-xs text-slate-500 mt-0.5">Manage FTP/SFTP user accounts. Users are shared between both servers.</p>
          </div>
          <button
            class="flex items-center gap-1.5 px-3 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 transition-colors"
            onclick={() => { showAddUser = true; resetUserForm(); }}
          >
            <Plus size={14} />
            Add User
          </button>
        </div>

        {#if showAddUser}
          <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
            <h3 class="text-sm font-semibold text-slate-700 mb-4">{editingUserIndex !== null ? 'Edit User' : 'Add User'}</h3>

            <div class="grid grid-cols-2 gap-3 mb-4">
              <div>
                <label for="ftp-user-name" class="block text-xs font-medium text-slate-500 mb-1.5">Username</label>
                <input id="ftp-user-name" type="text" bind:value={newUserUsername} placeholder="admin"
                  class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
              <div>
                <label for="ftp-user-pass" class="block text-xs font-medium text-slate-500 mb-1.5">Password</label>
                <div class="relative">
                  <input id="ftp-user-pass" type={showUserPassword ? 'text' : 'password'} bind:value={newUserPassword} placeholder="Password"
                    class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                  <button
                    type="button"
                    class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
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
                <label for="ftp-user-authorized-keys" class="block text-xs font-medium text-slate-500">Authorized Keys (optional)</label>
                <button
                  type="button"
                  class="flex items-center gap-1 px-2 py-1 text-[11px] font-medium text-slate-600 bg-slate-100 border border-slate-200 rounded hover:bg-slate-200 transition-colors"
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
                class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10 resize-y"></textarea>
              <p class="mt-1 text-[11px] text-slate-400">
                SSH public keys for SFTP key-based authentication (OpenSSH <code class="px-0.5 bg-slate-100 rounded">authorized_keys</code> format, one key per line).
                When set, password becomes optional.
              </p>
              {#if newUserAuthorizedKeys.trim()}
                <div class="mt-2 p-2.5 bg-slate-50 border border-slate-200 rounded text-[11px] text-slate-500 space-y-1.5">
                  <p class="font-medium text-slate-600">Connection instructions</p>
                  <p>
                    <span class="font-medium">Command line:</span>
                    <code class="px-1 py-0.5 bg-slate-100 rounded font-mono text-[10px]">sftp -P {sftpServePort || 2222} -i /path/to/{newUserUsername.trim() || 'user'}_id_ed25519 {newUserUsername.trim() || 'user'}@your-host</code>
                  </p>
                  <p>
                    <span class="font-medium">FileZilla:</span>
                    Edit &rarr; Settings &rarr; SFTP &rarr; Add key file. Then connect with Host / Port / Username.
                  </p>
                  <p>
                    <span class="font-medium">WinSCP:</span>
                    Session &rarr; Advanced &rarr; SSH &rarr; Authentication &rarr; Private key file.
                  </p>
                  <p class="text-slate-400">Keep the private key file secure. Never share it.</p>
                </div>
              {/if}
            </div>

            <div class="mb-4">
              <label for="ftp-user-shares" class="block text-xs font-medium text-slate-500 mb-1.5">Allowed Shares (optional)</label>
              <div class="flex gap-2">
                <input id="ftp-user-shares" type="text" bind:value={newUserShareInput}
                  placeholder="Share name"
                  onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addUserShare(); } }}
                  class="flex-1 px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                <button
                  class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
                  onclick={addUserShare}
                >
                  Add
                </button>
              </div>
              <p class="mt-1 text-[11px] text-slate-400">Leave empty to allow access to all shares. Otherwise, type share names to restrict access.</p>

              {#if newUserShares.length > 0}
                <div class="mt-2 flex flex-wrap gap-1.5">
                  {#each newUserShares as s, i}
                    <span class="inline-flex items-center gap-1 px-2 py-1 bg-blue-50 border border-blue-200 rounded text-xs text-blue-700">
                      {s}
                      <button class="w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-blue-400 hover:text-red-500"
                        onclick={() => removeUserShare(i)}>&times;</button>
                    </span>
                  {/each}
                </div>
              {/if}
            </div>

            <div class="mb-4">
              <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
                <input type="checkbox" bind:checked={newUserReadOnly} class="rounded border-slate-300" />
                Read-only (user cannot upload or delete, regardless of share settings)
              </label>
            </div>

            <div class="flex justify-end gap-2">
              <button class="px-3 py-2 text-sm text-slate-600 bg-white border border-slate-200 rounded-md hover:bg-slate-50 transition-colors"
                onclick={() => { showAddUser = false; resetUserForm(); }}>Cancel</button>
              <button class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                onclick={handleAddUser} disabled={isSavingUsers}>
                {isSavingUsers ? 'Saving...' : editingUserIndex !== null ? 'Save Changes' : 'Add User'}
              </button>
            </div>
          </div>
        {/if}

        {#if ftpUsers.length === 0 && !showAddUser}
          <div class="text-center py-8 bg-white border border-slate-200 rounded-lg">
            <p class="text-sm text-slate-500">No users configured</p>
            <p class="text-xs text-slate-400 mt-1">Add a user to enable FTP/SFTP access. Users can also be set in the config file.</p>
          </div>
        {:else}
          <div class="space-y-2">
            {#each ftpUsers as user, i (user.username)}
              <div class="flex items-center gap-4 p-4 bg-white border border-slate-200 rounded-lg hover:border-slate-300 transition-colors">
                <div class="flex-1 min-w-0">
                  <div class="flex items-center gap-2">
                    <span class="text-sm font-medium text-slate-800">{user.username}</span>
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
                      <span class="text-[11px] text-slate-400 mr-1">Shares:</span>
                      {#each user.shares as s}
                        <span class="px-1 py-0.5 text-[10px] rounded bg-slate-100 text-slate-600 border border-slate-200 mr-1">{s}</span>
                      {/each}
                    {:else}
                      <span class="text-[11px] text-slate-400">All shares</span>
                    {/if}
                  </div>
                </div>
                <div class="flex items-center gap-1 shrink-0">
                  <button class="p-1.5 text-slate-400 hover:text-blue-500 hover:bg-blue-50 rounded transition-colors"
                    onclick={() => handleEditUser(i)} title="Edit user">
                    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/><path d="m15 5 4 4"/></svg>
                  </button>
                  <button class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors"
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
  {/if}

  <!-- ══════════════════════════════════════════ -->
  <!-- File Servers Section -->
  <!-- ══════════════════════════════════════════ -->
  {#if activeSection === 'file_servers'}
    <div>
      <div class="mb-6">
        <h2 class="text-lg font-semibold text-slate-800">File Servers</h2>
        <p class="text-sm text-slate-500 mt-0.5">Configure built-in FTP, SFTP, and TFTP servers.</p>
      </div>

      <!-- FTP Server -->
      <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
        <div class="flex items-center justify-between mb-4">
          <h3 class="text-sm font-semibold text-slate-700">FTP Server</h3>
          <label class="flex items-center gap-2 cursor-pointer">
            <input type="checkbox" bind:checked={ftpServeEnabled}
              class="w-4 h-4 rounded border-slate-300 text-blue-600 focus:ring-blue-500" />
            <span class="text-xs font-medium text-slate-600">Enabled</span>
          </label>
        </div>

        {#if ftpServeEnabled}
          <div class="grid grid-cols-2 gap-4">
            <div>
              <label for="ftp-port" class="block text-xs font-medium text-slate-500 mb-1.5">Port</label>
              <input id="ftp-port" type="number" bind:value={ftpServePort} placeholder="2121"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
            </div>
            <div>
              <label for="ftp-host" class="block text-xs font-medium text-slate-500 mb-1.5">Host</label>
              <input id="ftp-host" type="text" bind:value={ftpServeHost} placeholder="0.0.0.0 (all interfaces)"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
            </div>
            <div>
              <label for="ftp-public-ip" class="block text-xs font-medium text-slate-500 mb-1.5">Public IP</label>
              <input id="ftp-public-ip" type="text" bind:value={ftpServePublicIP} placeholder="(for passive mode)"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
            </div>
            <div>
              <label for="ftp-passive-ports" class="block text-xs font-medium text-slate-500 mb-1.5">Passive Ports</label>
              <input id="ftp-passive-ports" type="text" bind:value={ftpServePassivePorts} placeholder="30000-30100"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
            </div>
            <div class="col-span-2">
              <label for="ftp-tls-mode" class="block text-xs font-medium text-slate-500 mb-1.5">TLS Mode</label>
              <select id="ftp-tls-mode"
                value={ftpServeTLSRequired}
                onchange={(e) => { ftpServeTLSRequired = Number((e.target as HTMLSelectElement).value); }}
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10 bg-white">
                <option value={0}>Disabled (plain FTP)</option>
                <option value={1}>Explicit FTPS (AUTH TLS)</option>
                <option value={2}>Implicit FTPS</option>
              </select>
              <p class="mt-1 text-[11px] text-slate-400">
                Explicit FTPS: clients connect in plain text and upgrade via AUTH TLS command.
                Implicit FTPS: entire connection is TLS from the start (typically port 990).
              </p>
            </div>
            {#if ftpServeTLSRequired > 0}
              <div class="col-span-2">
                <div class="flex items-center gap-3 mb-3">
                  <span class="text-xs font-medium text-slate-500">TLS Certificate & Key</span>
                  <div class="flex items-center border border-slate-200 rounded-md overflow-hidden">
                    <button
                      type="button"
                      class="px-2.5 py-1 text-[11px] font-medium transition-colors {ftpServeTLSInputMode === 'path' ? 'bg-slate-800 text-white' : 'bg-white text-slate-500 hover:text-slate-700'}"
                      onclick={() => { ftpServeTLSInputMode = 'path'; }}
                    >File Path</button>
                    <button
                      type="button"
                      class="px-2.5 py-1 text-[11px] font-medium transition-colors {ftpServeTLSInputMode === 'paste' ? 'bg-slate-800 text-white' : 'bg-white text-slate-500 hover:text-slate-700'}"
                      onclick={() => { ftpServeTLSInputMode = 'paste'; }}
                    >Paste PEM</button>
                  </div>
                </div>

                {#if ftpServeTLSInputMode === 'path'}
                  <div class="grid grid-cols-2 gap-4">
                    <div>
                      <label for="ftp-tls-cert" class="block text-xs font-medium text-slate-500 mb-1.5">TLS Certificate Path</label>
                      <input id="ftp-tls-cert" type="text" bind:value={ftpServeTLSCertFile} placeholder="/path/to/cert.pem"
                        class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                    </div>
                    <div>
                      <label for="ftp-tls-key" class="block text-xs font-medium text-slate-500 mb-1.5">TLS Key Path</label>
                      <input id="ftp-tls-key" type="text" bind:value={ftpServeTLSKeyFile} placeholder="/path/to/key.pem"
                        class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                    </div>
                  </div>
                {:else}
                  <div class="grid grid-cols-2 gap-4">
                    <div>
                      <label for="ftp-tls-cert-pem" class="block text-xs font-medium text-slate-500 mb-1.5">TLS Certificate (PEM)</label>
                      <textarea id="ftp-tls-cert-pem" bind:value={ftpServeTLSCertPEM} placeholder="-----BEGIN CERTIFICATE-----&#10;MIIBxTCCAWugAwIBAgIU...&#10;-----END CERTIFICATE-----" rows="6"
                        class="w-full px-3 py-2 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10 resize-y"></textarea>
                      {#if ftpServeTLSCertPEM && !ftpServeTLSCertPEM.includes('BEGIN CERTIFICATE')}
                        <p class="mt-1 text-[11px] text-red-600">
                          This does not look like a certificate. Expected a PEM block starting with <code class="font-mono">-----BEGIN CERTIFICATE-----</code>.
                          {#if ftpServeTLSCertPEM.includes('PUBLIC KEY')}You may have pasted a public key by mistake.{/if}
                        </p>
                      {/if}
                      <p class="mt-1 text-[11px] text-slate-400">Paste the X.509 certificate (the contents of your <code class="font-mono">cert.pem</code>). This is not the public key.</p>
                    </div>
                    <div>
                      <label for="ftp-tls-key-pem" class="block text-xs font-medium text-slate-500 mb-1.5">TLS Private Key (PEM)</label>
                      <textarea id="ftp-tls-key-pem" bind:value={ftpServeTLSKeyPEM} placeholder="-----BEGIN PRIVATE KEY-----&#10;MIGHAgEAMBMGByqGSM49...&#10;-----END PRIVATE KEY-----" rows="6"
                        class="w-full px-3 py-2 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10 resize-y"></textarea>
                      {#if ftpServeTLSKeyPEM && !ftpServeTLSKeyPEM.includes('PRIVATE KEY')}
                        <p class="mt-1 text-[11px] text-red-600">
                          This does not look like a private key. Expected a PEM block starting with <code class="font-mono">-----BEGIN PRIVATE KEY-----</code> (or <code class="font-mono">RSA PRIVATE KEY</code> / <code class="font-mono">EC PRIVATE KEY</code>).
                        </p>
                      {/if}
                      <p class="mt-1 text-[11px] text-slate-400">Paste the private key (the contents of your <code class="font-mono">key.pem</code>). Do not paste the public key.</p>
                    </div>
                  </div>
                {/if}

                <p class="mt-2 text-[11px] text-slate-400">
                  {#if ftpServeTLSInputMode === 'path'}
                    PEM-encoded TLS certificate and private key files on the server filesystem.
                  {:else}
                    Paste the PEM content directly. The certificate field expects an X.509 certificate (<code class="font-mono">BEGIN CERTIFICATE</code>), not a public key. The key field expects a private key (<code class="font-mono">BEGIN PRIVATE KEY</code>). Both are stored in the database.
                  {/if}
                  Generate a self-signed pair with:
                  <code class="px-1 py-0.5 bg-slate-100 rounded text-[10px] font-mono">openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -keyout key.pem -out cert.pem -days 3650 -nodes</code>
                </p>
              </div>
            {/if}
          </div>
        {/if}
      </div>

      <!-- SFTP Server -->
      <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
        <div class="flex items-center justify-between mb-4">
          <h3 class="text-sm font-semibold text-slate-700">SFTP Server</h3>
          <label class="flex items-center gap-2 cursor-pointer">
            <input type="checkbox" bind:checked={sftpServeEnabled}
              class="w-4 h-4 rounded border-slate-300 text-blue-600 focus:ring-blue-500" />
            <span class="text-xs font-medium text-slate-600">Enabled</span>
          </label>
        </div>

        {#if sftpServeEnabled}
          <div class="grid grid-cols-2 gap-4">
            <div>
              <label for="sftp-port" class="block text-xs font-medium text-slate-500 mb-1.5">Port</label>
              <input id="sftp-port" type="number" bind:value={sftpServePort} placeholder="2222"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
            </div>
            <div>
              <label for="sftp-host" class="block text-xs font-medium text-slate-500 mb-1.5">Host</label>
              <input id="sftp-host" type="text" bind:value={sftpServeHost} placeholder="0.0.0.0 (all interfaces)"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
            </div>
            <div class="col-span-2">
              <div class="flex items-center gap-3 mb-3">
                <span class="text-xs font-medium text-slate-500">Host Key</span>
                <div class="flex items-center border border-slate-200 rounded-md overflow-hidden">
                  <button
                    type="button"
                    class="px-2.5 py-1 text-[11px] font-medium transition-colors {sftpServeKeyInputMode === 'path' ? 'bg-slate-800 text-white' : 'bg-white text-slate-500 hover:text-slate-700'}"
                    onclick={() => { sftpServeKeyInputMode = 'path'; }}
                  >File Path</button>
                  <button
                    type="button"
                    class="px-2.5 py-1 text-[11px] font-medium transition-colors {sftpServeKeyInputMode === 'paste' ? 'bg-slate-800 text-white' : 'bg-white text-slate-500 hover:text-slate-700'}"
                    onclick={() => { sftpServeKeyInputMode = 'paste'; }}
                  >Paste PEM</button>
                </div>
              </div>

              {#if sftpServeKeyInputMode === 'path'}
                <label for="sftp-host-key" class="block text-xs font-medium text-slate-500 mb-1.5">Host Key Path</label>
                <input id="sftp-host-key" type="text" bind:value={sftpServeHostKeyPath} placeholder="(auto-generated if empty)"
                  class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              {:else}
                <label for="sftp-host-key-pem" class="block text-xs font-medium text-slate-500 mb-1.5">Host Key (PEM)</label>
                <textarea id="sftp-host-key-pem" bind:value={sftpServeHostKeyPEM} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;b3BlbnNzaC1rZXktdjEAAAA...&#10;-----END OPENSSH PRIVATE KEY-----" rows="6"
                  class="w-full px-3 py-2 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10 resize-y"></textarea>
                {#if sftpServeHostKeyPEM && !sftpServeHostKeyPEM.includes('PRIVATE KEY')}
                  <p class="mt-1 text-[11px] text-red-600">
                    This does not look like a private key. Expected a PEM block starting with <code class="font-mono">-----BEGIN OPENSSH PRIVATE KEY-----</code> or <code class="font-mono">-----BEGIN PRIVATE KEY-----</code>.
                    {#if sftpServeHostKeyPEM.includes('PUBLIC KEY')}You may have pasted a public key by mistake. Paste the private key instead.{/if}
                  </p>
                {/if}
                <p class="mt-1 text-[11px] text-slate-400">Paste the SSH private key content directly. This is stored in the database and avoids needing a file on disk.</p>
              {/if}

              <p class="mt-1 text-[11px] text-slate-400">
                {#if sftpServeKeyInputMode === 'path'}
                  Path to the server's SSH private key file (PEM format). This key identifies the server to connecting clients.
                  Leave empty to auto-generate an Ed25519 key. The generated key is automatically saved to the database and reused across restarts.
                {:else}
                  The host key must be a private key (e.g. <code class="font-mono">BEGIN OPENSSH PRIVATE KEY</code> or <code class="font-mono">BEGIN PRIVATE KEY</code>), not a public key.
                {/if}
                Supported key types: Ed25519, RSA, ECDSA.
                Generate one with: <code class="px-1 py-0.5 bg-slate-100 rounded text-[10px] font-mono">ssh-keygen -t ed25519 -f /path/to/host_key -N ""</code>.
              </p>
            </div>
          </div>
        {/if}
      </div>

      <!-- TFTP Server -->
      <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
        <div class="flex items-center justify-between mb-4">
          <h3 class="text-sm font-semibold text-slate-700">TFTP Server</h3>
          <label class="flex items-center gap-2 cursor-pointer">
            <input type="checkbox" bind:checked={tftpServeEnabled}
              class="w-4 h-4 rounded border-slate-300 text-blue-600 focus:ring-blue-500" />
            <span class="text-xs font-medium text-slate-600">Enabled</span>
          </label>
        </div>

        {#if tftpServeEnabled}
          <div class="grid grid-cols-2 gap-4">
            <div>
              <label for="tftp-port" class="block text-xs font-medium text-slate-500 mb-1.5">Port</label>
              <input id="tftp-port" type="number" bind:value={tftpServePort} placeholder="69"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
            </div>
            <div>
              <label for="tftp-host" class="block text-xs font-medium text-slate-500 mb-1.5">Host</label>
              <input id="tftp-host" type="text" bind:value={tftpServeHost} placeholder="0.0.0.0 (all interfaces)"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
            </div>
          </div>
        {/if}
      </div>

      <!-- Save button -->
      <div class="flex justify-end">
        <button
          class="px-4 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          onclick={handleSaveServers}
          disabled={isSavingServers}
        >
          {isSavingServers ? 'Saving...' : 'Save Server Settings'}
        </button>
      </div>
    </div>
  {/if}

  <!-- ══════════════════════════════════════════ -->
  <!-- Key Rotation Section -->
  <!-- ══════════════════════════════════════════ -->
  {#if activeSection === 'rotation'}
    <div>
      <div class="mb-4">
        <h2 class="text-lg font-semibold text-slate-800">Key Rotation</h2>
        <p class="text-sm text-slate-500 mt-0.5">Rotate the encryption key used to protect stored configurations</p>
      </div>

      <div class="p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
        <div class="mb-5 p-3 bg-amber-50 border border-amber-200 rounded-md">
          <p class="text-xs text-amber-800 leading-relaxed m-0">
            Key rotation will re-encrypt all stored data with the new key. This operation may take time depending on the amount of data.
            After rotation, update the <code class="px-1 py-0.5 bg-amber-100 rounded text-[11px]">PIKA_SECRET_ENCRYPTION_KEY</code> environment variable to the new key.
          </p>
        </div>

        <div class="mb-4">
          <label for="rotation-admin-secret" class="block text-xs font-medium text-slate-500 mb-1.5">Admin Secret</label>
          <div class="relative">
            <input
              id="rotation-admin-secret"
              type={showRotationAdminSecret ? 'text' : 'password'}
              bind:value={rotationAdminSecret}
              placeholder="Enter your admin secret"
              class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
              onclick={() => showRotationAdminSecret = !showRotationAdminSecret}
              title={showRotationAdminSecret ? 'Hide' : 'Show'}
            >
              {#if showRotationAdminSecret}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
            </button>
          </div>
          <p class="mt-1 text-[11px] text-slate-400">The admin secret configured in the Security tab</p>
        </div>

        <div class="mb-4">
          <label for="rotation-new-key" class="block text-xs font-medium text-slate-500 mb-1.5">New Encryption Key</label>
          <div class="relative">
            <input
              id="rotation-new-key"
              type={showNewKey ? 'text' : 'password'}
              bind:value={rotationNewKey}
              placeholder="Enter new encryption key"
              class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
              onclick={() => showNewKey = !showNewKey}
              title={showNewKey ? 'Hide' : 'Show'}
            >
              {#if showNewKey}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
            </button>
          </div>
          <p class="mt-1 text-[11px] text-slate-400">Any string — will be hashed (SHA-256) to derive the encryption key. After rotation, update the <code class="px-1 py-0.5 bg-slate-100 rounded text-[11px]">PIKA_SECRET_ENCRYPTION_KEY</code> environment variable.</p>
        </div>

        <button
          class="flex items-center justify-center gap-2 w-full px-4 py-2.5 text-sm font-medium text-white rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed
            {isRotating ? 'bg-amber-500' : 'bg-red-600 hover:bg-red-700'}"
          onclick={handleRotateKey}
          disabled={isRotating}
        >
          <RotateCw size={14} class={isRotating ? 'animate-spin' : ''} />
          {isRotating ? 'Rotating...' : 'Rotate Encryption Key'}
        </button>
      </div>
    </div>
  {/if}

  <!-- ══════════════════════════════════════════ -->
  <!-- Security Section (Admin Secret) -->
  <!-- ══════════════════════════════════════════ -->
  {#if activeSection === 'security'}
    <div>
      <div class="mb-4">
        <h2 class="text-lg font-semibold text-slate-800">Admin Secret</h2>
        <p class="text-sm text-slate-500 mt-0.5">Set or update the admin secret used to authorize key rotation</p>
      </div>

      <div class="p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
        {#if adminSecretConfigured}
          <div class="mb-5 flex items-center gap-2 p-3 bg-green-50 border border-green-200 rounded-md">
            <Shield size={14} class="text-green-600 shrink-0" />
            <p class="text-xs text-green-800 m-0">Admin secret is configured.</p>
          </div>
        {:else}
          <div class="mb-5 flex items-center gap-2 p-3 bg-amber-50 border border-amber-200 rounded-md">
            <Shield size={14} class="text-amber-600 shrink-0" />
            <p class="text-xs text-amber-800 m-0">No admin secret configured. Set one to enable key rotation.</p>
          </div>
        {/if}

        {#if adminSecretConfigured}
          <div class="mb-4">
            <label for="current-admin-secret" class="block text-xs font-medium text-slate-500 mb-1.5">Current Secret</label>
            <div class="relative">
              <input
                id="current-admin-secret"
                type={showCurrentAdminSecret ? 'text' : 'password'}
                bind:value={currentAdminSecret}
                placeholder="Enter current admin secret"
                class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
              <button
                type="button"
                class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
                onclick={() => showCurrentAdminSecret = !showCurrentAdminSecret}
                title={showCurrentAdminSecret ? 'Hide' : 'Show'}
              >
                {#if showCurrentAdminSecret}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
              </button>
            </div>
          </div>
        {/if}

        <div class="mb-4">
          <label for="new-admin-secret" class="block text-xs font-medium text-slate-500 mb-1.5">New Secret</label>
          <div class="relative">
            <input
              id="new-admin-secret"
              type={showNewAdminSecret ? 'text' : 'password'}
              bind:value={newAdminSecret}
              placeholder="Enter new admin secret"
              class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
              onclick={() => showNewAdminSecret = !showNewAdminSecret}
              title={showNewAdminSecret ? 'Hide' : 'Show'}
            >
              {#if showNewAdminSecret}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
            </button>
          </div>
        </div>

        <div class="mb-4">
          <label for="confirm-admin-secret" class="block text-xs font-medium text-slate-500 mb-1.5">Confirm New Secret</label>
          <div class="relative">
            <input
              id="confirm-admin-secret"
              type={showConfirmAdminSecret ? 'text' : 'password'}
              bind:value={confirmAdminSecret}
              placeholder="Confirm new admin secret"
              class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
              onclick={() => showConfirmAdminSecret = !showConfirmAdminSecret}
              title={showConfirmAdminSecret ? 'Hide' : 'Show'}
            >
              {#if showConfirmAdminSecret}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
            </button>
          </div>
        </div>

        <button
          class="flex items-center justify-center gap-2 w-full px-4 py-2.5 text-sm font-medium text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          onclick={handleSetAdminSecret}
          disabled={isSavingAdminSecret}
        >
          <Lock size={14} />
          {isSavingAdminSecret ? 'Saving...' : adminSecretConfigured ? 'Update Admin Secret' : 'Set Admin Secret'}
        </button>
      </div>
    </div>
  {/if}

  <!-- ══════════════════════════════════════════ -->
  <!-- Backup & Restore Section -->
  <!-- ══════════════════════════════════════════ -->
  {#if activeSection === 'backup'}
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
  {/if}
</div>
</div>
</div>
