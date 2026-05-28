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
        RequestRuleActionTrace,
        RequestRuleTestResult,
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

    // Draft request-rule tester inside the edit modal. This sends the
    // unsaved rule list to the backend evaluator and returns a trace.
    let ruleTestMethod = $state("GET");
    let ruleTestPath = $state("/legacy/myapp/config");
    let ruleTestHeaders = $state<{ name: string; value: string }[]>([]);
    let ruleTesting = $state(false);
    let ruleTestResult = $state<RequestRuleTestResult | null>(null);

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
    // it on by adding at least one rule; toggling the parent off
    // drops the whole field so the backend serializes it cleanly.

    type RuleDraft = import('@/lib/types/config').RequestRule;

    function defaultRequestRule(): RuleDraft {
        return {
            name: "",
            enabled: true,
            when: {},
            then: { type: "allow" },
            actions: [{ type: "allow" }],
        };
    }

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

    function moveRule(idx: number, dir: -1 | 1) {
        const rules = form.request_check?.rules;
        if (!rules) return;
        const j = idx + dir;
        if (j < 0 || j >= rules.length) return;
        const next = [...rules];
        [next[idx], next[j]] = [next[j], next[idx]];
        setRequestRules(next);
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

    type ActionDraft = import('@/lib/types/config').RequestAction;
    type CaptureTransformDraft = import('@/lib/types/config').CaptureTransform;

    function effectiveRuleActions(rule: RuleDraft): ActionDraft[] {
        if (rule.actions && rule.actions.length > 0) return rule.actions;
        if (rule.then?.type) return [rule.then];
        return [{ type: "allow" } satisfies ActionDraft];
    }

    function updateRule(rule: RuleDraft, nextRule: RuleDraft) {
        const rules = form.request_check?.rules;
        if (!rules) return;
        const idx = rules.indexOf(rule);
        if (idx < 0) return;
        const nextRules = [...rules];
        nextRules[idx] = nextRule;
        setRequestRules(nextRules);
    }

    function setRuleActions(rule: RuleDraft, actions: ActionDraft[]) {
        const nextActions = actions.length
            ? actions
            : [{ type: "allow" } satisfies ActionDraft];
        updateRule(rule, {
            ...rule,
            then: nextActions[0],
            actions: nextActions,
        });
    }

    function addRuleAction(rule: RuleDraft) {
        setRuleActions(rule, [
            ...effectiveRuleActions(rule),
            { type: "set_query", name: "variant", value: "prod" },
        ]);
    }

    function removeRuleAction(rule: RuleDraft, idx: number) {
        setRuleActions(
            rule,
            effectiveRuleActions(rule).filter((_, i) => i !== idx),
        );
    }

    function ruleHasAction(rule: RuleDraft, type: import('@/lib/types/config').RequestActionType): boolean {
        return effectiveRuleActions(rule).some((a) => a.type === type);
    }

    function setActionType(
        rule: RuleDraft,
        actionIdx: number,
        t: import('@/lib/types/config').RequestActionType,
    ) {
        const actions = effectiveRuleActions(rule);
        const action = actions[actionIdx] ?? { type: "allow" };
        // Recreate the action so stale fields (e.g. a status from
        // a previous "block" choice) don't sneak through when the
        // type changes.
        let next: ActionDraft;
        switch (t) {
            case "allow":
                next = { type: "allow" };
                break;
            case "block":
                next = {
                    type: "block",
                    status: action.status ?? 403,
                    body: action.body ?? "",
                    content_type:
                        action.content_type ?? "application/json",
                };
                break;
            case "set_header":
                next = {
                    type: "set_header",
                    name: action.name ?? "X-Tenant",
                    value: action.value ?? "",
                };
                break;
            case "del_header":
                next = {
                    type: "del_header",
                    name: action.name ?? "Cookie",
                };
                break;
            case "set_query":
                next = {
                    type: "set_query",
                    name: action.name ?? "variant",
                    value: action.value ?? "prod",
                };
                break;
            case "del_query":
                next = {
                    type: "del_query",
                    name: action.name ?? "debug",
                };
                break;
            case "set_path":
                next = {
                    type: "set_path",
                    value: action.value ?? "/",
                };
                break;
            case "replace_path":
                next = {
                    type: "replace_path",
                    pattern: action.pattern ?? "^/legacy/(.*)$",
                    value: action.value ?? "/$1",
                    capture_transforms: action.capture_transforms,
                };
                break;
        }

        setRuleActions(
            rule,
            actions.map((a, i) => (i === actionIdx ? next : a)),
        );
    }

    function setActionCaptureTransforms(
        rule: RuleDraft,
        actionIdx: number,
        transforms: CaptureTransformDraft[],
    ) {
        const actions = effectiveRuleActions(rule);
        const action = actions[actionIdx];
        if (!action || action.type !== "replace_path") return;
        const nextAction: ActionDraft = {
            ...action,
            capture_transforms: transforms.length ? transforms : undefined,
        };
        setRuleActions(
            rule,
            actions.map((a, i) => (i === actionIdx ? nextAction : a)),
        );
    }

    function addCaptureTransform(rule: RuleDraft, actionIdx: number) {
        const action = effectiveRuleActions(rule)[actionIdx];
        if (!action || action.type !== "replace_path") return;
        setActionCaptureTransforms(rule, actionIdx, [
            ...(action.capture_transforms ?? []),
            { capture: "1", find: "/", value: "-" },
        ]);
    }

    function updateCaptureTransform(
        rule: RuleDraft,
        actionIdx: number,
        transformIdx: number,
        field: keyof CaptureTransformDraft,
        value: string,
    ) {
        const action = effectiveRuleActions(rule)[actionIdx];
        if (!action || action.type !== "replace_path") return;
        const transforms = (action.capture_transforms ?? []).map((tr, i) =>
            i === transformIdx ? { ...tr, [field]: value } : tr,
        );
        setActionCaptureTransforms(rule, actionIdx, transforms);
    }

    function removeCaptureTransform(
        rule: RuleDraft,
        actionIdx: number,
        transformIdx: number,
    ) {
        const action = effectiveRuleActions(rule)[actionIdx];
        if (!action || action.type !== "replace_path") return;
        setActionCaptureTransforms(
            rule,
            actionIdx,
            (action.capture_transforms ?? []).filter((_, i) => i !== transformIdx),
        );
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

        const actions = effectiveRuleActions(rule);
        const summaries = actions.map(actionSummary);
        return `when ${when} → ${summaries.join(" → ")}`;
    }

    function actionSummary(t: ActionDraft): string {
        let then: string = t.type;
        if (t.type === "block") then = `block ${t.status ?? 403}`;
        else if (t.type === "set_header") then = `set ${t.name}=${t.value}`;
        else if (t.type === "del_header") then = `del ${t.name}`;
        else if (t.type === "set_query") then = `set ?${t.name}=${t.value}`;
        else if (t.type === "del_query") then = `del ?${t.name}`;
        else if (t.type === "set_path") then = `path → ${t.value}`;
        else if (t.type === "replace_path") {
            then = `path s/${t.pattern}/${t.value}/`;
            if (t.capture_transforms?.length) {
                then += ` + ${t.capture_transforms.length} capture transform${t.capture_transforms.length === 1 ? "" : "s"}`;
            }
        }
        return then;
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
        resetRuleTest();
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
        form.tls = form.tls ?? { enabled: false };
        editingId = ep.id;
        resetRuleTest();
        formOpen = true;
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
                "Delete this endpoint? The listener will stop immediately.",
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

    function resetRuleTest() {
        ruleTestMethod = "GET";
        ruleTestPath = "/legacy/myapp/config";
        ruleTestHeaders = [];
        ruleTestResult = null;
    }

    function addRuleTestHeader() {
        ruleTestHeaders = [...ruleTestHeaders, { name: "", value: "" }];
    }

    function setRuleTestHeader(
        idx: number,
        field: "name" | "value",
        value: string,
    ) {
        ruleTestHeaders = ruleTestHeaders.map((h, i) =>
            i === idx ? { ...h, [field]: value } : h,
        );
    }

    function removeRuleTestHeader(idx: number) {
        ruleTestHeaders = ruleTestHeaders.filter((_, i) => i !== idx);
    }

    async function runRuleTest() {
        ruleTesting = true;
        ruleTestResult = null;
        try {
            const headers: Record<string, string> = {};
            for (const h of ruleTestHeaders) {
                const name = h.name.trim();
                if (name) headers[name] = h.value;
            }
            ruleTestResult = await configStore.testPublicEndpointRules({
                request_check: form.request_check ?? { rules: [] },
                method: ruleTestMethod,
                path: ruleTestPath,
                headers: Object.keys(headers).length ? headers : undefined,
            });
        } catch (err: any) {
            const msg =
                err?.response?.data?.message ||
                err?.response?.data?.error ||
                err?.message ||
                "Rule test failed";
            addToast(msg, "alert");
        } finally {
            ruleTesting = false;
        }
    }

    function pathWithQuery(path?: string, rawQuery?: string): string {
        const p = path || "/";
        return rawQuery ? `${p}?${rawQuery}` : p;
    }

    function terminalLabel(result: RequestRuleTestResult): string {
        if (result.terminal === "block") {
            return `blocked with HTTP ${result.block?.status ?? 403}`;
        }
        if (result.terminal === "allow") {
            return "allowed by terminal rule";
        }
        return "allowed by default";
    }

    function actionTraceSummary(action: RequestRuleActionTrace): string {
        if (action.type === "allow") return "forwarded to the shim";
        if (action.type === "block") {
            return `blocked with HTTP ${action.block?.status ?? 403}`;
        }
        if (action.type === "set_header" || action.type === "del_header") {
            return `${action.header_name}: ${action.header_before || "∅"} → ${action.header_after || "∅"}`;
        }
        if (action.type === "set_query" || action.type === "del_query") {
            return `${action.query_name}: ${action.query_before || "∅"} → ${action.query_after || "∅"}`;
        }
        return `${pathWithQuery(action.before_path, action.before_query)} → ${pathWithQuery(action.after_path, action.after_query)}`;
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

    function endpointScheme(ep: PublicEndpoint): string {
        return ep.tls?.enabled ? "https" : "http";
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
                                {#if ep.tls?.enabled}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300"
                                        >https</span
                                    >
                                    {#if ep.tls.allow_http}
                                        <span
                                            class="text-[10px] px-1.5 py-0.5 rounded bg-amber-100 dark:bg-amber-900/40 text-amber-800 dark:text-amber-300"
                                            >http allowed</span
                                        >
                                    {/if}
                                {:else}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-amber-100 dark:bg-amber-900/40 text-amber-800 dark:text-amber-300"
                                        >http</span
                                    >
                                {/if}
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
                                {endpointScheme(ep)}://{ep.listen_host || "0.0.0.0"}:{ep.listen_port}{ep.base_path ===
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
                                class="mt-0.5 rounded"
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
                                    class="mt-0.5 rounded"
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
                                    placeholder="X-Pika-Token"
                                    class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 font-mono"
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

                                    <div class="space-y-2">
                                        {#each effectiveRuleActions(rule) as action, actionIdx}
                                            <div class="grid grid-cols-[auto_1fr_1fr_auto] gap-2 items-center text-xs">
                                                <span class="text-slate-500 dark:text-slate-400 font-mono">
                                                    {actionIdx === 0 ? "then" : "and"}
                                                </span>
                                                <select
                                                    value={action.type}
                                                    onchange={(e) =>
                                                        setActionType(
                                                            rule,
                                                            actionIdx,
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
                                                    {#if action.type === "block"}
                                                        <input type="number" min="100" max="599" bind:value={action.status} placeholder="403" class="w-20 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100" />
                                                        <input type="text" bind:value={action.body} placeholder="response body" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100" />
                                                    {:else if action.type === "set_header" || action.type === "set_query"}
                                                        <input type="text" bind:value={action.name} placeholder={action.type === "set_header" ? "X-Tenant" : "variant"} class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                                        <input type="text" bind:value={action.value} placeholder="value" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                                    {:else if action.type === "del_header" || action.type === "del_query"}
                                                        <input type="text" bind:value={action.name} placeholder={action.type === "del_header" ? "Cookie" : "debug"} class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                                    {:else if action.type === "set_path"}
                                                        <input type="text" bind:value={action.value} placeholder="/new/path" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                                    {:else if action.type === "replace_path"}
                                                        <div class="flex-1 space-y-1.5">
                                                            <div class="flex gap-2">
                                                                <input type="text" bind:value={action.pattern} placeholder="^/legacy/(.*)$" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                                                <input type="text" bind:value={action.value} placeholder="/$1" class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono" />
                                                            </div>
                                                            <div class="space-y-1 rounded border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 p-1.5">
                                                                <div class="flex items-center justify-between gap-2">
                                                                    <span class="text-[10px] uppercase tracking-wider text-slate-500 dark:text-slate-400">
                                                                        Capture transforms
                                                                    </span>
                                                                    <button
                                                                        type="button"
                                                                        onclick={() => addCaptureTransform(rule, actionIdx)}
                                                                        class="px-1.5 py-0.5 text-[10px] rounded bg-slate-200 dark:bg-warm-700 hover:bg-slate-300 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer inline-flex items-center gap-1"
                                                                    >
                                                                        <Plus size={10} /> add
                                                                    </button>
                                                                </div>
                                                                {#if action.capture_transforms?.length}
                                                                    {#each action.capture_transforms as transform, transformIdx}
                                                                        <div class="grid grid-cols-[4rem_1fr_1fr_auto] gap-1 items-center">
                                                                            <input
                                                                                type="text"
                                                                                value={transform.capture}
                                                                                oninput={(e) =>
                                                                                    updateCaptureTransform(
                                                                                        rule,
                                                                                        actionIdx,
                                                                                        transformIdx,
                                                                                        "capture",
                                                                                        (e.target as HTMLInputElement).value,
                                                                                    )}
                                                                                placeholder="1"
                                                                                class="px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono"
                                                                            />
                                                                            <input
                                                                                type="text"
                                                                                value={transform.find}
                                                                                oninput={(e) =>
                                                                                    updateCaptureTransform(
                                                                                        rule,
                                                                                        actionIdx,
                                                                                        transformIdx,
                                                                                        "find",
                                                                                        (e.target as HTMLInputElement).value,
                                                                                    )}
                                                                                placeholder="/"
                                                                                class="px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono"
                                                                            />
                                                                            <input
                                                                                type="text"
                                                                                value={transform.value}
                                                                                oninput={(e) =>
                                                                                    updateCaptureTransform(
                                                                                        rule,
                                                                                        actionIdx,
                                                                                        transformIdx,
                                                                                        "value",
                                                                                        (e.target as HTMLInputElement).value,
                                                                                    )}
                                                                                placeholder="-"
                                                                                class="px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono"
                                                                            />
                                                                            <button
                                                                                type="button"
                                                                                onclick={() => removeCaptureTransform(rule, actionIdx, transformIdx)}
                                                                                class="p-1 rounded hover:bg-red-50 dark:hover:bg-red-900/30 text-red-600 dark:text-red-400 cursor-pointer"
                                                                                title="Remove capture transform"
                                                                            >
                                                                                <Trash2 size={12} />
                                                                            </button>
                                                                        </div>
                                                                    {/each}
                                                                {:else}
                                                                    <p class="text-[10px] text-slate-500 dark:text-slate-400">
                                                                        Optional: pick a capture and replace literal text inside it before expanding the replacement.
                                                                    </p>
                                                                {/if}
                                                            </div>
                                                        </div>
                                                    {/if}
                                                </div>
                                                <button
                                                    type="button"
                                                    onclick={() => removeRuleAction(rule, actionIdx)}
                                                    disabled={effectiveRuleActions(rule).length === 1}
                                                    class="p-1 rounded hover:bg-red-50 dark:hover:bg-red-900/30 text-red-600 dark:text-red-400 cursor-pointer disabled:opacity-30"
                                                    title="Remove action"
                                                >
                                                    <Trash2 size={12} />
                                                </button>
                                            </div>
                                        {/each}
                                        <button
                                            type="button"
                                            onclick={() => addRuleAction(rule)}
                                            class="ml-10 px-2 py-1 text-[11px] rounded bg-slate-200 dark:bg-warm-700 hover:bg-slate-300 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer inline-flex items-center gap-1"
                                        >
                                            <Plus size={10} /> add action
                                        </button>
                                    </div>

                                    <p class="text-[11px] text-slate-500 dark:text-slate-400 font-mono">
                                        {ruleSummary(rule)}
                                    </p>
                                    {#if ruleHasAction(rule, "set_path")}
                                        <div class="text-[11px] text-slate-500 dark:text-slate-400 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded px-2 py-1.5">
                                            <strong class="text-slate-600 dark:text-slate-300">Path rewrite is literal.</strong>
                                            It replaces the whole request path, not a regex capture.
                                            Example: when path starts with
                                            <code class="font-mono">/legacy/app</code>, set path to
                                            <code class="font-mono">/myapp/config</code> so the shim sees
                                            <code class="font-mono">/myapp/config</code>.
                                        </div>
                                    {/if}
                                    {#if ruleHasAction(rule, "replace_path")}
                                        <div class="text-[11px] text-slate-500 dark:text-slate-400 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded px-2 py-1.5">
                                            <strong class="text-slate-600 dark:text-slate-300">Regex path replacement.</strong>
                                            The first field is a Go regexp pattern; the second is the replacement.
                                            Capture groups use
                                            <code class="font-mono">$1</code>,
                                            <code class="font-mono">${"${1}"}</code>, or
                                            <code class="font-mono">$name</code>.
                                            Example:
                                            <code class="font-mono">^/legacy/(.*)$</code>
                                            → <code class="font-mono">/$1</code>
                                            turns <code class="font-mono">/legacy/myapp/config</code>
                                            into <code class="font-mono">/myapp/config</code>.
                                            Capture transforms can edit a capture first: with pattern
                                            <code class="font-mono">^/legacy/(.*)$</code>, replacement
                                            <code class="font-mono">/legacy/${"${1}"}</code>, transform capture
                                            <code class="font-mono">1</code> find <code class="font-mono">/</code>
                                            to <code class="font-mono">-</code>,
                                            <code class="font-mono">/legacy/1/2/3</code>
                                            becomes <code class="font-mono">/legacy/1-2-3</code>.
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

                    <div class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 space-y-3">
                        <div class="flex items-center justify-between gap-3 flex-wrap">
                            <div>
                                <h5 class="text-xs font-semibold text-slate-700 dark:text-slate-200">
                                    Test draft rules
                                </h5>
                                <p class="text-[11px] text-slate-500 dark:text-slate-400">
                                    Runs the unsaved rules through the backend evaluator and shows what the shim would see.
                                </p>
                            </div>
                            <button
                                type="button"
                                onclick={runRuleTest}
                                disabled={ruleTesting}
                                class="px-2 py-1 text-xs rounded bg-accent-600 hover:bg-accent-700 text-white cursor-pointer inline-flex items-center gap-1 disabled:opacity-50"
                            >
                                <Play size={12} />
                                {ruleTesting ? "Testing…" : "Test rules"}
                            </button>
                        </div>

                        <div class="grid grid-cols-[6rem_1fr] gap-2 text-xs">
                            <select
                                bind:value={ruleTestMethod}
                                class="px-2 py-1 rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono"
                            >
                                <option value="GET">GET</option>
                                <option value="POST">POST</option>
                                <option value="PUT">PUT</option>
                                <option value="PATCH">PATCH</option>
                                <option value="DELETE">DELETE</option>
                                <option value="HEAD">HEAD</option>
                            </select>
                            <input
                                type="text"
                                bind:value={ruleTestPath}
                                placeholder="/legacy/myapp/config?variant=prod"
                                class="px-2 py-1 rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono"
                            />
                        </div>

                        <div class="space-y-1.5">
                            <div class="flex items-center gap-2">
                                <span class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400">
                                    Headers
                                </span>
                                <button
                                    type="button"
                                    onclick={addRuleTestHeader}
                                    class="px-1.5 py-0.5 text-[11px] rounded bg-slate-200 dark:bg-warm-700 hover:bg-slate-300 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer inline-flex items-center gap-1"
                                >
                                    <Plus size={10} /> add
                                </button>
                            </div>
                            {#each ruleTestHeaders as h, i}
                                <div class="flex gap-2 items-center">
                                    <input
                                        type="text"
                                        value={h.name}
                                        oninput={(e) =>
                                            setRuleTestHeader(
                                                i,
                                                "name",
                                                (e.target as HTMLInputElement).value,
                                            )}
                                        placeholder="Header"
                                        class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono"
                                    />
                                    <input
                                        type="text"
                                        value={h.value}
                                        oninput={(e) =>
                                            setRuleTestHeader(
                                                i,
                                                "value",
                                                (e.target as HTMLInputElement).value,
                                            )}
                                        placeholder="value"
                                        class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 font-mono"
                                    />
                                    <button
                                        type="button"
                                        onclick={() => removeRuleTestHeader(i)}
                                        class="p-1 rounded hover:bg-red-50 dark:hover:bg-red-900/30 text-red-600 dark:text-red-400 cursor-pointer"
                                        title="Remove header"
                                    >
                                        <Trash2 size={12} />
                                    </button>
                                </div>
                            {/each}
                        </div>

                        {#if ruleTestResult}
                            <div class="text-xs space-y-2 rounded border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-800 p-2">
                                <div class="font-mono text-slate-700 dark:text-slate-200">
                                    {terminalLabel(ruleTestResult)}
                                </div>
                                <div class="grid grid-cols-2 gap-2 text-[11px]">
                                    <div>
                                        <span class="text-slate-500 dark:text-slate-400">Input</span>
                                        <div class="font-mono text-slate-700 dark:text-slate-200">
                                            {ruleTestResult.initial.method}
                                            {pathWithQuery(ruleTestResult.initial.path, ruleTestResult.initial.raw_query)}
                                        </div>
                                    </div>
                                    <div>
                                        <span class="text-slate-500 dark:text-slate-400">Shim sees</span>
                                        <div class="font-mono text-slate-700 dark:text-slate-200">
                                            {ruleTestResult.final.method}
                                            {pathWithQuery(ruleTestResult.final.path, ruleTestResult.final.raw_query)}
                                        </div>
                                    </div>
                                </div>
                                {#if ruleTestResult.matched_rules.length === 0}
                                    <div class="text-[11px] text-slate-500 dark:text-slate-400">
                                        No rules matched; request falls through to the shim.
                                    </div>
                                {:else}
                                    <div class="space-y-1.5">
                                        {#each ruleTestResult.matched_rules as trace}
                                            <div class="text-[11px] border border-slate-200 dark:border-warm-700 rounded p-2 bg-slate-50 dark:bg-warm-900">
                                                <div class="font-medium text-slate-700 dark:text-slate-200">
                                                    Rule #{trace.rule_index + 1}{trace.rule_name ? ` — ${trace.rule_name}` : ""}
                                                </div>
                                                <div class="mt-1 space-y-1 font-mono text-slate-600 dark:text-slate-300">
                                                    {#each trace.actions as action}
                                                        <div>
                                                            {action.action_index + 1}. {action.type}: {actionTraceSummary(action)}
                                                        </div>
                                                    {/each}
                                                </div>
                                            </div>
                                        {/each}
                                    </div>
                                {/if}
                                {#if ruleTestResult.block}
                                    <pre class="text-[11px] p-2 rounded bg-slate-900 text-slate-100 overflow-auto max-h-32">{ruleTestResult.block.body}</pre>
                                {/if}
                            </div>
                        {/if}
                    </div>
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
                                            class="w-full px-2 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100"
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
                                            class="w-full px-2 py-1.5 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100"
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
