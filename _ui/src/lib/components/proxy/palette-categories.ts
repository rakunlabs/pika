// Per-middleware (and per-handler/switch) sub-category mapping.
//
// Only used by the palette UI to render a coloured left edge on
// each row so an operator can spot "auth gating" vs "path rewrite"
// at a glance without reading the label. The backend doesn't know
// or care about these categories — they live entirely in the UI.
//
// Adding a new node kind: pick the closest bucket below. If none
// fit, drop a new entry into PALETTE_COLOUR_BY_CATEGORY first.

export type PaletteCategory =
  | 'auth'      // basic-auth, auth-bearer, ip lists, header-compare
  | 'tcp-auth'  // TCP source IP access middleware
  | 'path'      // strip-prefix, add-prefix, regex-path
  | 'headers'   // cors, header-inject, header-remove
  | 'traffic'   // ratelimit, timeout, request-size-limit
  | 'transform' // compress, response-rewrite, logger, requestid
  | 'default';  // unknown / new — neutral slate

// Tailwind classes; we use border-l-* on the row so the colour
// shows as a 3px vertical stripe. Both light + dark tokens listed
// so the palette stays legible on either theme.
export const PALETTE_COLOUR_BY_CATEGORY: Record<PaletteCategory, string> = {
  auth:      'border-rose-500 dark:border-rose-400',
  'tcp-auth': 'border-violet-500 dark:border-violet-400',
  path:      'border-amber-500 dark:border-amber-400',
  headers:   'border-sky-500 dark:border-sky-400',
  traffic:   'border-violet-500 dark:border-violet-400',
  transform: 'border-teal-500 dark:border-teal-400',
  default:   'border-slate-300 dark:border-slate-600',
};

// Subtype → category. Unlisted subtypes fall back to 'default'.
// Switches and handlers don't get a category here on purpose: the
// palette already groups them under their own headers with distinct
// icon colours, so the stripe would just add noise.
const MIDDLEWARE_CATEGORY: Record<string, PaletteCategory> = {
  // auth / access gating
  'basic-auth':    'auth',
  'auth-bearer':   'auth',
  'ip-allowlist':  'auth',
  'ip-denylist':   'auth',
  'header-compare': 'auth',

  // path manipulation
  'strip-prefix': 'path',
  'add-prefix':   'path',
  'regex-path':   'path',

  // headers / cross-origin
  cors:            'headers',
  'header-inject': 'headers',
  'header-remove': 'headers',

  // traffic shaping
  ratelimit:           'traffic',
  timeout:             'traffic',
  'request-size-limit': 'traffic',

  // transforms / observability
  compress:          'transform',
  'response-rewrite': 'transform',
  'template-transform': 'transform',
  'js-script':       'transform',
  logger:            'transform',
  requestid:         'transform',
};

export function categoryFor(kind: string, subtype: string | undefined, protocol: string = 'http'): PaletteCategory {
  if (kind !== 'middleware' || !subtype) return 'default';
  if (protocol === 'tcp') return 'tcp-auth';
  return MIDDLEWARE_CATEGORY[subtype] ?? 'default';
}

export function stripeClassFor(kind: string, subtype: string | undefined, protocol: string = 'http'): string {
  return PALETTE_COLOUR_BY_CATEGORY[categoryFor(kind, subtype, protocol)];
}
