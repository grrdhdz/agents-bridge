package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

func newSelectionTestModel(t *testing.T, count int) *Model {
	t.Helper()
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = *updated.(*Model)
	for i := 0; i < count; i++ {
		e, err := protocol.NewEnvelope("instance-a", "m-"+strconv.Itoa(i), uint64(i+1), "peer", protocol.RoleExecutor, "cuerpo "+strconv.Itoa(i), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	}
	// Give the conversation focus, matching how a person would actually
	// reach these keys (tab first).
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	return updated.(*Model)
}

// TestSelectionDefaultsToLastMessageAndMovesWithArrows covers §6.3
// "Selección": entering the conversation selects the last message by
// default, and ↑/↓ (and k/j) move it.
func TestSelectionDefaultsToLastMessageAndMovesWithArrows(t *testing.T) {
	model := newSelectionTestModel(t, 5)
	if got := model.selectedIndex(); got != 4 {
		t.Fatalf("expected the last message (index 4) selected by default, got %d", got)
	}
	model.moveSelection(-1)
	if got := model.selectedIndex(); got != 3 {
		t.Fatalf("up should move to index 3, got %d", got)
	}
	model.moveSelection(-10)
	if got := model.selectedIndex(); got != 0 {
		t.Fatalf("moving past the start should clamp to index 0, got %d", got)
	}
	model.moveSelection(10)
	if got := model.selectedIndex(); got != 4 {
		t.Fatalf("moving past the end should clamp to the last index, got %d", got)
	}
}

// TestSelectFirstAndLastJumpToEnds covers g/G's amended meaning
// (message-level, not raw scroll).
func TestSelectFirstAndLastJumpToEnds(t *testing.T) {
	model := newSelectionTestModel(t, 5)
	model.selectFirst()
	if got := model.selectedIndex(); got != 0 {
		t.Fatalf("selectFirst should land on index 0, got %d", got)
	}
	model.selectLast()
	if got := model.selectedIndex(); got != 4 {
		t.Fatalf("selectLast should land on the last index, got %d", got)
	}
}

// TestSelectionMarkerAppearsOnlyWhileConversationHasFocus ensures the
// rendered ▶ marker (renderCardWithParams' Selected option) never shows
// while the composer has focus, where a selection would be meaningless.
func TestSelectionMarkerAppearsOnlyWhileConversationHasFocus(t *testing.T) {
	model := newSelectionTestModel(t, 2)
	view := model.View().Content
	if !strings.Contains(view, "▶") {
		t.Fatalf("expected the selection marker while the conversation has focus: %q", view)
	}
	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // back to composer
	model = updated.(*Model)
	view = model.View().Content
	if strings.Contains(view, "▶") {
		t.Fatalf("selection marker should not show while the composer has focus: %q", view)
	}
}

// TestToggleFoldExpandsAndCollapsesALongMessage covers "enter
// pliega/despliega": a message long enough to be folded by default expands
// on the first enter and re-collapses on the second.
func TestToggleFoldExpandsAndCollapsesALongMessage(t *testing.T) {
	model := newSelectionTestModel(t, 1)
	// A bullet list, not plain paragraph lines: CommonMark reflows plain
	// single-newline-separated text into one wrapped paragraph, which
	// would not reliably exceed 30 rendered lines. Each list item stays
	// on its own rendered line.
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "- línea larga número "+strconv.Itoa(i))
	}
	long := strings.Join(lines, "\n")
	e, err := protocol.NewEnvelope("instance-a", "long-1", 100, "peer", protocol.RoleExecutor, long, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	model.selectLast()

	view := model.View().Content
	if !strings.Contains(view, "líneas más") {
		t.Fatalf("a 40-line message should be folded by default: %q", view)
	}

	model.toggleFold()
	view = model.View().Content
	if strings.Contains(view, "líneas más") {
		t.Fatalf("enter should expand the folded message: %q", view)
	}

	model.toggleFold()
	view = model.View().Content
	if !strings.Contains(view, "líneas más") {
		t.Fatalf("a second enter should re-collapse it: %q", view)
	}
}

// TestCopySelectedUsesCopyCommandAndPushesToast covers §6.3's "y copia el
// cuerpo" plus §6.7's copy notice.
func TestCopySelectedUsesCopyCommandAndPushesToast(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	var copied []string
	model := New(Options{
		Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(),
		CopyCommand: func(s string) error { copied = append(copied, s); return nil },
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = *updated.(*Model)
	e, err := protocol.NewEnvelope("instance-a", "m1", 1, "peer", protocol.RoleExecutor, "TAREA\ncopia esto", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	model.focus = focusConversation
	model.selectLast()
	model.copySelected()
	if len(copied) != 1 || copied[0] != "copia esto" {
		t.Fatalf("expected the label-stripped body to be copied, got %#v", copied)
	}
	if len(model.toasts) != 1 || !strings.Contains(model.toasts[0].text, "copiado") {
		t.Fatalf("expected a copy toast, got %+v", model.toasts)
	}
}

// TestCopySelectedFailureStillPushesToast covers the failure path: no
// silent no-op when the clipboard command errors.
func TestCopySelectedFailureStillPushesToast(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{
		Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(),
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = *updated.(*Model)
	e, err := protocol.NewEnvelope("instance-a", "m1", 1, "peer", protocol.RoleExecutor, "hola", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	model.focus = focusConversation
	model.selectLast()
	model.copySelected()
	if len(model.toasts) != 1 || !strings.Contains(model.toasts[0].text, "no se pudo copiar") {
		t.Fatalf("expected a failure toast when there is no clipboard command, got %+v", model.toasts)
	}
}

// TestJumpSearchWalksMatchesAndWraps covers ctrl+f + n/N.
func TestJumpSearchWalksMatchesAndWraps(t *testing.T) {
	model := newSelectionTestModel(t, 0)
	bodies := []string{"nada", "clave uno", "nada", "clave dos"}
	for i, body := range bodies {
		e, err := protocol.NewEnvelope("instance-a", "m-"+strconv.Itoa(i), uint64(i+1), "peer", protocol.RoleExecutor, body, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	}
	model.searchQuery = "clave"
	model.searchIdx = -1
	model.jumpSearch(1)
	if model.selected != "m-1" {
		t.Fatalf("first jump should land on the first match (m-1), got %q", model.selected)
	}
	model.jumpSearch(1)
	if model.selected != "m-3" {
		t.Fatalf("second jump should land on the second match (m-3), got %q", model.selected)
	}
	model.jumpSearch(1)
	if model.selected != "m-1" {
		t.Fatalf("a third jump should wrap back to the first match, got %q", model.selected)
	}
	model.jumpSearch(-1)
	if model.selected != "m-3" {
		t.Fatalf("N should wrap backward, got %q", model.selected)
	}
}

// TestSearchHighlightsMatchesInView is an end-to-end check that committing
// a search actually shows the highlight in the rendered conversation.
func TestSearchHighlightsMatchesInView(t *testing.T) {
	model := newSelectionTestModel(t, 0)
	e, err := protocol.NewEnvelope("instance-a", "m1", 1, "peer", protocol.RoleExecutor, "buscar esta palabra clave", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	model.openSearch()
	model.searchInput.SetValue("clave")
	updated, _ := model.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if model.searchActive {
		t.Fatal("enter should commit and close the search field")
	}
	if model.searchQuery != "clave" {
		t.Fatalf("expected the committed query to survive, got %q", model.searchQuery)
	}
	view := model.View().Content
	// SearchMatchStyle (theme.go) is an explicit Surface-on-Notice swap,
	// not Style.Reverse — reverse video only flips whatever fg/bg a span
	// already carries, which would have reversed the card's own painted
	// Surface background right back to plain terminal colors instead of
	// standing out. The Notice color (used nowhere else on this line) is
	// the reliable signal that the match got its own styling.
	notice := model.th.Notice
	r, g, b, _ := notice.RGBA()
	noticeEscape := fmt.Sprintf("48;2;%d;%d;%d", uint8(r>>8), uint8(g>>8), uint8(b>>8))
	if !strings.Contains(view, "clave") {
		t.Fatalf("expected the match's text to still be present: %q", view)
	}
	if !strings.Contains(view, noticeEscape) {
		t.Fatalf("expected the search match highlighted with the theme's Notice color (%s), got %q", noticeEscape, view)
	}
}

// TestSidebarToggleOverridesWidthBreakpoint covers §6.4/ctrl+b: even below
// the 110-column breakpoint, the explicit toggle shows it, and toggling
// again hides it regardless of width.
func TestSidebarToggleOverridesWidthBreakpoint(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	model = *updated.(*Model)
	if model.sidebarVisible() {
		t.Fatal("80 columns should not show the sidebar by default")
	}
	model.toggleSidebar()
	if !model.sidebarVisible() {
		t.Fatal("ctrl+b should force the sidebar open even below the breakpoint")
	}
	if !strings.Contains(model.View().Content, "PUENTE") {
		t.Fatal("expected the sidebar's own content once forced open")
	}
	model.toggleSidebar()
	if model.sidebarVisible() {
		t.Fatal("a second ctrl+b should hide it again")
	}
}

func TestSidebarVisibleByDefaultAtWideTerminal(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model = *updated.(*Model)
	if !model.sidebarVisible() {
		t.Fatal("120 columns should show the sidebar by default (>= 110)")
	}
}
