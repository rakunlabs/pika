<script lang="ts">
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { onMount } from "svelte";
  import { Plus, Trash2, Eye, EyeOff, Webhook } from "lucide-svelte";
  import type { Hook, HookTarget, KafkaSecurity, KafkaSASLEntry, LogTarget } from "@/lib/types/config";

  // ── Hooks state ──
  let hooks = $state<Hook[]>([]);
  let showAddHook = $state(false);
  let editingHookIndex = $state<number | null>(null);
  let isSavingHooks = $state(false);
  // Hook form fields
  let hookName = $state('');
  let hookEnabled = $state(true);
  let hookEvents = $state<string[]>([]);
  let hookEventInput = $state('');
  let hookFilterMounts = $state<string[]>([]);
  let hookFilterMountInput = $state('');
  let hookFilterPathPattern = $state('');
  let hookTargets = $state<HookTarget[]>([]);
  // Target form (inline within hook form)
  let showAddTarget = $state(false);
  let targetType = $state<'http' | 'kafka' | 'redis' | 'nats' | 'log'>('http');
  let targetHttpUrl = $state('');
  let targetHttpMethod = $state('POST');
  let targetHttpHeaders = $state<{key: string; value: string}[]>([]);
  let targetHttpTimeout = $state('30s');
  let targetKafkaBrokers = $state<string[]>([]);
  let targetKafkaBrokerInput = $state('');
  let targetKafkaTopic = $state('');
  let targetKafkaAutoTopicCreation = $state(true);
  let targetKafkaKeyTemplate = $state('');
  let targetBodyTemplate = $state('');
  let editingTargetIndex = $state<number | null>(null);
  // Kafka security
  let targetKafkaTLSEnabled = $state(false);
  let targetKafkaTLSCertFile = $state('');
  let targetKafkaTLSCertPEM = $state('');
  let targetKafkaTLSKeyFile = $state('');
  let targetKafkaTLSKeyPEM = $state('');
  let targetKafkaTLSCAFile = $state('');
  let targetKafkaTLSCAPEM = $state('');
  let targetKafkaTLSInputMode = $state<'path' | 'pem'>('path');
  let targetKafkaSASLType = $state<'none' | 'plain' | 'scram'>('none');
  let targetKafkaSASLUser = $state('');
  let targetKafkaSASLPass = $state('');
  let targetKafkaSASLAlgorithm = $state<'SCRAM-SHA-256' | 'SCRAM-SHA-512'>('SCRAM-SHA-256');
  let targetKafkaSASLIsToken = $state(false);
  let showKafkaSASLPass = $state(false);
  // Redis fields
  let targetRedisCluster = $state(false);
  let targetRedisAddress = $state('');
  let targetRedisAddresses = $state<string[]>([]);
  let targetRedisAddressInput = $state('');
  let targetRedisPassword = $state('');
  let targetRedisDB = $state(0);
  let targetRedisChannel = $state('');
  let targetRedisTLSEnabled = $state(false);
  let targetRedisTLSCertFile = $state('');
  let targetRedisTLSKeyFile = $state('');
  let targetRedisTLSCAFile = $state('');
  // NATS fields
  let targetNatsUrl = $state('');
  let targetNatsSubject = $state('');
  let targetNatsToken = $state('');
  let targetNatsUsername = $state('');
  let targetNatsPassword = $state('');
  // Log (local slog) fields
  let targetLogLevel = $state<'debug' | 'info' | 'warn' | 'error'>('info');
  let targetLogMessage = $state('');
  let targetLogFields = $state<{key: string; value: string}[]>([]);

  // Known event types for quick-add
  const HOOK_EVENT_TYPES = [
    'file.created', 'file.updated', 'file.deleted',
    'dir.created', 'file.renamed', 'file.copied',
    'config.created', 'config.deleted', 'config.updated',
    '*'
  ];

  // Initialize hooks from configStore on mount
  onMount(() => {
    hooks = [...(configStore.settings?.hooks || [])];
  });

  // ── Hook handlers ──
  function resetHookForm() {
    hookName = '';
    hookEnabled = true;
    hookEvents = [];
    hookEventInput = '';
    hookFilterMounts = [];
    hookFilterMountInput = '';
    hookFilterPathPattern = '';
    hookTargets = [];
    editingHookIndex = null;
    showAddTarget = false;
    resetTargetForm();
  }

  function resetTargetForm() {
    targetType = 'http';
    targetHttpUrl = '';
    targetHttpMethod = 'POST';
    targetHttpHeaders = [];
    targetHttpTimeout = '30s';
    targetKafkaBrokers = [];
    targetKafkaBrokerInput = '';
    targetKafkaTopic = '';
    targetKafkaAutoTopicCreation = true;
    targetKafkaKeyTemplate = '';
    targetBodyTemplate = '';
    editingTargetIndex = null;
    // Kafka security
    targetKafkaTLSEnabled = false;
    targetKafkaTLSCertFile = '';
    targetKafkaTLSCertPEM = '';
    targetKafkaTLSKeyFile = '';
    targetKafkaTLSKeyPEM = '';
    targetKafkaTLSCAFile = '';
    targetKafkaTLSCAPEM = '';
    targetKafkaTLSInputMode = 'path';
    targetKafkaSASLType = 'none';
    targetKafkaSASLUser = '';
    targetKafkaSASLPass = '';
    targetKafkaSASLAlgorithm = 'SCRAM-SHA-256';
    targetKafkaSASLIsToken = false;
    showKafkaSASLPass = false;
    // Redis
    targetRedisCluster = false;
    targetRedisAddress = '';
    targetRedisAddresses = [];
    targetRedisAddressInput = '';
    targetRedisPassword = '';
    targetRedisDB = 0;
    targetRedisChannel = '';
    targetRedisTLSEnabled = false;
    targetRedisTLSCertFile = '';
    targetRedisTLSKeyFile = '';
    targetRedisTLSCAFile = '';
    // NATS
    targetNatsUrl = '';
    targetNatsSubject = '';
    targetNatsToken = '';
    targetNatsUsername = '';
    targetNatsPassword = '';
    // Log
    targetLogLevel = 'info';
    targetLogMessage = '';
    targetLogFields = [];
  }

  function addTargetLogField() {
    targetLogFields = [...targetLogFields, { key: '', value: '' }];
  }

  function removeTargetLogField(index: number) {
    targetLogFields = targetLogFields.filter((_, i) => i !== index);
  }

  function handleEditHook(index: number) {
    const h = hooks[index];
    hookName = h.name;
    hookEnabled = h.enabled;
    hookEvents = [...h.events];
    hookFilterMounts = [...(h.filter?.mounts || [])];
    hookFilterMountInput = '';
    hookFilterPathPattern = h.filter?.path_pattern || '';
    hookTargets = h.targets.map((t: HookTarget) => ({...t}));
    editingHookIndex = index;
    showAddHook = true;
    showAddTarget = false;
    resetTargetForm();
  }

  function addHookEvent() {
    const e = hookEventInput.trim();
    if (!e) return;
    if (hookEvents.includes(e)) { addToast('Event already added', 'alert'); return; }
    hookEvents = [...hookEvents, e];
    hookEventInput = '';
  }

  function removeHookEvent(index: number) {
    hookEvents = hookEvents.filter((_, i) => i !== index);
  }

  function addHookFilterMount() {
    const m = hookFilterMountInput.trim();
    if (!m) return;
    if (hookFilterMounts.includes(m)) { addToast('Mount already added', 'alert'); return; }
    hookFilterMounts = [...hookFilterMounts, m];
    hookFilterMountInput = '';
  }

  function removeHookFilterMount(index: number) {
    hookFilterMounts = hookFilterMounts.filter((_, i) => i !== index);
  }

  function addTargetKafkaBroker() {
    const b = targetKafkaBrokerInput.trim();
    if (!b) return;
    if (targetKafkaBrokers.includes(b)) { addToast('Broker already added', 'alert'); return; }
    targetKafkaBrokers = [...targetKafkaBrokers, b];
    targetKafkaBrokerInput = '';
  }

  function removeTargetKafkaBroker(index: number) {
    targetKafkaBrokers = targetKafkaBrokers.filter((_, i) => i !== index);
  }

  function addTargetHeader() {
    targetHttpHeaders = [...targetHttpHeaders, { key: '', value: '' }];
  }

  function removeTargetHeader(index: number) {
    targetHttpHeaders = targetHttpHeaders.filter((_, i) => i !== index);
  }

  function handleAddTarget() {
    let target: HookTarget;
    if (targetType === 'http') {
      if (!targetHttpUrl.trim()) { addToast('URL is required', 'alert'); return; }
      target = {
        type: 'http',
        http: {
          url: targetHttpUrl.trim(),
          method: targetHttpMethod || 'POST',
          headers: targetHttpHeaders.length > 0 ? Object.fromEntries(targetHttpHeaders.filter(h => h.key.trim()).map(h => [h.key.trim(), h.value])) : undefined,
          timeout: targetHttpTimeout || undefined,
        },
        body_template: targetBodyTemplate || undefined,
      };
    } else if (targetType === 'kafka') {
      if (targetKafkaBrokers.length === 0) { addToast('At least one Kafka broker is required', 'alert'); return; }
      if (!targetKafkaTopic.trim()) { addToast('Kafka topic is required', 'alert'); return; }

      // Build security config
      let security: KafkaSecurity | undefined;
      const hasTLS = targetKafkaTLSEnabled;
      const hasSASL = targetKafkaSASLType !== 'none';
      if (hasTLS || hasSASL) {
        security = {};
        if (hasTLS) {
          security.tls = {
            enabled: true,
            cert_file: targetKafkaTLSCertFile || undefined,
            cert_pem: targetKafkaTLSCertPEM || undefined,
            key_file: targetKafkaTLSKeyFile || undefined,
            key_pem: targetKafkaTLSKeyPEM || undefined,
            ca_file: targetKafkaTLSCAFile || undefined,
            ca_pem: targetKafkaTLSCAPEM || undefined,
          };
        }
        if (hasSASL) {
          const entry: KafkaSASLEntry = {};
          if (targetKafkaSASLType === 'plain') {
            entry.plain = { enabled: true, user: targetKafkaSASLUser, pass: targetKafkaSASLPass };
          } else if (targetKafkaSASLType === 'scram') {
            entry.scram = {
              enabled: true,
              algorithm: targetKafkaSASLAlgorithm,
              user: targetKafkaSASLUser,
              pass: targetKafkaSASLPass,
              is_token: targetKafkaSASLIsToken || undefined,
            };
          }
          security.sasl = [entry];
        }
      }

      target = {
        type: 'kafka',
        kafka: {
          brokers: [...targetKafkaBrokers],
          topic: targetKafkaTopic.trim(),
          key_template: targetKafkaKeyTemplate || undefined,
          auto_topic_creation: targetKafkaAutoTopicCreation,
          security: security,
        },
        body_template: targetBodyTemplate || undefined,
      };
    } else if (targetType === 'redis') {
      if (!targetRedisChannel.trim()) { addToast('Redis channel is required', 'alert'); return; }
      if (targetRedisCluster) {
        if (targetRedisAddresses.length === 0) { addToast('At least one cluster address is required', 'alert'); return; }
      } else {
        if (!targetRedisAddress.trim()) { addToast('Redis address is required', 'alert'); return; }
      }
      const redisTls: import('@/lib/types/config').RedisTLS | undefined = targetRedisTLSEnabled ? {
        enabled: true,
        cert_file: targetRedisTLSCertFile || undefined,
        key_file: targetRedisTLSKeyFile || undefined,
        ca_file: targetRedisTLSCAFile || undefined,
      } : undefined;
      target = {
        type: 'redis',
        redis: {
          address: targetRedisCluster ? undefined : targetRedisAddress.trim(),
          addresses: targetRedisCluster ? [...targetRedisAddresses] : undefined,
          password: targetRedisPassword || undefined,
          db: targetRedisCluster ? undefined : (targetRedisDB || undefined),
          channel: targetRedisChannel.trim(),
          tls: redisTls,
        },
        body_template: targetBodyTemplate || undefined,
      };
    } else if (targetType === 'nats') {
      if (!targetNatsUrl.trim()) { addToast('NATS URL is required', 'alert'); return; }
      if (!targetNatsSubject.trim()) { addToast('NATS subject is required', 'alert'); return; }
      target = {
        type: 'nats',
        nats: {
          url: targetNatsUrl.trim(),
          subject: targetNatsSubject.trim(),
          token: targetNatsToken || undefined,
          username: targetNatsUsername || undefined,
          password: targetNatsPassword || undefined,
        },
        body_template: targetBodyTemplate || undefined,
      };
    } else if (targetType === 'log') {
      const fieldsEntries = targetLogFields.filter(f => f.key.trim()).map(f => [f.key.trim(), f.value] as const);
      const log: LogTarget = {
        level: targetLogLevel,
        message: targetLogMessage || undefined,
        fields: fieldsEntries.length > 0 ? Object.fromEntries(fieldsEntries) : undefined,
      };
      // Body Template is intentionally omitted: the log sink renders its own
      // message and fields from the Event and ignores any rendered payload.
      target = {
        type: 'log',
        log,
      };
    } else {
      return;
    }

    if (editingTargetIndex !== null) {
      hookTargets = hookTargets.map((t, i) => i === editingTargetIndex ? target : t);
    } else {
      hookTargets = [...hookTargets, target];
    }
    showAddTarget = false;
    resetTargetForm();
  }

  function handleEditTarget(index: number) {
    const t = hookTargets[index];
    targetType = t.type as 'http' | 'kafka' | 'redis' | 'nats' | 'log';
    if (t.http) {
      targetHttpUrl = t.http.url;
      targetHttpMethod = t.http.method || 'POST';
      targetHttpHeaders = t.http.headers ? Object.entries(t.http.headers).map(([key, value]) => ({ key, value: String(value) })) : [];
      targetHttpTimeout = t.http.timeout || '30s';
    }
    if (t.kafka) {
      targetKafkaBrokers = [...t.kafka.brokers];
      targetKafkaTopic = t.kafka.topic;
      targetKafkaAutoTopicCreation = t.kafka.auto_topic_creation ?? true;
      targetKafkaKeyTemplate = t.kafka.key_template || '';
      // Security
      const sec = t.kafka.security;
      targetKafkaTLSEnabled = sec?.tls?.enabled ?? false;
      targetKafkaTLSCertFile = sec?.tls?.cert_file ?? '';
      targetKafkaTLSCertPEM = sec?.tls?.cert_pem ?? '';
      targetKafkaTLSKeyFile = sec?.tls?.key_file ?? '';
      targetKafkaTLSKeyPEM = sec?.tls?.key_pem ?? '';
      targetKafkaTLSCAFile = sec?.tls?.ca_file ?? '';
      targetKafkaTLSCAPEM = sec?.tls?.ca_pem ?? '';
      // Determine input mode from which fields are populated
      targetKafkaTLSInputMode = (sec?.tls?.ca_pem || sec?.tls?.cert_pem || sec?.tls?.key_pem) ? 'pem' : 'path';
      if (sec?.sasl?.length) {
        const s = sec.sasl[0];
        if (s.plain?.enabled) {
          targetKafkaSASLType = 'plain';
          targetKafkaSASLUser = s.plain.user ?? '';
          targetKafkaSASLPass = s.plain.pass ?? '';
        } else if (s.scram?.enabled) {
          targetKafkaSASLType = 'scram';
          targetKafkaSASLUser = s.scram.user ?? '';
          targetKafkaSASLPass = s.scram.pass ?? '';
          targetKafkaSASLAlgorithm = (s.scram.algorithm as 'SCRAM-SHA-256' | 'SCRAM-SHA-512') ?? 'SCRAM-SHA-256';
          targetKafkaSASLIsToken = s.scram.is_token ?? false;
        } else {
          targetKafkaSASLType = 'none';
        }
      } else {
        targetKafkaSASLType = 'none';
      }
    }
    if (t.redis) {
      targetRedisCluster = (t.redis.addresses?.length ?? 0) > 0;
      targetRedisAddress = t.redis.address || '';
      targetRedisAddresses = [...(t.redis.addresses || [])];
      targetRedisPassword = t.redis.password || '';
      targetRedisDB = t.redis.db || 0;
      targetRedisChannel = t.redis.channel;
      targetRedisTLSEnabled = t.redis.tls?.enabled || false;
      targetRedisTLSCertFile = t.redis.tls?.cert_file || '';
      targetRedisTLSKeyFile = t.redis.tls?.key_file || '';
      targetRedisTLSCAFile = t.redis.tls?.ca_file || '';
    }
    if (t.nats) {
      targetNatsUrl = t.nats.url;
      targetNatsSubject = t.nats.subject;
      targetNatsToken = t.nats.token || '';
      targetNatsUsername = t.nats.username || '';
      targetNatsPassword = t.nats.password || '';
    }
    if (t.log) {
      targetLogLevel = (t.log.level as 'debug' | 'info' | 'warn' | 'error') || 'info';
      targetLogMessage = t.log.message || '';
      targetLogFields = t.log.fields
        ? Object.entries(t.log.fields).map(([key, value]) => ({ key, value: String(value) }))
        : [];
    }
    targetBodyTemplate = t.body_template || '';
    editingTargetIndex = index;
    showAddTarget = true;
    showKafkaSASLPass = false;
  }

  function removeTarget(index: number) {
    hookTargets = hookTargets.filter((_, i) => i !== index);
  }

  async function handleAddHook() {
    const name = hookName.trim();
    if (!name) { addToast('Hook name is required', 'alert'); return; }
    if (hookEvents.length === 0) { addToast('At least one event type is required', 'alert'); return; }
    if (hookTargets.length === 0) { addToast('At least one target is required', 'alert'); return; }
    if (hooks.some((h, i) => h.name === name && i !== editingHookIndex)) {
      addToast(`A hook named "${name}" already exists`, 'alert');
      return;
    }

    const entry: Hook = {
      name,
      enabled: hookEnabled,
      events: [...hookEvents],
      filter: (hookFilterMounts.length > 0 || hookFilterPathPattern) ? {
        mounts: hookFilterMounts.length > 0 ? [...hookFilterMounts] : undefined,
        path_pattern: hookFilterPathPattern || undefined,
      } : undefined,
      targets: hookTargets.map(t => ({...t})),
    };

    let updated: Hook[];
    if (editingHookIndex !== null) {
      updated = hooks.map((h, i) => i === editingHookIndex ? entry : h);
    } else {
      updated = [...hooks, entry];
    }

    isSavingHooks = true;
    try {
      await configStore.saveHooks(updated);
      hooks = updated;
      showAddHook = false;
      resetHookForm();
    } catch {
      // toast already shown
    } finally {
      isSavingHooks = false;
    }
  }

  async function handleRemoveHook(index: number) {
    const h = hooks[index];
    if (!confirm(`Remove hook "${h.name}"?`)) return;

    const updated = hooks.filter((_, i) => i !== index);
    isSavingHooks = true;
    try {
      await configStore.saveHooks(updated);
      hooks = updated;
    } catch {
      // toast already shown
    } finally {
      isSavingHooks = false;
    }
  }

  async function handleToggleHook(index: number) {
    const updated = hooks.map((h, i) => i === index ? { ...h, enabled: !h.enabled } : h);
    isSavingHooks = true;
    try {
      await configStore.saveHooks(updated);
      hooks = updated;
    } catch {
      // toast already shown
    } finally {
      isSavingHooks = false;
    }
  }
</script>

<div>
  <div class="flex items-center justify-between mb-4">
    <div>
      <h2 class="text-lg font-semibold text-slate-800">Hooks</h2>
      <p class="text-sm text-slate-500 mt-0.5">Trigger HTTP webhooks or Kafka messages when files are created, updated, or deleted.</p>
    </div>
    <button
      class="flex items-center gap-1.5 px-3 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 transition-colors"
      onclick={() => { showAddHook = true; resetHookForm(); }}
    >
      <Plus size={14} />
      Add Hook
    </button>
  </div>

  <!-- Add/Edit Hook Form -->
  {#if showAddHook}
    <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
      <h3 class="text-sm font-semibold text-slate-700 mb-4">{editingHookIndex !== null ? 'Edit Hook' : 'Add Hook'}</h3>

      <!-- Hook Name -->
      <div class="mb-4">
        <label for="hook-name" class="block text-xs font-medium text-slate-500 mb-1.5">Hook Name</label>
        <input id="hook-name" type="text" bind:value={hookName} placeholder="e.g., upload-notifier"
          class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
      </div>

      <!-- Enabled -->
      <div class="mb-4">
        <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
          <input type="checkbox" bind:checked={hookEnabled} class="rounded border-slate-300" />
          Enabled
        </label>
      </div>

      <!-- Event Types -->
      <div class="mb-4">
        <label for="hook-event-input" class="block text-xs font-medium text-slate-500 mb-1.5">Event Types</label>
        <div class="flex gap-2 mb-2">
          <input id="hook-event-input" type="text" bind:value={hookEventInput}
            placeholder="e.g., file.created"
            onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addHookEvent(); } }}
            class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
          <button
            class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
            onclick={addHookEvent}
          >
            Add
          </button>
        </div>
        <!-- Quick-add buttons -->
        <div class="flex flex-wrap gap-1 mb-2">
          {#each HOOK_EVENT_TYPES as evtType}
            {#if !hookEvents.includes(evtType)}
              <button
                class="px-2 py-0.5 text-[10px] font-mono rounded border border-slate-200 bg-slate-50 text-slate-500 hover:bg-blue-50 hover:text-blue-600 hover:border-blue-200 transition-colors"
                onclick={() => { hookEvents = [...hookEvents, evtType]; }}
              >{evtType}</button>
            {/if}
          {/each}
        </div>
        {#if hookEvents.length > 0}
          <div class="flex flex-wrap gap-1.5">
            {#each hookEvents as ev, i}
              <span class="inline-flex items-center gap-1 px-2 py-1 bg-blue-50 border border-blue-200 rounded text-xs font-mono text-blue-700">
                {ev}
                <button
                  class="flex items-center justify-center w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-blue-400 hover:text-red-500 transition-colors"
                  onclick={() => removeHookEvent(i)}
                  title="Remove"
                >&times;</button>
              </span>
            {/each}
          </div>
        {/if}
      </div>

      <!-- Filters -->
      <div class="mb-4 p-4 bg-slate-50 border border-slate-200 rounded-md">
        <p class="text-xs font-semibold text-slate-500 mb-3 uppercase tracking-wider">Filters (optional)</p>

        <!-- Mount filter -->
        <div class="mb-3">
          <label for="hook-mount-filter" class="block text-xs font-medium text-slate-500 mb-1.5">Mount Prefixes</label>
          <div class="flex gap-2">
            <input id="hook-mount-filter" type="text" bind:value={hookFilterMountInput}
              placeholder="e.g., uploads"
              onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addHookFilterMount(); } }}
              class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10 bg-white" />
            <button class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors" onclick={addHookFilterMount}>Add</button>
          </div>
          <p class="mt-1 text-[11px] text-slate-400">Only fire for events on these mounts. Leave empty for all mounts.</p>
          {#if hookFilterMounts.length > 0}
            <div class="mt-2 flex flex-wrap gap-1.5">
              {#each hookFilterMounts as m, i}
                <span class="inline-flex items-center gap-1 px-2 py-1 bg-blue-50 border border-blue-200 rounded text-xs font-mono text-blue-700">
                  {m}
                  <button class="flex items-center justify-center w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-blue-400 hover:text-red-500 transition-colors" onclick={() => removeHookFilterMount(i)} title="Remove">&times;</button>
                </span>
              {/each}
            </div>
          {/if}
        </div>

        <!-- Path pattern -->
        <div>
          <label for="hook-path-pattern" class="block text-xs font-medium text-slate-500 mb-1.5">Path Pattern</label>
          <input id="hook-path-pattern" type="text" bind:value={hookFilterPathPattern} placeholder="e.g., *.pdf"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10 bg-white" />
          <p class="mt-1 text-[11px] text-slate-400">Glob pattern to match file paths. Leave empty for all paths.</p>
        </div>
      </div>

      <!-- Targets -->
      <div class="mb-4">
        <div class="flex items-center justify-between mb-2">
          <p class="text-xs font-semibold text-slate-500 uppercase tracking-wider">Targets</p>
          <button
            class="flex items-center gap-1 px-2 py-1 text-xs text-blue-600 bg-blue-50 border border-blue-200 rounded hover:bg-blue-100 transition-colors"
            onclick={() => { showAddTarget = true; resetTargetForm(); }}
          >
            <Plus size={12} />
            Add Target
          </button>
        </div>

        <!-- Target list -->
        {#if hookTargets.length > 0}
          <div class="space-y-2 mb-3">
            {#each hookTargets as t, i}
              <div class="flex items-center gap-3 p-3 bg-white border border-slate-200 rounded-md">
                <div class="flex-1 min-w-0">
                  <div class="flex items-center gap-2">
                    <span class="px-1.5 py-0.5 text-[10px] font-medium rounded
                      {t.type === 'http' ? 'bg-emerald-100 text-emerald-700' : t.type === 'kafka' ? 'bg-purple-100 text-purple-700' : t.type === 'redis' ? 'bg-red-100 text-red-700' : t.type === 'nats' ? 'bg-sky-100 text-sky-700' : 'bg-amber-100 text-amber-700'}">{t.type.toUpperCase()}</span>
                    {#if t.type === 'http' && t.http}
                      <span class="text-xs text-slate-600 font-mono truncate">{t.http.method || 'POST'} {t.http.url}</span>
                    {:else if t.type === 'kafka' && t.kafka}
                      <span class="text-xs text-slate-600 font-mono truncate">{t.kafka.topic} ({t.kafka.brokers.join(', ')})</span>
                    {:else if t.type === 'redis' && t.redis}
                      <span class="text-xs text-slate-600 font-mono truncate">{t.redis.channel} ({t.redis.addresses?.length ? t.redis.addresses.join(', ') : t.redis.address})</span>
                    {:else if t.type === 'nats' && t.nats}
                      <span class="text-xs text-slate-600 font-mono truncate">{t.nats.subject} ({t.nats.url})</span>
                    {:else if t.type === 'log' && t.log}
                      <span class="text-xs text-slate-600 font-mono truncate">{(t.log.level || 'info').toUpperCase()}{t.log.message ? `: ${t.log.message}` : ''}</span>
                    {/if}
                  </div>
                  {#if t.body_template}
                    <p class="mt-0.5 text-[10px] text-slate-400 truncate">Template: {t.body_template}</p>
                  {/if}
                </div>
                <div class="flex items-center gap-1 shrink-0">
                  <button class="p-1.5 text-slate-400 hover:text-blue-500 hover:bg-blue-50 rounded transition-colors" onclick={() => handleEditTarget(i)} title="Edit">
                    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/><path d="m15 5 4 4"/></svg>
                  </button>
                  <button class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors" onclick={() => removeTarget(i)} title="Remove">
                    <Trash2 size={14} />
                  </button>
                </div>
              </div>
            {/each}
          </div>
        {:else if !showAddTarget}
          <p class="text-xs text-slate-400 mb-3">No targets added yet. Add at least one HTTP or Kafka target.</p>
        {/if}

        <!-- Add/Edit Target Form -->
        {#if showAddTarget}
          <div class="p-4 bg-slate-50 border border-slate-200 rounded-md mb-3">
            <h4 class="text-xs font-semibold text-slate-600 mb-3">{editingTargetIndex !== null ? 'Edit Target' : 'Add Target'}</h4>

            <!-- Target type -->
            <div class="mb-3">
              <label class="block text-xs font-medium text-slate-500 mb-1.5">Type</label>
              <div class="flex gap-2">
                <button
                  class="flex-1 px-3 py-2 text-xs font-medium rounded-md border transition-colors
                    {targetType === 'http' ? 'bg-emerald-50 border-emerald-300 text-emerald-700' : 'bg-white border-slate-200 text-slate-500 hover:bg-slate-50'}"
                  onclick={() => targetType = 'http'}
                >HTTP Webhook</button>
                <button
                  class="flex-1 px-3 py-2 text-xs font-medium rounded-md border transition-colors
                    {targetType === 'kafka' ? 'bg-purple-50 border-purple-300 text-purple-700' : 'bg-white border-slate-200 text-slate-500 hover:bg-slate-50'}"
                  onclick={() => targetType = 'kafka'}
                >Kafka</button>
                <button
                  class="flex-1 px-3 py-2 text-xs font-medium rounded-md border transition-colors
                    {targetType === 'redis' ? 'bg-red-50 border-red-300 text-red-700' : 'bg-white border-slate-200 text-slate-500 hover:bg-slate-50'}"
                  onclick={() => targetType = 'redis'}
                >Redis</button>
                <button
                  class="flex-1 px-3 py-2 text-xs font-medium rounded-md border transition-colors
                    {targetType === 'nats' ? 'bg-sky-50 border-sky-300 text-sky-700' : 'bg-white border-slate-200 text-slate-500 hover:bg-slate-50'}"
                  onclick={() => targetType = 'nats'}
                >NATS</button>
                <button
                  class="flex-1 px-3 py-2 text-xs font-medium rounded-md border transition-colors
                    {targetType === 'log' ? 'bg-amber-50 border-amber-300 text-amber-700' : 'bg-white border-slate-200 text-slate-500 hover:bg-slate-50'}"
                  onclick={() => targetType = 'log'}
                >Log</button>
              </div>
            </div>

            {#if targetType === 'http'}
              <!-- HTTP target fields -->
              <div class="grid grid-cols-4 gap-3 mb-3">
                <div>
                  <label for="target-http-method" class="block text-xs font-medium text-slate-500 mb-1">Method</label>
                  <select id="target-http-method" bind:value={targetHttpMethod}
                    class="w-full px-2 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 bg-white">
                    <option value="POST">POST</option>
                    <option value="PUT">PUT</option>
                    <option value="PATCH">PATCH</option>
                  </select>
                </div>
                <div class="col-span-3">
                  <label for="target-http-url" class="block text-xs font-medium text-slate-500 mb-1">URL</label>
                  <input id="target-http-url" type="text" bind:value={targetHttpUrl} placeholder="https://example.com/webhook"
                    class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                </div>
              </div>
              <div class="mb-3">
                <label for="target-http-timeout" class="block text-xs font-medium text-slate-500 mb-1">Timeout</label>
                <input id="target-http-timeout" type="text" bind:value={targetHttpTimeout} placeholder="30s"
                  class="w-32 px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>

              <!-- HTTP Headers -->
              <div class="mb-3">
                <div class="flex items-center justify-between mb-1">
                  <label class="block text-xs font-medium text-slate-500">Headers</label>
                  <button class="text-[10px] text-blue-500 hover:text-blue-700" onclick={addTargetHeader}>+ Add Header</button>
                </div>
                {#each targetHttpHeaders as header, i}
                  <div class="flex gap-2 mb-1.5">
                    <input type="text" bind:value={header.key} placeholder="Header name"
                      class="flex-1 px-2 py-1.5 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500" />
                    <input type="text" bind:value={header.value} placeholder="Value"
                      class="flex-1 px-2 py-1.5 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500" />
                    <button class="p-1 text-slate-400 hover:text-red-500" onclick={() => removeTargetHeader(i)} title="Remove">
                      <Trash2 size={12} />
                    </button>
                  </div>
                {/each}
              </div>
            {:else if targetType === 'kafka'}
              <!-- Kafka target fields -->
              <div class="mb-3">
                <label for="target-kafka-broker" class="block text-xs font-medium text-slate-500 mb-1">Brokers</label>
                <div class="flex gap-2">
                  <input id="target-kafka-broker" type="text" bind:value={targetKafkaBrokerInput}
                    placeholder="kafka:9092"
                    onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addTargetKafkaBroker(); } }}
                    class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                  <button class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors" onclick={addTargetKafkaBroker}>Add</button>
                </div>
                {#if targetKafkaBrokers.length > 0}
                  <div class="mt-1.5 flex flex-wrap gap-1.5">
                    {#each targetKafkaBrokers as b, i}
                      <span class="inline-flex items-center gap-1 px-2 py-1 bg-purple-50 border border-purple-200 rounded text-xs font-mono text-purple-700">
                        {b}
                        <button class="flex items-center justify-center w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-purple-400 hover:text-red-500 transition-colors" onclick={() => removeTargetKafkaBroker(i)} title="Remove">&times;</button>
                      </span>
                    {/each}
                  </div>
                {/if}
              </div>
              <div class="mb-3">
                <label for="target-kafka-topic" class="block text-xs font-medium text-slate-500 mb-1">Topic</label>
                <input id="target-kafka-topic" type="text" bind:value={targetKafkaTopic} placeholder="pika-events"
                  class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
              <div class="mb-3">
                <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
                  <input type="checkbox" bind:checked={targetKafkaAutoTopicCreation} class="rounded border-slate-300" />
                  Auto topic creation
                </label>
                <p class="mt-1 text-[11px] text-slate-400 pl-5">Automatically create the topic on first produce. Requires broker <code class="px-0.5 bg-slate-100 rounded text-[10px]">auto.create.topics.enable=true</code>.</p>
              </div>
              <div class="mb-3">
                <label for="target-kafka-key" class="block text-xs font-medium text-slate-500 mb-1">Key Template (optional)</label>
                <input id="target-kafka-key" type="text" bind:value={targetKafkaKeyTemplate} placeholder={'{{.Mount}}/{{.Path}}'}
                  class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                <p class="mt-1 text-[11px] text-slate-400">Go template for the Kafka message key. Default: mount/path</p>
              </div>

              <!-- Security -->
              <div class="p-3 bg-white border border-slate-200 rounded-md mb-3">
                <p class="text-[11px] font-semibold text-slate-500 uppercase tracking-wider mb-3">Security</p>

                <!-- TLS -->
                <div class="mb-3">
                  <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer mb-2">
                    <input type="checkbox" bind:checked={targetKafkaTLSEnabled} class="rounded border-slate-300" />
                    Enable TLS
                  </label>
                  {#if targetKafkaTLSEnabled}
                    <!-- Input mode toggle -->
                    <div class="flex gap-1 mb-2 pl-5">
                      <button
                        class="px-2 py-1 text-[10px] font-medium rounded border transition-colors
                          {targetKafkaTLSInputMode === 'path' ? 'bg-blue-50 border-blue-300 text-blue-700' : 'bg-white border-slate-200 text-slate-500 hover:bg-slate-50'}"
                        onclick={() => targetKafkaTLSInputMode = 'path'}
                      >File Path</button>
                      <button
                        class="px-2 py-1 text-[10px] font-medium rounded border transition-colors
                          {targetKafkaTLSInputMode === 'pem' ? 'bg-blue-50 border-blue-300 text-blue-700' : 'bg-white border-slate-200 text-slate-500 hover:bg-slate-50'}"
                        onclick={() => targetKafkaTLSInputMode = 'pem'}
                      >PEM / Reference</button>
                    </div>

                    {#if targetKafkaTLSInputMode === 'path'}
                      <div class="grid grid-cols-1 gap-2 pl-5">
                        <div>
                          <label for="kafka-tls-ca" class="block text-[11px] font-medium text-slate-500 mb-1">CA File (optional)</label>
                          <input id="kafka-tls-ca" type="text" bind:value={targetKafkaTLSCAFile} placeholder="/path/to/ca.pem"
                            class="w-full px-2 py-1.5 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500" />
                        </div>
                        <div>
                          <label for="kafka-tls-cert" class="block text-[11px] font-medium text-slate-500 mb-1">Client Cert File (optional)</label>
                          <input id="kafka-tls-cert" type="text" bind:value={targetKafkaTLSCertFile} placeholder="/path/to/cert.pem"
                            class="w-full px-2 py-1.5 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500" />
                        </div>
                        <div>
                          <label for="kafka-tls-key" class="block text-[11px] font-medium text-slate-500 mb-1">Client Key File (optional)</label>
                          <input id="kafka-tls-key" type="text" bind:value={targetKafkaTLSKeyFile} placeholder="/path/to/key.pem"
                            class="w-full px-2 py-1.5 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500" />
                        </div>
                      </div>
                    {:else}
                      <div class="grid grid-cols-1 gap-2 pl-5">
                        <div>
                          <label for="kafka-tls-ca-pem" class="block text-[11px] font-medium text-slate-500 mb-1">CA Certificate (optional)</label>
                          <textarea id="kafka-tls-ca-pem" bind:value={targetKafkaTLSCAPEM} rows={3}
                            placeholder="Paste PEM content, or use raw://mount/path or config://file/key"
                            class="w-full px-2 py-1.5 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 resize-y"></textarea>
                        </div>
                        <div>
                          <label for="kafka-tls-cert-pem" class="block text-[11px] font-medium text-slate-500 mb-1">Client Certificate (optional)</label>
                          <textarea id="kafka-tls-cert-pem" bind:value={targetKafkaTLSCertPEM} rows={3}
                            placeholder="Paste PEM content, or use raw://certs/client.pem"
                            class="w-full px-2 py-1.5 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 resize-y"></textarea>
                        </div>
                        <div>
                          <label for="kafka-tls-key-pem" class="block text-[11px] font-medium text-slate-500 mb-1">Client Key (optional)</label>
                          <textarea id="kafka-tls-key-pem" bind:value={targetKafkaTLSKeyPEM} rows={3}
                            placeholder="Paste PEM content, or use config://tls/kafka-key"
                            class="w-full px-2 py-1.5 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 resize-y"></textarea>
                        </div>
                        <p class="text-[10px] text-slate-400">
                          Supports: inline PEM text, <code class="px-0.5 bg-slate-100 rounded">raw://mount/path</code> (from raw mounts), or <code class="px-0.5 bg-slate-100 rounded">config://file/key</code> (from config store).
                        </p>
                      </div>
                    {/if}
                  {/if}
                </div>

                <!-- SASL -->
                <div>
                  <label for="kafka-sasl-type" class="block text-[11px] font-medium text-slate-500 mb-1">SASL Authentication</label>
                  <select id="kafka-sasl-type" bind:value={targetKafkaSASLType}
                    class="w-full px-2 py-1.5 text-xs border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 bg-white mb-2">
                    <option value="none">None</option>
                    <option value="plain">SASL/PLAIN</option>
                    <option value="scram">SASL/SCRAM</option>
                  </select>

                  {#if targetKafkaSASLType !== 'none'}
                    <div class="pl-5 space-y-2">
                      {#if targetKafkaSASLType === 'scram'}
                        <div>
                          <label for="kafka-scram-algo" class="block text-[11px] font-medium text-slate-500 mb-1">Algorithm</label>
                          <select id="kafka-scram-algo" bind:value={targetKafkaSASLAlgorithm}
                            class="w-full px-2 py-1.5 text-xs border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 bg-white">
                            <option value="SCRAM-SHA-256">SCRAM-SHA-256</option>
                            <option value="SCRAM-SHA-512">SCRAM-SHA-512</option>
                          </select>
                        </div>
                      {/if}
                      <div>
                        <label for="kafka-sasl-user" class="block text-[11px] font-medium text-slate-500 mb-1">Username</label>
                        <input id="kafka-sasl-user" type="text" bind:value={targetKafkaSASLUser} placeholder="kafka-user"
                          class="w-full px-2 py-1.5 text-xs border border-slate-200 rounded-md focus:outline-none focus:border-blue-500" />
                      </div>
                      <div>
                        <label for="kafka-sasl-pass" class="block text-[11px] font-medium text-slate-500 mb-1">Password</label>
                        <div class="flex gap-1">
                          <input id="kafka-sasl-pass" type={showKafkaSASLPass ? 'text' : 'password'} bind:value={targetKafkaSASLPass} placeholder="password"
                            class="flex-1 px-2 py-1.5 text-xs border border-slate-200 rounded-md focus:outline-none focus:border-blue-500" />
                          <button class="p-1.5 text-slate-400 hover:text-slate-600 border border-slate-200 rounded-md" onclick={() => showKafkaSASLPass = !showKafkaSASLPass} title={showKafkaSASLPass ? 'Hide' : 'Show'}>
                            {#if showKafkaSASLPass}<EyeOff size={12} />{:else}<Eye size={12} />{/if}
                          </button>
                        </div>
                      </div>
                      {#if targetKafkaSASLType === 'scram'}
                        <label class="flex items-center gap-1.5 text-[11px] text-slate-500 cursor-pointer">
                          <input type="checkbox" bind:checked={targetKafkaSASLIsToken} class="rounded border-slate-300" />
                          Delegation token (tokenauth=true)
                        </label>
                      {/if}
                    </div>
                  {/if}
                </div>
              </div>
            {:else if targetType === 'redis'}
              <!-- Redis target fields -->
              <div class="flex gap-4 mb-3">
                <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
                  <input type="checkbox" bind:checked={targetRedisCluster} class="rounded border-slate-300" />
                  Cluster mode
                </label>
                <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
                  <input type="checkbox" bind:checked={targetRedisTLSEnabled} class="rounded border-slate-300" />
                  Use TLS
                </label>
              </div>

              {#if targetRedisTLSEnabled}
                <div class="grid grid-cols-3 gap-3 mb-3">
                  <div>
                    <label for="target-redis-tls-ca" class="block text-xs font-medium text-slate-500 mb-1">CA File (optional)</label>
                    <input id="target-redis-tls-ca" type="text" bind:value={targetRedisTLSCAFile} placeholder="/path/to/ca.pem"
                      class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                  </div>
                  <div>
                    <label for="target-redis-tls-cert" class="block text-xs font-medium text-slate-500 mb-1">Cert File (optional)</label>
                    <input id="target-redis-tls-cert" type="text" bind:value={targetRedisTLSCertFile} placeholder="/path/to/cert.pem"
                      class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                  </div>
                  <div>
                    <label for="target-redis-tls-key" class="block text-xs font-medium text-slate-500 mb-1">Key File (optional)</label>
                    <input id="target-redis-tls-key" type="text" bind:value={targetRedisTLSKeyFile} placeholder="/path/to/key.pem"
                      class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                  </div>
                </div>
                <p class="mb-3 text-[11px] text-slate-400">
                  Paths support references: <code class="px-0.5 bg-slate-100 rounded text-[10px]">raw://mount/path</code> (from raw mounts) or <code class="px-0.5 bg-slate-100 rounded text-[10px]">config://key</code> (from config store)
                </p>
              {/if}

              {#if targetRedisCluster}
                <div class="mb-3">
                  <label class="block text-xs font-medium text-slate-500 mb-1">Cluster Addresses</label>
                  <div class="flex flex-wrap gap-1.5 mb-2">
                    {#each targetRedisAddresses as addr, i}
                      <span class="flex items-center gap-1 px-2 py-0.5 bg-slate-100 text-slate-600 rounded text-xs font-mono">
                        {addr}
                        <button class="text-slate-400 hover:text-red-500" onclick={() => targetRedisAddresses = targetRedisAddresses.filter((_, j) => j !== i)}>&times;</button>
                      </span>
                    {/each}
                  </div>
                  <div class="flex gap-2">
                    <input type="text" bind:value={targetRedisAddressInput} placeholder="node:6379"
                      class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
                      onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); if (targetRedisAddressInput.trim()) { targetRedisAddresses = [...targetRedisAddresses, targetRedisAddressInput.trim()]; targetRedisAddressInput = ''; } } }} />
                    <button class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
                      onclick={() => { if (targetRedisAddressInput.trim()) { targetRedisAddresses = [...targetRedisAddresses, targetRedisAddressInput.trim()]; targetRedisAddressInput = ''; } }}>Add</button>
                  </div>
                </div>
              {:else}
                <div class="grid grid-cols-2 gap-3 mb-3">
                  <div>
                    <label for="target-redis-address" class="block text-xs font-medium text-slate-500 mb-1">Address</label>
                    <input id="target-redis-address" type="text" bind:value={targetRedisAddress} placeholder="localhost:6379"
                      class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                  </div>
                  <div>
                    <label for="target-redis-db" class="block text-xs font-medium text-slate-500 mb-1">DB (optional)</label>
                    <input id="target-redis-db" type="number" bind:value={targetRedisDB} min="0" max="15"
                      class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                  </div>
                </div>
              {/if}

              <div class="grid grid-cols-2 gap-3 mb-3">
                <div>
                  <label for="target-redis-channel" class="block text-xs font-medium text-slate-500 mb-1">Channel</label>
                  <input id="target-redis-channel" type="text" bind:value={targetRedisChannel} placeholder="pika-events"
                    class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                </div>
                <div>
                  <label for="target-redis-password" class="block text-xs font-medium text-slate-500 mb-1">Password (optional)</label>
                  <input id="target-redis-password" type="password" bind:value={targetRedisPassword} placeholder="Password"
                    class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                </div>
              </div>

            {:else if targetType === 'nats'}
              <!-- NATS target fields -->
              <div class="grid grid-cols-2 gap-3 mb-3">
                <div>
                  <label for="target-nats-url" class="block text-xs font-medium text-slate-500 mb-1">URL</label>
                  <input id="target-nats-url" type="text" bind:value={targetNatsUrl} placeholder="nats://localhost:4222"
                    class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                </div>
                <div>
                  <label for="target-nats-subject" class="block text-xs font-medium text-slate-500 mb-1">Subject</label>
                  <input id="target-nats-subject" type="text" bind:value={targetNatsSubject} placeholder="pika.events"
                    class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                </div>
              </div>
              <div class="mb-3">
                <label for="target-nats-token" class="block text-xs font-medium text-slate-500 mb-1">Token (optional)</label>
                <input id="target-nats-token" type="password" bind:value={targetNatsToken} placeholder="Auth token"
                  class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
              </div>
              <div class="grid grid-cols-2 gap-3 mb-3">
                <div>
                  <label for="target-nats-username" class="block text-xs font-medium text-slate-500 mb-1">Username (optional)</label>
                  <input id="target-nats-username" type="text" bind:value={targetNatsUsername} placeholder="Username"
                    class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                </div>
                <div>
                  <label for="target-nats-password" class="block text-xs font-medium text-slate-500 mb-1">Password (optional)</label>
                  <input id="target-nats-password" type="password" bind:value={targetNatsPassword} placeholder="Password"
                    class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                </div>
              </div>
            {:else if targetType === 'log'}
              <!-- Log (local slog) target fields -->
              <div class="grid grid-cols-4 gap-3 mb-3">
                <div>
                  <label for="target-log-level" class="block text-xs font-medium text-slate-500 mb-1">Level</label>
                  <select id="target-log-level" bind:value={targetLogLevel}
                    class="w-full px-2 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 bg-white">
                    <option value="debug">debug</option>
                    <option value="info">info</option>
                    <option value="warn">warn</option>
                    <option value="error">error</option>
                  </select>
                </div>
                <div class="col-span-3">
                  <label for="target-log-message" class="block text-xs font-medium text-slate-500 mb-1">Message Template (optional)</label>
                  <input id="target-log-message" type="text" bind:value={targetLogMessage}
                    placeholder={'file {{.Path}} {{.Type}} on {{.Mount}}'}
                    class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
                </div>
              </div>
              <p class="mb-3 text-[11px] text-slate-400">Default message (when empty) is the event type, e.g. <code class="px-0.5 bg-slate-100 rounded text-[10px]">file.created</code>.</p>

              <!-- Log Fields -->
              <div class="mb-3">
                <div class="flex items-center justify-between mb-1">
                  <label class="block text-xs font-medium text-slate-500">Fields</label>
                  <button class="text-[10px] text-blue-500 hover:text-blue-700" onclick={addTargetLogField}>+ Add Field</button>
                </div>
                {#each targetLogFields as field, i}
                  <div class="flex gap-2 mb-1.5">
                    <input type="text" bind:value={field.key} placeholder="attr name"
                      class="flex-1 px-2 py-1.5 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500" />
                    <input type="text" bind:value={field.value} placeholder={'{{.Mount}}/{{.Path}}'}
                      class="flex-1 px-2 py-1.5 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500" />
                    <button class="p-1 text-slate-400 hover:text-red-500" onclick={() => removeTargetLogField(i)} title="Remove">
                      <Trash2 size={12} />
                    </button>
                  </div>
                {/each}
                <p class="mt-1 text-[11px] text-slate-400">Each value is a Go template rendered against the Event. Rendered as slog attributes alongside <code class="px-0.5 bg-slate-100 rounded text-[10px]">hook</code>.</p>
              </div>
            {/if}

            <!-- Body Template (shared) — not applicable to the log target,
                 which renders its own Message and Fields directly from the Event. -->
            {#if targetType !== 'log'}
              <div class="mb-3">
                <label for="target-body-template" class="block text-xs font-medium text-slate-500 mb-1">Body Template (optional)</label>
                <textarea id="target-body-template" bind:value={targetBodyTemplate} rows={3}
                  placeholder={'{"text": "File {{.Path}} was {{.Type}} on {{.Mount}}"}'}
                  class="w-full px-3 py-2 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10 resize-y"></textarea>
                <p class="mt-1 text-[11px] text-slate-400">
                  Go text/template. Available fields: <code class="px-0.5 bg-slate-100 rounded text-[10px]">.Type</code>, <code class="px-0.5 bg-slate-100 rounded text-[10px]">.Mount</code>, <code class="px-0.5 bg-slate-100 rounded text-[10px]">.Path</code>, <code class="px-0.5 bg-slate-100 rounded text-[10px]">.Size</code>, <code class="px-0.5 bg-slate-100 rounded text-[10px]">.Protocol</code>, <code class="px-0.5 bg-slate-100 rounded text-[10px]">.User</code>, <code class="px-0.5 bg-slate-100 rounded text-[10px]">.Timestamp</code>. Leave empty for default JSON payload.
                </p>
              </div>
            {/if}

            <div class="flex justify-end gap-2">
              <button class="px-3 py-1.5 text-xs text-slate-600 bg-white border border-slate-200 rounded-md hover:bg-slate-50 transition-colors"
                onclick={() => { showAddTarget = false; resetTargetForm(); }}>Cancel</button>
              <button class="px-3 py-1.5 text-xs text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
                onclick={handleAddTarget}>{editingTargetIndex !== null ? 'Save Target' : 'Add Target'}</button>
            </div>
          </div>
        {/if}
      </div>

      <!-- Save / Cancel -->
      <div class="flex justify-end gap-2">
        <button
          class="px-3 py-2 text-sm text-slate-600 bg-white border border-slate-200 rounded-md hover:bg-slate-50 transition-colors"
          onclick={() => { showAddHook = false; resetHookForm(); }}
        >Cancel</button>
        <button
          class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          onclick={handleAddHook}
          disabled={isSavingHooks}
        >{isSavingHooks ? 'Saving...' : editingHookIndex !== null ? 'Save Changes' : 'Add Hook'}</button>
      </div>
    </div>
  {/if}

  <!-- Hook List -->
  {#if hooks.length === 0 && !showAddHook}
    <div class="text-center py-12 bg-white border border-slate-200 rounded-lg">
      <Webhook size={32} class="mx-auto text-slate-300 mb-3" />
      <p class="text-sm text-slate-500">No hooks configured</p>
      <p class="text-xs text-slate-400 mt-1">Add a hook to trigger notifications when files change</p>
    </div>
  {:else if !showAddHook}
    <div class="space-y-2">
      {#each hooks as h, i (h.name)}
        <div class="p-4 bg-white border border-slate-200 rounded-lg hover:border-slate-300 transition-colors">
          <div class="flex items-center gap-4">
            <div class="flex-1 min-w-0">
              <div class="flex items-center gap-2">
                <span class="text-sm font-medium text-slate-800">{h.name}</span>
                <span class="px-1.5 py-0.5 text-[10px] font-medium rounded {h.enabled ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-100 text-slate-500'}">
                  {h.enabled ? 'Active' : 'Disabled'}
                </span>
              </div>
              <div class="mt-1 flex flex-wrap gap-1">
                {#each h.events as ev}
                  <span class="px-1.5 py-0.5 text-[10px] font-mono rounded bg-blue-50 text-blue-600 border border-blue-100">{ev}</span>
                {/each}
              </div>
              {#if h.filter?.mounts?.length || h.filter?.path_pattern}
                <div class="mt-1 flex flex-wrap gap-1 items-center">
                  <span class="text-[10px] text-slate-400">Filters:</span>
                  {#if h.filter?.mounts}
                    {#each h.filter.mounts as m}
                      <span class="px-1.5 py-0.5 text-[10px] font-mono rounded bg-amber-50 text-amber-600 border border-amber-100">{m}</span>
                    {/each}
                  {/if}
                  {#if h.filter?.path_pattern}
                    <span class="px-1.5 py-0.5 text-[10px] font-mono rounded bg-violet-50 text-violet-600 border border-violet-100">{h.filter.path_pattern}</span>
                  {/if}
                </div>
              {/if}
              <div class="mt-1 flex flex-wrap gap-1">
                {#each h.targets as t}
                  <span class="px-1.5 py-0.5 text-[10px] font-medium rounded
                    {t.type === 'http' ? 'bg-emerald-50 text-emerald-600 border border-emerald-100' : t.type === 'kafka' ? 'bg-purple-50 text-purple-600 border border-purple-100' : t.type === 'redis' ? 'bg-red-50 text-red-600 border border-red-100' : t.type === 'nats' ? 'bg-sky-50 text-sky-600 border border-sky-100' : 'bg-amber-50 text-amber-600 border border-amber-100'}">
                    {t.type === 'http' && t.http ? t.http.url : t.type === 'kafka' && t.kafka ? t.kafka.topic : t.type === 'redis' && t.redis ? t.redis.channel : t.type === 'nats' && t.nats ? t.nats.subject : t.type === 'log' && t.log ? `log:${t.log.level || 'info'}` : t.type}
                  </span>
                {/each}
              </div>
            </div>
            <div class="flex items-center gap-1 shrink-0">
              <button
                class="p-1.5 text-slate-400 hover:text-emerald-500 hover:bg-emerald-50 rounded transition-colors"
                onclick={() => handleToggleHook(i)}
                title={h.enabled ? 'Disable' : 'Enable'}
              >
                {#if h.enabled}
                  <Eye size={14} />
                {:else}
                  <EyeOff size={14} />
                {/if}
              </button>
              <button
                class="p-1.5 text-slate-400 hover:text-blue-500 hover:bg-blue-50 rounded transition-colors"
                onclick={() => handleEditHook(i)}
                title="Edit hook"
              >
                <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/><path d="m15 5 4 4"/></svg>
              </button>
              <button
                class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors"
                onclick={() => handleRemoveHook(i)}
                title="Remove hook"
              >
                <Trash2 size={14} />
              </button>
            </div>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>
