import { createServer } from 'vite';
import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const desktop = fileURLToPath(new URL('../', import.meta.url));
const output = fileURLToPath(new URL('../../../docs/screenshots/desktop/', import.meta.url));
const server = await createServer({ root: desktop, configFile: fileURLToPath(new URL('../vite.config.ts', import.meta.url)), server: { host: '127.0.0.1', port: 0, strictPort: true } });
let browser;
try {
  await server.listen();
  const port = server.httpServer.address().port;
  browser = await chromium.launch({ headless: true });
  await mkdir(output, { recursive: true });
  for (const [width, height] of [[1280, 800], [900, 700]]) {
    for (const theme of ['light', 'dark']) {
      const context = await browser.newContext({ viewport: { width, height }, colorScheme: theme, locale: 'es-MX', timezoneId: 'America/Mexico_City', reducedMotion: 'reduce', deviceScaleFactor: 1 });
      const page = await context.newPage(); const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      await page.goto(`http://127.0.0.1:${port}/?demo=1`);
      await page.getByRole('button', { name: 'Abrir checkout-api' }).waitFor();
      await page.getByLabel('Tema', { exact: true }).selectOption(theme);
      await page.getByRole('heading', { name: 'Tus puentes' }).click();
      if (await page.locator('.bridge-card').count() !== 3) throw new Error('Demo sin tres puentes');
      const prefix = `${theme}-${width}x${height}`;
      await page.screenshot({ path: `${output}home-${prefix}.png` });
      await page.getByRole('button', { name: 'Abrir checkout-api' }).click();
      await page.getByText('Validación completa. Puedes cerrar esta tarea.').waitFor();
      if (await page.locator('.message').count() !== 7) throw new Error('Replay incompleto');
      await page.getByLabel('Estado del puente').waitFor();
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
      if (overflow || errors.length) throw new Error(`Overflow o errores: ${errors.join(', ')}`);
      await page.screenshot({ path: `${output}bridge-${prefix}.png` });
      await page.locator('.message pre').scrollIntoViewIfNeeded();
      if (!await page.locator('.hljs-keyword').count()) throw new Error('Código sin resaltado offline');
      await page.screenshot({ path: `${output}code-${prefix}.png` });
      await page.keyboard.press('Control+k');
      await page.getByRole('dialog', { name: 'Paleta de comandos' }).waitFor();
      await page.screenshot({ path: `${output}palette-${prefix}.png` });
      await page.keyboard.press('Escape');
      await page.getByRole('button', { name: 'Cerrar puente', exact: true }).click();
      await page.getByRole('dialog', { name: '¿Cerrar este puente?' }).waitFor();
      await page.getByRole('button', { name: 'Cancelar', exact: true }).focus();
      await page.keyboard.press('Shift+Tab');
      if (!await page.getByRole('button', { name: 'Cerrar puente', exact: true }).last().evaluate(el => el === document.activeElement)) throw new Error('Foco escapó del diálogo');
      await page.keyboard.press('Escape');
      if (await page.locator('dialog[open]').count()) throw new Error('Esc no canceló');
      console.log(`Capturas ${prefix}: inicio, conversación, código y paleta OK`);
      // Comprueba teclado y vuelta a inicio antes de cerrar el contexto.
      await page.getByLabel('Mensaje', { exact: true }).fill('Intervención de prueba');
      await page.getByLabel('Mensaje', { exact: true }).press('Control+Enter');
      await page.getByRole('log', { name: 'Mensajes' }).getByText('Intervención de prueba', { exact: true }).waitFor();
      await page.getByRole('button', { name: 'Volver a inicio' }).click();
      await page.getByRole('heading', { name: 'Tus puentes' }).waitFor();
      await context.close();
    }
  }
} finally {
  try { await browser?.close(); } finally { await server.close(); }
}
