import { afterEach, expect, test } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import BoardPreview from './BoardPreview';
afterEach(cleanup);
test('preview uses known labels and marks human notes yellow',()=>{
 const {container}=render(<BoardPreview notes={[{role:'executor',human:false,label:'RESULTADO'},{role:'orchestrator',human:true,label:'URGENTE'}]}/>);
 expect(screen.getByText('RESULTADO')).toBeDefined();expect(container.querySelector('.mini-note.human-note')).not.toBeNull();
});
test('unknown preview shows neutral notes without invented messages',()=>{
 const {container}=render(<BoardPreview/>);expect(container.querySelectorAll('.mini-note.empty-note')).toHaveLength(4);
 expect(container.textContent).not.toContain('TAREA');
});
