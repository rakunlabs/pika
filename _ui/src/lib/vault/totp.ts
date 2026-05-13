// TOTP code generation for vault items. Wraps the `otpauth` package
// in a tiny adapter so the rest of the SPA never imports it directly.
//
// Accepts either:
//   - a full otpauth:// URL (the common case — what 1Password / GA
//     emit when "Export to QR")
//   - a bare base32 secret with optional per-item override params
//     (period, digits, algorithm)
//
// Returns the live code and the seconds remaining in the current
// window so the UI can render a countdown bar.

import { TOTP, URI } from 'otpauth';

export interface TOTPField {
  value: string;
  totp_period?: number;
  totp_digits?: number;
  totp_algorithm?: 'SHA1' | 'SHA256' | 'SHA512';
}

export interface TOTPResult {
  code: string;
  period: number;
  remainingSeconds: number;
}

/**
 * Compute the current TOTP code + remaining seconds for the field.
 * Throws on a malformed otpauth URL / unparseable secret so the
 * editor can surface "invalid TOTP secret" inline.
 */
export function computeTOTP(field: TOTPField): TOTPResult {
  const value = (field.value ?? '').trim();
  if (!value) {
    throw new Error('TOTP secret is empty');
  }

  let totp: TOTP;
  if (value.startsWith('otpauth://')) {
    // URI.parse returns either a TOTP or HOTP instance; we only
    // support TOTP because HOTP requires a per-use counter the SPA
    // would have to track and persist.
    const parsed = URI.parse(value);
    if (!(parsed instanceof TOTP)) {
      throw new Error('Only TOTP is supported (HOTP counter URLs are not)');
    }
    totp = parsed;
  } else {
    totp = new TOTP({
      secret: value.replace(/\s+/g, '').toUpperCase(),
      period: field.totp_period ?? 30,
      digits: field.totp_digits ?? 6,
      algorithm: field.totp_algorithm ?? 'SHA1',
    });
  }

  const period = totp.period;
  const code = totp.generate();
  const now = Math.floor(Date.now() / 1000);
  const remainingSeconds = period - (now % period);
  return { code, period, remainingSeconds };
}
