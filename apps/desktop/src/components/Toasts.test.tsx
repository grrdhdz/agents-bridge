import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, act } from '@testing-library/react';
import { Toasts, useToasts } from './Toasts';
afterEach(()=>{cleanup();vi.useRealTimers();});
function Harness(){const {toasts,notify,dismiss}=useToasts();return <><button onClick={()=>notify('ID copiado')}>Copiar</button><Toasts items={toasts} onDismiss={dismiss}/></>;}
test('toasts announce, can be dismissed and expire', () => {
  vi.useFakeTimers(); render(<Harness/>); fireEvent.click(screen.getByText('Copiar'));
  expect(screen.getByRole('status').textContent).toContain('ID copiado');
  fireEvent.click(screen.getByRole('button',{name:'Descartar ID copiado'})); expect(screen.queryByText('ID copiado')).toBeNull();
  fireEvent.click(screen.getByText('Copiar')); act(()=>vi.advanceTimersByTime(8000)); expect(screen.queryByText('ID copiado')).toBeNull();
});
