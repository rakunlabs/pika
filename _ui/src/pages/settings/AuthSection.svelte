<script lang="ts">
    import { addToast } from "@/lib/store/toast.svelte";
    import { appStore } from "@/lib/store/store.svelte";
    import { onMount } from "svelte";
    import { ChevronDown, ChevronRight, Plus, Trash2 } from "lucide-svelte";
    import axios from "axios";

    // External role/scope mappings grant pika Permission bundles (not raw
    // capabilities). The catalog of selectable permissions comes from
    // /api/v1/permissions (appStore.permissions); each carries a stable `key`
    // plus a human `name`. Mapping an external role to a bundle keeps a single
    // source of truth — edit the bundle once and both internal users and
    // external roles follow.
    const availablePermissions = $derived(appStore.permissions ?? []);

    // ── Auth settings shape ──
    interface OAuth2Entry {
        name: string;
        display_name?: string;
        auth_url?: string;
        token_url?: string;
        userinfo_url?: string;
        issuer_url?: string;
        client_id?: string;
        client_secret?: string;
        // client_secret_set is a read-only indicator from GET /settings: the
        // backend masks the real secret and only tells us whether one exists.
        client_secret_set?: boolean;
        // clear_client_secret is a write-only flag: when true on save, the
        // backend deliberately wipes the stored secret instead of keeping it.
        clear_client_secret?: boolean;
        scopes?: string[];
        disable_pkce?: boolean;
        password_flow?: boolean;
        // How client credentials are sent to the token endpoint:
        // "basic" (default — HTTP Basic header / client_secret_basic),
        // "post" (sent as request params / client_secret_post), or
        // "bearer" (Authorization: Bearer <secret>). Empty == basic.
        token_auth_method?: string;
        auto_create_user?: boolean;
        // Dotted claim paths roles are read from. Empty means the default
        // ["roles"]. Supports nesting + a "*" wildcard for Keycloak, e.g.
        // ["realm_access.roles", "resource_access.*.roles"].
        roles_claims?: string[];
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
        local?: {
            enabled: boolean;
            name?: string;
            login_form_collapsed?: boolean;
        };
        account_security_admin_only?: boolean;
        oauth2?: OAuth2Entry[];
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
    function nsToSec(v: number | undefined): number | "" {
        if (v == null || v === 0) return "";
        return Math.round(v / NS_PER_SECOND);
    }
    function secToNs(v: number | string): number {
        const n = typeof v === "string" ? Number(v) : v;
        if (!Number.isFinite(n) || n <= 0) return 0;
        return Math.round(n * NS_PER_SECOND);
    }

    // ── State ──
    let saving = $state(false);
    let loadError = $state("");
    let restartRequired = $state(false);

    // Collapsible sections
    let openSections = $state<Set<string>>(new Set(["ui", "local"]));

    function toggleSection(key: string) {
        const next = new Set(openSections);
        if (next.has(key)) next.delete(key);
        else next.add(key);
        openSections = next;
    }

    // UI fields
    let uiTitle = $state("");
    let uiSubtitle = $state("");
    let uiIcon = $state("");

    let uiCustomCSSUrl = $state("");

    // Cookie fields
    let cookieName = $state("");
    let cookieDomain = $state("");
    let cookiePath = $state("");
    let cookieSecure = $state(false);
    let cookieHttpOnly = $state(false);
    let cookieSameSite = $state("");

    // Issuer fields
    let issuerAccessTTL = $state<number | "">("");
    let issuerRefreshTTL = $state<number | "">("");
    let issuerRotateRefresh = $state(false);

    // Local strategy
    let localEnabled = $state(false);
    let localName = $state("");
    let localLoginFormCollapsed = $state(false);
    let accountSecurityAdminOnly = $state(false);

    // OAuth2 entries
    let oauth2Entries = $state<OAuth2Entry[]>([]);
    let oauth2ScopeInputs = $state<string[]>([]);
    let oauth2RolesClaimInputs = $state<string[]>([]);

    // Header settings remain in state so saving another authentication option
    // preserves the existing strategy even though it is no longer editable here.
    let headerName = $state("");
    let headerUser = $state("");
    let headerEmail = $state("");
    let headerDisplayName = $state("");
    let headerRoles = $state("");
    let headerGroups = $state("");
    let headerTrustedProxies = $state<string[]>([]);

    // Passkey settings are also round-tripped to avoid clearing an existing
    // deployment configuration when another authentication option is saved.
    let passkeyEnabled = $state(false);
    let passkeyName = $state("");
    let passkeyLabel = $state("");
    let passkeyRPID = $state("");
    let passkeyRPDisplayName = $state("");
    let passkeyRPOrigins = $state<string[]>([]);
    let passkeyUserVerification = $state("");
    let passkeyChallengeTTLSec = $state<number | "">("");

    // Capabilities — Superadmins (Identity.Subject allowlist)
    let capSuperadmins = $state<string[]>([]);
    let capSuperadminInput = $state("");

    // Capabilities — role/scope mappings (for external identities: OAuth2 and
    // Header). Rows are the edit-time UI representation; each row is a
    // key (role or scope name as it appears in the identity) paired with a set
    // of pika Permission bundle keys granted when that role/scope is present.
    type MappingRow = { key: string; permissions: string[] };
    let capRoleMappings = $state<MappingRow[]>([]);
    let capScopeMappings = $state<MappingRow[]>([]);
    let capNewRoleKey = $state("");
    let capNewScopeKey = $state("");

    // Convert a Record<string, string[]> into edit-friendly row array.
    // Stable order is alphabetical by key so the form doesn't jitter across
    // reloads (JSON object iteration order is technically insertion-preserving
    // but server-side encoding may not preserve it).
    function mapToRows(m: Record<string, string[]> | undefined): MappingRow[] {
        if (!m) return [];
        return Object.keys(m)
            .sort()
            .map((k) => ({ key: k, permissions: [...(m[k] ?? [])] }));
    }

    // Convert back to the wire format, dropping rows with empty keys and
    // deduplicating the permission-key slices.
    function rowsToMap(
        rows: MappingRow[],
    ): Record<string, string[]> | undefined {
        const out: Record<string, string[]> = {};
        for (const row of rows) {
            const k = row.key.trim();
            if (!k) continue;
            const perms = Array.from(
                new Set(row.permissions.filter((c) => !!c)),
            );
            if (perms.length === 0) continue;
            out[k] = perms;
        }
        return Object.keys(out).length > 0 ? out : undefined;
    }

    function hasOAuth2ManualEndpoints(entry: OAuth2Entry): boolean {
        return !!(entry.token_url && (entry.password_flow || entry.auth_url));
    }

    function addRoleMapping() {
        const k = capNewRoleKey.trim();
        if (!k) return;
        if (capRoleMappings.some((r) => r.key === k)) return;
        capRoleMappings = [...capRoleMappings, { key: k, permissions: [] }];
        capNewRoleKey = "";
    }
    function removeRoleMapping(i: number) {
        capRoleMappings = capRoleMappings.filter((_, idx) => idx !== i);
    }
    function toggleRolePerm(rowIdx: number, permKey: string) {
        capRoleMappings = capRoleMappings.map((r, i) => {
            if (i !== rowIdx) return r;
            const has = r.permissions.includes(permKey);
            return {
                key: r.key,
                permissions: has
                    ? r.permissions.filter((c) => c !== permKey)
                    : [...r.permissions, permKey],
            };
        });
    }

    function addScopeMapping() {
        const k = capNewScopeKey.trim();
        if (!k) return;
        if (capScopeMappings.some((r) => r.key === k)) return;
        capScopeMappings = [...capScopeMappings, { key: k, permissions: [] }];
        capNewScopeKey = "";
    }
    function removeScopeMapping(i: number) {
        capScopeMappings = capScopeMappings.filter((_, idx) => idx !== i);
    }
    function toggleScopePerm(rowIdx: number, permKey: string) {
        capScopeMappings = capScopeMappings.map((r, i) => {
            if (i !== rowIdx) return r;
            const has = r.permissions.includes(permKey);
            return {
                key: r.key,
                permissions: has
                    ? r.permissions.filter((c) => c !== permKey)
                    : [...r.permissions, permKey],
            };
        });
    }

    // Permission keys selected on a row that no longer correspond to an
    // existing bundle (e.g. the bundle was deleted/renamed, or this is a
    // legacy capability-key value from before the permission-based mapping).
    // Surfaced as removable chips so they aren't silently dropped on save and
    // the operator can re-point them at a real permission.
    function unknownPermKeys(row: MappingRow): string[] {
        return row.permissions.filter(
            (k) => !availablePermissions.some((p) => p.key === k),
        );
    }

    // Rate limit
    let rlEnabled = $state(true);
    let rlWindowSec = $state<number | "">("");
    let rlIPSoft = $state<number | "">("");
    let rlIPHard = $state<number | "">("");
    let rlUserSoft = $state<number | "">("");
    let rlUserHard = $state<number | "">("");
    let rlBackoffBaseSec = $state<number | "">("");
    let rlBackoffMaxSec = $state<number | "">("");
    let rlTrustedProxies = $state<string[]>([]);
    let rlTrustedProxyInput = $state("");

    function addRlTrustedProxy() {
        const v = rlTrustedProxyInput.trim();
        if (!v) return;
        rlTrustedProxies = [...rlTrustedProxies, v];
        rlTrustedProxyInput = "";
    }
    function removeRlTrustedProxy(i: number) {
        rlTrustedProxies = rlTrustedProxies.filter((_, idx) => idx !== i);
    }

    function loadFromSettings(auth: AuthSettings) {
        uiTitle = auth.ui?.title ?? "";
        uiSubtitle = auth.ui?.subtitle ?? "";
        uiIcon = auth.ui?.icon ?? "";
        uiCustomCSSUrl = auth.ui?.custom_css_url ?? "";

        cookieName = auth.cookie?.name ?? "";
        cookieDomain = auth.cookie?.domain ?? "";
        cookiePath = auth.cookie?.path ?? "";
        cookieSecure = auth.cookie?.secure ?? false;
        cookieHttpOnly = auth.cookie?.http_only ?? false;
        cookieSameSite = auth.cookie?.same_site ?? "";

        issuerAccessTTL = auth.issuer?.access_ttl ?? "";
        issuerRefreshTTL = auth.issuer?.refresh_ttl ?? "";
        issuerRotateRefresh = auth.issuer?.rotate_refresh ?? false;

        localEnabled = auth.local?.enabled ?? false;
        localName = auth.local?.name ?? "";
        localLoginFormCollapsed = auth.local?.login_form_collapsed ?? false;
        accountSecurityAdminOnly = auth.account_security_admin_only ?? false;

        oauth2Entries = (auth.oauth2 ?? []).map((e) => ({
            ...e,
            // Never bind the (masked) secret into the input; carry the
            // "is set" indicator so the field can show the right hint and
            // offer an explicit clear. Reset any clear intent on (re)load.
            client_secret: "",
            client_secret_set: e.client_secret_set ?? false,
            clear_client_secret: false,
            roles_claims: [...(e.roles_claims ?? [])],
        }));
        oauth2ScopeInputs = oauth2Entries.map(() => "");
        oauth2RolesClaimInputs = oauth2Entries.map(() => "");

        headerName = auth.header?.name ?? "";
        headerUser = auth.header?.user ?? "";
        headerEmail = auth.header?.email ?? "";
        headerDisplayName = auth.header?.display_name_header ?? "";
        headerRoles = auth.header?.roles ?? "";
        headerGroups = auth.header?.groups ?? "";
        headerTrustedProxies = [...(auth.header?.trusted_proxies ?? [])];

        // Passkey
        passkeyEnabled = auth.passkey?.enabled ?? false;
        passkeyName = auth.passkey?.name ?? "";
        passkeyLabel = auth.passkey?.label ?? "";
        passkeyRPID = auth.passkey?.rp_id ?? "";
        passkeyRPDisplayName = auth.passkey?.rp_display_name ?? "";
        passkeyRPOrigins = [...(auth.passkey?.rp_origins ?? [])];
        passkeyUserVerification = auth.passkey?.user_verification ?? "";
        passkeyChallengeTTLSec = nsToSec(auth.passkey?.challenge_ttl);

        capSuperadmins = [...(auth.capabilities?.superadmins ?? [])];
        capSuperadminInput = "";
        capRoleMappings = mapToRows(auth.capabilities?.role_mapping);
        capScopeMappings = mapToRows(auth.capabilities?.scope_mapping);
        capNewRoleKey = "";
        capNewScopeKey = "";

        // Rate limit (if absent, leave fields empty so backend defaults apply)
        const rl = auth.rate_limit;
        rlEnabled = rl?.enabled ?? true;
        rlWindowSec = nsToSec(rl?.window);
        rlIPSoft = rl?.ip_soft_threshold ?? "";
        rlIPHard = rl?.ip_hard_threshold ?? "";
        rlUserSoft = rl?.user_soft_threshold ?? "";
        rlUserHard = rl?.user_hard_threshold ?? "";
        rlBackoffBaseSec = nsToSec(rl?.backoff_base);
        rlBackoffMaxSec = nsToSec(rl?.backoff_max);
        rlTrustedProxies = [...(rl?.trusted_proxy_cidrs ?? [])];
        rlTrustedProxyInput = "";
    }

    function buildPayload(): AuthSettings {
        const auth: AuthSettings = {};

        // UI
        const uiBlock: AuthSettings["ui"] = {};
        if (uiTitle) uiBlock.title = uiTitle;
        if (uiSubtitle) uiBlock.subtitle = uiSubtitle;
        if (uiIcon) uiBlock.icon = uiIcon;
        if (uiCustomCSSUrl) uiBlock.custom_css_url = uiCustomCSSUrl;
        if (Object.keys(uiBlock).length > 0) auth.ui = uiBlock;

        // Cookie
        const cookieBlock: AuthSettings["cookie"] = {};
        if (cookieName) cookieBlock.name = cookieName;
        if (cookieDomain) cookieBlock.domain = cookieDomain;
        if (cookiePath) cookieBlock.path = cookiePath;
        if (cookieSecure) cookieBlock.secure = true;
        if (cookieHttpOnly) cookieBlock.http_only = true;
        if (cookieSameSite) cookieBlock.same_site = cookieSameSite;
        if (Object.keys(cookieBlock).length > 0) auth.cookie = cookieBlock;

        // Issuer
        const issuerBlock: AuthSettings["issuer"] = {};
        if (issuerAccessTTL !== "")
            issuerBlock.access_ttl = Number(issuerAccessTTL);
        if (issuerRefreshTTL !== "")
            issuerBlock.refresh_ttl = Number(issuerRefreshTTL);
        if (issuerRotateRefresh) issuerBlock.rotate_refresh = true;
        if (Object.keys(issuerBlock).length > 0) auth.issuer = issuerBlock;

        // Local
        auth.local = { enabled: localEnabled };
        if (localName) auth.local.name = localName;
        if (localLoginFormCollapsed)
            auth.local.login_form_collapsed = true;
        if (accountSecurityAdminOnly)
            auth.account_security_admin_only = true;

        // OAuth2
        if (oauth2Entries.length > 0) {
            auth.oauth2 = oauth2Entries.map((e) => {
                const entry: OAuth2Entry = { name: e.name };
                const hasManualEndpoints = hasOAuth2ManualEndpoints(e);
                if (e.display_name) entry.display_name = e.display_name;
                if (e.auth_url) entry.auth_url = e.auth_url;
                if (e.token_url) entry.token_url = e.token_url;
                if (e.userinfo_url) entry.userinfo_url = e.userinfo_url;
                if (!hasManualEndpoints && e.issuer_url)
                    entry.issuer_url = e.issuer_url;
                if (e.client_id) entry.client_id = e.client_id;
                // Secret handling (clear takes precedence so a stale typed
                // value can't override an explicit wipe): clear flag asks the
                // backend to remove the stored secret; otherwise a typed value
                // replaces it; an untouched blank field is omitted so the
                // backend keeps the stored secret ("leave blank to keep").
                if (e.clear_client_secret) {
                    entry.clear_client_secret = true;
                } else if (e.client_secret) {
                    entry.client_secret = e.client_secret;
                }
                if (e.scopes && e.scopes.length > 0) entry.scopes = e.scopes;
                if (e.roles_claims && e.roles_claims.length > 0)
                    entry.roles_claims = e.roles_claims;
                if (e.disable_pkce) entry.disable_pkce = true;
                if (e.password_flow) entry.password_flow = true;
                // Only persist a non-default auth method; "basic"/empty is
                // the server default so omit it to keep the payload clean.
                if (e.token_auth_method && e.token_auth_method !== "basic")
                    entry.token_auth_method = e.token_auth_method;
                if (e.auto_create_user) entry.auto_create_user = true;
                return entry;
            });
        }

        // Header
        const headerBlock: AuthSettings["header"] = {};
        if (headerName) headerBlock.name = headerName;
        if (headerUser) headerBlock.user = headerUser;
        if (headerEmail) headerBlock.email = headerEmail;
        if (headerDisplayName)
            headerBlock.display_name_header = headerDisplayName;
        if (headerRoles) headerBlock.roles = headerRoles;
        if (headerGroups) headerBlock.groups = headerGroups;
        if (headerTrustedProxies.length > 0)
            headerBlock.trusted_proxies = [...headerTrustedProxies];
        if (Object.keys(headerBlock).length > 0) auth.header = headerBlock;

        // Passkey — always emit the block so toggling enabled=false persists
        // (mirrors how Local is handled). Optional fields are omitted when
        // empty to keep the saved JSON tidy.
        const passkeyBlock: NonNullable<AuthSettings["passkey"]> = {
            enabled: passkeyEnabled,
        };
        if (passkeyName) passkeyBlock.name = passkeyName;
        if (passkeyLabel) passkeyBlock.label = passkeyLabel;
        if (passkeyRPID) passkeyBlock.rp_id = passkeyRPID;
        if (passkeyRPDisplayName)
            passkeyBlock.rp_display_name = passkeyRPDisplayName;
        if (passkeyRPOrigins.length > 0)
            passkeyBlock.rp_origins = [...passkeyRPOrigins];
        if (passkeyUserVerification)
            passkeyBlock.user_verification = passkeyUserVerification;
        if (passkeyChallengeTTLSec !== "")
            passkeyBlock.challenge_ttl = secToNs(passkeyChallengeTTLSec);
        auth.passkey = passkeyBlock;

        // Capabilities — build a single block combining superadmins and the
        // role/scope → capability mappings. Each sub-field is omitted when
        // empty so the saved JSON stays tidy; a block with zero entries is
        // omitted entirely.
        const capsBlock: NonNullable<AuthSettings["capabilities"]> = {};
        if (capSuperadmins.length > 0)
            capsBlock.superadmins = [...capSuperadmins];
        const roleMap = rowsToMap(capRoleMappings);
        if (roleMap) capsBlock.role_mapping = roleMap;
        const scopeMap = rowsToMap(capScopeMappings);
        if (scopeMap) capsBlock.scope_mapping = scopeMap;
        if (Object.keys(capsBlock).length > 0) auth.capabilities = capsBlock;

        // Rate limit — always include the block so toggling Enabled persists.
        const rlBlock: AuthSettings["rate_limit"] = { enabled: rlEnabled };
        if (rlWindowSec !== "") rlBlock.window = secToNs(rlWindowSec);
        if (rlIPSoft !== "") rlBlock.ip_soft_threshold = Number(rlIPSoft);
        if (rlIPHard !== "") rlBlock.ip_hard_threshold = Number(rlIPHard);
        if (rlUserSoft !== "") rlBlock.user_soft_threshold = Number(rlUserSoft);
        if (rlUserHard !== "") rlBlock.user_hard_threshold = Number(rlUserHard);
        if (rlBackoffBaseSec !== "")
            rlBlock.backoff_base = secToNs(rlBackoffBaseSec);
        if (rlBackoffMaxSec !== "")
            rlBlock.backoff_max = secToNs(rlBackoffMaxSec);
        if (rlTrustedProxies.length > 0)
            rlBlock.trusted_proxy_cidrs = [...rlTrustedProxies];
        auth.rate_limit = rlBlock;

        return auth;
    }

    async function handleSave() {
        saving = true;
        restartRequired = false;
        try {
            const response = await axios.post("/api/v1/settings", {
                action: "set",
                auth: buildPayload(),
            });
            if (response.data?.restart_required) {
                restartRequired = true;
            }
            await appStore.loadInfo();
            addToast("Auth settings saved", "success");
        } catch (err: any) {
            const msg =
                err?.response?.data?.message || "Failed to save auth settings";
            addToast(msg, "alert");
        } finally {
            saving = false;
        }
    }

    // OAuth2 helpers
    function addOAuth2() {
        oauth2Entries = [
            ...oauth2Entries,
            { name: "", scopes: [], roles_claims: [] },
        ];
        oauth2ScopeInputs = [...oauth2ScopeInputs, ""];
        oauth2RolesClaimInputs = [...oauth2RolesClaimInputs, ""];
    }

    function removeOAuth2(i: number) {
        oauth2Entries = oauth2Entries.filter((_, idx) => idx !== i);
        oauth2ScopeInputs = oauth2ScopeInputs.filter((_, idx) => idx !== i);
        oauth2RolesClaimInputs = oauth2RolesClaimInputs.filter(
            (_, idx) => idx !== i,
        );
    }

    function addOAuth2RolesClaim(i: number) {
        const v = oauth2RolesClaimInputs[i]?.trim();
        if (!v) return;
        const entry = oauth2Entries[i];
        if (!entry) return;
        if ((entry.roles_claims ?? []).includes(v)) return;
        entry.roles_claims = [...(entry.roles_claims ?? []), v];
        oauth2Entries = [...oauth2Entries];
        oauth2RolesClaimInputs = oauth2RolesClaimInputs.map((s, idx) =>
            idx === i ? "" : s,
        );
    }

    function removeOAuth2RolesClaim(entryIdx: number, claimIdx: number) {
        const entry = oauth2Entries[entryIdx];
        if (!entry) return;
        entry.roles_claims = (entry.roles_claims ?? []).filter(
            (_, i) => i !== claimIdx,
        );
        oauth2Entries = [...oauth2Entries];
    }

    function addOAuth2Scope(i: number) {
        const v = oauth2ScopeInputs[i]?.trim();
        if (!v) return;
        const entry = oauth2Entries[i];
        if (!entry) return;
        entry.scopes = [...(entry.scopes ?? []), v];
        oauth2Entries = [...oauth2Entries];
        oauth2ScopeInputs = oauth2ScopeInputs.map((s, idx) =>
            idx === i ? "" : s,
        );
    }

    function removeOAuth2Scope(entryIdx: number, scopeIdx: number) {
        const entry = oauth2Entries[entryIdx];
        if (!entry) return;
        entry.scopes = (entry.scopes ?? []).filter((_, i) => i !== scopeIdx);
        oauth2Entries = [...oauth2Entries];
    }

    // Capabilities helpers
    function addSuperadmin() {
        const v = capSuperadminInput.trim();
        if (!v) return;
        if (capSuperadmins.includes(v)) return;
        capSuperadmins = [...capSuperadmins, v];
        capSuperadminInput = "";
    }

    function removeSuperadmin(i: number) {
        capSuperadmins = capSuperadmins.filter((_, idx) => idx !== i);
    }

    onMount(async () => {
        // Load the permission catalog so the role/scope mapping editor can
        // offer the existing bundles as selectable values. Best-effort: the
        // editor still renders (with unknown-key chips) if this fails.
        void appStore.loadPermissions();
        try {
            const response = await axios.get("/api/v1/settings");
            loadFromSettings(response.data?.auth || {});
        } catch (err: any) {
            loadError =
                err?.response?.data?.message || "Failed to load settings";
        }
    });
</script>

<div>
    <div class="mb-4">
        <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">
            Authentication
        </h2>
        <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
            Configure ada auth strategies, cookie settings, token issuance, and
            identity providers.
        </p>
    </div>

    {#if loadError}
        <div
            class="mb-4 p-3 bg-red-50 border border-red-200 rounded-md text-sm text-red-700"
        >
            {loadError}
        </div>
    {/if}

    {#if restartRequired}
        <div
            class="mb-4 p-3 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800"
        >
            A server restart is required for some changes to take effect.
        </div>
    {/if}

    <!-- ── UI section ── -->
    <div
        class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden"
    >
        <button
            type="button"
            class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
            onclick={() => toggleSection("ui")}
        >
            Login UI
            {#if openSections.has("ui")}<ChevronDown
                    size={15}
                />{:else}<ChevronRight size={15} />{/if}
        </button>
        {#if openSections.has("ui")}
            <div
                class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700"
            >
                <div class="grid grid-cols-2 gap-3">
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >Title</label
                        >
                        <input
                            type="text"
                            bind:value={uiTitle}
                            placeholder="pika"
                            class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >Subtitle</label
                        >
                        <input
                            type="text"
                            bind:value={uiSubtitle}
                            placeholder="optional"
                            class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                </div>
                <div>
                    <!-- svelte-ignore a11y_label_has_associated_control -->
                    <label
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Icon URL / data URI</label
                    >
                    <input
                        type="text"
                        bind:value={uiIcon}
                        placeholder="https://example.com/logo.png"
                        class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                    />
                </div>
                <div>
                    <!-- svelte-ignore a11y_label_has_associated_control -->
                    <label
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Custom CSS URL</label
                    >
                    <input
                        type="text"
                        bind:value={uiCustomCSSUrl}
                        placeholder="https://example.com/login.css"
                        class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                    />
                </div>
            </div>
        {/if}
    </div>

    <!-- ── Cookie section ── -->
    <div
        class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden"
    >
        <button
            type="button"
            class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
            onclick={() => toggleSection("cookie")}
        >
            Cookie
            {#if openSections.has("cookie")}<ChevronDown
                    size={15}
                />{:else}<ChevronRight size={15} />{/if}
        </button>
        {#if openSections.has("cookie")}
            <div
                class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700"
            >
                <div class="grid grid-cols-3 gap-3">
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >Cookie Name</label
                        >
                        <input
                            type="text"
                            bind:value={cookieName}
                            placeholder="pika_session"
                            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >Domain</label
                        >
                        <input
                            type="text"
                            bind:value={cookieDomain}
                            placeholder=".example.com"
                            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >Path</label
                        >
                        <input
                            type="text"
                            bind:value={cookiePath}
                            placeholder="/"
                            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                </div>
                <div class="grid grid-cols-2 gap-3">
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >SameSite</label
                        >
                        <select
                            bind:value={cookieSameSite}
                            class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        >
                            <option value="">Default</option>
                            <option value="Lax">Lax</option>
                            <option value="Strict">Strict</option>
                            <option value="None">None</option>
                        </select>
                    </div>
                    <div class="flex flex-col gap-2 pt-5">
                        <label
                            class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer"
                        >
                            <input
                                type="checkbox"
                                bind:checked={cookieSecure}
                                class="rounded border-slate-300"
                            />
                            Secure
                        </label>
                        <label
                            class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer"
                        >
                            <input
                                type="checkbox"
                                bind:checked={cookieHttpOnly}
                                class="rounded border-slate-300"
                            />
                            HttpOnly
                        </label>
                    </div>
                </div>
            </div>
        {/if}
    </div>

    <!-- ── Issuer section ── -->
    <div
        class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden"
    >
        <button
            type="button"
            class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
            onclick={() => toggleSection("issuer")}
        >
            Token Issuance
            {#if openSections.has("issuer")}<ChevronDown
                    size={15}
                />{:else}<ChevronRight size={15} />{/if}
        </button>
        {#if openSections.has("issuer")}
            <div
                class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700"
            >
                <p
                    class="text-xs text-slate-500 dark:text-slate-400 mt-3 leading-relaxed"
                >
                    Controls how long a signed-in session stays alive. Every
                    login produces a short-lived <span class="font-medium"
                        >access token</span
                    >
                    (validates each request) and a longer
                    <span class="font-medium">refresh token</span>
                    (used behind the scenes to silently mint a new access token when
                    the old one expires — the user isn't prompted). Both live server-side;
                    only an opaque session cookie reaches the browser.
                </p>
                <div class="grid grid-cols-2 gap-3">
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >Access TTL (seconds)</label
                        >
                        <p
                            class="text-[11px] text-slate-400 dark:text-slate-500 mb-2 leading-relaxed"
                        >
                            How often the access token is refreshed under the
                            hood. Shorter = faster reaction to admin actions
                            (kick, permission change, disable). Default 900 (15
                            min). Users notice nothing.
                        </p>
                        <input
                            type="number"
                            bind:value={issuerAccessTTL}
                            placeholder="900"
                            class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >Refresh TTL (seconds)</label
                        >
                        <p
                            class="text-[11px] text-slate-400 dark:text-slate-500 mb-2 leading-relaxed"
                        >
                            How long the refresh token lasts. When it expires
                            the user is forced back to the login screen. Default
                            86400 (1 day); 604800 = 1 week. Behavior depends on
                            the rotation setting below.
                        </p>
                        <input
                            type="number"
                            bind:value={issuerRefreshTTL}
                            placeholder="86400"
                            class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                </div>
                <div class="pt-2">
                    <label
                        class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer"
                    >
                        <input
                            type="checkbox"
                            bind:checked={issuerRotateRefresh}
                            class="rounded border-slate-300"
                        />
                        Rotate refresh tokens on use
                    </label>
                    <p
                        class="text-[11px] text-slate-400 dark:text-slate-500 mt-1.5 leading-relaxed"
                    >
                        <span
                            class="font-medium text-slate-600 dark:text-slate-300"
                            >Off (default):</span
                        >
                        the Refresh TTL is a <em>hard ceiling</em>. Even a user
                        who visits every day is forced back to the login screen
                        once Refresh TTL elapses from their original login time.
                        <br />
                        <span
                            class="font-medium text-slate-600 dark:text-slate-300"
                            >On:</span
                        > the refresh token is replaced with a new one every time
                        it's used, restarting the TTL clock. An active user stays
                        logged in indefinitely; only a gap of inactivity longer than
                        Refresh TTL forces re-login. Also mitigates refresh-token
                        theft — a stolen token becomes unusable as soon as the legitimate
                        user refreshes.
                    </p>
                </div>
            </div>
        {/if}
    </div>

    <!-- ── Local strategy ── -->
    <div
        class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden"
    >
        <button
            type="button"
            class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
            onclick={() => toggleSection("local")}
        >
            Local (Password) Strategy
            {#if openSections.has("local")}<ChevronDown
                    size={15}
                />{:else}<ChevronRight size={15} />{/if}
        </button>
        {#if openSections.has("local")}
            <div
                class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700"
            >
                <label
                    class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer"
                >
                    <input
                        type="checkbox"
                        bind:checked={localEnabled}
                        class="rounded border-slate-300"
                    />
                    Enable local username/password authentication
                </label>
                <label
                    class="flex items-start gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer"
                >
                    <input
                        type="checkbox"
                        bind:checked={localLoginFormCollapsed}
                        disabled={!localEnabled}
                        class="mt-0.5 rounded border-slate-300 disabled:opacity-40"
                    />
                    <span>
                        Collapse the local login form by default
                        <span
                            class="block mt-0.5 text-xs text-slate-500 dark:text-slate-400"
                        >
                            Users can reveal it from the compact Local login control.
                            Authentication remains available to every local user.
                        </span>
                    </span>
                </label>
                <div>
                    <!-- svelte-ignore a11y_label_has_associated_control -->
                    <label
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Strategy label</label
                    >
                    <input
                        type="text"
                        bind:value={localName}
                        placeholder="Local"
                        class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                    />
                </div>
                <div
                    class="pt-3 border-t border-slate-200 dark:border-warm-700"
                >
                    <label
                        class="flex items-start gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer"
                    >
                        <input
                            type="checkbox"
                            bind:checked={accountSecurityAdminOnly}
                            class="mt-0.5 rounded border-slate-300"
                        />
                        <span>
                            Restrict Account Security to superadmins
                            <span
                                class="block mt-0.5 text-xs text-slate-500 dark:text-slate-400"
                            >
                                Hides the section and blocks its passkey and TOTP
                                APIs for other users.
                            </span>
                        </span>
                    </label>
                </div>
            </div>
        {/if}
    </div>

    <!-- ── OAuth2 strategies ── -->
    <div
        class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden"
    >
        <button
            type="button"
            class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
            onclick={() => toggleSection("oauth2")}
        >
            OAuth2 Providers
            {#if openSections.has("oauth2")}<ChevronDown
                    size={15}
                />{:else}<ChevronRight size={15} />{/if}
        </button>
        {#if openSections.has("oauth2")}
            <div
                class="px-5 pb-5 pt-3 border-t border-slate-100 dark:border-warm-700 space-y-4"
            >
                {#each oauth2Entries as entry, i}
                    <div
                        class="p-4 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg space-y-3"
                    >
                        <div class="flex items-center justify-between mb-1">
                            <span
                                class="text-xs font-semibold text-slate-600 dark:text-slate-300"
                                >Provider #{i + 1}</span
                            >
                            <button
                                type="button"
                                onclick={() => removeOAuth2(i)}
                                class="p-1 text-slate-400 dark:text-slate-500 hover:text-red-500 transition-colors"
                            >
                                <Trash2 size={13} />
                            </button>
                        </div>
                        <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
                            <div>
                                <!-- svelte-ignore a11y_label_has_associated_control -->
                                <label
                                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                                    >Name (URL key)</label
                                >
                                <input
                                    type="text"
                                    bind:value={entry.name}
                                    placeholder="google"
                                    class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                                />
                            </div>
                            <div>
                                <!-- svelte-ignore a11y_label_has_associated_control -->
                                <label
                                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                                    >Display Name</label
                                >
                                <input
                                    type="text"
                                    bind:value={entry.display_name}
                                    placeholder="Google"
                                    class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                                />
                            </div>
                        </div>
                        <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
                            <div>
                                <!-- svelte-ignore a11y_label_has_associated_control -->
                                <label
                                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                                    >Authorization URL</label
                                >
                                <input
                                    type="text"
                                    bind:value={entry.auth_url}
                                    placeholder="https://gitlab.com/oauth/authorize"
                                    class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                                />
                            </div>
                            <div>
                                <!-- svelte-ignore a11y_label_has_associated_control -->
                                <label
                                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                                    >Token URL</label
                                >
                                <input
                                    type="text"
                                    bind:value={entry.token_url}
                                    placeholder="https://gitlab.com/oauth/token"
                                    class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                                />
                            </div>
                        </div>
                        <div>
                            <!-- svelte-ignore a11y_label_has_associated_control -->
                            <label
                                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                                >UserInfo URL</label
                            >
                            <input
                                type="text"
                                bind:value={entry.userinfo_url}
                                placeholder="https://gitlab.com/oauth/userinfo"
                                class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                            />
                            <p
                                class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500"
                            >
                                Optional. If set, pika fetches identity claims
                                with the access token; otherwise it falls back
                                to token claims.
                            </p>
                        </div>
                        {#if entry.issuer_url && !hasOAuth2ManualEndpoints(entry)}
                            <p
                                class="text-[11px] text-amber-600 dark:text-amber-400"
                            >
                                This provider still has a legacy issuer URL
                                saved. Fill Authorization URL and Token URL to
                                stop using discovery.
                            </p>
                        {/if}
                        <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
                            <div>
                                <!-- svelte-ignore a11y_label_has_associated_control -->
                                <label
                                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                                    >Client ID</label
                                >
                                <input
                                    type="text"
                                    bind:value={entry.client_id}
                                    class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                                />
                            </div>
                            <div>
                                <!-- svelte-ignore a11y_label_has_associated_control -->
                                <label
                                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                                    >Client Secret</label
                                >
                                <input
                                    type="password"
                                    bind:value={entry.client_secret}
                                    disabled={entry.clear_client_secret}
                                    placeholder={entry.clear_client_secret
                                        ? "(will be cleared on save)"
                                        : entry.client_secret_set
                                          ? "(secret set — leave blank to keep)"
                                          : "(no secret set)"}
                                    class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 disabled:opacity-50 disabled:cursor-not-allowed"
                                />
                                {#if entry.client_secret_set}
                                    <label
                                        class="mt-1 flex items-center gap-1.5 text-[10px] text-slate-500 dark:text-slate-400 cursor-pointer"
                                    >
                                        <input
                                            type="checkbox"
                                            checked={entry.clear_client_secret}
                                            onchange={(ev) => {
                                                entry.clear_client_secret =
                                                    ev.currentTarget.checked;
                                                if (entry.clear_client_secret)
                                                    entry.client_secret = "";
                                            }}
                                            class="rounded border-slate-300"
                                        />
                                        Clear stored secret
                                    </label>
                                {/if}
                                <p
                                    class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500"
                                >
                                    {#if entry.clear_client_secret}
                                        The stored secret will be removed when
                                        you save.
                                    {:else if entry.client_secret_set}
                                        Leave blank to keep the existing secret,
                                        or type a new one to replace it.
                                    {:else}
                                        No secret stored yet — enter one to
                                        enable this provider.
                                    {/if}
                                </p>
                            </div>
                        </div>
                        <!-- Token-endpoint client authentication. Default is
                             HTTP Basic (client_secret_basic). Switch modes when
                             the provider rejects the secret with an
                             "invalid_client" / "client_secret does not match"
                             error even though the secret is correct. -->
                        <div>
                            <!-- svelte-ignore a11y_label_has_associated_control -->
                            <label
                                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                                >Client authentication</label
                            >
                            <select
                                value={entry.token_auth_method ?? "basic"}
                                onchange={(ev) =>
                                    (entry.token_auth_method =
                                        ev.currentTarget.value)}
                                class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
                            >
                                <option value="basic"
                                    >HTTP Basic header — client_secret_basic
                                    (default)</option
                                >
                                <option value="post"
                                    >Request parameters — client_secret_post</option
                                >
                                <option value="bearer"
                                    >Bearer token — Authorization: Bearer</option
                                >
                            </select>
                            <p
                                class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500"
                            >
                                How the client secret is sent to the token
                                endpoint. Try
                                <code>client_secret_post</code> if login fails
                                with an "invalid_client" / "client_secret does
                                not match" error despite a correct secret.
                                <code>post</code> sends the credentials as request
                                parameters (query string), not the form body.
                            </p>
                        </div>
                        <!-- Scopes -->
                        <div>
                            <!-- svelte-ignore a11y_label_has_associated_control -->
                            <label
                                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                                >Scopes</label
                            >
                            <div class="flex gap-2">
                                <input
                                    type="text"
                                    bind:value={oauth2ScopeInputs[i]}
                                    placeholder="openid"
                                    onkeydown={(e) => {
                                        if (e.key === "Enter") {
                                            e.preventDefault();
                                            addOAuth2Scope(i);
                                        }
                                    }}
                                    class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                                />
                                <button
                                    type="button"
                                    class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
                                    onclick={() => addOAuth2Scope(i)}
                                    >Add</button
                                >
                            </div>
                            {#if (entry.scopes ?? []).length > 0}
                                <div class="mt-2 flex flex-wrap gap-1.5">
                                    {#each entry.scopes ?? [] as scope, si}
                                        <span
                                            class="inline-flex items-center gap-1 px-2 py-1 bg-accent-50 border border-accent-200 rounded text-xs font-mono text-brand-700"
                                        >
                                            {scope}
                                            <button
                                                type="button"
                                                class="w-3.5 h-3.5 flex items-center justify-center bg-transparent border-none cursor-pointer text-brand-400 hover:text-red-500"
                                                onclick={() =>
                                                    removeOAuth2Scope(i, si)}
                                                >&times;</button
                                            >
                                        </span>
                                    {/each}
                                </div>
                            {/if}
                        </div>
                        <!-- Roles claim paths -->
                        <div>
                            <!-- svelte-ignore a11y_label_has_associated_control -->
                            <label
                                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                                >Roles claim path(s)</label
                            >
                            <div class="flex gap-2">
                                <input
                                    type="text"
                                    bind:value={oauth2RolesClaimInputs[i]}
                                    placeholder="realm_access.roles"
                                    onkeydown={(e) => {
                                        if (e.key === "Enter") {
                                            e.preventDefault();
                                            addOAuth2RolesClaim(i);
                                        }
                                    }}
                                    class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                                />
                                <button
                                    type="button"
                                    class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
                                    onclick={() => addOAuth2RolesClaim(i)}
                                    >Add</button
                                >
                            </div>
                            {#if (entry.roles_claims ?? []).length > 0}
                                <div class="mt-2 flex flex-wrap gap-1.5">
                                    {#each entry.roles_claims ?? [] as claim, ci}
                                        <span
                                            class="inline-flex items-center gap-1 px-2 py-1 bg-accent-50 border border-accent-200 rounded text-xs font-mono text-brand-700"
                                        >
                                            {claim}
                                            <button
                                                type="button"
                                                class="w-3.5 h-3.5 flex items-center justify-center bg-transparent border-none cursor-pointer text-brand-400 hover:text-red-500"
                                                onclick={() =>
                                                    removeOAuth2RolesClaim(
                                                        i,
                                                        ci,
                                                    )}
                                                >&times;</button
                                            >
                                        </span>
                                    {/each}
                                </div>
                            {/if}
                            <p
                                class="mt-1 text-[11px] text-slate-400 dark:text-slate-500"
                            >
                                Where to read roles from in the token/userinfo
                                claims. Leave empty for the default <code
                                    >roles</code
                                >
                                claim. Supports nesting and a <code>*</code>
                                wildcard — for Keycloak use
                                <code>realm_access.roles</code> and/or
                                <code>resource_access.*.roles</code>. Mapped to
                                permissions in Capabilities &amp; Superadmins
                                below.
                            </p>
                        </div>
                        <div class="flex flex-wrap gap-4">
                            <label
                                class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
                            >
                                <input
                                    type="checkbox"
                                    bind:checked={entry.disable_pkce}
                                    class="rounded border-slate-300"
                                />
                                Disable PKCE
                            </label>
                            <label
                                class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
                            >
                                <input
                                    type="checkbox"
                                    bind:checked={entry.password_flow}
                                    class="rounded border-slate-300"
                                />
                                Password flow
                            </label>
                            <label
                                class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
                            >
                                <input
                                    type="checkbox"
                                    bind:checked={entry.auto_create_user}
                                    class="rounded border-slate-300"
                                />
                                Auto-create users
                            </label>
                        </div>
                        <p class="text-[11px] text-slate-400 dark:text-slate-500">
                            When enabled, unknown OAuth2 identities create an
                            external-only pika user. Existing linked users or
                            verified-email matches are reused first.
                        </p>
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

    <!-- ── Rate Limiting ── -->
    <div
        class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden"
    >
        <button
            type="button"
            class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
            onclick={() => toggleSection("rate_limit")}
        >
            Rate Limiting (Brute-Force Protection)
            {#if openSections.has("rate_limit")}<ChevronDown
                    size={15}
                />{:else}<ChevronRight size={15} />{/if}
        </button>
        {#if openSections.has("rate_limit")}
            <div
                class="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700"
            >
                <p class="text-xs text-slate-500 dark:text-slate-400 mt-3">
                    Limits failed login attempts per client IP and per username.
                    Below the soft threshold requests are unaffected; above it
                    the response is delayed exponentially; above the hard
                    threshold the request is rejected with HTTP 429. Changes
                    take effect after server restart.
                </p>

                <label
                    class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer"
                >
                    <input
                        type="checkbox"
                        bind:checked={rlEnabled}
                        class="rounded border-slate-300"
                    />
                    Enable rate limiting on /login/pass and /login/register
                </label>

                <div class="grid grid-cols-2 gap-3">
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >Window (seconds)</label
                        >
                        <input
                            type="number"
                            min="1"
                            bind:value={rlWindowSec}
                            placeholder="900"
                            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >Backoff Base (seconds)</label
                        >
                        <input
                            type="number"
                            min="0"
                            bind:value={rlBackoffBaseSec}
                            placeholder="1"
                            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                </div>

                <div class="grid grid-cols-2 gap-3">
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >IP Soft Threshold</label
                        >
                        <input
                            type="number"
                            min="1"
                            bind:value={rlIPSoft}
                            placeholder="3"
                            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >IP Hard Threshold</label
                        >
                        <input
                            type="number"
                            min="1"
                            bind:value={rlIPHard}
                            placeholder="30"
                            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                </div>

                <div class="grid grid-cols-2 gap-3">
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >User Soft Threshold</label
                        >
                        <input
                            type="number"
                            min="1"
                            bind:value={rlUserSoft}
                            placeholder="3"
                            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                    <div>
                        <!-- svelte-ignore a11y_label_has_associated_control -->
                        <label
                            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                            >User Hard Threshold</label
                        >
                        <input
                            type="number"
                            min="1"
                            bind:value={rlUserHard}
                            placeholder="15"
                            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                    </div>
                </div>

                <div>
                    <!-- svelte-ignore a11y_label_has_associated_control -->
                    <label
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Backoff Max (seconds)</label
                    >
                    <input
                        type="number"
                        min="1"
                        bind:value={rlBackoffMaxSec}
                        placeholder="15"
                        class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                    />
                </div>

                <div>
                    <!-- svelte-ignore a11y_label_has_associated_control -->
                    <label
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Trusted Proxy CIDRs</label
                    >
                    <p class="text-xs text-slate-500 dark:text-slate-400 mb-2">
                        CIDR blocks whose X-Forwarded-For / X-Real-IP /
                        True-Client-IP headers are honored for client-IP
                        extraction. Leave empty when pika is directly
                        internet-facing — XFF is forgeable from untrusted
                        upstreams.
                    </p>
                    <div class="flex gap-2">
                        <input
                            type="text"
                            bind:value={rlTrustedProxyInput}
                            placeholder="10.0.0.0/8"
                            onkeydown={(e) => {
                                if (e.key === "Enter") {
                                    e.preventDefault();
                                    addRlTrustedProxy();
                                }
                            }}
                            class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                        <button
                            type="button"
                            onclick={addRlTrustedProxy}
                            class="px-3 py-2 text-sm bg-slate-100 dark:bg-warm-900 hover:bg-slate-200 dark:hover:bg-warm-700 rounded-md cursor-pointer"
                        >
                            <Plus size={14} />
                        </button>
                    </div>
                    {#if rlTrustedProxies.length > 0}
                        <div class="mt-2 space-y-1">
                            {#each rlTrustedProxies as cidr, i}
                                <div
                                    class="flex items-center justify-between gap-2 px-3 py-1.5 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md text-xs font-mono"
                                >
                                    <span>{cidr}</span>
                                    <button
                                        type="button"
                                        onclick={() => removeRlTrustedProxy(i)}
                                        class="text-slate-400 dark:text-slate-500 hover:text-red-600 cursor-pointer"
                                    >
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
    <div
        class="mb-3 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden"
    >
        <button
            type="button"
            class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:bg-warm-900 transition-colors cursor-pointer"
            onclick={() => toggleSection("capabilities")}
        >
            Capabilities &amp; Superadmins
            {#if openSections.has("capabilities")}<ChevronDown
                    size={15}
                />{:else}<ChevronRight size={15} />{/if}
        </button>
        {#if openSections.has("capabilities")}
            <div
                class="px-5 pb-5 pt-1 border-t border-slate-100 dark:border-warm-700 space-y-5"
            >
                <!-- Superadmins -->
                <div class="mt-3">
                    <!-- svelte-ignore a11y_label_has_associated_control -->
                    <label
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Superadmins</label
                    >
                    <p
                        class="text-[11px] text-slate-400 dark:text-slate-500 mb-2"
                    >
                        Identity subjects that bypass every permission check.
                        For the local strategy this is the pika username; for
                        OAuth2 this is the <code
                            class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded"
                            >sub</code
                        >
                        claim (often an opaque provider ID, not an email).
                    </p>
                    <div class="flex gap-2">
                        <input
                            type="text"
                            bind:value={capSuperadminInput}
                            placeholder="username or OIDC sub"
                            onkeydown={(e) => {
                                if (e.key === "Enter") {
                                    e.preventDefault();
                                    addSuperadmin();
                                }
                            }}
                            class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                        <button
                            type="button"
                            class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
                            onclick={addSuperadmin}>Add</button
                        >
                    </div>
                    {#if capSuperadmins.length > 0}
                        <div class="mt-2 flex flex-wrap gap-1.5">
                            {#each capSuperadmins as name, i}
                                <span
                                    class="inline-flex items-center gap-1 px-2 py-1 bg-amber-50 border border-amber-200 rounded text-xs font-mono text-amber-700"
                                >
                                    {name}
                                    <button
                                        type="button"
                                        class="w-3.5 h-3.5 flex items-center justify-center bg-transparent border-none cursor-pointer text-amber-400 hover:text-red-500"
                                        onclick={() => removeSuperadmin(i)}
                                        >&times;</button
                                    >
                                </span>
                            {/each}
                        </div>
                    {/if}
                </div>

                <!-- Role → permission mapping -->
                <div
                    class="pt-4 border-t border-slate-100 dark:border-warm-700"
                >
                    <!-- svelte-ignore a11y_label_has_associated_control -->
                    <label
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Role → Permissions</label
                    >
                    <p
                        class="text-[11px] text-slate-400 dark:text-slate-500 mb-2"
                    >
                        Maps a role name (from the identity's
                        <code
                            class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded"
                            >roles</code
                        >
                        claim for OAuth2, or the Roles header for the Header strategy)
                        to one or more pika permissions. A user carrying any listed
                        role is granted the union of those permissions' capabilities.
                    </p>
                    <div class="flex gap-2">
                        <input
                            type="text"
                            bind:value={capNewRoleKey}
                            placeholder="e.g. editor, admin"
                            onkeydown={(e) => {
                                if (e.key === "Enter") {
                                    e.preventDefault();
                                    addRoleMapping();
                                }
                            }}
                            class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                        <button
                            type="button"
                            class="flex items-center gap-1 px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
                            onclick={addRoleMapping}
                        >
                            <Plus size={13} />
                            Add role
                        </button>
                    </div>
                    {#if capRoleMappings.length === 0}
                        <p
                            class="mt-3 text-[11px] text-slate-400 dark:text-slate-500 italic"
                        >
                            No role mappings configured. External identities
                            (OAuth2 and Header) will get zero permissions
                            unless listed as superadmins.
                        </p>
                    {:else}
                        <div class="mt-3 space-y-2">
                            {#each capRoleMappings as row, rowIdx}
                                <div
                                    class="p-3 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
                                >
                                    <div
                                        class="flex items-center justify-between mb-2 gap-2"
                                    >
                                        <input
                                            type="text"
                                            bind:value={row.key}
                                            placeholder="role name"
                                            class="flex-1 px-2 py-1 text-xs font-mono bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                                        />
                                        <button
                                            type="button"
                                            class="p-1 text-slate-400 dark:text-slate-500 hover:text-red-500 transition-colors cursor-pointer"
                                            onclick={() =>
                                                removeRoleMapping(rowIdx)}
                                            aria-label="Remove role mapping"
                                        >
                                            <Trash2 size={13} />
                                        </button>
                                    </div>
                                    {#if availablePermissions.length === 0}
                                        <p
                                            class="text-[11px] text-amber-600 dark:text-amber-400"
                                        >
                                            No permissions defined yet. Create
                                            permissions in the Permissions
                                            section first, then assign them here.
                                        </p>
                                    {:else}
                                        <div
                                            class="grid grid-cols-2 gap-x-3 gap-y-1"
                                        >
                                            {#each availablePermissions as perm}
                                                <label
                                                    class="flex items-start gap-2 text-xs text-slate-700 dark:text-slate-200 cursor-pointer"
                                                >
                                                    <input
                                                        type="checkbox"
                                                        checked={row.permissions.includes(
                                                            perm.key,
                                                        )}
                                                        onchange={() =>
                                                            toggleRolePerm(
                                                                rowIdx,
                                                                perm.key,
                                                            )}
                                                        class="mt-0.5 rounded border-slate-300"
                                                    />
                                                    <span class="leading-tight">
                                                        <span
                                                            class="font-medium"
                                                            >{perm.name}</span
                                                        >
                                                        <span
                                                            class="block text-[10px] text-slate-400 dark:text-slate-500 font-mono"
                                                            >{perm.key}</span
                                                        >
                                                    </span>
                                                </label>
                                            {/each}
                                        </div>
                                    {/if}
                                    {#if unknownPermKeys(row).length > 0}
                                        <div class="mt-2 flex flex-wrap gap-1">
                                            {#each unknownPermKeys(row) as uk}
                                                <button
                                                    type="button"
                                                    onclick={() =>
                                                        toggleRolePerm(
                                                            rowIdx,
                                                            uk,
                                                        )}
                                                    title="Unknown permission — click to remove"
                                                    class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] font-mono rounded bg-amber-50 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300 border border-amber-300 dark:border-amber-700 cursor-pointer"
                                                >
                                                    {uk}
                                                    <Trash2 size={10} />
                                                </button>
                                            {/each}
                                        </div>
                                    {/if}
                                </div>
                            {/each}
                        </div>
                    {/if}
                </div>

                <!-- Scope → permission mapping -->
                <div
                    class="pt-4 border-t border-slate-100 dark:border-warm-700"
                >
                    <!-- svelte-ignore a11y_label_has_associated_control -->
                    <label
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Scope → Permissions</label
                    >
                    <p
                        class="text-[11px] text-slate-400 dark:text-slate-500 mb-2"
                    >
                        Maps an OAuth2 scope (from the token response or the
                        <code
                            class="px-1 py-0.5 bg-slate-100 dark:bg-warm-900 rounded"
                            >scope</code
                        >
                        claim) to pika permissions. Less common than role mappings;
                        use when you want "every user who got scope X" to gain specific
                        rights.
                    </p>
                    <div class="flex gap-2">
                        <input
                            type="text"
                            bind:value={capNewScopeKey}
                            placeholder="e.g. pika:admin"
                            onkeydown={(e) => {
                                if (e.key === "Enter") {
                                    e.preventDefault();
                                    addScopeMapping();
                                }
                            }}
                            class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                        />
                        <button
                            type="button"
                            class="flex items-center gap-1 px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
                            onclick={addScopeMapping}
                        >
                            <Plus size={13} />
                            Add scope
                        </button>
                    </div>
                    {#if capScopeMappings.length > 0}
                        <div class="mt-3 space-y-2">
                            {#each capScopeMappings as row, rowIdx}
                                <div
                                    class="p-3 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
                                >
                                    <div
                                        class="flex items-center justify-between mb-2 gap-2"
                                    >
                                        <input
                                            type="text"
                                            bind:value={row.key}
                                            placeholder="scope name"
                                            class="flex-1 px-2 py-1 text-xs font-mono bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                                        />
                                        <button
                                            type="button"
                                            class="p-1 text-slate-400 dark:text-slate-500 hover:text-red-500 transition-colors cursor-pointer"
                                            onclick={() =>
                                                removeScopeMapping(rowIdx)}
                                            aria-label="Remove scope mapping"
                                        >
                                            <Trash2 size={13} />
                                        </button>
                                    </div>
                                    {#if availablePermissions.length === 0}
                                        <p
                                            class="text-[11px] text-amber-600 dark:text-amber-400"
                                        >
                                            No permissions defined yet. Create
                                            permissions in the Permissions
                                            section first, then assign them here.
                                        </p>
                                    {:else}
                                        <div
                                            class="grid grid-cols-2 gap-x-3 gap-y-1"
                                        >
                                            {#each availablePermissions as perm}
                                                <label
                                                    class="flex items-start gap-2 text-xs text-slate-700 dark:text-slate-200 cursor-pointer"
                                                >
                                                    <input
                                                        type="checkbox"
                                                        checked={row.permissions.includes(
                                                            perm.key,
                                                        )}
                                                        onchange={() =>
                                                            toggleScopePerm(
                                                                rowIdx,
                                                                perm.key,
                                                            )}
                                                        class="mt-0.5 rounded border-slate-300"
                                                    />
                                                    <span class="leading-tight">
                                                        <span
                                                            class="font-medium"
                                                            >{perm.name}</span
                                                        >
                                                        <span
                                                            class="block text-[10px] text-slate-400 dark:text-slate-500 font-mono"
                                                            >{perm.key}</span
                                                        >
                                                    </span>
                                                </label>
                                            {/each}
                                        </div>
                                    {/if}
                                    {#if unknownPermKeys(row).length > 0}
                                        <div class="mt-2 flex flex-wrap gap-1">
                                            {#each unknownPermKeys(row) as uk}
                                                <button
                                                    type="button"
                                                    onclick={() =>
                                                        toggleScopePerm(
                                                            rowIdx,
                                                            uk,
                                                        )}
                                                    title="Unknown permission — click to remove"
                                                    class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] font-mono rounded bg-amber-50 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300 border border-amber-300 dark:border-amber-700 cursor-pointer"
                                                >
                                                    {uk}
                                                    <Trash2 size={10} />
                                                </button>
                                            {/each}
                                        </div>
                                    {/if}
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
            {saving ? "Saving..." : "Save Auth Settings"}
        </button>
    </div>
</div>
