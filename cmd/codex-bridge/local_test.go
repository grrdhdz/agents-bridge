package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grrdhdz/codex-agents-bridge/internal/control"
)

type localRun struct {
	root, cwd string
	cancel    context.CancelFunc
	done      chan struct{} // closed when runLocal returns; err holds its result
	err       error
	ready     map[string]any
}

func startLocal(t *testing.T) *localRun {
	t.Helper()
	run := &localRun{root: filepath.Join(t.TempDir(), "instances"), cwd: "/repo/compartido", done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	reader, writer := io.Pipe()
	go func() {
		run.err = runLocal(ctx, writer, run.root, run.cwd)
		_ = writer.Close()
		close(run.done)
	}()
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		t.Fatalf("local did not print ready line: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, reader) }()
	if err := json.Unmarshal([]byte(line), &run.ready); err != nil {
		t.Fatalf("ready line is not JSON: %q", line)
	}
	t.Cleanup(func() {
		cancel()
		<-run.done
	})
	return run
}

func (r *localRun) ctl(stdin string, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := runCtl(context.Background(), args, ctlEnv{stdin: strings.NewReader(stdin), stdout: &stdout, stderr: &stderr, root: r.root, cwd: r.cwd})
	return code, stdout.String(), stderr.String()
}

func TestLocalPublishesBothRolesWithoutSecretsAndCleansUp(t *testing.T) {
	run := startLocal(t)
	if run.ready["type"] != "ready" || run.ready["mode"] != "local" || run.ready["instance_id"] == "" {
		t.Fatalf("unexpected ready line: %+v", run.ready)
	}
	for _, key := range []string{"token", "capability", "control_url"} {
		if _, found := run.ready[key]; found {
			t.Fatalf("ready line leaked %s", key)
		}
	}
	descriptors, err := control.ListDescriptors(run.root)
	if err != nil || len(descriptors) != 2 {
		t.Fatalf("expected two descriptors, got %d %v", len(descriptors), err)
	}
	run.cancel()
	select {
	case <-run.done:
		if run.err != nil {
			t.Fatalf("local returned error: %v", run.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local did not stop after cancel")
	}
	descriptors, _ = control.ListDescriptors(run.root)
	if len(descriptors) != 0 {
		t.Fatalf("descriptors survived shutdown: %d", len(descriptors))
	}
}

func TestCtlSendAndWaitBetweenLocalRoles(t *testing.T) {
	run := startLocal(t)

	code, stdout, stderr := run.ctl("TAREA\nrevisa el README\n", "send", "--role", "orchestrator", "--body-file", "-")
	if code != 0 {
		t.Fatalf("send failed: %d %s", code, stderr)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(stdout), &sent); err != nil || sent["message_id"] == "" || sent["status"] != "queued" {
		t.Fatalf("send without --message-id should generate one: %s", stdout)
	}

	code, stdout, stderr = run.ctl("", "wait", "--role", "executor", "--timeout", "3s", "--format", "text")
	if code != 0 {
		t.Fatalf("wait failed: %d %s", code, stderr)
	}
	header, body, found := strings.Cut(stdout, "\n")
	if !found || !strings.HasPrefix(header, "--- codex-bridge message_id="+sent["message_id"].(string)) || !strings.Contains(header, "from=orchestrator") {
		t.Fatalf("unexpected text header: %q", header)
	}
	if body != "TAREA\nrevisa el README\n" {
		t.Fatalf("multiline body changed: %q", body)
	}

	code, stdout, _ = run.ctl("", "wait", "--role", "executor", "--timeout", "100ms")
	if code != 0 || !strings.Contains(stdout, `"status":"timeout"`) {
		t.Fatalf("expected jsonl timeout, got %d %s", code, stdout)
	}
}

func TestCtlExitCodes(t *testing.T) {
	run := startLocal(t)
	if code, _, stderr := run.ctl("", "wait", "--timeout", "100ms"); code != exitUsage || !strings.Contains(stderr, "INSTANCE_AMBIGUOUS") {
		t.Fatalf("missing role in local mode should be ambiguous: %d %s", code, stderr)
	}
	if code, _, stderr := run.ctl("", "wait", "--role", "executor", "--instance-id", "nope"); code != exitNotFound || !strings.Contains(stderr, "INSTANCE_NOT_FOUND") {
		t.Fatalf("unknown instance should exit 3: %d %s", code, stderr)
	}
	if code, _, _ := run.ctl("", "wait", "--role", "jefe"); code != exitUsage {
		t.Fatalf("invalid role should exit 2, got %d", code)
	}
	if code, _, _ := run.ctl("x", "send", "--role", "orchestrator", "--message-id", "fixed", "--body-file", "-"); code != 0 {
		t.Fatalf("first send failed: %d", code)
	}
	if code, _, stderr := run.ctl("y", "send", "--role", "orchestrator", "--message-id", "fixed", "--body-file", "-"); code != exitConflict || !strings.Contains(stderr, "ID_CONFLICT") {
		t.Fatalf("conflicting message_id should exit 6: %d %s", code, stderr)
	}
	if code, stdout, _ := run.ctl("", "list"); code != 0 || strings.Contains(stdout, "capability") || strings.Contains(stdout, "control_url") || strings.Count(stdout, `"instance_id"`) < 2 {
		t.Fatalf("list output invalid or leaks secrets: %d %s", code, stdout)
	}
}
