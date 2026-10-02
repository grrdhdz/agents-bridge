package hooks

import (
	"context"
	"sync"
	"testing"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

func TestSessionUpdatesAreSerializedAndRebindingKeepsProgress(t *testing.T) {
	h := newHookBridge(t)
	ctx := context.Background()
	b := Binding{Harness: "codex", SessionID: "session", InstanceID: h.owner.InstanceID(), Role: protocol.RoleExecutor}
	if err := h.store.Bind(ctx, b); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.store.Update(ctx, "codex", "session", func(b *Binding) error { b.StopBlocks++; b.LastNotifiedEventSeq = 42; return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := h.store.Bind(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := h.store.Lookup(ctx, "codex", "session")
	if err != nil || got.StopBlocks != 8 || got.LastNotifiedEventSeq != 42 {
		t.Fatalf("lost session state: %+v %v", got, err)
	}
}
