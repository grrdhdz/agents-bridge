import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import CommandPalette from './CommandPalette';
afterEach(cleanup);
test('palette filters actions, runs selection and closes', () => {
  const run=vi.fn(), close=vi.fn();
  render(<CommandPalette open onOpenChange={close} actions={[{id:'export', label:'Exportar Markdown', run}, {id:'home', label:'Ir a inicio', run:vi.fn()}]} />);
  fireEvent.change(screen.getByRole('combobox'), {target:{value:'Exportar'}});
  fireEvent.click(screen.getByRole('option', {name:'Exportar Markdown'}));
  expect(run).toHaveBeenCalled(); expect(close).toHaveBeenCalledWith(false);
});
