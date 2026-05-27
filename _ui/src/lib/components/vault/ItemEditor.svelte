<script lang="ts">
  import {
    Star,
    Archive,
    Trash2,
    Save,
    Plus,
    Eye,
    EyeOff,
    Copy,
    Check,
    GripVertical,
    X,
    Dices,
    RotateCcw,
    KeyRound,
    CreditCard,
    UserSquare2,
    FileText,
    Terminal,
    Plug,
    Database,
    Server,
    FileBadge,
    ShieldCheck,
    Folder as FolderIcon,
    Pencil,
    ExternalLink,
    History,
  } from "lucide-svelte";
  import { vaultStore } from "@/lib/vault/store.svelte";
  import {
    newCustomField,
    extractHostnames,
    typeLabel,
    vaultItemAccent,
  } from "@/lib/vault/templates";
  import * as api from "@/lib/vault/api";
  import type {
    VaultItem,
    VaultItemType,
    VaultItemVersion,
  } from "@/lib/vault/api";
  import type { VaultItemField, VaultItemPayload } from "@/lib/vault/crypto";
  import { renderMarkdown } from "@/lib/vault/markdown";
  import { addToast } from "@/lib/store/toast.svelte";
  import { backdropClose } from "@/lib/actions/backdropClose";
  import TOTPDisplay from "./TOTPDisplay.svelte";
  import PasswordGenerator from "./PasswordGenerator.svelte";

  interface Props {
    item: VaultItem;
    /** Decrypted title plaintext (null when the AEAD failed). */
    title: string | null;
    /** Decrypted tag array (empty when absent or AEAD failed). */
    tagsCleartext: string[];
    /** Decrypted hostname array. */
    hostnamesCleartext: string[];
    /** Decrypted folder label. Empty = no folder. Null = AEAD failed. */
    folderCleartext: string | null;
    payload: VaultItemPayload | null;
    onClose: () => void;
  }
  let {
    item,
    title: initialTitle,
    tagsCleartext,
    hostnamesCleartext,
    folderCleartext,
    payload,
    onClose,
  }: Props = $props();

  // ─── View / Edit mode ──────────────────────────────────────────
  //
  // The editor has two distinct modes. View is the default for
  // existing items so the common path (open, glance, copy, close)
  // doesn't put every text field into editable affordance. The
  // single exception is a freshly-decrypted item that's totally
  // empty (no fields, no notes) — that's almost always something
  // the user just created and wants to fill in, so we open it
  // directly in edit mode.
  //
  // The mode flips back to view automatically after a successful
  // save, mirroring 1Password.
  /* eslint-disable svelte/no-reactive-reassign */
  function emptyPayload(p: VaultItemPayload | null): boolean {
    if (!p) return false;
    const fieldsEmpty =
      !p.fields ||
      p.fields.length === 0 ||
      p.fields.every((f) => !f.value && !f.label);
    return fieldsEmpty && !p.notes;
  }
  // svelte-ignore state_referenced_locally
  let mode = $state<"view" | "edit">(
    emptyPayload(payload) && !item.deleted_at ? "edit" : "view",
  );

  // Working state — kept in sync with props through the {#key} wrap
  // in Vault.svelte. Edits are local until Save commits them.
  // svelte-ignore state_referenced_locally
  let title = $state(initialTitle ?? "");
  // svelte-ignore state_referenced_locally
  let tags = $state<string[]>([...tagsCleartext]);
  let tagDraft = $state("");
  // svelte-ignore state_referenced_locally
  let folder = $state(folderCleartext ?? "");
  // svelte-ignore state_referenced_locally
  let favorite = $state(item.favorite ?? false);
  // svelte-ignore state_referenced_locally
  let fields = $state<VaultItemField[]>(
    payload ? deepCopyFields(payload.fields) : [],
  );
  // svelte-ignore state_referenced_locally
  let notes = $state(payload?.notes ?? "");
  let busy = $state(false);
  let unmasked = $state<Set<string>>(new Set()); // field ids whose value is shown
  let generatorFor = $state<string | null>(null);
  let copiedField = $state<string | null>(null);

  // Track the original hostnames so we don't re-encrypt URL state
  // on saves that didn't touch the URL fields.
  // svelte-ignore state_referenced_locally
  const originalHostnames = [...hostnamesCleartext];

  const dirty = $derived(
    title !== (initialTitle ?? "") ||
      JSON.stringify(tags) !== JSON.stringify(tagsCleartext) ||
      folder.trim() !== (folderCleartext ?? "").trim() ||
      favorite !== (item.favorite ?? false) ||
      JSON.stringify(fields) !== JSON.stringify(payload?.fields ?? []) ||
      (notes ?? "") !== (payload?.notes ?? ""),
  );

  const folderSuggestions = $derived(vaultStore.allFolders());

  function deepCopyFields(f: VaultItemField[]): VaultItemField[] {
    return f.map((x) => ({ ...x }));
  }

  // Per-type icon for the hero header.
  function typeIcon(type: VaultItemType) {
    switch (type) {
      case "login":
        return KeyRound;
      case "card":
        return CreditCard;
      case "identity":
        return UserSquare2;
      case "secure_note":
        return FileText;
      case "ssh_key":
        return Terminal;
      case "api_credential":
        return Plug;
      case "database":
        return Database;
      case "server":
        return Server;
      case "license":
        return FileBadge;
      case "tls_cert":
        return ShieldCheck;
      default:
        return FileText;
    }
  }
  const HeaderIcon = $derived(typeIcon(item.type as VaultItemType));
  // Per-type accent palette (DESIGN_SYSTEM.md §11). Drives the
  // hero tile color, the left-edge stripe, and the timeline dot
  // in the history panel — so every visual that says "what kind of
  // item is this" speaks the same language.
  const accent = $derived(vaultItemAccent(item.type as VaultItemType));

  // ─── Field helpers ─────────────────────────────────────────────

  function fieldTypeLabel(t: string): string {
    // Spelled out for the read-mode field card header. Mirrors the
    // <select> options in edit mode.
    switch (t) {
      case "password":
        return "Password";
      case "email":
        return "Email";
      case "phone":
        return "Phone";
      case "url":
        return "URL";
      case "username":
        return "Username";
      case "totp":
        return "One-time code";
      case "date":
        return "Date";
      case "month_year":
        return "MM/YY";
      case "cvv":
        return "CVV";
      case "card_number":
        return "Card number";
      case "pin":
        return "PIN";
      case "address":
        return "Address";
      case "ssh_private_key":
        return "SSH private key";
      case "ssh_public_key":
        return "SSH public key";
      case "api_key":
        return "API key";
      case "secret_token":
        return "Secret token";
      case "hostname":
        return "Hostname";
      case "port":
        return "Port";
      case "connection_string":
        return "Connection string";
      case "text":
      default:
        return "Text";
    }
  }

  // Tipi multiline ister mi (textarea olarak gösterilsin)?
  function isMultilineFieldType(t: string): boolean {
    return (
      t === "ssh_private_key" ||
      t === "ssh_public_key" ||
      t === "connection_string" ||
      t === "address"
    );
  }

  // URL field için clickable bir host gösterimi.
  function safeHref(value: string): string | null {
    try {
      const u = new URL(value.trim());
      if (u.protocol === "http:" || u.protocol === "https:") return u.href;
      return null;
    } catch {
      return null;
    }
  }

  function addTag() {
    const t = tagDraft.trim();
    if (!t) return;
    if (!tags.includes(t)) tags = [...tags, t];
    tagDraft = "";
  }
  function removeTag(t: string) {
    tags = tags.filter((x) => x !== t);
  }
  function toggleMask(id: string) {
    if (unmasked.has(id)) unmasked.delete(id);
    else unmasked.add(id);
    unmasked = new Set(unmasked);
  }
  async function copyValue(id: string, value: string) {
    if (!value) return;
    try {
      await navigator.clipboard.writeText(value);
      copiedField = id;
      setTimeout(() => {
        copiedField = null;
      }, 1500);
    } catch {
      addToast("Clipboard unavailable", "warn");
    }
  }
  function addField() {
    fields = [...fields, newCustomField()];
  }
  function removeField(id: string) {
    fields = fields.filter((f) => f.id !== id);
  }
  function moveField(id: string, delta: number) {
    const idx = fields.findIndex((f) => f.id === id);
    if (idx < 0) return;
    const target = idx + delta;
    if (target < 0 || target >= fields.length) return;
    const copy = [...fields];
    [copy[idx], copy[target]] = [copy[target], copy[idx]];
    fields = copy;
  }
  function applyGenerator(value: string) {
    if (!generatorFor) return;
    fields = fields.map((f) => (f.id === generatorFor ? { ...f, value } : f));
    generatorFor = null;
  }

  // ─── Save / mutation ───────────────────────────────────────────

  async function save() {
    if (busy) return;
    if (!title.trim()) {
      addToast("Title is required", "alert");
      return;
    }
    busy = true;
    try {
      const newPayload: VaultItemPayload = {
        fields,
        notes: notes || undefined,
      };
      const hostnames = extractHostnames(newPayload);
      const hostnamesChanged =
        JSON.stringify(hostnames) !== JSON.stringify(originalHostnames);
      const folderChanged = folder.trim() !== (folderCleartext ?? "").trim();
      await vaultStore.updateItem(
        item.id,
        { expected_version: item.version },
        {
          title: title.trim(),
          tags,
          urlHostnames: hostnamesChanged ? hostnames : undefined,
          folder: folderChanged ? folder.trim() : undefined,
          favorite,
          payload: newPayload,
        },
      );
      addToast("Saved", "success", 1500);
      // Invalidate the version cache so the next history open
      // reflects the freshly created snapshot.
      versionsCache = null;
      mode = "view";
    } catch (e: any) {
      const status = e?.response?.status;
      if (status === 409) {
        addToast(
          "This item was changed elsewhere. Reload to see the latest version.",
          "alert",
        );
      } else {
        addToast(
          e?.response?.data?.message ?? e?.message ?? "Save failed",
          "alert",
        );
      }
    } finally {
      busy = false;
    }
  }

  function cancelEdit() {
    title = initialTitle ?? "";
    tags = [...tagsCleartext];
    tagDraft = "";
    folder = folderCleartext ?? "";
    favorite = item.favorite ?? false;
    fields = payload ? deepCopyFields(payload.fields) : [];
    notes = payload?.notes ?? "";
    mode = "view";
  }

  async function trash() {
    busy = true;
    try {
      await vaultStore.softDeleteItem(item.id);
      addToast("Moved to trash", "success", 1500);
      onClose();
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? "Delete failed", "alert");
    } finally {
      busy = false;
    }
  }
  async function purge() {
    if (!confirm("Permanently delete this item? This cannot be undone."))
      return;
    busy = true;
    try {
      await vaultStore.purgeItem(item.id);
      addToast("Deleted permanently", "success", 1500);
      onClose();
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? "Delete failed", "alert");
    } finally {
      busy = false;
    }
  }
  async function restore() {
    busy = true;
    try {
      await vaultStore.restoreItem(item.id);
      addToast("Restored", "success", 1500);
      onClose();
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? "Restore failed", "alert");
    } finally {
      busy = false;
    }
  }
  async function toggleArchive() {
    busy = true;
    try {
      await vaultStore.updateItem(
        item.id,
        { expected_version: item.version },
        { archived: !item.archived },
      );
      addToast(item.archived ? "Unarchived" : "Archived", "success", 1500);
      onClose();
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? "Update failed", "alert");
    } finally {
      busy = false;
    }
  }

  // Persist the favorite toggle from the read-mode header without
  // entering edit mode. The local `favorite` flag has already been
  // flipped by the click handler (optimistic UI); we revert it on
  // failure so the display matches the server.
  async function favoriteFlip() {
    try {
      await vaultStore.updateItem(
        item.id,
        { expected_version: item.version },
        { favorite },
      );
    } catch (e: any) {
      favorite = item.favorite ?? false;
      addToast(e?.response?.data?.message ?? "Failed to update", "alert");
    }
  }

  // ─── Field history ─────────────────────────────────────────────
  //
  // The server keeps an append-only snapshot of every prior version
  // of an item (vault_item_versions). When the user opens the
  // history flyout on a field, we fetch the version list once and
  // decrypt the payload of each snapshot client-side. We cache the
  // decrypted history list per editor mount so toggling the panel
  // doesn't re-fetch.
  //
  // A history entry surfaces, per field, its label + value as of
  // that snapshot. Fields that didn't exist yet (added later) are
  // simply absent from the entry — they don't show a "removed" row
  // in this version.
  //
  // The history payload is decrypted with the live vault key. If
  // the user has rotated keys in the past, older snapshots may
  // fail to decrypt; we render them as "(unreadable)" so the
  // timeline still shows the user that activity happened.

  interface HistoryEntry {
    version: number;
    updatedAt: string;
    author?: string;
    fieldsById: Map<string, { label: string; value: string; type: string }>;
    decryptOk: boolean;
  }
  let versionsCache = $state<HistoryEntry[] | null>(null);
  let versionsLoading = $state(false);

  // Item-level history panel state. The panel sits at the bottom
  // of the read-mode body and shows EVERY snapshot with its
  // changed fields (compared to the snapshot immediately newer
  // than it in the timeline — i.e. "what got changed AT this
  // version"). Toggled from the header's History button.
  let historyOpen = $state(false);
  // Per-snapshot sensitive-value reveal state. Default is masked
  // for any field that's currently flagged sensitive on the live
  // item. Key is `${version}:${fieldId}` so each snapshot tracks
  // its own reveal state independently.
  let historyRevealed = $state<Set<string>>(new Set());
  let copiedHistKey = $state<string | null>(null);

  async function toggleHistoryPanel(): Promise<void> {
    if (historyOpen) {
      historyOpen = false;
      return;
    }
    historyOpen = true;
    await loadVersions();
  }

  function toggleHistoryReveal(key: string): void {
    const next = new Set(historyRevealed);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    historyRevealed = next;
  }

  async function copyHistoryValue(key: string, value: string): Promise<void> {
    if (!value) return;
    try {
      await navigator.clipboard.writeText(value);
      copiedHistKey = key;
      setTimeout(() => {
        copiedHistKey = null;
      }, 1500);
    } catch {
      addToast("Clipboard unavailable", "warn");
    }
  }

  async function loadVersions(): Promise<void> {
    if (versionsCache || versionsLoading) return;
    versionsLoading = true;
    try {
      const raw: VaultItemVersion[] = await api.listItemVersions(item.id);
      // The endpoint returns newest first; we keep that order so
      // the rendered history reads "most recent change at top".
      const out: HistoryEntry[] = [];
      for (const v of raw) {
        // decryptPayloadBytes encapsulates the vault key access —
        // we never see the key in this component, which matches
        // the store's principle that key material stays internal.
        const p = await vaultStore.decryptPayloadBytes(v.encrypted_payload);
        const fieldsById = new Map<
          string,
          { label: string; value: string; type: string }
        >();
        if (p?.fields) {
          for (const f of p.fields) {
            fieldsById.set(f.id, {
              label: f.label ?? "",
              value: f.value ?? "",
              type: f.type ?? "text",
            });
          }
        }
        out.push({
          version: v.version,
          updatedAt: v.updated_at,
          author: v.author,
          fieldsById,
          decryptOk: p !== null,
        });
      }
      versionsCache = out;
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? "Failed to load history", "alert");
    } finally {
      versionsLoading = false;
    }
  }

  // Item-level diff timeline. Each rendered row represents one
  // snapshot AND lists the fields that changed AT that version
  // relative to the NEXT-NEWER snapshot in the timeline (or, for
  // the most recent snapshot, relative to the live item).
  //
  // We compute this once per cache load. The output is ordered
  // newest-first; for each entry we surface:
  //   - timestamp, version, author
  //   - changed field rows: { fieldId, label, type, sensitive,
  //                            beforeValue, afterValue }
  // where "after" is what this snapshot had and "before" is what
  // the previous (older) snapshot had. Title changes (currently
  // not snapshotted at field granularity; only encrypted_title +
  // encrypted_payload are stored) bubble up via a synthetic
  // "title" pseudo-field when the snapshot's payload contains a
  // distinct title; in practice the API returns title separately
  // (see VaultItemVersion.encrypted_title), but the SPA currently
  // shows payload diffs only — keeping the scope tight.
  //
  // Snapshots whose payload failed to decrypt (e.g. old key after
  // a master-password rotation) render as an "(unreadable)" row
  // so users still see THAT the change happened.

  interface DiffRow {
    fieldId: string;
    label: string;
    type: string;
    sensitive: boolean;
    /** Value AT this snapshot (i.e. what the user changed TO). */
    after: string;
    /** Value BEFORE this snapshot (in the older snapshot). */
    before: string;
    kind: "added" | "removed" | "changed";
  }
  interface TimelineEntry {
    version: number;
    updatedAt: string;
    author?: string;
    decryptOk: boolean;
    rows: DiffRow[];
    /** Number of payload-field diffs (added + removed + changed). */
    changeCount: number;
  }

  // A field is "sensitive" in this timeline if the LIVE item flags
  // it sensitive — we don't trust the snapshot's own flag because
  // a previous version may have had `sensitive: false` for a
  // field that's now flagged sensitive (and vice versa). Defaulting
  // to "live flag wins" matches the principle of "the current
  // owner decides what's sensitive on display."
  function isLiveSensitive(fieldId: string, fallbackType: string): boolean {
    const live = fields.find((f) => f.id === fieldId);
    if (live) return !!live.sensitive;
    // Field no longer exists on the live item — fall back to a
    // type-based default so password / secret_token / api_key
    // history is still masked.
    return (
      fallbackType === "password" ||
      fallbackType === "secret_token" ||
      fallbackType === "api_key" ||
      fallbackType === "ssh_private_key" ||
      fallbackType === "cvv" ||
      fallbackType === "pin"
    );
  }

  const timeline = $derived.by<TimelineEntry[]>(() => {
    if (!versionsCache) return [];
    const out: TimelineEntry[] = [];
    // versionsCache is newest-first from the API.
    // For each snapshot we want to diff against the NEXT-OLDER
    // snapshot in the array. The newest snapshot in the cache is
    // *not* the live item — it's the most recent ARCHIVED row, so
    // we diff it against the live item separately at the top of
    // the loop.
    const liveFields = new Map<
      string,
      { label: string; value: string; type: string }
    >();
    for (const f of fields) {
      liveFields.set(f.id, {
        label: f.label ?? "",
        value: f.value ?? "",
        type: f.type ?? "text",
      });
    }

    // Walk newest → oldest. For index i, the "after" is
    // versionsCache[i].fieldsById, the "before" is
    // versionsCache[i+1].fieldsById, or the live fields if i==-1.
    // We emit one entry per snapshot. The "live diff" (live vs
    // newest snapshot) is added at the very top as a synthetic
    // entry whose version is the live item.version.
    const buildDiff = (
      after: Map<string, { label: string; value: string; type: string }>,
      before: Map<string, { label: string; value: string; type: string }>,
    ): DiffRow[] => {
      const rows: DiffRow[] = [];
      const seen = new Set<string>();
      for (const [id, a] of after) {
        seen.add(id);
        const b = before.get(id);
        if (!b) {
          rows.push({
            fieldId: id,
            label: a.label || a.type,
            type: a.type,
            sensitive: isLiveSensitive(id, a.type),
            after: a.value,
            before: "",
            kind: "added",
          });
          continue;
        }
        if (a.value !== b.value || a.label !== b.label) {
          rows.push({
            fieldId: id,
            label: a.label || b.label || a.type,
            type: a.type,
            sensitive: isLiveSensitive(id, a.type),
            after: a.value,
            before: b.value,
            kind: "changed",
          });
        }
      }
      for (const [id, b] of before) {
        if (seen.has(id)) continue;
        rows.push({
          fieldId: id,
          label: b.label || b.type,
          type: b.type,
          sensitive: isLiveSensitive(id, b.type),
          after: "",
          before: b.value,
          kind: "removed",
        });
      }
      return rows;
    };

    // Do NOT emit a synthetic "current" entry for the live item.
    // The history panel shows the PAST — every snapshot is an
    // archived prior version. The current state is already visible
    // in the read-mode cards above the panel, so duplicating it
    // here just adds noise (and the user has to mentally diff
    // "this row says password=X, is that the current or the
    // previous?"). The `liveFields` map is still built above
    // because `isLiveSensitive` uses it to decide whether to mask
    // a value in older snapshots.

    for (let i = 0; i < versionsCache.length; i++) {
      const entry = versionsCache[i];
      const older = versionsCache[i + 1];
      if (!entry.decryptOk) {
        out.push({
          version: entry.version,
          updatedAt: entry.updatedAt,
          author: entry.author,
          decryptOk: false,
          rows: [],
          changeCount: 0,
        });
        continue;
      }
      const beforeMap = older?.decryptOk
        ? older.fieldsById
        : new Map<string, { label: string; value: string; type: string }>();
      const rows = buildDiff(entry.fieldsById, beforeMap);
      out.push({
        version: entry.version,
        updatedAt: entry.updatedAt,
        author: entry.author,
        decryptOk: true,
        rows,
        changeCount: rows.length,
      });
    }
    return out;
  });

  function relativeOrAbsolute(iso: string): string {
    try {
      const d = new Date(iso);
      const now = Date.now();
      const diff = (now - d.getTime()) / 1000;
      if (diff < 60) return "just now";
      if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
      if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
      if (diff < 86400 * 7) return `${Math.floor(diff / 86400)}d ago`;
      return d.toLocaleDateString();
    } catch {
      return iso;
    }
  }
  function fullTimestamp(iso: string): string {
    try {
      return new Date(iso).toLocaleString();
    } catch {
      return iso;
    }
  }
</script>

<div class="flex flex-col h-full bg-white dark:bg-warm-950">
  <!-- ───── Header: type-colored stripe + icon tile + breadcrumb + actions.
       The left border `border-l-4 ${accent.stripe}` is the "what
       kind of item is this" visual cue that propagates from the
       list row (small tile) up to the editor (full-height stripe).
       Sits on the same color as the hero tile so the eye reads
       them as one accent zone. ───── -->
  <div
    class="flex items-center gap-3 px-4 py-3 border-b border-slate-200 dark:border-warm-700 border-l-4 {accent.stripe}"
  >
    <div
      class="shrink-0 w-11 h-11 rounded-md flex items-center justify-center {accent.tile}"
    >
      <HeaderIcon size={22} />
    </div>
    <div class="flex-1 min-w-0">
      <div
        class="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-slate-500 dark:text-slate-400 flex-wrap"
      >
        <span>{typeLabel(item.type as VaultItemType)}</span>
        {#if (mode === "view" ? (folderCleartext ?? "") : folder).trim()}
          <span class="text-slate-300 dark:text-warm-700">·</span>
          <FolderIcon size={10} class="text-accent-600 dark:text-accent-400" />
          <span
            class="truncate normal-case tracking-normal text-slate-600 dark:text-slate-300"
            >{(mode === "view" ? (folderCleartext ?? "") : folder).trim()}</span
          >
        {/if}
        {#if item.archived && !item.deleted_at}
          <span class="text-slate-300 dark:text-warm-700">·</span>
          <span
            class="inline-flex items-center gap-0.5 text-amber-600 dark:text-amber-400"
            ><Archive size={10} /> Archived</span
          >
        {/if}
        {#if item.deleted_at}
          <span class="text-slate-300 dark:text-warm-700">·</span>
          <span
            class="inline-flex items-center gap-0.5 text-red-600 dark:text-red-400"
            ><Trash2 size={10} /> In trash</span
          >
        {/if}
      </div>
      <div
        class="truncate text-base font-semibold leading-tight mt-0.5 {!(
          mode === 'view' ? (initialTitle ?? '') : title
        ).trim()
          ? 'text-slate-400 italic'
          : ''}"
      >
        {(mode === "view" ? (initialTitle ?? "") : title).trim() ||
          "(untitled)"}
      </div>
    </div>
    <div class="flex items-center gap-1 shrink-0">
      {#if item.deleted_at}
        <button
          onclick={restore}
          disabled={busy}
          class="flex items-center gap-1 px-2 py-1 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 cursor-pointer"
        >
          <RotateCcw size={12} /> Restore
        </button>
        <button
          onclick={purge}
          disabled={busy}
          class="flex items-center gap-1 px-2 py-1 text-xs rounded bg-red-600 text-white hover:bg-red-700 cursor-pointer"
        >
          <Trash2 size={12} /> Delete forever
        </button>
      {:else if mode === "view"}
        <button
          onclick={() => ((favorite = !favorite), void favoriteFlip())}
          class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer"
          title={favorite ? "Unfavorite" : "Favorite"}
          aria-pressed={favorite}
        >
          <Star
            size={15}
            fill={favorite ? "currentColor" : "none"}
            class={favorite ? "text-amber-500" : "text-slate-400"}
          />
        </button>
        <!-- Item-level history toggle. Opens an inline timeline at
             the bottom of the body. We pre-load on first open so
             the panel doesn't flicker between "Loading" and
             "Empty" for the common single-version case. -->
        <button
          onclick={toggleHistoryPanel}
          class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer"
          title="View change history"
          aria-pressed={historyOpen}
        >
          <History
            size={14}
            class={historyOpen
              ? "text-accent-600 dark:text-accent-400"
              : "text-slate-400"}
          />
        </button>
        <button
          onclick={toggleArchive}
          disabled={busy}
          class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer disabled:opacity-40"
          title={item.archived ? "Unarchive" : "Archive"}
        >
          <Archive
            size={14}
            class={item.archived
              ? "text-amber-600 dark:text-amber-400"
              : "text-slate-400"}
          />
        </button>
        <button
          onclick={trash}
          disabled={busy}
          class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer disabled:opacity-40"
          title="Move to trash"
        >
          <Trash2 size={14} class="text-slate-400 hover:text-red-600" />
        </button>
        <div class="w-px h-5 bg-slate-200 dark:bg-warm-700 mx-1"></div>
        <button
          onclick={() => (mode = "edit")}
          class="flex items-center gap-1 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer"
        >
          <Pencil size={12} /> Edit
        </button>
      {:else}
        <button
          onclick={() => (favorite = !favorite)}
          class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer"
          title={favorite ? "Unfavorite" : "Favorite"}
          aria-pressed={favorite}
        >
          <Star
            size={15}
            fill={favorite ? "currentColor" : "none"}
            class={favorite ? "text-amber-500" : "text-slate-400"}
          />
        </button>
        <button
          onclick={cancelEdit}
          disabled={busy}
          class="px-3 py-1.5 text-xs rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer disabled:opacity-40"
        >
          Cancel
        </button>
        <button
          onclick={save}
          disabled={busy || !dirty}
          class="flex items-center gap-1 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        >
          <Save size={12} /> Save
        </button>
      {/if}
    </div>
  </div>

  <!-- ───── Body. Scrollbar lives on the outer container so it
       sits on the pane's right edge regardless of the inner
       content width. The inner column is LEFT-ALIGNED (no
       `mx-auto`) — user feedback was that mid-screen content
       reads as a centered "card", which makes the editor feel
       disconnected from the item list on the left. The
       `max-w-3xl` cap still prevents lines from stretching too
       wide on large monitors. ───── -->
  <div class="flex-1 overflow-y-auto">
    <div class="max-w-3xl p-4 sm:p-6 space-y-4">
      {#if !payload}
        <div
          class="bg-red-50 dark:bg-red-950/30 border border-red-300 dark:border-red-700 rounded p-3 text-sm text-red-700 dark:text-red-300"
        >
          This item's encrypted payload could not be decrypted. It may have been
          encrypted under a different vault key (e.g. before a vault reset). You
          can still delete it.
        </div>
      {/if}

      {#if mode === "view"}
        <!-- ╭─ READ MODE ─────────────────────────────────────╮ -->
        <!-- Tags row -->
        {#if tagsCleartext.length > 0}
          <div class="flex flex-wrap items-center gap-1">
            <span
              class="text-[10px] uppercase tracking-wider text-slate-400 mr-1"
              >Tags</span
            >
            {#each tagsCleartext as t (t)}
              <span
                class="inline-block px-2 py-0.5 text-xs rounded bg-slate-100 dark:bg-warm-800 border border-slate-200 dark:border-warm-700"
              >
                {t}
              </span>
            {/each}
          </div>
        {/if}

        <!-- Fields as read-only cards. Two-row layout per card:
             a tinted header strip with the label + type pill + row
             actions (eye, copy), then a value zone below. Sensitive
             fields get an amber left-stripe so a glance tells the
             user "this row holds something secret". -->
        {#if payload && fields.length > 0}
          <div class="space-y-2">
            {#each fields as field (field.id)}
              {@const isURL = field.type === "url"}
              {@const href = isURL ? safeHref(field.value) : null}
              {@const isMultiline = isMultilineFieldType(field.type)}
              {@const isMasked = field.sensitive && !unmasked.has(field.id)}
              <div
                class="group rounded-lg border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 overflow-hidden hover:border-slate-300 dark:hover:border-warm-600 transition-colors {field.sensitive
                  ? 'border-l-4 border-l-amber-400 dark:border-l-amber-500'
                  : ''}"
              >
                <!-- Card header strip -->
                <div
                  class="flex items-center gap-2 px-3 py-1.5 bg-slate-50 dark:bg-warm-800/60 border-b border-slate-100 dark:border-warm-800"
                >
                  <span
                    class="text-xs font-medium text-slate-700 dark:text-slate-200 truncate"
                  >
                    {field.label || fieldTypeLabel(field.type)}
                  </span>
                  <span
                    class="text-[9px] uppercase tracking-wider px-1.5 py-0.5 rounded bg-slate-200/70 dark:bg-warm-700 text-slate-600 dark:text-slate-300"
                  >
                    {fieldTypeLabel(field.type)}
                  </span>
                  {#if field.sensitive}
                    <span
                      class="text-[9px] uppercase tracking-wider text-amber-700 dark:text-amber-400 inline-flex items-center gap-0.5"
                    >
                      <Eye size={9} /> Sensitive
                    </span>
                  {/if}
                  <span class="flex-1"></span>
                  <!-- Row actions live in the header strip so they
                       stay aligned regardless of value height
                       (multiline keys vs. single-line passwords). -->
                  {#if field.sensitive && field.value}
                    <button
                      type="button"
                      onclick={() => toggleMask(field.id)}
                      class="p-1 rounded hover:bg-slate-200 dark:hover:bg-warm-700 cursor-pointer text-slate-500 dark:text-slate-400"
                      aria-label="Toggle visibility"
                      title={unmasked.has(field.id)
                        ? "Hide value"
                        : "Show value"}
                    >
                      {#if unmasked.has(field.id)}<EyeOff
                          size={13}
                        />{:else}<Eye size={13} />{/if}
                    </button>
                  {/if}
                  {#if field.value}
                    <button
                      type="button"
                      onclick={() => copyValue(field.id, field.value)}
                      class="p-1 rounded hover:bg-slate-200 dark:hover:bg-warm-700 cursor-pointer text-slate-500 dark:text-slate-400"
                      aria-label="Copy value"
                      title="Copy to clipboard"
                    >
                      {#if copiedField === field.id}<Check
                          size={13}
                          class="text-emerald-600 dark:text-emerald-400"
                        />{:else}<Copy size={13} />{/if}
                    </button>
                  {/if}
                </div>
                <!-- Value zone -->
                <div class="px-3 py-2.5 min-w-0">
                  {#if !field.value}
                    <span
                      class="text-sm text-slate-400 dark:text-slate-500 italic"
                      >(empty)</span
                    >
                  {:else if field.type === "totp"}
                    <TOTPDisplay {field} />
                  {:else if isURL && href}
                    <a
                      {href}
                      target="_blank"
                      rel="noopener noreferrer"
                      class="text-sm font-mono break-all text-accent-600 dark:text-accent-400 underline decoration-dotted hover:decoration-solid inline-flex items-center gap-1"
                    >
                      {field.value}
                      <ExternalLink size={11} class="shrink-0 opacity-60" />
                    </a>
                  {:else if isMultiline}
                    <pre
                      class="text-xs font-mono whitespace-pre-wrap break-all text-slate-700 dark:text-slate-100 {isMasked
                        ? 'select-none blur-sm'
                        : ''}">{field.value}</pre>
                  {:else if isMasked}
                    <div
                      class="text-base font-mono tracking-widest text-slate-700 dark:text-slate-100 leading-snug"
                    >
                      {"•".repeat(Math.min(field.value.length, 24))}
                    </div>
                  {:else}
                    <div
                      class="text-sm font-mono break-all text-slate-700 dark:text-slate-100 leading-snug"
                    >
                      {field.value}
                    </div>
                  {/if}
                </div>
              </div>
            {/each}
          </div>
        {/if}

        <!-- Notes — markdown rendered for secure_note, plain for others -->
        {#if payload && (notes ?? "").trim()}
          <div
            class="rounded-lg border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 overflow-hidden"
          >
            <div
              class="flex items-center gap-1.5 px-4 py-1.5 bg-slate-50 dark:bg-warm-800/60 border-b border-slate-100 dark:border-warm-800"
            >
              <FileText size={11} class="text-slate-500 dark:text-slate-400" />
              <span
                class="text-xs font-medium text-slate-700 dark:text-slate-200"
                >Notes</span
              >
              {#if item.type === "secure_note"}
                <span
                  class="text-[9px] uppercase tracking-wider px-1.5 py-0.5 rounded bg-amber-100 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300"
                >
                  Markdown
                </span>
              {/if}
            </div>
            <div class="px-4 py-3">
              {#if item.type === "secure_note"}
                <!-- renderMarkdown produces escape-safe HTML — see
                     _ui/src/lib/vault/markdown.ts for the contract. -->
                <div
                  class="prose-vault text-sm text-slate-700 dark:text-slate-100"
                >
                  {@html renderMarkdown(notes ?? "")}
                </div>
              {:else}
                <div
                  class="whitespace-pre-wrap text-sm text-slate-700 dark:text-slate-100 break-words"
                >
                  {notes}
                </div>
              {/if}
            </div>
          </div>
        {/if}

        <!-- ───── History timeline (item-level) ───── -->
        {#if historyOpen}
          <div
            class="rounded-lg border border-slate-200 dark:border-warm-800 bg-slate-50/40 dark:bg-warm-900/30 overflow-hidden"
          >
            <div
              class="flex items-center justify-between px-4 py-2 border-b border-slate-200 dark:border-warm-800"
            >
              <div
                class="flex items-center gap-1.5 text-[11px] uppercase tracking-wider text-slate-600 dark:text-slate-300 font-medium"
              >
                <History size={12} /> Change history
              </div>
              <button
                onclick={() => (historyOpen = false)}
                class="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer"
                aria-label="Close history"
              >
                <X size={14} />
              </button>
            </div>
            <div class="px-4 py-3">
              {#if versionsLoading && !versionsCache}
                <div class="text-xs text-slate-400 italic">
                  Loading history…
                </div>
              {:else if timeline.length === 0}
                <div class="text-xs text-slate-400 italic">
                  No prior versions. This item has only been saved once.
                </div>
              {:else}
                <!-- Newest at top. Each entry: small header
                     (timestamp + version + author) + list of
                     changed fields. Sensitive fields render
                     their values masked by default; the eye
                     toggle is per (snapshot, field). -->
                <div class="space-y-3">
                  {#each timeline as entry (entry.version + ":" + entry.updatedAt)}
                    <div class="relative pl-4">
                      <!-- Timeline dot. Absolute-positioned so the
                           text aligns nicely without a real <ul>. -->
                      <!-- Timeline dot uses the item's type accent
                           so the entire history panel speaks the
                           same color language as the hero stripe. -->
                      <div
                        class="absolute left-0 top-1.5 w-2 h-2 rounded-full {entry.decryptOk
                          ? accent.dot
                          : 'bg-red-400'}"
                      ></div>
                      <div
                        class="flex items-baseline gap-1.5 text-[10px] text-slate-500 dark:text-slate-400"
                      >
                        <span class="tabular-nums font-medium"
                          >v{entry.version}</span
                        >
                        <span class="text-slate-300 dark:text-warm-700">·</span>
                        <span title={fullTimestamp(entry.updatedAt)}
                          >{relativeOrAbsolute(entry.updatedAt)}</span
                        >
                        {#if entry.author}
                          <span class="text-slate-300 dark:text-warm-700"
                            >·</span
                          >
                          <span class="truncate">{entry.author}</span>
                        {/if}
                      </div>
                      {#if !entry.decryptOk}
                        <div class="mt-1 text-xs italic text-red-500">
                          (unreadable — encrypted with a previous vault key)
                        </div>
                      {:else if entry.rows.length === 0}
                        <div class="mt-1 text-xs italic text-slate-400">
                          No payload changes at this version (only metadata).
                        </div>
                      {:else}
                        <!-- Per-row: just the value AS OF this
                             snapshot. We dropped the side-by-side
                             before/after pair — users wanted "show
                             me what the value WAS at this version"
                             not a diff column. "Removed" rows have
                             nothing useful to display (the value
                             ceased to exist here), so we render a
                             compact note instead of a card. -->
                        <div class="mt-1.5 space-y-1.5">
                          {#each entry.rows as row (row.fieldId)}
                            {@const valueKey = `${entry.version}:${row.fieldId}`}
                            {@const revealed =
                              !row.sensitive || historyRevealed.has(valueKey)}
                            {#if row.kind === "removed"}
                              <div
                                class="flex items-center gap-1.5 px-2.5 py-1 text-[11px] text-slate-500 dark:text-slate-400 italic border border-slate-200 dark:border-warm-800 rounded"
                              >
                                <span
                                  class="font-medium not-italic text-slate-600 dark:text-slate-300"
                                  >{row.label}</span
                                >
                                <span>removed at this version</span>
                              </div>
                            {:else}
                              <div
                                class="rounded border border-slate-200 dark:border-warm-800 bg-white dark:bg-warm-950/60 overflow-hidden"
                              >
                                <div
                                  class="flex items-center gap-1.5 px-2.5 py-1 bg-slate-50 dark:bg-warm-900/60 border-b border-slate-100 dark:border-warm-800"
                                >
                                  <span
                                    class="text-[10px] uppercase tracking-wider font-medium text-slate-600 dark:text-slate-300"
                                    >{row.label}</span
                                  >
                                  <span
                                    class="text-[9px] uppercase tracking-wider text-slate-400"
                                    >{fieldTypeLabel(row.type)}</span
                                  >
                                  <span class="flex-1"></span>
                                  {#if row.kind === "added"}
                                    <span
                                      class="text-[9px] uppercase tracking-wider text-emerald-600 dark:text-emerald-400"
                                      >Added</span
                                    >
                                  {:else}
                                    <span
                                      class="text-[9px] uppercase tracking-wider text-amber-600 dark:text-amber-400"
                                      >Changed</span
                                    >
                                  {/if}
                                  {#if row.sensitive && row.after}
                                    <button
                                      type="button"
                                      onclick={() =>
                                        toggleHistoryReveal(valueKey)}
                                      class="p-0.5 rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer text-slate-400"
                                      aria-label="Toggle visibility"
                                    >
                                      {#if revealed}<EyeOff
                                          size={11}
                                        />{:else}<Eye size={11} />{/if}
                                    </button>
                                  {/if}
                                  {#if row.after}
                                    <button
                                      type="button"
                                      onclick={() =>
                                        copyHistoryValue(valueKey, row.after)}
                                      class="p-0.5 rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer text-slate-400"
                                      aria-label="Copy value"
                                    >
                                      {#if copiedHistKey === valueKey}<Check
                                          size={11}
                                          class="text-emerald-600"
                                        />{:else}<Copy size={11} />{/if}
                                    </button>
                                  {/if}
                                </div>
                                <div class="px-2.5 py-1.5 min-w-0">
                                  {#if !row.after}
                                    <div class="text-xs italic text-slate-400">
                                      (empty)
                                    </div>
                                  {:else if !revealed}
                                    <div
                                      class="text-sm font-mono tracking-widest text-slate-600 dark:text-slate-300"
                                    >
                                      {"•".repeat(
                                        Math.min(row.after.length, 24),
                                      )}
                                    </div>
                                  {:else}
                                    <div
                                      class="text-sm font-mono break-all text-slate-700 dark:text-slate-200"
                                    >
                                      {row.after}
                                    </div>
                                  {/if}
                                </div>
                              </div>
                            {/if}
                          {/each}
                        </div>
                      {/if}
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          </div>
        {/if}

        <!-- Read-mode metadata footer -->
        <div
          class="text-[10px] text-slate-400 pt-2 border-t border-slate-100 dark:border-warm-800 space-y-0.5"
        >
          <div>
            Created <span title={fullTimestamp(item.created_at)}
              >{relativeOrAbsolute(item.created_at)}</span
            >
          </div>
          <div>
            Updated <span title={fullTimestamp(item.updated_at)}
              >{relativeOrAbsolute(item.updated_at)}</span
            >
            · v{item.version}
          </div>
          {#if item.deleted_at}
            <div class="text-red-500">
              Deleted <span title={fullTimestamp(item.deleted_at)}
                >{relativeOrAbsolute(item.deleted_at)}</span
              >
            </div>
          {/if}
        </div>
        <!-- ╰────────────────────────────────────────────────╯ -->
      {:else}
        <!-- ╭─ EDIT MODE ─────────────────────────────────────╮ -->
        <!-- Title -->
        <div>
          <label
            class="block text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-1"
            for="vi-title">Title</label
          >
          <input
            id="vi-title"
            type="text"
            bind:value={title}
            class="w-full px-3 py-2 text-base font-medium rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
          />
        </div>

        <!-- Folder -->
        <div>
          <label
            class="block text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-1"
            for="vi-folder"
          >
            Folder
            <span
              class="ml-1 normal-case tracking-normal text-[10px] text-slate-400"
              >(optional, e.g. Personal / Work)</span
            >
          </label>
          <input
            id="vi-folder"
            type="text"
            list="vi-folder-suggestions"
            bind:value={folder}
            placeholder="No folder"
            autocomplete="off"
            class="w-full px-3 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
          />
          <datalist id="vi-folder-suggestions">
            {#each folderSuggestions as f (f)}
              <option value={f}></option>
            {/each}
          </datalist>
        </div>

        <!-- Tags -->
        <div>
          <label
            class="block text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-1"
            for="vi-tag-draft">Tags</label
          >
          <div class="flex flex-wrap gap-1 mb-2">
            {#each tags as tag (tag)}
              <span
                class="inline-flex items-center gap-1 px-2 py-0.5 text-xs rounded bg-slate-100 dark:bg-warm-800 border border-slate-200 dark:border-warm-700"
              >
                {tag}
                <button
                  type="button"
                  onclick={() => removeTag(tag)}
                  class="hover:text-red-600 cursor-pointer"
                  aria-label="Remove tag"
                >
                  <X size={10} />
                </button>
              </span>
            {/each}
          </div>
          <input
            id="vi-tag-draft"
            type="text"
            placeholder="Add a tag and press Enter"
            bind:value={tagDraft}
            onkeydown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addTag();
              }
            }}
            class="w-full px-3 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
          />
        </div>

        <!-- Fields editor -->
        {#if payload}
          <div class="space-y-2">
            <div class="flex items-center justify-between">
              <div
                class="text-[10px] font-medium uppercase tracking-wider text-slate-500"
              >
                Fields
              </div>
              <button
                type="button"
                onclick={addField}
                class="flex items-center gap-1 px-2 py-1 text-xs rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer"
              >
                <Plus size={12} /> Add field
              </button>
            </div>

            {#each fields as field, idx (field.id)}
              <div
                class="border border-slate-200 dark:border-warm-700 rounded p-2 bg-slate-50 dark:bg-warm-900/30"
              >
                <div class="flex items-center gap-2 flex-wrap">
                  <div class="flex flex-col">
                    <button
                      type="button"
                      disabled={idx === 0}
                      onclick={() => moveField(field.id, -1)}
                      class="disabled:opacity-20 cursor-pointer disabled:cursor-not-allowed"
                      aria-label="Move up"
                    >
                      <GripVertical size={12} />
                    </button>
                  </div>
                  <input
                    type="text"
                    bind:value={fields[idx].label}
                    class="w-32 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500"
                    placeholder="Label"
                  />
                  <select
                    bind:value={fields[idx].type}
                    class="px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500"
                  >
                    <option value="text">Text</option>
                    <option value="password">Password</option>
                    <option value="email">Email</option>
                    <option value="phone">Phone</option>
                    <option value="url">URL</option>
                    <option value="username">Username</option>
                    <option value="totp">TOTP</option>
                    <option value="date">Date</option>
                    <option value="month_year">MM/YY</option>
                    <option value="cvv">CVV</option>
                    <option value="card_number">Card number</option>
                    <option value="pin">PIN</option>
                    <option value="address">Address</option>
                    <option value="ssh_private_key">SSH private key</option>
                    <option value="ssh_public_key">SSH public key</option>
                    <option value="api_key">API key</option>
                    <option value="secret_token">Secret token</option>
                    <option value="hostname">Hostname</option>
                    <option value="port">Port</option>
                    <option value="connection_string">Connection string</option>
                  </select>
                  <label class="flex items-center gap-1 text-xs cursor-pointer">
                    <input
                      type="checkbox"
                      bind:checked={fields[idx].sensitive}
                    />
                    <span class="text-slate-500">mask</span>
                  </label>
                  <div class="flex-1"></div>
                  <button
                    type="button"
                    onclick={() => removeField(field.id)}
                    class="text-slate-400 hover:text-red-600 cursor-pointer"
                    aria-label="Remove field"
                  >
                    <X size={14} />
                  </button>
                </div>

                <div class="mt-1.5 flex items-start gap-1">
                  {#if field.type === "totp"}
                    <div class="flex-1 space-y-1 min-w-0">
                      <input
                        type="text"
                        bind:value={fields[idx].value}
                        placeholder="otpauth://totp/... or base32 secret"
                        class="w-full px-2 py-1 text-xs font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500"
                      />
                      {#if field.value}
                        <TOTPDisplay {field} />
                      {/if}
                    </div>
                  {:else if isMultilineFieldType(field.type)}
                    <textarea
                      bind:value={fields[idx].value}
                      rows="4"
                      class="flex-1 min-w-0 px-2 py-1 text-xs font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 {field.sensitive &&
                      !unmasked.has(field.id)
                        ? '[-webkit-text-security:disc] [text-security:disc]'
                        : ''}"
                    ></textarea>
                  {:else}
                    <input
                      type={field.sensitive && !unmasked.has(field.id)
                        ? "password"
                        : "text"}
                      bind:value={fields[idx].value}
                      class="flex-1 min-w-0 px-2 py-1 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500"
                    />
                  {/if}
                  {#if field.sensitive}
                    <button
                      type="button"
                      onclick={() => toggleMask(field.id)}
                      class="p-1.5 rounded hover:bg-slate-200 dark:hover:bg-warm-800 cursor-pointer"
                      aria-label="Toggle visibility"
                    >
                      {#if unmasked.has(field.id)}<EyeOff
                          size={14}
                        />{:else}<Eye size={14} />{/if}
                    </button>
                  {/if}
                  <button
                    type="button"
                    onclick={() => copyValue(field.id, field.value)}
                    class="p-1.5 rounded hover:bg-slate-200 dark:hover:bg-warm-800 cursor-pointer"
                    aria-label="Copy value"
                  >
                    {#if copiedField === field.id}<Check
                        size={14}
                        class="text-emerald-600"
                      />{:else}<Copy size={14} />{/if}
                  </button>
                  {#if field.type === "password"}
                    <button
                      type="button"
                      onclick={() => (generatorFor = field.id)}
                      class="p-1.5 rounded hover:bg-slate-200 dark:hover:bg-warm-800 cursor-pointer"
                      aria-label="Generate password"
                    >
                      <Dices size={14} />
                    </button>
                  {/if}
                </div>
              </div>
            {/each}
          </div>

          <!-- Notes editor -->
          <div>
            <label
              class="block text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-1"
              for="vi-notes"
            >
              Notes
              {#if item.type === "secure_note"}
                <span
                  class="ml-1 normal-case tracking-normal text-[10px] text-slate-400"
                  >(markdown supported)</span
                >
              {/if}
            </label>
            <textarea
              id="vi-notes"
              bind:value={notes}
              rows="6"
              class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
            ></textarea>
          </div>
        {/if}
        <!-- ╰────────────────────────────────────────────────╯ -->
      {/if}
    </div>
  </div>
</div>

<!-- Password generator overlay (edit mode only) -->
{#if generatorFor}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
    use:backdropClose={() => (generatorFor = null)}
    role="dialog"
    tabindex="-1"
    onkeydown={(e) => {
      if (e.key === "Escape") generatorFor = null;
    }}
    aria-modal="true"
  >
    <div class="bg-white dark:bg-warm-800 rounded-lg shadow-xl p-4 w-96">
      <div class="flex items-center justify-between mb-3">
        <h3 class="text-sm font-semibold">Password generator</h3>
        <button
          onclick={() => (generatorFor = null)}
          class="text-slate-400 hover:text-slate-600 cursor-pointer"
          aria-label="Close"
        >
          <X size={16} />
        </button>
      </div>
      <PasswordGenerator onApply={applyGenerator} />
    </div>
  </div>
{/if}

<style>
  /* Lightweight typographic polish for the rendered markdown in
     secure notes. We don't pull in @tailwindcss/typography to keep
     bundle weight down; this gets us most of the way. */
  :global(
      .prose-vault h1,
      .prose-vault h2,
      .prose-vault h3,
      .prose-vault h4,
      .prose-vault h5,
      .prose-vault h6
    ) {
    color: inherit;
  }
  :global(.prose-vault p:first-child) {
    margin-top: 0;
  }
  :global(.prose-vault p:last-child) {
    margin-bottom: 0;
  }
  :global(.prose-vault ul li, .prose-vault ol li) {
    margin: 0.15rem 0;
  }
</style>
