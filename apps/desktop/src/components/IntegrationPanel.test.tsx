import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import IntegrationPanel, { hooksNeedReview } from './IntegrationPanel';
import { createDemoClient } from '../demo/client';

afterEach(cleanup);
test('review distinguishes trust, opted out and actionable failures', async () => {
  const s = await createDemoClient().integrationStatus();
  expect(hooksNeedReview(s)).toBe(true);
  s.harnesses.codex.trust_note = undefined;
  expect(hooksNeedReview(s)).toBe(false);
  s.harnesses.codex.installed = false; s.harnesses.codex.opted_out = true;
  expect(hooksNeedReview(s)).toBe(false);
  s.harnesses.claude.error = 'JSON inválido';
  expect(hooksNeedReview(s)).toBe(true);
});
test('panel shows errors and PATH notes; failed toggle keeps known state and focus is trapped', async () => {
  const client = createDemoClient(), status = await client.integrationStatus(), update = vi.fn();
  status.cli_on_path = false; status.path_note = 'Windows: abre una terminal nueva para usar el PATH actualizado.';
  status.cli_error = 'Destino ajeno'; status.harnesses.claude.error = 'JSON inválido; archivo intacto';
  vi.spyOn(client, 'integrationSet').mockRejectedValue(new Error('Permiso denegado'));
  render(<IntegrationPanel client={client} status={status} onUpdate={update} onClose={()=>{}}/>);
  expect(screen.getByText('Fuera del PATH')).toBeDefined();
  expect(screen.getByText(/terminal nueva/)).toBeDefined();
  expect(screen.getAllByRole('alert')).toHaveLength(2);
  const box = screen.getByRole('checkbox', { name: 'Mantener hooks instalados — Claude Code' });
  fireEvent.click(box);
  await waitFor(()=>expect(screen.getByText(/No se pudo cambiar la integración/)).toBeDefined());
  expect(update).not.toHaveBeenCalled(); expect((box as HTMLInputElement).checked).toBe(true);
  const close = screen.getByRole('button', { name: 'Cerrar panel de hooks' }); close.focus();
  fireEvent.keyDown(close, {key:'Tab'}); expect(document.activeElement).toBe(box);
});
