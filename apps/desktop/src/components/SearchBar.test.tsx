import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import SearchBar from './SearchBar';
afterEach(cleanup);
test('search exposes result count, previous/next and Escape close', () => {
  const next=vi.fn(), close=vi.fn();
  render(<SearchBar query="proxy" count={3} index={1} onQuery={vi.fn()} onMove={next} onClose={close} />);
  expect(screen.getByRole('status').textContent).toBe('2 de 3 mensajes');
  fireEvent.click(screen.getByRole('button', {name:'Siguiente coincidencia'})); expect(next).toHaveBeenCalledWith(1);
  fireEvent.keyDown(screen.getByLabelText('Buscar en la conversación'), {key:'Escape'}); expect(close).toHaveBeenCalled();
});
