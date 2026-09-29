// overlay.go implements §6.6/§6.7's floating windows (command palette,
// confirmation dialog, help, toasts): each is a small bordered box
// composited *over* the normal screen — not a full-screen replacement —
// using lipgloss v2's own layer/canvas compositor
// (charm.land/lipgloss/v2: Layer, Compositor, Canvas), so the interface
// stays visible behind them exactly like a real floating window manager.
package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// floatingWindow is one composited box: its own rendered content (border
// included) plus the absolute screen position it is drawn at. Every
// click region a window registers is expressed in these same absolute
// coordinates (x+col, y+row), so hitregion.go never needs to know a
// region came from a floating window rather than the base screen.
type floatingWindow struct {
	content       string
	x, y          int
	width, height int
}

// clampInt keeps v within [lo, hi] (hi < lo is treated as "no room": v
// collapses to lo).
func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// centerWindow returns the top-left (x, y) that centers a windowWidth ×
// windowHeight box within a termWidth × termHeight screen, never
// negative (a window taller/wider than the terminal simply starts at 0).
func centerWindow(termWidth, termHeight, windowWidth, windowHeight int) (x, y int) {
	x = (termWidth - windowWidth) / 2
	if x < 0 {
		x = 0
	}
	y = (termHeight - windowHeight) / 2
	if y < 0 {
		y = 0
	}
	return x, y
}

// compositeWindows draws base (the full screen, already exactly
// termWidth × termHeight — see layout_test.go) and then each window on
// top, in order (later windows on top of earlier ones, toasts last so
// they float over everything else including an open modal). This is
// lipgloss v2's real layer compositor (Layer/Compositor/Canvas,
// charm.land/lipgloss/v2/{layer,canvas}.go) rather than hand-spliced
// strings: each window becomes its own Layer positioned at (x, y), and
// the Canvas rasterizes every layer's cells (correctly handling each
// line's ansi-visible width) into the final screen.
func compositeWindows(base string, termWidth, termHeight int, windows ...floatingWindow) string {
	if len(windows) == 0 {
		return base
	}
	layers := make([]*lipgloss.Layer, 0, len(windows)+1)
	layers = append(layers, lipgloss.NewLayer(base).X(0).Y(0).Z(0))
	for i, w := range windows {
		layers = append(layers, lipgloss.NewLayer(w.content).X(w.x).Y(w.y).Z(i+1))
	}
	compositor := lipgloss.NewCompositor(layers...)
	canvas := lipgloss.NewCanvas(termWidth, termHeight)
	canvas.Compose(compositor)
	return canvas.Render()
}

// windowBorderStyler is the one method windowBox needs from a theme —
// spelled out explicitly rather than importing theme.Theme, since that
// would be this file's only reason to depend on the theme package.
type windowBorderStyler interface {
	BorderStyle(active bool) lipgloss.Style
}

// windowBox borders and pads content (already word-wrapped to the
// caller's chosen inner width, one string per line — no line may exceed
// innerWidth) into a floating window's own rendered form, returning it
// alongside the outer width/height (border and padding included —
// lipgloss.Style.Width/Height set the box's *total* size, not the
// content's, subtracting border/padding internally) a caller needs to
// center the window and register regions against.
func windowBox(th windowBorderStyler, active bool, innerWidth int, lines []string) (rendered string, outerWidth, outerHeight int) {
	outerWidth = innerWidth + 2 /*padding*/ + 2 /*border*/
	outerHeight = len(lines) + 2                /*border*/
	style := th.BorderStyle(active).Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(outerWidth).MaxWidth(outerWidth)
	rendered = style.Render(strings.Join(lines, "\n"))
	return rendered, outerWidth, outerHeight
}
