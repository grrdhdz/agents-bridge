package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
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
	code := runCtl(context.Background(), args, ctlEnv{stdin: strings.NewReader(stdin), stdout: &stdout, stderr: &stderr, root: r.root})
	return code, stdout.String(), stderr.String()
}

// ctlID passes --instance-id automatically, since every ctl command now
// requires it (there is no cwd-based selection to fall back on).
func (r *localRun) ctlID(stdin, instanceID string, args ...string) (int, string, string) {
	return r.ctl(stdin, append([]string{args[0], "--instance-id", instanceID}, args[1:]...)...)
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
	instanceID := run.ready["instance_id"].(string)

	code, stdout, stderr := run.ctlID("TAREA\nrevisa el README\n", instanceID, "send", "--role", "orchestrator", "--body-file", "-")
	if code != 0 {
		t.Fatalf("send failed: %d %s", code, stderr)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(stdout), &sent); err != nil || sent["message_id"] == "" || sent["status"] != "queued" {
		t.Fatalf("send without --message-id should generate one: %s", stdout)
	}

	code, stdout, stderr = run.ctlID("", instanceID, "wait", "--role", "executor", "--timeout", "3s", "--format", "text")
	if code != 0 {
		t.Fatalf("wait failed: %d %s", code, stderr)
	}
	header, body, found := strings.Cut(stdout, "\n")
	if !found || !strings.HasPrefix(header, "--- codex-bridge instance="+instanceID+" message_id="+sent["message_id"].(string)) || !strings.Contains(header, "from=orchestrator") {
		t.Fatalf("unexpected text header: %q", header)
	}
	if body != "TAREA\nrevisa el README\n" {
		t.Fatalf("multiline body changed: %q", body)
	}

	code, stdout, _ = run.ctlID("", instanceID, "wait", "--role", "executor", "--timeout", "100ms")
	if code != 0 || !strings.Contains(stdout, `"status":"timeout"`) {
		t.Fatalf("expected jsonl timeout, got %d %s", code, stdout)
	}
}

func TestCtlWaitTextTimeoutHeaderCarriesInstance(t *testing.T) {
	run := startLocal(t)
	instanceID := run.ready["instance_id"].(string)
	code, stdout, stderr := run.ctlID("", instanceID, "wait", "--role", "executor", "--timeout", "100ms", "--format", "text")
	if code != 0 {
		t.Fatalf("wait failed: %d %s", code, stderr)
	}
	want := "--- codex-bridge instance=" + instanceID + " timeout\n"
	if stdout != want {
		t.Fatalf("timeout text output = %q, want %q", stdout, want)
	}
}

func TestCtlExitCodes(t *testing.T) {
	run := startLocal(t)
	instanceID := run.ready["instance_id"].(string)
	if code, _, stderr := run.ctl("", "wait", "--role", "executor", "--timeout", "100ms"); code != exitUsage || !strings.Contains(stderr, "--instance-id is required") {
		t.Fatalf("missing --instance-id should be a usage error: %d %s", code, stderr)
	}
	if code, _, stderr := run.ctlID("", instanceID, "wait", "--timeout", "100ms"); code != exitUsage || !strings.Contains(stderr, "INSTANCE_AMBIGUOUS") {
		t.Fatalf("missing role in local mode should be ambiguous: %d %s", code, stderr)
	}
	if code, _, stderr := run.ctl("", "wait", "--role", "executor", "--instance-id", "nope"); code != exitNotFound || !strings.Contains(stderr, "INSTANCE_NOT_FOUND") {
		t.Fatalf("unknown instance should exit 3: %d %s", code, stderr)
	}
	if code, _, _ := run.ctlID("", instanceID, "wait", "--role", "jefe"); code != exitUsage {
		t.Fatalf("invalid role should exit 2, got %d", code)
	}
	if code, _, _ := run.ctlID("x", instanceID, "send", "--role", "orchestrator", "--message-id", "fixed", "--body-file", "-"); code != 0 {
		t.Fatalf("first send failed: %d", code)
	}
	if code, _, stderr := run.ctlID("y", instanceID, "send", "--role", "orchestrator", "--message-id", "fixed", "--body-file", "-"); code != exitConflict || !strings.Contains(stderr, "ID_CONFLICT") {
		t.Fatalf("conflicting message_id should exit 6: %d %s", code, stderr)
	}
	if code, stdout, _ := run.ctl("", "list"); code != 0 || strings.Contains(stdout, "capability") || strings.Contains(stdout, "control_url") || strings.Count(stdout, `"instance_id"`) < 2 {
		t.Fatalf("list output invalid or leaks secrets: %d %s", code, stdout)
	}
}

// writeLiveDescriptor writes a descriptor file whose control_url is
// reachable but whose ExpiresAt is in the future, without going through
// control.StartWithRoot (which would also start a listener). It mirrors the
// unexported JSON schema in internal/control/descriptor.go.
func writeLiveDescriptor(t *testing.T, root, instanceID, controlURL string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	descriptor := map[string]any{
		"descriptor_version": 1,
		"instance_id":        instanceID,
		"pid":                1,
		"local_role":         "win-executor",
		"control_url":        controlURL,
		"capability":         "test-capability",
		"cwd":                "/repo",
		"started_at":         now.Format(time.RFC3339Nano),
		"heartbeat_at":       now.Format(time.RFC3339Nano),
		"expires_at":         now.Add(time.Minute).Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, instanceID+"-executor.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCtlWaitControlUnreachableWhenLoopbackPortIsClosed covers the sandbox
// case: a descriptor that still looks alive (ExpiresAt in the future), but
// whose loopback port nobody is listening on anymore (e.g. codex-bridge ctl
// invoked from inside a network-less sandbox). ctl must not report
// INSTANCE_NOT_FOUND/INSTANCE_CLOSED for this — those mean the instance
// itself is gone — but a distinct, actionable CONTROL_UNREACHABLE at exit 8.
func TestCtlWaitControlUnreachableWhenLoopbackPortIsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "instances")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	writeLiveDescriptor(t, root, "closed-port-instance", "http://"+addr)

	var stdout, stderr bytes.Buffer
	code := runCtl(context.Background(), []string{"wait", "--instance-id", "closed-port-instance", "--role", "executor", "--timeout", "500ms"}, ctlEnv{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr, root: root})
	if code != exitTransport {
		t.Fatalf("expected exit 8, got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "CONTROL_UNREACHABLE") {
		t.Fatalf("expected CONTROL_UNREACHABLE, got %s", stderr.String())
	}
}
