<!--
  OAuth2ProviderCard — editor for a single OAuth2 provider entry in the
  Authentication settings. Mutates the bound entry in place.
-->
<script lang="ts">
  import { Trash2 } from "lucide-svelte";
  import { hasOAuth2ManualEndpoints, type OAuth2Entry } from "./types";

  type Props = {
    entry: OAuth2Entry;
    index: number;
    onRemove: () => void;
  };

  let { entry = $bindable(), index, onRemove }: Props = $props();

  const uid = $props.id();
  const idPrefix = `auth-oauth2-${uid}`;

  let scopeInput = $state("");
  let rolesClaimInput = $state("");

  function addRolesClaim() {
    const v = rolesClaimInput.trim();
    if (!v) return;
    if ((entry.roles_claims ?? []).includes(v)) return;
    entry.roles_claims = [...(entry.roles_claims ?? []), v];
    rolesClaimInput = "";
  }

  function removeRolesClaim(claimIdx: number) {
    entry.roles_claims = (entry.roles_claims ?? []).filter(
      (_, i) => i !== claimIdx,
    );
  }

  function addScope() {
    const v = scopeInput.trim();
    if (!v) return;
    entry.scopes = [...(entry.scopes ?? []), v];
    scopeInput = "";
  }

  function removeScope(scopeIdx: number) {
    entry.scopes = (entry.scopes ?? []).filter((_, i) => i !== scopeIdx);
  }
</script>

<div
    class="p-4 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg space-y-3"
>
    <div class="flex items-center justify-between mb-1">
        <span
            class="text-xs font-semibold text-slate-600 dark:text-slate-300"
            >Provider #{index + 1}</span
        >
        <button
            type="button"
            onclick={onRemove}
            class="p-1 text-slate-400 dark:text-slate-500 hover:text-vermilion-500 transition-colors cursor-pointer"
            aria-label="Remove provider #{index + 1}"
            title="Remove provider"
        >
            <Trash2 size={13} />
        </button>
    </div>
    <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div>
            <label
                for={`${idPrefix}-name-url-key`}
                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                >Name (URL key)</label
            >
            <input
                id={`${idPrefix}-name-url-key`}
                type="text"
                bind:value={entry.name}
                placeholder="google"
                class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
            />
        </div>
        <div>
            <label
                for={`${idPrefix}-display-name`}
                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                >Display Name</label
            >
            <input
                id={`${idPrefix}-display-name`}
                type="text"
                bind:value={entry.display_name}
                placeholder="Google"
                class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
            />
        </div>
    </div>
    <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div>
            <label
                for={`${idPrefix}-authorization-url`}
                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                >Authorization URL</label
            >
            <input
                id={`${idPrefix}-authorization-url`}
                type="text"
                bind:value={entry.auth_url}
                placeholder="https://gitlab.com/oauth/authorize"
                class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
            />
        </div>
        <div>
            <label
                for={`${idPrefix}-token-url`}
                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                >Token URL</label
            >
            <input
                id={`${idPrefix}-token-url`}
                type="text"
                bind:value={entry.token_url}
                placeholder="https://gitlab.com/oauth/token"
                class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
            />
        </div>
    </div>
    <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div>
            <label
                for={`oauth2-userinfo`}
                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                >UserInfo URL</label
            >
            <input
                id={`oauth2-userinfo`}
                type="text"
                bind:value={entry.userinfo_url}
                placeholder="https://gitlab.com/oauth/userinfo"
                class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
            />
            <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
                Fetches identity claims with the access token.
                Use this alternative when no JWKS URL is configured.
            </p>
        </div>
        <div>
            <label
                for={`oauth2-jwks`}
                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                >JWKS URL</label
            >
            <input
                id={`oauth2-jwks`}
                type="text"
                bind:value={entry.jwks_url}
                placeholder="Provider's jwks_uri"
                class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
            />
            <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
                Provides public keys to verify the <code>id_token</code>.
                Copy <code>jwks_uri</code> from your trusted provider's
                <code>.well-known/openid-configuration</code> document.
            </p>
        </div>
    </div>
    <p class="text-xs text-slate-500 dark:text-slate-400">
        For manual endpoints, configure at least one of JWKS URL
        or UserInfo URL. Leaving both blank cannot resolve identity securely.
    </p>
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
            <label
                for={`${idPrefix}-client-id`}
                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                >Client ID</label
            >
            <input
                id={`${idPrefix}-client-id`}
                type="text"
                bind:value={entry.client_id}
                class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
            />
        </div>
        <div>
            <label
                for={`${idPrefix}-client-secret`}
                class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
                >Client Secret</label
            >
            <input
                id={`${idPrefix}-client-secret`}
                type="password"
                bind:value={entry.client_secret}
                disabled={entry.clear_client_secret}
                placeholder={entry.clear_client_secret
                    ? "(will be cleared on save)"
                    : entry.client_secret_set
                      ? "(secret set — leave blank to keep)"
                      : "(no secret set)"}
                class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500 disabled:opacity-50 disabled:cursor-not-allowed"
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
                        class="rounded border-slate-300 dark:border-warm-600"
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
        <label
            for={`${idPrefix}-client-authentication`}
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
            >Client authentication</label
        >
        <select
            id={`${idPrefix}-client-authentication`}
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
        <label
            for={`${idPrefix}-scopes`}
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
            >Scopes</label
        >
        <div class="flex gap-2">
            <input
                id={`${idPrefix}-scopes`}
                type="text"
                bind:value={scopeInput}
                placeholder="openid"
                onkeydown={(e) => {
                    if (e.key === "Enter") {
                        e.preventDefault();
                        addScope();
                    }
                }}
                class="flex-1 px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
            />
            <button
                type="button"
                class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
                onclick={() => addScope()}
                >Add</button
            >
        </div>
        {#if (entry.scopes ?? []).length > 0}
            <div class="mt-2 flex flex-wrap gap-1.5">
                {#each entry.scopes ?? [] as scope, si}
                    <span
                        class="inline-flex items-center gap-1 px-2 py-1 bg-accent-50 dark:bg-accent-900/40 border border-accent-200 dark:border-accent-700 rounded text-xs font-mono text-accent-700 dark:text-accent-300"
                    >
                        {scope}
                        <button
                            type="button"
                            class="w-3.5 h-3.5 flex items-center justify-center bg-transparent border-none cursor-pointer text-accent-400 hover:text-vermilion-500"
                            aria-label="Remove scope {scope}"
                            onclick={() =>
                                removeScope(si)}
                            >&times;</button
                        >
                    </span>
                {/each}
            </div>
        {/if}
    </div>
    <!-- Roles claim paths -->
    <div>
        <label
            for={`${idPrefix}-roles-claim-path-s`}
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
            >Roles claim path(s)</label
        >
        <div class="flex gap-2">
            <input
                id={`${idPrefix}-roles-claim-path-s`}
                type="text"
                bind:value={rolesClaimInput}
                placeholder="realm_access.roles"
                onkeydown={(e) => {
                    if (e.key === "Enter") {
                        e.preventDefault();
                        addRolesClaim();
                    }
                }}
                class="flex-1 px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
            />
            <button
                type="button"
                class="px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
                onclick={() => addRolesClaim()}
                >Add</button
            >
        </div>
        {#if (entry.roles_claims ?? []).length > 0}
            <div class="mt-2 flex flex-wrap gap-1.5">
                {#each entry.roles_claims ?? [] as claim, ci}
                    <span
                        class="inline-flex items-center gap-1 px-2 py-1 bg-accent-50 dark:bg-accent-900/40 border border-accent-200 dark:border-accent-700 rounded text-xs font-mono text-accent-700 dark:text-accent-300"
                    >
                        {claim}
                        <button
                            type="button"
                            class="w-3.5 h-3.5 flex items-center justify-center bg-transparent border-none cursor-pointer text-accent-400 hover:text-vermilion-500"
                            aria-label="Remove claim path {claim}"
                            onclick={() =>
                                removeRolesClaim(ci)}
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
                class="rounded border-slate-300 dark:border-warm-600"
            />
            Disable PKCE
        </label>
        <label
            class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
        >
            <input
                type="checkbox"
                bind:checked={entry.password_flow}
                class="rounded border-slate-300 dark:border-warm-600"
            />
            Password flow
        </label>
        <label
            class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
        >
            <input
                type="checkbox"
                bind:checked={entry.auto_create_user}
                class="rounded border-slate-300 dark:border-warm-600"
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
