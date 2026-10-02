import { readFile, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { compile } from 'json-schema-to-typescript';

const schemaUrl = new URL('../../../engine/api/schema.json', import.meta.url);
const outputUrl = new URL('../src/api/types.ts', import.meta.url);
const raw = await readFile(schemaUrl, 'utf8');
const hash = createHash('sha256').update(raw).digest('hex');
const generated = await compile(JSON.parse(raw), 'AgentsBridgePacket', {
  bannerComment: `/* Generado desde engine/api/schema.json. No editar.\n * SHA256: ${hash}\n * npm run generate:types\n */`,
  unreachableDefinitions: true,
  style: { singleQuote: true, semi: true },
});
if (process.argv.includes('--check')) {
  let current = '';
  try { current = await readFile(outputUrl, 'utf8'); } catch { /* archivo ausente */ }
  if (current !== generated) throw new Error('Tipos desactualizados: ejecuta npm run generate:types');
  console.log('Tipos del contrato al día.');
} else {
  await writeFile(outputUrl, generated);
}
