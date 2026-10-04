import { describe, expect, it } from 'vitest';
import { estimateStrength, generatePassphrase, generatePassword } from './generator';

const AMBIGUOUS = /[lIO01]/;

describe('generatePassword', () => {
  it('defaults to 20 chars with every class present', () => {
    for (let i = 0; i < 50; i++) {
      const p = generatePassword();
      expect(p).toHaveLength(20);
      expect(p).toMatch(/[a-z]/);
      expect(p).toMatch(/[A-Z]/);
      expect(p).toMatch(/[0-9]/);
      expect(p).toMatch(/[^A-Za-z0-9]/);
      expect(p).not.toMatch(AMBIGUOUS);
    }
  });

  it('clamps length to [8, 256]', () => {
    expect(generatePassword({ length: 1 })).toHaveLength(8);
    expect(generatePassword({ length: 0 })).toHaveLength(8);
    expect(generatePassword({ length: 10_000 })).toHaveLength(256);
    expect(generatePassword({ length: 33 })).toHaveLength(33);
  });

  it('respects disabled character classes', () => {
    for (let i = 0; i < 50; i++) {
      const digitsOnly = generatePassword({ lower: false, upper: false, symbols: false });
      expect(digitsOnly).toMatch(/^[2-9]+$/);
      const lowerOnly = generatePassword({ upper: false, digits: false, symbols: false });
      expect(lowerOnly).toMatch(/^[a-z]+$/);
      expect(lowerOnly).not.toContain('l');
    }
  });

  it('includes ambiguous characters only when allowed', () => {
    let sawAmbiguous = false;
    for (let i = 0; i < 200 && !sawAmbiguous; i++) {
      sawAmbiguous = AMBIGUOUS.test(
        generatePassword({ length: 64, symbols: false, excludeAmbiguous: false }),
      );
    }
    expect(sawAmbiguous).toBe(true);
  });

  it('falls back to lower+digits when every class is disabled', () => {
    const p = generatePassword({ lower: false, upper: false, digits: false, symbols: false });
    expect(p).toMatch(/^[a-z2-9]+$/);
  });

  it('produces distinct values', () => {
    const set = new Set(Array.from({ length: 100 }, () => generatePassword()));
    expect(set.size).toBe(100);
  });
});

describe('generatePassphrase', () => {
  it('uses the requested word count and separator', () => {
    const p = generatePassphrase({ words: 4, separator: '.' });
    expect(p.split('.')).toHaveLength(4);
  });

  it('clamps word count to [3, 16]', () => {
    expect(generatePassphrase({ words: 1 }).split('-')).toHaveLength(3);
    expect(generatePassphrase({ words: 99 }).split('-')).toHaveLength(16);
  });

  it('appends a number without hanging (regression: secureRandIndex(900))', () => {
    for (let i = 0; i < 200; i++) {
      const n = Number(generatePassphrase({ words: 3, appendNumber: true }).split('-')[3]);
      expect(n).toBeGreaterThanOrEqual(100);
      expect(n).toBeLessThanOrEqual(999);
    }
  });

  it('capitalizes and appends a 3-digit number', () => {
    const parts = generatePassphrase({ words: 3, capitalize: true, appendNumber: true }).split('-');
    expect(parts).toHaveLength(4);
    for (const w of parts.slice(0, 3)) expect(w[0]).toBe(w[0].toUpperCase());
    expect(parts[3]).toMatch(/^[1-9]\d{2}$/);
  });
});

describe('estimateStrength', () => {
  it.each([
    ['', 0, 'terrible'],
    ['short1A', 0, 'terrible'],
    ['alllowercaseletters', 0, 'terrible'],
    ['abcdefg1', 1, 'weak'],
    ['abcdefgH1234', 2, 'fair'],
    ['abcdefgH12345678', 3, 'strong'],
    ['abcdefgH12345678!xyz', 4, 'very_strong'],
  ] as const)('%j → %i (%s)', (pwd, score, label) => {
    expect(estimateStrength(pwd)).toEqual({ score, label });
  });

  it('rates generated defaults as very strong', () => {
    expect(estimateStrength(generatePassword()).score).toBe(4);
  });
});
