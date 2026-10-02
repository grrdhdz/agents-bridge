// backgroundpaint.go implements round 5's fix for a real report: a person
// testing --theme dark *inside the app's own terminal panel* (an
// xterm.js-based embedded terminal, white background by default) saw the
// screen still all white with dark "plastas" (round 4's tinted bubbles) and
// white gaps between header spans — because that terminal does not honor
// tea.View.BackgroundColor (or the OSC 11 sequence bubbletea's renderer
// uses to set it) at all. View.BackgroundColor stays set (view.go) as a
// first line of defense for terminals that *do* support it, but it cannot
// be the only mechanism.
//
// paintFrameBackground is the fallback that works everywhere: a single,
// centralized pass over the fully composited frame (backdrop plus any
// floating windows) that explicitly paints every visible cell's
// background — Surface by default, SurfaceRaised for the status bar row
// and each floating window's own rectangle (spec: "Surface por defecto;
// los overlays y la barra de estado usan su propia superficie") — filling
// in every gap a span's own ANSI reset would otherwise leave at the real
// terminal's own (unpredictable) default. It never touches a span that
// already carries its own explicit background (a code block, painted
// SurfaceRaised by markdown.go) — it only fills gaps, never overrides a
// deliberate, different surface.
package tui

import (
	"image/color"
	"regexp"
	"strconv"
	"strings"
)

// ansiSGRRe matches one SGR ("Select Graphic Rendition") escape sequence —
// \x1b[...m, any content — so normalizeLineBackground can walk a rendered
// line as an alternating sequence of codes and plain text without a full
// ANSI parser.
var ansiSGRRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// bgZone is one rectangle of the frame, in absolute screen coordinates,
// that defaults to its own surface instead of the frame-wide default: the
// status bar row and each floating window's own bounds.
type bgZone struct {
	x, y, w, h int
	bg         color.Color
}

// bgAt resolves the default background at (x, y): the last zone (in
// iteration order) that covers it, or defaultBG when none does. None of
// this package's own zones actually overlap in practice, so iteration
// order never matters in the current callers.
func bgAt(x, y int, zones []bgZone, defaultBG color.Color) color.Color {
	for i := len(zones) - 1; i >= 0; i-- {
		z := zones[i]
		if x >= z.x && x < z.x+z.w && y >= z.y && y < z.y+z.h {
			return z.bg
		}
	}
	return defaultBG
}

// colorRGB renders c as the "r;g;b" parameter lipgloss itself emits after
// "48;2;" for a 24-bit background, so a run this file injects is
// byte-identical in form to one lipgloss rendered directly.
func colorRGB(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return strconv.Itoa(int(r>>8)) + ";" + strconv.Itoa(int(g>>8)) + ";" + strconv.Itoa(int(b>>8))
}

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == b
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// paintFrameBackground is the single centralized pass every frame goes
// through before it is handed to tea.NewView (View, view.go) — never
// called under NO_COLOR (spec §8: NO_COLOR carries no color at all, not
// even this). No other file in this package injects a background outside
// a genuinely distinct surface (a code block); this is the only place that
// paints the default.
func paintFrameBackground(frame string, width, height int, defaultBG color.Color, zones []bgZone) string {
	lines := strings.Split(frame, "\n")
	for i, line := range lines {
		if i >= height {
			break
		}
		lines[i] = normalizeLineBackground(line, i, width, defaultBG, zones)
	}
	return strings.Join(lines, "\n")
}

// skipColorParams reports how many *additional* SGR parameters (beyond the
// "38"/"48" token itself) a truecolor ("2;r;g;b", 4 tokens) or 256-color
// ("5;index", 2 tokens) foreground/background selector consumes, so the
// caller's scan can jump straight past a color tuple's own r/g/b numbers
// without ever inspecting them as if they were independent SGR codes —
// critical, since a channel value can itself equal 48, 49 or 0 (e.g. a
// color component of exactly 48) and must never be mistaken for a
// background-set/reset code in its own right.
func skipColorParams(params []string, idx int) int {
	if idx >= len(params) {
		return 0
	}
	switch params[idx] {
	case "2":
		return 4 // mode + r + g + b
	case "5":
		return 2 // mode + index
	default:
		return 0
	}
}

// normalizeLineBackground rewrites one screen row so every visible column
// from 0 to width-1 carries an explicit 24-bit background: it replays the
// row's own SGR codes to track whether a background is currently active,
// injecting this position's own default (bgAt) into every text run and
// every reset that leaves it unset, then pads out to width with more of
// the same. A span with its own explicit background (bgActive becomes
// true) is left completely untouched.
func normalizeLineBackground(line string, y, width int, defaultBG color.Color, zones []bgZone) string {
	var out strings.Builder
	bgActive := false
	var lastInjected color.Color
	col := 0

	inject := func() {
		want := bgAt(col, y, zones, defaultBG)
		if !sameColor(lastInjected, want) {
			out.WriteString("\x1b[48;2;" + colorRGB(want) + "m")
			lastInjected = want
		}
	}
	writeText := func(text string) {
		for _, r := range text {
			if !bgActive {
				inject()
			}
			out.WriteRune(r)
			col++
		}
	}

	pos := 0
	for _, m := range ansiSGRRe.FindAllStringIndex(line, -1) {
		start, end := m[0], m[1]
		if start > pos {
			writeText(line[pos:start])
		}
		code := line[start:end]
		params := strings.Split(code[2:len(code)-1], ";")
		i := 0
		for i < len(params) {
			switch params[i] {
			case "", "0", "49":
				// A reset (full "0"/"" or background-only "49") always
				// wipes out whatever background is currently active —
				// including one this very function injected into an
				// earlier gap on this line: lipgloss's own spans are
				// often individually self-resetting (down to single
				// characters, e.g. a border glyph), so a previously
				// injected background does not actually survive past
				// this point even though bgActive never went through an
				// intervening real "48" — invalidate lastInjected too so
				// the next gap re-emits instead of (wrongly) assuming
				// the terminal still has it.
				bgActive = false
				lastInjected = nil
				i++
			case "38":
				i += 1 + skipColorParams(params, i+1)
			case "48":
				// A real background just became active: forget whatever
				// we last injected so a later gap on this same line
				// re-injects fresh rather than assuming it's still
				// current.
				bgActive = true
				lastInjected = nil
				i += 1 + skipColorParams(params, i+1)
			default:
				i++
			}
		}
		out.WriteString(code)
		pos = end
	}
	if pos < len(line) {
		writeText(line[pos:])
	}

	for col < width {
		if !bgActive {
			inject()
		}
		out.WriteByte(' ')
		col++
	}
	out.WriteString("\x1b[0m")
	return out.String()
}
