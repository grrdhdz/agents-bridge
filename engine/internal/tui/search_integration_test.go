package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

// TestSearchFieldOpensEmptyAcceptsBackspaceAndTyping is an integration test
// for a real report: pressing ctrl+f showed "Buscar: 🔎b" with a "b" that
// could not be deleted. It drives real tea.KeyPressMsg values through
// Model.Update exactly like a terminal would: ctrl+f, backspace, type
// "tail", enter.
func TestSearchFieldOpensEmptyAcceptsBackspaceAndTyping(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	send := func(msg tea.Msg) {
		updated, _ := model.Update(msg)
		model = *updated.(*Model)
	}
	send(tea.WindowSizeMsg{Width: 100, Height: 30})

	send(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !model.searchActive {
		t.Fatal("ctrl+f should open the search field")
	}
	if model.searchInput.Value() != "" {
		t.Fatalf("the search field should open empty, got %q", model.searchInput.Value())
	}
	view := model.View().Content
	if !strings.Contains(view, "Buscar: ") {
		t.Fatalf("expected the search bar to be visible, got %q", view)
	}
	if strings.Contains(view, "🔎") {
		t.Fatalf("the prompt must not use an emoji (ambiguous terminal width), got %q", view)
	}
	// The real bug: an empty field with no real Width() set renders only
	// the *first rune of its own placeholder* as if it were the cursor's
	// character, with nothing after it — a stray "b" (from "buscar…")
	// that looks exactly like a keystroke the user never made, and isn't
	// backspace-able because it was never part of the value. A cursor
	// legitimately drawn *over* the placeholder's first letter is fine
	// (that's normal for any empty, focused text field) as long as the
	// rest of the placeholder still follows it — the bug is the
	// placeholder being truncated down to that one letter.
	plain := ansi.Strip(model.searchBar())
	if !strings.Contains(plain, "buscar") {
		t.Fatalf("expected the full placeholder \"buscar…\" to render, got %q", plain)
	}

	// backspace on an empty field must be a harmless no-op, not leave a
	// stray character.
	send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if model.searchInput.Value() != "" {
		t.Fatalf("backspace on an empty field should still be empty, got %q", model.searchInput.Value())
	}

	send(tea.KeyPressMsg{Text: "t", Code: 't'})
	send(tea.KeyPressMsg{Text: "a", Code: 'a'})
	send(tea.KeyPressMsg{Text: "i", Code: 'i'})
	send(tea.KeyPressMsg{Text: "l", Code: 'l'})
	if model.searchInput.Value() != "tail" {
		t.Fatalf("typing t-a-i-l should produce exactly \"tail\", got %q", model.searchInput.Value())
	}

	send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if model.searchInput.Value() != "tai" {
		t.Fatalf("backspace should delete the last character, got %q", model.searchInput.Value())
	}
	send(tea.KeyPressMsg{Text: "l", Code: 'l'})
	if model.searchInput.Value() != "tail" {
		t.Fatalf("retyping the deleted character should restore \"tail\", got %q", model.searchInput.Value())
	}

	send(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model.searchActive {
		t.Fatal("enter should commit and close the search field")
	}
	if model.searchQuery != "tail" {
		t.Fatalf("expected the committed query to be \"tail\", got %q", model.searchQuery)
	}
}
