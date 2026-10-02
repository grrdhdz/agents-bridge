import { execFileSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const desktop = resolve(dirname(fileURLToPath(import.meta.url)), '..');

export function targetConfig(target) {
  const arch = target.startsWith('aarch64-') ? 'arm64' : target.startsWith('x86_64-') ? 'amd64' : undefined;
  const os = target.includes('-apple-darwin') ? 'darwin' : target.includes('-windows-') ? 'windows' : target.includes('-linux-') ? 'linux' : undefined;
  if (!arch || !os) throw new Error(`Target no soportado: ${target}`);
  return { goos: os, goarch: arch, filename: `agents-bridge-${target}${os === 'windows' ? '.exe' : ''}` };
}

export function buildSidecar() {
  const host = execFileSync('rustc', ['-vV'], { encoding: 'utf8' }).match(/^host: (.+)$/m)?.[1];
  const target = process.env.TAURI_ENV_TARGET_TRIPLE || process.env.CARGO_BUILD_TARGET || host;
  const config = targetConfig(target || '');
  const binaries = join(desktop, 'src-tauri', 'binaries');
  mkdirSync(binaries, { recursive: true });
  const output = join(binaries, config.filename);
  execFileSync('go', ['build', '-trimpath', '-o', output, './cmd/agents-bridge'], {
    cwd: resolve(desktop, '../../engine'), stdio: 'inherit',
    env: { ...process.env, GOOS: config.goos, GOARCH: config.goarch, CGO_ENABLED: '0' },
  });
  console.log(`Sidecar ${target}: ${output}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) buildSidecar();
