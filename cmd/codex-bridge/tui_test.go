package main

import (
	"context"
	"strings"
	"testing"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
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

// TestRunTUIObserverRequiresInstanceID is a small usage-error check; it
// never reaches tea.Program.Run because instance selection fails first.
func TestRunTUIObserverRequiresInstanceID(t *testing.T) {
	if err := runTUIObserver(nil, t.TempDir()); err == nil || !strings.Contains(err.Error(), "--instance-id") {
		t.Fatalf("expected a usage error, got %v", err)
	}
}
