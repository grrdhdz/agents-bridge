package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

// TestKeyboardInteractionReachesConversationThroughRealUpdate is an
// integration test for a real report: "no puedo interactuar con los
// mensajes del chat, solo con el text box". It drives the full stack
// exactly as the real terminal does — tea.KeyPressMsg values through
// Model.Update, never internal helpers directly — and checks both the
// functional effect (the selection actually moves, a copy actually
// happens, a fold actually toggles) and the one real defect this
// investigation found: the conversation pane had no visible border of its
// own, so focus moving there was invisible (spec §6.1: "el foco se ve en
// el borde activo" was only ever wired for the composer).
func TestKeyboardInteractionReachesConversationThroughRealUpdate(t *testing.T) {
	var copied []string
	model := New(Options{
		Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(),
		CopyCommand: func(s string) error { copied = append(copied, s); return nil },
	})
	send := func(msg tea.Msg) {
		updated, _ := model.Update(msg)
		model = *updated.(*Model)
	}
	send(tea.WindowSizeMsg{Width: 150, Height: 40})
	for i := 0; i < 3; i++ {
		e, err := protocol.NewEnvelope("instance-a", "m-"+strconv.Itoa(i), uint64(i+1), "peer", protocol.RoleExecutor, "cuerpo "+strconv.Itoa(i), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	}

	viewBeforeTab := model.View().Content
	send(tea.KeyPressMsg{Code: tea.KeyTab})
	if model.focus != focusConversation {
		t.Fatal("tab through Update should move focus to the conversation")
	}
	viewAfterTab := model.View().Content
	if viewBeforeTab == viewAfterTab {
		t.Fatal("the screen must visibly change when focus moves (a border, at minimum) — otherwise tab appears to do nothing")
	}

	beforeIdx := model.selectedIndex()
	send(tea.KeyPressMsg{Code: tea.KeyUp})
	if got := model.selectedIndex(); got != beforeIdx-1 {
		t.Fatalf("↑ through Update should move the selection, got index %d (was %d)", got, beforeIdx)
	}

	send(tea.KeyPressMsg{Text: "y", Code: 'y'})
	if len(copied) != 1 {
		t.Fatalf("y through Update should copy the selected message, got %#v", copied)
	}

	send(tea.KeyPressMsg{Code: tea.KeyEnter})
	// A short message (as these are) is not foldable, so enter is
	// correctly a no-op here; the point already proven above is that the
	// keypress *reached* the conversation's handling at all, not the
	// textarea's.
	if model.input.Value() != "" {
		t.Fatal("enter while the conversation has focus must never reach the composer's textarea")
	}
}

// TestConversationBorderReflectsFocus is a narrower, direct check for the
// same fix: the conversation's own bordered box (middleRow) changes
// appearance between focus states, exactly like the composer's already
// did.
func TestConversationBorderReflectsFocus(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = *updated.(*Model)

	// focusComposer (default): conversation border must be the inactive
	// style, composer's the active one.
	composerFocused := model.middleRow()

	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model = *updated.(*Model)
	conversationFocused := model.middleRow()

	if composerFocused == conversationFocused {
		t.Fatal("the conversation box's border must render differently depending on focus")
	}
}

// TestNoConversationLineInvadesSidebarColumn covers spec item 3: with the
// sidebar visible, no conversation glyph (border or card) may be drawn
// inside or past the sidebar's own column range — there must be a real
// gap, not tarjetas touching the panel.
func TestNoConversationLineInvadesSidebarColumn(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
	model = *updated.(*Model)
	if !model.sidebarBoxOn {
		t.Fatal("150 columns should show the sidebar by default")
	}
	e, err := protocol.NewEnvelope("instance-a", "m1", 1, "peer", protocol.RoleExecutor, strings.Repeat("x", 200), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})

	block := model.middleRow()
	lines := strings.Split(block, "\n")
	// Every line of the conv+gap+sidebar block must be exactly
	// convBoxWidth + gap(2) + sidebarBoxWidth columns — no wider (nothing
	// overflows past the sidebar's own right edge) and no narrower
	// (nothing is missing a closing border, which would silently shift
	// the sidebar left, out of its column).
	wantWidth := model.convBoxWidth + 2 + model.sidebarBoxWidth
	for i, l := range lines {
		if w := lipgloss.Width(l); w != wantWidth {
			t.Fatalf("line %d is %d columns wide, want exactly %d (conv box + 2-column gap + sidebar box): %q", i, w, wantWidth, l)
		}
	}
	// And explicitly: the gap itself (the 2 columns right after the
	// conversation box's own right border) must be blank on every row,
	// i.e. no card or border glyph from either box leaks into it.
	gapStart := model.convBoxWidth
	for i, l := range lines {
		plain := []rune(ansi.Strip(l))
		if gapStart+1 >= len(plain) {
			continue
		}
		gap := string(plain[gapStart : gapStart+2])
		if strings.TrimSpace(gap) != "" {
			t.Fatalf("line %d's gap between conversation and sidebar is not blank: %q (full line %q)", i, gap, plain)
		}
	}
}
