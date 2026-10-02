import { fileURLToPath } from 'node:url';
// Prueba del paquete nativo: no automatiza controles ni captura la pantalla.
import { spawn, execFileSync } from 'node:child_process';
import { mkdtemp, rm, readFile, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { once } from 'node:events';
import { createInterface } from 'node:readline';
import { isolatedEnvironment } from './isolation.mjs';
import assert from 'node:assert/strict';

if (process.platform !== 'darwin') throw new Error('Esta prueba del paquete .app requiere macOS');
const profile = process.argv.includes('--release') ? 'release' : 'debug';
const binary = new URL(`../src-tauri/target/${profile}/bundle/macos/agents-bridge.app/Contents/MacOS/agents-bridge-desktop`, import.meta.url);
const root = await mkdtemp(join(tmpdir(), 'agents-bridge-native-smoke-'));
const env = await isolatedEnvironment(root);
const child = spawn(fileURLToPath(binary), [], { cwd: root, detached: true, env, stdio: ['ignore', 'pipe', 'pipe'] });
const exited = once(child, 'exit');
let ended = false;
exited.then(() => { ended = true; });
let timeout;
try {
  const connected = new Promise((resolve, reject) => {
    timeout = setTimeout(() => reject(new Error('La WebView no completó hello e integración en 60 s')), 60000);
    child.once('error', reject);
    exited.then(() => reject(new Error('La app terminó antes de hello')));
    let hello = '', integrated = false;
    const lines = createInterface({ input: child.stderr });
    lines.on('line', line => {
      if (/^Motor conectado: v\d.* · API v1$/.test(line)) hello = line;
      if (line === 'Integración de hooks comprobada') integrated = true;
      if (integrated && hello) resolve(hello);
    });
  });
  console.log(await connected);
  const cli = join(env.HOME, '.local', 'bin', 'agents-bridge');
  assert.ok((await stat(cli)).mode & 0o111);
  assert.equal(execFileSync(cli, ['--version'], { env, encoding: 'utf8' }).trim(), 'agents-bridge v0.5.1');
  for (const [harness, file] of [['claude', 'settings.json'], ['codex', 'hooks.json']]) {
    const config = JSON.parse(await readFile(join(env.HOME, `.${harness}`, file), 'utf8'));
    for (const event of ['SessionStart', 'UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'Stop']) {
      const entries = config.hooks[event].flatMap(group => group.hooks);
      assert.equal(entries.length, 1);
      assert.ok(entries[0].command.includes(cli));
      assert.ok(entries[0].command.endsWith(`hook ${harness} ${event}`));
    }
  }
  console.log('CLI v0.5.1 y hooks de Claude/Codex instalados solo en HOME temporal: OK');
  console.log('Paquete .app → React invoke → Rust → sidecar Go → hello v1: OK');
} finally {
  clearTimeout(timeout);
  if (!ended) process.kill(-child.pid, 'SIGTERM');
  const killTimeout = setTimeout(() => { if (!ended) process.kill(-child.pid, 'SIGKILL'); }, 3000);
  try { await exited; } finally { clearTimeout(killTimeout); }
  await rm(root, { recursive: true });
}
