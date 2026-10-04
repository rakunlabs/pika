<script lang="ts">
  import { untrack } from "svelte";
  import { Eye, EyeOff } from "lucide-svelte";
  import type {
    HookTarget,
    KafkaSASLEntry,
    KafkaSecurity,
  } from "@/lib/types/config";
  import { addToast } from "@/lib/store/toast.svelte";
  import { inputClass, inputSmClass } from "@/lib/ui";
  import {
    addBtnClass,
    checkboxClass,
    codeClass,
    labelClass,
    labelSmClass,
  } from "./shared";

  type Props = { initial?: HookTarget["kafka"] };
  let { initial }: Props = $props();

  type SaslType = "none" | "plain" | "scram";
  type ScramAlgo = "SCRAM-SHA-256" | "SCRAM-SHA-512";

  const init = untrack(() => initial);
  const sec = init?.security;
  const sasl0 = sec?.sasl?.[0];
  const initSasl: SaslType = sasl0?.plain?.enabled
    ? "plain"
    : sasl0?.scram?.enabled
      ? "scram"
      : "none";

  let brokers = $state<string[]>([...(init?.brokers ?? [])]);
  let brokerInput = $state("");
  let topic = $state(init?.topic ?? "");
  let autoTopicCreation = $state(init?.auto_topic_creation ?? true);
  let keyTemplate = $state(init?.key_template || "");
  // Security
  let tlsEnabled = $state(sec?.tls?.enabled ?? false);
  let tlsCertFile = $state(sec?.tls?.cert_file ?? "");
  let tlsCertPEM = $state(sec?.tls?.cert_pem ?? "");
  let tlsKeyFile = $state(sec?.tls?.key_file ?? "");
  let tlsKeyPEM = $state(sec?.tls?.key_pem ?? "");
  let tlsCAFile = $state(sec?.tls?.ca_file ?? "");
  let tlsCAPEM = $state(sec?.tls?.ca_pem ?? "");
  // Determine input mode from which fields are populated
  let tlsInputMode = $state<"path" | "pem">(
    sec?.tls?.ca_pem || sec?.tls?.cert_pem || sec?.tls?.key_pem
      ? "pem"
      : "path",
  );
  let saslType = $state<SaslType>(initSasl);
  let saslUser = $state(
    (initSasl === "plain" ? sasl0?.plain?.user : sasl0?.scram?.user) ?? "",
  );
  let saslPass = $state(
    (initSasl === "plain" ? sasl0?.plain?.pass : sasl0?.scram?.pass) ?? "",
  );
  let saslAlgorithm = $state<ScramAlgo>(
    (initSasl === "scram" ? (sasl0?.scram?.algorithm as ScramAlgo) : undefined) ??
      "SCRAM-SHA-256",
  );
  let saslIsToken = $state(
    initSasl === "scram" ? (sasl0?.scram?.is_token ?? false) : false,
  );
  let showSaslPass = $state(false);

  function addBroker() {
    const b = brokerInput.trim();
    if (!b) return;
    if (brokers.includes(b)) {
      addToast("Broker already added", "alert");
      return;
    }
    brokers = [...brokers, b];
    brokerInput = "";
  }

  export function build(bodyTemplate: string | undefined): HookTarget | null {
    if (brokers.length === 0) {
      addToast("At least one Kafka broker is required", "alert");
      return null;
    }
    if (!topic.trim()) {
      addToast("Kafka topic is required", "alert");
      return null;
    }

    // Build security config
    let security: KafkaSecurity | undefined;
    const hasSASL = saslType !== "none";
    if (tlsEnabled || hasSASL) {
      security = {};
      if (tlsEnabled) {
        security.tls = {
          enabled: true,
          cert_file: tlsCertFile || undefined,
          cert_pem: tlsCertPEM || undefined,
          key_file: tlsKeyFile || undefined,
          key_pem: tlsKeyPEM || undefined,
          ca_file: tlsCAFile || undefined,
          ca_pem: tlsCAPEM || undefined,
        };
      }
      if (hasSASL) {
        const entry: KafkaSASLEntry = {};
        if (saslType === "plain") {
          entry.plain = { enabled: true, user: saslUser, pass: saslPass };
        } else if (saslType === "scram") {
          entry.scram = {
            enabled: true,
            algorithm: saslAlgorithm,
            user: saslUser,
            pass: saslPass,
            is_token: saslIsToken || undefined,
          };
        }
        security.sasl = [entry];
      }
    }

    return {
      type: "kafka",
      kafka: {
        brokers: [...brokers],
        topic: topic.trim(),
        key_template: keyTemplate || undefined,
        auto_topic_creation: autoTopicCreation,
        security,
      },
      body_template: bodyTemplate,
    };
  }

  const modeBtn = (active: boolean) =>
    `px-2 py-1 text-[10px] font-medium rounded border transition-colors cursor-pointer ${
      active
        ? "bg-accent-50 border-accent-300 text-accent-700 dark:bg-accent-900/40 dark:border-accent-700 dark:text-accent-300"
        : "bg-white dark:bg-warm-800 border-slate-200 dark:border-warm-700 text-slate-500 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-warm-700"
    }`;
</script>

<div class="mb-3">
  <label for="target-kafka-broker" class={labelClass}>Brokers</label>
  <div class="flex gap-2">
    <input
      id="target-kafka-broker"
      type="text"
      bind:value={brokerInput}
      placeholder="kafka:9092"
      onkeydown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          addBroker();
        }
      }}
      class="{inputClass} flex-1 font-mono"
    />
    <button type="button" class={addBtnClass} onclick={addBroker}>Add</button>
  </div>
  {#if brokers.length > 0}
    <div class="mt-1.5 flex flex-wrap gap-1.5">
      {#each brokers as b, i}
        <span
          class="inline-flex items-center gap-1 px-2 py-1 bg-purple-50 dark:bg-purple-950/40 border border-purple-200 dark:border-purple-700 rounded text-xs font-mono text-purple-700 dark:text-purple-300"
        >
          {b}
          <button
            type="button"
            class="flex items-center justify-center w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-purple-400 hover:text-vermilion-500 transition-colors"
            onclick={() => (brokers = brokers.filter((_, j) => j !== i))}
            title="Remove"
            aria-label="Remove broker {b}">&times;</button
          >
        </span>
      {/each}
    </div>
  {/if}
</div>
<div class="mb-3">
  <label for="target-kafka-topic" class={labelClass}>Topic</label>
  <input
    id="target-kafka-topic"
    type="text"
    bind:value={topic}
    placeholder="pika-events"
    class="{inputClass} font-mono"
  />
</div>
<div class="mb-3">
  <label
    class="flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-300 cursor-pointer"
  >
    <input type="checkbox" bind:checked={autoTopicCreation} class={checkboxClass} />
    Auto topic creation
  </label>
  <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500 pl-5">
    Automatically create the topic on first produce. Requires broker <code
      class={codeClass}>auto.create.topics.enable=true</code
    >.
  </p>
</div>
<div class="mb-3">
  <label for="target-kafka-key" class={labelClass}>Key Template (optional)</label>
  <input
    id="target-kafka-key"
    type="text"
    bind:value={keyTemplate}
    placeholder={"{{.Mount}}/{{.Path}}"}
    class="{inputClass} font-mono"
  />
  <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">
    Go template for the Kafka message key. Default: mount/path
  </p>
</div>

<!-- Security -->
<div
  class="p-3 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-md mb-3"
>
  <p
    class="text-[11px] font-semibold text-slate-500 dark:text-slate-400 uppercase tracking-wider mb-3"
  >
    Security
  </p>

  <!-- TLS -->
  <div class="mb-3">
    <label
      class="flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-300 cursor-pointer mb-2"
    >
      <input type="checkbox" bind:checked={tlsEnabled} class={checkboxClass} />
      Enable TLS
    </label>
    {#if tlsEnabled}
      <!-- Input mode toggle -->
      <div class="flex gap-1 mb-2 pl-5">
        <button
          type="button"
          class={modeBtn(tlsInputMode === "path")}
          aria-pressed={tlsInputMode === "path"}
          onclick={() => (tlsInputMode = "path")}>File Path</button
        >
        <button
          type="button"
          class={modeBtn(tlsInputMode === "pem")}
          aria-pressed={tlsInputMode === "pem"}
          onclick={() => (tlsInputMode = "pem")}>PEM / Reference</button
        >
      </div>

      {#if tlsInputMode === "path"}
        <div class="grid grid-cols-1 gap-2 pl-5">
          <div>
            <label for="kafka-tls-ca" class={labelSmClass}>CA File (optional)</label>
            <input
              id="kafka-tls-ca"
              type="text"
              bind:value={tlsCAFile}
              placeholder="/path/to/ca.pem"
              class="{inputSmClass} font-mono"
            />
          </div>
          <div>
            <label for="kafka-tls-cert" class={labelSmClass}
              >Client Cert File (optional)</label
            >
            <input
              id="kafka-tls-cert"
              type="text"
              bind:value={tlsCertFile}
              placeholder="/path/to/cert.pem"
              class="{inputSmClass} font-mono"
            />
          </div>
          <div>
            <label for="kafka-tls-key" class={labelSmClass}
              >Client Key File (optional)</label
            >
            <input
              id="kafka-tls-key"
              type="text"
              bind:value={tlsKeyFile}
              placeholder="/path/to/key.pem"
              class="{inputSmClass} font-mono"
            />
          </div>
        </div>
      {:else}
        <div class="grid grid-cols-1 gap-2 pl-5">
          <div>
            <label for="kafka-tls-ca-pem" class={labelSmClass}
              >CA Certificate (optional)</label
            >
            <textarea
              id="kafka-tls-ca-pem"
              bind:value={tlsCAPEM}
              rows={3}
              placeholder="Paste PEM content, or use raw://mount/path#/key or config://file#/path/to/key"
              class="{inputSmClass} font-mono resize-y"
            ></textarea>
          </div>
          <div>
            <label for="kafka-tls-cert-pem" class={labelSmClass}
              >Client Certificate (optional)</label
            >
            <textarea
              id="kafka-tls-cert-pem"
              bind:value={tlsCertPEM}
              rows={3}
              placeholder="Paste PEM content, or use raw://certs/client.pem"
              class="{inputSmClass} font-mono resize-y"
            ></textarea>
          </div>
          <div>
            <label for="kafka-tls-key-pem" class={labelSmClass}
              >Client Key (optional)</label
            >
            <textarea
              id="kafka-tls-key-pem"
              bind:value={tlsKeyPEM}
              rows={3}
              placeholder="Paste PEM content, or use config://tls/kafka-key#/pem"
              class="{inputSmClass} font-mono resize-y"
            ></textarea>
          </div>
          <p class="text-[10px] text-slate-400 dark:text-slate-500">
            Supports: inline PEM text, <code class={codeClass}>raw://mount/path</code>
            (from raw mounts), or
            <code class={codeClass}>config://file/key</code>
            (from config store). Structured JSON/YAML/TOML files can use JSON
            Pointer selectors like
            <code class={codeClass}>#/client/cert</code>.
          </p>
        </div>
      {/if}
    {/if}
  </div>

  <!-- SASL -->
  <div>
    <label for="kafka-sasl-type" class={labelSmClass}>SASL Authentication</label>
    <select id="kafka-sasl-type" bind:value={saslType} class="{inputSmClass} mb-2">
      <option value="none">None</option>
      <option value="plain">SASL/PLAIN</option>
      <option value="scram">SASL/SCRAM</option>
    </select>

    {#if saslType !== "none"}
      <div class="pl-5 space-y-2">
        {#if saslType === "scram"}
          <div>
            <label for="kafka-scram-algo" class={labelSmClass}>Algorithm</label>
            <select
              id="kafka-scram-algo"
              bind:value={saslAlgorithm}
              class={inputSmClass}
            >
              <option value="SCRAM-SHA-256">SCRAM-SHA-256</option>
              <option value="SCRAM-SHA-512">SCRAM-SHA-512</option>
            </select>
          </div>
        {/if}
        <div>
          <label for="kafka-sasl-user" class={labelSmClass}>Username</label>
          <input
            id="kafka-sasl-user"
            type="text"
            bind:value={saslUser}
            placeholder="kafka-user"
            class={inputSmClass}
          />
        </div>
        <div>
          <label for="kafka-sasl-pass" class={labelSmClass}>Password</label>
          <div class="flex gap-1">
            <input
              id="kafka-sasl-pass"
              type={showSaslPass ? "text" : "password"}
              bind:value={saslPass}
              placeholder="password"
              class="{inputSmClass} flex-1"
            />
            <button
              type="button"
              class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-slate-600 dark:hover:text-slate-300 border border-slate-200 dark:border-warm-700 rounded-md cursor-pointer"
              onclick={() => (showSaslPass = !showSaslPass)}
              title={showSaslPass ? "Hide" : "Show"}
              aria-label={showSaslPass ? "Hide password" : "Show password"}
            >
              {#if showSaslPass}<EyeOff size={12} />{:else}<Eye size={12} />{/if}
            </button>
          </div>
        </div>
        {#if saslType === "scram"}
          <label
            class="flex items-center gap-1.5 text-[11px] text-slate-500 dark:text-slate-400 cursor-pointer"
          >
            <input type="checkbox" bind:checked={saslIsToken} class={checkboxClass} />
            Delegation token (tokenauth=true)
          </label>
        {/if}
      </div>
    {/if}
  </div>
</div>
