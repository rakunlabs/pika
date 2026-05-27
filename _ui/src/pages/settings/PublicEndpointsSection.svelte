<script lang="ts">
    import { configStore } from "@/lib/store/config.svelte";
    import { addToast } from "@/lib/store/toast.svelte";
    import { onMount } from "svelte";
    import {
        Plus,
        Trash2,
        Plug,
        Play,
        RefreshCw,
        AlertTriangle,
        ShieldCheck,
        Power,
        PowerOff,
    } from "lucide-svelte";
    import type {
        PublicEndpoint,
        PublicEndpointStatus,
        PublicEndpointTestResult,
        EndpointAuth,
    } from "@/lib/types/config";

    // ── Local state mirrors of the settings list and the live
    // diagnostic status so the UI can show a "running" badge next
    // to each entry without re-loading the whole settings document.
    let endpoints = $state<PublicEndpoint[]>([]);
    let statuses = $state<PublicEndpointStatus[]>([]);
    let loading = $state(false);
    let saving = $state(false);

    // Modal / form state for create + edit. We keep one shared form
    // and copy values in/out on open/save so the cancel path is
    // trivial.
    let formOpen = $state(false);
    let editingId = $state<string | null>(null);
    let form = $state<PublicEndpoint>(newEmptyEndpoint());
    const externalResourceNames = $derived(
        Object.keys(configStore.settings?.external || {}).sort(),
    );

    // Probe panel state (per-endpoint).
    let probeOpenFor = $state<string | null>(null);
    let probeKey = $state("");
    let probeVariant = $state("");
    let probeVersion = $state("");
    let probeRaw = $state(false);
    let probeFormat = $state("");
    // probeHeaders is an editable list of (name, value) pairs the
    // operator wants forwarded onto the synthetic request — auth
    // tokens, request-check policy headers (X-Tenant, etc.), or
    // anything else the live handler chain inspects.
    let probeHeaders = $state<{ name: string; value: string }[]>([]);
    let probeResult = $state<PublicEndpointTestResult | null>(null);
    let probing = $state(false);

    function newEmptyEndpoint(): PublicEndpoint {
        // Default new endpoints to "static" — the simplest config
        // data mode: path tail -> config key, no Go template and no
        // compatibility envelope.
        return {
            id: "",
            name: "",
            enabled: true,
            listen_host: "0.0.0.0",
            listen_port: 9090,
            base_path: "/",
            mode: "static",
            static: defaultStaticConfig(),
            consul: undefined,
            external: undefined,
            custom: undefined,
            auth: { mode: "none" },
            request_check: undefined,
        };
    }

    // Request check helpers ------------------------------------
    //
    // The stage is a list of declarative rules. The operator turns
    // it on by adding at least one rule; toggling the parent off
    // drops the whole field so the backend serializes it cleanly.

    type RuleDraft = import('@/lib/types/config').RequestRule;

    function ensureRequestCheck() {
        if (!form.request_check) {
            form.request_check = { rules: [] };
        }
        if (!form.request_check.rules) {
            form.request_check.rules = [];
        }
    }

    function addRequestRule() {
        ensureRequestCheck();
        form.request_check!.rules!.push({
            name: "",
            enabled: true,
            when: {},
            then: { type: "allow" },
        });
    }

    function removeRequestRule(idx: number) {
        if (!form.request_check?.rules) return;
        form.request_check.rules.splice(idx, 1);
        if (form.request_check.rules.length === 0) {
            form.request_check = undefined;
        }
    }

    function moveRule(idx: number, dir: -1 | 1) {
        const rules = form.request_check?.rules;
        if (!rules) return;
        const j = idx + dir;
        if (j < 0 || j >= rules.length) return;
        [rules[idx], rules[j]] = [rules[j], rules[idx]];
    }

    // When picker: operator selects ONE matcher shape from a
    // small enum and the form materialises the matching field.
    // Multi-matcher (AND) rules need multiple rule rows — keeps
    // the per-row UI trivial.
    type WhenKind =
        | "any"
        | "method"
        | "path_equals"
        | "path_prefix"
        | "header_equals"
        | "header_present"
        | "header_absent"
        | "query_equals"
        | "query_present"
        | "query_absent";

    function whenKindOf(rule: RuleDraft): WhenKind {
        const w = rule.when || {};
        if (w.method) return "method";
        if (w.path_equals) return "path_equals";
        if (w.path_prefix) return "path_prefix";
        if (w.header_equals) return "header_equals";
        if (w.header_present) return "header_present";
        if (w.header_absent) return "header_absent";
        if (w.query_equals) return "query_equals";
        if (w.query_present) return "query_present";
        if (w.query_absent) return "query_absent";
        return "any";
    }

    function setWhenKind(rule: RuleDraft, kind: WhenKind) {
        // Reset matcher then populate the chosen field with an
        // empty value so the form binds without optional-chain
        // surprises.
        rule.when = {};
        switch (kind) {
            case "any":
                break;
            case "method":
                rule.when.method = "GET";
                break;
            case "path_equals":
                rule.when.path_equals = "/";
                break;
            case "path_prefix":
                rule.when.path_prefix = "/";
                break;
            case "header_equals":
                rule.when.header_equals = { name: "X-Tenant", value: "" };
                break;
            case "header_present":
                rule.when.header_present = "X-Tenant";
                break;
            case "header_absent":
                rule.when.header_absent = "X-Tenant";
                break;
            case "query_equals":
                rule.when.query_equals = { name: "variant", value: "" };
                break;
            case "query_present":
                rule.when.query_present = "variant";
                break;
            case "query_absent":
                rule.when.query_absent = "variant";
                break;
        }
    }

    function setActionType(
        rule: RuleDraft,
        t: import('@/lib/types/config').RequestActionType,
    ) {
        // Recreate the action so stale fields (e.g. a status from
        // a previous "block" choice) don't sneak through when the
        // type changes.
        switch (t) {
            case "allow":
                rule.then = { type: "allow" };
                break;
            case "block":
                rule.then = {
                    type: "block",
                    status: rule.then.status ?? 403,
                    body: rule.then.body ?? "",
                    content_type:
                        rule.then.content_type ?? "application/json",
                };
                break;
            case "set_header":
                rule.then = {
                    type: "set_header",
                    name: rule.then.name ?? "X-Tenant",
                    value: rule.then.value ?? "",
                };
                break;
            case "del_header":
                rule.then = {
                    type: "del_header",
                    name: rule.then.name ?? "Cookie",
                };
                break;
            case "set_query":
                rule.then = {
                    type: "set_query",
                    name: rule.then.name ?? "variant",
                    value: rule.then.value ?? "prod",
                };
                break;
            case "del_query":
                rule.then = {
                    type: "del_query",
                    name: rule.then.name ?? "debug",
                };
                break;
            case "set_path":
                rule.then = {
                    type: "set_path",
                    value: rule.then.value ?? "/",
                };
                break;
            case "replace_path":
                rule.then = {
                    type: "replace_path",
                    pattern: rule.then.pattern ?? "^/legacy/(.*)$",
                    value: rule.then.value ?? "/$1",
                };
                break;
        }
    }

    function ruleSummary(rule: RuleDraft): string {
        const w = rule.when || {};
        const parts: string[] = [];
        if (w.method) parts.push(`method=${w.method}`);
        if (w.path_equals) parts.push(`path=${w.path_equals}`);
        if (w.path_prefix) parts.push(`path^=${w.path_prefix}`);
        if (w.header_equals)
            parts.push(
                `${w.header_equals.name}=${w.header_equals.value || "?"}`,
            );
        if (w.header_present) parts.push(`has ${w.header_present}`);
        if (w.header_absent) parts.push(`no ${w.header_absent}`);
        if (w.query_equals)
            parts.push(
                `?${w.query_equals.name}=${w.query_equals.value || "?"}`,
            );
        if (w.query_present) parts.push(`?${w.query_present}`);
        if (w.query_absent) parts.push(`no ?${w.query_absent}`);
        const when = parts.length ? parts.join(" & ") : "any";

        const t = rule.then;
        let then: string = t.type;
        if (t.type === "block") then = `block ${t.status ?? 403}`;
        else if (t.type === "set_header") then = `set ${t.name}=${t.value}`;
        else if (t.type === "del_header") then = `del ${t.name}`;
        else if (t.type === "set_query") then = `set ?${t.name}=${t.value}`;
        else if (t.type === "del_query") then = `del ?${t.name}`;
        else if (t.type === "set_path") then = `path → ${t.value}`;
        else if (t.type === "replace_path") then = `path s/${t.pattern}/${t.value}/`;
        return `when ${when} → ${then}`;
    }

    onMount(() => {
        void (async () => {
            if (!configStore.settings) {
                await configStore.loadSettings();
            }
            endpoints = [...(configStore.settings?.public_endpoints || [])];
            await refreshStatus();
        })();
    });

    async function refreshStatus() {
        loading = true;
        try {
            statuses = await configStore.listPublicEndpointStatus();
        } finally {
            loading = false;
        }
    }

    function statusFor(id: string): PublicEndpointStatus | undefined {
        return statuses.find((s) => s.id === id);
    }

    function openCreate() {
        form = newEmptyEndpoint();
        editingId = null;
        formOpen = true;
    }

    function openEdit(ep: PublicEndpoint) {
        // Clone so the modal can safely discard edits on cancel.
        form = JSON.parse(JSON.stringify(ep));
        // The backend never echoes stored static tokens; the field
        // arrives empty. Surface an empty array so the textarea
        // edits in-place. Operators who want to keep their tokens
        // should leave the field alone (we send back an empty list
        // only when they explicitly clear it).
        if (form.auth.mode === "static_token" && !form.auth.static_tokens) {
            form.auth.static_tokens = [];
        }
        if (form.mode === "static" && !form.static) {
            form.static = defaultStaticConfig();
        }
        if (form.mode === "external" && !form.external) {
            form.external = defaultExternalConfig();
        }
        editingId = ep.id;
        formOpen = true;
    }

    function defaultStaticConfig() {
        return {};
    }

    function defaultExternalConfig() {
        return { resource: externalResourceNames[0] ?? "" };
    }

    function setMode(m: "static" | "consul" | "external" | "custom") {
        form.mode = m;
        if (m === "static") {
            form.static = form.static ?? defaultStaticConfig();
            form.consul = undefined;
            form.external = undefined;
            form.custom = undefined;
        } else if (m === "consul") {
            form.static = undefined;
            form.consul = {};
            form.external = undefined;
            form.custom = undefined;
        } else if (m === "external") {
            form.static = undefined;
            form.consul = undefined;
            form.external = form.external ?? defaultExternalConfig();
            form.custom = undefined;
        } else {
            form.static = undefined;
            form.external = undefined;
            form.custom = form.custom ?? {
                body_template: defaultConsulEnvelopeTemplate(),
                content_type: "application/json",
                status_on_missing: 404,
                allow_format_override: true,
            };
            form.consul = undefined;
        }
    }

    function setAuthMode(m: EndpointAuth["mode"]) {
        form.auth.mode = m;
        if (m !== "static_token") {
            form.auth.static_tokens = undefined;
            form.auth.header_name = undefined;
        } else {
            form.auth.static_tokens = form.auth.static_tokens ?? [];
            form.auth.header_name = form.auth.header_name ?? "Authorization";
        }
    }

    function defaultConsulEnvelopeTemplate(): string {
        // A useful starting template: produces the Consul-shaped
        // JSON envelope so new users have a concrete example to
        // adapt.
        return [
            "[",
            '  {',
            '    "Key": "{{ .Key }}",',
            '    "Value": "{{ .DataB64 }}",',
            '    "CreateIndex": 0,',
            '    "ModifyIndex": 0,',
            '    "LockIndex": 0,',
            '    "Flags": 0,',
            '    "Session": ""',
            '  }',
            "]",
        ].join("\n");
    }

    async function save() {
        if (form.mode === "external" && !form.external?.resource) {
            addToast("Choose an external resource for this endpoint", "alert");
            return;
        }
        saving = true;
        try {
            const next = endpoints.slice();
            if (editingId) {
                const idx = next.findIndex((e) => e.id === editingId);
                if (idx >= 0) {
                    next[idx] = form;
                } else {
                    next.push(form);
                }
            } else {
                // New endpoints have empty IDs — backend mints them.
                next.push(form);
            }
            await configStore.savePublicEndpoints(next);
            // Pull the persisted shape (now with IDs, timestamps)
            // back out of the store so the local mirror matches.
            endpoints = [
                ...(configStore.settings?.public_endpoints || next),
            ];
            formOpen = false;
            await refreshStatus();
        } catch {
            /* configStore shows the toast */
        } finally {
            saving = false;
        }
    }

    async function deleteEndpoint(id: string) {
        if (
            !confirm(
                "Delete this public endpoint? The listener will stop immediately.",
            )
        ) {
            return;
        }
        const next = endpoints.filter((e) => e.id !== id);
        try {
            await configStore.savePublicEndpoints(next);
            endpoints = [
                ...(configStore.settings?.public_endpoints || next),
            ];
            await refreshStatus();
        } catch {
            /* toast handled in store */
        }
    }

    async function toggleEnabled(ep: PublicEndpoint) {
        const next = endpoints.map((e) =>
            e.id === ep.id ? { ...e, enabled: !e.enabled } : e,
        );
        try {
            await configStore.savePublicEndpoints(next);
            endpoints = [
                ...(configStore.settings?.public_endpoints || next),
            ];
            await refreshStatus();
        } catch {
            /* toast handled in store */
        }
    }

    function openProbe(ep: PublicEndpoint) {
        probeOpenFor = ep.id;
        probeKey = "";
        probeVariant = "";
        probeVersion = "";
        probeRaw = false;
        probeFormat = "";
        probeHeaders = [];
        probeResult = null;
    }

    function addProbeHeader() {
        probeHeaders.push({ name: "", value: "" });
    }
    function removeProbeHeader(idx: number) {
        probeHeaders.splice(idx, 1);
    }

    async function runProbe() {
        if (!probeOpenFor) return;
        probing = true;
        probeResult = null;
        try {
            const hdrs: Record<string, string> = {};
            for (const h of probeHeaders) {
                const name = h.name.trim();
                if (name) hdrs[name] = h.value;
            }
            probeResult = await configStore.testPublicEndpoint(probeOpenFor, {
                key: probeKey,
                variant: probeVariant || undefined,
                version: probeVersion || undefined,
                raw: probeRaw || undefined,
                format: probeFormat || undefined,
                headers: Object.keys(hdrs).length > 0 ? hdrs : undefined,
            });
        } catch (err: any) {
            const msg =
                err?.response?.data?.message || err?.message || "Probe failed";
            addToast(msg, "alert");
        } finally {
            probing = false;
        }
    }

    function staticTokensText(): string {
        return (form.auth.static_tokens ?? []).join("\n");
    }
    function setStaticTokens(text: string) {
        form.auth.static_tokens = text
            .split(/\r?\n/)
            .map((t) => t.trim())
            .filter((t) => t.length > 0);
    }
</script>

<div class="space-y-6">
    <div class="flex items-start justify-between gap-4">
        <div>
            <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">
                Endpoints
            </h2>
            <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
                Expose pika config data on operator-defined HTTP ports, with
                optional per-request inspection / modification.
            </p>
        </div>
        <div class="flex gap-2">
            <button
                type="button"
                onclick={refreshStatus}
                class="px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer disabled:opacity-50 inline-flex items-center gap-1.5"
                disabled={loading}
            >
                <RefreshCw size={14} class={loading ? "animate-spin" : ""} />
                Refresh
            </button>
            <button
                type="button"
                onclick={openCreate}
                class="px-3 py-1.5 text-xs rounded bg-accent-600 hover:bg-accent-700 text-white cursor-pointer inline-flex items-center gap-1.5"
            >
                <Plus size={14} />
                New endpoint
            </button>
        </div>
    </div>

    {#if endpoints.length === 0}
        <div
            class="p-6 rounded-lg border border-dashed border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-800 text-center"
        >
            <Plug size={20} class="mx-auto text-slate-400 dark:text-slate-500" />
            <p class="mt-2 text-sm text-slate-600 dark:text-slate-300">
                No endpoints configured yet.
            </p>
            <p class="text-xs text-slate-500 dark:text-slate-400 mt-1">
                Add one to serve direct config bytes, Consul KV compatibility,
                or a custom response shape.
            </p>
        </div>
    {:else}
        <div class="space-y-3">
            {#each endpoints as ep (ep.id || ep.name)}
                {@const status = statusFor(ep.id)}
                <div
                    class="p-4 rounded-lg border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-800"
                >
                    <div class="flex items-start justify-between gap-4">
                        <div class="min-w-0">
                            <div class="flex items-center gap-2 flex-wrap">
                                <h3
                                    class="text-sm font-semibold text-slate-800 dark:text-slate-100 truncate"
                                >
                                    {ep.name || "(unnamed)"}
                                </h3>
                                {#if !ep.enabled}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 dark:bg-warm-700 text-slate-600 dark:text-slate-300"
                                        >disabled</span
                                    >
                                {:else if status?.running}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 inline-flex items-center gap-1"
                                    >
                                        <ShieldCheck size={10} /> running
                                    </span>
                                {:else if status?.last_error}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-amber-100 dark:bg-amber-900/40 text-amber-800 dark:text-amber-300 inline-flex items-center gap-1"
                                    >
                                        <AlertTriangle size={10} /> bind failed
                                    </span>
                                {:else}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 dark:bg-warm-700 text-slate-600 dark:text-slate-300"
                                        >pending</span
                                    >
                                {/if}
                                <span
                                    class="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 dark:bg-warm-700 text-slate-700 dark:text-slate-200"
                                    >mode: {ep.mode}</span
                                >
                                <span
                                    class="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 dark:bg-warm-700 text-slate-700 dark:text-slate-200"
                                    >auth: {ep.auth.mode}</span
                                >
                                {#if ep.request_check}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-indigo-100 dark:bg-indigo-900/40 text-indigo-700 dark:text-indigo-300"
                                        >request check</span
                                    >
                                {/if}
                            </div>
                            <p
                                class="text-xs text-slate-500 dark:text-slate-400 mt-1 font-mono"
                            >
                                http://{ep.listen_host || "0.0.0.0"}:{ep.listen_port}{ep.base_path ===
                                "/"
                                    ? ""
                                    : ep.base_path}
                            </p>
                            {#if status?.last_error}
                                <p
                                    class="text-xs text-amber-700 dark:text-amber-300 mt-1 font-mono"
                                >
                                    {status.last_error}
                                </p>
                            {/if}
                        </div>
                        <div class="flex gap-1 shrink-0">
                            <button
                                type="button"
                                onclick={() => toggleEnabled(ep)}
                                class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-600 dark:text-slate-300 cursor-pointer"
                                title={ep.enabled ? "Disable" : "Enable"}
                            >
                                {#if ep.enabled}
                                    <PowerOff size={14} />
                                {:else}
                                    <Power size={14} />
                                {/if}
                            </button>
                            <button
                                type="button"
                                onclick={() => openProbe(ep)}
                                class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-600 dark:text-slate-300 cursor-pointer"
                                title="Test"
                            >
                                <Play size={14} />
                            </button>
                            <button
                                type="button"
                                onclick={() => openEdit(ep)}
                                class="px-2 py-1 text-xs rounded bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer"
                            >
                                Edit
                            </button>
                            <button
                                type="button"
                                onclick={() => deleteEndpoint(ep.id)}
                                class="p-1.5 rounded hover:bg-red-50 dark:hover:bg-red-900/30 text-red-600 dark:text-red-400 cursor-pointer"
                                title="Delete"
                            >
                                <Trash2 size={14} />
                            </button>
                        </div>
                    </div>

                    {#if probeOpenFor === ep.id}
                        <div
                            class="mt-4 p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 space-y-3"
                        >
                            <div class="flex items-center gap-2 flex-wrap">
                                <input
                                    type="text"
                                    bind:value={probeKey}
                                    placeholder="config/key/path"
                                    class="flex-1 min-w-[12rem] px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono"
                                />
                                <input
                                    type="text"
                                    bind:value={probeVariant}
                                    placeholder="variant (optional)"
                                    class="w-32 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100"
                                />
                                <input
                                    type="text"
                                    bind:value={probeVersion}
                                    placeholder="version"
                                    class="w-24 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100"
                                />
                                <select
                                    bind:value={probeFormat}
                                    class="px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100"
                                >
                                    <option value="">format (auto)</option>
                                    <option value="json">json</option>
                                    <option value="yaml">yaml</option>
                                    <option value="toml">toml</option>
                                </select>
                                <label
                                    class="inline-flex items-center gap-1 text-xs text-slate-700 dark:text-slate-200"
                                >
                                    <input
                                        type="checkbox"
                                        bind:checked={probeRaw}
                                        class="rounded"
                                    /> raw
                                </label>
                                <button
                                    type="button"
                                    onclick={runProbe}
                                    disabled={probing}
                                    class="px-2 py-1 text-xs rounded bg-accent-600 hover:bg-accent-700 text-white cursor-pointer inline-flex items-center gap-1 disabled:opacity-50"
                                >
                                    <Play size={12} />
                                    Probe
                                </button>
                                <button
                                    type="button"
                                    onclick={() => (probeOpenFor = null)}
                                    class="text-xs text-slate-500 dark:text-slate-400 hover:underline cursor-pointer"
                                >
                                    Close
                                </button>
                            </div>

                            <div class="space-y-1.5">
                                <div class="flex items-center gap-2">
                                    <span class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400">
                                        Request headers
                                    </span>
                                    <button
                                        type="button"
                                        onclick={addProbeHeader}
                                        class="px-1.5 py-0.5 text-[11px] rounded bg-slate-200 dark:bg-warm-700 hover:bg-slate-300 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer inline-flex items-center gap-1"
                                    >
                                        <Plus size={10} /> add
                                    </button>
                                    <span class="text-[10px] text-slate-400 dark:text-slate-500">
                                        forwarded onto the synthetic request — use this for auth tokens, X-Tenant, …
                                    </span>
                                </div>
                                {#each probeHeaders as h, i}
                                    <div class="flex gap-2 items-center">
                                        <input
                                            type="text"
                                            bind:value={h.name}
                                            placeholder="Header"
                                            class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono"
                                        />
                                        <input
                                            type="text"
                                            bind:value={h.value}
                                            placeholder="value"
                                            class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono"
                                        />
                                        <button
                                            type="button"
                                            onclick={() => removeProbeHeader(i)}
                                            class="p-1 rounded hover:bg-red-50 dark:hover:bg-red-900/30 text-red-600 dark:text-red-400 cursor-pointer"
                                            title="Remove"
                                        >
                                            <Trash2 size={12} />
                                        </button>
                                    </div>
                                {/each}
                            </div>
                            {#if probeResult}
                                <div class="text-xs space-y-1">
                                    <div
                                        class="font-mono text-slate-600 dark:text-slate-300"
                                    >
                                        HTTP {probeResult.status}
                                    </div>
                                    {#if probeResult.headers && Object.keys(probeResult.headers).length > 0}
                                        <div
                                            class="font-mono text-slate-500 dark:text-slate-400"
                                        >
                                            {#each Object.entries(probeResult.headers) as [k, v]}
                                                <div>{k}: {v}</div>
                                            {/each}
                                        </div>
                                    {/if}
                                    <pre
                                        class="text-[11px] p-2 rounded bg-slate-900 text-slate-100 overflow-auto max-h-64">{probeResult.body}</pre>
                                </div>
                            {/if}
                        </div>
                    {/if}
                </div>
            {/each}
        </div>
    {/if}
</div>

{#if formOpen}
    <div
        class="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4"
    >
        <button
            type="button"
            aria-label="Close dialog"
            class="absolute inset-0 w-full h-full cursor-default"
            onclick={() => (formOpen = false)}
        ></button>
        <div
            class="relative bg-white dark:bg-warm-800 rounded-lg shadow-xl max-w-3xl w-full max-h-[90vh] overflow-y-auto border border-slate-200 dark:border-warm-700"
            role="dialog"
            aria-modal="true"
            tabindex={-1}
        >
            <div
                class="p-4 border-b border-slate-200 dark:border-warm-700 flex items-center justify-between"
            >
                <h3 class="text-base font-semibold text-slate-800 dark:text-slate-100">
                    {editingId ? "Edit endpoint" : "New endpoint"}
                </h3>
                <button
                    type="button"
                    onclick={() => (formOpen = false)}
                    class="text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 text-sm cursor-pointer"
                >
                    Cancel
                </button>
            </div>

            <div class="p-4 space-y-5">
                <section class="space-y-3">
                    <h4 class="text-xs uppercase tracking-wider text-slate-500 dark:text-slate-400">
                        General
                    </h4>
                    <div class="grid grid-cols-2 gap-3">
                        <label class="text-xs space-y-1 block">
                            <span class="text-slate-600 dark:text-slate-300">Name</span>
                            <input
                                type="text"
                                bind:value={form.name}
                                placeholder="consul-prod"
                                class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100"
                            />
                        </label>
                        <label class="text-xs space-y-1 block">
                            <span class="text-slate-600 dark:text-slate-300">
                                Base path
                            </span>
                            <input
                                type="text"
                                bind:value={form.base_path}
                                placeholder="/consul"
                                class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono"
                            />
                        </label>
                        <label class="text-xs space-y-1 block">
                            <span class="text-slate-600 dark:text-slate-300">Listen host</span>
                            <input
                                type="text"
                                bind:value={form.listen_host}
                                placeholder="0.0.0.0"
                                class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono"
                            />
                        </label>
                        <label class="text-xs space-y-1 block">
                            <span class="text-slate-600 dark:text-slate-300">Listen port</span>
                            <input
                                type="number"
                                bind:value={form.listen_port}
                                min="1"
                                max="65535"
                                class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100"
                            />
                        </label>
                    </div>
                    <label class="inline-flex items-center gap-2 text-xs text-slate-700 dark:text-slate-200">
                        <input
                            type="checkbox"
                            bind:checked={form.enabled}
                            class="rounded"
                        />
                        Enabled (bind the listener)
                    </label>
                </section>

                <section class="space-y-3">
                    <div class="flex items-center gap-2">
                        <span class="inline-flex items-center justify-center w-5 h-5 rounded-full bg-slate-200 dark:bg-warm-700 text-[10px] font-semibold text-slate-700 dark:text-slate-200">1</span>
                        <h4 class="text-xs uppercase tracking-wider text-slate-500 dark:text-slate-400">
                            Authentication
                        </h4>
                    </div>
                    <div class="flex gap-2">
                        <button
                            type="button"
                            onclick={() => setAuthMode("none")}
                            class="px-3 py-1.5 text-xs rounded border cursor-pointer
                                {form.auth.mode === 'none'
                                ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                                : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200'}"
                        >
                            None
                        </button>
                        <button
                            type="button"
                            onclick={() => setAuthMode("bearer_token")}
                            class="px-3 py-1.5 text-xs rounded border cursor-pointer
                                {form.auth.mode === 'bearer_token'
                                ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                                : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200'}"
                        >
                            Pika API token
                        </button>
                        <button
                            type="button"
                            onclick={() => setAuthMode("static_token")}
                            class="px-3 py-1.5 text-xs rounded border cursor-pointer
                                {form.auth.mode === 'static_token'
                                ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                                : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200'}"
                        >
                            Static token
                        </button>
                    </div>
                    {#if form.auth.mode === "none"}
                        <p class="text-xs text-amber-700 dark:text-amber-300 inline-flex items-center gap-1">
                            <AlertTriangle size={12} />
                            No authentication — only safe behind a private
                            network or a fronting reverse proxy.
                        </p>
                    {:else if form.auth.mode === "bearer_token"}
                        <p class="text-xs text-slate-500 dark:text-slate-400">
                            Requires an
                            <code class="font-mono">Authorization: Bearer</code>
                            header carrying any pika API token with the
                            <code class="font-mono">files.read</code>
                            capability.
                        </p>
                    {:else if form.auth.mode === "static_token"}
                        <div class="grid grid-cols-2 gap-3">
                            <label class="text-xs space-y-1 block col-span-2">
                                <span class="text-slate-600 dark:text-slate-300">
                                    Allowed tokens (one per line)
                                </span>
                                <textarea
                                    rows="3"
                                    value={staticTokensText()}
                                    oninput={(e) =>
                                        setStaticTokens(
                                            (e.target as HTMLTextAreaElement)
                                                .value,
                                        )}
                                    class="w-full px-2 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono"
                                ></textarea>
                            </label>
                            <label class="text-xs space-y-1 block">
                                <span class="text-slate-600 dark:text-slate-300">
                                    Header name
                                </span>
                                <input
                                    type="text"
                                    bind:value={form.auth.header_name}
                                    placeholder="Authorization"
                                    class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono"
                                />
                            </label>
                            <p class="text-[11px] text-slate-500 dark:text-slate-400 col-span-2">
                                Tokens are sealed at rest. Leaving this list
                                empty when editing keeps the previously stored
                                tokens; an explicit empty list clears them.
                            </p>
                        </div>
                    {/if}
                </section>

                <section class="space-y-3">
                    <div class="flex items-center gap-2">
                        <span class="inline-flex items-center justify-center w-5 h-5 rounded-full bg-slate-200 dark:bg-warm-700 text-[10px] font-semibold text-slate-700 dark:text-slate-200">2</span>
                        <h4 class="text-xs uppercase tracking-wider text-slate-500 dark:text-slate-400">
                            Request rules
                        </h4>
                        <span class="text-[10px] text-slate-400 dark:text-slate-500">
                            optional — runs between auth and the shim
                        </span>
                    </div>
                    <p class="text-xs text-slate-500 dark:text-slate-400">
                        Rules evaluate top-to-bottom. <strong>allow</strong> and
                        <strong>block</strong> stop evaluation; <strong>set_*</strong>/<strong>del_*</strong>
                        modify the request and keep going. If nothing
                        terminates, the request reaches the shim.
                    </p>

                    {#if form.request_check?.rules && form.request_check.rules.length > 0}
                        <div class="space-y-3">
                            {#each form.request_check.rules as rule, idx}
                                {@const kind = whenKindOf(rule)}
                                <div
                                    class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 space-y-2"
                                >
                                    <div class="flex items-center gap-2 flex-wrap">
                                        <span class="text-[10px] font-mono text-slate-500 dark:text-slate-400">#{idx + 1}</span>
                                        <input
                                            type="text"
                                            bind:value={rule.name}
                                            placeholder="rule name (optional)"
                                            class="flex-1 min-w-[8rem] px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100"
                                        />
                                        <label class="inline-flex items-center gap-1 text-[11px] text-slate-700 dark:text-slate-200">
                                            <input
                                                type="checkbox"
                                                bind:checked={rule.enabled}
                                                class="rounded"
                                            />
                                            enabled
                                        </label>
                                        <button
                                            type="button"
                                            onclick={() => moveRule(idx, -1)}
                                            disabled={idx === 0}
                                            class="px-1.5 py-0.5 text-[11px] rounded bg-slate-200 dark:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer disabled:opacity-40"
                                            title="Move up"
                                        >↑</button>
                                        <button
                                            type="button"
                                            onclick={() => moveRule(idx, 1)}
                                            disabled={idx === form.request_check!.rules!.length - 1}
                                            class="px-1.5 py-0.5 text-[11px] rounded bg-slate-200 dark:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer disabled:opacity-40"
                                            title="Move down"
                                        >↓</button>
                                        <button
                                            type="button"
                                            onclick={() => removeRequestRule(idx)}
                                            class="p-1 rounded hover:bg-red-50 dark:hover:bg-red-900/30 text-red-600 dark:text-red-400 cursor-pointer"
                                            title="Delete rule"
                                        >
                                            <Trash2 size={12} />
                                        </button>
                                    </div>

                                    <div class="grid grid-cols-[auto_1fr_1fr] gap-2 items-center text-xs">
                                        <span class="text-slate-500 dark:text-slate-400 font-mono">when</span>
                                        <select
                                            value={kind}
                                            onchange={(e) =>
                                                setWhenKind(
                                                    rule,
                                                    (e.target as HTMLSelectElement)
                                                        .value as WhenKind,
                                                )}
                                            class="px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100"
                                        >
                                            <option value="any">any request</option>
                                            <option value="method">method equals</option>
                                            <option value="path_equals">path equals</option>
                                            <option value="path_prefix">path starts with</option>
                                            <option value="header_equals">header equals</option>
                                            <option value="header_present">header present</option>
                                            <option value="header_absent">header missing</option>
                                            <option value="query_equals">query equals</option>
                                            <option value="query_present">query present</option>
                                            <option value="query_absent">query missing</option>
                                        </select>
                                        <div class="flex gap-2">
                                            {#if kind === "method"}
                                                <input type="text" bind:value={rule.when.method} placeholder="GET" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {:else if kind === "path_equals"}
                                                <input type="text" bind:value={rule.when.path_equals} placeholder="/v1/kv/foo" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {:else if kind === "path_prefix"}
                                                <input type="text" bind:value={rule.when.path_prefix} placeholder="/v1/kv/" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {:else if kind === "header_equals" && rule.when.header_equals}
                                                <input type="text" bind:value={rule.when.header_equals.name} placeholder="X-Tenant" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                                <input type="text" bind:value={rule.when.header_equals.value} placeholder="value" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {:else if kind === "header_present"}
                                                <input type="text" bind:value={rule.when.header_present} placeholder="X-Tenant" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {:else if kind === "header_absent"}
                                                <input type="text" bind:value={rule.when.header_absent} placeholder="X-Tenant" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {:else if kind === "query_equals" && rule.when.query_equals}
                                                <input type="text" bind:value={rule.when.query_equals.name} placeholder="variant" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                                <input type="text" bind:value={rule.when.query_equals.value} placeholder="value" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {:else if kind === "query_present"}
                                                <input type="text" bind:value={rule.when.query_present} placeholder="variant" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {:else if kind === "query_absent"}
                                                <input type="text" bind:value={rule.when.query_absent} placeholder="variant" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {/if}
                                        </div>
                                    </div>

                                    <div class="grid grid-cols-[auto_1fr_1fr] gap-2 items-center text-xs">
                                        <span class="text-slate-500 dark:text-slate-400 font-mono">then</span>
                                        <select
                                            value={rule.then.type}
                                            onchange={(e) =>
                                                setActionType(
                                                    rule,
                                                    (e.target as HTMLSelectElement)
                                                        .value as import('@/lib/types/config').RequestActionType,
                                                )}
                                            class="px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100"
                                        >
                                            <option value="allow">allow (stop)</option>
                                            <option value="block">block (stop)</option>
                                            <option value="set_header">set header</option>
                                            <option value="del_header">delete header</option>
                                            <option value="set_query">set query param</option>
                                            <option value="del_query">delete query param</option>
                                            <option value="set_path">set path (literal)</option>
                                            <option value="replace_path">regex replace path</option>
                                        </select>
                                        <div class="flex gap-2">
                                            {#if rule.then.type === "block"}
                                                <input type="number" min="100" max="599" bind:value={rule.then.status} placeholder="403" class="w-20 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100" />
                                                <input type="text" bind:value={rule.then.body} placeholder="response body" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100" />
                                            {:else if rule.then.type === "set_header" || rule.then.type === "set_query"}
                                                <input type="text" bind:value={rule.then.name} placeholder={rule.then.type === "set_header" ? "X-Tenant" : "variant"} class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                                <input type="text" bind:value={rule.then.value} placeholder="value" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {:else if rule.then.type === "del_header" || rule.then.type === "del_query"}
                                                <input type="text" bind:value={rule.then.name} placeholder={rule.then.type === "del_header" ? "Cookie" : "debug"} class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {:else if rule.then.type === "set_path"}
                                                <input type="text" bind:value={rule.then.value} placeholder="/new/path" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {:else if rule.then.type === "replace_path"}
                                                <input type="text" bind:value={rule.then.pattern} placeholder="^/legacy/(.*)$" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                                <input type="text" bind:value={rule.then.value} placeholder="/$1" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                            {/if}
                                        </div>
                                    </div>

                                    <p class="text-[11px] text-slate-500 dark:text-slate-400 font-mono">
                                        {ruleSummary(rule)}
                                    </p>
                                    {#if rule.then.type === "set_path"}
                                        <div class="text-[11px] text-slate-500 dark:text-slate-400 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded px-2 py-1.5">
                                            <strong class="text-slate-600 dark:text-slate-300">Path rewrite is literal.</strong>
                                            It replaces the whole request path, not a regex capture.
                                            Example: when path starts with
                                            <code class="font-mono">/legacy/app</code>, set path to
                                            <code class="font-mono">/myapp/config</code> so the shim sees
                                            <code class="font-mono">/myapp/config</code>.
                                        </div>
                                    {:else if rule.then.type === "replace_path"}
                                        <div class="text-[11px] text-slate-500 dark:text-slate-400 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded px-2 py-1.5">
                                            <strong class="text-slate-600 dark:text-slate-300">Regex path replacement.</strong>
                                            The first field is a Go regexp pattern; the second is the replacement.
                                            Capture groups use
                                            <code class="font-mono">$1</code> or
                                            <code class="font-mono">$name</code>.
                                            Example:
                                            <code class="font-mono">^/legacy/(.*)$</code>
                                            → <code class="font-mono">/$1</code>
                                            turns <code class="font-mono">/legacy/myapp/config</code>
                                            into <code class="font-mono">/myapp/config</code>.
                                        </div>
                                    {/if}
                                </div>
                            {/each}
                        </div>
                    {:else}
                        <p class="text-xs text-slate-400 dark:text-slate-500 italic">
                            No rules yet — requests pass straight through to the shim.
                        </p>
                    {/if}

                    <button
                        type="button"
                        onclick={addRequestRule}
                        class="px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer inline-flex items-center gap-1.5"
                    >
                        <Plus size={12} /> Add rule
                    </button>
                </section>

                <section class="space-y-3">
                    <div class="flex items-center gap-2">
                        <span class="inline-flex items-center justify-center w-5 h-5 rounded-full bg-slate-200 dark:bg-warm-700 text-[10px] font-semibold text-slate-700 dark:text-slate-200">3</span>
                        <h4 class="text-xs uppercase tracking-wider text-slate-500 dark:text-slate-400">
                            Mode
                        </h4>
                    </div>
                    <div class="flex gap-2">
                        <button
                            type="button"
                            onclick={() => setMode("static")}
                            class="px-3 py-1.5 text-xs rounded border cursor-pointer
                                {form.mode === 'static'
                                ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                                : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200'}"
                        >
                            Config data (no template)
                        </button>
                        <button
                            type="button"
                            onclick={() => setMode("consul")}
                            class="px-3 py-1.5 text-xs rounded border cursor-pointer
                                {form.mode === 'consul'
                                ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                                : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200'}"
                        >
                            Consul KV (read-only)
                        </button>
                        <button
                            type="button"
                            onclick={() => setMode("external")}
                            class="px-3 py-1.5 text-xs rounded border cursor-pointer
                                {form.mode === 'external'
                                ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                                : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200'}"
                        >
                            External resource
                        </button>
                        <button
                            type="button"
                            onclick={() => setMode("custom")}
                            class="px-3 py-1.5 text-xs rounded border cursor-pointer
                                {form.mode === 'custom'
                                ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                                : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200'}"
                        >
                            Custom Go template
                        </button>
                    </div>

                    {#if form.mode === "static" && form.static}
                        <div class="space-y-2">
                            <p class="text-xs text-slate-500 dark:text-slate-400">
                                Returns resolved config bytes directly, same as
                                <code class="font-mono">/data/&lt;key&gt;</code>.
                                The URL tail after the base path is the config
                                key; <code class="font-mono">?variant=</code>,
                                <code class="font-mono">?version=</code>, and
                                <code class="font-mono">?format=</code> work the
                                same way. No Go template.
                            </p>
                            <p class="text-[11px] text-slate-500 dark:text-slate-400 font-mono">
                                GET {form.base_path && form.base_path !== "/" ? form.base_path : ""}/myapp/config → pika /data/myapp/config
                            </p>
                        </div>
                    {:else if form.mode === "consul"}
                        <p class="text-xs text-slate-500 dark:text-slate-400">
                            Mounts <code class="font-mono">{form.base_path || "/"}/v1/kv/&lt;key&gt;</code> with
                            Consul's read-only KV semantics. Supports
                            <code class="font-mono">?raw</code>,
                            <code class="font-mono">?variant</code>,
                            <code class="font-mono">?version</code>,
                            <code class="font-mono">?format</code>.
                        </p>
                    {:else if form.mode === "external" && form.external}
                        <div class="space-y-2">
                            <p class="text-xs text-slate-500 dark:text-slate-400">
                                Reads directly from one configured External
                                resource. The URL tail after the base path is
                                passed as the provider-specific path; pika's
                                <code class="font-mono">/data</code> inheritance
                                pipeline is not used.
                            </p>
                            <label class="text-xs space-y-1 block">
                                <span class="text-slate-600 dark:text-slate-300">External resource</span>
                                <select
                                    bind:value={form.external.resource}
                                    class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100"
                                >
                                    {#if externalResourceNames.length === 0}
                                        <option value="">No external resources configured</option>
                                    {:else}
                                        {#each externalResourceNames as name}
                                            <option value={name}>{name}</option>
                                        {/each}
                                    {/if}
                                </select>
                            </label>
                            {#if externalResourceNames.length === 0}
                                <p class="text-xs text-amber-700 dark:text-amber-300 inline-flex items-center gap-1">
                                    <AlertTriangle size={12} /> Add an External resource first, then select it here.
                                </p>
                            {:else}
                                <p class="text-[11px] text-slate-500 dark:text-slate-400 font-mono">
                                    GET {form.base_path && form.base_path !== "/" ? form.base_path : ""}/apps/api/db → {form.external.resource || "resource"}:apps/api/db
                                </p>
                            {/if}
                        </div>
                    {:else if form.mode === "custom" && form.custom}
                        <div class="space-y-2">
                            <label class="text-xs space-y-1 block">
                                <span class="text-slate-600 dark:text-slate-300">Body template (Go text/template)</span>
                                <textarea
                                    bind:value={form.custom.body_template}
                                    rows="10"
                                    class="w-full px-2 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono"
                                ></textarea>
                            </label>
                            <div class="grid grid-cols-3 gap-3">
                                <label class="text-xs space-y-1 block">
                                    <span class="text-slate-600 dark:text-slate-300">Content-Type</span>
                                    <input
                                        type="text"
                                        bind:value={form.custom.content_type}
                                        placeholder="application/json"
                                        class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono"
                                    />
                                </label>
                                <label class="text-xs space-y-1 block">
                                    <span class="text-slate-600 dark:text-slate-300">Status on missing</span>
                                    <input
                                        type="number"
                                        bind:value={form.custom.status_on_missing}
                                        min="100"
                                        max="599"
                                        class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100"
                                    />
                                </label>
                                <label class="inline-flex items-center gap-2 text-xs text-slate-700 dark:text-slate-200 mt-5">
                                    <input
                                        type="checkbox"
                                        bind:checked={form.custom.allow_format_override}
                                        class="rounded"
                                    />
                                    Allow ?format=
                                </label>
                            </div>
                            <details
                                class="text-xs text-slate-600 dark:text-slate-300"
                            >
                                <summary class="cursor-pointer">
                                    Template variables reference
                                </summary>
                                <ul class="ml-4 mt-1 space-y-0.5 font-mono">
                                    <li>.Key — resolved config key</li>
                                    <li>.Variant / .Version — query params</li>
                                    <li>.Raw — bool, ?raw flag presence</li>
                                    <li>.Format — requested output format</li>
                                    <li>.Data — []byte response body</li>
                                    <li>.DataString — string view of .Data</li>
                                    <li>.DataB64 — base64-encoded .Data</li>
                                    <li>.Found — false when key missing</li>
                                    <li>.ResolvedFormat — actual stored format</li>
                                    <li>.Now — current server time</li>
                                </ul>
                            </details>
                        </div>
                    {/if}
                </section>
            </div>

            <div
                class="p-4 border-t border-slate-200 dark:border-warm-700 flex justify-end gap-2"
            >
                <button
                    type="button"
                    onclick={() => (formOpen = false)}
                    class="px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer"
                >
                    Cancel
                </button>
                <button
                    type="button"
                    onclick={save}
                    disabled={saving}
                    class="px-3 py-1.5 text-xs rounded bg-accent-600 hover:bg-accent-700 text-white cursor-pointer disabled:opacity-50"
                >
                    {saving ? "Saving…" : "Save"}
                </button>
            </div>
        </div>
    </div>
{/if}
