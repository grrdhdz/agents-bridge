package main

import (
	"context"
	"testing"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

// TestSelectObserverDescriptorPrefersOrchestratorThenExecutor covers §6:
// the observer attaches through the orchestrator's descriptor when it lives
// on this machine, and falls back to the executor's (the join-side case)
// otherwise.
func TestSelectObserverDescriptorPrefersOrchestratorThenExecutor(t *testing.T) {
	run := startLocal(t)
	instanceID := run.ready["instance_id"].(string)

	descriptor, err := selectObserverDescriptor(run.root, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.LocalRole != protocol.RoleOrchestrator {
		t.Fatalf("local mode has both roles; observer should prefer the orchestrator's, got %q", descriptor.LocalRole)
	}

	// A join-side machine only ever has the executor's descriptor.
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	client, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleExecutor, server.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	endpoint, err := control.Start(client, control.Options{Role: protocol.RoleExecutor, Mode: control.ModeTailscaleJoin, Root: run.root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(endpoint.Close)

	descriptor, err = selectObserverDescriptor(run.root, server.InstanceID())
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.LocalRole != protocol.RoleExecutor {
		t.Fatalf("with only an executor descriptor, observer should fall back to it, got %q", descriptor.LocalRole)
	}
}

// TestSelectObserverDescriptorNotFound covers the error path: an unknown
// instance_id is reported, not silently ignored.
func TestSelectObserverDescriptorNotFound(t *testing.T) {
	run := startLocal(t)
	if _, err := selectObserverDescriptor(run.root, "does-not-exist"); err == nil {
		t.Fatal("expected an error for an unknown instance_id")
	}
}

// TestTUIWithoutInstanceIDOpensTheHomeScreen covers spec §5: `agents-bridge
// tui` with no --instance-id is the list of bridges (phase 3), not a usage
// error; --instance-id still goes straight to that bridge.
func TestTUIWithoutInstanceIDOpensTheHomeScreen(t *testing.T) {
	old := startHomeTUI
	t.Cleanup(func() { startHomeTUI = old })
	calls := 0
	startHomeTUI = func(theme.Theme, string) error { calls++; return nil }
	if err := runTUIObserver(nil, t.TempDir()); err != nil || calls != 1 {
		t.Fatalf("expected the home screen to start once, got %d calls, err %v", calls, err)
	}
	if err := runTUIObserver([]string{"--instance-id", "does-not-exist"}, t.TempDir()); err == nil || calls != 1 {
		t.Fatalf("--instance-id must not open the home screen (calls=%d, err=%v)", calls, err)
	}
	if err := runTUIObserver([]string{"--theme", "neon"}, t.TempDir()); err == nil || calls != 1 {
		t.Fatalf("a bad --theme is still rejected before anything starts (calls=%d, err=%v)", calls, err)
	}
}

func TestDirectObserverHasNoHomeToReturnTo(t *testing.T) {
	direct, fromHome := directObserverCapabilities(), tui.CapabilitiesForObserver()
	if direct.ReturnHome {
		t.Fatal("entered with --instance-id there is no home screen: esc must not try to return")
	}
	direct.ReturnHome = fromHome.ReturnHome
	if direct != fromHome {
		t.Fatalf("apart from ReturnHome the direct observer is the same observer: %+v vs %+v", direct, fromHome)
	}
}
