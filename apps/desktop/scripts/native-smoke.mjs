// Prueba del paquete nativo: no automatiza controles ni captura la pantalla.
import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { once } from 'node:events';
import { createInterface } from 'node:readline';

if (process.platform !== 'darwin') throw new Error('Esta prueba del paquete .app requiere macOS');
const binary = new URL('../src-tauri/target/debug/bundle/macos/agents-bridge.app/Contents/MacOS/agents-bridge-desktop', import.meta.url);
const root = await mkdtemp(join(tmpdir(), 'agents-bridge-native-smoke-'));
const child = spawn(binary.pathname, [], { cwd: root, detached: true, env: { ...process.env, TMPDIR: root }, stdio: ['ignore', 'pipe', 'pipe'] });
const exited = once(child, 'exit');
let ended = false;
exited.then(() => { ended = true; });
let timeout;
try {
  const connected = new Promise((resolve, reject) => {
    timeout = setTimeout(() => reject(new Error('La WebView no completó hello en 15 s')), 15000);
    child.once('error', reject);
    exited.then(() => reject(new Error('La app terminó antes de hello')));
    const lines = createInterface({ input: child.stderr });
    lines.on('line', line => {
      if (/^Motor conectado: v\d.* · API v1$/.test(line)) resolve(line);
    });
  });
  console.log(await connected);
  console.log('Paquete .app → React invoke → Rust → sidecar Go → hello v1: OK');
} finally {
  clearTimeout(timeout);
  if (!ended) process.kill(-child.pid, 'SIGTERM');
  await exited;
  await rm(root, { recursive: true });
}
