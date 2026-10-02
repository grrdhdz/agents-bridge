import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';

// Launch only the app/sidecar with this environment; build tools keep their own PATH.
export async function isolatedEnvironment(root) {
  const home = join(root, 'home'), bin = join(root, 'bin');
  await Promise.all([home, bin, join(root, 'local'), join(root, 'config')].map(path => mkdir(path, { recursive: true })));
  return { ...process.env, HOME: home, USERPROFILE: home, CODEX_HOME: join(home, ".codex"), CLAUDE_CONFIG_DIR: join(home, ".claude"), LOCALAPPDATA: join(root, 'local'), APPDATA: join(root, 'config'), XDG_CONFIG_HOME: join(root, 'config'), XDG_DATA_HOME: join(root, 'data'), TMPDIR: root, TEMP: root, TMP: root, PATH: bin };
}
