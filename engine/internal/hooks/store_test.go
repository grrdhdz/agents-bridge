package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

type hookBridge struct {
	store                         Store
	owner, worker                 *bridge.Client
	ownerEndpoint, workerEndpoint *control.Endpoint
	activity                      *control.Activity
}

func newHookBridge(t *testing.T) hookBridge {
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
	root := filepath.Join(t.TempDir(), "instances")
	activity := control.NewActivity()
	roles := control.NewRoles(nil, protocol.RoleOrchestrator, protocol.RoleExecutor)
	start := func(c *bridge.Client) *control.Endpoint {
		e, err := control.Start(c, control.Options{Root: root, Activity: activity, Roles: roles})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		return e
	}
	return hookBridge{store: Store{Root: filepath.Join(t.TempDir(), "bindings"), DescriptorRoot: root}, owner: owner, worker: worker, ownerEndpoint: start(owner), workerEndpoint: start(worker), activity: activity}
}
func TestStoreBindLookupListUnbindAndPrune(t *testing.T) {
	h := newHookBridge(t)
	ctx := context.Background()
	b := Binding{Harness: "claude", SessionID: "../../session", InstanceID: h.owner.InstanceID(), Role: protocol.RoleExecutor}
	if err := h.store.Bind(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := h.store.Lookup(ctx, b.Harness, b.SessionID)
	if err != nil || got.InstanceID != b.InstanceID || got.BoundAt.IsZero() {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	list, err := h.store.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	entries, err := os.ReadDir(h.store.Root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries: %v %v", entries, err)
	}
	if err := control.VerifyOwnerOnly(filepath.Join(h.store.Root, entries[0].Name())); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(h.store.Root)
		if info.Mode().Perm() != 0700 {
			t.Fatal("binding directory not private")
		}
	}
	b.Harness = "codex"
	if err := h.store.Bind(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Unbind(ctx, "claude", b.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Lookup(ctx, "claude", b.SessionID); !errors.Is(err, ErrNotBound) {
		t.Fatalf("unbind: %v", err)
	}
	h.workerEndpoint.Close()
	list, err = h.store.List(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("stale binding: %+v %v", list, err)
	}
}
func TestStoreRejectsMissingBridgeAndInvalidIdentity(t *testing.T) {
	h := newHookBridge(t)
	for _, b := range []Binding{{Harness: "claude", SessionID: "s", InstanceID: "absent", Role: protocol.RoleExecutor}, {Harness: "other", SessionID: "s", InstanceID: h.owner.InstanceID(), Role: protocol.RoleExecutor}, {Harness: "claude", InstanceID: h.owner.InstanceID(), Role: protocol.RoleExecutor}, {Harness: "claude", SessionID: "s", InstanceID: h.owner.InstanceID(), Role: "bad"}} {
		if err := h.store.Bind(context.Background(), b); err == nil {
			t.Fatalf("accepted invalid binding: %+v", b)
		}
	}
	if _, err := os.Stat(h.store.Root); !os.IsNotExist(err) {
		t.Fatal("invalid bind wrote storage")
	}
}
func TestStoreConcurrentSessionsAndNoSecrets(t *testing.T) {
	h := newHookBridge(t)
	ctx := context.Background()
	for _, s := range []string{"one", "two"} {
		if err := h.store.Bind(ctx, Binding{Harness: "codex", SessionID: s, InstanceID: h.owner.InstanceID(), Role: protocol.RoleExecutor}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := h.store.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("independent sessions: %+v %v", list, err)
	}
	b, _ := h.store.Lookup(ctx, "codex", "one")
	before := b.BoundAt
	time.Sleep(time.Millisecond)
	if err := h.store.Bind(ctx, b); err != nil {
		t.Fatal(err)
	}
	b, _ = h.store.Lookup(ctx, "codex", "one")
	if !b.BoundAt.Equal(before) {
		t.Fatal("idempotent bind reset session")
	}
}

func TestStorePrunesExpiredDescriptorWithClosedEndpoint(t *testing.T) {
	h := newHookBridge(t)
	ctx := context.Background()
	b := Binding{Harness: "codex", SessionID: "stale", InstanceID: h.owner.InstanceID(), Role: protocol.RoleExecutor}
	if err := h.store.Bind(ctx, b); err != nil {
		t.Fatal(err)
	}
	d := h.workerEndpoint.Descriptor()
	d.ExpiresAt = time.Now().Add(-time.Hour)
	d.ControlURL = "http://127.0.0.1:1"
	data, _ := json.Marshal(d)
	if err := os.WriteFile(filepath.Join(h.store.DescriptorRoot, d.InstanceID+"-"+string(d.LocalRole)+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	list, err := h.store.List(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("dead binding retained: %+v %v", list, err)
	}
}
