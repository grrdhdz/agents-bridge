package bridges

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/internal/bridge"
	"github.com/grrdhdz/agents-bridge/internal/control"
	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

type liveBridge struct {
	root       string
	instanceID string
	cwd        string
	stopped    *atomic.Bool
}

// startBridge runs a real loopback server with both roles' control
// endpoints, like `agents-bridge local` does.
func startBridge(t *testing.T, root, cwd string) liveBridge {
	t.Helper()
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
	stopped := &atomic.Bool{}
	ownerEndpoint, err := control.Start(owner, control.Options{Role: protocol.RoleOrchestrator, Mode: control.ModeLocal, CWD: cwd, Root: root, CanStop: true, Stop: func() { stopped.Store(true) }, PeerConnected: func() bool { return true }, Activity: control.NewActivity()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ownerEndpoint.Close)
	workerEndpoint, err := control.Start(worker, control.Options{Role: protocol.RoleExecutor, Mode: control.ModeLocal, CWD: cwd, Root: root, PeerConnected: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workerEndpoint.Close)
	return liveBridge{root: root, instanceID: server.InstanceID(), cwd: cwd, stopped: stopped}
}

func privateRoot(t *testing.T) string { return filepath.Join(t.TempDir(), "instances") }

func TestListDescribesLiveBridgesGroupedWithProjectAndPeer(t *testing.T) {
	root := privateRoot(t)
	one := startBridge(t, root, filepath.Join("/work", "api-server"))
	two := startBridge(t, root, "")

	infos, err := List(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("two instances (each with two role descriptors) should be two rows, got %d: %+v", len(infos), infos)
	}
	byID := map[string]Info{}
	for _, info := range infos {
		byID[info.InstanceID] = info
	}
	a := byID[one.instanceID]
	if a.Project != "api-server" || a.Mode != "local" || len(a.Roles) != 2 || !a.PeerConnected || a.PID == 0 || a.StartedAt.IsZero() {
		t.Fatalf("unexpected description: %+v", a)
	}
	if _, ok := byID[two.instanceID]; !ok {
		t.Fatal("the second instance is listed too")
	}
}

func TestSelectStopDescriptorPrefersOrchestratorAndReportsNotFound(t *testing.T) {
	root := privateRoot(t)
	b := startBridge(t, root, "/x")
	descriptors, err := control.ListDescriptors(root)
	if err != nil {
		t.Fatal(err)
	}
	d, err := SelectStopDescriptor(descriptors, b.instanceID)
	if err != nil || d.LocalRole != protocol.RoleOrchestrator {
		t.Fatalf("stop goes through the orchestrator's endpoint, got %+v %v", d, err)
	}
	if _, err := SelectStopDescriptor(descriptors, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStopCallsTheOrchestratorEndpointAndReportsUnknownInstances(t *testing.T) {
	root := privateRoot(t)
	b := startBridge(t, root, "/x")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Stop(ctx, root, b.instanceID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !b.stopped.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !b.stopped.Load() {
		t.Fatal("the endpoint's Stop callback should have run")
	}
	if err := Stop(ctx, root, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestObserverDescriptorPrefersOrchestratorThenExecutor(t *testing.T) {
	root := privateRoot(t)
	b := startBridge(t, root, "/x")
	d, err := ObserverDescriptor(root, b.instanceID)
	if err != nil || d.LocalRole != protocol.RoleOrchestrator {
		t.Fatalf("got %+v %v", d, err)
	}
	if _, err := ObserverDescriptor(root, "nope"); err == nil {
		t.Fatal("an unknown instance is an error")
	}
}
