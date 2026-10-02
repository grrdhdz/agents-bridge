package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

// fastPolicy keeps the reconnect loop's cadence in milliseconds so the tests
// never wait on the production 1s tick.
func fastPolicy(timeout time.Duration) reconnectPolicy {
	return reconnectPolicy{timeout: timeout, interval: 20 * time.Millisecond, attemptTimeout: 300 * time.Millisecond}
}

// fakeHost is a hand-rolled TCP peer that speaks just enough of the protocol
// (hello -> welcome) to let a real bridge.Client connect, so tests can drop
// the connection or refuse reconnects without ever sending FrameClose.
type fakeHost struct {
	t          *testing.T
	instanceID string
	ln         net.Listener
	addr       string
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &fakeHost{t: t, instanceID: "fake-instance", ln: ln, addr: ln.Addr().String()}
	t.Cleanup(func() { _ = h.ln.Close() })
	return h
}

// accept handles one hello and answers with reply (a welcome by default).
func (h *fakeHost) accept(reply *protocol.Frame) net.Conn {
	conn, err := h.ln.Accept()
	if err != nil {
		return nil
	}
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		_ = conn.Close()
		return nil
	}
	frame := protocol.Frame{Type: protocol.FrameWelcome, InstanceID: h.instanceID, ReconnectToken: "resume-token"}
	if reply != nil {
		frame = *reply
	}
	_ = json.NewEncoder(conn).Encode(frame)
	if reply != nil && reply.Type == protocol.FrameError {
		_ = conn.Close()
	}
	return conn
}

func (h *fakeHost) dial() (*bridge.Client, net.Conn) {
	h.t.Helper()
	connCh := make(chan net.Conn, 1)
	go func() { connCh <- h.accept(nil) }()
	client, _, err := bridge.Dial(context.Background(), h.addr, h.instanceID, protocol.RoleExecutor, "join-token")
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(client.Close)
	return client, <-connCh
}

// relisten reopens the listener on the same address after a simulated outage.
func (h *fakeHost) relisten() {
	h.t.Helper()
	ln, err := net.Listen("tcp", h.addr)
	if err != nil {
		h.t.Fatal(err)
	}
	h.ln = ln
	h.t.Cleanup(func() { _ = ln.Close() })
}

func startJoinHeadless(t *testing.T, client *bridge.Client, root string, policy reconnectPolicy) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := runJoinHeadless(ctx, client, writer, root, "", policy)
		_ = writer.Close()
		done <- err
	}()
	if _, err := bufio.NewReader(reader).ReadString('\n'); err != nil {
		t.Fatalf("join --headless did not print a ready line: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, reader) }()
	return cancel, done
}

func exitCodeOf(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return -1
}

// TestJoinHeadlessEndsCleanlyWhenHostCloses reproduces the orphaned executor:
// a real headless host and a real headless join over loopback; when the host
// stops, the join must exit 0 on its own and remove its descriptor.
func TestJoinHeadlessEndsCleanlyWhenHostCloses(t *testing.T) {
	hostRoot := filepath.Join(t.TempDir(), "instances")
	joinRoot := filepath.Join(t.TempDir(), "instances-join")
	readyFile := filepath.Join(t.TempDir(), "ready.json")
	hostCtx, hostCancel := context.WithCancel(context.Background())
	defer hostCancel()
	var hostOut bytes.Buffer
	hostDone := make(chan error, 1)
	go func() {
		hostDone <- runOrchestrator(hostCtx, &hostOut, hostRoot, 0, true, readyFile, loopbackDetect, theme.New(theme.ModeDark, false, nil))
	}()

	var descriptor control.Descriptor
	var joinCommand string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		descriptors, _ := control.ListDescriptors(hostRoot)
		data, _ := readFileIfExists(readyFile)
		var ready map[string]any
		if len(descriptors) == 1 && json.Unmarshal(bytes.TrimRight(data, "\n"), &ready) == nil {
			descriptor = descriptors[0]
			joinCommand, _ = ready["join_command"].(string)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if joinCommand == "" {
		t.Fatal("host never published its descriptor and ready file")
	}

	// The host formats the command for PowerShell: drop its line-continuation
	// backticks, then the "agents-bridge join" prefix.
	var args []string
	for _, field := range strings.Fields(joinCommand)[2:] {
		if field != "`" {
			args = append(args, field)
		}
	}
	args = append(args, "--headless")
	joinCtx, joinCancel := context.WithCancel(context.Background())
	defer joinCancel()
	reader, writer := io.Pipe()
	joinDone := make(chan error, 1)
	go func() {
		err := runJoin(joinCtx, args, writer, joinRoot)
		_ = writer.Close()
		joinDone <- err
	}()
	if _, err := bufio.NewReader(reader).ReadString('\n'); err != nil {
		t.Fatalf("join never became ready: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, reader) }()
	if descriptors, err := control.ListDescriptors(joinRoot); err != nil || len(descriptors) != 1 {
		t.Fatalf("join should have published a descriptor: %+v %v", descriptors, err)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	response, err := control.Do(stopCtx, descriptor, http.MethodPost, "/v1/stop", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	select {
	case err := <-joinDone:
		if err != nil {
			t.Fatalf("join must end cleanly when the host closes, got %v", err)
		}
	case <-time.After(5 * time.Second):
		joinCancel()
		t.Fatal("headless join kept running after the host closed the bridge")
	}
	if descriptors, err := control.ListDescriptors(joinRoot); err != nil || len(descriptors) != 0 {
		t.Fatalf("join descriptor should be gone: %+v %v", descriptors, err)
	}
	select {
	case <-hostDone:
	case <-time.After(3 * time.Second):
		t.Fatal("host did not stop")
	}
}

// TestJoinHeadlessEndsWhenServerClosesWithFrameClose covers the same rule at
// the runJoinHeadless level, with the default (production) policy.
func TestJoinHeadlessEndsWhenServerClosesWithFrameClose(t *testing.T) {
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	worker, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleExecutor, server.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	root := filepath.Join(t.TempDir(), "instances")
	cancel, done := startJoinHeadless(t, worker, root, reconnectPolicy{})
	server.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected a clean end, got %v", err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("join did not end after the server closed")
	}
	if d, _ := control.ListDescriptors(root); len(d) != 0 {
		t.Fatalf("descriptor left behind: %+v", d)
	}
}

// TestJoinHeadlessGivesUpAfterReconnectTimeout covers a host that vanishes
// without FrameClose: the join ends with the transport exit code.
func TestJoinHeadlessGivesUpAfterReconnectTimeout(t *testing.T) {
	host := newFakeHost(t)
	client, conn := host.dial()
	root := filepath.Join(t.TempDir(), "instances")
	cancel, done := startJoinHeadless(t, client, root, fastPolicy(500*time.Millisecond))

	_ = conn.Close()
	_ = host.ln.Close()
	start := time.Now()
	select {
	case err := <-done:
		if err == nil || exitCodeOf(err) != exitTransport {
			t.Fatalf("expected a transport exit error, got %v", err)
		}
		if elapsed := time.Since(start); elapsed < 450*time.Millisecond || elapsed > 4*time.Second {
			t.Fatalf("gave up after %v, expected about the reconnect timeout", elapsed)
		}
		if !strings.Contains(err.Error(), "500ms") {
			t.Fatalf("message should name the timeout: %v", err)
		}
	case <-time.After(6 * time.Second):
		cancel()
		t.Fatal("join never gave up on a vanished host")
	}
	if d, _ := control.ListDescriptors(root); len(d) != 0 {
		t.Fatalf("descriptor left behind: %+v", d)
	}
}

// TestJoinHeadlessReconnectRestartsTheClock: a brief outage that recovers
// must not count toward a later outage's budget.
func TestJoinHeadlessReconnectRestartsTheClock(t *testing.T) {
	host := newFakeHost(t)
	client, conn := host.dial()
	cancel, done := startJoinHeadless(t, client, filepath.Join(t.TempDir(), "instances"), fastPolicy(600*time.Millisecond))

	_ = conn.Close()
	_ = host.ln.Close()
	time.Sleep(350 * time.Millisecond)
	host.relisten()
	conn2 := host.accept(nil)
	// Held past 600ms since the first loss: without a reset the join would
	// already have given up.
	time.Sleep(500 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("join ended although it had reconnected: %v", err)
	default:
	}
	_ = conn2.Close()
	_ = host.ln.Close()
	second := time.Now()
	select {
	case err := <-done:
		if exitCodeOf(err) != exitTransport {
			t.Fatalf("expected transport exit, got %v", err)
		}
		if elapsed := time.Since(second); elapsed < 450*time.Millisecond {
			t.Fatalf("second outage got only %v of budget", elapsed)
		}
	case <-time.After(6 * time.Second):
		cancel()
		t.Fatal("join never gave up after the second outage")
	}
}

// TestJoinHeadlessEndsImmediatelyOnDefinitiveRejection: an auth/instance
// rejection is not a network problem, so waiting out the timeout is pointless.
func TestJoinHeadlessEndsImmediatelyOnDefinitiveRejection(t *testing.T) {
	host := newFakeHost(t)
	client, conn := host.dial()
	cancel, done := startJoinHeadless(t, client, filepath.Join(t.TempDir(), "instances"), fastPolicy(time.Minute))
	_ = conn.Close()
	go host.accept(&protocol.Frame{Type: protocol.FrameError, Code: "PAIRING_INVALID", Detail: "invalid or expired pairing token"})
	select {
	case err := <-done:
		if err == nil || exitCodeOf(err) == 0 || !strings.Contains(err.Error(), "PAIRING_INVALID") {
			t.Fatalf("expected a rejection error naming the code, got %v", err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("join kept retrying after a definitive rejection")
	}
}

// TestJoinHeadlessKeepsRetryingOnTransientRejection: ROLE_ALREADY_BOUND is
// what a server says while it has not yet noticed the old connection died.
func TestJoinHeadlessKeepsRetryingOnTransientRejection(t *testing.T) {
	host := newFakeHost(t)
	client, conn := host.dial()
	cancel, done := startJoinHeadless(t, client, filepath.Join(t.TempDir(), "instances"), fastPolicy(time.Minute))
	defer cancel()
	_ = conn.Close()
	host.accept(&protocol.Frame{Type: protocol.FrameError, Code: "ROLE_ALREADY_BOUND", Detail: "role is already connected"})
	conn2 := host.accept(nil)
	if conn2 != nil {
		defer conn2.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for !client.Connected() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !client.Connected() {
		t.Fatal("client never recovered after a transient rejection")
	}
	select {
	case err := <-done:
		t.Fatalf("join ended on a transient rejection: %v", err)
	default:
	}
}

// TestKeepConnectedStopsAfterServerClose guards `local` too: its in-process
// clients must not redial a closed instance.
func TestKeepConnectedStopsAfterServerClose(t *testing.T) {
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	worker, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleExecutor, server.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	done := make(chan struct{})
	go func() { keepConnected(context.Background(), worker); close(done) }()
	server.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("keepConnected kept running after the server closed the instance")
	}
}

func TestParseJoinFlagsReconnectTimeout(t *testing.T) {
	base := []string{"--host", "h", "--port", "1234", "--instance", "i", "--token", "t"}
	jf, err := parseJoinFlags(base)
	if err != nil || jf.reconnectTimeout != 15*time.Minute {
		t.Fatalf("default should be 15m: %+v %v", jf, err)
	}
	jf, err = parseJoinFlags(append(append([]string{}, base...), "--reconnect-timeout", "0"))
	if err != nil || jf.reconnectTimeout != 0 {
		t.Fatalf("0 must mean retry forever: %+v %v", jf, err)
	}
	jf, err = parseJoinFlags(append(append([]string{}, base...), "--reconnect-timeout", "45s"))
	if err != nil || jf.reconnectTimeout != 45*time.Second {
		t.Fatalf("45s not parsed: %+v %v", jf, err)
	}
	if _, err := parseJoinFlags(append(append([]string{}, base...), "--reconnect-timeout", "-1s")); err == nil {
		t.Fatal("negative timeout must be rejected")
	}
}

// TestJoinHeadlessEndsCleanlyOnInstanceClosedRejection: a host that closed
// while we were disconnected answers the reconnect hello with INSTANCE_CLOSED;
// that is the host closing (exit 0), not an auth failure (4) nor a transient
// error to retry.
func TestJoinHeadlessEndsCleanlyOnInstanceClosedRejection(t *testing.T) {
	host := newFakeHost(t)
	client, conn := host.dial()
	root := filepath.Join(t.TempDir(), "instances")
	cancel, done := startJoinHeadless(t, client, root, fastPolicy(time.Minute))
	_ = conn.Close()
	go host.accept(&protocol.Frame{Type: protocol.FrameError, Code: "INSTANCE_CLOSED", Detail: "bridge instance is closed"})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("INSTANCE_CLOSED must end cleanly, got %v", err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("join kept retrying after INSTANCE_CLOSED")
	}
	if d, _ := control.ListDescriptors(root); len(d) != 0 {
		t.Fatalf("descriptor left behind: %+v", d)
	}
	select {
	case <-client.ServerClosed():
	default:
		t.Fatal("client should report the server as closed")
	}
}
