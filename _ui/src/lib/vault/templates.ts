// Per-type item field templates. The "new item" UI picks a template
// based on the chosen type; users can add/remove/reorder fields
// afterwards. Each field id is generated fresh per instantiation so
// two items of the same type don't share field ids.
//
// Mirrors service.KnownVaultItemTypes server-side. If a new type
// is added there, add a template here too.

import type { VaultItemField, VaultItemPayload } from './crypto';
import type { VaultItemType } from './api';

/** Generates a short random id (8 hex chars) for a brand-new field. */
function newFieldID(): string {
  const buf = new Uint8Array(4);
  crypto.getRandomValues(buf);
  return Array.from(buf, b => b.toString(16).padStart(2, '0')).join('');
}

function mkField(partial: Omit<VaultItemField, 'id'>): VaultItemField {
  return { id: newFieldID(), ...partial };
}

/** Default payload for a brand-new item of the given type. */
export function templateFor(type: VaultItemType): VaultItemPayload {
  switch (type) {
    case 'login':
      return {
        fields: [
          mkField({ type: 'username', label: 'Username', value: '' }),
          mkField({ type: 'password', label: 'Password', value: '', sensitive: true }),
          mkField({ type: 'url', label: 'Website', value: '' }),
        ],
      };
    case 'card':
      return {
        fields: [
          mkField({ type: 'text', label: 'Cardholder', value: '' }),
          mkField({ type: 'card_number', label: 'Card number', value: '', sensitive: true }),
          mkField({ type: 'month_year', label: 'Expires', value: '' }),
          mkField({ type: 'cvv', label: 'CVV', value: '', sensitive: true }),
          mkField({ type: 'pin', label: 'PIN', value: '', sensitive: true }),
        ],
      };
    case 'identity':
      return {
        fields: [
          mkField({ type: 'text', label: 'First name', value: '' }),
          mkField({ type: 'text', label: 'Last name', value: '' }),
          mkField({ type: 'email', label: 'Email', value: '' }),
          mkField({ type: 'phone', label: 'Phone', value: '' }),
          mkField({ type: 'address', label: 'Address', value: '' }),
        ],
      };
    case 'secure_note':
      return { fields: [], notes: '' };
    case 'ssh_key':
      return {
        fields: [
          mkField({ type: 'ssh_public_key', label: 'Public key', value: '' }),
          mkField({ type: 'ssh_private_key', label: 'Private key', value: '', sensitive: true }),
          mkField({ type: 'password', label: 'Passphrase', value: '', sensitive: true }),
          mkField({ type: 'text', label: 'Fingerprint', value: '' }),
        ],
      };
    case 'api_credential':
      return {
        fields: [
          mkField({ type: 'url', label: 'Endpoint', value: '' }),
          mkField({ type: 'api_key', label: 'API key', value: '', sensitive: true }),
          mkField({ type: 'secret_token', label: 'API secret', value: '', sensitive: true }),
        ],
      };
    case 'database':
      return {
        fields: [
          mkField({ type: 'hostname', label: 'Host', value: '' }),
          mkField({ type: 'port', label: 'Port', value: '' }),
          mkField({ type: 'text', label: 'Database', value: '' }),
          mkField({ type: 'username', label: 'Username', value: '' }),
          mkField({ type: 'password', label: 'Password', value: '', sensitive: true }),
          mkField({ type: 'connection_string', label: 'Connection string', value: '', sensitive: true }),
        ],
      };
    case 'server':
      return {
        fields: [
          mkField({ type: 'hostname', label: 'Hostname', value: '' }),
          mkField({ type: 'text', label: 'IP', value: '' }),
          mkField({ type: 'port', label: 'SSH port', value: '22' }),
          mkField({ type: 'username', label: 'Username', value: '' }),
          mkField({ type: 'password', label: 'Password', value: '', sensitive: true }),
        ],
      };
    case 'license':
      return {
        fields: [
          mkField({ type: 'text', label: 'Product', value: '' }),
          mkField({ type: 'text', label: 'Version', value: '' }),
          mkField({ type: 'api_key', label: 'License key', value: '', sensitive: true }),
          mkField({ type: 'email', label: 'Support email', value: '' }),
        ],
      };
    case 'tls_cert':
      return {
        fields: [
          mkField({ type: 'ssh_public_key', label: 'Certificate (PEM)', value: '' }),
          mkField({ type: 'ssh_private_key', label: 'Private key (PEM)', value: '', sensitive: true }),
          mkField({ type: 'ssh_public_key', label: 'CA chain (PEM)', value: '' }),
          mkField({ type: 'text', label: 'Fingerprint', value: '' }),
          mkField({ type: 'date', label: 'Expires', value: '' }),
        ],
      };
  }
}

/** A new empty field — used when the user clicks "Add field" in the editor. */
export function newCustomField(): VaultItemField {
  return mkField({ type: 'text', label: 'Field', value: '' });
}

/** Friendly label for the type picker. Mirrors the type vocabulary. */
export function typeLabel(t: VaultItemType): string {
  switch (t) {
    case 'login': return 'Login';
    case 'card': return 'Payment card';
    case 'identity': return 'Identity';
    case 'secure_note': return 'Secure note';
    case 'ssh_key': return 'SSH key';
    case 'api_credential': return 'API credential';
    case 'database': return 'Database';
    case 'server': return 'Server';
    case 'license': return 'Software license';
    case 'tls_cert': return 'TLS certificate';
  }
}

/**
 * Visual accent classes for a vault item type. Returned as full
 * class strings rather than a stem because Tailwind's JIT can't see
 * dynamic concatenations like `bg-${stem}-100` — every class has to
 * appear verbatim somewhere in the source. The DESIGN_SYSTEM.md
 * "Item type → color stripe" table is the source of truth for the
 * mapping; keep them in sync.
 *
 *   `tile`  — bg + text classes for the small rounded icon tile
 *             that fronts each item row / hero header.
 *   `stripe` — left-border accent stripe for the editor hero.
 *   `dot`   — solid bg disc for selection / timeline markers.
 */
export interface VaultItemAccent {
  tile: string;
  stripe: string;
  dot: string;
}

export function vaultItemAccent(t: VaultItemType): VaultItemAccent {
  switch (t) {
    case 'login':
      return {
        tile: 'bg-accent-100 dark:bg-accent-950/40 text-accent-700 dark:text-accent-300',
        stripe: 'border-accent-500 dark:border-accent-400',
        dot: 'bg-accent-500',
      };
    case 'card':
      return {
        tile: 'bg-vermilion-100 dark:bg-vermilion-950/40 text-vermilion-700 dark:text-vermilion-300',
        stripe: 'border-vermilion-500 dark:border-vermilion-400',
        dot: 'bg-vermilion-500',
      };
    case 'identity':
      return {
        tile: 'bg-sky-100 dark:bg-sky-950/40 text-sky-700 dark:text-sky-300',
        stripe: 'border-sky-500 dark:border-sky-400',
        dot: 'bg-sky-500',
      };
    case 'secure_note':
      return {
        tile: 'bg-amber-100 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300',
        stripe: 'border-amber-500 dark:border-amber-400',
        dot: 'bg-amber-500',
      };
    case 'ssh_key':
      return {
        tile: 'bg-purple-100 dark:bg-purple-950/40 text-purple-700 dark:text-purple-300',
        stripe: 'border-purple-500 dark:border-purple-400',
        dot: 'bg-purple-500',
      };
    case 'api_credential':
      return {
        tile: 'bg-indigo-100 dark:bg-indigo-950/40 text-indigo-700 dark:text-indigo-300',
        stripe: 'border-indigo-500 dark:border-indigo-400',
        dot: 'bg-indigo-500',
      };
    case 'database':
      return {
        tile: 'bg-cyan-100 dark:bg-cyan-950/40 text-cyan-700 dark:text-cyan-300',
        stripe: 'border-cyan-500 dark:border-cyan-400',
        dot: 'bg-cyan-500',
      };
    case 'server':
      return {
        tile: 'bg-slate-200 dark:bg-warm-700 text-slate-700 dark:text-slate-200',
        stripe: 'border-slate-500 dark:border-slate-400',
        dot: 'bg-slate-500',
      };
    case 'license':
      return {
        tile: 'bg-emerald-100 dark:bg-emerald-950/40 text-emerald-700 dark:text-emerald-300',
        stripe: 'border-emerald-500 dark:border-emerald-400',
        dot: 'bg-emerald-500',
      };
    case 'tls_cert':
      return {
        tile: 'bg-rose-100 dark:bg-rose-950/40 text-rose-700 dark:text-rose-300',
        stripe: 'border-rose-500 dark:border-rose-400',
        dot: 'bg-rose-500',
      };
  }
}

/**
 * Pull URL hostnames out of a payload for the cleartext index the
 * server stores. The server uses this for the future autofill /
 * browser-extension path. Returns an empty array for items without
 * URL fields.
 */
export function extractHostnames(payload: VaultItemPayload): string[] {
  const out = new Set<string>();
  for (const f of payload.fields) {
    if (f.type !== 'url') continue;
    const v = f.value?.trim();
    if (!v) continue;
    try {
      // Tolerate values without a scheme by prepending https://.
      const u = new URL(/^[a-z][a-z0-9+.\-]*:\/\//i.test(v) ? v : `https://${v}`);
      if (u.hostname) out.add(u.hostname.toLowerCase());
    } catch {
      // Unparseable URL — skip silently. The user can still see the
      // raw value inside the encrypted payload.
    }
  }
  return Array.from(out);
}
