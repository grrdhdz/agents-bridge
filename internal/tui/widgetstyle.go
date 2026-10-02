// widgetstyle.go configures bubbles' textarea (the composer) and textinput
// (the search field) from this Model's own theme, instead of leaving them
// on the library's built-in defaults.
//
// textarea.New() and textinput.New() both hard-code
// DefaultDarkStyles() internally (bubbles v2), regardless of which theme
// the rest of the TUI is using — a real report traced the composer's
// "franja de fondo oscuro" (a dark strip under the cursor line, visible
// even in the light theme) to exactly this: CursorLine's default
// background is ANSI color "0" (black) whenever textarea.New() is used
// as-is. SetStyles here replaces that with the theme's own Surface/Text
// colors, so a light theme's composer never shows a leftover dark-mode
// background.
package tui

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/agents-bridge/internal/tui/theme"
)

// textAreaStyles builds the composer's textarea.Styles from th: every
// surface (base, cursor line) painted with th.Surface/th.SurfaceRaised,
// every foreground chosen from th's own contrast-checked colors — never
// the library's raw ANSI-16 defaults, which are unaware of the actual
// theme in use. NO_COLOR skips every color, matching every other themed
// style in this package (§8: "degradar a atributos y símbolos").
func textAreaStyles(th theme.Theme) textarea.Styles {
	if th.NoColor {
		plain := lipgloss.NewStyle()
		state := textarea.StyleState{Base: plain, Text: plain, LineNumber: plain, CursorLineNumber: plain, CursorLine: plain, EndOfBuffer: plain, Placeholder: plain, Prompt: plain}
		return textarea.Styles{Focused: state, Blurred: state, Cursor: textarea.CursorStyle{Shape: tea.CursorBlock, Blink: true}}
	}
	base := lipgloss.NewStyle().Background(th.Surface)
	text := lipgloss.NewStyle().Foreground(th.Text).Background(th.Surface)
	muted := lipgloss.NewStyle().Foreground(th.Muted).Background(th.Surface)
	// The active line gets the "raised" surface (the same one the status
	// bar uses) — a subtle, theme-correct highlight instead of the
	// library's black-on-everything default.
	cursorLine := lipgloss.NewStyle().Background(th.SurfaceRaised)

	focused := textarea.StyleState{
		Base: base, Text: text, LineNumber: muted, CursorLineNumber: muted,
		CursorLine: cursorLine, EndOfBuffer: base, Placeholder: muted, Prompt: muted,
	}
	blurred := focused // same surfaces; the composer's own border (BorderStyle) already signals focus.

	return textarea.Styles{
		Focused: focused,
		Blurred: blurred,
		Cursor:  textarea.CursorStyle{Color: th.Text, Shape: tea.CursorBlock, Blink: true},
	}
}

// textInputStyles builds the search field's textinput.Styles from th. The
// field has no border/background box of its own (it sits inline in the
// footer, after the "Buscar: " label — see searchBar, view.go), so only
// foreground colors are themed; painting an isolated background rectangle
// there would look like a stray patch against the plain footer around it.
func textInputStyles(th theme.Theme) textinput.Styles {
	if th.NoColor {
		plain := lipgloss.NewStyle()
		state := textinput.StyleState{Text: plain, Placeholder: plain, Suggestion: plain, Prompt: plain}
		return textinput.Styles{Focused: state, Blurred: state, Cursor: textinput.CursorStyle{Shape: tea.CursorBlock, Blink: true}}
	}
	text := lipgloss.NewStyle().Foreground(th.Text)
	muted := lipgloss.NewStyle().Foreground(th.Muted)
	state := textinput.StyleState{Text: text, Placeholder: muted, Suggestion: muted, Prompt: muted}
	return textinput.Styles{
		Focused: state,
		Blurred: state,
		Cursor:  textinput.CursorStyle{Color: th.Text, Shape: tea.CursorBlock, Blink: true},
	}
}
