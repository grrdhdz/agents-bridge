export type ThemeMode = 'auto' | 'light' | 'dark';
// Paleta semántica alineada con la TUI; no depende de sus archivos en runtime.
export const palettes = {
  light: { surface: '#dce3ee', panel: '#d0d9e7', card: '#e5ebf4', text: '#0f172a', muted: '#334155', border: '#64748b', orchestrator: '#1e40af', executor: '#166534', human: '#92400e', info: '#075985', warning: '#854d0e', danger: '#b91c1c', notice: '#5b21b6', codeKeyword: '#1e40af', codeString: '#166534', codeNumber: '#5b21b6', codeComment: '#334155', codeAttr: '#92400e', codeLiteral: '#075985', onAccent: '#ffffff' },
  dark: { surface: '#0b1220', panel: '#16233a', card: '#101c2f', text: '#e2e8f0', muted: '#94a3b8', border: '#718198', orchestrator: '#93c5fd', executor: '#86efac', human: '#fbbf24', info: '#38bdf8', warning: '#facc15', danger: '#f87171', notice: '#c4b5fd', codeKeyword: '#93c5fd', codeString: '#86efac', codeNumber: '#c4b5fd', codeComment: '#94a3b8', codeAttr: '#fbbf24', codeLiteral: '#38bdf8', onAccent: '#0b1220' },
};
export function applyTheme(mode: ThemeMode) {
  const resolved = mode === 'auto' ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : mode;
  document.documentElement.dataset.theme = resolved;
  for (const [name, value] of Object.entries(palettes[resolved])) document.documentElement.style.setProperty(`--${name}`, value);
  document.documentElement.style.colorScheme = resolved;
}
