package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/internal/control"
)

func (r *localRun) stop(instanceID string) (int, string, string) {
	var stdout, stderr strings.Builder
	code := runStop(context.Background(), []string{"--instance-id", instanceID}, ctlEnv{stdout: &stdout, stderr: &stderr, root: r.root})
	return code, stdout.String(), stderr.String()
}

// TestStopClosesLocalInstanceAndRemovesDescriptors covers §10.4: stop closes
// the local instance like Ctrl+C would and removes its descriptors.
func TestStopClosesLocalInstanceAndRemovesDescriptors(t *testing.T) {
	run := startLocal(t)
	instanceID := run.ready["instance_id"].(string)

	code, stdout, stderr := run.stop(instanceID)
	if code != 0 {
		t.Fatalf("stop failed: %d %s", code, stderr)
	}
	if !strings.Contains(stdout, `"operation":"stop"`) || !strings.Contains(stdout, instanceID) {
		t.Fatalf("unexpected stop output: %s", stdout)
	}

	select {
	case <-run.done:
		if run.err != nil {
			t.Fatalf("local returned error after stop: %v", run.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local did not stop after POST /v1/stop")
	}

	descriptors, _ := control.ListDescriptors(run.root)
	if len(descriptors) != 0 {
		t.Fatalf("descriptors survived stop: %d", len(descriptors))
	}
}

// TestStopUnknownInstanceExitsNotFound covers §10.4.
func TestStopUnknownInstanceExitsNotFound(t *testing.T) {
	run := startLocal(t)
	code, _, stderr := run.stop("does-not-exist")
	if code != exitNotFound || !strings.Contains(stderr, "INSTANCE_NOT_FOUND") {
		t.Fatalf("unknown instance should exit 3, got %d %s", code, stderr)
	}
}

// TestStopFromExecutorOnlyDescriptorIsForbidden covers §9: when only the
// executor's descriptor for a local instance remains reachable (its
// orchestrator counterpart already gone), stop must not be able to use it to
// tear down the bridge — it reports FORBIDDEN instead of silently no-op'ing
// or succeeding.
func TestStopFromExecutorOnlyDescriptorIsForbidden(t *testing.T) {
	run := startLocal(t)
	instanceID := run.ready["instance_id"].(string)

	if _, err := control.ListDescriptors(run.root); err != nil {
		t.Fatal(err)
	}
	// Remove the orchestrator's descriptor file directly so selection falls
	// back to the executor's, whose endpoint must still refuse to stop. The
	// on-disk name mirrors descriptorFileName in internal/control/descriptor.go,
	// which uses the protocol.Role string ("mac-orchestrator"), not the ctl
	// alias ("orchestrator").
	if err := os.Remove(filepath.Join(run.root, instanceID+"-mac-orchestrator.json")); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run.stop(instanceID)
	if code != exitForbidden || !strings.Contains(stderr, "FORBIDDEN") {
		t.Fatalf("stop via the executor-only descriptor should be FORBIDDEN, got %d %s", code, stderr)
	}
	run.cancel()
}
