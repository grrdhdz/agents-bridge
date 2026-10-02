import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, render, screen, fireEvent, waitFor } from '@testing-library/react';
import Composer from './Composer';
afterEach(cleanup);
test('Ctrl+Enter sends selected label, clears and recovers history', async () => {
  const send=vi.fn().mockResolvedValue(undefined);render(<Composer onSend={send} disabled={false}/>);
  fireEvent.change(screen.getByLabelText('Mensaje'),{target:{value:'Revisa la prueba'}});fireEvent.change(screen.getByLabelText('Etiqueta del mensaje'),{target:{value:'URGENTE'}});
  fireEvent.keyDown(screen.getByLabelText('Mensaje'),{key:'Enter',ctrlKey:true});
  await waitFor(()=>expect(send).toHaveBeenCalledWith('Revisa la prueba','URGENTE'));
  await waitFor(()=>expect((screen.getByLabelText('Mensaje') as HTMLTextAreaElement).value).toBe(''));
  fireEvent.change(screen.getByLabelText('Historial de mensajes'),{target:{value:'0'}});expect((screen.getByLabelText('Mensaje') as HTMLTextAreaElement).value).toBe('Revisa la prueba');
});
test('failed send keeps the draft and displays error; empty never sends',async()=>{
  const send=vi.fn().mockRejectedValue('Motor reiniciándose');render(<Composer onSend={send} disabled={false}/>);
  fireEvent.keyDown(screen.getByLabelText('Mensaje'),{key:'Enter',metaKey:true});expect(send).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText('Mensaje'),{target:{value:'Borrador'}});fireEvent.keyDown(screen.getByLabelText('Mensaje'),{key:'Enter',metaKey:true});
  expect(await screen.findByRole('alert')).toBeDefined();expect((screen.getByLabelText('Mensaje') as HTMLTextAreaElement).value).toBe('Borrador');
});
