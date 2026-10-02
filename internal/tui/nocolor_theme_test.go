package tui

import (
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/internal/tui/theme"
)

// TestToggleThemeUnderNoColorExplainsInsteadOfDoingNothing: with NO_COLOR no
// theme can paint anything, so "Cambiar tema" used to change nothing visible
// and say nothing — it looked like a broken light theme. It must now leave
// the theme alone and show a notice saying why.
func TestToggleThemeUnderNoColorExplainsInsteadOfDoingNothing(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, Theme: theme.New(theme.ModeDark, true, nil)})
	model.toggleTheme()
	if model.th.Mode != theme.ModeDark || !model.th.NoColor {
		t.Fatalf("theme should be unchanged under NO_COLOR, got mode %s NoColor %v", model.th.Mode, model.th.NoColor)
	}
	if len(model.toasts) != 1 || model.toasts[0].text != noColorThemeNotice {
		t.Fatalf("expected the NO_COLOR notice, got %+v", model.toasts)
	}
}

func TestHomeToggleThemeUnderNoColorExplains(t *testing.T) {
	h := NewHome(HomeOptions{Source: &fakeSource{}, Theme: theme.New(theme.ModeDark, true, nil), Now: func() time.Time { return homeClock }})
	h.runHomePaletteItem("theme")
	if h.th.Mode != theme.ModeDark {
		t.Fatalf("home theme should be unchanged under NO_COLOR, got %s", h.th.Mode)
	}
	if len(h.toasts) != 1 || h.toasts[0].text != noColorThemeNotice {
		t.Fatalf("expected the NO_COLOR notice on the home screen, got %+v", h.toasts)
	}
}

// TestToggleThemeWithColorStillSwitches guards the normal path.
func TestToggleThemeWithColorStillSwitches(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, Theme: theme.New(theme.ModeDark, false, nil)})
	model.toggleTheme()
	if model.th.Mode != theme.ModeLight || len(model.toasts) != 0 {
		t.Fatalf("with colors the theme should switch silently, got mode %s toasts %+v", model.th.Mode, model.toasts)
	}
}
