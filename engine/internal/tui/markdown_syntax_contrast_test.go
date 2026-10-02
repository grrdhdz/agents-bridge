package tui

import (
	"image/color"
	"math"
	"reflect"
	"testing"

	"charm.land/glamour/v2/ansi"
	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

func wcagChannel(c uint32) float64 {
	v := float64(c>>8) / 255.0
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func wcagLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return 0.2126*wcagChannel(r) + 0.7152*wcagChannel(g) + 0.0722*wcagChannel(b)
}

func wcagRatio(a, b color.Color) float64 {
	la, lb := wcagLuminance(a), wcagLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestCodeBlockSyntaxColorsMeetWCAG recorre toda entrada con color de
// primer plano de la configuración chroma efectiva (la que glamour aplica a
// los bloques de código) y exige ≥4.5:1 contra el fondo contra el que se
// pinta: el fondo propio del token si lo tiene, o SurfaceRaised.
func TestCodeBlockSyntaxColorsMeetWCAG(t *testing.T) {
	const min = 4.5
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		th := theme.New(mode, false, nil)
		cfg := glamourStyle(th, protocol.RoleOrchestrator)
		if cfg.CodeBlock.Chroma == nil {
			t.Fatalf("%s: sin configuración chroma", mode)
		}
		v := reflect.ValueOf(*cfg.CodeBlock.Chroma)
		for i := 0; i < v.NumField(); i++ {
			name := v.Type().Field(i).Name
			prim, ok := v.Field(i).Interface().(ansi.StylePrimitive)
			if !ok || prim.Color == nil || name == "Background" {
				continue
			}
			var bg color.Color = th.SurfaceRaised
			if prim.BackgroundColor != nil {
				bg = lipgloss.Color(*prim.BackgroundColor)
			}
			got := wcagRatio(lipgloss.Color(*prim.Color), bg)
			t.Logf("%s %-20s %s %.2f:1", mode, name, *prim.Color, got)
			if got < min {
				t.Errorf("%s: %s (%s) contrasta %.2f:1, mínimo %.1f:1", mode, name, *prim.Color, got, min)
			}
		}
	}
}
