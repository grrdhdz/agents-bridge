package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
	"github.com/grrdhdz/codex-agents-bridge/internal/tailscale"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

// loopbackDetect is the injected tailscale detection for phase 3 tests (§8):
// it lets runOrchestrator run entirely over loopback, with a DNSName that a
// join command in the same test process can actually dial, without a real
// Tailscale install or network.
func loopbackDetect(context.Context) (tailscale.Info, error) {
	return tailscale.Info{IPv4: "127.0.0.1", DNSName: "127.0.0.1"}, nil
}

// TestParseHostFlagsRejectsHeadlessWithoutReadyFile covers §8: headless on
// the Mac/tailscale-host side is refused (USAGE) unless --ready-file is also
// given, since that file is the only place the join command's token may go.
func TestParseHostFlagsRejectsHeadlessWithoutReadyFile(t *testing.T) {
	if _, err := parseHostFlags([]string{"--headless"}); err == nil || !strings.Contains(err.Error(), "--ready-file") {
		t.Fatalf("expected an error naming --ready-file, got %v", err)
	}
	if _, err := parseHostFlags([]string{"--headless", "--ready-file", "/tmp/x.json"}); err != nil {
		t.Fatalf("headless with --ready-file should parse cleanly: %v", err)
	}
	hf, err := parseHostFlags(nil)
	if err != nil || hf.headless || hf.idleTimeout != 0 {
		t.Fatalf("unexpected host defaults: %+v err=%v", hf, err)
	}
}

// TestRunOrchestratorHeadlessRejectsMissingReadyFile is the same rule
// enforced at runOrchestrator itself, in case a caller other than main()
// skips parseHostFlags.
func TestRunOrchestratorHeadlessRejectsMissingReadyFile(t *testing.T) {
	var stdout bytes.Buffer
	err := runOrchestrator(context.Background(), &stdout, filepath.Join(t.TempDir(), "instances"), 0, true, "", loopbackDetect, theme.New(theme.ModeDark, false, nil))
	if err == nil || !strings.Contains(err.Error(), "--ready-file") {
		t.Fatalf("expected a --ready-file error, got %v", err)
	}
}

// TestRunOrchestratorHeadlessWritesJoinCommandOnlyToReadyFile covers §8: in
// headless mode the join command (which carries the one-use pairing token)
// is written only into --ready-file's join_command field, never to stdout —
// and the descriptor it publishes is tailscale-host.
func TestRunOrchestratorHeadlessWritesJoinCommandOnlyToReadyFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "instances")
	readyFile := filepath.Join(t.TempDir(), "ready.json")
	ctx, cancel := context.WithCancel(context.Background())
	var stdout bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runOrchestrator(ctx, &stdout, root, 0, true, readyFile, loopbackDetect, theme.New(theme.ModeDark, false, nil))
	}()

	var ready map[string]any
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := readFileIfExists(readyFile)
		if err == nil && len(data) > 0 {
			if jsonErr := json.Unmarshal(bytes.TrimRight(data, "\n"), &ready); jsonErr == nil {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ready == nil {
		t.Fatal("--ready-file was never written with valid JSON")
	}
	if ready["type"] != "ready" || ready["mode"] != "tailscale-host" || ready["instance_id"] == "" {
		t.Fatalf("unexpected ready-file content: %+v", ready)
	}
	joinCommand, _ := ready["join_command"].(string)
	if !strings.Contains(joinCommand, "--token") {
		t.Fatalf("ready-file's join_command should carry the pairing token: %q", joinCommand)
	}
	if stdout.Len() != 0 {
		t.Fatalf("headless host must never write to stdout, got %q", stdout.String())
	}

	descriptors, err := control.ListDescriptors(root)
	if err != nil || len(descriptors) != 1 || descriptors[0].Mode != control.ModeTailscaleHost || descriptors[0].LocalRole != protocol.RoleOrchestrator {
		t.Fatalf("expected one tailscale-host orchestrator descriptor, got %+v err=%v", descriptors, err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runOrchestrator returned an error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("headless host did not stop after ctx cancel")
	}
}

// TestJoinHeadlessPublishesDescriptorAndAllowsCtlWaitSend covers §8: a
// headless join connects to a live bridge.Server (standing in for a Mac
// host), publishes a tailscale-join descriptor, prints only the plain ready
// line (no secrets), and a local ctl agent can wait/send through it exactly
// as it would for `local`.
func TestJoinHeadlessPublishesDescriptorAndAllowsCtlWaitSend(t *testing.T) {
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	owner, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()

	worker, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleExecutor, server.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	root := filepath.Join(t.TempDir(), "instances")
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runJoinHeadless(ctx, worker, writer, root, "") }()

	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		t.Fatalf("join --headless did not print a ready line: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, reader) }()
	var ready map[string]any
	if err := json.Unmarshal([]byte(line), &ready); err != nil {
		t.Fatalf("ready line is not JSON: %q", line)
	}
	if ready["type"] != "ready" || ready["mode"] != "tailscale-join" || ready["instance_id"] != server.InstanceID() {
		t.Fatalf("unexpected ready line: %+v", ready)
	}
	for _, key := range []string{"token", "capability", "control_url"} {
		if _, found := ready[key]; found {
			t.Fatalf("ready line leaked %s", key)
		}
	}

	descriptors, err := control.ListDescriptors(root)
	if err != nil || len(descriptors) != 1 || descriptors[0].Mode != control.ModeTailscaleJoin || descriptors[0].LocalRole != protocol.RoleExecutor {
		t.Fatalf("expected one tailscale-join executor descriptor, got %+v err=%v", descriptors, err)
	}

	instanceID := server.InstanceID()
	if _, err := owner.PublishWithIDSource("tarea-remota", "hazlo en Windows", protocol.SourceAgentControl); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runCtl(context.Background(), []string{"wait", "--instance-id", instanceID, "--role", "executor", "--timeout", "3s", "--format", "text"}, ctlEnv{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr, root: root})
	if code != 0 {
		t.Fatalf("ctl wait against the headless join failed: %d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hazlo en Windows") {
		t.Fatalf("ctl wait did not deliver the orchestrator's message: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runCtl(context.Background(), []string{"send", "--instance-id", instanceID, "--role", "executor", "--message-id", "resultado-remoto", "--body-file", "-"}, ctlEnv{stdin: strings.NewReader("RESULTADO\nhecho"), stdout: &stdout, stderr: &stderr, root: root})
	if code != 0 {
		t.Fatalf("ctl send against the headless join failed: %d %s", code, stderr.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		events, _, _, err := owner.ReadEvents(0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.MessageID == "resultado-remoto" && event.Envelope != nil {
				found = true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		t.Fatal("ctl send through the headless join never reached the Mac-side owner client")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runJoinHeadless returned an error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("headless join did not stop after ctx cancel")
	}
}

// TestStopClosesHeadlessHostAndHeadlessJoinIndependently covers §8's "stop
// cierra ambos": POST /v1/stop against a headless host's own orchestrator
// endpoint closes that process, and against a headless join's own executor
// endpoint closes only the join process — each independently of the other.
func TestStopClosesHeadlessHostAndHeadlessJoinIndependently(t *testing.T) {
	root := filepath.Join(t.TempDir(), "instances")
	readyFile := filepath.Join(t.TempDir(), "ready.json")
	hostCtx, hostCancel := context.WithCancel(context.Background())
	defer hostCancel()
	var stdout bytes.Buffer
	hostDone := make(chan error, 1)
	go func() {
		hostDone <- runOrchestrator(hostCtx, &stdout, root, 0, true, readyFile, loopbackDetect, theme.New(theme.ModeDark, false, nil))
	}()

	var descriptor control.Descriptor
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		descriptors, err := control.ListDescriptors(root)
		if err == nil && len(descriptors) == 1 {
			descriptor = descriptors[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if descriptor.InstanceID == "" {
		t.Fatal("headless host never published its descriptor")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := control.Do(ctx, descriptor, http.MethodPost, "/v1/stop", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 from /v1/stop, got %d", response.StatusCode)
	}

	select {
	case err := <-hostDone:
		if err != nil {
			t.Fatalf("headless host returned an error after /v1/stop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("headless host did not stop after POST /v1/stop")
	}
}

func readFileIfExists(path string) ([]byte, error) {
	return os.ReadFile(path)
}
