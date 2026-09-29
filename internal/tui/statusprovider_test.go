package tui

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

// fakeStatusTransport is a fakeTransport that also implements
// StatusProvider, so Model.Init wires up the poll (spec §7).
type fakeStatusTransport struct {
	fakeTransport
	status BridgeStatus
	err    error
	calls  int
}

func (f *fakeStatusTransport) Status(context.Context) (BridgeStatus, error) {
	f.calls++
	return f.status, f.err
}

// TestInitPollsStatusProviderWhenTransportSupportsIt covers §7: Init must
// include the poll command when the transport implements StatusProvider.
func TestInitPollsStatusProviderWhenTransportSupportsIt(t *testing.T) {
	transport := &fakeStatusTransport{fakeTransport: fakeTransport{instanceID: "abc"}, status: BridgeStatus{PeerConnected: true, Cwd: "/tmp/proj"}}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	// Init returns a tea.Batch of commands, several of which are
	// long-lived tickers (reconnectTick, statusPollTick) that must never
	// be invoked synchronously in a test — only fetchStatusCmd's own
	// one-shot Status call matters here (spec: "sondeo simulado" means
	// calling the fetch directly, not running Bubble Tea's real loop).
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("expected Init to return commands")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected Init's cmd to produce a tea.BatchMsg, got %T", cmd())
	}
	var applied bool
	for _, sub := range batch {
		if sub == nil {
			continue
		}
		if result, ok := sub().(statusResultMsg); ok {
			updated, _ := model.Update(result)
			model = *updated.(*Model)
			applied = true
		}
	}
	if !applied {
		t.Fatal("expected one of Init's commands to be fetchStatusCmd, producing a statusResultMsg")
	}
	if !model.haveStatus {
		t.Fatal("expected a status result to have been applied")
	}
	if !model.status.PeerConnected || model.status.Cwd != "/tmp/proj" {
		t.Fatalf("expected the fake provider's status to be stored, got %+v", model.status)
	}
	if transport.calls != 1 {
		t.Fatalf("expected exactly one Status call from Init's own fetch, got %d", transport.calls)
	}
}

// TestInitDoesNotPollWhenTransportLacksStatusProvider covers the fallback:
// a plain fakeTransport does not implement StatusProvider, so no poll
// command should be scheduled and haveStatus stays false.
func TestInitDoesNotPollWhenTransportLacksStatusProvider(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	model.Init()
	if model.haveStatus {
		t.Fatal("a transport without StatusProvider should never populate status")
	}
}

// TestStatusResultTogglePushesConnectDisconnectToast covers the sidebar's
// participant presence combined with §6.7's connect/disconnect notice: the
// first poll only establishes a baseline (no toast, nothing to compare
// against yet), and a later poll whose PeerConnected flips produces
// exactly one toast.
func TestStatusResultTogglePushesConnectDisconnectToast(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})

	updated, _ := model.Update(statusResultMsg{status: BridgeStatus{PeerConnected: true}})
	model = *updated.(*Model)
	if len(model.toasts) != 0 {
		t.Fatalf("the first poll should just establish a baseline, no toast yet, got %+v", model.toasts)
	}

	updated, _ = model.Update(statusResultMsg{status: BridgeStatus{PeerConnected: false}})
	model = *updated.(*Model)
	if len(model.toasts) != 1 {
		t.Fatalf("a peer disconnect should push exactly one toast, got %+v", model.toasts)
	}

	updated, _ = model.Update(statusResultMsg{status: BridgeStatus{PeerConnected: false}})
	model = *updated.(*Model)
	if len(model.toasts) != 1 {
		t.Fatalf("an unchanged poll should not push another toast, got %+v", model.toasts)
	}

	updated, _ = model.Update(statusResultMsg{status: BridgeStatus{PeerConnected: true}})
	model = *updated.(*Model)
	if len(model.toasts) != 2 {
		t.Fatalf("reconnecting should push a second toast, got %+v", model.toasts)
	}
}

// TestStatusResultErrorIsIgnored covers a failed poll: it must not
// overwrite the last known good status or spuriously flip the toast
// tracking.
func TestStatusResultErrorIsIgnored(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(statusResultMsg{status: BridgeStatus{PeerConnected: true}, err: nil})
	model = *updated.(*Model)
	updated, _ = model.Update(statusResultMsg{err: errors.New("boom")})
	model = *updated.(*Model)
	if !model.status.PeerConnected {
		t.Fatal("a failed poll must not clobber the last known good status")
	}
	if len(model.toasts) != 0 {
		t.Fatal("a failed poll must not push a spurious toast")
	}
}
