import { expect, test } from 'vitest';
import { relativeTime, shortInstance } from './format';
test('heartbeat uses readable relative seconds and minutes', () => {
  const now = Date.parse('2026-10-02T06:00:00Z');
  expect(relativeTime('2026-10-02T05:59:52Z', now)).toBe('hace 8 s');
  expect(relativeTime('2026-10-02T05:57:00Z', now)).toBe('hace 3 min');
  expect(relativeTime(undefined, now)).toBe('—');
  expect(relativeTime('invalid', now)).toBe('—');
});
test('short IDs have eight clean characters without separators', () => {
  expect(shortInstance('demo-checkout-001')).toBe('demochec');
});
