package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

func click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func wheelDown(x, y int) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}
}

// newMouseTestModel builds a Model with count short messages, sized and
// rendered once (so hitregion.go's regions are populated exactly as they
// would be for a real frame) — every mouse test below clicks into that
// already-rendered frame, the same information Update would have.
func newMouseTestModel(t *testing.T, width, height, count int) *Model {
	t.Helper()
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m := updated.(*Model)
	for i := 0; i < count; i++ {
		e, err := protocol.NewEnvelope("instance-a", "m-"+strconv.Itoa(i), uint64(i+1), "peer", protocol.RoleExecutor, "cuerpo "+strconv.Itoa(i), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		m.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	}
	m.View() // populate m.regions, exactly like a real frame
	return m
}

// TestClickOnCardSelectsItAndFocusesConversation covers the mouse
// addendum's first bullet.
func TestClickOnCardSelectsItAndFocusesConversation(t *testing.T) {
	m := newMouseTestModel(t, 100, 30, 3)
	if m.focus != focusComposer {
		t.Fatal("composer should have focus by default")
	}
	// Row convBoxTop+1 (the box's first interior row) is the first
	// message's header line.
	updated, _ := m.Update(click(convBoxLeft+2, convBoxTop+1))
	m = updated.(*Model)
	if m.focus != focusConversation {
		t.Fatal("clicking a card should move focus to the conversation")
	}
	if m.selected == "" {
		t.Fatal("clicking a card should select it")
	}
}

// TestClickOnFoldIndicatorExpandsCard covers the mouse addendum's second
// bullet (expand half).
func TestClickOnFoldIndicatorExpandsCard(t *testing.T) {
	m := newMouseTestModel(t, 100, 30, 0)
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "- línea "+strconv.Itoa(i))
	}
	e, err := protocol.NewEnvelope("instance-a", "long-1", 1, "peer", protocol.RoleExecutor, strings.Join(lines, "\n"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	m.View()

	if !m.isCardFolded(e) {
		t.Fatal("a 40-line message should start folded")
	}
	// Find the fold-indicator row among this frame's registered regions by
	// asking resolveCardClick for every viewport row, exactly like
	// registerCardRegions did — a real test should locate the row the same
	// way View derived it, not hard-code a guess.
	var foldY int = -1
	for r := 0; r < m.viewport.Height(); r++ {
		row, ok := m.resolveCardClick(r)
		if ok && ansiContainsFoldIndicator(row.text) {
			foldY = convBoxTop + 1 + r
			break
		}
	}
	if foldY < 0 {
		t.Fatal("expected a visible fold indicator row for a folded 40-line message")
	}
	updated, _ := m.Update(click(convBoxLeft+2, foldY))
	m = updated.(*Model)
	if m.isCardFolded(e) {
		t.Fatal("clicking the fold indicator should expand the card")
	}
}

// TestClickOnExpandedHeaderCollapsesCard covers the mouse addendum's
// second bullet (collapse half).
func TestClickOnExpandedHeaderCollapsesCard(t *testing.T) {
	m := newMouseTestModel(t, 100, 30, 0)
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "- línea "+strconv.Itoa(i))
	}
	e, err := protocol.NewEnvelope("instance-a", "long-1", 1, "peer", protocol.RoleExecutor, strings.Join(lines, "\n"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	m.setCardFolded(e.MessageID, false) // expand it first
	m.viewport.GotoTop()                // refreshViewport auto-follows the bottom; force the header into view
	m.View()

	row, ok := m.resolveCardClick(0)
	if !ok || !row.isHeader {
		t.Fatal("row 0 should be this message's header")
	}
	updated, _ := m.Update(click(convBoxLeft+2, convBoxTop+1))
	m = updated.(*Model)
	if !m.isCardFolded(e) {
		t.Fatal("clicking an already-expanded message's header should collapse it")
	}
}

// TestClickOnComposerFocusesIt covers the mouse addendum's third bullet.
func TestClickOnComposerFocusesIt(t *testing.T) {
	m := newMouseTestModel(t, 100, 30, 1)
	updated, _ := m.Update(click(convBoxLeft+2, convBoxTop+1))
	m = updated.(*Model)
	if m.focus != focusConversation {
		t.Fatal("setup: clicking a card should have focused the conversation")
	}
	m.View()
	// View's row bookkeeping: status(0) + blank(1) + middleRow(convBoxHeight
	// rows, starting at convBoxTop) + blank(1) + shortcutsBar(1) + blank(1)
	// = the composer box's own top border row, with no search/unread/
	// error/copyInfo/toasts present in this test.
	composerBorderRow := convBoxTop + m.convBoxHeight + 3
	updated, _ = m.Update(click(convBoxLeft+3, composerBorderRow+1))
	m = updated.(*Model)
	if m.focus != focusComposer {
		t.Fatal("clicking the composer should give it focus")
	}
}

// TestWheelScrollsConversationWithoutChangingFocus and
// TestWheelOverSidebarDoesNothing cover the mouse addendum's fourth
// bullet.
func TestWheelScrollsConversationWithoutChangingFocus(t *testing.T) {
	m := newMouseTestModel(t, 100, 20, 40)
	m.viewport.GotoTop()
	beforeTop := m.viewport.YOffset()
	updated, _ := m.Update(wheelDown(convBoxLeft+2, convBoxTop+1))
	m = updated.(*Model)
	if m.viewport.YOffset() == beforeTop {
		t.Fatal("a wheel event over the conversation should scroll it")
	}
	if m.focus != focusComposer {
		t.Fatal("a wheel event must never change focus")
	}
}

func TestWheelOverSidebarDoesNothing(t *testing.T) {
	m := newMouseTestModel(t, 150, 20, 40)
	if !m.sidebarBoxOn {
		t.Fatal("150 columns should show the sidebar")
	}
	before := m.viewport.YOffset()
	sidebarX := m.convBoxWidth + 3
	updated, _ := m.Update(wheelDown(sidebarX, convBoxTop+1))
	m = updated.(*Model)
	if m.viewport.YOffset() != before {
		t.Fatal("a wheel event over the sidebar must not scroll the conversation underneath it")
	}
	if m.focus != focusComposer {
		t.Fatal("a wheel event must never change focus")
	}
}

// TestPaletteItemClickRunsIt and TestPaletteClickOutsideCloses cover the
// mouse addendum's fifth bullet (palette half).
func TestPaletteItemClickRunsIt(t *testing.T) {
	m := newMouseTestModel(t, 100, 30, 0)
	m.openPalette()
	for _, r := range "tema" {
		updated, _ := m.handlePaletteKey(tea.KeyPressMsg{Text: string(r)})
		m = updated.(*Model)
	}
	beforeMode := m.th.Mode
	m.View()
	items := m.filteredPaletteItems()
	if len(items) != 1 || items[0].id != "toggle-theme" {
		t.Fatalf("setup: expected exactly toggle-theme after filtering, got %#v", items)
	}
	// paletteWindow lays out: title(0), blank(1), query(2), blank(3),
	// item(4), then centers that (a fixed-width, content-height) box over
	// the whole screen (spec's floating overlay rewrite) — so the item's
	// absolute screen position is the window's own (x, y) plus its row 4.
	// m.View() (above) already built the window and registered its
	// regions once; recompute the same (x, y) here rather than calling
	// paletteWindow() again, which would just register a harmless but
	// confusing duplicate set of regions.
	innerWidth := paletteInnerWidth(m.width)
	outerWidth := innerWidth + 4
	outerHeight := 5 + 2 // title/blank/query/blank/item + border
	winX, winY := centerWindow(m.width, m.height, outerWidth, outerHeight)
	itemRow := 4
	updated, _ := m.Update(click(winX+2, winY+1+itemRow))
	m = updated.(*Model)
	if m.paletteOpen {
		t.Fatal("clicking an item should close the palette")
	}
	if m.th.Mode == beforeMode {
		t.Fatal("clicking the toggle-theme item should have run it")
	}
}

func TestPaletteClickOutsideCloses(t *testing.T) {
	m := newMouseTestModel(t, 100, 30, 0)
	m.openPalette()
	m.View()
	updated, _ := m.Update(click(0, 25)) // well past any item row
	m = updated.(*Model)
	if m.paletteOpen {
		t.Fatal("a click that misses every item should close the palette")
	}
}

// TestDialogButtonClicksConfirmAndCancel and
// TestDialogClickOutsideCancels cover the mouse addendum's fifth bullet
// (dialog half).
func TestDialogButtonClicksConfirmAndCancel(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	stopCalls := 0
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), OnStop: func() { stopCalls++ }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := updated.(*Model)
	m.openConfirmCloseDialog()
	m.View()
	// dialogWindow lays out: message(0), blank(1), buttons(2), blank(3),
	// help(4), centered as a floating window (spec's overlay rewrite).
	// win.x/win.y come from a second call to dialogWindow — harmless
	// (idempotent; it just re-registers the same regions) — rather than
	// duplicating its innerWidth arithmetic here.
	win := m.dialogWindow()
	buttonsRow := 2
	updated, _ = m.Update(click(win.x+2, win.y+1+buttonsRow))
	m = updated.(*Model)
	if stopCalls != 1 || m.confirm != nil {
		t.Fatalf("clicking the close button should confirm, got stopCalls=%d confirm=%v", stopCalls, m.confirm)
	}
}

func TestDialogClickOutsideCancels(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	stopCalls := 0
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), OnStop: func() { stopCalls++ }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := updated.(*Model)
	m.openConfirmCloseDialog()
	m.View()
	updated, _ = m.Update(click(0, 10)) // below the buttons row
	m = updated.(*Model)
	if stopCalls != 0 || m.confirm != nil {
		t.Fatalf("a click outside the buttons should cancel without confirming, got stopCalls=%d confirm=%v", stopCalls, m.confirm)
	}
}

// TestShortcutClickRunsItsAction and TestHelpClickAnywhereCloses cover the
// mouse addendum's sixth and seventh bullets.
func TestShortcutClickRunsItsAction(t *testing.T) {
	m := newMouseTestModel(t, 150, 30, 0)
	if m.sidebarBoxOn == false {
		t.Fatal("setup expects the sidebar breakpoint")
	}
	row := convBoxTop + m.convBoxHeight + 1
	rendered := m.shortcutsBarLine()
	idx := strings.Index(rendered, "ctrl+b panel lateral")
	if idx < 0 {
		t.Fatalf("expected the sidebar-toggle shortcut to be visible at 150 columns, got %q", rendered)
	}
	wasVisible := m.sidebarBoxOn
	updated, _ := m.Update(click(idx+1, row))
	m = updated.(*Model)
	if m.sidebarBoxOn == wasVisible {
		t.Fatal("clicking the \"ctrl+b panel lateral\" shortcut should toggle the sidebar")
	}
}

func TestHelpClickAnywhereCloses(t *testing.T) {
	m := newMouseTestModel(t, 100, 30, 0)
	m.showHelp = true
	m.View()
	updated, _ := m.Update(click(5, 5))
	m = updated.(*Model)
	if m.showHelp {
		t.Fatal("any click on the help overlay should close it")
	}
}
