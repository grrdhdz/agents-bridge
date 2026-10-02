package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

// TestRealControlTransportIdleMatchesWallClockNotAFakeOffset reproduces a
// real report: a bridge created minutes ago, with a message sent seconds
// ago, showed "inactivo 430m49s" in the status bar and "activo 430m50s" /
// "inactivo 430m49s" in the sidebar — durations with no relation to
// reality. This drives a *real* control.Endpoint (ControlTransport, the
// observer's transport) with the real wall clock (no injected Now),
// publishes a live message, and checks that the reported idle/uptime stay
// within a few seconds of the real elapsed time.
func TestRealControlTransportIdleMatchesWallClockNotAFakeOffset(t *testing.T) {
	h := newObserverHarness(t)
	transport := NewControlTransport(h.ownerEndpoint.Descriptor())
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForObserver()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := updated.(*Model)

	sub, err := transport.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)

	// The bridge (control.Start, in newObserverHarness) was created just
	// now, in this test — StartedAt must be within a second or two of
	// "now", never hundreds of minutes off.
	status, err := transport.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(status.StartedAt); d < 0 || d > 5*time.Second {
		t.Fatalf("StatusProvider.Status reported StartedAt %v ago, want well under 5s (a freshly created bridge): %v", d, status.StartedAt)
	}

	// A live peer message, published for real through the worker's own
	// client (exactly what a real executor sending a message does), must
	// bring lastActivity — and therefore the status bar's idle reading —
	// back to (approximately) zero, not leave it at whatever it was.
	time.Sleep(50 * time.Millisecond) // let some real, small idle accrue first
	if _, err := h.worker.Publish("hola"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	event, err := sub.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	m.handleFrame(event.Frame)

	idle := time.Now().Sub(m.lastActivity)
	if idle < 0 || idle > 5*time.Second {
		t.Fatalf("after a message arrived moments ago, real idle since lastActivity is %v, want well under 5s", idle)
	}

	view := m.View().Content
	if strings.Contains(view, "430m") {
		t.Fatalf("status bar still shows the bogus ~430 minute reading: %q", view)
	}
}

// TestClientTransportIdleMatchesWallClockNotAFakeOffset is the same check
// against clientTransport (host/join's own transport, wrapping a real
// *bridge.Client), so both of the redesign's Transport implementations are
// covered, per the report's request.
func TestClientTransportIdleMatchesWallClockNotAFakeOffset(t *testing.T) {
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	owner, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	worker, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleExecutor, server.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)

	model := New(Options{Client: owner, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), PeerConnected: server.WorkerConnected})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := updated.(*Model)
	sub, err := m.transport.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)

	status, err := m.transport.(StatusProvider).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(status.StartedAt); d < 0 || d > 5*time.Second {
		t.Fatalf("clientTransport.Status reported StartedAt %v ago, want well under 5s: %v", d, status.StartedAt)
	}

	time.Sleep(50 * time.Millisecond)
	if _, err := worker.Publish("hola"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	event, err := sub.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	m.handleFrame(event.Frame)

	idle := time.Now().Sub(m.lastActivity)
	if idle < 0 || idle > 5*time.Second {
		t.Fatalf("after a message arrived moments ago, real idle since lastActivity is %v, want well under 5s", idle)
	}
}
