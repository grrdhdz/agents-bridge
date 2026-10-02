import test from 'node:test';
import assert from 'node:assert/strict';
import { readdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';

async function files(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const nested = await Promise.all(entries.map(e => e.isDirectory() ? files(join(dir, e.name)) : [join(dir, e.name)]));
  return nested.flat();
}

test('runtime app only uses the public CLI and generated contract', async () => {
  const paths = [...await files('src'), ...await files('src-tauri/src')];
  for (const path of paths) {
    if (path.endsWith('types.ts')) continue;
    const source = await readFile(path, 'utf8');
    assert.doesNotMatch(source, /engine\/internal|\.\.\/.*engine|control_url|capability|\.agents-bridge|ReadFile|read_to_string|readFile/, path);
  }
  const cargo = await readFile('src-tauri/Cargo.toml', 'utf8');
  assert.doesNotMatch(cargo, /path\s*=/);
});
