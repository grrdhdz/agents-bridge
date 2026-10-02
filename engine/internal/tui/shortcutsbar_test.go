package tui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

// fgSequence is the truecolor SGR foreground a rendered span starts with.
func fgSequence(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

// lineContaining returns the rendered line holding text, or fails.
func lineContaining(t *testing.T, view, text string) string {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, text) {
			return line
		}
	}
	t.Fatalf("no line contains %q", text)
	return ""
}

// TestShortcutsBarsUseTheThemesMutedForeground is the regression for the
// shortcuts strips rendering in the terminal's own default foreground: on a
// dark terminal scheme that is light gray, nearly invisible on the light
// theme's bluish Surface. Both the bridge screen and the home screen must
// paint it with the theme's Muted color, which the WCAG tests cover.
func TestShortcutsBarsUseTheThemesMutedForeground(t *testing.T) {
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		muted := fgSequence(theme.New(mode, false, nil).Muted)
		bridge := lineContaining(t, goldenModel(t, 80, mode).View().Content, "ctrl+c salir")
		if !strings.Contains(bridge, muted) {
			t.Fatalf("%s bridge shortcuts bar lacks the Muted foreground %s: %q", mode, muted, bridge)
		}
		home := lineContaining(t, newHomeTest(t, &fakeSource{}, 80, 24, mode).View().Content, "? más")
		if !strings.Contains(home, muted) {
			t.Fatalf("%s home shortcuts bar lacks the Muted foreground %s: %q", mode, muted, home)
		}
	}
}
