import { afterEach, expect, test } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import MarkdownBody from './MarkdownBody';
afterEach(cleanup);
test('renders markdown and offline highlighted code without executing HTML or loading images', () => {
  const {container} = render(<MarkdownBody body={'**Importante**\n\n```js\nconst answer = 42;\n```\n<script>alert(1)</script>\n![image](https://example.com/private.png)'} />);
  expect(screen.getByText('Importante').tagName).toBe('STRONG');
  expect(container.querySelector('.hljs-keyword')?.textContent).toBe('const');
  expect(container.querySelector('script')).toBeNull();
  expect(container.querySelector('img')).toBeNull();
});
test('folds after 30 lines and expands without losing content', () => {
  const body = Array.from({length:35}, (_,i) => `Línea ${i+1}`).join('\n');
  render(<MarkdownBody body={body} />);
  expect(screen.queryByText(/Línea 35/)).toBeNull();
  fireEvent.click(screen.getByRole('button', {name:'Mostrar 5 líneas más'}));
  expect(screen.getByText(/Línea 35/)).toBeDefined();
  expect(screen.getByRole('button', {name:'Mostrar menos'}).getAttribute('aria-expanded')).toBe('true');
});
test('search highlights literal text, including code, and reveals folded matches', () => {
  const {container} = render(<MarkdownBody body={'Buscar [x]\n```js\nconst x = "[x]";\n```'} query="[x]" />);
  expect(container.querySelectorAll('mark')).toHaveLength(2);
});
