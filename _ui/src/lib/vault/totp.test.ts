import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { computeTOTP } from './totp';

// RFC 6238 Appendix B test vectors. The seeds are the ASCII strings
// "12345678901234567890" repeated to the HMAC key length, base32-encoded.
const SEED_SHA1 = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ';
const SEED_SHA256 = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQGEZA';
const SEED_SHA512 =
  'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQGEZDGNA';

const VECTORS: Array<[number, string, string, string]> = [
  // [unix seconds, SHA1, SHA256, SHA512]
  [59, '94287082', '46119246', '90693936'],
  [1111111109, '07081804', '68084774', '25091201'],
  [1111111111, '14050471', '67062674', '99943326'],
  [1234567890, '89005924', '91819424', '93441116'],
  [2000000000, '69279037', '90698825', '38618901'],
  [20000000000, '65353130', '77737706', '47863826'],
];

describe('computeTOTP', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it.each(VECTORS)('RFC 6238 vector at T=%i', (t, sha1, sha256, sha512) => {
    vi.setSystemTime(t * 1000);
    expect(
      computeTOTP({ value: SEED_SHA1, totp_digits: 8, totp_algorithm: 'SHA1' }).code,
    ).toBe(sha1);
    expect(
      computeTOTP({ value: SEED_SHA256, totp_digits: 8, totp_algorithm: 'SHA256' }).code,
    ).toBe(sha256);
    expect(
      computeTOTP({ value: SEED_SHA512, totp_digits: 8, totp_algorithm: 'SHA512' }).code,
    ).toBe(sha512);
  });

  it('accepts otpauth:// URLs', () => {
    vi.setSystemTime(59 * 1000);
    const r = computeTOTP({
      value: `otpauth://totp/Pika:me?secret=${SEED_SHA1}&digits=8&algorithm=SHA1&period=30`,
    });
    expect(r.code).toBe('94287082');
    expect(r.period).toBe(30);
  });

  it('defaults to 6 digits / 30s and tolerates whitespace + lowercase', () => {
    vi.setSystemTime(59 * 1000);
    const spaced = SEED_SHA1.toLowerCase().replace(/(.{4})/g, '$1 ');
    const r = computeTOTP({ value: spaced });
    expect(r.code).toBe('287082');
    expect(r.period).toBe(30);
  });

  it('reports seconds remaining in the window', () => {
    vi.setSystemTime(65 * 1000);
    expect(computeTOTP({ value: SEED_SHA1 }).remainingSeconds).toBe(25);
  });

  it('rejects empty secrets and HOTP URLs', () => {
    expect(() => computeTOTP({ value: '  ' })).toThrow(/empty/);
    expect(() =>
      computeTOTP({ value: `otpauth://hotp/x?secret=${SEED_SHA1}&counter=0` }),
    ).toThrow(/TOTP/);
  });
});
