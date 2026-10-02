import test from 'node:test';
import assert from 'node:assert/strict';
import { targetConfig } from './sidecar.mjs';

test('sidecar names and Go targets are platform specific', () => {
  assert.deepEqual(targetConfig('aarch64-apple-darwin'), { goos: 'darwin', goarch: 'arm64', filename: 'agents-bridge-aarch64-apple-darwin' });
  assert.deepEqual(targetConfig('x86_64-pc-windows-msvc'), { goos: 'windows', goarch: 'amd64', filename: 'agents-bridge-x86_64-pc-windows-msvc.exe' });
  assert.throws(() => targetConfig('unknown-target'), /Target no soportado/);
});
