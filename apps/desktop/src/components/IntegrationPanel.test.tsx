import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import IntegrationPanel, { hooksNeedReview } from './IntegrationPanel';
import { createDemoClient } from '../demo/client';
import { palettes } from '../theme';

afterEach(cleanup);
function luminance(hex:string) { const c=hex.slice(1).match(/../g)!.map(v=>parseInt(v,16)/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4); return c[0]*.2126+c[1]*.7152+c[2]*.0722; }
function ratio(a:string,b:string) { const x=luminance(a),y=luminance(b); return (Math.max(x,y)+.05)/(Math.min(x,y)+.05); }
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
  const box = screen.getByRole('switch', { name: 'Mantener hooks instalados — Claude Code' });
  box.focus(); fireEvent.keyDown(box, { key: ' ' }); fireEvent.click(box);
  await waitFor(()=>expect(screen.getByText(/No se pudo cambiar la integración/)).toBeDefined());
  expect(update).not.toHaveBeenCalled(); expect(box.getAttribute('aria-checked')).toBe('true');
  const lastSwitch=screen.getByRole('switch',{name:'Mantener hooks instalados — Codex'}); lastSwitch.focus();
  fireEvent.keyDown(lastSwitch,{key:'Tab'}); expect(document.activeElement).toBe(screen.getByRole('button',{name:'Cerrar panel de hooks'}));
});


test('switch has Space and Enter button activation; status badges expose all states', async () => {
  const client = createDemoClient(), status = await client.integrationStatus(), onClose=vi.fn();
  const set=vi.spyOn(client,'integrationSet');
  status.harnesses.claude.error = 'configuración inválida';
  const {rerender}=render(<IntegrationPanel client={client} status={status} onUpdate={next=>{status.harnesses=next.harnesses;}} onClose={onClose}/>);
  expect(screen.getByText('Error')).toBeDefined();
  expect(screen.getByText('Revisar')).toBeDefined();
  status.harnesses.claude.error=undefined;
  rerender(<IntegrationPanel client={client} status={status} onUpdate={next=>{status.harnesses=next.harnesses;}} onClose={onClose}/>);
  expect(screen.getByText('Instalados')).toBeDefined();
  status.harnesses.claude.opted_out=true;
  rerender(<IntegrationPanel client={client} status={status} onUpdate={next=>{status.harnesses=next.harnesses;}} onClose={onClose}/>);
  expect(screen.getByText('Desactivados')).toBeDefined();
  status.harnesses.claude.opted_out=false;
  rerender(<IntegrationPanel client={client} status={status} onUpdate={next=>{status.harnesses=next.harnesses;}} onClose={onClose}/>);
  const toggle=screen.getByRole('switch',{name:'Mantener hooks instalados — Codex'});
  toggle.focus(); fireEvent.keyDown(toggle,{key:'Enter'}); fireEvent.click(toggle);
  await waitFor(()=>expect(toggle.getAttribute('aria-checked')).toBe('false'));
  expect(set).toHaveBeenCalledWith({harness:'codex',enabled:false});
  const close=screen.getByRole('button',{name:'Cerrar panel de hooks'});
  fireEvent.keyDown(close,{key:'Escape'}); expect(onClose).toHaveBeenCalled();
});

test('state badges and callout backgrounds keep WCAG contrast in both themes', () => {
  for (const p of Object.values(palettes)) {
    for (const [ink,fill] of [[p.hookInstalled,p.hookInstalledBg],[p.hookReview,p.hookReviewBg],[p.hookDisabled,p.hookDisabledBg],[p.hookError,p.hookErrorBg]]) {
      expect(ratio(ink,fill)).toBeGreaterThanOrEqual(4.5);
      expect(ratio(p.text,fill)).toBeGreaterThanOrEqual(4.5);
      expect(ratio(p.border,fill)).toBeGreaterThanOrEqual(3);
      expect(ratio(p.focus,fill)).toBeGreaterThanOrEqual(3);
    }
  }
});
