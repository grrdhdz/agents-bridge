package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

func hasControl(s string) bool {
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

func sanitizeModel(t *testing.T, width, height int) *Model {
	t.Helper()
	m := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	m.input.Focus()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(*Model)
}

func TestComposerDropsControlCharactersFromTypedAndPastedText(t *testing.T) {
	m := sanitizeModel(t, 100, 30)
	for _, text := range []string{"\x1b", "\x12", "E\x12", "\x1b[<32;56;57M", "a\x7fb", "x\u009by"} {
		m.Update(tea.KeyPressMsg{Text: text})
	}
	m.Update(tea.PasteMsg{Content: "hola\x1b[<64;39;29M mundo\x12\r\nlínea\ttab\x00"})
	value := m.input.Value()
	if hasControl(value) {
		t.Fatalf("composer holds control characters: %q", value)
	}
	if strings.Contains(value, "[<") {
		t.Fatalf("a whole escape sequence (ESC [ ... M) must go, not just its ESC: %q", value)
	}
	if !strings.Contains(value, "hola") || !strings.Contains(value, "mundo\nlínea") || !strings.Contains(value, "tab") {
		t.Fatalf("legitimate text (with newline and tab) must survive: %q", value)
	}
}

func TestSearchFieldDropsControlCharacters(t *testing.T) {
	m := sanitizeModel(t, 100, 30)
	m.openSearch()
	m.Update(tea.KeyPressMsg{Text: "a\x1b\x12b"})
	m.Update(tea.PasteMsg{Content: "c\x1bd"})
	v := m.searchInput.Value()
	if hasControl(v) {
		t.Fatalf("search field holds control characters: %q", v)
	}
	if !strings.Contains(v, "ab") {
		t.Fatalf("the printable parts must be kept: %q", v)
	}
}

func TestViewKeepsTerminalHeightEvenWithAnomalousComposerContent(t *testing.T) {
	for _, h := range []int{16, 24, 40} {
		m := sanitizeModel(t, 100, h)
		// Bypass the input filter: this is the last line of defense.
		m.input.SetValue("E\x12\x1b[<32;56;57M\x1b[<32;55;57M\n\n\n\n\n\n\n\x1b[<64;39;29M\r\r")
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != h {
			t.Fatalf("height %d: View() has %d lines", h, len(lines))
		}
		if !strings.Contains(lines[0], "codex-bridge") {
			t.Fatalf("height %d: status bar must stay first, got %q", h, lines[0])
		}
	}
}
