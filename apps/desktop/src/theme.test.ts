import { expect, test } from 'vitest';
import { palettes } from './theme';
function luminance(hex: string) { const c = hex.slice(1).match(/../g)!.map(v => parseInt(v,16)/255).map(v => v <= .04045 ? v/12.92 : ((v+.055)/1.055)**2.4); return c[0]*.2126+c[1]*.7152+c[2]*.0722; }
function ratio(a: string, b: string) { const x=luminance(a),y=luminance(b); return (Math.max(x,y)+.05)/(Math.min(x,y)+.05); }
test('all theme text and role tokens meet 4.5:1 and borders meet 3:1', () => {
  for (const p of Object.values(palettes)) {
    for (const bg of [p.surface,p.panel,p.card]) {
      for (const color of [p.text,p.muted,p.orchestrator,p.executor,p.human,p.info,p.warning,p.danger,p.notice,p.codeKeyword,p.codeString,p.codeNumber,p.codeComment,p.codeAttr,p.codeLiteral]) expect(ratio(color,bg)).toBeGreaterThanOrEqual(4.5);
      expect(ratio(p.border,bg)).toBeGreaterThanOrEqual(3);
      expect(ratio(p.focus,bg)).toBeGreaterThanOrEqual(3);
    }
    expect(ratio(p.onSelection,p.selection)).toBeGreaterThanOrEqual(4.5);
    expect(ratio(p.onDanger,p.danger)).toBeGreaterThanOrEqual(4.5);
    expect(ratio(p.onAccent,p.accent)).toBeGreaterThanOrEqual(4.5);
  }
});

test('canvas notes, code and primary actions keep accessible contrast in both themes', () => {
  for (const palette of Object.values(palettes)) {
    for (const fill of [palette.noteOrchestrator,palette.noteExecutor,palette.noteHuman,palette.noteEmpty,palette.codeBg]) {
      for (const color of [palette.text,palette.muted,palette.orchestrator,palette.executor,palette.human,palette.info,palette.warning,palette.danger,palette.notice,palette.codeKeyword,palette.codeString,palette.codeNumber,palette.codeComment,palette.codeAttr,palette.codeLiteral]) expect(ratio(color,fill)).toBeGreaterThanOrEqual(4.5);
      expect(ratio(palette.border,fill)).toBeGreaterThanOrEqual(3);
      expect(ratio(palette.focus,fill)).toBeGreaterThanOrEqual(3);
    }
    expect(ratio(palette.onAccent,palette.accent)).toBeGreaterThanOrEqual(4.5);
    expect(ratio(palette.onSelection,palette.selection)).toBeGreaterThanOrEqual(4.5);
    expect(ratio(palette.onDanger,palette.danger)).toBeGreaterThanOrEqual(4.5);
  }
});
