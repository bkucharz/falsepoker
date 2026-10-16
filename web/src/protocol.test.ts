import { describe, expect, it } from 'vitest';
import { counter, nameError, pathCode, validCode } from './protocol';

describe('protocol inputs', () => {
  it.each([
    ['', false],
    ['ABCDEF123456', true],
    [' a1b2c3d4e5f6 ', true],
    ['a1b2c3d4e5f', false],
    ['a1b2c3d4e5fg', false],
  ])('validates room code %s', (input, valid) =>
    expect(validCode(input)).toBe(valid),
  );
  it.each(['', '01', '-1', '1.5', '18446744073709551616'])(
    'rejects invalid counter %s',
    (value) => expect(() => counter(value)).toThrow(),
  );
  it('requires string counters even when a JSON number looks valid', () => {
    expect(() => counter(12 as unknown as string)).toThrow();
  });
  it('compares counters beyond Number precision numerically', () => {
    expect(counter('9007199254740993')).toBeGreaterThan(
      counter('9007199254740992'),
    );
    expect(counter('10')).toBeGreaterThan(counter('9'));
  });
  it('counts Unicode names by code point and rejects control characters', () => {
    expect(nameError('😀'.repeat(32))).toBeNull();
    expect(nameError('😀'.repeat(33))).not.toBeNull();
    expect(nameError('A\u0000B')).not.toBeNull();
    expect(nameError('   ')).not.toBeNull();
  });
  it('recognizes invitation routes without accepting arbitrary URLs', () => {
    expect(pathCode('/room/ABCDEF123456')).toBe('abcdef123456');
    expect(pathCode('/room/not-a-code')).toBeNull();
    expect(pathCode('/api/v1/rooms/abcdef123456')).toBeNull();
  });
});
