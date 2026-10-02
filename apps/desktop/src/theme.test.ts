import { expect, test } from 'vitest';
import { palettes } from './theme';
function luminance(hex: string) { const c = hex.slice(1).match(/../g)!.map(v => parseInt(v,16)/255).map(v => v <= .04045 ? v/12.92 : ((v+.055)/1.055)**2.4); return c[0]*.2126+c[1]*.7152+c[2]*.0722; }
function ratio(a: string, b: string) { const x=luminance(a),y=luminance(b); return (Math.max(x,y)+.05)/(Math.min(x,y)+.05); }
test('all theme text and role tokens meet 4.5:1 and borders meet 3:1', () => {
  for (const p of Object.values(palettes)) {
    for (const bg of [p.surface,p.panel,p.card]) {
      for (const color of [p.text,p.muted,p.orchestrator,p.executor,p.human,p.info,p.warning,p.danger,p.notice]) expect(ratio(color,bg)).toBeGreaterThanOrEqual(4.5);
      expect(ratio(p.border,bg)).toBeGreaterThanOrEqual(3);
    }
    expect(ratio(p.onAccent,p.orchestrator)).toBeGreaterThanOrEqual(4.5);
    expect(ratio(p.onAccent,p.danger)).toBeGreaterThanOrEqual(4.5);
  }
});
