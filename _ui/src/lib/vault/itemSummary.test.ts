import { describe, expect, it } from 'vitest';
import type { VaultItemField, VaultItemPayload } from './crypto';
import {
  deriveNoteTitle,
  expiryState,
  itemSubtitle,
  relativeTime,
  searchText,
  secretOf,
  urlOf,
  usernameOf,
} from './itemSummary';

let seq = 0;
function f(type: VaultItemField['type'], label: string, value: string, sensitive = false): VaultItemField {
  return { id: String(seq++), type, label, value, sensitive };
}
const payload = (...fields: VaultItemField[]): VaultItemPayload => ({ fields });

describe('itemSubtitle', () => {
  it('login shows user and bare host', () => {
    const p = payload(f('username', 'Username', 'ray'), f('password', 'Password', 'x', true), f('url', 'Website', 'www.github.com/login'));
    expect(itemSubtitle('login', p)).toBe('ray · github.com');
  });

  it('server shows user@host', () => {
    const p = payload(f('hostname', 'Hostname', 'db1.local'), f('username', 'Username', 'root'));
    expect(itemSubtitle('server', p)).toBe('root@db1.local');
  });

  it('database shows host:port/db', () => {
    const p = payload(
      f('hostname', 'Host', 'pg'),
      f('port', 'Port', '5432'),
      f('text', 'Database', 'app'),
      f('username', 'Username', 'u'),
    );
    expect(itemSubtitle('database', p)).toBe('u · pg:5432/app');
  });

  it('card shows only the last four digits', () => {
    const p = payload(f('card_number', 'Card number', '4242 4242 4242 1234', true), f('month_year', 'Expires', '08/27'));
    const s = itemSubtitle('card', p);
    expect(s).toBe('•••• 1234 · 08/27');
    expect(s).not.toContain('4242');
  });

  it('note uses the first non-empty line, stripped of markdown', () => {
    expect(itemSubtitle('secure_note', { fields: [], notes: '\n# Wifi codes\nmore' })).toBe('Wifi codes');
  });

  it('note skips a first line that repeats the title', () => {
    expect(itemSubtitle('secure_note', { fields: [], notes: '# Wifi codes\n\nguest: **abc**' }, 'Wifi codes')).toBe(
      'guest: abc',
    );
  });

  it('derives a note title from its first line', () => {
    expect(deriveNoteTitle('\n\n## *Server* notes\nbody')).toBe('Server notes');
    expect(deriveNoteTitle('   ')).toBe('');
  });

  it('empty payload gives empty subtitle', () => {
    expect(itemSubtitle('login', null)).toBe('');
    expect(itemSubtitle('login', payload())).toBe('');
  });
});

describe('quick-copy helpers', () => {
  it('falls back from username to email', () => {
    expect(usernameOf(payload(f('email', 'Email', 'a@b.c')))).toBe('a@b.c');
  });

  it('picks the first non-empty secret', () => {
    const p = payload(f('password', 'Password', '', true), f('api_key', 'Key', 'k', true));
    expect(secretOf(p)?.value).toBe('k');
  });

  it('only returns http(s) urls', () => {
    expect(urlOf(payload(f('url', 'Website', 'example.com')))).toBe('https://example.com/');
    expect(urlOf(payload(f('url', 'Website', 'javascript:alert(1)')))).toBe('');
  });
});

describe('searchText', () => {
  it('excludes sensitive values', () => {
    const p = payload(f('username', 'Username', 'Ray'), f('password', 'Password', 'hunter2', true));
    const t = searchText(p);
    expect(t).toContain('ray');
    expect(t).not.toContain('hunter2');
  });
});

describe('expiryState', () => {
  const now = new Date(2026, 9, 4);
  it('card is valid through its expiry month', () => {
    expect(expiryState('card', payload(f('month_year', 'Expires', '10/26')), now)).toBe('soon');
    expect(expiryState('card', payload(f('month_year', 'Expires', '09/26')), now)).toBe('expired');
    expect(expiryState('card', payload(f('month_year', 'Expires', '12/2027')), now)).toBeNull();
  });

  it('certificate date', () => {
    expect(expiryState('tls_cert', payload(f('date', 'Expires', '2026-10-20')), now)).toBe('soon');
    expect(expiryState('tls_cert', payload(f('date', 'Expires', 'garbage')), now)).toBeNull();
  });
});

describe('relativeTime', () => {
  const now = Date.parse('2026-10-04T12:00:00Z');
  it('formats buckets', () => {
    expect(relativeTime('2026-10-04T11:59:30Z', now)).toBe('just now');
    expect(relativeTime('2026-10-04T11:00:00Z', now)).toBe('1h ago');
    expect(relativeTime('2026-10-01T12:00:00Z', now)).toBe('3d ago');
    expect(relativeTime(undefined, now)).toBe('');
  });
});
