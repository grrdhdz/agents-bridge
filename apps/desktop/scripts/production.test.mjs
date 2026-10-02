import test from 'node:test';
import assert from 'node:assert/strict';
import { assertDemoExcluded } from './check-production.mjs';
test('production guard rejects simulated data', () => {
  assert.throws(() => assertDemoExcluded('const fixture = "demo-checkout-001"'), /demo apareció/);
  assert.doesNotThrow(() => assertDemoExcluded('const client = realClient;'));
});
