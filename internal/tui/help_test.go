package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestHelpWindowScrollsWhenContentDoesNotFit covers §6.6's "flotante,
// desplazable si no cabe": at a terminal short enough that the whole
// keymap listing cannot fit in the floating window, arrow keys scroll it
// instead of immediately closing it, and esc still closes it regardless.
func TestHelpWindowScrollsWhenContentDoesNotFit(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 15})
	m := updated.(*Model)
	m.openHelp()

	first := m.View().Content
	if !strings.Contains(first, "↑/↓ desplaza") {
		t.Fatalf("a short terminal should show the scrollable help title, got %q", first)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(*Model)
	if !m.showHelp {
		t.Fatal("↓ should scroll, not close, a help window that needs scrolling")
	}
	if m.helpScroll != 1 {
		t.Fatalf("expected helpScroll to advance by one, got %d", m.helpScroll)
	}
	second := m.View().Content
	if first == second {
		t.Fatal("scrolling should change what is visible in the help window")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(*Model)
	if m.showHelp {
		t.Fatal("esc should still close a scrollable help window")
	}
}

// TestHelpWindowClosesOnAnyKeyWhenItAlreadyFits covers the non-scrolling
// case (the original, simpler behavior): a tall enough terminal shows
// everything at once, so any key closes it immediately.
func TestHelpWindowClosesOnAnyKeyWhenItAlreadyFits(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	m := updated.(*Model)
	m.openHelp()
	view := m.View().Content
	if strings.Contains(view, "desplaza") {
		t.Fatalf("a tall terminal should not claim to be scrollable, got %q", view)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(*Model)
	if m.showHelp {
		t.Fatal("any key should close a help window that already fits")
	}
}
