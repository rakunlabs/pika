<script lang="ts">
  import { onMount } from "svelte";
  import axios from "axios";
  import {
    AlertTriangle,
    Network,
    RefreshCw,
    Server,
    ShieldCheck,
    Wifi,
  } from "lucide-svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import type { ClusterNode, ClusterStatus } from "@/lib/types/config";

  type GraphNode = ClusterNode & { x: number; y: number };

  let status = $state<ClusterStatus | null>(null);
  let loading = $state(false);
  let lastUpdated = $state("");

  async function refreshStatus() {
    loading = true;
    try {
      const { data } = await axios.get<ClusterStatus>("/api/v1/cluster/status");
      status = data;
      lastUpdated = new Date().toLocaleTimeString();
    } catch (error: any) {
      const msg =
        error.response?.data?.message ||
        error.response?.statusText ||
        "Failed to read cluster status";
      addToast(msg, "alert");
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    refreshStatus();
  });

  const statusTone = $derived.by(() => {
    if (!status) return "loading";
    if (!status.enabled) return "disabled";
    if (!status.has_quorum || status.role === "leader_unhealthy")
      return "warning";
    return "healthy";
  });

  const graphNodes = $derived.by(() => layoutNodes(status?.nodes ?? []));
  const selfNode = $derived(graphNodes.find((n) => n.self) ?? graphNodes[0]);
  const peerNodes = $derived(graphNodes.filter((n) => !n.self));
  const nodesLabel = $derived.by(() => {
    if (!status) return "N/A";
    if (status.expected_replicas > 0)
      return `${status.online_nodes}/${status.expected_replicas}`;
    return String(status.online_nodes);
  });

  function layoutNodes(nodes: ClusterNode[]): GraphNode[] {
    if (nodes.length === 0) return [];

    const self = nodes.find((n) => n.self) ?? nodes[0];
    const peers = nodes.filter((n) => n.id !== self.id);
    const out: GraphNode[] = [{ ...self, x: 50, y: 50 }];
    const radius = 34;

    peers.forEach((node, index) => {
      const angle =
        -Math.PI / 2 + (2 * Math.PI * index) / Math.max(peers.length, 1);
      out.push({
        ...node,
        x: 50 + Math.cos(angle) * radius,
        y: 50 + Math.sin(angle) * radius,
      });
    });

    return out;
  }

  function valueOrDash(value?: string | number | null): string {
    if (value === undefined || value === null || value === "") return "N/A";
    return String(value);
  }

  function roleLabel(role?: string): string {
    switch (role) {
      case "leader":
        return "Leader";
      case "leader_unhealthy":
        return "Leader, no quorum";
      case "follower":
        return "Follower";
      case "standalone":
        return "Standalone";
      default:
        return "Peer";
    }
  }

  function statusTitle(): string {
    if (!status) return "Loading cluster status";
    if (!status.enabled) return "Cluster is disabled";
    if (!status.has_quorum) return "Cluster quorum is missing";
    if (status.role === "leader_unhealthy") return "Leader is unhealthy";
    return `Cluster is connected as ${roleLabel(status.role).toLowerCase()}`;
  }

  function statusDescription(): string {
    if (!status) return "Reading the current alan/bw runtime view.";
    if (!status.enabled)
      return "This deployment is running in single-node mode. Writes are handled locally and no peers are expected.";
    if (!status.has_quorum)
      return "This node cannot see enough peers to satisfy the configured replica quorum. Writes may fail until peers reconnect.";
    return `${status.online_nodes} node${status.online_nodes === 1 ? "" : "s"} online. Local DB version is ${status.version}.`;
  }

  function calloutClass(): string {
    if (statusTone === "healthy")
      return "bg-emerald-50 dark:bg-emerald-950/30 border-emerald-300 dark:border-emerald-700 text-emerald-900 dark:text-emerald-200";
    if (statusTone === "warning")
      return "bg-amber-50 dark:bg-amber-950/40 border-amber-300 dark:border-amber-700 text-amber-900 dark:text-amber-200";
    return "bg-slate-50 dark:bg-warm-900 border-slate-200 dark:border-warm-700 text-slate-700 dark:text-slate-200";
  }

  function iconClass(): string {
    if (statusTone === "healthy")
      return "text-emerald-700 dark:text-emerald-300";
    if (statusTone === "warning") return "text-amber-700 dark:text-amber-300";
    return "text-slate-500 dark:text-slate-400";
  }

  function nodeClass(node: GraphNode): string {
    if (node.leader)
      return "bg-accent-50 dark:bg-accent-900/40 border-accent-200 dark:border-accent-700 text-accent-800 dark:text-accent-200";
    if (node.self)
      return "bg-white dark:bg-warm-800 border-slate-300 dark:border-warm-600 text-slate-800 dark:text-slate-100";
    return "bg-white dark:bg-warm-800 border-slate-200 dark:border-warm-700 text-slate-700 dark:text-slate-200";
  }

  function pillClass(node: ClusterNode): string {
    if (node.leader)
      return "bg-accent-50 dark:bg-accent-900/40 text-accent-700 dark:text-accent-300 border-accent-200 dark:border-accent-700";
    if (node.self)
      return "bg-slate-100 dark:bg-warm-700 text-slate-700 dark:text-slate-200 border-slate-200 dark:border-warm-600";
    return "bg-slate-50 dark:bg-warm-900 text-slate-500 dark:text-slate-400 border-slate-200 dark:border-warm-700";
  }
</script>

<div class="space-y-6">
  <div class="flex items-start justify-between gap-4">
    <div>
      <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">
        Cluster
      </h2>
      <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
        Current alan peer connection, leader state and visible nodes for this
        pika instance.
      </p>
    </div>
    <button
      type="button"
      class="px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed inline-flex items-center gap-1.5"
      onclick={refreshStatus}
      disabled={loading}
    >
      <RefreshCw size={14} class={loading ? "animate-spin" : ""} />
      Refresh
    </button>
  </div>

  <div class={`p-4 rounded-lg border flex items-start gap-3 ${calloutClass()}`}>
    {#if statusTone === "healthy"}
      <ShieldCheck size={17} class={`shrink-0 mt-0.5 ${iconClass()}`} />
    {:else if statusTone === "warning"}
      <AlertTriangle size={17} class={`shrink-0 mt-0.5 ${iconClass()}`} />
    {:else}
      <Network size={17} class={`shrink-0 mt-0.5 ${iconClass()}`} />
    {/if}
    <div>
      <p class="text-sm font-medium m-0">{statusTitle()}</p>
      <p class="text-xs mt-0.5 m-0">{statusDescription()}</p>
    </div>
  </div>

  {#if status}
    <div class="grid gap-3 sm:grid-cols-4">
      <div
        class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900"
      >
        <p
          class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400"
        >
          Role
        </p>
        <p
          class="mt-1 text-sm font-semibold text-slate-800 dark:text-slate-100"
        >
          {roleLabel(status.role)}
        </p>
      </div>
      <div
        class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900"
      >
        <p
          class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400"
        >
          Nodes
        </p>
        <p
          class="mt-1 text-sm font-semibold text-slate-800 dark:text-slate-100"
        >
          {nodesLabel}
        </p>
      </div>
      <div
        class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900"
      >
        <p
          class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400"
        >
          Quorum
        </p>
        <p
          class={status.has_quorum
            ? "mt-1 text-sm font-semibold text-emerald-700 dark:text-emerald-300"
            : "mt-1 text-sm font-semibold text-amber-700 dark:text-amber-300"}
        >
          {status.has_quorum ? "Healthy" : "Missing"}
        </p>
      </div>
      <div
        class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900"
      >
        <p
          class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400"
        >
          DB version
        </p>
        <p
          class="mt-1 text-sm font-semibold text-slate-800 dark:text-slate-100 font-mono"
        >
          {status.version}
        </p>
      </div>
    </div>

    <div class="grid gap-4 lg:grid-cols-2">
      <section
        class="p-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm"
      >
        <div class="flex items-center gap-2 mb-4">
          <Wifi size={15} class="text-accent-600 dark:text-accent-400" />
          <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-100">
            Connection
          </h3>
        </div>

        <dl class="divide-y divide-slate-100 dark:divide-warm-700">
          <div class="flex items-baseline gap-4 py-2 first:pt-0">
            <dt
              class="w-28 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
            >
              Local
            </dt>
            <dd
              class="flex-1 text-sm text-slate-800 dark:text-slate-100 font-mono break-all"
            >
              {valueOrDash(status.local_addr)}
            </dd>
          </div>
          <div class="flex items-baseline gap-4 py-2">
            <dt
              class="w-28 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
            >
              Leader
            </dt>
            <dd
              class="flex-1 text-sm text-slate-800 dark:text-slate-100 font-mono break-all"
            >
              {status.is_leader ? "this node" : valueOrDash(status.leader_addr)}
            </dd>
          </div>
          <div class="flex items-baseline gap-4 py-2">
            <dt
              class="w-28 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
            >
              DNS
            </dt>
            <dd
              class="flex-1 text-sm text-slate-800 dark:text-slate-100 font-mono break-all"
            >
              {valueOrDash(status.config.dns_addr)}
            </dd>
          </div>
          <div class="flex items-baseline gap-4 py-2">
            <dt
              class="w-28 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
            >
              Bind
            </dt>
            <dd
              class="flex-1 text-sm text-slate-800 dark:text-slate-100 font-mono break-all"
            >
              {valueOrDash(status.config.bind_addr)}:{valueOrDash(
                status.config.port,
              )}
            </dd>
          </div>
          <div class="flex items-baseline gap-4 py-2">
            <dt
              class="w-28 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
            >
              Security
            </dt>
            <dd class="flex-1 text-sm text-slate-800 dark:text-slate-100">
              {status.config.security_enabled
                ? "Pre-shared key enabled"
                : "No pre-shared key"}
            </dd>
          </div>
          <div class="flex items-baseline gap-4 py-2">
            <dt
              class="w-28 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
            >
              Lock key
            </dt>
            <dd
              class="flex-1 text-sm text-slate-800 dark:text-slate-100 font-mono break-all"
            >
              {status.config.lock_key}
            </dd>
          </div>
          <div class="flex items-baseline gap-4 py-2 last:pb-0">
            <dt
              class="w-28 shrink-0 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
            >
              Sync
            </dt>
            <dd class="flex-1 text-sm text-slate-800 dark:text-slate-100">
              Every {valueOrDash(status.config.sync_interval)}
            </dd>
          </div>
        </dl>
      </section>

      <section
        class="p-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm"
      >
        <div class="flex items-center justify-between gap-3 mb-4">
          <div class="flex items-center gap-2">
            <Server size={15} class="text-accent-600 dark:text-accent-400" />
            <h3
              class="text-sm font-semibold text-slate-800 dark:text-slate-100"
            >
              Connected nodes
            </h3>
          </div>
          {#if lastUpdated}
            <span class="text-[11px] text-slate-400 dark:text-slate-500"
              >Updated {lastUpdated}</span
            >
          {/if}
        </div>

        <div
          class="relative h-64 overflow-hidden rounded-lg border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900"
        >
          <svg
            class="absolute inset-0 w-full h-full text-slate-300 dark:text-warm-600"
            viewBox="0 0 100 100"
            preserveAspectRatio="none"
            aria-hidden="true"
          >
            {#if selfNode}
              {#each peerNodes as node}
                <line
                  x1={selfNode.x}
                  y1={selfNode.y}
                  x2={node.x}
                  y2={node.y}
                  stroke="currentColor"
                  stroke-width="0.7"
                />
              {/each}
            {/if}
          </svg>

          {#each graphNodes as node}
            <div
              class={`absolute w-28 -translate-x-1/2 -translate-y-1/2 rounded-lg border px-2.5 py-2 text-center shadow-sm ${nodeClass(node)}`}
              style={`left: ${node.x}%; top: ${node.y}%;`}
            >
              <p class="text-xs font-semibold truncate">{node.label}</p>
              <p class="mt-0.5 text-[10px] uppercase tracking-wider opacity-75">
                {roleLabel(node.role)}
              </p>
              <p class="mt-1 text-[10px] font-mono truncate opacity-80">
                {valueOrDash(node.address)}
              </p>
            </div>
          {/each}
        </div>

        <div class="mt-3 space-y-2">
          {#each status.nodes as node}
            <div
              class="flex items-center justify-between gap-3 p-2 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900"
            >
              <div class="min-w-0">
                <p
                  class="text-xs font-medium text-slate-800 dark:text-slate-100"
                >
                  {node.label}
                </p>
                <p
                  class="text-[11px] text-slate-500 dark:text-slate-400 font-mono truncate"
                >
                  {valueOrDash(node.address)}
                </p>
              </div>
              <span
                class={`shrink-0 px-2 py-0.5 rounded-full border text-[11px] ${pillClass(node)}`}
              >
                {node.self ? "this node" : roleLabel(node.role)}
              </span>
            </div>
          {/each}
        </div>
      </section>
    </div>
  {:else}
    <div
      class="p-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm text-sm text-slate-500 dark:text-slate-400"
    >
      Loading cluster status...
    </div>
  {/if}
</div>
