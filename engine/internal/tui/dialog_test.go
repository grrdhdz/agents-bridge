package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

// TestConfirmCloseDialogYesClosesHostImmediately covers the palette's
// "Cerrar puente" reaching a host: confirming calls close() (which also
// calls OnStop, per Capabilities.CloseOnQuit).
func TestConfirmCloseDialogYesClosesHostImmediately(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	stopCalls := 0
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), OnStop: func() { stopCalls++ }})
	model.openConfirmCloseDialog()
	if model.confirm == nil {
		t.Fatal("expected a pending confirmation")
	}
	updated, cmd := model.handleDialogKey(tea.KeyPressMsg{Text: "y", Code: 'y'})
	model = *updated.(*Model)
	if model.confirm != nil {
		t.Fatal("confirming should clear the pending dialog")
	}
	if stopCalls != 1 || model.state != "closed" || !transport.closed {
		t.Fatalf("confirming should close the bridge, got stopCalls=%d state=%q closed=%v", stopCalls, model.state, transport.closed)
	}
	if cmd == nil {
		t.Fatal("expected a tea.Quit cmd once the model is closed")
	}
}

// TestConfirmCloseDialogNoCancelsWithoutSideEffects covers the cancel path.
func TestConfirmCloseDialogNoCancelsWithoutSideEffects(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	stopCalls := 0
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), OnStop: func() { stopCalls++ }})
	model.openConfirmCloseDialog()
	updated, _ := model.handleDialogKey(tea.KeyPressMsg{Text: "n", Code: 'n'})
	model = *updated.(*Model)
	if model.confirm != nil {
		t.Fatal("cancelling should clear the pending dialog")
	}
	if stopCalls != 0 || model.state == "closed" || transport.closed {
		t.Fatal("cancelling must have no side effects")
	}
}

// TestConfirmCloseDialogOnObserverStopsWithoutClosingItself mirrors
// handleStopCommand's own distinction (§4): a mode without CloseOnQuit
// (the observer) only asks the endpoint to stop, it does not mark itself
// closed or close its own transport.
func TestConfirmCloseDialogOnObserverStopsWithoutClosingItself(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	stopCalls := 0
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForObserver(), OnStop: func() { stopCalls++ }})
	model.openConfirmCloseDialog()
	updated, _ := model.handleDialogKey(tea.KeyPressMsg{Text: "y", Code: 'y'})
	model = *updated.(*Model)
	if stopCalls != 1 {
		t.Fatalf("expected OnStop to be called once, got %d", stopCalls)
	}
	if model.state == "closed" || transport.closed {
		t.Fatal("the observer must not close its own window/transport just because it asked the endpoint to stop")
	}
}

// TestConfirmCloseDialogWithoutOnStopPushesToastInstead covers join's own
// "no callback" case (existing test TestJoinQuitClosesItsOwnSideWithoutACallback
// covers /quit; this is the same gap for the new dialog entry point).
func TestConfirmCloseDialogWithoutOnStopPushesToastInstead(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleExecutor, Capabilities: CapabilitiesForJoin()})
	model.openConfirmCloseDialog()
	if model.confirm != nil {
		t.Fatal("without OnStop there is nothing to confirm")
	}
	if len(model.toasts) != 1 {
		t.Fatalf("expected a toast explaining why, got %+v", model.toasts)
	}
}
