package main

import (
	"testing"

	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

// TestResolveThemeAutoIsDeferredToTheProgram: no mode reads stdin to learn
// the background; an interactive "auto" starts dark and undecided so the TUI
// can ask through Bubble Tea, and explicit choices win outright.
func TestResolveThemeAutoIsDeferredToTheProgram(t *testing.T) {
	t.Setenv("AGENTS_BRIDGE_THEME", "")
	t.Setenv("NO_COLOR", "")
	th, err := resolveTheme("", true)
	if err != nil || th.Mode != theme.ModeDark || !th.Auto {
		t.Fatalf("interactive auto = %q auto=%v err=%v", th.Mode, th.Auto, err)
	}
	th, _ = resolveTheme("", false)
	if th.Mode != theme.ModeDark || th.Auto {
		t.Fatalf("non-interactive auto must be settled dark, got %q auto=%v", th.Mode, th.Auto)
	}
	th, _ = resolveTheme("light", true)
	if th.Mode != theme.ModeLight || th.Auto {
		t.Fatalf("--theme light = %q auto=%v", th.Mode, th.Auto)
	}
	t.Setenv("AGENTS_BRIDGE_THEME", "light")
	th, _ = resolveTheme("", true)
	if th.Mode != theme.ModeLight || th.Auto {
		t.Fatalf("env light = %q auto=%v", th.Mode, th.Auto)
	}
	th, _ = resolveTheme("dark", true)
	if th.Mode != theme.ModeDark || th.Auto {
		t.Fatalf("flag must beat env: %q auto=%v", th.Mode, th.Auto)
	}
}
