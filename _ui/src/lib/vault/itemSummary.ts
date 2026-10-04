// Cleartext-free helpers for the vault item list. Everything here runs
// on already-decrypted payloads in the browser; nothing is sent to the
// server.

import type { VaultItemField, VaultItemPayload } from './crypto';
import type { VaultItemType } from './api';

function field(p: VaultItemPayload | null, ...types: VaultItemField['type'][]): VaultItemField | undefined {
  if (!p) return undefined;
  for (const t of types) {
    const f = p.fields.find((x) => x.type === t && x.value?.trim());
    if (f) return f;
  }
  return undefined;
}

function byLabel(p: VaultItemPayload | null, label: string): string {
  const l = label.toLowerCase();
  return p?.fields.find((f) => f.label.toLowerCase() === l && f.value?.trim())?.value.trim() ?? '';
}

/** First non-empty username-ish value (username, then email). */
export function usernameOf(p: VaultItemPayload | null): string {
  return field(p, 'username', 'email')?.value.trim() ?? '';
}

/** The primary secret worth a one-click copy. */
export function secretOf(p: VaultItemPayload | null): VaultItemField | undefined {
  return field(p, 'password', 'api_key', 'secret_token', 'card_number', 'connection_string', 'pin');
}

/** First URL, normalized to an absolute http(s) URL, or "". */
export function urlOf(p: VaultItemPayload | null): string {
  const v = field(p, 'url')?.value.trim();
  if (!v) return '';
  const abs = /^[a-z][a-z0-9+.-]*:\/\//i.test(v) ? v : `https://${v}`;
  try {
    const u = new URL(abs);
    return u.protocol === 'http:' || u.protocol === 'https:' ? u.href : '';
  } catch {
    return '';
  }
}

function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, '');
  } catch {
    return '';
  }
}

function lastDigits(v: string, n = 4): string {
  const d = v.replace(/\D/g, '');
  return d.length >= n ? d.slice(-n) : '';
}

/**
 * One short line that identifies the item at a glance, chosen per
 * type. Never includes a sensitive value except the last four card
 * digits, which every card UI shows anyway.
 */
export function itemSubtitle(type: VaultItemType, p: VaultItemPayload | null, title = ''): string {
  if (!p) return '';
  const user = usernameOf(p);
  const host = field(p, 'hostname')?.value.trim() ?? '';
  const join = (...parts: string[]) => parts.filter(Boolean).join(' · ');
  switch (type) {
    case 'login':
      return join(user, hostOf(urlOf(p)));
    case 'server': {
      const addr = host || byLabel(p, 'ip');
      if (!addr) return user;
      return user ? `${user}@${addr}` : addr;
    }
    case 'database': {
      const port = field(p, 'port')?.value.trim();
      const db = byLabel(p, 'database');
      const addr = host ? `${host}${port ? ':' + port : ''}${db ? '/' + db : ''}` : db;
      return join(user, addr);
    }
    case 'card': {
      const num = field(p, 'card_number')?.value ?? '';
      const last = lastDigits(num);
      return join(last ? `•••• ${last}` : '', field(p, 'month_year')?.value.trim() ?? '');
    }
    case 'identity': {
      const name = [byLabel(p, 'first name'), byLabel(p, 'last name')].filter(Boolean).join(' ');
      return join(name, field(p, 'email')?.value.trim() ?? '');
    }
    case 'api_credential':
      return hostOf(urlOf(p)) || field(p, 'url')?.value.trim() || '';
    case 'license':
      return join(byLabel(p, 'product'), byLabel(p, 'version'));
    case 'ssh_key':
    case 'tls_cert':
      return byLabel(p, 'fingerprint');
    case 'secure_note':
      return noteSnippet(p.notes ?? '', title);
  }
  return '';
}

export const UNTITLED_NOTE = 'Untitled note';

function plainLine(l: string): string {
  return l.replace(/^[#>*\-+\s]+|[*_`]+/g, '').trim();
}

/** Title suggestion for a note: its first non-empty line, unformatted. */
export function deriveNoteTitle(src: string): string {
  const line = src.split('\n').map(plainLine).find(Boolean);
  return line ? line.slice(0, 80) : '';
}

/** First note line that isn't just a repeat of the title. */
function noteSnippet(src: string, title: string): string {
  const t = title.trim().toLowerCase();
  const line = src
    .split('\n')
    .map(plainLine)
    .find((l) => l && l.toLowerCase() !== t);
  return line ? line.slice(0, 80) : '';
}

/** Lower-cased text the list search matches against. Never includes secrets. */
export function searchText(p: VaultItemPayload | null): string {
  if (!p) return '';
  const parts: string[] = [];
  for (const f of p.fields) {
    if (f.sensitive || !f.value) continue;
    if (f.type === 'ssh_private_key' || f.type === 'totp') continue;
    parts.push(f.value);
  }
  return parts.join('\n').toLowerCase();
}

export type ExpiryState = 'expired' | 'soon';

/**
 * Parses card "MM/YY" / "MM/YYYY" and ISO dates. A card is valid
 * through the end of its expiry month.
 */
export function expiryDate(type: VaultItemType, p: VaultItemPayload | null): Date | null {
  if (!p) return null;
  if (type === 'card') {
    const v = field(p, 'month_year')?.value.trim() ?? '';
    const m = v.match(/^(\d{1,2})\s*\/\s*(\d{2}|\d{4})$/);
    if (!m) return null;
    const month = Number(m[1]);
    if (month < 1 || month > 12) return null;
    const year = m[2].length === 2 ? 2000 + Number(m[2]) : Number(m[2]);
    return new Date(year, month, 1);
  }
  if (type === 'tls_cert' || type === 'license') {
    const v = field(p, 'date')?.value.trim() ?? '';
    const m = v.match(/^(\d{4})-(\d{2})-(\d{2})/);
    if (!m) return null;
    return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + 1);
  }
  return null;
}

const SOON_MS = 30 * 24 * 60 * 60 * 1000;

export function expiryState(type: VaultItemType, p: VaultItemPayload | null, now = new Date()): ExpiryState | null {
  const end = expiryDate(type, p);
  if (!end) return null;
  const diff = end.getTime() - now.getTime();
  if (diff <= 0) return 'expired';
  if (diff <= SOON_MS) return 'soon';
  return null;
}

/** "just now", "5m ago", "3h ago", "2d ago", or a short date. */
export function relativeTime(iso: string | undefined, now = Date.now()): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  if (s < 30 * 86400) return `${Math.floor(s / 86400)}d ago`;
  return new Date(t).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
}
