package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

// TestPaletteIsAFloatingWindowOverTheBackdrop covers spec item 4: the
// palette must be a centered floating window, not a full-screen
// replacement — the conversation (and its border) stays visible around
// it, and the composited screen is still exactly m.width × m.height.
func TestPaletteIsAFloatingWindowOverTheBackdrop(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m := updated.(*Model)
	for i := 0; i < 5; i++ {
		e, err := protocol.NewEnvelope("instance-a", "m-"+strconv.Itoa(i), uint64(i+1), "peer", protocol.RoleExecutor, "cuerpo "+strconv.Itoa(i), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		m.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	}
	m.openPalette()
	content := m.View().Content
	lines := strings.Split(content, "\n")
	if len(lines) != 40 {
		t.Fatalf("composited screen should still be exactly 40 lines, got %d", len(lines))
	}
	for i, l := range lines {
		// Trailing whitespace is trimmed by the canvas compositor (a
		// blank backdrop line, or a card's own trailing padding past
		// where content ends, renders narrower than 120 — the terminal
		// fills the rest with its own background either way); the actual
		// invariant is "never wider than the terminal", matching the
		// same check golden_test.go already uses for the non-composited
		// case.
		if w := lipgloss.Width(l); w > 120 {
			t.Fatalf("line %d is wider than the terminal (120): got %d: %q", i, w, l)
		}
	}
	if !strings.Contains(content, "agents-bridge") {
		t.Fatal("the status bar (backdrop) should still be visible around the floating palette")
	}
	if !strings.Contains(content, "cuerpo 4") {
		t.Fatal("the conversation content (backdrop) should still be visible around the floating palette")
	}
	if !strings.Contains(content, "Paleta de comandos") {
		t.Fatal("the palette window itself should be visible")
	}
	// The window is centered, not flush with an edge: its top-left border
	// corner must not be at row 0 or column 0.
	win := m.paletteWindow()
	if win.x == 0 || win.y == 0 {
		t.Fatalf("expected the palette window to be centered, not flush against an edge: x=%d y=%d", win.x, win.y)
	}
}

// TestDialogIsAFloatingWindowOverTheBackdrop mirrors the same check for
// the confirmation dialog.
func TestDialogIsAFloatingWindowOverTheBackdrop(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), OnStop: func() {}})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := updated.(*Model)
	m.openConfirmCloseDialog()
	content := m.View().Content
	if !strings.Contains(content, "agents-bridge") {
		t.Fatal("the status bar should still be visible around the floating dialog")
	}
	if !strings.Contains(content, "Cerrar puente") {
		t.Fatal("the dialog window itself should be visible")
	}
	win := m.dialogWindow()
	if win.x == 0 || win.y == 0 {
		t.Fatalf("expected the dialog to be centered, not flush against an edge: x=%d y=%d", win.x, win.y)
	}
}

// TestHelpIsAFloatingWindowOverTheBackdrop mirrors the same check for the
// help overlay.
func TestHelpIsAFloatingWindowOverTheBackdrop(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m := updated.(*Model)
	m.openHelp()
	content := m.View().Content
	if !strings.Contains(content, "agents-bridge") {
		t.Fatal("the status bar should still be visible around the floating help window")
	}
	if !strings.Contains(content, "Ayuda") {
		t.Fatal("the help window itself should be visible")
	}
}

// TestToastsDoNotChangeLayoutHeight covers spec item 4's last bullet:
// toasts float over the conversation and must never grow or shrink the
// rest of the layout (composer position, footer, etc.) — pushing a toast
// must not move anything else.
func TestToastsDoNotChangeLayoutHeight(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := updated.(*Model)
	before := m.View().Content
	beforeConvBoxHeight := m.convBoxHeight

	m.pushToast("aviso uno")
	m.pushToast("aviso dos")
	m.pushToast("aviso tres")

	after := m.View().Content
	if m.convBoxHeight != beforeConvBoxHeight {
		t.Fatalf("pushing toasts must not change the conversation box height, got %d, want %d", m.convBoxHeight, beforeConvBoxHeight)
	}
	linesBefore := strings.Split(before, "\n")
	linesAfter := strings.Split(after, "\n")
	if len(linesBefore) != len(linesAfter) {
		t.Fatalf("pushing toasts changed the total number of screen lines: %d -> %d", len(linesBefore), len(linesAfter))
	}
	if !strings.Contains(after, "aviso tres") {
		t.Fatal("expected the toast to actually be visible somewhere on screen")
	}
}
