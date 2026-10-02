import { afterEach, expect, test, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import Confirm from './Confirm';
afterEach(cleanup);
test('confirmation traps focus, Escape cancels and restores previous focus', () => {
  HTMLDialogElement.prototype.showModal=function(){this.setAttribute('open','');};
  const cancel=vi.fn(), confirm=vi.fn(); const previous=document.createElement('button'); document.body.append(previous); previous.focus();
  const view=render(<Confirm name="instance" busy={false} onCancel={cancel} onConfirm={confirm}/>);
  const first=screen.getByRole('button',{name:'Cancelar'}), last=screen.getByRole('button',{name:'Cerrar puente'});
  last.focus(); fireEvent.keyDown(last,{key:'Tab'}); expect(document.activeElement).toBe(first);
  first.focus(); fireEvent.keyDown(first,{key:'Tab',shiftKey:true}); expect(document.activeElement).toBe(last);
  fireEvent.keyDown(last,{key:'Escape'}); expect(cancel).toHaveBeenCalled(); expect(confirm).not.toHaveBeenCalled();
  view.unmount(); expect(document.activeElement).toBe(previous); previous.remove();
});
