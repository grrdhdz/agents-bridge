package tui

import (
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

// TestComposerNeverUsesTextareasBuiltinDarkCursorLine is a regression test
// for a real report: the light theme's composer showed a leftover dark
// strip ("franja de fondo oscuro") under the cursor line. The cause was
// textarea.New() hard-coding DefaultDarkStyles() internally regardless of
// theme (its CursorLine background is ANSI "0", i.e. black, always) —
// widgetstyle.go's textAreaStyles must override that with the theme's own
// SurfaceRaised, in both themes.
func TestComposerNeverUsesTextareasBuiltinDarkCursorLine(t *testing.T) {
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		th := theme.New(mode, false, nil)
		model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, Theme: th})
		got := model.input.Styles().Focused.CursorLine.GetBackground()
		want := th.SurfaceRaised
		if got != want {
			t.Fatalf("%s: composer's CursorLine background is %#v, want the theme's own SurfaceRaised %#v", mode, got, want)
		}
		// The library's own default is ANSI-16 color "0" (black) or "255"
		// (white) — neither of which is one of our hex Surface colors, so
		// this also catches "SetStyles was never called" directly.
		if _, isNoColor := got.(lipgloss.NoColor); isNoColor {
			t.Fatalf("%s: composer's CursorLine has no background set at all", mode)
		}
	}
}

// TestSearchFieldUsesThemeColorsNotLibraryDefaults covers the same
// principle for the search field: its Placeholder/Text colors must come
// from the theme, not bubbles' raw ANSI-256 "240"/"7" defaults (which are
// exactly the "gris muy pálido" class of color the report complained
// about on a light background).
func TestSearchFieldUsesThemeColorsNotLibraryDefaults(t *testing.T) {
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		th := theme.New(mode, false, nil)
		model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, Theme: th})
		got := model.searchInput.Styles().Blurred.Placeholder.GetForeground()
		if got != th.Muted {
			t.Fatalf("%s: search field placeholder color is %#v, want the theme's own Muted %#v", mode, got, th.Muted)
		}
	}
}

// TestToggleThemeRestylesComposerAndSearch covers the palette's "Cambiar
// tema" action: switching themes at runtime must re-apply the new theme's
// widget styles too, not just the conversation's own rendering.
func TestToggleThemeRestylesComposerAndSearch(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, Theme: theme.New(theme.ModeDark, false, nil)})
	before := model.input.Styles().Focused.CursorLine.GetBackground()
	model.toggleTheme()
	after := model.input.Styles().Focused.CursorLine.GetBackground()
	if before == after {
		t.Fatal("toggling the theme should restyle the composer's cursor line too")
	}
	if after != model.th.SurfaceRaised {
		t.Fatalf("after toggling, composer CursorLine should match the new theme's SurfaceRaised, got %#v want %#v", after, model.th.SurfaceRaised)
	}
}
