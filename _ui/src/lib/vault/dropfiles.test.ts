import { describe, expect, it } from 'vitest';
import { collectFromInput, decodeText, fileKind, formatBytes } from './dropfiles';

describe('formatBytes', () => {
  it('formats sizes', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(50 * 1024 * 1024)).toBe('50 MB');
  });
});

describe('fileKind', () => {
  it('detects kinds by type and extension', () => {
    expect(fileKind('a.png', 'image/png')).toBe('image');
    expect(fileKind('a.pdf')).toBe('pdf');
    expect(fileKind('a.tar.gz')).toBe('archive');
    expect(fileKind('main.go')).toBe('code');
    expect(fileKind('notes.md')).toBe('text');
    expect(fileKind('x', '', true)).toBe('folder');
    expect(fileKind('blob.bin')).toBe('other');
  });
});

describe('collectFromInput', () => {
  it('splits webkitRelativePath into folder + file', () => {
    const f = new File(['x'], 'c.txt');
    Object.defineProperty(f, 'webkitRelativePath', { value: 'a/b/c.txt' });
    const g = new File(['y'], 'd.txt');
    const list = { 0: f, 1: g, length: 2, item: (i: number) => [f, g][i] } as unknown as FileList;
    const out = collectFromInput(list);
    expect(out[0].relDir).toBe('a/b');
    expect(out[1].relDir).toBe('');
  });
});

describe('decodeText', () => {
  it('accepts UTF-8 text and strips the BOM', () => {
    const d = decodeText(new Uint8Array([0xef, 0xbb, 0xbf, ...new TextEncoder().encode('héllo\n')]));
    expect(d).toEqual({ text: 'héllo\n', binary: false, lossy: false });
  });
  it('flags binary content', () => {
    const d = decodeText(new Uint8Array([0x7f, 0x45, 0x4c, 0x46, 0x02, 0x01, 0x00, 0x00]));
    expect(d.binary).toBe(true);
  });
  it('marks invalid UTF-8 as lossy', () => {
    const d = decodeText(new Uint8Array([0x61, 0xe9, 0x62]));
    expect(d.lossy).toBe(true);
  });
});
