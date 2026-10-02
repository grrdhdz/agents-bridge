import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, render, screen, fireEvent, act, waitFor } from '@testing-library/react';
import App from './App';
import { createDemoClient } from './demo/client';

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
