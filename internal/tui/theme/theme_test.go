package theme

import (
	"image/color"
	"math"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

func TestResolveModeFlagWinsOverEnv(t *testing.T) {
	mode, err := ResolveMode("light", "dark")
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeLight {
		t.Fatalf("flag should win over env, got %q", mode)
	}
}

func TestResolveModeFallsBackToEnvThenAuto(t *testing.T) {
	mode, err := ResolveMode("", "dark")
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeDark {
		t.Fatalf("expected env value when flag is empty, got %q", mode)
	}

	mode, err = ResolveMode("", "")
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeAuto {
		t.Fatalf("expected auto as the default, got %q", mode)
	}
}

func TestResolveModeRejectsUnknownValues(t *testing.T) {
	if _, err := ResolveMode("neon", ""); err == nil {
		t.Fatal("expected an error for an unknown theme value")
	}
}

func TestNewAutoUsesDetectDark(t *testing.T) {
	dark := New(ModeAuto, false, func() bool { return true })
	if dark.Mode != ModeDark {
		t.Fatalf("detectDark=true should resolve to dark, got %q", dark.Mode)
	}
	light := New(ModeAuto, false, func() bool { return false })
	if light.Mode != ModeLight {
		t.Fatalf("detectDark=false should resolve to light, got %q", light.Mode)
	}
}

func TestNoColorDropsForegroundButKeepsBold(t *testing.T) {
	plain := New(ModeDark, true, nil)
	if !plain.NoColor {
		t.Fatal("expected NoColor to be true")
	}
	style := plain.RoleStyle(protocol.RoleOrchestrator)
	if _, isNoColor := style.GetForeground().(lipgloss.NoColor); !isNoColor {
		t.Fatalf("NO_COLOR theme must not set a foreground color, got %#v", style.GetForeground())
	}
	if !style.GetBold() {
		t.Fatal("NO_COLOR theme should still emphasize with bold")
	}
}

func TestColorThemeSetsDistinctRoleForegrounds(t *testing.T) {
	dark := New(ModeDark, false, nil)
	orch := dark.RoleStyle(protocol.RoleOrchestrator).GetForeground()
	exec := dark.RoleStyle(protocol.RoleExecutor).GetForeground()
	if orch == nil || exec == nil {
		t.Fatal("colored theme should set a foreground for both roles")
	}
	if orch == exec {
		t.Fatal("orchestrator and executor should use distinct colors")
	}
}

func TestStatusSymbolsAreStableAcrossThemes(t *testing.T) {
	cases := map[string]string{
		"queued-ram": "·",
		"accepted":   "✓",
		"delivered":  "✓✓",
		"rejected":   "✗",
		"received":   "✓",
	}
	for _, mode := range []Mode{ModeDark, ModeLight} {
		th := New(mode, false, nil)
		for status, want := range cases {
			if got := th.StatusSymbol(status); got != want {
				t.Fatalf("mode=%s status=%q: got %q want %q", mode, status, got, want)
			}
		}
	}
}

func TestConnIndicatorSymbols(t *testing.T) {
	th := New(ModeDark, false, nil)
	if th.ConnIndicator("connected") != "●" {
		t.Fatal("connected should render a filled dot")
	}
	if th.ConnIndicator("reconnecting") != "◌" {
		t.Fatal("reconnecting should render a dotted circle")
	}
	if th.ConnIndicator("disconnected") != "○" {
		t.Fatal("disconnected should render a hollow dot")
	}
}

// TestBorderStyleColorsTheBorderGlyphsThemselves is a regression test for a
// real bug found while implementing round 5's bordered message cards:
// lipgloss.Style.Foreground only colors *content* rendered inside a
// border, never the border glyphs themselves (charm.land/lipgloss/v2's
// applyBorder reads borderTopForegroundKey/etc., which only
// Style.BorderForeground sets) — so every bordered box in this package
// (conversation/composer/sidebar/overlay) has been rendering its border
// perfectly colorless this whole time, regardless of focus state. This
// matters even more now that round 5 also gives each message card a
// role-colored border as its *only* visual distinction (no more tinted
// fill) — a colorless border would make that distinction invisible.
func TestBorderStyleColorsTheBorderGlyphsThemselves(t *testing.T) {
	th := New(ModeDark, false, nil)
	rendered := th.BorderStyle(true).Border(lipgloss.RoundedBorder()).Render("x")
	top := strings.Split(rendered, "\n")[0]
	if !strings.Contains(top, "38;2;") {
		t.Fatalf("the top border line should carry its own explicit foreground color (via BorderForeground, not just Foreground), got %q", top)
	}
}

func TestLightAndDarkThemesDiffer(t *testing.T) {
	dark := New(ModeDark, false, nil)
	light := New(ModeLight, false, nil)
	if dark.Text == light.Text {
		t.Fatal("dark and light themes should use different text colors")
	}
}

// --- WCAG 2 contrast ---------------------------------------------------
//
// relativeLuminance and contrastRatio implement the standard WCAG 2
// formulas (https://www.w3.org/TR/WCAG21/#dfn-relative-luminance and
// #dfn-contrast-ratio) directly against sRGB channel values, independent
// of lipgloss — a small, deliberately dependency-free reimplementation so
// this test can't accidentally pass by sharing a bug with the production
// color type.

func srgbChannel(c uint32) float64 {
	// color.Color.RGBA() returns 16-bit-per-channel premultiplied values;
	// our theme colors are always fully opaque, so dividing by 257
	// recovers the original 8-bit sRGB channel (0..255) exactly.
	v := float64(c/257) / 255.0
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func relativeLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	rl, gl, bl := srgbChannel(r), srgbChannel(g), srgbChannel(b)
	return 0.2126*rl + 0.7152*gl + 0.0722*bl
}

func contrastRatio(a, b color.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestPaletteContrastMeetsWCAGMinimums covers spec item 1's measurable
// criteria, in *both* themes (the report was about light, but the same
// floor must hold in dark): primary text ≥7:1, secondary/muted text and
// every semantic role/label/status color ≥4.5:1, active borders ≥3:1,
// inactive borders ≥2:1 — each checked against the surface it is actually
// painted on (Surface for cards/panel/composer, SurfaceRaised for the
// status bar), not a generic #ffffff/#000000 assumption.
func TestPaletteContrastMeetsWCAGMinimums(t *testing.T) {
	const (
		minPrimary   = 7.0
		minSecondary = 4.5
		minBorderAct = 3.0
		minBorderIn  = 2.0
	)
	for _, mode := range []Mode{ModeDark, ModeLight} {
		th := New(mode, false, nil)
		type pair struct {
			name string
			fg   color.Color
			bg   color.Color
			min  float64
		}
		pairs := []pair{
			{"Text on Surface (primary)", th.Text, th.Surface, minPrimary},
			{"Text on SurfaceRaised (status bar/code)", th.Text, th.SurfaceRaised, minPrimary},
			{"Muted on Surface (secondary)", th.Muted, th.Surface, minSecondary},
			{"Muted on SurfaceRaised (code comments)", th.Muted, th.SurfaceRaised, minSecondary},
			{"Orchestrator on Surface", th.Orchestrator, th.Surface, minSecondary},
			{"Executor on Surface", th.Executor, th.Surface, minSecondary},
			{"Human on Surface", th.Human, th.Surface, minSecondary},
			{"Success on Surface", th.Success, th.Surface, minSecondary},
			{"Info on Surface", th.Info, th.Surface, minSecondary},
			{"Warning on Surface", th.Warning, th.Surface, minSecondary},
			{"Danger on Surface", th.Danger, th.Surface, minSecondary},
			{"Notice on Surface", th.Notice, th.Surface, minSecondary},
			{"BorderActive on Surface", th.BorderActive, th.Surface, minBorderAct},
			{"BorderInactive on Surface", th.BorderInactive, th.Surface, minBorderIn},
			// Round 5: message cards are no longer tinted (bubble fill
			// removed — "revierte el relleno de color"); each is instead
			// distinguished by a role-colored (or Human-colored, for a
			// human-authored message) line border, and the selected card by
			// an Info-colored one. A border only needs to be *visible*
			// against the surface it sits on (WCAG's own "graphical object"
			// floor), not full text contrast.
			{"Orchestrator card border on Surface", th.Orchestrator, th.Surface, minBorderAct},
			{"Executor card border on Surface", th.Executor, th.Surface, minBorderAct},
			{"Human card border on Surface", th.Human, th.Surface, minBorderAct},
			{"Info card border (selected) on Surface", th.Info, th.Surface, minBorderAct},
		}
		for _, p := range pairs {
			t.Run(string(mode)+"/"+p.name, func(t *testing.T) {
				got := contrastRatio(p.fg, p.bg)
				if got < p.min {
					t.Fatalf("%s: contrast %.2f:1 is below the required %.1f:1", p.name, got, p.min)
				}
			})
		}
	}
}

// TestLightSurfaceIsBluishNotWhite pins the light theme's surfaces to the
// bluish tone the command palette introduced: the whole light TUI, not just
// its overlays, should read as that tone ("todo del tono azulado de la
// paleta de comandos, no blanco"). It replaces the earlier off-white
// (#F3F4F6) request. Both surfaces stay light — never pure #FFFFFF glare,
// never dark enough to stop being a light theme — and the raised one (status
// bar, overlays, code blocks) a touch more marked than the base.
func TestLightSurfaceIsBluishNotWhite(t *testing.T) {
	light := New(ModeLight, false, nil)
	white := color.White
	for _, c := range []struct {
		name string
		col  color.Color
	}{{"Surface", light.Surface}, {"SurfaceRaised", light.SurfaceRaised}} {
		ratio := contrastRatio(c.col, white)
		if ratio <= 1.0 {
			t.Fatalf("%s should differ from pure white, got contrast ratio %.3f", c.name, ratio)
		}
		if ratio > 1.5 {
			t.Fatalf("%s should stay a light surface, got contrast ratio %.3f against white", c.name, ratio)
		}
		r, _, b, _ := c.col.RGBA()
		if b <= r {
			t.Fatalf("%s should be bluish (blue channel above red), got %v", c.name, c.col)
		}
	}
	if contrastRatio(light.SurfaceRaised, white) <= contrastRatio(light.Surface, white) {
		t.Fatal("SurfaceRaised (status bar) should be at least as marked as Surface, not lighter")
	}
}

func sameColor(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// TestCoordinationLabelStyles covers v0.4.0's badges: URGENTE takes the
// danger color, PROGRESO the muted one (both already contrast-checked in
// TestPaletteContrastMeetsWCAGMinimums), and under NO_COLOR they keep an
// attribute-only difference (reverse for URGENTE, faint for PROGRESO).
func TestCoordinationLabelStyles(t *testing.T) {
	for _, mode := range []Mode{ModeDark, ModeLight} {
		th := New(mode, false, nil)
		if got := th.LabelStyle("URGENTE").GetForeground(); !sameColor(got, th.Danger) {
			t.Fatalf("%s: URGENTE should use Danger, got %v", mode, got)
		}
		if got := th.LabelStyle("PROGRESO").GetForeground(); !sameColor(got, th.Muted) {
			t.Fatalf("%s: PROGRESO should use Muted, got %v", mode, got)
		}
		plain := New(mode, true, nil)
		if !plain.LabelStyle("URGENTE").GetReverse() {
			t.Fatalf("%s: NO_COLOR URGENTE should be reverse video", mode)
		}
		if !plain.LabelStyle("PROGRESO").GetFaint() {
			t.Fatalf("%s: NO_COLOR PROGRESO should be faint", mode)
		}
	}
}

// TestEveryLabelColorMeetsSecondaryContrast checks each badge's foreground
// against the surface it sits on, so a future label cannot slip in below the
// 4.5:1 floor.
func TestEveryLabelColorMeetsSecondaryContrast(t *testing.T) {
	for _, mode := range []Mode{ModeDark, ModeLight} {
		th := New(mode, false, nil)
		for _, label := range []string{"TAREA", "PREGUNTA", "RESPUESTA", "RESULTADO", "FIN", "URGENTE", "PROGRESO"} {
			fg := th.LabelStyle(label).GetForeground()
			if got := contrastRatio(fg, th.Surface); got < 4.5 {
				t.Errorf("%s/%s badge contrast %.2f:1 is below 4.5:1", mode, label, got)
			}
		}
	}
}

// TestAutoWithoutAnswerIsDarkAndUndecidedAndNeverQueries: New must not touch
// the terminal at all (no stdin reader); an unanswered auto is dark with
// Auto set, and any explicit choice or injected answer settles it.
func TestAutoWithoutAnswerIsDarkAndUndecidedAndNeverQueries(t *testing.T) {
	th := New(ModeAuto, false, nil)
	if th.Mode != ModeDark || !th.Auto {
		t.Fatalf("auto/nil = %q auto=%v", th.Mode, th.Auto)
	}
	if got := New(ModeAuto, false, func() bool { return false }); got.Mode != ModeLight || got.Auto {
		t.Fatalf("auto with an answer = %q auto=%v", got.Mode, got.Auto)
	}
	for _, m := range []Mode{ModeDark, ModeLight} {
		if New(m, false, nil).Auto {
			t.Fatalf("%s is explicit, not auto", m)
		}
	}
}
