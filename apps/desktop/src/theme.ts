export type ThemeMode = 'auto' | 'light' | 'dark';
// Tokens de contraste compartidos por el chat, las superficies y las notas.
export const palettes = {
  light: {
    surface:'#f7f5ef', chatBg:'#efe9df', panel:'#ffffff', card:'#ffffff', text:'#202124', muted:'#4b4f58', border:'#747a85',
    hookInstalled:'#226143', hookInstalledBg:'#e0f0df', hookReview:'#795700', hookReviewBg:'#fff1b7', hookDisabled:'#4b4f58', hookDisabledBg:'#e6e5df', hookError:'#a32632', hookErrorBg:'#fbe2e3',
    orchestrator:'#2548a8', executor:'#226143', human:'#75520c', info:'#214fc4', warning:'#795700', danger:'#a32632', notice:'#6b3f94',
    noteOrchestrator:'#dfeaff', noteExecutor:'#e0f0df', noteHuman:'#fff1b7', noteEmpty:'#e6e5df', codeBg:'#f1eee6',
    accent:'#f4c844', onAccent:'#272218', selection:'#3153bb', onSelection:'#ffffff', onDanger:'#ffffff', focus:'#2455d6', dot:'#dedbd2',
    codeKeyword:'#2548a8', codeString:'#226143', codeNumber:'#6b3f94', codeComment:'#4b4f58', codeAttr:'#75520c', codeLiteral:'#214fc4',
  },
  dark: {
    surface:'#191b20', chatBg:'#17212b', panel:'#252830', card:'#2b2e37', text:'#f7f4ec', muted:'#c7c9d0', border:'#969ba8',
    hookInstalled:'#b6e6b8', hookInstalledBg:'#304838', hookReview:'#ffe493', hookReviewBg:'#504727', hookDisabled:'#c7c9d0', hookDisabledBg:'#3b3d46', hookError:'#ffb2bb', hookErrorBg:'#522c34',
    orchestrator:'#abc9ff', executor:'#b6e6b8', human:'#ffe493', info:'#abc2ff', warning:'#ffe493', danger:'#ffb2bb', notice:'#d4bafa',
    noteOrchestrator:'#303f58', noteExecutor:'#304838', noteHuman:'#504727', noteEmpty:'#3b3d46', codeBg:'#22252c',
    accent:'#f4c844', onAccent:'#272218', selection:'#3153bb', onSelection:'#ffffff', onDanger:'#202124', focus:'#99b4ff', dot:'#33363e',
    codeKeyword:'#abc9ff', codeString:'#b6e6b8', codeNumber:'#d4bafa', codeComment:'#c7c9d0', codeAttr:'#ffe493', codeLiteral:'#abc2ff',
  },
};
export function applyTheme(mode: ThemeMode) {
  const resolved = mode === 'auto' ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : mode;
  document.documentElement.dataset.theme = resolved;
  for (const [name, value] of Object.entries(palettes[resolved])) document.documentElement.style.setProperty(`--${name}`, value);
  document.documentElement.style.colorScheme = resolved;
}
