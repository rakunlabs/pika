<!--
  EndpointFormModal — create / edit dialog for a single public endpoint.
  Works on a deep clone of the endpoint so cancel simply discards it.
-->
<script lang="ts">
    import { untrack } from "svelte";
    import { AlertTriangle, Plus } from "lucide-svelte";
    import Modal from "@/lib/components/Modal.svelte";
    import { configStore } from "@/lib/store/config.svelte";
    import { addToast } from "@/lib/store/toast.svelte";
    import type {
        EndpointAuth,
        PublicEndpoint,
        RequestRuleTestResult,
    } from "@/lib/types/config";
    import RuleEditor from "./RuleEditor.svelte";
    import RuleTester from "./RuleTester.svelte";
    import { defaultRequestRule, type RuleDraft } from "./rules";

    type Props = {
        /** Endpoint to edit, or null to create a new one. */
        endpoint: PublicEndpoint | null;
        /** Current saved list; the edited entry is merged into it on save. */
        endpoints: PublicEndpoint[];
        onSaved: (next: PublicEndpoint[]) => void;
        onClose: () => void;
    };

    let { endpoint, endpoints, onSaved, onClose }: Props = $props();

    const externalResourceNames = $derived(
        Object.keys(configStore.settings?.external || {}).sort(),
    );

    const editingId = untrack(() => endpoint?.id ?? null);
    let form = $state<PublicEndpoint>(untrack(() => initialForm(endpoint)));
    let saving = $state(false);
    let ruleTestResult = $state<RequestRuleTestResult | null>(null);

    function initialForm(ep: PublicEndpoint | null): PublicEndpoint {
        if (!ep) return newEmptyEndpoint();
        // Clone so the modal can safely discard edits on cancel.
        const f: PublicEndpoint = JSON.parse(JSON.stringify(ep));
        // The backend never echoes stored static tokens; the field
        // arrives empty. Surface an empty array so the textarea
        // edits in-place. Operators who want to keep their tokens
        // should leave the field alone (we send back an empty list
        // only when they explicitly clear it).
        if (f.auth.mode === "static_token" && !f.auth.static_tokens) {
            f.auth.static_tokens = [];
        }
        if (f.mode === "static" && !f.static) {
            f.static = defaultStaticConfig();
        }
        if (f.mode === "external" && !f.external) {
            f.external = defaultExternalConfig();
        }
        f.tls = f.tls ?? { enabled: false };
        return f;
    }

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
            tls: { enabled: true },
            request_check: undefined,
        };
    }

    // Request check helpers ------------------------------------
    //
    // The stage is a list of declarative rules. The operator turns
    // it on by adding at least one rule; removing the last rule
    // drops the whole field so the backend serializes it cleanly.

    function setRequestRules(rules: RuleDraft[]) {
        form.request_check = rules.length
            ? { ...(form.request_check ?? {}), rules }
            : undefined;
        ruleTestResult = null;
    }

    function addRequestRule() {
        setRequestRules([
            ...(form.request_check?.rules ?? []),
            defaultRequestRule(),
        ]);
    }

    function removeRequestRule(idx: number) {
        const rules = form.request_check?.rules;
        if (!rules) return;
        setRequestRules(rules.filter((_, i) => i !== idx));
    }

    function replaceRule(idx: number, next: RuleDraft) {
        const rules = form.request_check?.rules;
        if (!rules) return;
        setRequestRules(rules.map((r, i) => (i === idx ? next : r)));
    }

    function moveRule(idx: number, dir: -1 | 1) {
        const rules = form.request_check?.rules;
        if (!rules) return;
        const j = idx + dir;
        if (j < 0 || j >= rules.length) return;
        const next = [...rules];
        [next[idx], next[j]] = [next[j], next[idx]];
        setRequestRules(next);
    }

    function defaultStaticConfig() {
        return {};
    }

    function defaultExternalConfig() {
        return { resource: externalResourceNames[0] ?? "" };
    }

    // Tri-state view of ExternalCompat.raw_value for the response-mode
    // select. Persisted as boolean | null | undefined on the wire;
    // we map missing/null → "inherit" so the picker shows a stable
    // default until the operator chooses to override.
    type ExternalRawMode = "inherit" | "raw" | "wrapped";
    const externalRawMode: ExternalRawMode = $derived.by(() => {
        if (form.mode !== "external" || !form.external) return "inherit";
        if (form.external.raw_value === true) return "raw";
        if (form.external.raw_value === false) return "wrapped";
        return "inherit";
    });

    function setExternalRawMode(mode: ExternalRawMode) {
        if (!form.external) return;
        switch (mode) {
            case "inherit":
                // Drop the override entirely so the resource's own
                // raw_value/content_type take effect on the next request.
                // We also clear content_type because the placeholder
                // copy switches to "inherit"; leaving a leftover value
                // would silently still apply.
                form.external.raw_value = undefined;
                form.external.content_type = undefined;
                break;
            case "raw":
                form.external.raw_value = true;
                break;
            case "wrapped":
                form.external.raw_value = false;
                break;
        }
    }

    // Placeholder for the content-type input. Reflects what the
    // server will use when the field is left empty so the operator
    // sees the effective default without having to read the docs.
    const externalContentTypePlaceholder = $derived.by(() => {
        if (externalRawMode === "raw") return "application/yaml (default)";
        if (externalRawMode === "wrapped") return "application/json (default)";
        return "Inherit from resource";
    });

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
            form.auth.header_name = form.auth.header_name ?? "X-Pika-Token";
        }
    }

    function setEndpointTLSEnabled(enabled: boolean) {
        form.tls = {
            ...(form.tls ?? {}),
            enabled,
            allow_http: enabled ? form.tls?.allow_http : false,
        };
    }

    function setEndpointHTTPAllowed(allowHTTP: boolean) {
        form.tls = {
            ...(form.tls ?? {}),
            enabled: true,
            allow_http: allowHTTP,
        };
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
        if (form.tls && !form.tls.enabled) {
            form.tls.allow_http = false;
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
            onSaved([...(configStore.settings?.public_endpoints || next)]);
        } catch {
            /* configStore shows the toast */
        } finally {
            saving = false;
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

<Modal
    open={true}
    {onClose}
    labelledby="endpoint-form-title"
    panelClass="rounded-lg max-w-4xl w-full max-h-[90vh] overflow-y-auto"
>
    <div
        class="p-4 border-b border-slate-200 dark:border-warm-700 flex items-center justify-between"
    >
        <h3
            id="endpoint-form-title"
            class="text-base font-semibold text-slate-800 dark:text-slate-100"
        >
            {editingId ? "Edit endpoint" : "New endpoint"}
        </h3>
        <button
            type="button"
            onclick={onClose}
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
                    class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
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
                    class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono focus:outline-none focus:ring-2 focus:ring-accent-500"
                />
            </label>
            <label class="text-xs space-y-1 block">
                <span class="text-slate-600 dark:text-slate-300">Listen host</span>
                <input
                    type="text"
                    bind:value={form.listen_host}
                    placeholder="0.0.0.0"
                    class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono focus:outline-none focus:ring-2 focus:ring-accent-500"
                />
            </label>
            <label class="text-xs space-y-1 block">
                <span class="text-slate-600 dark:text-slate-300">Listen port</span>
                <input
                    type="number"
                    bind:value={form.listen_port}
                    min="1"
                    max="65535"
                    class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
                />
            </label>
        </div>
        <label class="inline-flex items-center gap-2 text-xs text-slate-700 dark:text-slate-200">
            <input
                type="checkbox"
                bind:checked={form.enabled}
                class="rounded border-slate-300 dark:border-warm-600"
            />
            Enabled (bind the listener)
        </label>
        <div class="space-y-2 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 p-3">
            <label class="inline-flex items-start gap-2 text-xs text-slate-700 dark:text-slate-200 cursor-pointer">
                <input
                    type="checkbox"
                    checked={form.tls?.enabled === true}
                    onchange={(e) =>
                        setEndpointTLSEnabled(
                            (e.currentTarget as HTMLInputElement)
                                .checked,
                        )}
                    class="mt-0.5 rounded border-slate-300 dark:border-warm-600"
                />
                <span>
                    <span class="block font-medium text-slate-700 dark:text-slate-200">Serve this endpoint over HTTPS</span>
                    <span class="block text-[11px] text-slate-500 dark:text-slate-400 mt-0.5">Uses the managed certificate from Settings → Certificates. New endpoints default to HTTPS.</span>
                </span>
            </label>
            {#if form.tls?.enabled}
                <label class="inline-flex items-start gap-2 text-xs text-slate-700 dark:text-slate-200 cursor-pointer">
                    <input
                        type="checkbox"
                        checked={form.tls?.allow_http === true}
                        onchange={(e) =>
                            setEndpointHTTPAllowed(
                                (e.currentTarget as HTMLInputElement)
                                    .checked,
                            )}
                        class="mt-0.5 rounded border-slate-300 dark:border-warm-600"
                    />
                    <span>
                        <span class="block font-medium text-slate-700 dark:text-slate-200">Also allow plaintext HTTP on this port</span>
                        <span class="block text-[11px] text-amber-700 dark:text-amber-300 mt-0.5">Only use this behind a trusted proxy or private network.</span>
                    </span>
                </label>
            {:else}
                <p class="text-[11px] text-amber-700 dark:text-amber-300 inline-flex items-center gap-1">
                    <AlertTriangle size={12} /> This endpoint will serve plaintext HTTP.
                </p>
            {/if}
        </div>
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
                    : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-warm-700'}"
            >
                None
            </button>
            <button
                type="button"
                onclick={() => setAuthMode("bearer_token")}
                class="px-3 py-1.5 text-xs rounded border cursor-pointer
                    {form.auth.mode === 'bearer_token'
                    ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                    : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-warm-700'}"
            >
                Pika API token
            </button>
            <button
                type="button"
                onclick={() => setAuthMode("static_token")}
                class="px-3 py-1.5 text-xs rounded border cursor-pointer
                    {form.auth.mode === 'static_token'
                    ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                    : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-warm-700'}"
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
                        class="w-full px-2 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono focus:outline-none focus:ring-2 focus:ring-accent-500"
                    ></textarea>
                </label>
                <label class="text-xs space-y-1 block">
                    <span class="text-slate-600 dark:text-slate-300">
                        Header name
                    </span>
                    <input
                        type="text"
                        bind:value={form.auth.header_name}
                        placeholder="X-Pika-Token"
                        class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono focus:outline-none focus:ring-2 focus:ring-accent-500"
                    />
                </label>
                <p class="text-[11px] text-slate-500 dark:text-slate-400 col-span-2">
                    Send the raw token value in this header; do not
                    prefix it with <code class="font-mono">Bearer</code>.
                    Tokens are sealed at rest. Leaving this list empty
                    when editing keeps the previously stored tokens;
                    an explicit empty list clears them.
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
                    {#each form.request_check.rules as _rule, idx}
                        <RuleEditor
                            bind:rule={form.request_check.rules[idx]}
                            index={idx}
                            count={form.request_check.rules.length}
                            onMove={(dir) => moveRule(idx, dir)}
                            onRemove={() => removeRequestRule(idx)}
                            onReplace={(next) => replaceRule(idx, next)}
                        />
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

            <RuleTester
                requestCheck={form.request_check}
                bind:result={ruleTestResult}
            />
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
                    : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-warm-700'}"
            >
                Config data (no template)
            </button>
            <button
                type="button"
                onclick={() => setMode("consul")}
                class="px-3 py-1.5 text-xs rounded border cursor-pointer
                    {form.mode === 'consul'
                    ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                    : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-warm-700'}"
            >
                Consul KV (read-only)
            </button>
            <button
                type="button"
                onclick={() => setMode("external")}
                class="px-3 py-1.5 text-xs rounded border cursor-pointer
                    {form.mode === 'external'
                    ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                    : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-warm-700'}"
            >
                External resource
            </button>
            <button
                type="button"
                onclick={() => setMode("custom")}
                class="px-3 py-1.5 text-xs rounded border cursor-pointer
                    {form.mode === 'custom'
                    ? 'bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300'
                    : 'bg-white dark:bg-warm-900 border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-warm-700'}"
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
                        class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
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

                <!-- Per-endpoint response-shape override. Inherit
                     (default) keeps current behaviour: the
                     resource's own raw_value / content_type
                     settings flow through unchanged. Picking
                     "raw" or "wrapped" lets one resource serve
                     different shapes across multiple endpoints
                     — useful for wrapper backends like GCP
                     Secret Manager that store YAML/plaintext
                     payloads. -->
                <details class="mt-2 group">
                    <summary
                        class="cursor-pointer text-xs font-medium text-slate-600 dark:text-slate-300 py-1 select-none"
                    >
                        Response shape override (optional)
                    </summary>
                    <div
                        class="mt-2 pl-3 border-l-2 border-slate-200 dark:border-warm-700 space-y-2"
                    >
                        <p
                            class="text-[11px] text-slate-500 dark:text-slate-400"
                        >
                            Overrides the resource's <code>raw_value</code>
                            / <code>content_type</code> for this endpoint
                            only. Most useful when the underlying
                            backend wraps non-JSON payloads as
                            <code>{`{"value": "..."}`}</code> (GCP
                            Secret Manager, AWS Secrets Manager,
                            Consul KV, etcd, plain HTTP).
                        </p>
                        <label
                            class="text-xs space-y-1 block"
                        >
                            <span
                                class="text-slate-600 dark:text-slate-300"
                                >Response mode</span
                            >
                            <select
                                value={externalRawMode}
                                onchange={(e) =>
                                    setExternalRawMode(
                                        (
                                            e.currentTarget as HTMLSelectElement
                                        )
                                            .value as ExternalRawMode,
                                    )}
                                class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
                            >
                                <option value="inherit"
                                    >Inherit from resource
                                    (default)</option
                                >
                                <option value="raw"
                                    >Force raw bytes (no wrapper)</option
                                >
                                <option value="wrapped"
                                    >Force {`{"value": "..."}`} JSON
                                    wrap</option
                                >
                            </select>
                        </label>
                        <label
                            class="text-xs space-y-1 block"
                        >
                            <span
                                class="text-slate-600 dark:text-slate-300"
                                >Content-Type (optional)</span
                            >
                            <input
                                type="text"
                                value={form.external.content_type ??
                                    ""}
                                oninput={(e) => {
                                    if (!form.external) return;
                                    const v = (
                                        e.currentTarget as HTMLInputElement
                                    ).value;
                                    form.external.content_type =
                                        v === "" ? undefined : v;
                                }}
                                placeholder={externalContentTypePlaceholder}
                                class="w-full px-2 py-1.5 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
                            />
                        </label>
                    </div>
                </details>
            </div>
        {:else if form.mode === "custom" && form.custom}
            <div class="space-y-2">
                <label class="text-xs space-y-1 block">
                    <span class="text-slate-600 dark:text-slate-300">Body template (Go text/template)</span>
                    <textarea
                        bind:value={form.custom.body_template}
                        rows="10"
                        class="w-full px-2 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono focus:outline-none focus:ring-2 focus:ring-accent-500"
                    ></textarea>
                </label>
                <div class="grid grid-cols-3 gap-3">
                    <label class="text-xs space-y-1 block">
                        <span class="text-slate-600 dark:text-slate-300">Content-Type</span>
                        <input
                            type="text"
                            bind:value={form.custom.content_type}
                            placeholder="application/json"
                            class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono focus:outline-none focus:ring-2 focus:ring-accent-500"
                        />
                    </label>
                    <label class="text-xs space-y-1 block">
                        <span class="text-slate-600 dark:text-slate-300">Status on missing</span>
                        <input
                            type="number"
                            bind:value={form.custom.status_on_missing}
                            min="100"
                            max="599"
                            class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
                        />
                    </label>
                    <label class="inline-flex items-center gap-2 text-xs text-slate-700 dark:text-slate-200 mt-5">
                        <input
                            type="checkbox"
                            bind:checked={form.custom.allow_format_override}
                            class="rounded border-slate-300 dark:border-warm-600"
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
            onclick={onClose}
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
</Modal>
