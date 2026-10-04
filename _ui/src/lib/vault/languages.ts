// Maps a file name to a CodeMirror language. JSON/YAML/TOML are already
// in the main bundle (config editor); the rest are loaded on demand.

import { LanguageSupport, StreamLanguage } from '@codemirror/language';
import { json } from '@codemirror/lang-json';
import { yaml } from '@codemirror/lang-yaml';
import { toml } from '@codemirror/legacy-modes/mode/toml';

type Loader = () => Promise<LanguageSupport>;

async function legacy(load: () => Promise<unknown>): Promise<LanguageSupport> {
  const mode = await load();
  return new LanguageSupport(StreamLanguage.define(mode as never));
}

const byExt: Record<string, Loader> = {
  json: async () => json(),
  yaml: async () => yaml(),
  yml: async () => yaml(),
  toml: async () => new LanguageSupport(StreamLanguage.define(toml)),
  md: () => legacy(async () => (await import('./markdownMode')).markdownMode),
  markdown: () => legacy(async () => (await import('./markdownMode')).markdownMode),
  sh: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/shell')).shell),
  bash: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/shell')).shell),
  env: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/properties')).properties),
  ini: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/properties')).properties),
  conf: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/properties')).properties),
  properties: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/properties')).properties),
  py: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/python')).python),
  go: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/go')).go),
  rs: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/rust')).rust),
  js: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/javascript')).javascript),
  ts: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/javascript')).typescript),
  sql: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/sql')).standardSQL),
  xml: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/xml')).xml),
  html: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/xml')).html),
  svelte: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/xml')).html),
  css: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/css')).css),
  nginx: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/nginx')).nginx),
  dockerfile: () => legacy(async () => (await import('@codemirror/legacy-modes/mode/dockerfile')).dockerFile),
};

export function languageLoader(name: string): Loader | null {
  const lower = name.toLowerCase();
  if (lower === 'dockerfile') return byExt.dockerfile;
  if (lower === '.env' || lower.startsWith('.env.')) return byExt.env;
  const i = lower.lastIndexOf('.');
  return i >= 0 ? (byExt[lower.slice(i + 1)] ?? null) : null;
}
