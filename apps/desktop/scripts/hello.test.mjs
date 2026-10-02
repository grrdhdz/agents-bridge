import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createInterface } from 'node:readline';
import { once } from 'node:events';
import { targetConfig } from './sidecar.mjs';

test('the packaged sidecar answers public hello and exits on EOF', { timeout: 10000 }, async t => {
  const target = execFileSync('rustc', ['-vV'], { encoding: 'utf8' }).match(/^host: (.+)$/m)[1];
  const binary = new URL(`../src-tauri/binaries/${targetConfig(target).filename}`, import.meta.url);
  const root = await mkdtemp(join(tmpdir(), 'agents-bridge-desktop-'));
  const child = spawn(fileURLToPath(binary), ['api'], { env: { ...process.env, TMPDIR: root, LOCALAPPDATA: root }, stdio: ['pipe', 'pipe', 'pipe'] });
  const exited = once(child, 'exit');
  let ended = false;
  exited.then(() => { ended = true; });
  t.after(async () => {
    if (!ended) child.kill();
    await exited;
    await rm(root, { recursive: true });
  });
  const lines = createInterface({ input: child.stdout });
  const response = once(lines, 'line');
  child.stdin.write(JSON.stringify({ v: 1, id: 'desktop-hello', op: 'hello', args: {} }) + '\n');
  const [line] = await response;
  const packet = JSON.parse(line);
  assert.equal(packet.v, 1);
  assert.equal(packet.id, 'desktop-hello');
  assert.equal(packet.ok, true);
  assert.equal(packet.result.contract_version, 1);
  assert.match(packet.result.engine_version, /^v\d/);
  assert.doesNotMatch(line, /capability|token|control_url|descriptors|127\.0\.0\.1/);
  child.stdin.end();
  const [code] = await exited;
  assert.equal(code, 0);
});
