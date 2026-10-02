import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
const json=async path=>JSON.parse(await readFile(path,'utf8'));
test('release manifests agree on 0.5.1, platform bundles and app identity',async()=>{
  const pkg=await json('package.json'),cfg=await json('src-tauri/tauri.conf.json');
  assert.equal(pkg.version,'0.5.1');assert.equal(cfg.version,'0.5.1');
  assert.equal(cfg.identifier,'dev.grrdhdz.agents-bridge');assert.equal(cfg.productName,'agents-bridge');
  assert.deepEqual(cfg.bundle.targets,['app','dmg']);
  assert.match(await readFile('src-tauri/Cargo.toml','utf8'),/version = "0\.5\.1"/);
  assert.deepEqual((await json('src-tauri/tauri.windows.conf.json')).bundle.targets,['msi','nsis']);
  assert.match(await readFile('src-tauri/icons/app.svg','utf8'),/agents-bridge/);
});
