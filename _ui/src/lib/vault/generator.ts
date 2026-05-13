// Password / passphrase generation. Runs entirely in the browser
// using crypto.getRandomValues so no network round-trip is needed —
// the server has no insight into generated values.
//
// The character classes are toggled independently and at least one
// must be active. Output length is clamped to [8, 256] so a UI
// slider bug can't produce a 0-length password or an absurd one.

const LOWER = 'abcdefghijkmnopqrstuvwxyz'; // skip "l"
const UPPER = 'ABCDEFGHJKLMNPQRSTUVWXYZ'; // skip "I" and "O"
const DIGITS = '23456789'; // skip "0" and "1"
const SYMBOLS = '!@#$%^&*()-_=+[]{};:,.<>?/~';

// Confusable variants we add when the user UNSETS excludeAmbiguous.
const LOWER_AMBI = 'l';
const UPPER_AMBI = 'IO';
const DIGITS_AMBI = '01';

export interface PasswordOptions {
  length?: number;
  lower?: boolean;
  upper?: boolean;
  digits?: boolean;
  symbols?: boolean;
  excludeAmbiguous?: boolean;
}

export function generatePassword(opts: PasswordOptions = {}): string {
  const length = clamp(opts.length ?? 20, 8, 256);
  const useLower = opts.lower ?? true;
  const useUpper = opts.upper ?? true;
  const useDigits = opts.digits ?? true;
  const useSymbols = opts.symbols ?? true;
  const exclude = opts.excludeAmbiguous ?? true;

  // Build the working alphabet. Each class contributes its base set;
  // the ambiguous-variants are mixed in only when the user opted out
  // of the exclusion.
  let alphabet = '';
  if (useLower) alphabet += LOWER + (exclude ? '' : LOWER_AMBI);
  if (useUpper) alphabet += UPPER + (exclude ? '' : UPPER_AMBI);
  if (useDigits) alphabet += DIGITS + (exclude ? '' : DIGITS_AMBI);
  if (useSymbols) alphabet += SYMBOLS;
  if (!alphabet) {
    // The UI should prevent this state; if it leaks through, fall
    // back to lower+digits so we never panic the caller.
    alphabet = LOWER + DIGITS;
  }

  // Rejection sampling to avoid modulo bias when alphabet length
  // doesn't divide 256 evenly. Burns at most a few extra bytes per
  // character; cheap.
  const out: string[] = [];
  const buf = new Uint8Array(length * 2);
  const ceil = 256 - (256 % alphabet.length);
  while (out.length < length) {
    crypto.getRandomValues(buf);
    for (let i = 0; i < buf.length && out.length < length; i++) {
      const b = buf[i];
      if (b >= ceil) continue;
      out.push(alphabet[b % alphabet.length]);
    }
  }
  // Ensure at least one of each requested class appears. The
  // shuffle-fix preserves the random distribution while satisfying
  // the constraint cheaply for typical lengths (≥ 8).
  return enforceClasses(out.join(''), { useLower, useUpper, useDigits, useSymbols, exclude });
}

function enforceClasses(
  pwd: string,
  cfg: { useLower: boolean; useUpper: boolean; useDigits: boolean; useSymbols: boolean; exclude: boolean },
): string {
  const need: string[] = [];
  if (cfg.useLower && !/[a-z]/.test(pwd)) need.push(pick(LOWER + (cfg.exclude ? '' : LOWER_AMBI)));
  if (cfg.useUpper && !/[A-Z]/.test(pwd)) need.push(pick(UPPER + (cfg.exclude ? '' : UPPER_AMBI)));
  if (cfg.useDigits && !/[0-9]/.test(pwd)) need.push(pick(DIGITS + (cfg.exclude ? '' : DIGITS_AMBI)));
  if (cfg.useSymbols && !/[^A-Za-z0-9]/.test(pwd)) need.push(pick(SYMBOLS));
  if (!need.length) return pwd;
  const arr = pwd.split('');
  // Replace random positions with the required characters.
  const positions = new Set<number>();
  while (positions.size < need.length) {
    positions.add(secureRandIndex(arr.length));
  }
  let i = 0;
  for (const p of positions) {
    arr[p] = need[i++];
  }
  return arr.join('');
}

function pick(alphabet: string): string {
  return alphabet[secureRandIndex(alphabet.length)];
}

function secureRandIndex(max: number): number {
  // Same rejection-sampling pattern, single byte at a time.
  const buf = new Uint8Array(1);
  const ceil = 256 - (256 % max);
  // eslint-disable-next-line no-constant-condition
  while (true) {
    crypto.getRandomValues(buf);
    if (buf[0] < ceil) return buf[0] % max;
  }
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, n));
}

// ─── Passphrase ──────────────────────────────────────────────────

// Short embedded wordlist (~100 words). The full EFF diceware list is
// ~7800 words but adds ~80KB to the bundle; this trimmed set gives
// ~6.6 bits of entropy per word, so a 5-word passphrase carries ~33
// bits — fine for a memorable secondary password but not the master.
// We include the count check + a warning in the UI so the user knows
// to favor length here.
const WORDS = [
  'apple','amber','angel','arrow','atlas','azure','badge','baker','beach','belly',
  'block','blush','bonus','brick','brisk','cabin','candy','canon','carbon','chess',
  'cider','clock','clover','crane','crisp','crown','daily','deltas','dimmer','disco',
  'dragon','dream','eager','earth','ember','epoch','ether','event','fable','falcon',
  'fancy','feast','field','flame','flint','forge','frost','globe','gleam','gnome',
  'grain','grape','green','happy','harbor','heron','honey','hover','humor','ivory',
  'jelly','jolly','kayak','kilo','linen','lobby','lunar','mango','marble','meadow',
  'melon','minor','mirth','mocha','myth','noble','north','ocean','olive','onion',
  'opal','orbit','otter','panda','pearl','penny','plank','plum','polar','prism',
  'queen','quiet','quill','radar','raven','rebel','river','rocky','ruby','salt',
];

export interface PassphraseOptions {
  words?: number;
  separator?: string;
  capitalize?: boolean;
  appendNumber?: boolean;
}

export function generatePassphrase(opts: PassphraseOptions = {}): string {
  const count = clamp(opts.words ?? 5, 3, 16);
  const sep = opts.separator ?? '-';
  const out: string[] = [];
  for (let i = 0; i < count; i++) {
    const w = WORDS[secureRandIndex(WORDS.length)];
    out.push(opts.capitalize ? w[0].toUpperCase() + w.slice(1) : w);
  }
  let phrase = out.join(sep);
  if (opts.appendNumber) {
    phrase += sep + String(secureRandIndex(900) + 100); // 3-digit
  }
  return phrase;
}

// ─── Password strength estimator ─────────────────────────────────

/**
 * Lightweight strength estimator. Returns a 0..4 score and a
 * suggested label. This is NOT a zxcvbn replacement — it doesn't
 * detect dictionary words, leet substitutions, common patterns,
 * etc. Use it to flag obvious weakness ("too short", "only lower")
 * in the editor; for serious password review the user should rely
 * on a dedicated tool.
 *
 * The score map:
 *  0 — terrible (< 8 chars OR single class)
 *  1 — weak (≥ 8 chars, ≥ 2 classes)
 *  2 — fair (≥ 12 chars, ≥ 3 classes)
 *  3 — strong (≥ 16 chars, ≥ 3 classes)
 *  4 — very strong (≥ 20 chars, 4 classes)
 */
export interface StrengthEstimate {
  score: 0 | 1 | 2 | 3 | 4;
  label: 'terrible' | 'weak' | 'fair' | 'strong' | 'very_strong';
}

export function estimateStrength(pwd: string): StrengthEstimate {
  if (!pwd) return { score: 0, label: 'terrible' };
  const classes =
    (/[a-z]/.test(pwd) ? 1 : 0) +
    (/[A-Z]/.test(pwd) ? 1 : 0) +
    (/[0-9]/.test(pwd) ? 1 : 0) +
    (/[^A-Za-z0-9]/.test(pwd) ? 1 : 0);

  if (pwd.length < 8 || classes < 2) return { score: 0, label: 'terrible' };
  if (pwd.length >= 20 && classes === 4) return { score: 4, label: 'very_strong' };
  if (pwd.length >= 16 && classes >= 3) return { score: 3, label: 'strong' };
  if (pwd.length >= 12 && classes >= 3) return { score: 2, label: 'fair' };
  return { score: 1, label: 'weak' };
}
