package control

import (
	"bufio"
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
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

type controlHarness struct {
	server   *bridge.Server
	client   *bridge.Client
	endpoint *Endpoint
}

func newControlHarness(t *testing.T) controlHarness {
	t.Helper()
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	client, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	endpoint, err := StartWithRoot(client, protocol.RoleOrchestrator, t.TempDir(), privateRoot(t))
	if err != nil {
		client.Close()
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		endpoint.Close()
		client.Close()
		server.Close()
	})
	return controlHarness{server: server, client: client, endpoint: endpoint}
}

// privateRoot returns a not-yet-created descriptor root; descriptorDir creates
// it with 0700 exactly as it does for the platform runtime directory.
func privateRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "instances")
}

func controlHTTP(t *testing.T, endpoint *Endpoint, method, path, body string, capability string) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, endpoint.descriptor.ControlURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+capability)
	request.Header.Set("X-Codex-Bridge-Request-ID", "test-request-1")
	if body != "" {
		request.Header.Set("Content-Type", controlContentType)
	}
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func TestDescriptorIsAtomicProtectedAndEphemeral(t *testing.T) {
	h := newControlHarness(t)
	data, err := os.ReadFile(h.endpoint.descriptorPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "body") || strings.Contains(text, "history") || strings.Contains(text, "envelope") {
		t.Fatalf("descriptor contains chat data: %s", text)
	}
	info, err := os.Stat(h.endpoint.descriptorPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("descriptor mode = %o, want 600", info.Mode().Perm())
	}
	// Close is idempotent and removes the descriptor; the cleanup above may call
	// it again without resurrecting any state.
	h.endpoint.Close()
	if _, err := os.Stat(h.endpoint.descriptorPath); !os.IsNotExist(err) {
		t.Fatalf("descriptor should be removed on close, stat error=%v", err)
	}
}

func TestControlHTTPAuthRequestIDContentTypeReadAndSend(t *testing.T) {
	h := newControlHarness(t)
	response, data := controlHTTP(t, h.endpoint, http.MethodGet, "/v1/health", "", h.endpoint.capability)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != controlContentType {
		t.Fatalf("health response status/content type = %d/%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	var health responseEnvelope
	if err := json.Unmarshal(data, &health); err != nil {
		t.Fatal(err)
	}
	if !health.OK || health.RequestID != "test-request-1" || health.InstanceID != h.server.InstanceID() {
		t.Fatalf("unexpected health response: %+v", health)
	}

	response, data = controlHTTP(t, h.endpoint, http.MethodGet, "/v1/health", "", "wrong")
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(string(data), "UNAUTHORIZED") {
		t.Fatalf("invalid capability should be 401: %d %s", response.StatusCode, data)
	}

	body := `{"v":1,"message_id":"control-message","body":"linea 1\nlinea 2"}`
	response, data = controlHTTP(t, h.endpoint, http.MethodPost, "/v1/send", body, h.endpoint.capability)
	if response.StatusCode != http.StatusAccepted || !strings.Contains(string(data), `"status":"queued"`) {
		t.Fatalf("send should be queued: %d %s", response.StatusCode, data)
	}
	response, data = controlHTTP(t, h.endpoint, http.MethodGet, "/v1/read?after_event_seq=0&limit=100", "", h.endpoint.capability)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(data), "linea 1\\nlinea 2") {
		t.Fatalf("read should preserve body and return JSONL: %d %s", response.StatusCode, data)
	}

	response, data = controlHTTP(t, h.endpoint, http.MethodPost, "/v1/send", `{"v":1,"message_id":"control-message","body":"different"}`, h.endpoint.capability)
	if response.StatusCode != http.StatusConflict || !strings.Contains(string(data), "ID_CONFLICT") {
		t.Fatalf("message id conflict should be 409: %d %s", response.StatusCode, data)
	}
}

func TestControlWatchFlushesReplayAndCancels(t *testing.T) {
	h := newControlHarness(t)
	if _, err := h.client.PublishWithID("watch-message", "watch body"); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, h.endpoint.descriptor.ControlURL+"/v1/watch?after_event_seq=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+h.endpoint.capability)
	request.Header.Set("X-Codex-Bridge-Request-ID", "watch-request")
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != controlContentType {
		t.Fatalf("watch status/content type = %d/%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, `"request_id":"watch-request"`) || !strings.Contains(line, `"event_seq"`) {
		t.Fatalf("watch replay missing request id/event sequence: %s", line)
	}
}

func TestDescriptorSelectionAndStaleCleanup(t *testing.T) {
	root := privateRoot(t)
	if _, err := descriptorDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "stale.json")
	stale := Descriptor{DescriptorVersion: DescriptorVersion, InstanceID: "stale", PID: 1, LocalRole: protocol.RoleOrchestrator, ControlURL: "http://127.0.0.1:1", Capability: "cap", CWD: "/stale", StartedAt: time.Now().Add(-time.Minute), HeartbeatAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(-time.Second)}
	if err := writeDescriptor(path, stale); err != nil {
		t.Fatal(err)
	}
	descriptors, err := ListDescriptors(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 0 {
		t.Fatalf("stale descriptor should be removed, got %+v", descriptors)
	}

	active := Descriptor{DescriptorVersion: DescriptorVersion, InstanceID: "active", PID: 1, LocalRole: protocol.RoleOrchestrator, ControlURL: "http://127.0.0.1:1", Capability: "cap", CWD: "/project", StartedAt: time.Now(), HeartbeatAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	if err := writeDescriptor(filepath.Join(root, "active.json"), active); err != nil {
		t.Fatal(err)
	}
	// Selection never falls back to cwd: instance_id alone must be enough,
	// regardless of the descriptor's own cwd.
	selected, err := SelectDescriptor(root, "active", "")
	if err != nil || selected.InstanceID != "active" {
		t.Fatalf("explicit instance selection failed: %+v %v", selected, err)
	}
}

func TestDescriptorsForBothRolesCoexistAndSelectByRole(t *testing.T) {
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
	root := privateRoot(t)
	cwd := "/same/repo"
	ownerEndpoint, err := StartWithRoot(owner, protocol.RoleOrchestrator, cwd, root)
	if err != nil {
		t.Fatal(err)
	}
	defer ownerEndpoint.Close()
	workerEndpoint, err := StartWithRoot(worker, protocol.RoleExecutor, cwd, root)
	if err != nil {
		t.Fatal(err)
	}
	defer workerEndpoint.Close()

	descriptors, err := ListDescriptors(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 2 {
		t.Fatalf("expected one descriptor per role, got %d", len(descriptors))
	}
	// Both descriptors share instance_id and cwd; without --role the
	// selection is ambiguous purely on instance_id, never on cwd.
	if _, err := SelectDescriptor(root, server.InstanceID(), ""); err == nil || err.Error() != "INSTANCE_AMBIGUOUS" {
		t.Fatalf("same instance_id without role must be ambiguous, got %v", err)
	}
	selected, err := SelectDescriptor(root, server.InstanceID(), protocol.RoleExecutor)
	if err != nil || selected.LocalRole != protocol.RoleExecutor {
		t.Fatalf("role selection failed: %+v %v", selected, err)
	}
	selected, err = SelectDescriptor(root, server.InstanceID(), protocol.RoleOrchestrator)
	if err != nil || selected.LocalRole != protocol.RoleOrchestrator {
		t.Fatalf("instance+role selection failed: %+v %v", selected, err)
	}
	workerEndpoint.Close()
	if _, err := SelectDescriptor(root, server.InstanceID(), protocol.RoleExecutor); err == nil || err.Error() != "INSTANCE_NOT_FOUND" {
		t.Fatalf("closed role endpoint should disappear, got %v", err)
	}
}

func TestDescriptorDirectoryWithLoosePermissionsIsRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ListDescriptors(root); err == nil {
		t.Fatal("descriptor directory readable by others must be rejected")
	}
}
