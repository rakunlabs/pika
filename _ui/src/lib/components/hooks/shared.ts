// Shared helpers for the hook target editors.

import type { HookTarget } from '@/lib/types/config';

export type HookTargetType = 'http' | 'kafka' | 'redis' | 'nats' | 'log';

/** Every target-type editor exposes this via `bind:this`. */
export interface TargetFieldsApi {
  /** Validate and build the target. Returns null (after toasting) on error. */
  build(bodyTemplate: string | undefined): HookTarget | null;
}

export type KeyValue = { key: string; value: string };

export function recordToPairs(rec: Record<string, unknown> | undefined): KeyValue[] {
  return rec ? Object.entries(rec).map(([key, value]) => ({ key, value: String(value) })) : [];
}

export function pairsToRecord(pairs: KeyValue[]): Record<string, string> {
  return Object.fromEntries(pairs.filter((p) => p.key.trim()).map((p) => [p.key.trim(), p.value]));
}

export const labelClass = 'block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1';
export const labelSmClass = 'block text-[11px] font-medium text-slate-500 dark:text-slate-400 mb-1';
export const codeClass = 'px-0.5 bg-slate-100 dark:bg-warm-700 rounded text-[10px]';
export const checkboxClass = 'rounded border-slate-300 dark:border-warm-600';
export const addBtnClass =
  'px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer';
export const removeIconBtnClass =
  'p-1 text-slate-400 dark:text-slate-500 hover:text-vermilion-500 cursor-pointer';

/** Badge colors per target type (light + dark). */
export const targetTypeBadge: Record<string, string> = {
  http: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300',
  kafka: 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300',
  redis: 'bg-vermilion-100 text-vermilion-700 dark:bg-vermilion-900/40 dark:text-vermilion-300',
  nats: 'bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-300',
  log: 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300',
};

/** Selected-state styles for the target type picker buttons. */
export const targetTypeSelected: Record<HookTargetType, string> = {
  http: 'bg-emerald-50 border-emerald-300 text-emerald-700 dark:bg-emerald-950/40 dark:border-emerald-700 dark:text-emerald-300',
  kafka: 'bg-purple-50 border-purple-300 text-purple-700 dark:bg-purple-950/40 dark:border-purple-700 dark:text-purple-300',
  redis: 'bg-vermilion-50 border-vermilion-300 text-vermilion-700 dark:bg-vermilion-950/40 dark:border-vermilion-700 dark:text-vermilion-300',
  nats: 'bg-sky-50 border-sky-300 text-sky-700 dark:bg-sky-950/40 dark:border-sky-700 dark:text-sky-300',
  log: 'bg-amber-50 border-amber-300 text-amber-700 dark:bg-amber-950/40 dark:border-amber-700 dark:text-amber-300',
};
