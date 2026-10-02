import { readdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';
export function assertDemoExcluded(text) {
  for (const marker of ['DEMO_DATA_ONLY', 'demo-checkout-001', 'demo-sub-', 'v0.5.0-demo', 'Revisa el proxy y entrega las pruebas de integración.']) {
    if (text.includes(marker)) throw new Error(`El demo apareció en producción: ${marker}`);
  }
}
export async function checkProduction(root = new URL('../dist/', import.meta.url)) {
  async function scan(dir) { for (const item of await readdir(dir, { withFileTypes: true })) { const file = join(dir, item.name); if (item.isDirectory()) await scan(file); else if (/\.(js|html|map)$/.test(item.name)) assertDemoExcluded(await readFile(file, 'utf8')); } }
  await scan(typeof root === 'string' ? root : root.pathname);
  console.log('Producción: cliente y datos demo excluidos.');
}
if (process.argv[1]?.endsWith('check-production.mjs')) await checkProduction();
