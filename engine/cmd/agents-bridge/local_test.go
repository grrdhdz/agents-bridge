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
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

type localRun struct {
	root, cwd string
	cancel    context.CancelFunc
	done      chan struct{} // closed when runLocal returns; err holds its result
	err       error
	ready     map[string]any
}

func startLocal(t *testing.T) *localRun {
	return startLocalWithIdleTimeout(t, 0)
}

// startLocalWithIdleTimeout mirrors startLocal but lets idle-timeout tests
// pass a short duration instead of the disabled default.
func startLocalWithIdleTimeout(t *testing.T, idleTimeout time.Duration) *localRun {
	return startLocalWithOptions(t, idleTimeout, "")
}

// startLocalWithOptions is the shared entry point for every startLocal*
// helper, so ready-file tests can pass one without a third public helper.
func startLocalWithOptions(t *testing.T, idleTimeout time.Duration, readyFile string) *localRun {
	t.Helper()
	run := &localRun{root: filepath.Join(t.TempDir(), "instances"), cwd: "/repo/compartido", done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	reader, writer := io.Pipe()
	go func() {
		run.err = runLocal(ctx, writer, run.root, run.cwd, idleTimeout, readyFile, true, alwaysNotTerminal, theme.New(theme.ModeDark, false, nil))
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
	if !found || !strings.HasPrefix(header, "--- agents-bridge instance="+instanceID+" message_id="+sent["message_id"].(string)) || !strings.Contains(header, "from=orchestrator") {
		t.Fatalf("unexpected text header: %q", header)
	}
	// ctl send leaves source unset, which the server defaults to
	// agent-control (§6.1); the text header surfaces it for the skill.
	if !strings.Contains(header, "source=agent-control") {
		t.Fatalf("text header should carry source=agent-control: %q", header)
	}
	if body != "TAREA\nrevisa el README\n" {
		t.Fatalf("multiline body changed: %q", body)
	}

	code, stdout, _ = run.ctlID("", instanceID, "wait", "--role", "executor", "--timeout", "100ms")
	if code != 0 || !strings.Contains(stdout, `"status":"timeout"`) {
		t.Fatalf("expected jsonl timeout, got %d %s", code, stdout)
	}
}

// TestCtlSendNormalizesWindowsShellEncoding covers bodies piped from Windows
// PowerShell 5.1: leading UTF-8 BOMs and CRLF must not reach the other agent,
// or the first line would no longer be exactly the label.
// bom is U+FEFF, which UTF-8 encodes as EF BB BF.
var bom = string(rune(0xFEFF))

func TestCtlSendNormalizesWindowsShellEncoding(t *testing.T) {
	run := startLocal(t)
	instanceID := run.ready["instance_id"].(string)
	code, _, stderr := run.ctlID(bom+bom+"TAREA\r\nacción ñ\r\n", instanceID, "send", "--role", "orchestrator", "--body-file", "-")
	if code != 0 {
		t.Fatalf("send failed: %d %s", code, stderr)
	}
	code, stdout, stderr := run.ctlID("", instanceID, "wait", "--role", "executor", "--timeout", "3s", "--format", "text")
	if code != 0 {
		t.Fatalf("wait failed: %d %s", code, stderr)
	}
	_, body, _ := strings.Cut(stdout, "\n")
	if body != "TAREA\nacción ñ\n" {
		t.Fatalf("body was not normalized: %q", body)
	}
}

func TestNormalizeBody(t *testing.T) {
	utf16LE := []byte{0xFF, 0xFE, 'T', 0, 'A', 0, '\r', 0, '\n', 0, 0xF1, 0x00}
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"plain body is unchanged", []byte("TAREA\nhola\n"), "TAREA\nhola\n"},
		{"lone CR is kept", []byte("a\rb"), "a\rb"},
		{"one BOM", []byte(bom + "TAREA"), "TAREA"},
		{"two BOMs and CRLF", []byte(bom + bom + "TAREA\r\nx\r\n"), "TAREA\nx\n"},
		{"UTF-16LE from Out-File", utf16LE, "TA\nñ"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(normalizeBody(c.in)); got != c.want {
				t.Fatalf("normalizeBody = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCtlWaitTextTimeoutHeaderCarriesInstance(t *testing.T) {
	run := startLocal(t)
	instanceID := run.ready["instance_id"].(string)
	code, stdout, stderr := run.ctlID("", instanceID, "wait", "--role", "executor", "--timeout", "100ms", "--format", "text")
	if code != 0 {
		t.Fatalf("wait failed: %d %s", code, stderr)
	}
	want := "--- agents-bridge instance=" + instanceID + " timeout\n"
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
// whose loopback port nobody is listening on anymore (e.g. agents-bridge ctl
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

// TestReadyFileWritesReadyLineWithPrivatePermsAndFailsIfExists covers §7.1
// and §10.8: --ready-file writes the same ready record as stdout, with mode
// 0600, and refuses to overwrite an existing file.
func TestReadyFileWritesReadyLineWithPrivatePermsAndFailsIfExists(t *testing.T) {
	dir := t.TempDir()
	readyFile := filepath.Join(dir, "ready.json")

	run := startLocalWithOptions(t, 0, readyFile)
	instanceID := run.ready["instance_id"].(string)

	data, err := os.ReadFile(readyFile)
	if err != nil {
		t.Fatalf("ready-file was not written: %v", err)
	}
	if err := control.VerifyOwnerOnly(readyFile); err != nil {
		t.Fatalf("ready-file is not owner-only: %v", err)
	}
	var fileReady map[string]any
	if err := json.Unmarshal(trimTrailingNewline(data), &fileReady); err != nil {
		t.Fatalf("ready-file content is not JSON: %q: %v", data, err)
	}
	if fileReady["instance_id"] != instanceID || fileReady["type"] != "ready" {
		t.Fatalf("ready-file content mismatch: %+v", fileReady)
	}
	for _, key := range []string{"token", "capability", "control_url"} {
		if _, found := fileReady[key]; found {
			t.Fatalf("ready-file leaked %s", key)
		}
	}
	run.cancel()
}

func trimTrailingNewline(data []byte) []byte {
	return bytes.TrimRight(data, "\n")
}

// TestReadyFileFailsWhenItAlreadyExists covers the O_EXCL requirement: a
// pre-existing file must never be silently overwritten.
func TestReadyFileFailsWhenItAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	readyFile := filepath.Join(dir, "ready.json")
	if err := os.WriteFile(readyFile, []byte("ya existe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout bytes.Buffer
	err := runLocal(ctx, &stdout, filepath.Join(t.TempDir(), "instances"), "/repo", 0, readyFile, true, alwaysNotTerminal, theme.New(theme.ModeDark, false, nil))
	if err == nil {
		t.Fatal("runLocal should fail when --ready-file already exists")
	}
	content, readErr := os.ReadFile(readyFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "ya existe\n" {
		t.Fatalf("existing ready-file content was overwritten: %q", content)
	}
	if stdout.String() != "" {
		t.Fatalf("nothing should have started once --ready-file failed: stdout=%q", stdout.String())
	}
}

// TestParseLocalFlags covers the small flag parser for `local`.
func TestParseLocalFlags(t *testing.T) {
	lf, err := parseLocalFlags([]string{"--idle-timeout", "10s", "--ready-file", "/tmp/x.json", "--headless"})
	if err != nil {
		t.Fatal(err)
	}
	if lf.idleTimeout != 10*time.Second || lf.readyFile != "/tmp/x.json" || !lf.headless {
		t.Fatalf("unexpected parsed flags: %+v", lf)
	}
	lf, err = parseLocalFlags(nil)
	if err != nil || lf.idleTimeout != defaultLocalIdleTimeout || lf.readyFile != "" || lf.headless {
		t.Fatalf("unexpected defaults: %+v err=%v", lf, err)
	}
}

// alwaysNotTerminal is the isTerminal used by tests that want the plain
// headless/select behavior deterministically, regardless of the real
// process's stdout.
func alwaysNotTerminal() bool { return false }

// TestShouldShowEmbeddedTUI covers §7.1's decision point: TUI only with no
// --headless and a real terminal; headless always wins even over a
// terminal, and a nil isTerminal is treated as "not a terminal" rather than
// panicking.
func TestShouldShowEmbeddedTUI(t *testing.T) {
	cases := []struct {
		name       string
		headless   bool
		isTerminal func() bool
		want       bool
	}{
		{"tty and not headless shows the TUI", false, func() bool { return true }, true},
		{"headless wins even with a tty", true, func() bool { return true }, false},
		{"no tty means headless behavior", false, func() bool { return false }, false},
		{"nil isTerminal is treated as not-a-terminal", false, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldShowEmbeddedTUI(c.headless, c.isTerminal); got != c.want {
				t.Fatalf("shouldShowEmbeddedTUI(%v, isTerminal) = %v, want %v", c.headless, got, c.want)
			}
		})
	}
}

// TestWatchExternalStopCallsQuitWhenStopFires covers §7.1: an external stop
// (another terminal's `agents-bridge stop`, or --idle-timeout) must close the
// embedded TUI, not just the headless select loop.
func TestWatchExternalStopCallsQuitWhenStopFires(t *testing.T) {
	stop := newStopper()
	quit := make(chan struct{})
	go watchExternalStop(context.Background(), stop, func() { close(quit) })
	select {
	case <-quit:
		t.Fatal("quit should not fire before stop does")
	case <-time.After(50 * time.Millisecond):
	}
	stop.stop()
	select {
	case <-quit:
	case <-time.After(2 * time.Second):
		t.Fatal("quit was never called after stop fired")
	}
}

// TestWatchExternalStopCallsQuitWhenContextEnds covers process-level
// shutdown (SIGINT/SIGTERM) as another way the embedded TUI must close.
func TestWatchExternalStopCallsQuitWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	quit := make(chan struct{})
	go watchExternalStop(ctx, newStopper(), func() { close(quit) })
	cancel()
	select {
	case <-quit:
	case <-time.After(2 * time.Second):
		t.Fatal("quit was never called after ctx ended")
	}
}

// TestBuildEmbeddedObserverOptionsClosingItStopsTheBridge covers §7.1's core
// promise: the embedded TUI's OnStop actually reaches the orchestrator
// endpoint's Stop callback — i.e. closing the TUI closes the whole bridge,
// through the same POST /v1/stop path `agents-bridge tui` uses, not a
// shortcut that only looks right.
func TestBuildEmbeddedObserverOptionsClosingItStopsTheBridge(t *testing.T) {
	h := newLocalHarnessForTUITest(t)
	opts := buildEmbeddedObserverOptions(h.ownerEndpoint, theme.New(theme.ModeDark, false, nil))
	if opts.Capabilities != tui.CapabilitiesForLocal() || opts.LocalRole != "mac-orchestrator" || opts.Transport == nil {
		t.Fatalf("unexpected embedded observer options: %+v", opts)
	}
	if opts.OnStop == nil {
		t.Fatal("embedded observer options must set OnStop")
	}
	opts.OnStop()
	select {
	case <-h.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the embedded TUI (OnStop) should have stopped the bridge via POST /v1/stop")
	}
}

// TestRunLocalShowsEmbeddedTUIAndSkipsStdoutReady covers §7.1 end to end
// through runLocal itself: with isTerminal true and no --headless, it takes
// the TUI branch (proven by invoking the injected program runner instead of
// the headless select loop) and never prints ready to stdout. runTeaProgram
// is swapped for the duration of the test so no real terminal is needed.
func TestRunLocalShowsEmbeddedTUIAndSkipsStdoutReady(t *testing.T) {
	programStarted := make(chan *tea.Program, 1)
	previous := runTeaProgram
	runTeaProgram = func(p *tea.Program) error {
		programStarted <- p
		return nil
	}
	t.Cleanup(func() { runTeaProgram = previous })

	root := filepath.Join(t.TempDir(), "instances")
	var stdout bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := runLocal(ctx, &stdout, root, "/repo", 0, "", false, func() bool { return true }, theme.New(theme.ModeDark, false, nil))
	if err != nil {
		t.Fatalf("runLocal (TUI branch) returned an error: %v", err)
	}
	select {
	case <-programStarted:
	default:
		t.Fatal("runLocal should have started the embedded TUI program")
	}
	if strings.Contains(stdout.String(), "ready") {
		t.Fatalf("the TUI branch must not print the ready line to stdout: %q", stdout.String())
	}
}

// localHarnessForTUITest is the minimal real bridge+control harness needed
// to prove OnStop really reaches POST /v1/stop, without going through
// runLocal (which would need a terminal for its own embedded TUI).
type localHarnessForTUIT struct {
	ownerEndpoint *control.Endpoint
	stopped       chan struct{}
}

func newLocalHarnessForTUITest(t *testing.T) localHarnessForTUIT {
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
	stopped := make(chan struct{})
	var once sync.Once
	root := filepath.Join(t.TempDir(), "instances")
	ownerEndpoint, err := control.Start(owner, control.Options{
		Role:    protocol.RoleOrchestrator,
		Mode:    control.ModeLocal,
		Root:    root,
		CanStop: true,
		Stop:    func() { once.Do(func() { close(stopped) }) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ownerEndpoint.Close)
	return localHarnessForTUIT{ownerEndpoint: ownerEndpoint, stopped: stopped}
}
