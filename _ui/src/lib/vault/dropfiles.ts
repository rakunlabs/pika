// Helpers for turning a drag-and-drop or <input> selection into a flat
// list of files with their relative folder paths, so dropped folders
// keep their structure on upload.

export interface PickedFile {
  file: File;
  /** Folder path relative to the drop target, "" for top-level files. */
  relDir: string;
}

/* eslint-disable @typescript-eslint/no-explicit-any */
type Entry = any;

function readAllEntries(dir: Entry): Promise<Entry[]> {
  const reader = dir.createReader();
  const out: Entry[] = [];
  return new Promise((resolve, reject) => {
    // readEntries returns at most ~100 entries per call (Chrome), so
    // keep reading until an empty batch.
    const next = () =>
      reader.readEntries((batch: Entry[]) => {
        if (!batch.length) return resolve(out);
        out.push(...batch);
        next();
      }, reject);
    next();
  });
}

async function walk(entry: Entry, prefix: string): Promise<PickedFile[]> {
  if (entry.isFile) {
    const file = await new Promise<File>((resolve, reject) => entry.file(resolve, reject));
    return [{ file, relDir: prefix }];
  }
  if (!entry.isDirectory) return [];
  const children = await readAllEntries(entry);
  const dir = prefix ? `${prefix}/${entry.name}` : entry.name;
  const nested = await Promise.all(children.map((c) => walk(c, dir)));
  const flat = nested.flat();
  // Preserve empty folders by emitting a marker with no file.
  if (flat.length === 0) return [{ file: null as unknown as File, relDir: dir }];
  return flat;
}

/**
 * Collect files from a drop event. Must be called synchronously inside
 * the drop handler: DataTransfer items are only readable during the
 * event, so the entries are captured before the first await.
 */
export function collectDropped(dt: DataTransfer | null): Promise<PickedFile[]> {
  if (!dt) return Promise.resolve([]);
  const entries: Entry[] = [];
  for (const item of Array.from(dt.items ?? [])) {
    if (item.kind !== 'file') continue;
    const e = (item as any).webkitGetAsEntry?.();
    if (e) entries.push(e);
  }
  if (entries.length) {
    return Promise.all(entries.map((e) => walk(e, ''))).then((n) => n.flat());
  }
  return Promise.resolve(Array.from(dt.files ?? []).map((file) => ({ file, relDir: '' })));
}

/** Files from <input type=file> (with or without webkitdirectory). */
export function collectFromInput(files: FileList | null): PickedFile[] {
  return Array.from(files ?? []).map((file) => {
    const rel = (file as any).webkitRelativePath as string | undefined;
    if (!rel) return { file, relDir: '' };
    const i = rel.lastIndexOf('/');
    return { file, relDir: i > 0 ? rel.slice(0, i) : '' };
  });
}

/** True when the drag carries OS files (as opposed to an in-app move). */
export function isFileDrag(e: DragEvent): boolean {
  return Array.from(e.dataTransfer?.types ?? []).includes('Files');
}

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '—';
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export type FileKind = 'folder' | 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'archive' | 'code' | 'sheet' | 'other';

const ext = (name: string) => {
  const i = name.lastIndexOf('.');
  return i > 0 ? name.slice(i + 1).toLowerCase() : '';
};

export function fileKind(name: string, contentType = '', isDir = false): FileKind {
  if (isDir) return 'folder';
  const ct = contentType.toLowerCase();
  const e = ext(name);
  if (ct.startsWith('image/')) return 'image';
  if (ct.startsWith('video/')) return 'video';
  if (ct.startsWith('audio/')) return 'audio';
  if (ct === 'application/pdf' || e === 'pdf') return 'pdf';
  if (['zip', 'tar', 'gz', 'tgz', '7z', 'rar', 'xz', 'bz2', 'zst'].includes(e)) return 'archive';
  if (['xls', 'xlsx', 'ods'].includes(e)) return 'sheet';
  if (['csv', 'tsv'].includes(e)) return 'text';
  if (['js', 'ts', 'go', 'py', 'rs', 'java', 'c', 'h', 'cpp', 'sh', 'json', 'yaml', 'yml', 'xml', 'html', 'css', 'sql', 'svelte', 'toml', 'ini', 'dockerfile', 'rs', 'lua', 'nginx'].includes(e)) return 'code';
  if (ct.startsWith('text/') || ['txt', 'md', 'markdown', 'log', 'env', 'ini', 'conf', 'pem', 'crt', 'key', 'pub'].includes(e)) return 'text';
  return 'other';
}

/** True for markdown files (rendered preview + raw toggle). */
export function isMarkdown(name: string, contentType = ''): boolean {
  const e = ext(name);
  return e === 'md' || e === 'markdown' || contentType.toLowerCase().startsWith('text/markdown');
}

/** Kinds that can be opened in the in-browser text editor. */
export function editable(kind: FileKind, size: number): boolean {
  return (kind === 'text' || kind === 'code') && size <= EDIT_MAX_BYTES;
}

/** Larger files are view-only/download-only to keep the editor responsive. */
export const EDIT_MAX_BYTES = 5 * 1024 * 1024;

/** Kinds that the preview pane can render inline. */
export function previewable(kind: FileKind): boolean {
  return ['image', 'video', 'audio', 'pdf', 'text', 'code'].includes(kind);
}

export interface DecodedText {
  text: string;
  /** Content looks binary (NUL bytes or mostly invalid UTF-8). */
  binary: boolean;
  /** Decoding replaced invalid sequences, so saving would alter bytes. */
  lossy: boolean;
}

/**
 * Decodes bytes as UTF-8 and classifies them. Saving a lossy decode
 * rewrites invalid sequences as U+FFFD, so callers should warn before
 * allowing edits.
 */
export function decodeText(bytes: Uint8Array): DecodedText {
  // Strip a UTF-8 BOM so it doesn't show up as a stray character.
  const body = bytes.length >= 3 && bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf ? bytes.subarray(3) : bytes;
  let lossy = false;
  let text: string;
  try {
    text = new TextDecoder('utf-8', { fatal: true }).decode(body);
  } catch {
    lossy = true;
    text = new TextDecoder('utf-8').decode(body);
  }
  const sample = body.subarray(0, 8192);
  let nul = 0;
  let control = 0;
  for (const b of sample) {
    if (b === 0) nul++;
    else if (b < 0x09 || (b > 0x0d && b < 0x20)) control++;
  }
  let replacement = 0;
  if (lossy) for (const ch of text.slice(0, 8192)) if (ch === '\uFFFD') replacement++;
  const n = Math.max(1, sample.length);
  const binary = nul > 0 || control / n > 0.1 || replacement / n > 0.05;
  return { text, binary, lossy };
}
