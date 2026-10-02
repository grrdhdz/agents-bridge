import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { invoke } from '@tauri-apps/api/core';
import App from './App';

vi.mock('@tauri-apps/api/core', () => ({ invoke: vi.fn() }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });

test('the window invokes public hello and displays the real response version', async () => {
  vi.mocked(invoke).mockResolvedValue({ engine_version: 'v9.8.7', contract_version: 1 });
  render(<App />);
  expect(screen.getByText('Conectando con el motor…')).toBeDefined();
  expect(await screen.findByText('agents-bridge v9.8.7 · API v1')).toBeDefined();
  expect(screen.getByText('Motor conectado')).toBeDefined();
  expect(invoke).toHaveBeenCalledExactlyOnceWith('engine_hello');
});

test('a failed hello is visible without a fabricated version', async () => {
  vi.mocked(invoke).mockRejectedValue('El motor no respondió a tiempo');
  render(<App />);
  expect(await screen.findByText('No se pudo conectar')).toBeDefined();
  expect(screen.getByText('El motor no respondió a tiempo')).toBeDefined();
  expect(screen.queryByText('Motor conectado')).toBeNull();
});
