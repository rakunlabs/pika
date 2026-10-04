<script lang="ts">
  import { onMount } from "svelte";
  import axios from "axios";
  import { RefreshCw } from "lucide-svelte";
  import { apiErrorMessage } from "@/lib/api/client";
  import { addToast } from "@/lib/store/toast.svelte";
  import type { AuditEntry, AuditPage } from "@/lib/types/config";
  import { btnSecondary, inputClass, labelClass } from "@/lib/ui";

  const pageSize = 50;

  let entries = $state<AuditEntry[]>([]);
  let total = $state(0);
  let offset = $state(0);
  let loading = $state(false);
  let actor = $state("");

  async function load(): Promise<void> {
    loading = true;
    try {
      const params = new URLSearchParams({
        _limit: String(pageSize),
        _offset: String(offset),
        _sort: "-time",
      });
      if (actor.trim()) params.set("actor", actor.trim());
      const res = await axios.get<AuditPage>(`/api/v1/audit?${params}`);
      entries = res.data.entries ?? [];
      total = res.data.total ?? 0;
    } catch (err) {
      addToast(apiErrorMessage(err, "Failed to load audit log"), "alert");
    } finally {
      loading = false;
    }
  }

  function search(): void {
    offset = 0;
    void load();
  }

  function page(delta: number): void {
    offset = Math.max(0, offset + delta * pageSize);
    void load();
  }

  function formatTime(t: string): string {
    return new Date(t).toLocaleString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  }

  function statusClass(status?: number): string {
    if (!status) return "text-slate-500 dark:text-slate-400";
    if (status >= 400) return "text-vermilion-600 dark:text-vermilion-400";
    return "text-emerald-600 dark:text-emerald-400";
  }

  onMount(load);
</script>

<div class="space-y-6">
  <div class="flex items-start justify-between gap-4">
    <div>
      <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">
        Audit Log
      </h2>
      <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
        State-changing requests and login attempts. Request bodies are never
        recorded.
      </p>
    </div>
    <button
      class={`${btnSecondary} inline-flex items-center gap-1.5`}
      onclick={() => void load()}
      disabled={loading}
    >
      <RefreshCw size={14} class={loading ? "animate-spin" : ""} />
      Refresh
    </button>
  </div>

  <form
    class="flex items-end gap-2"
    onsubmit={(e) => {
      e.preventDefault();
      search();
    }}
  >
    <div class="flex-1 max-w-xs">
      <label class={labelClass} for="audit-actor">Actor</label>
      <input
        id="audit-actor"
        class={inputClass}
        placeholder="user name or token:name"
        bind:value={actor}
      />
    </div>
    <button type="submit" class={btnSecondary}>Filter</button>
  </form>

  <div
    class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg overflow-x-auto"
  >
    <table class="w-full text-xs">
      <thead>
        <tr
          class="text-left text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400 border-b border-slate-200 dark:border-warm-700"
        >
          <th class="px-3 py-2 font-medium">Time</th>
          <th class="px-3 py-2 font-medium">Actor</th>
          <th class="px-3 py-2 font-medium">Action</th>
          <th class="px-3 py-2 font-medium">Target</th>
          <th class="px-3 py-2 font-medium">Status</th>
          <th class="px-3 py-2 font-medium">IP</th>
        </tr>
      </thead>
      <tbody>
        {#each entries as e (e.id)}
          <tr
            class="border-b last:border-b-0 border-slate-100 dark:border-warm-700 text-slate-700 dark:text-slate-200"
          >
            <td class="px-3 py-2 whitespace-nowrap">{formatTime(e.time)}</td>
            <td class="px-3 py-2 whitespace-nowrap">{e.actor || "—"}</td>
            <td class="px-3 py-2 font-mono whitespace-nowrap">{e.action}</td>
            <td class="px-3 py-2 font-mono break-all">{e.target || ""}</td>
            <td class={`px-3 py-2 font-mono ${statusClass(e.status)}`}>
              {e.status || ""}
            </td>
            <td class="px-3 py-2 font-mono whitespace-nowrap">{e.ip || ""}</td>
          </tr>
        {:else}
          <tr>
            <td
              colspan="6"
              class="px-3 py-8 text-center text-slate-500 dark:text-slate-400"
            >
              {loading ? "Loading…" : "No audit entries."}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>

  {#if total > pageSize}
    <div
      class="flex items-center justify-between text-xs text-slate-500 dark:text-slate-400"
    >
      <span>
        {offset + 1}–{Math.min(offset + pageSize, total)} of {total}
      </span>
      <div class="flex gap-2">
        <button
          class={btnSecondary}
          disabled={offset === 0 || loading}
          onclick={() => page(-1)}>Previous</button
        >
        <button
          class={btnSecondary}
          disabled={offset + pageSize >= total || loading}
          onclick={() => page(1)}>Next</button
        >
      </div>
    </div>
  {/if}
</div>
