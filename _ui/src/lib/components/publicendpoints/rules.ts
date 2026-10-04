// Pure helpers for the public-endpoint request-rule editors.

import type {
  CaptureTransform,
  RequestAction,
  RequestActionType,
  RequestRule,
  RequestRuleActionTrace,
  RequestRuleTestResult,
} from '@/lib/types/config';

export type RuleDraft = RequestRule;
export type ActionDraft = RequestAction;
export type CaptureTransformDraft = CaptureTransform;

export function defaultRequestRule(): RuleDraft {
  return {
    name: '',
    enabled: true,
    when: {},
    then: { type: 'allow' },
    actions: [{ type: 'allow' }],
  };
}

// When picker: operator selects ONE matcher shape from a small enum and
// the form materialises the matching field. Multi-matcher (AND) rules
// need multiple rule rows — keeps the per-row UI trivial.
export type WhenKind =
  | 'any'
  | 'method'
  | 'path_equals'
  | 'path_prefix'
  | 'header_equals'
  | 'header_present'
  | 'header_absent'
  | 'query_equals'
  | 'query_present'
  | 'query_absent';

export function whenKindOf(rule: RuleDraft): WhenKind {
  const w = rule.when || {};
  if (w.method) return 'method';
  if (w.path_equals) return 'path_equals';
  if (w.path_prefix) return 'path_prefix';
  if (w.header_equals) return 'header_equals';
  if (w.header_present) return 'header_present';
  if (w.header_absent) return 'header_absent';
  if (w.query_equals) return 'query_equals';
  if (w.query_present) return 'query_present';
  if (w.query_absent) return 'query_absent';
  return 'any';
}

/**
 * Reset the matcher then populate the chosen field with a placeholder
 * value so the form binds without optional-chain surprises. Mutates.
 */
export function setWhenKind(rule: RuleDraft, kind: WhenKind): void {
  rule.when = {};
  switch (kind) {
    case 'any':
      break;
    case 'method':
      rule.when.method = 'GET';
      break;
    case 'path_equals':
      rule.when.path_equals = '/';
      break;
    case 'path_prefix':
      rule.when.path_prefix = '/';
      break;
    case 'header_equals':
      rule.when.header_equals = { name: 'X-Tenant', value: '' };
      break;
    case 'header_present':
      rule.when.header_present = 'X-Tenant';
      break;
    case 'header_absent':
      rule.when.header_absent = 'X-Tenant';
      break;
    case 'query_equals':
      rule.when.query_equals = { name: 'variant', value: '' };
      break;
    case 'query_present':
      rule.when.query_present = 'variant';
      break;
    case 'query_absent':
      rule.when.query_absent = 'variant';
      break;
  }
}

export function effectiveRuleActions(rule: RuleDraft): ActionDraft[] {
  if (rule.actions && rule.actions.length > 0) return rule.actions;
  if (rule.then?.type) return [rule.then];
  return [{ type: 'allow' } satisfies ActionDraft];
}

export function ruleHasAction(rule: RuleDraft, type: RequestActionType): boolean {
  return effectiveRuleActions(rule).some((a) => a.type === type);
}

/**
 * Recreate an action for a new type so stale fields (e.g. a status from
 * a previous "block" choice) don't sneak through when the type changes.
 */
export function actionOfType(action: ActionDraft, t: RequestActionType): ActionDraft {
  switch (t) {
    case 'allow':
      return { type: 'allow' };
    case 'block':
      return {
        type: 'block',
        status: action.status ?? 403,
        body: action.body ?? '',
        content_type: action.content_type ?? 'application/json',
      };
    case 'set_header':
      return { type: 'set_header', name: action.name ?? 'X-Tenant', value: action.value ?? '' };
    case 'del_header':
      return { type: 'del_header', name: action.name ?? 'Cookie' };
    case 'set_query':
      return { type: 'set_query', name: action.name ?? 'variant', value: action.value ?? 'prod' };
    case 'del_query':
      return { type: 'del_query', name: action.name ?? 'debug' };
    case 'set_path':
      return { type: 'set_path', value: action.value ?? '/' };
    case 'replace_path':
      return {
        type: 'replace_path',
        pattern: action.pattern ?? '^/legacy/(.*)$',
        value: action.value ?? '/$1',
        capture_transforms: action.capture_transforms,
      };
  }
}

export function actionSummary(t: ActionDraft): string {
  let then: string = t.type;
  if (t.type === 'block') then = `block ${t.status ?? 403}`;
  else if (t.type === 'set_header') then = `set ${t.name}=${t.value}`;
  else if (t.type === 'del_header') then = `del ${t.name}`;
  else if (t.type === 'set_query') then = `set ?${t.name}=${t.value}`;
  else if (t.type === 'del_query') then = `del ?${t.name}`;
  else if (t.type === 'set_path') then = `path → ${t.value}`;
  else if (t.type === 'replace_path') {
    then = `path s/${t.pattern}/${t.value}/`;
    if (t.capture_transforms?.length) {
      then += ` + ${t.capture_transforms.length} capture transform${t.capture_transforms.length === 1 ? '' : 's'}`;
    }
  }
  return then;
}

export function ruleSummary(rule: RuleDraft): string {
  const w = rule.when || {};
  const parts: string[] = [];
  if (w.method) parts.push(`method=${w.method}`);
  if (w.path_equals) parts.push(`path=${w.path_equals}`);
  if (w.path_prefix) parts.push(`path^=${w.path_prefix}`);
  if (w.header_equals) parts.push(`${w.header_equals.name}=${w.header_equals.value || '?'}`);
  if (w.header_present) parts.push(`has ${w.header_present}`);
  if (w.header_absent) parts.push(`no ${w.header_absent}`);
  if (w.query_equals) parts.push(`?${w.query_equals.name}=${w.query_equals.value || '?'}`);
  if (w.query_present) parts.push(`?${w.query_present}`);
  if (w.query_absent) parts.push(`no ?${w.query_absent}`);
  const when = parts.length ? parts.join(' & ') : 'any';
  const summaries = effectiveRuleActions(rule).map(actionSummary);
  return `when ${when} → ${summaries.join(' → ')}`;
}

export function pathWithQuery(path?: string, rawQuery?: string): string {
  const p = path || '/';
  return rawQuery ? `${p}?${rawQuery}` : p;
}

export function terminalLabel(result: RequestRuleTestResult): string {
  if (result.terminal === 'block') {
    return `blocked with HTTP ${result.block?.status ?? 403}`;
  }
  if (result.terminal === 'allow') {
    return 'allowed by terminal rule';
  }
  return 'allowed by default';
}

export function actionTraceSummary(action: RequestRuleActionTrace): string {
  if (action.type === 'allow') return 'forwarded to the shim';
  if (action.type === 'block') {
    return `blocked with HTTP ${action.block?.status ?? 403}`;
  }
  if (action.type === 'set_header' || action.type === 'del_header') {
    return `${action.header_name}: ${action.header_before || '∅'} → ${action.header_after || '∅'}`;
  }
  if (action.type === 'set_query' || action.type === 'del_query') {
    return `${action.query_name}: ${action.query_before || '∅'} → ${action.query_after || '∅'}`;
  }
  return `${pathWithQuery(action.before_path, action.before_query)} → ${pathWithQuery(action.after_path, action.after_query)}`;
}

/** Compact input used throughout the endpoint editors (inside tier-1.5 panels). */
export const ruleInputClass =
  'px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500';

export const removeBtnClass =
  'p-1 rounded hover:bg-vermilion-50 dark:hover:bg-vermilion-900/40 text-vermilion-600 dark:text-vermilion-400 cursor-pointer';

export const smallNeutralBtnClass =
  'px-1.5 py-0.5 text-[11px] rounded bg-slate-200 dark:bg-warm-700 hover:bg-slate-300 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer inline-flex items-center gap-1';
