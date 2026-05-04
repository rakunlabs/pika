<script lang="ts">
  import { configStore } from "@/lib/store/config.svelte";
  import { appStore, type PermissionInfo } from "@/lib/store/store.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { onMount } from "svelte";
  import { Plus, Trash2, RefreshCw, FlaskConical, Users, Eye, EyeOff } from "lucide-svelte";
  import type { SyncSource, LDAPSyncSpec, LDAPAttributeMap, SyncSourceStatus, SyncReport } from "@/lib/types/config";

  // ── State ──
  let sources = $state<SyncSource[]>([]);
  let statuses = $state<SyncSourceStatus[]>([]);
  let permissions = $state<PermissionInfo[]>([]);
  let editingIndex = $state<number | null>(null); // -1 = new, >=0 = existing index
  let saving = $state(false);
  let runningSourceId = $state<string | null>(null);
  let testResult = $state<{ source_id: string; total: number; entries: { dn: string; attributes: Record<string, string[]> }[] } | null>(null);
  let confirmDeleteIdx = $state<number | null>(null);
  let showBindPassword = $state(false);

  // Editor form fields — mirror SyncSource shape but flat for form binding.
  let formId = $state('');
  let formName = $state('');
  let formEnabled = $state(true);
  let formAddress = $state('');
  let formTLS = $state(false);
  let formInsecureSkip = $state(false);
  let formBindDN = $state('');
  let formBindPassword = $state('');
  let formUserBaseDN = $state('');
  let formUserFilter = $state('(objectClass=person)');
  let formPageSize = $state(500);
  // Attribute mapping
  let attrUsername = $state('uid');
  let attrSubject = $state('');
  let attrEmail = $state('mail');
  let attrDisplayName = $state('displayName');
  let attrGivenName = $state('givenName');
  let attrSurname = $state('sn');
  let attrGroups = $state('memberOf');
  // Group → permission map (rows of {group, permissionIds})
  let groupRows = $state<{ group: string; permissionIds: string[] }[]>([]);
  // Schedule
  let scheduleMode = $state<'manual' | 'interval'>('manual');
  let scheduleMinutes = $state(10);
  let onMissing = $state<'disable' | 'ignore'>('disable');

  onMount(async () => {
    await configStore.loadSettings();
    sources = configStore.settings?.user_sync?.sources ?? [];
    statuses = await configStore.listUserSyncStatus();
    await appStore.loadPermissions();
    permissions = appStore.permissions;
  });

  function findStatus(id: string): SyncSourceStatus | undefined {
    return statuses.find(s => s.id === id);
  }

  function permissionName(id: string): string {
    return permissions.find(p => p.id === id)?.name ?? id;
  }

  // ── Editor lifecycle ──
  function startNew() {
    editingIndex = -1;
    formId = '';
    formName = '';
    formEnabled = true;
    formAddress = '';
    formTLS = false;
    formInsecureSkip = false;
    formBindDN = '';
    formBindPassword = '';
    formUserBaseDN = '';
    formUserFilter = '(objectClass=person)';
    formPageSize = 500;
    attrUsername = 'uid';
    attrSubject = '';
    attrEmail = 'mail';
    attrDisplayName = 'displayName';
    attrGivenName = 'givenName';
    attrSurname = 'sn';
    attrGroups = 'memberOf';
    groupRows = [];
    scheduleMode = 'manual';
    scheduleMinutes = 10;
    onMissing = 'disable';
    showBindPassword = false;
    testResult = null;
  }

  function startEdit(idx: number) {
    const src = sources[idx];
    editingIndex = idx;
    formId = src.id;
    formName = src.name;
    formEnabled = src.enabled;
    const ldap = src.ldap ?? {} as LDAPSyncSpec;
    formAddress = ldap.address ?? '';
    formTLS = ldap.tls ?? false;
    formInsecureSkip = ldap.insecure_skip ?? false;
    formBindDN = ldap.bind_dn ?? '';
    formBindPassword = ldap.bind_password ?? '';
    formUserBaseDN = ldap.user_base_dn ?? '';
    formUserFilter = ldap.user_filter ?? '(objectClass=person)';
    formPageSize = ldap.page_size ?? 500;
    const attr: LDAPAttributeMap = ldap.attributes ?? { username: 'uid' };
    attrUsername = attr.username ?? 'uid';
    attrSubject = attr.subject ?? '';
    attrEmail = attr.email ?? '';
    attrDisplayName = attr.display_name ?? '';
    attrGivenName = attr.given_name ?? '';
    attrSurname = attr.surname ?? '';
    attrGroups = attr.groups ?? '';
    groupRows = Object.entries(ldap.group_permissions ?? {}).map(([group, ids]) => ({ group, permissionIds: [...ids] }));
    scheduleMode = src.schedule.mode;
    scheduleMinutes = src.schedule.interval_minutes ?? 10;
    onMissing = (src.on_missing as 'disable' | 'ignore') ?? 'disable';
    showBindPassword = false;
    testResult = null;
  }

  function cancelEdit() {
    editingIndex = null;
    testResult = null;
  }

  // ── Group permission rows ──
  function addGroupRow() {
    groupRows = [...groupRows, { group: '', permissionIds: [] }];
  }

  function removeGroupRow(idx: number) {
    groupRows = groupRows.filter((_, i) => i !== idx);
  }

  function updateGroupRow(idx: number, group: string) {
    const next = [...groupRows];
    next[idx] = { ...next[idx], group };
    groupRows = next;
  }

  function toggleGroupPermission(idx: number, permId: string) {
    const next = [...groupRows];
    const row = { ...next[idx] };
    if (row.permissionIds.includes(permId)) {
      row.permissionIds = row.permissionIds.filter(id => id !== permId);
    } else {
      row.permissionIds = [...row.permissionIds, permId];
    }
    next[idx] = row;
    groupRows = next;
  }

  // ── Save ──
  function buildSourceFromForm(): SyncSource {
    const groupPermissions: Record<string, string[]> = {};
    for (const row of groupRows) {
      const g = row.group.trim();
      if (!g || row.permissionIds.length === 0) continue;
      groupPermissions[g] = [...row.permissionIds];
    }
    const ldap: LDAPSyncSpec = {
      address: formAddress.trim(),
      tls: formTLS || undefined,
      insecure_skip: formInsecureSkip || undefined,
      bind_dn: formBindDN.trim(),
      bind_password: formBindPassword || undefined,
      user_base_dn: formUserBaseDN.trim(),
      user_filter: formUserFilter.trim() || undefined,
      page_size: formPageSize > 0 ? formPageSize : undefined,
      attributes: {
        username: attrUsername.trim() || 'uid',
        subject: attrSubject.trim() || undefined,
        email: attrEmail.trim() || undefined,
        display_name: attrDisplayName.trim() || undefined,
        given_name: attrGivenName.trim() || undefined,
        surname: attrSurname.trim() || undefined,
        groups: attrGroups.trim() || undefined,
      },
      group_permissions: Object.keys(groupPermissions).length > 0 ? groupPermissions : undefined,
    };
    return {
      id: formId.trim(),
      name: formName.trim() || formId.trim(),
      type: 'ldap',
      enabled: formEnabled,
      ldap,
      schedule: { mode: scheduleMode, interval_minutes: scheduleMode === 'interval' ? scheduleMinutes : undefined },
      on_missing: onMissing,
    };
  }

  async function saveCurrent() {
    if (editingIndex === null) return;
    if (!formId.trim()) {
      addToast('Source ID is required', 'alert');
      return;
    }
    if (!/^[a-z0-9-_]+$/.test(formId.trim())) {
      addToast('Source ID may only contain lowercase letters, digits, "-" and "_"', 'alert');
      return;
    }
    if (!formAddress.trim() || !formUserBaseDN.trim()) {
      addToast('Address and User Base DN are required', 'alert');
      return;
    }

    saving = true;
    try {
      const built = buildSourceFromForm();
      const next = [...sources];
      if (editingIndex === -1) {
        if (next.some(s => s.id === built.id)) {
          addToast(`Source ID "${built.id}" is already in use`, 'alert');
          saving = false;
          return;
        }
        next.push(built);
      } else {
        next[editingIndex] = built;
      }
      await configStore.saveUserSync({ sources: next });
      sources = next;
      statuses = await configStore.listUserSyncStatus();
      editingIndex = null;
    } catch (err) {
      // saveUserSync already toasted
    } finally {
      saving = false;
    }
  }

  async function deleteSource(idx: number) {
    const next = sources.filter((_, i) => i !== idx);
    try {
      await configStore.saveUserSync({ sources: next });
      sources = next;
      statuses = await configStore.listUserSyncStatus();
      confirmDeleteIdx = null;
    } catch {
      // toasted by store
    }
  }

  // ── Run / Test ──
  async function runNow(id: string) {
    runningSourceId = id;
    try {
      const report: SyncReport = await configStore.runUserSync(id);
      const errs = report.errors?.length ?? 0;
      const summary = `Found ${report.found} · Created ${report.created} · Updated ${report.updated} · Disabled ${report.disabled}` + (errs ? ` · ${errs} errors` : '');
      addToast(`Sync ${id}: ${summary}`, errs ? 'alert' : 'success');
      statuses = await configStore.listUserSyncStatus();
    } catch (err: any) {
      addToast(err?.response?.data?.message ?? 'Sync failed', 'alert');
    } finally {
      runningSourceId = null;
    }
  }

  async function runTest() {
    // The test endpoint reads from saved settings, so the source must
    // already exist. Editing-but-not-saved configs require Save first.
    if (editingIndex === null) return;
    if (editingIndex === -1) {
      addToast('Save the source first before testing', 'info');
      return;
    }
    const id = sources[editingIndex].id;
    try {
      const out = await configStore.testUserSync(id);
      testResult = { source_id: id, total: out.total_returned, entries: out.entries };
    } catch (err: any) {
      addToast(err?.response?.data?.message ?? 'Test failed', 'alert');
      testResult = null;
    }
  }

  function formatDate(s?: string): string {
    if (!s) return '';
    return new Date(s).toLocaleString();
  }
</script>

<div>
  <div class="flex items-center justify-between mb-3">
    <div>
      <h2 class="text-base font-semibold text-slate-800 flex items-center gap-2">
        <Users size={16} /> User Sync
      </h2>
      <p class="text-xs text-slate-500 mt-0.5">Provision and refresh users from external directories. Each source owns its synced users + group-mapped permissions; admin grants are preserved.</p>
    </div>
    {#if editingIndex === null}
      <button onclick={startNew}
        class="flex items-center gap-1.5 px-3 py-1.5 bg-blue-500 text-white text-xs font-medium rounded-md hover:bg-blue-600 transition-colors">
        <Plus size={14} /> Add Source
      </button>
    {/if}
  </div>

  {#if editingIndex === null}
    <!-- ── List view ── -->
    {#if sources.length === 0}
      <div class="bg-white rounded-lg border border-slate-200 p-8 text-center text-sm text-slate-500">
        No sync sources configured.
      </div>
    {:else}
      <div class="space-y-2">
        {#each sources as src, idx (src.id)}
          {@const status = findStatus(src.id)}
          <div class="bg-white rounded-lg border border-slate-200 p-3">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="font-medium text-slate-800 text-sm">{src.name}</span>
                  <code class="text-[10px] text-slate-500 bg-slate-100 px-1.5 py-0.5 rounded">{src.id}</code>
                  <span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded font-medium
                    {src.enabled ? 'bg-green-50 text-green-700' : 'bg-slate-100 text-slate-500'}">
                    {src.enabled ? 'enabled' : 'disabled'}
                  </span>
                  <span class="text-[10px] text-slate-500">{status?.schedule_human ?? 'manual'}</span>
                </div>
                <div class="text-[11px] text-slate-500 mt-1 truncate">
                  {src.ldap?.address ?? ''} · {src.ldap?.user_base_dn ?? ''}
                </div>
                {#if status?.last}
                  <div class="text-[11px] text-slate-500 mt-1.5">
                    Last run: {formatDate(status.last.finished_at)} ·
                    Found <span class="text-slate-700 font-medium">{status.last.found}</span> ·
                    Created <span class="text-green-700 font-medium">{status.last.created}</span> ·
                    Updated <span class="text-blue-700 font-medium">{status.last.updated}</span> ·
                    Disabled <span class="text-amber-700 font-medium">{status.last.disabled}</span>
                    {#if status.last.errors?.length}
                      · <span class="text-red-700 font-medium">{status.last.errors.length} errors</span>
                    {/if}
                  </div>
                {/if}
              </div>
              <div class="flex items-center gap-1 shrink-0">
                <button onclick={() => runNow(src.id)} disabled={runningSourceId === src.id}
                  class="px-2 py-1 text-[11px] bg-blue-50 text-blue-700 rounded hover:bg-blue-100 disabled:opacity-50 transition-colors flex items-center gap-1">
                  <RefreshCw size={11} class={runningSourceId === src.id ? 'animate-spin' : ''} />
                  {runningSourceId === src.id ? 'Running…' : 'Sync now'}
                </button>
                <button onclick={() => startEdit(idx)}
                  class="px-2 py-1 text-[11px] bg-slate-100 text-slate-700 rounded hover:bg-slate-200 transition-colors">Edit</button>
                {#if confirmDeleteIdx === idx}
                  <button onclick={() => deleteSource(idx)} class="px-2 py-1 text-[11px] bg-red-500 text-white rounded hover:bg-red-600 transition-colors">Confirm</button>
                  <button onclick={() => { confirmDeleteIdx = null; }} class="px-2 py-1 text-[11px] bg-slate-200 text-slate-600 rounded hover:bg-slate-300 transition-colors">Cancel</button>
                {:else}
                  <button onclick={() => { confirmDeleteIdx = idx; }} class="p-1 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors" title="Delete source">
                    <Trash2 size={14} />
                  </button>
                {/if}
              </div>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  {:else}
    <!-- ── Editor view ── -->
    <div class="bg-white rounded-lg border border-slate-200 p-4 space-y-4">
      <h3 class="text-sm font-semibold text-slate-800">{editingIndex === -1 ? 'New sync source' : `Edit "${sources[editingIndex]?.name}"`}</h3>

      <!-- Identity -->
      <div class="grid grid-cols-2 gap-3">
        <div>
          <label for="src-id" class="block text-xs text-slate-500 mb-1">Source ID</label>
          <input id="src-id" type="text" bind:value={formId} disabled={editingIndex !== -1}
            placeholder="ldap-prod"
            class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:bg-slate-50 disabled:text-slate-500" />
          <p class="text-[10px] text-slate-400 mt-0.5">Stable handle. Used internally as the user_identities provider; cannot be changed after save.</p>
        </div>
        <div>
          <label for="src-name" class="block text-xs text-slate-500 mb-1">Display name</label>
          <input id="src-name" type="text" bind:value={formName}
            placeholder="Production LDAP"
            class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500" />
        </div>
      </div>

      <label class="flex items-center gap-2 text-sm">
        <input type="checkbox" bind:checked={formEnabled} class="rounded border-slate-300 text-blue-500 focus:ring-blue-500" />
        Enabled (periodic schedule fires only when checked)
      </label>

      <!-- Connection -->
      <fieldset class="border border-slate-200 rounded-md p-3 space-y-3">
        <legend class="text-xs font-medium text-slate-600 px-1">LDAP connection</legend>
        <div class="grid grid-cols-2 gap-3">
          <div class="col-span-2">
            <label for="ldap-addr" class="block text-xs text-slate-500 mb-1">Address</label>
            <input id="ldap-addr" type="text" bind:value={formAddress}
              placeholder="ldap://ad.example.com:389 or ldaps://ad.example.com:636"
              class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500" />
            <p class="text-[10px] text-slate-400 mt-0.5">Bare host:port works too — TLS toggle below picks the scheme.</p>
          </div>
        </div>
        <div class="flex gap-4">
          <label class="flex items-center gap-2 text-sm">
            <input type="checkbox" bind:checked={formTLS} class="rounded border-slate-300 text-blue-500 focus:ring-blue-500" />
            TLS
          </label>
          <label class="flex items-center gap-2 text-sm">
            <input type="checkbox" bind:checked={formInsecureSkip} class="rounded border-slate-300 text-blue-500 focus:ring-blue-500" />
            Skip certificate verification
            <span class="text-[10px] text-amber-600">(dev only)</span>
          </label>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label for="ldap-binddn" class="block text-xs text-slate-500 mb-1">Bind DN</label>
            <input id="ldap-binddn" type="text" bind:value={formBindDN}
              placeholder="cn=admin,dc=example,dc=com"
              class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500" />
          </div>
          <div>
            <label for="ldap-bindpw" class="block text-xs text-slate-500 mb-1">Bind password</label>
            <div class="relative">
              <input id="ldap-bindpw" type={showBindPassword ? 'text' : 'password'} bind:value={formBindPassword}
                placeholder={editingIndex !== -1 ? 'leave blank to keep current' : ''}
                class="w-full px-3 py-1.5 pr-9 border border-slate-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500" />
              <button type="button" onclick={() => { showBindPassword = !showBindPassword; }}
                class="absolute right-2 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-600">
                {#if showBindPassword}<EyeOff size={14} />{:else}<Eye size={14} />{/if}
              </button>
            </div>
          </div>
        </div>
      </fieldset>

      <!-- Search -->
      <fieldset class="border border-slate-200 rounded-md p-3 space-y-3">
        <legend class="text-xs font-medium text-slate-600 px-1">Search</legend>
        <div>
          <label for="ldap-base" class="block text-xs text-slate-500 mb-1">User Base DN</label>
          <input id="ldap-base" type="text" bind:value={formUserBaseDN}
            placeholder="ou=people,dc=example,dc=com"
            class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500" />
        </div>
        <div class="grid grid-cols-3 gap-3">
          <div class="col-span-2">
            <label for="ldap-filter" class="block text-xs text-slate-500 mb-1">User filter</label>
            <input id="ldap-filter" type="text" bind:value={formUserFilter}
              placeholder="(objectClass=person)"
              class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500" />
          </div>
          <div>
            <label for="ldap-page" class="block text-xs text-slate-500 mb-1">Page size</label>
            <input id="ldap-page" type="number" min="1" bind:value={formPageSize}
              class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500" />
          </div>
        </div>
      </fieldset>

      <!-- Attribute mapping -->
      <fieldset class="border border-slate-200 rounded-md p-3 space-y-2">
        <legend class="text-xs font-medium text-slate-600 px-1">Attribute mapping</legend>
        <p class="text-[11px] text-slate-500 -mt-1">Each field is the LDAP attribute name to read for that pika user field. Leave blank to skip.</p>
        <div class="grid grid-cols-2 gap-2">
          <div>
            <label class="block text-xs text-slate-500 mb-1">Username</label>
            <input type="text" bind:value={attrUsername} placeholder="required · e.g. uid, sAMAccountName"
              class="w-full px-2 py-1 border border-slate-300 rounded text-[11px] font-mono focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
          <div>
            <label class="block text-xs text-slate-500 mb-1">Subject</label>
            <input type="text" bind:value={attrSubject} placeholder="stable ID; defaults to Username · e.g. entryUUID, objectGUID"
              class="w-full px-2 py-1 border border-slate-300 rounded text-[11px] font-mono focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
          <div>
            <label class="block text-xs text-slate-500 mb-1">Email</label>
            <input type="text" bind:value={attrEmail} placeholder="e.g. mail"
              class="w-full px-2 py-1 border border-slate-300 rounded text-[11px] font-mono focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
          <div>
            <label class="block text-xs text-slate-500 mb-1">Display name</label>
            <input type="text" bind:value={attrDisplayName} placeholder="e.g. displayName, gecos, cn"
              class="w-full px-2 py-1 border border-slate-300 rounded text-[11px] font-mono focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
          <div>
            <label class="block text-xs text-slate-500 mb-1">Given name</label>
            <input type="text" bind:value={attrGivenName} placeholder="optional · e.g. givenName"
              class="w-full px-2 py-1 border border-slate-300 rounded text-[11px] font-mono focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
          <div>
            <label class="block text-xs text-slate-500 mb-1">Surname</label>
            <input type="text" bind:value={attrSurname} placeholder="optional · e.g. sn"
              class="w-full px-2 py-1 border border-slate-300 rounded text-[11px] font-mono focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
          <div class="col-span-2">
            <label class="block text-xs text-slate-500 mb-1">Groups</label>
            <input type="text" bind:value={attrGroups} placeholder="multi-valued · e.g. memberOf — used by Group → Permission map below"
              class="w-full px-2 py-1 border border-slate-300 rounded text-[11px] font-mono focus:outline-none focus:ring-1 focus:ring-blue-500" />
          </div>
        </div>
      </fieldset>

      <!-- Group → permission mapping -->
      <fieldset class="border border-slate-200 rounded-md p-3 space-y-2">
        <legend class="text-xs font-medium text-slate-600 px-1">Group → permissions</legend>
        <p class="text-[11px] text-slate-500 -mt-1">When a user's Groups attribute (e.g. memberOf) contains a value listed here, they're granted the selected permissions. Match is verbatim — for AD-style memberOf, paste the full group DN.</p>
        {#if permissions.length === 0}
          <div class="text-[11px] text-slate-400 italic py-2">No permissions defined yet. Create them under Users → Permissions first.</div>
        {/if}
        {#each groupRows as row, idx (idx)}
          <div class="border border-slate-200 rounded p-2 space-y-1.5 bg-slate-50/50">
            <div class="flex items-center gap-1">
              <input type="text" value={row.group}
                oninput={(e) => updateGroupRow(idx, e.currentTarget.value)}
                placeholder="cn=editors,ou=groups,dc=example,dc=com"
                class="flex-1 px-2 py-1 border border-slate-300 rounded text-[11px] font-mono focus:outline-none focus:ring-1 focus:ring-blue-500" />
              <button type="button" onclick={() => removeGroupRow(idx)}
                class="p-1 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors" title="Remove">
                <Trash2 size={12} />
              </button>
            </div>
            {#if permissions.length > 0}
              <div class="flex flex-wrap gap-1.5">
                {#each permissions as perm (perm.id)}
                  {@const checked = row.permissionIds.includes(perm.id)}
                  <button type="button" onclick={() => toggleGroupPermission(idx, perm.id)}
                    class="text-[10px] px-2 py-0.5 rounded border transition-colors
                      {checked ? 'bg-blue-50 border-blue-300 text-blue-700 font-medium' : 'bg-white border-slate-200 text-slate-600 hover:border-slate-300'}">
                    {perm.name}
                  </button>
                {/each}
              </div>
            {/if}
          </div>
        {/each}
        <button type="button" onclick={addGroupRow}
          class="text-[11px] text-blue-600 hover:underline">+ Add group mapping</button>
      </fieldset>

      <!-- Schedule -->
      <fieldset class="border border-slate-200 rounded-md p-3 space-y-2">
        <legend class="text-xs font-medium text-slate-600 px-1">Schedule</legend>
        <div class="flex items-center gap-3 text-sm">
          <label class="flex items-center gap-1.5">
            <input type="radio" name="sch-mode" value="manual" checked={scheduleMode === 'manual'} onchange={() => { scheduleMode = 'manual'; }} />
            Manual only
          </label>
          <label class="flex items-center gap-1.5">
            <input type="radio" name="sch-mode" value="interval" checked={scheduleMode === 'interval'} onchange={() => { scheduleMode = 'interval'; }} />
            Every
          </label>
          <input type="number" min="1" bind:value={scheduleMinutes} disabled={scheduleMode !== 'interval'}
            class="w-20 px-2 py-1 border border-slate-300 rounded text-sm focus:outline-none focus:ring-1 focus:ring-blue-500 disabled:bg-slate-50" />
          <span class="text-sm text-slate-500">minutes</span>
        </div>
        <p class="text-[11px] text-slate-400">Manual + JIT (first-login auto-provision via the LDAP login strategy) always work regardless of schedule.</p>
      </fieldset>

      <!-- On missing -->
      <div>
        <label for="on-missing" class="block text-xs text-slate-500 mb-1">When a user disappears from the directory</label>
        <select id="on-missing" bind:value={onMissing}
          class="px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
          <option value="disable">Disable the local user (reversible)</option>
          <option value="ignore">Leave the local user alone</option>
        </select>
        <p class="text-[10px] text-slate-400 mt-0.5">Only users provisioned by this source are affected. Local users and users from other sources are never touched.</p>
      </div>

      <!-- Test result panel -->
      {#if testResult}
        <div class="border border-slate-200 rounded-md p-3 bg-slate-50 text-[11px]">
          <div class="font-medium text-slate-700 mb-1.5">Test results · {testResult.total} entries returned</div>
          {#if testResult.entries.length === 0}
            <div class="text-slate-500 italic">No matches. Verify Base DN + filter.</div>
          {:else}
            <ul class="space-y-2">
              {#each testResult.entries as e}
                <li class="border border-slate-200 rounded p-2 bg-white">
                  <div class="font-mono text-slate-600 truncate" title={e.dn}>{e.dn}</div>
                  <div class="text-slate-500 mt-1 space-y-0.5">
                    {#each Object.entries(e.attributes) as [k, vals]}
                      <div class="font-mono"><span class="text-slate-400">{k}:</span> {vals.join(', ')}</div>
                    {/each}
                  </div>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {/if}

      <div class="flex items-center gap-2 pt-2 border-t border-slate-100">
        <button onclick={saveCurrent} disabled={saving}
          class="px-4 py-1.5 bg-blue-500 text-white text-sm rounded-md hover:bg-blue-600 disabled:opacity-50 transition-colors">
          {saving ? 'Saving…' : 'Save'}
        </button>
        <button onclick={cancelEdit}
          class="px-4 py-1.5 bg-white border border-slate-300 text-slate-600 text-sm rounded-md hover:bg-slate-50 transition-colors">
          Cancel
        </button>
        {#if editingIndex !== -1}
          <button onclick={runTest}
            class="ml-auto px-3 py-1.5 bg-amber-50 text-amber-700 text-sm rounded-md hover:bg-amber-100 transition-colors flex items-center gap-1.5">
            <FlaskConical size={13} /> Test (5 entries)
          </button>
        {/if}
      </div>
    </div>
  {/if}
</div>


