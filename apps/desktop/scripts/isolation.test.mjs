import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, sep } from 'node:path';
import { isolatedEnvironment } from './isolation.mjs';

test('test launches override all home, harness configuration and PATH locations', async () => {
  const root = await mkdtemp(join(tmpdir(), 'agents-bridge-isolation-'));
  try {
    const env = await isolatedEnvironment(root);
    for (const key of ['HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'PATH', 'CODEX_HOME', 'CLAUDE_CONFIG_DIR']) {
      assert.ok(env[key]?.startsWith(root + sep), `${key} points outside the isolated root`);
    }
    assert.equal(env.PATH, join(root, 'bin'));
  } finally { await rm(root, { recursive: true }); }
});
