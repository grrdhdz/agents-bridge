package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

// TestViewFillsExactlyTheTerminalHeightWithStatusBarFirst is a regression
// test for a real report: with the sidebar visible, the status bar simply
// did not appear on screen — the view scrolled past the top of the
// terminal. The suspected cause is that resize()'s height budget was not
// updated to account for the conversation's own new border (2 rows) on
// top of everything already budgeted (composer border, footer lines),
// so the total content grew taller than the terminal without resize()
// ever finding out.
func TestViewFillsExactlyTheTerminalHeightWithStatusBarFirst(t *testing.T) {
	for _, height := range []int{20, 30, 50} {
		for _, width := range []int{60, 80, 120, 160} {
			t.Run(sizeName(width, height), func(t *testing.T) {
				transport := &fakeTransport{instanceID: "abc"}
				model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
				updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
				m := updated.(*Model)
				content := m.View().Content
				lines := strings.Split(content, "\n")
				if len(lines) != height {
					t.Fatalf("%dx%d: View() produced %d lines, want exactly %d:\n%s", width, height, len(lines), height, content)
				}
				if !strings.Contains(lines[0], "agents-bridge") {
					t.Fatalf("%dx%d: the first line should be the status bar, got %q", width, height, lines[0])
				}
			})
		}
	}
}

// TestViewFillsExactlyTheTerminalHeightWithOptionalFooterLines covers the
// other half of the same bug: footerLineCount previously never counted
// the copy-info line (host pairing instructions), so whenever it was
// visible the real content was one row taller than resize() budgeted for.
func TestViewFillsExactlyTheTerminalHeightWithOptionalFooterLines(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), JoinCommand: "agents-bridge join --x", CopyCommand: func(string) error { return nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := updated.(*Model)
	m.Init() // copies the join command, populating copyInfo
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*Model)
	m.error = "algo salió mal"
	m.unread = 3
	m.resize()

	content := m.View().Content
	lines := strings.Split(content, "\n")
	if len(lines) != 30 {
		t.Fatalf("with copyInfo, error and unread all present, View() produced %d lines, want exactly 30:\n%s", len(lines), content)
	}
}

func sizeName(w, h int) string {
	return "w" + itoaForTest(w) + "h" + itoaForTest(h)
}

func itoaForTest(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
