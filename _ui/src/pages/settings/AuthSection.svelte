<script lang="ts">
 import { addToast } from "@/lib/store/toast.svelte";
 import { appStore } from "@/lib/store/store.svelte";
 import { onMount } from "svelte";
 import { ChevronDown, ChevronRight, Plus, Trash2 } from "lucide-svelte";
 import axios from 'axios';

 // Capability catalog is served by /api/v1/info (appStore.info.capabilities)
 // and carries key + human-readable name + description for every
 // capability the server recognizes. Using that as the source of truth for
 // the role/scope checkbox grids keeps the UI in lockstep with the
 // backend's actual enforcement surface.
 const knownCapabilities = $derived(appStore.info?.capabilities ?? []);

 // ── Auth settings shape ──
 interface OAuth2Entry {
 name: string;
 display_name?: string;
 issuer_url?: string;
 client_id?: string;
 client_secret?: string;
 scopes?: string[];
 disable_pkce?: boolean;
 password_flow?: boolean;
 }

 interface AuthSettings {
 ui?: {
 title?: string;
 subtitle?: string;
 icon?: string;
 // Note: version is intentionally absent. The login UI displays the
 // server version from /api/v1/info (build-time ldflags), not from
 // editable settings, so the operator can't desync the displayed
 // version from the actual binary.
 theme?: Record<string, string>;
 custom_css_url?: string;
 };
 cookie?: {
 name?: string;
 domain?: string;
 path?: string;
 secure?: boolean;
 http_only?: boolean;
 same_site?: string;
 };
 issuer?: {
 access_ttl?: number;
 refresh_ttl?: number;
 rotate_refresh?: boolean;
 };
 local?: { enabled: boolean; name?: string };
 oauth2?: OAuth2Entry[];
 ldap?: {
 name?: string;
 addr?: string;
 bind_dn?: string;
 bind_password?: string;
 };
 header?: {
 name?: string;
 user?: string;
 email?: string;
 display_name_header?: string;
 roles?: string;
 groups?: string;
 trusted_proxies?: string[];
 };
 // Passkey strategy config (WebAuthn). Per-user enrollment lives in
 // /api/v1/me/passkeys; this block only describes RP identity + UV
 // policy. challenge_ttl is a Go time.Duration (nanoseconds) on the
 // wire, normalised to seconds in the form.
 passkey?: {
 enabled: boolean;
 name?: string;
 label?: string;
 rp_id?: string;
 rp_display_name?: string;
 rp_origins?: string[];
 user_verification?: string;
 challenge_ttl?: number;
 };
 // Access tokens are accepted only via `Authorization: Bearer <key>`.
 // No tunable header, no fallback — the settings surface would imply
 // flexibility that isn't actually wanted.
 capabilities?: {
 superadmins?: string[];
 role_mapping?: Record<string, string[]>;
 scope_mapping?: Record<string, string[]>;
 };
 rate_limit?: {
 enabled?: boolean;
 // Durations are Go time.Duration (nanoseconds) on the wire.
 window?: number;
 ip_soft_threshold?: number;
 ip_hard_threshold?: number;
 user_soft_threshold?: number;
 user_hard_threshold?: number;
 backoff_base?: number;
 backoff_max?: number;
 trusted_proxy_cidrs?: string[];
 };
 }

 // ── Duration helpers ──
 // The backend serializes time.Duration as nanoseconds (Go's default
 // json.Marshal for time.Duration). The UI works in seconds for the user's
 // sake; the helpers below convert at the boundaries.
 const NS_PER_SECOND = 1_000_000_000;
 function nsToSec(v: number | undefined): number | '' {
 if (v == null || v === 0) return '';
 return Math.round(v / NS_PER_SECOND);
 }
 function secToNs(v: number | string): number {
 const n = typeof v === 'string' ? Number(v) : v;
 if (!Number.isFinite(n) || n <= 0) return 0;
 return Math.round(n * NS_PER_SECOND);
 }

 // ── State ──
 let saving = $state(false);
 let loadError = $state('');
 let restartRequired = $state(false);

 // Collapsible sections
 let openSections = $state<Set<string>>(new Set(['ui', 'local']));

 function toggleSection(key: string) {
 const next = new Set(openSections);
 if (next.has(key)) next.delete(key); else next.add(key);
 openSections = next;
 }

 // UI fields
 let uiTitle = $state('');
 let uiSubtitle = $state('');
 let uiIcon = $state('');

 let uiCustomCSSUrl = $state('');

 // Cookie fields
 let cookieName = $state('');
 let cookieDomain = $state('');
 let cookiePath = $state('');
 let cookieSecure = $state(false);
 let cookieHttpOnly = $state(false);
 let cookieSameSite = $state('');

 // Issuer fields
 let issuerAccessTTL = $state<number | ''>('');
 let issuerRefreshTTL = $state<number | ''>('');
 let issuerRotateRefresh = $state(false);

 // Local strategy
 let localEnabled = $state(false);
 let localName = $state('');

 // OAuth2 entries
 let oauth2Entries = $state<OAuth2Entry[]>([]);
 let oauth2ScopeInputs = $state<string[]>([]);

 // LDAP
 let ldapName = $state('');
 let ldapAddr = $state('');
 let ldapBindDN = $state('');
 let ldapBindPassword = $state('');
 let ldapBindPasswordChanged = $state(false);

 // Header
 let headerName = $state('');
 let headerUser = $state('');
 let headerEmail = $state('');
 let headerDisplayName = $state('');
 let headerRoles = $state('');
 let headerGroups = $state('');
 let headerTrustedProxies = $state<string[]>([]);
 let headerTrustedProxyInput = $state('');

 // Passkey (WebAuthn). RP ID + Origins are required for the feature to
 // actually run; the BuildPasskeyEngine helper on the backend treats
 // empty RPID / origins as "feature off" silently, so the form flags
 // them as required when Enable is checked.
 let passkeyEnabled = $state(false);
 let passkeyName = $state('');
 let passkeyLabel = $state('');
 let passkeyRPID = $state('');
 let passkeyRPDisplayName = $state('');
 let passkeyRPOrigins = $state<string[]>([]);
 let passkeyRPOriginInput = $state('');
 let passkeyUserVerification = $state('');
 let passkeyChallengeTTLSec = $state<number | ''>('');

 function addPasskeyOrigin() {
 const v = passkeyRPOriginInput.trim();
 if (!v) return;
 if (passkeyRPOrigins.includes(v)) return;
 passkeyRPOrigins = [...passkeyRPOrigins, v];
 passkeyRPOriginInput = '';
 }
 function removePasskeyOrigin(i: number) {
 passkeyRPOrigins = passkeyRPOrigins.filter((_, idx) => idx !== i);
 }

 // Validation surfaces: RPID must be a bare host (ada rejects /:?#),
 // and at least one origin is required when Enable is checked.
 const passkeyRPIDInvalid = $derived(
 passkeyEnabled && passkeyRPID.length > 0 && /[\/:?#]/.test(passkeyRPID)
 );
 const passkeyOriginsMissing = $derived(passkeyEnabled && passkeyRPOrigins.length === 0);
 const passkeyRPIDMissing = $derived(passkeyEnabled && !passkeyRPID.trim());

 // Capabilities — Superadmins (Identity.Subject allowlist)
 let capSuperadmins = $state<string[]>([]);
 let capSuperadminInput = $state('');

 // Capabilities — role/scope mappings (for external identities: OAuth2,
 // Header, LDAP). Rows are the edit-time UI representation; each row is a
 // key (role or scope name as it appears in the identity) paired with a
 // set of pika capability keys granted when that role/scope is present.
 type MappingRow = { key: string; capabilities: string[] };
 let capRoleMappings = $state<MappingRow[]>([]);
 let capScopeMappings = $state<MappingRow[]>([]);
 let capNewRoleKey = $state('');
 let capNewScopeKey = $state('');

 // Convert a Record<string, string[]> into edit-friendly row array.
 // Stable order is alphabetical by key so the form doesn't jitter across
 // reloads (JSON object iteration order is technically insertion-preserving
 // but server-side encoding may not preserve it).
 function mapToRows(m: Record<string, string[]> | undefined): MappingRow[] {
 if (!m) return [];
 return Object.keys(m)
 .sort()
 .map((k) => ({ key: k, capabilities: [...(m[k] ?? [])] }));
 }

 // Convert back to the wire format, dropping rows with empty keys and
 // deduplicating capability slices.
 function rowsToMap(rows: MappingRow[]): Record<string, string[]> | undefined {
 const out: Record<string, string[]> = {};
 for (const row of rows) {
 const k = row.key.trim();
 if (!k) continue;
 const caps = Array.from(new Set(row.capabilities.filter((c) => !!c)));
 if (caps.length === 0) continue;
 out[k] = caps;
 }
 return Object.keys(out).length > 0 ? out : undefined;
 }

 function addRoleMapping() {
 const k = capNewRoleKey.trim();
 if (!k) return;
 if (capRoleMappings.some((r) => r.key === k)) return;
 capRoleMappings = [...capRoleMappings, { key: k, capabilities: [] }];
 capNewRoleKey = '';
 }
 function removeRoleMapping(i: number) {
 capRoleMappings = capRoleMappings.filter((_, idx) => idx !== i);
 }
 function toggleRoleCap(rowIdx: number, capKey: string) {
 capRoleMappings = capRoleMappings.map((r, i) => {
 if (i !== rowIdx) return r;
 const has = r.capabilities.includes(capKey);
 return {
 key: r.key,
 capabilities: has
 ? r.capabilities.filter((c) => c !== capKey)
 : [...r.capabilities, capKey],
 };
 });
 }

 function addScopeMapping() {
 const k = capNewScopeKey.trim();
 if (!k) return;
 if (capScopeMappings.some((r) => r.key === k)) return;
 capScopeMappings = [...capScopeMappings, { key: k, capabilities: [] }];
 capNewScopeKey = '';
 }
 function removeScopeMapping(i: number) {
 capScopeMappings = capScopeMappings.filter((_, idx) => idx !== i);
 }
 function toggleScopeCap(rowIdx: number, capKey: string) {
 capScopeMappings = capScopeMappings.map((r, i) => {
 if (i !== rowIdx) return r;
 const has = r.capabilities.includes(capKey);
 return {
 key: r.key,
 capabilities: has
 ? r.capabilities.filter((c) => c !== capKey)
 : [...r.capabilities, capKey],
 };
 });
 }

 // Rate limit
 let rlEnabled = $state(true);
 let rlWindowSec = $state<number | ''>('');
 let rlIPSoft = $state<number | ''>('');
 let rlIPHard = $state<number | ''>('');
 let rlUserSoft = $state<number | ''>('');
 let rlUserHard = $state<number | ''>('');
 let rlBackoffBaseSec = $state<number | ''>('');
 let rlBackoffMaxSec = $state<number | ''>('');
 let rlTrustedProxies = $state<string[]>([]);
 let rlTrustedProxyInput = $state('');

 function addRlTrustedProxy() {
 const v = rlTrustedProxyInput.trim();
 if (!v) return;
 rlTrustedProxies = [...rlTrustedProxies, v];
 rlTrustedProxyInput = '';
 }
 function removeRlTrustedProxy(i: number) {
 rlTrustedProxies = rlTrustedProxies.filter((_, idx) => idx !== i);
 }

 function loadFromSettings(auth: AuthSettings) {
 uiTitle = auth.ui?.title ?? '';
 uiSubtitle = auth.ui?.subtitle ?? '';
 uiIcon = auth.ui?.icon ?? '';
 uiCustomCSSUrl = auth.ui?.custom_css_url ?? '';

 cookieName = auth.cookie?.name ?? '';
 cookieDomain = auth.cookie?.domain ?? '';
 cookiePath = auth.cookie?.path ?? '';
 cookieSecure = auth.cookie?.secure ?? false;
 cookieHttpOnly = auth.cookie?.http_only ?? false;
 cookieSameSite = auth.cookie?.same_site ?? '';

 issuerAccessTTL = auth.issuer?.access_ttl ?? '';
 issuerRefreshTTL = auth.issuer?.refresh_ttl ?? '';
 issuerRotateRefresh = auth.issuer?.rotate_refresh ?? false;

 localEnabled = auth.local?.enabled ?? false;
 localName = auth.local?.name ?? '';

 oauth2Entries = (auth.oauth2 ?? []).map((e) => ({ ...e, client_secret: '' }));
 oauth2ScopeInputs = oauth2Entries.map(() => '');

 ldapName = auth.ldap?.name ?? '';
 ldapAddr = auth.ldap?.addr ?? '';
 ldapBindDN = auth.ldap?.bind_dn ?? '';
 ldapBindPassword = '';
 ldapBindPasswordChanged = false;

 headerName = auth.header?.name ?? '';
 headerUser = auth.header?.user ?? '';
 headerEmail = auth.header?.email ?? '';
 headerDisplayName = auth.header?.display_name_header ?? '';
 headerRoles = auth.header?.roles ?? '';
 headerGroups = auth.header?.groups ?? '';
 headerTrustedProxies = [...(auth.header?.trusted_proxies ?? [])];
 headerTrustedProxyInput = '';

 // Passkey
 passkeyEnabled = auth.passkey?.enabled ?? false;
 passkeyName = auth.passkey?.name ?? '';
 passkeyLabel = auth.passkey?.label ?? '';
 passkeyRPID = auth.passkey?.rp_id ?? '';
 passkeyRPDisplayName = auth.passkey?.rp_display_name ?? '';
 passkeyRPOrigins = [...(auth.passkey?.rp_origins ?? [])];
 passkeyRPOriginInput = '';
 passkeyUserVerification = auth.passkey?.user_verification ?? '';
 passkeyChallengeTTLSec = nsToSec(auth.passkey?.challenge_ttl);

 capSuperadmins = [...(auth.capabilities?.superadmins ?? [])];
 capSuperadminInput = '';
 capRoleMappings = mapToRows(auth.capabilities?.role_mapping);
 capScopeMappings = mapToRows(auth.capabilities?.scope_mapping);
 capNewRoleKey = '';
 capNewScopeKey = '';

 // Rate limit (if absent, leave fields empty so backend defaults apply)
 const rl = auth.rate_limit;
 rlEnabled = rl?.enabled ?? true;
 rlWindowSec = nsToSec(rl?.window);
 rlIPSoft = rl?.ip_soft_threshold ?? '';
 rlIPHard = rl?.ip_hard_threshold ?? '';
 rlUserSoft = rl?.user_soft_threshold ?? '';
 rlUserHard = rl?.user_hard_threshold ?? '';
 rlBackoffBaseSec = nsToSec(rl?.backoff_base);
 rlBackoffMaxSec = nsToSec(rl?.backoff_max);
 rlTrustedProxies = [...(rl?.trusted_proxy_cidrs ?? [])];
 rlTrustedProxyInput = '';
 }

 function buildPayload(): AuthSettings {
 const auth: AuthSettings = {};

 // UI
 const uiBlock: AuthSettings['ui'] = {};
 if (uiTitle) uiBlock.title = uiTitle;
 if (uiSubtitle) uiBlock.subtitle = uiSubtitle;
 if (uiIcon) uiBlock.icon = uiIcon;
 if (uiCustomCSSUrl) uiBlock.custom_css_url = uiCustomCSSUrl;
 if (Object.keys(uiBlock).length > 0) auth.ui = uiBlock;

 // Cookie
 const cookieBlock: AuthSettings['cookie'] = {};
 if (cookieName) cookieBlock.name = cookieName;
 if (cookieDomain) cookieBlock.domain = cookieDomain;
 if (cookiePath) cookieBlock.path = cookiePath;
 if (cookieSecure) cookieBlock.secure = true;
 if (cookieHttpOnly) cookieBlock.http_only = true;
 if (cookieSameSite) cookieBlock.same_site = cookieSameSite;
 if (Object.keys(cookieBlock).length > 0) auth.cookie = cookieBlock;

 // Issuer
 const issuerBlock: AuthSettings['issuer'] = {};
 if (issuerAccessTTL !== '') issuerBlock.access_ttl = Number(issuerAccessTTL);
 if (issuerRefreshTTL !== '') issuerBlock.refresh_ttl = Number(issuerRefreshTTL);
 if (issuerRotateRefresh) issuerBlock.rotate_refresh = true;
 if (Object.keys(issuerBlock).length > 0) auth.issuer = issuerBlock;

 // Local
 auth.local = { enabled: localEnabled };
 if (localName) auth.local.name = localName;

 // OAuth2
 if (oauth2Entries.length > 0) {
 auth.oauth2 = oauth2Entries.map((e) => {
 const entry: OAuth2Entry = { name: e.name };
 if (e.display_name) entry.display_name = e.display_name;
 if (e.issuer_url) entry.issuer_url = e.issuer_url;
 if (e.client_id) entry.client_id = e.client_id;
 if (e.client_secret) entry.client_secret = e.client_secret;
 if (e.scopes && e.scopes.length > 0) entry.scopes = e.scopes;
 if (e.disable_pkce) entry.disable_pkce = true;
 if (e.password_flow) entry.password_flow = true;
 return entry;
 });
 }

 // LDAP
 const ldapBlock: AuthSettings['ldap'] = {};
 if (ldapName) ldapBlock.name = ldapName;
 if (ldapAddr) ldapBlock.addr = ldapAddr;
 if (ldapBindDN) ldapBlock.bind_dn = ldapBindDN;
 if (ldapBindPasswordChanged && ldapBindPassword) ldapBlock.bind_password = ldapBindPassword;
 if (Object.keys(ldapBlock).length > 0) auth.ldap = ldapBlock;

 // Header
 const headerBlock: AuthSettings['header'] = {};
 if (headerName) headerBlock.name = headerName;
 if (headerUser) headerBlock.user = headerUser;
 if (headerEmail) headerBlock.email = headerEmail;
 if (headerDisplayName) headerBlock.display_name_header = headerDisplayName;
 if (headerRoles) headerBlock.roles = headerRoles;
 if (headerGroups) headerBlock.groups = headerGroups;
 if (headerTrustedProxies.length > 0) headerBlock.trusted_proxies = [...headerTrustedProxies];
 if (Object.keys(headerBlock).length > 0) auth.header = headerBlock;

 // Passkey — always emit the block so toggling enabled=false persists
 // (mirrors how Local is handled). Optional fields are omitted when
 // empty to keep the saved JSON tidy.
 const passkeyBlock: NonNullable<AuthSettings['passkey']> = { enabled: passkeyEnabled };
 if (passkeyName) passkeyBlock.name = passkeyName;
 if (passkeyLabel) passkeyBlock.label = passkeyLabel;
 if (passkeyRPID) passkeyBlock.rp_id = passkeyRPID;
 if (passkeyRPDisplayName) passkeyBlock.rp_display_name = passkeyRPDisplayName;
 if (passkeyRPOrigins.length > 0) passkeyBlock.rp_origins = [...passkeyRPOrigins];
 if (passkeyUserVerification) passkeyBlock.user_verification = passkeyUserVerification;
 if (passkeyChallengeTTLSec !== '') passkeyBlock.challenge_ttl = secToNs(passkeyChallengeTTLSec);
 auth.passkey = passkeyBlock;

 // Capabilities — build a single block combining superadmins and the
 // role/scope → capability mappings. Each sub-field is omitted when
 // empty so the saved JSON stays tidy; a block with zero entries is
 // omitted entirely.
 const capsBlock: NonNullable<AuthSettings['capabilities']> = {};
 if (capSuperadmins.length > 0) capsBlock.superadmins = [...capSuperadmins];
 const roleMap = rowsToMap(capRoleMappings);
 if (roleMap) capsBlock.role_mapping = roleMap;
 const scopeMap = rowsToMap(capScopeMappings);
 if (scopeMap) capsBlock.scope_mapping = scopeMap;
 if (Object.keys(capsBlock).length > 0) auth.capabilities = capsBlock;

 // Rate limit — always include the block so toggling Enabled persists.
 const rlBlock: AuthSettings['rate_limit'] = { enabled: rlEnabled };
 if (rlWindowSec !== '') rlBlock.window = secToNs(rlWindowSec);
 if (rlIPSoft !== '') rlBlock.ip_soft_threshold = Number(rlIPSoft);
 if (rlIPHard !== '') rlBlock.ip_hard_threshold = Number(rlIPHard);
 if (rlUserSoft !== '') rlBlock.user_soft_threshold = Number(rlUserSoft);
 if (rlUserHard !== '') rlBlock.user_hard_threshold = Number(rlUserHard);
 if (rlBackoffBaseSec !== '') rlBlock.backoff_base = secToNs(rlBackoffBaseSec);
 if (rlBackoffMaxSec !== '') rlBlock.backoff_max = secToNs(rlBackoffMaxSec);
 if (rlTrustedProxies.length > 0) rlBlock.trusted_proxy_cidrs = [...rlTrustedProxies];
 auth.rate_limit = rlBlock;

 return auth;
 }

 async function handleSave() {
 saving = true;
 restartRequired = false;
 try {
 const response = await axios.post('/api/v1/settings', {
 action: 'set',
 auth: buildPayload(),
 });
 if (response.data?.restart_required) {
 restartRequired = true;
 }
 addToast('Auth settings saved', 'success');
 } catch (err: any) {
 const msg = err?.response?.data?.message || 'Failed to save auth settings';
 addToast(msg, 'alert');
 } finally {
 saving = false;
 }
 }

 // OAuth2 helpers
 function addOAuth2() {
 oauth2Entries = [...oauth2Entries, { name: '', scopes: [] }];
 oauth2ScopeInputs = [...oauth2ScopeInputs, ''];
 }

 function removeOAuth2(i: number) {
 oauth2Entries = oauth2Entries.filter((_, idx) => idx !== i);
 oauth2ScopeInputs = oauth2ScopeInputs.filter((_, idx) => idx !== i);
 }

 function addOAuth2Scope(i: number) {
 const v = oauth2ScopeInputs[i]?.trim();
 if (!v) return;
 const entry = oauth2Entries[i];
 if (!entry) return;
 entry.scopes = [...(entry.scopes ?? []), v];
 oauth2Entries = [...oauth2Entries];
 oauth2ScopeInputs = oauth2ScopeInputs.map((s, idx) => idx === i ? '' : s);
 }

 function removeOAuth2Scope(entryIdx: number, scopeIdx: number) {
 const entry = oauth2Entries[entryIdx];
 if (!entry) return;
 entry.scopes = (entry.scopes ?? []).filter((_, i) => i !== scopeIdx);
 oauth2Entries = [...oauth2Entries];
 }

 // Trusted proxy helpers
 function addTrustedProxy() {
 const v = headerTrustedProxyInput.trim();
 if (!v) return;
 headerTrustedProxies = [...headerTrustedProxies, v];
 headerTrustedProxyInput = '';
 }

 function removeTrustedProxy(i: number) {
 headerTrustedProxies = headerTrustedProxies.filter((_, idx) => idx !== i);
 }

 // Capabilities helpers
 function addSuperadmin() {
 const v = capSuperadminInput.trim();
 if (!v) return;
 if (capSuperadmins.includes(v)) return;
 capSuperadmins = [...capSuperadmins, v];
 capSuperadminInput = '';
 }

 function removeSuperadmin(i: number) {
 capSuperadmins = capSuperadmins.filter((_, idx) => idx !== i);
 }

 onMount(async () => {
 try {
 const response = await axios.get('/api/v1/settings');
 loadFromSettings(response.data?.auth || {});
 } catch (err: any) {
 loadError = err?.response?.data?.message || 'Failed to load settings';
 }
 });
</script>

<div>
 <div class="mb-4">
 <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">Authentication</h2>
 <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
 Configure ada auth strategies, cookie settings, token issuance, and identity providers.
 </p>
 </div>

 {#if loadError}
 <div class="mb-4 p-3 bg-red-50 border border-red-200 rounded-md text-sm text-red-700">{loadError}</div>
 {/if}

 {#if restartRequired}
 <div class="mb-4 p-3 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800">
 A server restart is required for some changes to take effect.
 </div>
 {/if}

 <!-- ── UI section ── -->
 <div class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden">
 <button
 type="button"
 class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => toggleSection('ui')}
 >
 Login UI
 {#if openSections.has('ui')}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
 </button>
 {#if openSections.has('ui')}
 <div class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700">
 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Title</label>
 <input type="text" bind:value={uiTitle} placeholder="pika" class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Subtitle</label>
 <input type="text" bind:value={uiSubtitle} placeholder="optional" class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Icon URL / data URI</label>
 <input type="text" bind:value={uiIcon} placeholder="https://example.com/logo.png" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Custom CSS URL</label>
 <input type="text" bind:value={uiCustomCSSUrl} placeholder="https://example.com/login.css" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 {/if}
 </div>

 <!-- ── Cookie section ── -->
 <div class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden">
 <button
 type="button"
 class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => toggleSection('cookie')}
 >
 Cookie
 {#if openSections.has('cookie')}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
 </button>
 {#if openSections.has('cookie')}
 <div class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700">
 <div class="grid grid-cols-3 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Cookie Name</label>
 <input type="text" bind:value={cookieName} placeholder="pika_session" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Domain</label>
 <input type="text" bind:value={cookieDomain} placeholder=".example.com" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Path</label>
 <input type="text" bind:value={cookiePath} placeholder="/" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">SameSite</label>
 <select bind:value={cookieSameSite} class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10">
 <option value="">Default</option>
 <option value="Lax">Lax</option>
 <option value="Strict">Strict</option>
 <option value="None">None</option>
 </select>
 </div>
 <div class="flex flex-col gap-2 pt-5">
 <label class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer">
 <input type="checkbox" bind:checked={cookieSecure} class="rounded border-slate-300" />
 Secure
 </label>
 <label class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer">
 <input type="checkbox" bind:checked={cookieHttpOnly} class="rounded border-slate-300" />
 HttpOnly
 </label>
 </div>
 </div>
 </div>
 {/if}
 </div>

 <!-- ── Issuer section ── -->
 <div class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden">
 <button
 type="button"
 class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => toggleSection('issuer')}
 >
 Token Issuance
 {#if openSections.has('issuer')}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
 </button>
 {#if openSections.has('issuer')}
 <div class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700">
 <p class="text-xs text-slate-500 dark:text-slate-400 mt-3 leading-relaxed">
 Controls how long a signed-in session stays alive. Every login
 produces a short-lived <span class="font-medium">access token</span>
 (validates each request) and a longer <span class="font-medium">refresh token</span>
 (used behind the scenes to silently mint a new access token when
 the old one expires — the user isn't prompted). Both live
 server-side; only an opaque session cookie reaches the browser.
 </p>
 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Access TTL (seconds)</label>
 <p class="text-[11px] text-slate-400 dark:text-slate-500 mb-2 leading-relaxed">
 How often the access token is refreshed under the hood.
 Shorter = faster reaction to admin actions (kick, permission
 change, disable). Default 900 (15 min). Users notice nothing.
 </p>
 <input type="number" bind:value={issuerAccessTTL} placeholder="900" class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Refresh TTL (seconds)</label>
 <p class="text-[11px] text-slate-400 dark:text-slate-500 mb-2 leading-relaxed">
 How long the refresh token lasts. When it expires the user
 is forced back to the login screen. Default 86400 (1 day);
 604800 = 1 week. Behavior depends on the rotation setting
 below.
 </p>
 <input type="number" bind:value={issuerRefreshTTL} placeholder="86400" class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div class="pt-2">
 <label class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer">
 <input type="checkbox" bind:checked={issuerRotateRefresh} class="rounded border-slate-300" />
 Rotate refresh tokens on use
 </label>
 <p class="text-[11px] text-slate-400 dark:text-slate-500 mt-1.5 leading-relaxed">
 <span class="font-medium text-slate-600 dark:text-slate-300">Off (default):</span>
 the Refresh TTL is a <em>hard ceiling</em>. Even a user who
 visits every day is forced back to the login screen once Refresh
 TTL elapses from their original login time.
 <br />
 <span class="font-medium text-slate-600 dark:text-slate-300">On:</span> the
 refresh token is replaced with a new one every time it's used,
 restarting the TTL clock. An active user stays logged in
 indefinitely; only a gap of inactivity longer than Refresh TTL
 forces re-login. Also mitigates refresh-token theft — a stolen
 token becomes unusable as soon as the legitimate user refreshes.
 </p>
 </div>
 </div>
 {/if}
 </div>

 <!-- ── Local strategy ── -->
 <div class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden">
 <button
 type="button"
 class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => toggleSection('local')}
 >
 Local (Password) Strategy
 {#if openSections.has('local')}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
 </button>
 {#if openSections.has('local')}
 <div class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700">
 <label class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer">
 <input type="checkbox" bind:checked={localEnabled} class="rounded border-slate-300" />
 Enable local username/password authentication
 </label>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Strategy label</label>
 <input type="text" bind:value={localName} placeholder="Local" class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 {/if}
 </div>

 <!-- ── OAuth2 strategies ── -->
 <div class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden">
 <button
 type="button"
 class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => toggleSection('oauth2')}
 >
 OAuth2 Providers
 {#if openSections.has('oauth2')}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
 </button>
 {#if openSections.has('oauth2')}
 <div class="px-5 pb-5 pt-3 border-t border-slate-100 dark:border-warm-700 space-y-4">
 {#each oauth2Entries as entry, i}
 <div class="p-4 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg space-y-3">
 <div class="flex items-center justify-between mb-1">
 <span class="text-xs font-semibold text-slate-600 dark:text-slate-300">Provider #{i + 1}</span>
 <button type="button" onclick={() => removeOAuth2(i)} class="p-1 text-slate-400 dark:text-slate-500 hover:text-red-500 transition-colors">
 <Trash2 size={13} />
 </button>
 </div>
 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Name (URL key)</label>
 <input type="text" bind:value={entry.name} placeholder="google" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Display Name</label>
 <input type="text" bind:value={entry.display_name} placeholder="Google" class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Issuer URL</label>
 <input type="text" bind:value={entry.issuer_url} placeholder="https://accounts.google.com" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Client ID</label>
 <input type="text" bind:value={entry.client_id} class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Client Secret</label>
 <input type="password" bind:value={entry.client_secret} placeholder="(leave blank to keep)" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500">Leave blank to keep existing secret.</p>
 </div>
 </div>
 <!-- Scopes -->
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Scopes</label>
 <div class="flex gap-2">
 <input type="text" bind:value={oauth2ScopeInputs[i]} placeholder="openid"
 onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addOAuth2Scope(i); } }}
 class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <button type="button" class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer" onclick={() => addOAuth2Scope(i)}>Add</button>
 </div>
 {#if (entry.scopes ?? []).length > 0}
 <div class="mt-2 flex flex-wrap gap-1.5">
 {#each entry.scopes ?? [] as scope, si}
 <span class="inline-flex items-center gap-1 px-2 py-1 bg-accent-50 border border-accent-200 rounded text-xs font-mono text-brand-700">
 {scope}
 <button type="button" class="w-3.5 h-3.5 flex items-center justify-center bg-transparent border-none cursor-pointer text-brand-400 hover:text-red-500" onclick={() => removeOAuth2Scope(i, si)}>&times;</button>
 </span>
 {/each}
 </div>
 {/if}
 </div>
 <div class="flex gap-4">
 <label class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="checkbox" bind:checked={entry.disable_pkce} class="rounded border-slate-300" />
 Disable PKCE
 </label>
 <label class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300 cursor-pointer">
 <input type="checkbox" bind:checked={entry.password_flow} class="rounded border-slate-300" />
 Password flow
 </label>
 </div>
 </div>
 {/each}
 <button
 type="button"
 onclick={addOAuth2}
 class="flex items-center gap-1.5 px-3 py-2 text-sm text-accent-700 bg-accent-50 rounded-md hover:bg-accent-100 transition-colors cursor-pointer"
 >
 <Plus size={13} /> Add Provider
 </button>
 </div>
 {/if}
 </div>

 <!-- ── LDAP ── -->
 <div class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden">
 <button
 type="button"
 class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => toggleSection('ldap')}
 >
 LDAP
 {#if openSections.has('ldap')}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
 </button>
 {#if openSections.has('ldap')}
 <div class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700">
 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Strategy label</label>
 <input type="text" bind:value={ldapName} placeholder="LDAP" class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Server Address</label>
 <input type="text" bind:value={ldapAddr} placeholder="ldap://ldap.example.com:389" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Bind DN</label>
 <input type="text" bind:value={ldapBindDN} placeholder="cn=admin,dc=example,dc=com" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Bind Password</label>
 <input type="password" bind:value={ldapBindPassword}
 oninput={() => ldapBindPasswordChanged = true}
 placeholder="(secret set — leave blank to keep)"
 class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500">Leave blank to keep existing password.</p>
 </div>
 </div>
 {/if}
 </div>

 <!-- ── Header strategy ── -->
 <div class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden">
 <button
 type="button"
 class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => toggleSection('header')}
 >
 Header / Proxy Auth
 {#if openSections.has('header')}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
 </button>
 {#if openSections.has('header')}
 <div class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700">
 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Strategy label</label>
 <input type="text" bind:value={headerName} placeholder="Header" class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">User Header</label>
 <input type="text" bind:value={headerUser} placeholder="X-Remote-User" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Email Header</label>
 <input type="text" bind:value={headerEmail} placeholder="X-Remote-Email" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Display Name Header</label>
 <input type="text" bind:value={headerDisplayName} placeholder="X-Remote-Name" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Roles Header</label>
 <input type="text" bind:value={headerRoles} placeholder="X-Remote-Roles" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Groups Header</label>
 <input type="text" bind:value={headerGroups} placeholder="X-Remote-Groups" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>
 <!-- Trusted Proxies -->
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Trusted Proxies (CIDR / IP)</label>
 <div class="flex gap-2">
 <input type="text" bind:value={headerTrustedProxyInput} placeholder="10.0.0.0/8"
 onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addTrustedProxy(); } }}
 class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <button type="button" class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer" onclick={addTrustedProxy}>Add</button>
 </div>
 {#if headerTrustedProxies.length > 0}
 <div class="mt-2 flex flex-wrap gap-1.5">
 {#each headerTrustedProxies as proxy, i}
 <span class="inline-flex items-center gap-1 px-2 py-1 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded text-xs font-mono text-slate-600 dark:text-slate-300">
 {proxy}
 <button type="button" class="w-3.5 h-3.5 flex items-center justify-center bg-transparent border-none cursor-pointer text-slate-400 dark:text-slate-500 hover:text-red-500" onclick={() => removeTrustedProxy(i)}>&times;</button>
 </span>
 {/each}
 </div>
 {/if}
 </div>
 </div>
 {/if}
 </div>

 <!-- ── Passkey strategy ── -->
 <div class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden">
 <button
 type="button"
 class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => toggleSection('passkey')}
 >
 Passkey (WebAuthn)
 {#if openSections.has('passkey')}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
 </button>
 {#if openSections.has('passkey')}
 <div class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700">
 <p class="text-xs text-slate-500 dark:text-slate-400 mt-3 leading-relaxed">
 Lets users sign in with a platform biometric (Touch ID / Windows
 Hello) or a hardware security key instead of a password.
 Per-user enrollment happens in
 <span class="font-medium">Settings → Account Security</span>;
 this section only configures how the WebAuthn ceremony is bootstrapped.
 The feature is silently inactive when <em>Enable</em> is off, or when
 <em>RP ID</em> / <em>RP Origins</em> are blank.
 </p>

 <label class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer">
 <input type="checkbox" bind:checked={passkeyEnabled} class="rounded border-slate-300" />
 Enable passkey login
 </label>

 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Name (URL key)</label>
 <input type="text" bind:value={passkeyName} placeholder="passkey" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500">
 Login endpoint becomes <code class="px-1 bg-slate-100 dark:bg-warm-800 rounded">/login/pass/&lt;name&gt;</code>. Default <code>passkey</code>.
 </p>
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Display label</label>
 <input type="text" bind:value={passkeyLabel} placeholder="Passkey" class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500">
 Button text on the login page. Default <code>Passkey</code>.
 </p>
 </div>
 </div>

 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">
 RP ID {#if passkeyEnabled}<span class="text-red-500" title="Required when passkey is enabled">*</span>{/if}
 </label>
 <input
 type="text"
 bind:value={passkeyRPID}
 placeholder="example.com"
 class="w-full px-3 py-2 text-sm font-mono border rounded-md focus:outline-none focus:ring-2 focus:ring-accent-500/10 {passkeyRPIDInvalid || passkeyRPIDMissing ? 'border-red-400 dark:border-red-500 focus:border-red-500' : 'border-slate-200 dark:border-warm-700 focus:border-accent-500'}"
 />
 <p class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500">
 The effective domain credentials are bound to — bare host only,
 no scheme or port. Use <code>localhost</code> for local dev.
 Empty silently disables the feature.
 </p>
 {#if passkeyRPIDInvalid}
 <p class="mt-1 text-[11px] text-red-600 dark:text-red-400">
 RP ID must be a bare host (no <code>/</code>, <code>:</code>, <code>?</code>, or <code>#</code>). Use the domain only.
 </p>
 {/if}
 {#if passkeyRPIDMissing}
 <p class="mt-1 text-[11px] text-red-600 dark:text-red-400">
 RP ID is required when passkey is enabled.
 </p>
 {/if}
 </div>

 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">RP display name</label>
 <input type="text" bind:value={passkeyRPDisplayName} placeholder="Pika" class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500">
 Shown in the platform passkey UI (e.g. "Sign in to <em>…</em>?").
 Falls back to the login title, then "Pika".
 </p>
 </div>

 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">
 RP Origins {#if passkeyEnabled}<span class="text-red-500" title="At least one origin is required when passkey is enabled">*</span>{/if}
 </label>
 <div class="flex gap-2">
 <input
 type="text"
 bind:value={passkeyRPOriginInput}
 placeholder="https://example.com"
 onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addPasskeyOrigin(); } }}
 class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <button type="button" class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer" onclick={addPasskeyOrigin}>Add</button>
 </div>
 <p class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500">
 Full origins (scheme + host + optional port) the browser may report
 in <code>clientDataJSON</code>. Add every origin pika is reachable from.
 </p>
 {#if passkeyRPOrigins.length > 0}
 <div class="mt-2 flex flex-wrap gap-1.5">
 {#each passkeyRPOrigins as origin, i}
 <span class="inline-flex items-center gap-1 px-2 py-1 bg-accent-50 border border-accent-200 rounded text-xs font-mono text-brand-700">
 {origin}
 <button type="button" class="w-3.5 h-3.5 flex items-center justify-center bg-transparent border-none cursor-pointer text-brand-400 hover:text-red-500" onclick={() => removePasskeyOrigin(i)}>&times;</button>
 </span>
 {/each}
 </div>
 {/if}
 {#if passkeyOriginsMissing}
 <p class="mt-1 text-[11px] text-red-600 dark:text-red-400">
 At least one origin is required when passkey is enabled.
 </p>
 {/if}
 </div>

 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">User verification</label>
 <select bind:value={passkeyUserVerification} class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10">
 <option value="">preferred (default)</option>
 <option value="required">required</option>
 <option value="preferred">preferred</option>
 <option value="discouraged">discouraged</option>
 </select>
 <p class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500 leading-relaxed">
 <span class="font-medium">required:</span> biometric / PIN must succeed (passkey as sole factor).<br />
 <span class="font-medium">preferred:</span> biometric if the authenticator supports it, else user-presence only.<br />
 <span class="font-medium">discouraged:</span> presence only (tap a key).
 </p>
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Challenge TTL (seconds)</label>
 <input type="number" min="1" bind:value={passkeyChallengeTTLSec} placeholder="300" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <p class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500">
 How long a registration / login ceremony stays valid. Default 300 (5 min).
 </p>
 </div>
 </div>
 </div>
 {/if}
 </div>

 <!-- ── Rate Limiting ── -->
 <div class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden">
 <button
 type="button"
 class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => toggleSection('rate_limit')}
 >
 Rate Limiting (Brute-Force Protection)
 {#if openSections.has('rate_limit')}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
 </button>
 {#if openSections.has('rate_limit')}
 <div class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700">
 <p class="text-xs text-slate-500 dark:text-slate-400 mt-3">
 Limits failed login attempts per client IP and per username.
 Below the soft threshold requests are unaffected; above it the
 response is delayed exponentially; above the hard threshold the
 request is rejected with HTTP 429. Changes take effect after
 server restart.
 </p>

 <label class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer">
 <input type="checkbox" bind:checked={rlEnabled} class="rounded border-slate-300" />
 Enable rate limiting on /login/pass and /login/register
 </label>

 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Window (seconds)</label>
 <input type="number" min="1" bind:value={rlWindowSec} placeholder="900" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Backoff Base (seconds)</label>
 <input type="number" min="0" bind:value={rlBackoffBaseSec} placeholder="1" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>

 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">IP Soft Threshold</label>
 <input type="number" min="1" bind:value={rlIPSoft} placeholder="3" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">IP Hard Threshold</label>
 <input type="number" min="1" bind:value={rlIPHard} placeholder="30" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>

 <div class="grid grid-cols-2 gap-3">
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">User Soft Threshold</label>
 <input type="number" min="1" bind:value={rlUserSoft} placeholder="3" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">User Hard Threshold</label>
 <input type="number" min="1" bind:value={rlUserHard} placeholder="15" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>
 </div>

 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Backoff Max (seconds)</label>
 <input type="number" min="1" bind:value={rlBackoffMaxSec} placeholder="15" class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 </div>

 <div>
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Trusted Proxy CIDRs</label>
 <p class="text-xs text-slate-500 dark:text-slate-400 mb-2">
 CIDR blocks whose X-Forwarded-For / X-Real-IP / True-Client-IP
 headers are honored for client-IP extraction. Leave empty when
 pika is directly internet-facing — XFF is forgeable from
 untrusted upstreams.
 </p>
 <div class="flex gap-2">
 <input
 type="text"
 bind:value={rlTrustedProxyInput}
 placeholder="10.0.0.0/8"
 onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addRlTrustedProxy(); } }}
 class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <button type="button" onclick={addRlTrustedProxy} class="px-3 py-2 text-sm bg-slate-100 dark:bg-warm-900 hover:bg-slate-200 dark:hover:bg-warm-700 rounded-md cursor-pointer">
 <Plus size={14} />
 </button>
 </div>
 {#if rlTrustedProxies.length > 0}
 <div class="mt-2 space-y-1">
 {#each rlTrustedProxies as cidr, i}
 <div class="flex items-center justify-between gap-2 px-3 py-1.5 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md text-xs font-mono">
 <span>{cidr}</span>
 <button type="button" onclick={() => removeRlTrustedProxy(i)} class="text-slate-400 dark:text-slate-500 hover:text-red-600 cursor-pointer">
 <Trash2 size={12} />
 </button>
 </div>
 {/each}
 </div>
 {/if}
 </div>
 </div>
 {/if}
 </div>

 <!-- ── Capabilities ── -->
 <div class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden">
 <button
 type="button"
 class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
 onclick={() => toggleSection('capabilities')}
 >
 Capabilities &amp; Superadmins
 {#if openSections.has('capabilities')}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
 </button>
 {#if openSections.has('capabilities')}
 <div class="px-5 pb-5 pt-1 border-t border-slate-100 dark:border-warm-700 space-y-5">
 <!-- Superadmins -->
 <div class="mt-3">
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Superadmins</label>
 <p class="text-[11px] text-slate-400 dark:text-slate-500 mb-2">
 Identity subjects that bypass every permission check. For the
 local strategy this is the pika username; for OAuth2 this is
 the <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded">sub</code>
 claim (often an opaque provider ID, not an email).
 </p>
 <div class="flex gap-2">
 <input type="text" bind:value={capSuperadminInput} placeholder="username or OIDC sub"
 onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addSuperadmin(); } }}
 class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10" />
 <button type="button" class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer" onclick={addSuperadmin}>Add</button>
 </div>
 {#if capSuperadmins.length > 0}
 <div class="mt-2 flex flex-wrap gap-1.5">
 {#each capSuperadmins as name, i}
 <span class="inline-flex items-center gap-1 px-2 py-1 bg-amber-50 border border-amber-200 rounded text-xs font-mono text-amber-700">
 {name}
 <button type="button" class="w-3.5 h-3.5 flex items-center justify-center bg-transparent border-none cursor-pointer text-amber-400 hover:text-red-500" onclick={() => removeSuperadmin(i)}>&times;</button>
 </span>
 {/each}
 </div>
 {/if}
 </div>

 <!-- Role → capability mapping -->
 <div class="pt-4 border-t border-slate-100 dark:border-warm-700">
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Role → Capabilities</label>
 <p class="text-[11px] text-slate-400 dark:text-slate-500 mb-2">
 Maps a role name (from the identity's
 <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded">roles</code>
 claim for OAuth2, or the Roles header for the Header strategy)
 to a set of pika capabilities. A user carrying any listed role
 is granted the union of its capabilities.
 </p>
 <div class="flex gap-2">
 <input
 type="text"
 bind:value={capNewRoleKey}
 placeholder="e.g. editor, admin"
 onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addRoleMapping(); } }}
 class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <button type="button" class="flex items-center gap-1 px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer" onclick={addRoleMapping}>
 <Plus size={13} />
 Add role
 </button>
 </div>
 {#if capRoleMappings.length === 0}
 <p class="mt-3 text-[11px] text-slate-400 dark:text-slate-500 italic">No role mappings configured. External identities (OAuth2, LDAP, Header) will get zero capabilities unless listed as superadmins.</p>
 {:else}
 <div class="mt-3 space-y-2">
 {#each capRoleMappings as row, rowIdx}
 <div class="p-3 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md">
 <div class="flex items-center justify-between mb-2 gap-2">
 <input
 type="text"
 bind:value={row.key}
 placeholder="role name"
 class="flex-1 px-2 py-1 text-xs font-mono bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <button type="button" class="p-1 text-slate-400 dark:text-slate-500 hover:text-red-500 transition-colors cursor-pointer" onclick={() => removeRoleMapping(rowIdx)} aria-label="Remove role mapping">
 <Trash2 size={13} />
 </button>
 </div>
 <div class="grid grid-cols-2 gap-x-3 gap-y-1">
 {#each knownCapabilities as cap}
 <label class="flex items-start gap-2 text-xs text-slate-700 dark:text-slate-200 cursor-pointer">
 <input
 type="checkbox"
 checked={row.capabilities.includes(cap.key)}
 onchange={() => toggleRoleCap(rowIdx, cap.key)}
 class="mt-0.5 rounded border-slate-300"
 />
 <span class="leading-tight">
 <span class="font-medium">{cap.name}</span>
 <span class="block text-[10px] text-slate-400 dark:text-slate-500 font-mono">{cap.key}</span>
 </span>
 </label>
 {/each}
 </div>
 </div>
 {/each}
 </div>
 {/if}
 </div>

 <!-- Scope → capability mapping -->
 <div class="pt-4 border-t border-slate-100 dark:border-warm-700">
 <!-- svelte-ignore a11y_label_has_associated_control -->
 <label class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Scope → Capabilities</label>
 <p class="text-[11px] text-slate-400 dark:text-slate-500 mb-2">
 Maps an OAuth2 scope (from the token response or the
 <code class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded">scope</code>
 claim) to pika capabilities. Less common than role mappings;
 use when you want "every user who got scope X" to gain
 specific rights.
 </p>
 <div class="flex gap-2">
 <input
 type="text"
 bind:value={capNewScopeKey}
 placeholder="e.g. pika:admin"
 onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addScopeMapping(); } }}
 class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <button type="button" class="flex items-center gap-1 px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer" onclick={addScopeMapping}>
 <Plus size={13} />
 Add scope
 </button>
 </div>
 {#if capScopeMappings.length > 0}
 <div class="mt-3 space-y-2">
 {#each capScopeMappings as row, rowIdx}
 <div class="p-3 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md">
 <div class="flex items-center justify-between mb-2 gap-2">
 <input
 type="text"
 bind:value={row.key}
 placeholder="scope name"
 class="flex-1 px-2 py-1 text-xs font-mono bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
 />
 <button type="button" class="p-1 text-slate-400 dark:text-slate-500 hover:text-red-500 transition-colors cursor-pointer" onclick={() => removeScopeMapping(rowIdx)} aria-label="Remove scope mapping">
 <Trash2 size={13} />
 </button>
 </div>
 <div class="grid grid-cols-2 gap-x-3 gap-y-1">
 {#each knownCapabilities as cap}
 <label class="flex items-start gap-2 text-xs text-slate-700 dark:text-slate-200 cursor-pointer">
 <input
 type="checkbox"
 checked={row.capabilities.includes(cap.key)}
 onchange={() => toggleScopeCap(rowIdx, cap.key)}
 class="mt-0.5 rounded border-slate-300"
 />
 <span class="leading-tight">
 <span class="font-medium">{cap.name}</span>
 <span class="block text-[10px] text-slate-400 dark:text-slate-500 font-mono">{cap.key}</span>
 </span>
 </label>
 {/each}
 </div>
 </div>
 {/each}
 </div>
 {/if}
 </div>
 </div>
 {/if}
 </div>

 <!-- Save button -->
 <div class="flex justify-end mt-4">
 <button
 type="button"
 onclick={handleSave}
 disabled={saving}
 class="px-4 py-2 text-sm font-medium text-white bg-accent-600 rounded-md hover:bg-accent-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors cursor-pointer"
 >
 {saving ? 'Saving...' : 'Save Auth Settings'}
 </button>
 </div>
</div>
