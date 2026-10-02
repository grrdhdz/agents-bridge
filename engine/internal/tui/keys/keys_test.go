package keys

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func TestNoCollisionsWithinAnyContext(t *testing.T) {
	m := New()
	for _, group := range m.Groups() {
		seen := map[string]string{}
		for _, binding := range group.Bindings {
			for _, k := range binding.Keys() {
				if owner, exists := seen[k]; exists {
					t.Fatalf("context %q: key %q bound to both %q and %q", group.Name, k, owner, binding.Help().Desc)
				}
				seen[k] = binding.Help().Desc
			}
		}
	}
}

func TestHelpListsEveryBindingGroupedByContext(t *testing.T) {
	m := New()
	help := m.Help()
	for _, group := range m.Groups() {
		if !strings.Contains(help, group.Name) {
			t.Fatalf("help text should mention context %q:\n%s", group.Name, help)
		}
		for _, binding := range group.Bindings {
			if !strings.Contains(help, binding.Help().Desc) {
				t.Fatalf("help text should mention binding %q (context %q):\n%s", binding.Help().Desc, group.Name, help)
			}
		}
	}
}

func TestSendMatchesCtrlSAndCtrlEnter(t *testing.T) {
	m := New()
	if !key.Matches(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}, m.Composer.Send) {
		t.Fatal("ctrl+s should match Send")
	}
	if !key.Matches(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}, m.Composer.Send) {
		t.Fatal("ctrl+enter should match Send")
	}
}

func TestFocusToggleIsTab(t *testing.T) {
	m := New()
	if !key.Matches(tea.KeyPressMsg{Code: tea.KeyTab}, m.Global.FocusToggle) {
		t.Fatal("tab should toggle focus")
	}
}

// TestBarIsAlwaysOneLineAndEndsInMore covers spec §3 (amended): the
// shortcuts bar is a single line at any width, ends in "? más", and never
// exceeds the given width even at the narrowest supported terminal.
func TestBarIsAlwaysOneLineAndEndsInMore(t *testing.T) {
	m := New()
	for _, width := range []int{60, 80, 120} {
		bar := m.Bar(width, nil, "Global", "Composer")
		if strings.Contains(bar, "\n") {
			t.Fatalf("width %d: bar should be a single line, got %q", width, bar)
		}
		if !strings.HasSuffix(bar, "? más") {
			t.Fatalf("width %d: bar should end in \"? más\", got %q", width, bar)
		}
		if len([]rune(bar)) > width {
			t.Fatalf("width %d: bar is wider than the terminal: %q", width, bar)
		}
	}
}

// TestBarShowsMoreEntriesAsWidthGrows covers the "por prioridad según
// ancho" part: a wider bar can fit strictly more of the keymap's entries
// than a narrower one (until everything fits).
func TestBarShowsMoreEntriesAsWidthGrows(t *testing.T) {
	m := New()
	narrow := m.Bar(60, nil, "Global", "Composer")
	wide := m.Bar(120, nil, "Global", "Composer")
	if len(wide) <= len(narrow) {
		t.Fatalf("a wider bar should fit at least as much content: narrow=%q wide=%q", narrow, wide)
	}
}

// TestBarKeepsGlobalBindingsAtHighestPriority ensures the first (highest
// priority) group's bindings survive even a narrow bar, since they apply
// everywhere.
func TestBarKeepsGlobalBindingsAtHighestPriority(t *testing.T) {
	m := New()
	bar := m.Bar(60, nil, "Global", "Composer")
	firstGlobal := m.Global.FocusToggle.Help()
	if !strings.Contains(bar, firstGlobal.Key) {
		t.Fatalf("the first global binding should always fit first, got %q", bar)
	}
}

func TestHelpKeyOnlyBoundInConversationContext(t *testing.T) {
	m := New()
	for _, group := range m.Groups() {
		if group.Name == conversationContext || group.Name == homeContext {
			continue // the home screen has no text input competing for "?"
		}
		for _, binding := range group.Bindings {
			for _, k := range binding.Keys() {
				if k == "?" {
					t.Fatalf("context %q should not bind ? itself (reserved for conversation/home help)", group.Name)
				}
			}
		}
	}
}

// TestHelpForRestrictsToTheRequestedContexts covers phase 3: each screen's
// help shows only its own contexts (the bridge view never lists the home
// screen's keys, and vice versa).
func TestHelpForRestrictsToTheRequestedContexts(t *testing.T) {
	m := New()
	bridgeHelp := m.HelpFor(globalContext, conversationContext, composerContext)
	if strings.Contains(bridgeHelp, homeContext) || strings.Contains(bridgeHelp, "cerrar puente") {
		t.Fatalf("bridge help must not list the home context:\n%s", bridgeHelp)
	}
	homeHelp := m.HelpFor(homeContext)
	for _, want := range []string{"entrar", "cerrar puente", "crear puente", "refrescar", "filtrar", "salir"} {
		if !strings.Contains(homeHelp, want) {
			t.Fatalf("home help should list %q:\n%s", want, homeHelp)
		}
	}
	if strings.Contains(homeHelp, "plegar") {
		t.Fatalf("home help must not list conversation keys:\n%s", homeHelp)
	}
}

func TestDisabledBindingsAreOmittedFromHelpAndBar(t *testing.T) {
	m := New()
	m.Global.Home.SetEnabled(false)
	if strings.Contains(m.HelpFor(globalContext), "volver a inicio") {
		t.Fatal("a disabled binding must not appear in help")
	}
	if strings.Contains(m.Bar(200, nil, globalContext), "volver a inicio") {
		t.Fatal("a disabled binding must not appear in the bar")
	}
}
