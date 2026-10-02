import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, render, screen, fireEvent, act, waitFor } from '@testing-library/react';
import App from './App';
import { createDemoClient, demoInstances } from './demo/client';
const saveMock=vi.hoisted(()=>vi.fn());
vi.mock('@tauri-apps/plugin-dialog',()=>({save:saveMock}));

afterEach(() => { cleanup(); vi.useRealTimers(); });
test('home filters, opens a bridge and receives replay without consuming it', async () => {
  const api = createDemoClient(); const unbind = vi.spyOn(api, 'unsubscribe');
  render(<App client={api} />);
  expect(await screen.findByRole('button', { name: 'Abrir checkout-api' })).toBeDefined();
  fireEvent.change(screen.getByLabelText('Filtrar puentes'), { target: { value: 'checkout' } });
  expect(screen.queryByRole('button', { name: 'Abrir docs-site' })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Abrir checkout-api' }));
  expect(await screen.findByText('Revisa el proxy y entrega las pruebas de integración.')).toBeDefined();
  expect(screen.getByLabelText('Estado del puente')).toBeDefined();
  fireEvent.click(screen.getByRole('button', { name: 'Volver a inicio' }));
  expect(await screen.findByRole('heading', { name: 'Tus puentes' })).toBeDefined();
  expect(unbind).toHaveBeenCalled();
});
test('creating uses the API and stop requires a confirmation', async () => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  const api = createDemoClient(); const create = vi.spyOn(api, 'createLocal'); const stop = vi.spyOn(api, 'stop');
  render(<App client={api} />);
  fireEvent.click(await screen.findByRole('button', { name: /Nuevo puente local/ }));
  expect(create).toHaveBeenCalled();
  await screen.findByRole('button', { name: 'Volver a inicio' });
  fireEvent.click(screen.getByRole('button', { name: 'Volver a inicio' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Cerrar checkout-api' }));
  expect(stop).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Cerrar puente' }));
  await screen.findByRole('button', { name: 'Abrir docs-site' });
  expect(stop).toHaveBeenCalledWith({ instance_id: 'demo-checkout-001' });
});

test('home refreshes every two seconds without changing the client', async () => {
  vi.useFakeTimers(); const api = createDemoClient(); const list = vi.spyOn(api, 'list');
  await act(async () => { render(<App client={api}/>); });
  expect(list).toHaveBeenCalledTimes(1);
  await act(async () => { vi.advanceTimersByTime(2000); });
  expect(list).toHaveBeenCalledTimes(2);
});
test('a rejected intervention remains visible and keeps the composer draft', async () => {
  const api = createDemoClient(); vi.spyOn(api, 'send').mockRejectedValue('Canal temporalmente cerrado');
  render(<App client={api}/>);
  fireEvent.click(await screen.findByRole('button', { name: 'Abrir checkout-api' }));
  await screen.findByText('Revisa el proxy y entrega las pruebas de integración.');
  fireEvent.change(screen.getByLabelText('Mensaje'), { target: { value: 'Reintentar esta prueba' } });
  fireEvent.keyDown(screen.getByLabelText('Mensaje'), { key: 'Enter', ctrlKey: true });
  await waitFor(() => expect(screen.getByRole('article', { name: 'Orquestador, RESPUESTA, Rechazado' })).toBeDefined());
  expect((screen.getByLabelText('Mensaje') as HTMLTextAreaElement).value).toBe('Reintentar esta prueba');
});

test('keyboard palette changes theme, search highlights and navigates, help closes with Escape', async () => {
  render(<App client={createDemoClient()}/>);
  fireEvent.click(await screen.findByRole('button',{name:'Abrir checkout-api'}));
  await screen.findByText('Revisa el proxy y entrega las pruebas de integración.');
  fireEvent.keyDown(window,{key:'k',metaKey:true});
  fireEvent.change(screen.getByRole('combobox'),{target:{value:'Tema oscuro'}});
  fireEvent.click(screen.getByRole('option',{name:'Tema oscuro'}));
  expect(document.documentElement.dataset.theme).toBe('dark');
  fireEvent.keyDown(window,{key:'f',ctrlKey:true});
  fireEvent.change(screen.getByLabelText('Buscar en la conversación'),{target:{value:'proxy'}});
  expect(document.querySelectorAll('mark').length).toBeGreaterThan(0);
  expect(document.querySelector('.search-selected')).toBeDefined();
  fireEvent.click(screen.getByRole('button',{name:'Siguiente coincidencia'}));
  expect(screen.getByText('2 de 2 mensajes')).toBeDefined();
  fireEvent.keyDown(screen.getByLabelText('Buscar en la conversación'),{key:'Escape'});
  expect(screen.queryByLabelText('Buscar en la conversación')).toBeNull();
  fireEvent.keyDown(window,{key:'?'});
  expect(screen.getByRole('dialog',{name:'Atajos de teclado'})).toBeDefined();
  fireEvent.keyDown(screen.getByRole('button',{name:'Cerrar ayuda'}),{key:'Escape'});
  expect(screen.queryByRole('dialog')).toBeNull();
});
test('copy exports the full ID, and export uses the public API after native save', async () => {
  const writeText=vi.fn().mockResolvedValue(undefined); Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText}});
  const api=createDemoClient(), exported=vi.spyOn(api,'export'); saveMock.mockResolvedValue('/tmp/conversation.jsonl');
  render(<App client={api}/>);
  fireEvent.click(await screen.findByRole('button',{name:'Copiar ID de checkout-api'}));
  await waitFor(()=>expect(writeText).toHaveBeenCalledWith('demo-checkout-001'));
  expect(await screen.findByText('ID completo copiado')).toBeDefined();
  fireEvent.click(screen.getByRole('button',{name:'Abrir checkout-api'}));
  fireEvent.change(screen.getByLabelText('Formato de exportación'),{target:{value:'jsonl'}});
  await waitFor(()=>expect(exported).toHaveBeenCalledWith({instance_id:'demo-checkout-001',format:'jsonl',output:'/tmp/conversation.jsonl'}));
  expect(await screen.findByText('Conversación exportada')).toBeDefined();
});
test('reconnection and peer changes generate readable notifications', async () => {
  vi.useFakeTimers(); const api=createDemoClient(); let status:(s:{status:'connected'|'restarting'})=>void=()=>{};
  vi.spyOn(api,'onStatus').mockImplementation(async fn=>{status=fn;return ()=>{};});
  let instances=demoInstances(); vi.spyOn(api,'list').mockImplementation(async()=>({instances}));
  await act(async()=>{render(<App client={api}/>);});
  await act(async()=>{status({status:'restarting'});});
  expect(screen.getByText('Reconectando con el motor…')).toBeDefined();
  await act(async()=>{status({status:'connected'});});
  expect(screen.getByText('Motor reconectado; suscripciones recuperadas')).toBeDefined();
  instances=instances.map(i=>({...i,peer_connected:false}));
  await act(async()=>vi.advanceTimersByTime(2000));
  expect(screen.getByText('Ejecutor desconectado · checkout-api')).toBeDefined();
  instances=[]; await act(async()=>vi.advanceTimersByTime(2000));
  expect(screen.getByText('Puente checkout-api cerrado')).toBeDefined();
});

test('gallery starts with create card and tools are named keyboard controls',async()=>{
 render(<App client={createDemoClient()}/>);await screen.findByRole('button',{name:'Abrir checkout-api'});
 expect(document.querySelector('.app-shell')?.classList.contains('chat-background')).toBe(false);
 expect(document.querySelector('.bridge-list')?.firstElementChild?.className).toContain('create-card');
 expect(screen.getByRole('navigation',{name:'Herramientas'})).toBeDefined();
 fireEvent.click(screen.getByRole('button',{name:'Buscar puentes'}));expect(document.activeElement).toBe(screen.getByLabelText('Filtrar puentes'));
});
test('folding sidebar preserves subscription, conversation and draft',async()=>{
 const api=createDemoClient(), subscribe=vi.spyOn(api,'subscribe');render(<App client={api}/>);
 fireEvent.click(await screen.findByRole('button',{name:'Abrir checkout-api'}));await screen.findByText('Revisa el proxy y entrega las pruebas de integración.');
 expect(document.querySelector('.app-shell')?.classList.contains('chat-background')).toBe(true);
 fireEvent.change(screen.getByLabelText('Mensaje'),{target:{value:'Borrador sin enviar'}});
 fireEvent.click(screen.getByRole('button',{name:'Plegar panel lateral'}));
 expect(screen.queryByLabelText('Estado del puente')).toBeNull();expect(subscribe).toHaveBeenCalledTimes(1);
 expect((screen.getByLabelText('Mensaje') as HTMLTextAreaElement).value).toBe('Borrador sin enviar');
 fireEvent.click(screen.getByRole('button',{name:'Mostrar panel lateral'}));expect(screen.getByLabelText('Estado del puente')).toBeDefined();
 expect(subscribe).toHaveBeenCalledTimes(1);
});

test('gallery previews reuse observed messages without subscribing to other bridges',async()=>{
 const api=createDemoClient(),subscribe=vi.spyOn(api,'subscribe'),unsubscribe=vi.spyOn(api,'unsubscribe');
 render(<App client={api}/>);await screen.findByRole('button',{name:'Abrir checkout-api'});
 expect(document.querySelectorAll('.mini-note.empty-note')).toHaveLength(12);
 fireEvent.click(screen.getByRole('button',{name:'Abrir checkout-api'}));
 await screen.findByText('Validación completa. Puedes cerrar esta tarea.');
 fireEvent.click(screen.getByRole('button',{name:'Volver a inicio'}));
 await waitFor(()=>expect(unsubscribe).toHaveBeenCalledTimes(1));
 expect(subscribe).toHaveBeenCalledTimes(1);
 expect(document.querySelectorAll('.mini-note.empty-note')).toHaveLength(8);
 expect(screen.getByRole('button',{name:'Abrir checkout-api'}).querySelector('.mini-note.human-note')).not.toBeNull();
});


test('startup ensures hooks once; indicator opens an accessible panel and toggles through API', async () => {
  vi.useFakeTimers(); const api = createDemoClient(); const ensure = vi.spyOn(api, 'integrationEnsure'); const set = vi.spyOn(api, 'integrationSet');
  await act(async () => { render(<App client={api}/>); });
  expect(ensure).toHaveBeenCalledTimes(1);
  await act(async () => { vi.advanceTimersByTime(6000); });
  expect(ensure).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole('button', { name: 'Hooks: revisar' }));
  expect(screen.getByRole('dialog', { name: 'Hooks de usuario' })).toBeDefined();
  expect(screen.getByText(/acepta los hooks como confiables/)).toBeDefined();
  fireEvent.click(screen.getByRole('switch', { name: 'Mantener hooks instalados — Claude Code' }));
  await act(async () => {});
  expect(set).toHaveBeenCalledWith({ harness: 'claude', enabled: false });
  expect(screen.getByRole('switch', { name: 'Mantener hooks instalados — Claude Code' }).getAttribute('aria-checked')).toBe('false');
  fireEvent.keyDown(screen.getByRole('button', { name: 'Cerrar panel de hooks' }), { key: 'Escape' });
  expect(screen.queryByRole('dialog')).toBeNull();
});
test('failed automatic integration is nonblocking and does not retry in a loop', async () => {
  vi.useFakeTimers(); const api = createDemoClient(); const ensure = vi.spyOn(api, 'integrationEnsure').mockRejectedValue(new Error('No se pudo preparar la integración'));
  await act(async () => { render(<App client={api}/>); });
  expect(screen.getByRole('button', { name: 'Abrir checkout-api' })).toBeDefined();
  expect(screen.getByRole('button', { name: 'Hooks: revisar' })).toBeDefined();
  expect(screen.getByText(/No se pudieron preparar los hooks/)).toBeDefined();
  await act(async () => { vi.advanceTimersByTime(10000); });
  expect(ensure).toHaveBeenCalledTimes(1);
});

test('integration startup runs even when hello fails', async () => {
 const client=createDemoClient(); vi.spyOn(client,'hello').mockRejectedValue(new Error('Hello no disponible'));
 const ensure=vi.spyOn(client,'integrationEnsure'); render(<App client={client}/>);
 await waitFor(()=>expect(ensure).toHaveBeenCalledTimes(1));
});
