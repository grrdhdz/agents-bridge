import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import EditableName from './EditableName';

afterEach(cleanup);
const label = () => screen.getByTitle('Doble clic o F2 para renombrar');
test('double click edits, Enter saves trimmed name and shows it at once', async () => {
  const onRename = vi.fn().mockResolvedValue(undefined);
  render(<EditableName value="proyecto" placeholder="Sin nombre" onRename={onRename}/>);
  fireEvent.doubleClick(label());
  const input = screen.getByRole('textbox', { name: 'Nombre del puente' });
  fireEvent.change(input, { target: { value: '  Pagos  ' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  expect(onRename).toHaveBeenCalledWith('Pagos');
  expect(label().textContent).toBe('Pagos');
});
test('F2 edits, Escape cancels without saving', () => {
  const onRename = vi.fn().mockResolvedValue(undefined);
  render(<EditableName value="proyecto" placeholder="Sin nombre" onRename={onRename}/>);
  fireEvent.keyDown(label(), { key: 'F2' });
  const input = screen.getByRole('textbox', { name: 'Nombre del puente' });
  fireEvent.change(input, { target: { value: 'Otro' } });
  fireEvent.keyDown(input, { key: 'Escape' });
  expect(onRename).not.toHaveBeenCalled();
  expect(label().textContent).toBe('proyecto');
});
test('blur saves once; unchanged name is not sent; rejection reverts', async () => {
  const onRename = vi.fn().mockRejectedValue(new Error('no'));
  render(<EditableName value="proyecto" placeholder="Sin nombre" onRename={onRename}/>);
  fireEvent.doubleClick(label());
  fireEvent.blur(screen.getByRole('textbox'));
  expect(onRename).not.toHaveBeenCalled();
  fireEvent.doubleClick(label());
  const input = screen.getByRole('textbox');
  fireEvent.change(input, { target: { value: 'Nuevo' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  fireEvent.blur(input);
  expect(onRename).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(label().textContent).toBe('proyecto'));
});
test('input respects the 64 character limit', () => {
  render(<EditableName value="p" placeholder="Sin nombre" onRename={vi.fn()}/>);
  fireEvent.doubleClick(label());
  expect(screen.getByRole('textbox').getAttribute('maxLength')).toBe('64');
});
