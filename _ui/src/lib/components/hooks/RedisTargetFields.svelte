<script lang="ts">
  import { untrack } from "svelte";
  import type { HookTarget, RedisTLS } from "@/lib/types/config";
  import { addToast } from "@/lib/store/toast.svelte";
  import { inputClass } from "@/lib/ui";
  import { addBtnClass, checkboxClass, codeClass, labelClass } from "./shared";

  type Props = { initial?: HookTarget["redis"] };
  let { initial }: Props = $props();

  const init = untrack(() => initial);
  let cluster = $state((init?.addresses?.length ?? 0) > 0);
  let address = $state(init?.address || "");
  let addresses = $state<string[]>([...(init?.addresses || [])]);
  let addressInput = $state("");
  let password = $state(init?.password || "");
  let db = $state(init?.db || 0);
  let channel = $state(init?.channel ?? "");
  let tlsEnabled = $state(init?.tls?.enabled || false);
  let tlsCertFile = $state(init?.tls?.cert_file || "");
  let tlsKeyFile = $state(init?.tls?.key_file || "");
  let tlsCAFile = $state(init?.tls?.ca_file || "");

  function addAddress() {
    if (addressInput.trim()) {
      addresses = [...addresses, addressInput.trim()];
      addressInput = "";
    }
  }

  export function build(bodyTemplate: string | undefined): HookTarget | null {
    if (!channel.trim()) {
      addToast("Redis channel is required", "alert");
      return null;
    }
    if (cluster) {
      if (addresses.length === 0) {
        addToast("At least one cluster address is required", "alert");
        return null;
      }
    } else if (!address.trim()) {
      addToast("Redis address is required", "alert");
      return null;
    }
    const tls: RedisTLS | undefined = tlsEnabled
      ? {
          enabled: true,
          cert_file: tlsCertFile || undefined,
          key_file: tlsKeyFile || undefined,
          ca_file: tlsCAFile || undefined,
        }
      : undefined;
    return {
      type: "redis",
      redis: {
        address: cluster ? undefined : address.trim(),
        addresses: cluster ? [...addresses] : undefined,
        password: password || undefined,
        db: cluster ? undefined : db || undefined,
        channel: channel.trim(),
        tls,
      },
      body_template: bodyTemplate,
    };
  }
</script>

<div class="flex gap-4 mb-3">
  <label
    class="flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-300 cursor-pointer"
  >
    <input type="checkbox" bind:checked={cluster} class={checkboxClass} />
    Cluster mode
  </label>
  <label
    class="flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-300 cursor-pointer"
  >
    <input type="checkbox" bind:checked={tlsEnabled} class={checkboxClass} />
    Use TLS
  </label>
</div>

{#if tlsEnabled}
  <div class="grid grid-cols-3 gap-3 mb-3">
    <div>
      <label for="target-redis-tls-ca" class={labelClass}>CA File (optional)</label>
      <input
        id="target-redis-tls-ca"
        type="text"
        bind:value={tlsCAFile}
        placeholder="/path/to/ca.pem"
        class="{inputClass} font-mono"
      />
    </div>
    <div>
      <label for="target-redis-tls-cert" class={labelClass}
        >Cert File (optional)</label
      >
      <input
        id="target-redis-tls-cert"
        type="text"
        bind:value={tlsCertFile}
        placeholder="/path/to/cert.pem"
        class="{inputClass} font-mono"
      />
    </div>
    <div>
      <label for="target-redis-tls-key" class={labelClass}>Key File (optional)</label>
      <input
        id="target-redis-tls-key"
        type="text"
        bind:value={tlsKeyFile}
        placeholder="/path/to/key.pem"
        class="{inputClass} font-mono"
      />
    </div>
  </div>
  <p class="mb-3 text-[11px] text-slate-400 dark:text-slate-500">
    Paths support references: <code class={codeClass}>raw://mount/path</code>
    (from raw mounts) or
    <code class={codeClass}>config://key</code>
    (from config store), plus JSON Pointer selectors like
    <code class={codeClass}>#/key</code> for structured files.
  </p>
{/if}

{#if cluster}
  <div class="mb-3">
    <label for="target-redis-cluster-address" class={labelClass}
      >Cluster Addresses</label
    >
    <div class="flex flex-wrap gap-1.5 mb-2">
      {#each addresses as addr, i}
        <span
          class="flex items-center gap-1 px-2 py-0.5 bg-slate-100 dark:bg-warm-700 text-slate-600 dark:text-slate-300 rounded text-xs font-mono"
        >
          {addr}
          <button
            type="button"
            class="text-slate-400 dark:text-slate-500 hover:text-vermilion-500 cursor-pointer"
            aria-label="Remove address {addr}"
            onclick={() => (addresses = addresses.filter((_, j) => j !== i))}
            >&times;</button
          >
        </span>
      {/each}
    </div>
    <div class="flex gap-2">
      <input
        id="target-redis-cluster-address"
        type="text"
        bind:value={addressInput}
        placeholder="node:6379"
        class="{inputClass} flex-1 font-mono"
        onkeydown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            addAddress();
          }
        }}
      />
      <button type="button" class={addBtnClass} onclick={addAddress}>Add</button>
    </div>
  </div>
{:else}
  <div class="grid grid-cols-2 gap-3 mb-3">
    <div>
      <label for="target-redis-address" class={labelClass}>Address</label>
      <input
        id="target-redis-address"
        type="text"
        bind:value={address}
        placeholder="localhost:6379"
        class="{inputClass} font-mono"
      />
    </div>
    <div>
      <label for="target-redis-db" class={labelClass}>DB (optional)</label>
      <input
        id="target-redis-db"
        type="number"
        bind:value={db}
        min="0"
        max="15"
        class={inputClass}
      />
    </div>
  </div>
{/if}

<div class="grid grid-cols-2 gap-3 mb-3">
  <div>
    <label for="target-redis-channel" class={labelClass}>Channel</label>
    <input
      id="target-redis-channel"
      type="text"
      bind:value={channel}
      placeholder="pika-events"
      class="{inputClass} font-mono"
    />
  </div>
  <div>
    <label for="target-redis-password" class={labelClass}
      >Password (optional)</label
    >
    <input
      id="target-redis-password"
      type="password"
      bind:value={password}
      placeholder="Password"
      class={inputClass}
    />
  </div>
</div>
