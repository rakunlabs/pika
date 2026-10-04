<script lang="ts">
  import { onMount } from "svelte";
  import axios from "axios";
  import { RefreshCw, Clock } from "lucide-svelte";
  import { apiErrorMessage } from "@/lib/api/client";
  import { addToast } from "@/lib/store/toast.svelte";
  import { configStore } from "@/lib/store/config.svelte";
  import type {
    AuditEntry,
    AuditPage,
    AuditRetentionInfo,
  } from "@/lib/types/config";
  import {
    btnPrimary,
    btnSecondary,
    cardClass,
    inputClass,
    labelClass,
    selectClass,
  } from "@/lib/ui";

  const pageSize = 50;

  let entries = $state<AuditEntry[]>([]);
  let total = $state(0);
  let offset = $state(0);
  let loading = $state(false);
  let actor = $state("");

  // ── Retention ──
  type RetentionMode = "config" | "custom" | "forever";
  type RetentionUnit = "days" | "hours";

  let retentionInfo = $state<AuditRetentionInfo | null>(null);
  let mode = $state<RetentionMode>("config");
  let amount = $state(90);
  let unit = $state<RetentionUnit>("days");
  let savingRetention = $state(false);

  // goDurationHours converts a Go duration string ("2160h0m0s", "30m")
  // to hours. Returns 0 for "0s"/"0" and null when unparseable.
  function goDurationHours(d: string): number | null {
    if (d === "0" || d === "") return 0;
    const re = /(\d+(?:\.\d+)?)(h|m|s|ms|us|µs|ns)/g;
    const factor: Record<string, number> = {
      h: 1,
      m: 1 / 60,
      s: 1 / 3600,
      ms: 1 / 3_600_000,
      us: 1 / 3_600_000_000,
      µs: 1 / 3_600_000_000,
      ns: 1 / 3_600_000_000_000,
    };
    let hours = 0;
    let matched = "";
    for (const m of d.matchAll(re)) {
      hours += parseFloat(m[1]) * factor[m[2]];
      matched += m[0];
    }
    return matched === d ? hours : null;
  }

  function describeRetention(d: string): string {
    const h = goDurationHours(d);
    if (h === null) return d;
    if (h === 0) return "forever";
    if (h % 24 === 0) return `${h / 24} day${h === 24 ? "" : "s"}`;
    return `${+h.toFixed(2)} hour${h === 1 ? "" : "s"}`;
  }

  function applyDraftFrom(stored: string | undefined): void {
    if (!stored) {
      mode = "config";
      return;
    }
    const h = goDurationHours(stored);
    if (h === 0) {
      mode = "forever";
      return;
    }
    mode = "custom";
    if (h !== null && h % 24 === 0) {
      amount = h / 24;
      unit = "days";
    } else {
      amount = h ?? 1;
      unit = "hours";
    }
  }

  async function loadRetention(): Promise<void> {
    try {
      const res = await axios.get<AuditRetentionInfo>(
        "/api/v1/audit/retention",
      );
      retentionInfo = res.data;
    } catch (err) {
      addToast(apiErrorMessage(err, "Failed to load audit retention"), "alert");
    }
  }

  async function loadRetentionSettings(): Promise<void> {
    await configStore.loadSettings();
    applyDraftFrom(configStore.settings?.audit?.retention);
  }

  const draftValid = $derived(
    mode !== "custom" ||
      (Number.isFinite(amount) &&
        amount > 0 &&
        (unit === "days" ? amount * 24 : amount) >= 1),
  );

  async function saveRetention(): Promise<void> {
    if (!draftValid) return;
    let retention = "";
    if (mode === "forever") retention = "0";
    if (mode === "custom") {
      const hours = unit === "days" ? amount * 24 : amount;
      retention = `${Math.round(hours)}h`;
    }
    savingRetention = true;
    try {
      await configStore.saveAuditSettings({ retention });
      await loadRetention();
    } catch {
      // toast already shown by the store
    } finally {
      savingRetention = false;
    }
  }

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

  onMount(() => {
    void load();
    void loadRetention();
    void loadRetentionSettings();
  });
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

  <section class={cardClass}>
    <h3
      class="text-sm font-semibold mb-1 flex items-center gap-1.5 text-slate-800 dark:text-slate-100"
    >
      <Clock size={14} class="text-accent-600 dark:text-accent-400" />
      Retention
    </h3>
    <p class="text-xs text-slate-500 dark:text-slate-400 mb-3">
      Entries older than the retention window are pruned hourly by the
      cluster leader.
      {#if retentionInfo}
        Currently keeping entries
        <span class="font-medium text-slate-700 dark:text-slate-200">
          {describeRetention(retentionInfo.retention)}
        </span>
        ({retentionInfo.source === "settings"
          ? "set here"
          : "from config file"}).
      {/if}
    </p>

    <form
      class="space-y-3"
      onsubmit={(e) => {
        e.preventDefault();
        void saveRetention();
      }}
    >
      <div class="flex flex-wrap items-end gap-2">
        <div class="w-56">
          <label class={labelClass} for="audit-retention-mode">Keep entries</label>
          <select
            id="audit-retention-mode"
            class={selectClass}
            bind:value={mode}
          >
            <option value="config">
              Use config value{retentionInfo
                ? ` (${describeRetention(retentionInfo.config_retention)})`
                : ""}
            </option>
            <option value="custom">For a custom period</option>
            <option value="forever">Forever</option>
          </select>
        </div>

        {#if mode === "custom"}
          <div class="w-28">
            <label class={labelClass} for="audit-retention-amount">Period</label>
            <input
              id="audit-retention-amount"
              type="number"
              min="1"
              step="1"
              class={inputClass}
              bind:value={amount}
            />
          </div>
          <div class="w-28">
            <label class="sr-only" for="audit-retention-unit">Unit</label>
            <select
              id="audit-retention-unit"
              class={selectClass}
              bind:value={unit}
            >
              <option value="days">days</option>
              <option value="hours">hours</option>
            </select>
          </div>
        {/if}

        <button
          type="submit"
          class={btnPrimary}
          disabled={savingRetention || !draftValid}
        >
          {savingRetention ? "Saving…" : "Save"}
        </button>
      </div>

      {#if mode === "custom" && !draftValid}
        <p class="text-xs text-vermilion-600 dark:text-vermilion-400">
          Retention must be at least 1 hour.
        </p>
      {/if}
    </form>
  </section>

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
