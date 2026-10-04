<script lang="ts">
    import { apiServerMessage } from "@/lib/api/client";
    import { addToast } from "@/lib/store/toast.svelte";
    import { appStore } from "@/lib/store/store.svelte";
    import { onMount } from "svelte";
    import { Plus, Trash2 } from "lucide-svelte";
    import CollapsibleCard from "@/lib/components/CollapsibleCard.svelte";
    import OAuth2ProviderCard from "@/lib/components/auth/OAuth2ProviderCard.svelte";
    import PermissionMappingEditor from "@/lib/components/auth/PermissionMappingEditor.svelte";
    import {
        hasOAuth2ManualEndpoints,
        mapToRows,
        nsToSec,
        rowsToMap,
        secToNs,
        type AuthSettings,
        type MappingRow,
        type OAuth2Entry,
    } from "@/lib/components/auth/types";
    import axios from "axios";

    // External role/scope mappings grant pika Permission bundles (not raw
    // capabilities). The catalog of selectable permissions comes from
    // /api/v1/permissions (appStore.permissions); each carries a stable `key`
    // plus a human `name`. Mapping an external role to a bundle keeps a single
    // source of truth — edit the bundle once and both internal users and
    // external roles follow.
    const availablePermissions = $derived(appStore.permissions ?? []);

    // ── Auth settings shape ──



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
    let cookieDisableHttpOnly = $state(false);
    let cookieSameSite = $state("");

    // Issuer fields
    let issuerAccessTTL = $state<number | "">("");
    let issuerRefreshTTL = $state<number | "">("");
    let issuerDisableRefreshRotation = $state(false);

    // Local strategy
    let localEnabled = $state(false);
    let localName = $state("");
    let localLoginFormCollapsed = $state(false);
    let accountSecurityAdminOnly = $state(false);

    // OAuth2 entries
    let oauth2Entries = $state<OAuth2Entry[]>([]);

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
        cookieDisableHttpOnly = auth.cookie?.disable_http_only ?? false;
        cookieSameSite = auth.cookie?.same_site ?? "";

        issuerAccessTTL = auth.issuer?.access_ttl ?? "";
        issuerRefreshTTL = auth.issuer?.refresh_ttl ?? "";
        issuerDisableRefreshRotation =
            auth.issuer?.disable_refresh_rotation ?? false;

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
        if (cookieDisableHttpOnly) cookieBlock.disable_http_only = true;
        if (cookieSameSite) cookieBlock.same_site = cookieSameSite;
        if (Object.keys(cookieBlock).length > 0) auth.cookie = cookieBlock;

        // Issuer
        const issuerBlock: AuthSettings["issuer"] = {};
        if (issuerAccessTTL !== "")
            issuerBlock.access_ttl = Number(issuerAccessTTL);
        if (issuerRefreshTTL !== "")
            issuerBlock.refresh_ttl = Number(issuerRefreshTTL);
        if (issuerDisableRefreshRotation)
            issuerBlock.disable_refresh_rotation = true;
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
                if (e.jwks_url) entry.jwks_url = e.jwks_url;
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
        } catch (err) {
            const msg = apiServerMessage(err, "Failed to save auth settings");
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
    }

    function removeOAuth2(i: number) {
        oauth2Entries = oauth2Entries.filter((_, idx) => idx !== i);
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
        } catch (err) {
            loadError = apiServerMessage(err, "Failed to load settings");
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
            class="mb-4 p-3 bg-red-50 dark:bg-red-950/40 border border-red-300 dark:border-red-700 rounded-md text-sm text-red-700 dark:text-red-300"
        >
            {loadError}
        </div>
    {/if}

    {#if restartRequired}
        <div
            class="mb-4 p-3 bg-amber-50 dark:bg-amber-950/40 border border-amber-300 dark:border-amber-700 rounded-md text-sm text-amber-800 dark:text-amber-200"
        >
            A server restart is required for some changes to take effect.
        </div>
    {/if}

    <!-- ── UI section ── -->
    <CollapsibleCard
        title="Login UI"
        open={openSections.has("ui")}
        onToggle={() => toggleSection("ui")}
        bodyClass="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700"
    >
            <div class="grid grid-cols-2 gap-3">
                <div>
                    <label
                        for="auth-title"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Title</label
                    >
                    <input
                        id="auth-title"
                        type="text"
                        bind:value={uiTitle}
                        placeholder="pika"
                        class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
                <div>
                    <label
                        for="auth-subtitle"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Subtitle</label
                    >
                    <input
                        id="auth-subtitle"
                        type="text"
                        bind:value={uiSubtitle}
                        placeholder="optional"
                        class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
            </div>
            <div>
                <label
                    for="auth-icon-url-data-uri"
                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                    >Icon URL / data URI</label
                >
                <input
                    id="auth-icon-url-data-uri"
                    type="text"
                    bind:value={uiIcon}
                    placeholder="https://example.com/logo.png"
                    class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                />
            </div>
            <div>
                <label
                    for="auth-custom-css-url"
                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                    >Custom CSS URL</label
                >
                <input
                    id="auth-custom-css-url"
                    type="text"
                    bind:value={uiCustomCSSUrl}
                    placeholder="https://example.com/login.css"
                    class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                />
            </div>
    </CollapsibleCard>

    <!-- ── Cookie section ── -->
    <CollapsibleCard
        title="Cookie"
        open={openSections.has("cookie")}
        onToggle={() => toggleSection("cookie")}
        bodyClass="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700"
    >
            <div class="grid grid-cols-3 gap-3">
                <div>
                    <label
                        for="auth-cookie-name"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Cookie Name</label
                    >
                    <input
                        id="auth-cookie-name"
                        type="text"
                        bind:value={cookieName}
                        placeholder="pika_session"
                        class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
                <div>
                    <label
                        for="auth-domain"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Domain</label
                    >
                    <input
                        id="auth-domain"
                        type="text"
                        bind:value={cookieDomain}
                        placeholder=".example.com"
                        class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
                <div>
                    <label
                        for="auth-path"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Path</label
                    >
                    <input
                        id="auth-path"
                        type="text"
                        bind:value={cookiePath}
                        placeholder="/"
                        class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
            </div>
            <div class="grid grid-cols-2 gap-3">
                <div>
                    <label
                        for="auth-samesite"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >SameSite</label
                    >
                    <select
                        id="auth-samesite"
                        bind:value={cookieSameSite}
                        class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
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
                            class="rounded border-slate-300 dark:border-warm-600"
                        />
                        Always mark Secure
                    </label>
                    <label
                        class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer"
                    >
                        <input
                            type="checkbox"
                            bind:checked={cookieDisableHttpOnly}
                            class="rounded border-slate-300 dark:border-warm-600"
                        />
                        Expose cookie to JavaScript
                    </label>
                </div>
            </div>
            <div class="px-4 pb-4 -mt-1">
                <p
                    class="text-[11px] text-slate-400 dark:text-slate-500 leading-relaxed"
                >
                    <span
                        class="font-medium text-slate-600 dark:text-slate-300"
                        >Secure</span
                    >
                    is applied automatically whenever a request arrives over
                    HTTPS, so you normally leave this off. Turn it on when TLS
                    terminates at a proxy that doesn't forward a protocol hint
                    and the automatic detection can't see it.
                    <br />
                    <span
                        class="font-medium text-slate-600 dark:text-slate-300"
                        >HttpOnly</span
                    > is on by default and should stay that way — nothing in
                    the UI reads the session cookie, and a cookie readable by
                    script is one XSS away from being stolen.
                </p>
            </div>
    </CollapsibleCard>

    <!-- ── Issuer section ── -->
    <CollapsibleCard
        title="Token Issuance"
        open={openSections.has("issuer")}
        onToggle={() => toggleSection("issuer")}
        bodyClass="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700"
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
                    <label
                        for="auth-access-ttl-seconds"
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
                        id="auth-access-ttl-seconds"
                        type="number"
                        bind:value={issuerAccessTTL}
                        placeholder="900"
                        class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
                <div>
                    <label
                        for="auth-refresh-ttl-seconds"
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
                        id="auth-refresh-ttl-seconds"
                        type="number"
                        bind:value={issuerRefreshTTL}
                        placeholder="86400"
                        class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
            </div>
            <div class="pt-2">
                <label
                    class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer"
                >
                    <input
                        type="checkbox"
                        bind:checked={issuerDisableRefreshRotation}
                        class="rounded border-slate-300 dark:border-warm-600"
                    />
                    Disable refresh-token rotation
                </label>
                <p
                    class="text-[11px] text-slate-400 dark:text-slate-500 mt-1.5 leading-relaxed"
                >
                    <span
                        class="font-medium text-slate-600 dark:text-slate-300"
                        >Off (default):</span
                    > the refresh token is replaced with a new one every time
                    it's used, restarting the TTL clock. An active user stays
                    logged in indefinitely; only a gap of inactivity longer than
                    Refresh TTL forces re-login. It also limits refresh-token
                    theft — a stolen token stops working the moment the real
                    user refreshes.
                    <br />
                    <span
                        class="font-medium text-slate-600 dark:text-slate-300"
                        >On:</span
                    >
                    the Refresh TTL becomes a <em>hard ceiling</em>. Even a
                    user who visits every day is sent back to the login
                    screen once Refresh TTL elapses from their original
                    login time. Only worth it if several clients share one
                    session and would rotate each other out of it.
                </p>
            </div>
    </CollapsibleCard>

    <!-- ── Local strategy ── -->
    <CollapsibleCard
        title="Local (Password) Strategy"
        open={openSections.has("local")}
        onToggle={() => toggleSection("local")}
        bodyClass="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700"
    >
            <label
                class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer"
            >
                <input
                    type="checkbox"
                    bind:checked={localEnabled}
                    class="rounded border-slate-300 dark:border-warm-600"
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
                <label
                    for="auth-strategy-label"
                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                    >Strategy label</label
                >
                <input
                    id="auth-strategy-label"
                    type="text"
                    bind:value={localName}
                    placeholder="Local"
                    class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
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
                        class="mt-0.5 rounded border-slate-300 dark:border-warm-600"
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
    </CollapsibleCard>

    <!-- ── OAuth2 strategies ── -->
    <CollapsibleCard
        title="OAuth2 Providers"
        open={openSections.has("oauth2")}
        onToggle={() => toggleSection("oauth2")}
        bodyClass="px-5 pb-5 pt-3 border-t border-slate-100 dark:border-warm-700 space-y-4"
    >
            {#each oauth2Entries as entry, i (entry)}
                <OAuth2ProviderCard
                    bind:entry={oauth2Entries[i]}
                    index={i}
                    onRemove={() => removeOAuth2(i)}
                />
            {/each}
            <button
                type="button"
                onclick={addOAuth2}
                class="flex items-center gap-1.5 px-3 py-2 text-sm text-accent-700 dark:text-accent-300 bg-accent-50 dark:bg-accent-900/40 rounded-md hover:bg-accent-100 dark:hover:bg-accent-900/60 transition-colors cursor-pointer"
            >
                <Plus size={13} /> Add Provider
            </button>
    </CollapsibleCard>

    <!-- ── Rate Limiting ── -->
    <CollapsibleCard
        title="Rate Limiting (Brute-Force Protection)"
        open={openSections.has("rate_limit")}
        onToggle={() => toggleSection("rate_limit")}
        bodyClass="px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700"
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
                    class="rounded border-slate-300 dark:border-warm-600"
                />
                Enable rate limiting on /login/pass and /login/register
            </label>

            <div class="grid grid-cols-2 gap-3">
                <div>
                    <label
                        for="auth-window-seconds"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Window (seconds)</label
                    >
                    <input
                        id="auth-window-seconds"
                        type="number"
                        min="1"
                        bind:value={rlWindowSec}
                        placeholder="900"
                        class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
                <div>
                    <label
                        for="auth-backoff-base-seconds"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >Backoff Base (seconds)</label
                    >
                    <input
                        id="auth-backoff-base-seconds"
                        type="number"
                        min="0"
                        bind:value={rlBackoffBaseSec}
                        placeholder="1"
                        class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
            </div>

            <div class="grid grid-cols-2 gap-3">
                <div>
                    <label
                        for="auth-ip-soft-threshold"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >IP Soft Threshold</label
                    >
                    <input
                        id="auth-ip-soft-threshold"
                        type="number"
                        min="1"
                        bind:value={rlIPSoft}
                        placeholder="3"
                        class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
                <div>
                    <label
                        for="auth-ip-hard-threshold"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >IP Hard Threshold</label
                    >
                    <input
                        id="auth-ip-hard-threshold"
                        type="number"
                        min="1"
                        bind:value={rlIPHard}
                        placeholder="30"
                        class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
            </div>

            <div class="grid grid-cols-2 gap-3">
                <div>
                    <label
                        for="auth-user-soft-threshold"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >User Soft Threshold</label
                    >
                    <input
                        id="auth-user-soft-threshold"
                        type="number"
                        min="1"
                        bind:value={rlUserSoft}
                        placeholder="3"
                        class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
                <div>
                    <label
                        for="auth-user-hard-threshold"
                        class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                        >User Hard Threshold</label
                    >
                    <input
                        id="auth-user-hard-threshold"
                        type="number"
                        min="1"
                        bind:value={rlUserHard}
                        placeholder="15"
                        class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </div>
            </div>

            <div>
                <label
                    for="auth-backoff-max-seconds"
                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                    >Backoff Max (seconds)</label
                >
                <input
                    id="auth-backoff-max-seconds"
                    type="number"
                    min="1"
                    bind:value={rlBackoffMaxSec}
                    placeholder="15"
                    class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                />
            </div>

            <div>
                <label
                    for="auth-trusted-proxy-cidrs"
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
                        id="auth-trusted-proxy-cidrs"
                        type="text"
                        bind:value={rlTrustedProxyInput}
                        placeholder="10.0.0.0/8"
                        onkeydown={(e) => {
                            if (e.key === "Enter") {
                                e.preventDefault();
                                addRlTrustedProxy();
                            }
                        }}
                        class="flex-1 px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                    <button
                        type="button"
                        onclick={addRlTrustedProxy}
                        class="px-3 py-2 text-sm bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 rounded-md cursor-pointer" aria-label="Add trusted proxy CIDR" title="Add trusted proxy CIDR"
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
                                    class="text-slate-400 dark:text-slate-500 hover:text-vermilion-600 cursor-pointer"
                                >
                                    <Trash2 size={12} />
                                </button>
                            </div>
                        {/each}
                    </div>
                {/if}
            </div>
    </CollapsibleCard>

    <!-- ── Capabilities ── -->
    <CollapsibleCard
        title="Capabilities & Superadmins"
        open={openSections.has("capabilities")}
        onToggle={() => toggleSection("capabilities")}
        bodyClass="px-5 pb-5 pt-1 border-t border-slate-100 dark:border-warm-700 space-y-5"
    >
            <!-- Superadmins -->
            <div class="mt-3">
                <label
                    for="auth-superadmins"
                    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                    >Superadmins</label
                >
                <p
                    class="text-[11px] text-slate-400 dark:text-slate-500 mb-2"
                >
                    Identity subjects that bypass every permission check.
                    For the local strategy this is the pika username; for
                    OAuth2 this is the <code
                        class="px-1 py-0.5 bg-slate-100 dark:bg-warm-700 rounded"
                        >sub</code
                    >
                    claim (often an opaque provider ID, not an email).
                </p>
                <div class="flex gap-2">
                    <input
                        id="auth-superadmins"
                        type="text"
                        bind:value={capSuperadminInput}
                        placeholder="username or OIDC sub"
                        onkeydown={(e) => {
                            if (e.key === "Enter") {
                                e.preventDefault();
                                addSuperadmin();
                            }
                        }}
                        class="flex-1 px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
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
                                class="inline-flex items-center gap-1 px-2 py-1 bg-amber-50 dark:bg-amber-950/40 border border-amber-200 dark:border-amber-700 rounded text-xs font-mono text-amber-700 dark:text-amber-300"
                            >
                                {name}
                                <button
                                    type="button"
                                    class="w-3.5 h-3.5 flex items-center justify-center bg-transparent border-none cursor-pointer text-amber-400 hover:text-vermilion-500"
                                    onclick={() => removeSuperadmin(i)}
                                    >&times;</button
                                >
                            </span>
                        {/each}
                    </div>
                {/if}
            </div>

            <!-- Role → permission mapping -->
            <PermissionMappingEditor
                bind:rows={capRoleMappings}
                {availablePermissions}
                label="Role → Permissions"
                noun="role"
                placeholder="e.g. editor, admin"
                inputId="auth-role-permissions"
                emptyText="No role mappings configured. External identities (OAuth2 and Header) will get zero permissions unless listed as superadmins."
            >
                {#snippet description()}
                    Maps a role name (from the identity's
                    <code
                        class="px-1 py-0.5 bg-slate-100 dark:bg-warm-700 rounded"
                        >roles</code
                    >
                    claim for OAuth2, or the Roles header for the Header strategy)
                    to one or more pika permissions. A user carrying any listed
                    role is granted the union of those permissions' capabilities.
                {/snippet}
            </PermissionMappingEditor>

            <!-- Scope → permission mapping -->
            <PermissionMappingEditor
                bind:rows={capScopeMappings}
                {availablePermissions}
                label="Scope → Permissions"
                noun="scope"
                placeholder="e.g. pika:admin"
                inputId="auth-scope-permissions"
            >
                {#snippet description()}
                    Maps an OAuth2 scope (from the token response or the
                    <code
                        class="px-1 py-0.5 bg-slate-100 dark:bg-warm-700 rounded"
                        >scope</code
                    >
                    claim) to pika permissions. Less common than role mappings;
                    use when you want "every user who got scope X" to gain specific
                    rights.
                {/snippet}
            </PermissionMappingEditor>
    </CollapsibleCard>

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
