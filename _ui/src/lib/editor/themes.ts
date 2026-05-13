import type { Extension } from '@codemirror/state';
import { oneDark } from '@codemirror/theme-one-dark';
import { githubLight, githubDark } from '@uiw/codemirror-theme-github';
import { dracula } from '@uiw/codemirror-theme-dracula';
import { solarizedLight, solarizedDark } from '@uiw/codemirror-theme-solarized';
import { nord } from '@uiw/codemirror-theme-nord';
import { monokai } from '@uiw/codemirror-theme-monokai';
import { gruvboxLight, gruvboxDark } from '@uiw/codemirror-theme-gruvbox-dark';

import type { EditorThemeKey } from '@/lib/store/prefs.svelte';

export interface EditorThemeMeta {
  key: EditorThemeKey;
  label: string;
  isDark: boolean;
  // undefined = no extension, which yields CodeMirror's default light theme.
  extension: Extension | undefined;
}

// Order here is the order rendered in the Appearance settings dropdown.
// Light themes first, dark themes second — within each group alphabetical
// by display name except where a pairing (light/dark of the same family)
// makes neighbours more useful.
export const EDITOR_THEMES: EditorThemeMeta[] = [
  { key: 'default-light',   label: 'Default Light',   isDark: false, extension: undefined },
  { key: 'github-light',    label: 'GitHub Light',    isDark: false, extension: githubLight },
  { key: 'solarized-light', label: 'Solarized Light', isDark: false, extension: solarizedLight },
  { key: 'gruvbox-light',   label: 'Gruvbox Light',   isDark: false, extension: gruvboxLight },

  { key: 'one-dark',        label: 'One Dark',        isDark: true,  extension: oneDark },
  { key: 'github-dark',     label: 'GitHub Dark',     isDark: true,  extension: githubDark },
  { key: 'dracula',         label: 'Dracula',         isDark: true,  extension: dracula },
  { key: 'solarized-dark',  label: 'Solarized Dark',  isDark: true,  extension: solarizedDark },
  { key: 'nord',            label: 'Nord',            isDark: true,  extension: nord },
  { key: 'monokai',         label: 'Monokai',         isDark: true,  extension: monokai },
  { key: 'gruvbox-dark',    label: 'Gruvbox Dark',    isDark: true,  extension: gruvboxDark },
];

const themeIndex: Record<string, EditorThemeMeta> = Object.fromEntries(
  EDITOR_THEMES.map((t) => [t.key, t]),
);

// Falls back to One Dark if a stale/unknown key is somehow stored.
export function resolveEditorTheme(key: string): EditorThemeMeta {
  return themeIndex[key] ?? themeIndex['one-dark'];
}

// Convenience: pick a sensible gutter color set for a given theme. CodeMirror
// theme extensions paint the editor content but the wrapper's host element
// gutter sometimes needs to mirror them so the seams disappear.
export function gutterStylesFor(theme: EditorThemeMeta): {
  background: string;
  color: string;
} {
  if (theme.isDark) {
    return { background: '#1e1e1e', color: '#6e7681' };
  }
  return { background: '#f6f8fa', color: '#57606a' };
}

// Curated list of monospace font stacks exposed in the Appearance UI.
// Each entry's CSS family name corresponds to a face that's bundled
// via @fontsource/* and imported in _ui/src/style/fonts.css — so the
// font always renders correctly regardless of what the user has
// installed locally. The `value` string is the exact `font-family`
// declaration CodeMirror sets on .cm-content; the trailing fallback
// chain ends with `monospace` so a missing face never produces a
// proportional-font rendering.
export const EDITOR_FONT_FAMILIES: { label: string; value: string }[] = [
  // Geist Mono — Vercel's monospace, default for new users.
  { label: 'Geist Mono',      value: "'Geist Mono', ui-monospace, monospace" },
  { label: 'JetBrains Mono',  value: "'JetBrains Mono', ui-monospace, monospace" },
  { label: 'Fira Code',       value: "'Fira Code', ui-monospace, monospace" },
  { label: 'Source Code Pro', value: "'Source Code Pro', ui-monospace, monospace" },
  { label: 'IBM Plex Mono',   value: "'IBM Plex Mono', ui-monospace, monospace" },
  { label: 'Inconsolata',     value: "'Inconsolata', ui-monospace, monospace" },
  { label: 'Roboto Mono',     value: "'Roboto Mono', ui-monospace, monospace" },
  // System monospace — uses whatever the OS shows for ui-monospace
  // (San Francisco Mono on macOS, Cascadia Mono on Windows, etc.).
  { label: 'System Monospace', value: 'ui-monospace, SFMono-Regular, Menlo, Monaco, monospace' },
];

export const EDITOR_FONT_SIZE_MIN = 8;
export const EDITOR_FONT_SIZE_MAX = 48;
