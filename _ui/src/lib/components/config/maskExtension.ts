import { StateField, StateEffect, type Extension, type Range } from '@codemirror/state';
import {
  Decoration,
  type DecorationSet,
  EditorView,
  ViewPlugin,
  type ViewUpdate,
  WidgetType,
} from '@codemirror/view';
import { syntaxTree } from '@codemirror/language';
import type { FileFormat } from '@/lib/types/config';
import { addToast } from '@/lib/store/toast.svelte';

// Detect Apple platforms once for the tooltip wording.
const isMac =
  typeof navigator !== 'undefined' &&
  /Mac|iPhone|iPad|iPod/i.test(navigator.platform);
const modKeyLabel = isMac ? '⌘' : 'Ctrl';

// ─────────────────────────────────────────────────────────────────────────────
// State
// ─────────────────────────────────────────────────────────────────────────────

interface MaskState {
  enabled: boolean;
  // Set of "<from>-<to>" keys for ranges the user has individually revealed.
  revealed: Set<string>;
}

const rangeKey = (from: number, to: number) => `${from}-${to}`;

/** Globally enable / disable the tape. Disabling clears all individual reveals. */
export const setMaskEnabledEffect = StateEffect.define<boolean>();

/** Reveal a single range (used by the widget's dblclick handler). */
export const revealRangeEffect = StateEffect.define<{ from: number; to: number }>();

const maskStateField = StateField.define<MaskState>({
  create: () => ({ enabled: true, revealed: new Set() }),
  update(value, tr) {
    let next = value;

    // Map revealed ranges through document changes so an edit inside a revealed
    // value doesn't accidentally re-tape it.
    if (tr.docChanged && next.revealed.size > 0) {
      const remapped = new Set<string>();
      for (const key of next.revealed) {
        const [oldFrom, oldTo] = key.split('-').map(Number);
        if (Number.isNaN(oldFrom) || Number.isNaN(oldTo)) continue;
        const newFrom = tr.changes.mapPos(oldFrom, 1);
        const newTo = tr.changes.mapPos(oldTo, -1);
        if (newFrom < newTo) remapped.add(rangeKey(newFrom, newTo));
      }
      next = { ...next, revealed: remapped };
    }

    for (const e of tr.effects) {
      if (e.is(setMaskEnabledEffect)) {
        // Re-mask: turning the toggle either way wipes individual reveals so
        // toggle-off → toggle-on returns to a fully masked state.
        next = { enabled: e.value, revealed: new Set() };
      } else if (e.is(revealRangeEffect)) {
        const revealed = new Set(next.revealed);
        revealed.add(rangeKey(e.value.from, e.value.to));
        next = { ...next, revealed };
      }
    }

    return next;
  },
});

// ─────────────────────────────────────────────────────────────────────────────
// Widget — the visible "hazard tape" replacement
// ─────────────────────────────────────────────────────────────────────────────

class TapeWidget extends WidgetType {
  constructor(
    readonly from: number,
    readonly to: number,
  ) {
    super();
  }

  eq(other: TapeWidget): boolean {
    return other.from === this.from && other.to === this.to;
  }

  toDOM(view: EditorView): HTMLElement {
    const span = document.createElement('span');
    span.className = 'cm-mask';
    span.setAttribute('role', 'button');
    span.setAttribute('tabindex', '0');
    span.setAttribute(
      'aria-label',
      `Masked value, double-click to reveal, ${modKeyLabel}+click to copy`,
    );
    span.title = `Double-click to reveal · ${modKeyLabel}+click to copy`;
    span.dataset.from = String(this.from);
    span.dataset.to = String(this.to);

    const reveal = () => {
      view.dispatch({
        effects: revealRangeEffect.of({ from: this.from, to: this.to }),
      });
    };

    const copy = async () => {
      const text = view.state.doc.sliceString(this.from, this.to);
      try {
        await navigator.clipboard.writeText(text);
        // Brief green flash so the user sees something happened
        // even before the toast lands.
        span.classList.add('cm-mask--copied');
        setTimeout(() => span.classList.remove('cm-mask--copied'), 350);
        addToast('Value copied to clipboard', 'success', 1500);
      } catch {
        addToast('Failed to copy value', 'alert');
      }
    };

    span.addEventListener('click', (e) => {
      if (e.ctrlKey || e.metaKey) {
        e.preventDefault();
        e.stopPropagation();
        copy();
      }
    });
    span.addEventListener('dblclick', (e) => {
      e.preventDefault();
      e.stopPropagation();
      reveal();
    });
    span.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        reveal();
      } else if ((e.ctrlKey || e.metaKey) && (e.key === 'c' || e.key === 'C')) {
        e.preventDefault();
        copy();
      }
    });

    return span;
  }

  // Default returns true — we want CM to leave widget events alone so our
  // own listeners are the source of truth.
}

// ─────────────────────────────────────────────────────────────────────────────
// Decoration builders
// ─────────────────────────────────────────────────────────────────────────────

function pushDecoration(
  builder: Range<Decoration>[],
  revealed: Set<string>,
  from: number,
  to: number,
) {
  if (from >= to) return;
  if (revealed.has(rangeKey(from, to))) return;
  builder.push(
    Decoration.replace({ widget: new TapeWidget(from, to) }).range(from, to),
  );
}

function buildJsonDecorations(view: EditorView): DecorationSet {
  const state = view.state.field(maskStateField);
  if (!state.enabled) return Decoration.none;
  const tree = syntaxTree(view.state);
  const builder: Range<Decoration>[] = [];
  for (const { from, to } of view.visibleRanges) {
    tree.iterate({
      from,
      to,
      enter(node) {
        const name = node.name;
        // PropertyName is its own node type, so this list never matches keys.
        if (
          name === 'String' ||
          name === 'Number' ||
          name === 'True' ||
          name === 'False' ||
          name === 'Null'
        ) {
          pushDecoration(builder, state.revealed, node.from, node.to);
          return false; // don't descend further into atomic value
        }
      },
    });
  }
  return Decoration.set(builder, true);
}

function buildYamlDecorations(view: EditorView): DecorationSet {
  const state = view.state.field(maskStateField);
  if (!state.enabled) return Decoration.none;
  const tree = syntaxTree(view.state);
  const builder: Range<Decoration>[] = [];
  for (const { from, to } of view.visibleRanges) {
    let keyDepth = 0;
    tree.iterate({
      from,
      to,
      enter(node) {
        if (node.name === 'Key') {
          keyDepth++;
          return; // descend, but every literal inside Key is skipped
        }
        if (keyDepth > 0) return;
        if (
          node.name === 'Literal' ||
          node.name === 'QuotedLiteral' ||
          node.name === 'BlockLiteralContent'
        ) {
          pushDecoration(builder, state.revealed, node.from, node.to);
          return false;
        }
      },
      leave(node) {
        if (node.name === 'Key') keyDepth--;
      },
    });
  }
  return Decoration.set(builder, true);
}

function buildTomlDecorations(view: EditorView): DecorationSet {
  const state = view.state.field(maskStateField);
  if (!state.enabled) return Decoration.none;
  const builder: Range<Decoration>[] = [];
  const doc = view.state.doc;

  const inViewport = (from: number, to: number) =>
    view.visibleRanges.some((r) => to >= r.from && from <= r.to);

  // Multi-line state carried across iterations.
  let inMultiStr: { delim: '"""' | "'''"; start: number } | null = null;
  let inMultiArr: { start: number } | null = null;

  for (let lineNo = 1; lineNo <= doc.lines; lineNo++) {
    const line = doc.line(lineNo);
    const text = line.text;

    if (inMultiStr) {
      const idx = text.indexOf(inMultiStr.delim);
      if (idx >= 0) {
        const end = line.from + idx + 3;
        if (inViewport(inMultiStr.start, end)) {
          pushDecoration(builder, state.revealed, inMultiStr.start, end);
        }
        inMultiStr = null;
      }
      continue;
    }

    if (inMultiArr) {
      const idx = text.indexOf(']');
      if (idx >= 0) {
        const end = line.from + idx + 1;
        if (inViewport(inMultiArr.start, end)) {
          pushDecoration(builder, state.revealed, inMultiArr.start, end);
        }
        inMultiArr = null;
      }
      continue;
    }

    const trimmed = text.trimStart();
    if (trimmed === '' || trimmed.startsWith('#') || trimmed.startsWith('[')) continue;

    const eqIdx = text.indexOf('=');
    if (eqIdx < 0) continue;

    let valStart = eqIdx + 1;
    while (valStart < text.length && (text[valStart] === ' ' || text[valStart] === '\t')) {
      valStart++;
    }
    if (valStart >= text.length) continue;

    // Find value end, stopping at a `#` that's not inside quotes.
    let valEnd = text.length;
    let inStr: '"' | "'" | null = null;
    for (let i = valStart; i < text.length; i++) {
      const c = text[i];
      if (inStr) {
        if (c === inStr && text[i - 1] !== '\\') inStr = null;
      } else {
        if (c === '"' || c === "'") inStr = c;
        else if (c === '#') {
          valEnd = i;
          break;
        }
      }
    }
    while (valEnd > valStart && (text[valEnd - 1] === ' ' || text[valEnd - 1] === '\t')) {
      valEnd--;
    }
    if (valEnd <= valStart) continue;

    const valText = text.slice(valStart, valEnd);
    const absFrom = line.from + valStart;
    const absTo = line.from + valEnd;

    // Multi-line triple-quoted string?
    if (valText.startsWith('"""') || valText.startsWith("'''")) {
      const delim: '"""' | "'''" = valText.startsWith('"""') ? '"""' : "'''";
      const after = valText.slice(3);
      const closeIdx = after.indexOf(delim);
      if (closeIdx >= 0) {
        if (inViewport(absFrom, absTo)) {
          pushDecoration(builder, state.revealed, absFrom, absTo);
        }
      } else {
        inMultiStr = { delim, start: absFrom };
      }
      continue;
    }

    // Multi-line array?
    if (valText.startsWith('[') && !valText.includes(']')) {
      inMultiArr = { start: absFrom };
      continue;
    }

    if (inViewport(absFrom, absTo)) {
      pushDecoration(builder, state.revealed, absFrom, absTo);
    }
  }

  return Decoration.set(builder, true);
}

// ─────────────────────────────────────────────────────────────────────────────
// ViewPlugin per format
// ─────────────────────────────────────────────────────────────────────────────

type Builder = (view: EditorView) => DecorationSet;

function makeViewPlugin(builder: Builder) {
  return ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;
      constructor(view: EditorView) {
        this.decorations = builder(view);
      }
      update(u: ViewUpdate) {
        const stateChanged =
          u.state.field(maskStateField) !== u.startState.field(maskStateField);
        if (u.docChanged || u.viewportChanged || stateChanged) {
          this.decorations = builder(u.view);
        }
      }
    },
    { decorations: (v) => v.decorations },
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Public API
// ─────────────────────────────────────────────────────────────────────────────

/**
 * Build the mask extension array for a given format. Returns `[]` for `raw`
 * (no structure to mask) so callers can drop it into an extensions array
 * unconditionally.
 */
export function createMaskExtension(format: FileFormat): Extension {
  switch (format) {
    case 'json':
      return [maskStateField, makeViewPlugin(buildJsonDecorations)];
    case 'yaml':
      return [maskStateField, makeViewPlugin(buildYamlDecorations)];
    case 'toml':
      return [maskStateField, makeViewPlugin(buildTomlDecorations)];
    case 'raw':
    default:
      return [];
  }
}

/** Toggle the global tape. Calling this also clears any individual reveals. */
export function setMaskEnabled(view: EditorView, enabled: boolean): void {
  // Guard against views that were created without the state field (e.g. raw).
  const f = view.state.field(maskStateField, false);
  if (f === undefined) return;
  view.dispatch({ effects: setMaskEnabledEffect.of(enabled) });
}

/** Snapshot the mask state surfaced to consumers (UI button, etc.). */
export interface MaskInfo {
  enabled: boolean;
  hasReveals: boolean;
}

/** Read the current mask info from a view, if the extension is installed. */
export function getMaskInfo(view: EditorView): MaskInfo | undefined {
  const s = view.state.field(maskStateField, false);
  if (!s) return undefined;
  return { enabled: s.enabled, hasReveals: s.revealed.size > 0 };
}

/**
 * Build a watcher extension that calls `onChange` whenever the mask state
 * changes (toggle, reveal, doc-edit-driven remap, reconfigure). Use this to
 * keep a Svelte/UI variable in sync with what the editor actually shows.
 */
export function createMaskWatcher(onChange: (info: MaskInfo) => void): Extension {
  return ViewPlugin.fromClass(
    class {
      constructor(view: EditorView) {
        const s = view.state.field(maskStateField, false);
        if (s) onChange({ enabled: s.enabled, hasReveals: s.revealed.size > 0 });
      }
      update(u: ViewUpdate) {
        const cur = u.state.field(maskStateField, false);
        const prev = u.startState.field(maskStateField, false);
        if (cur && cur !== prev) {
          onChange({ enabled: cur.enabled, hasReveals: cur.revealed.size > 0 });
        }
      }
    },
  );
}
