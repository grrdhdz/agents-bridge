package control

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/internal/bridge"
	"github.com/grrdhdz/agents-bridge/internal/protocol"
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
	endpoint, err := Start(client, Options{Role: protocol.RoleOrchestrator, CWD: t.TempDir(), Root: privateRoot(t)})
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
	request.Header.Set("X-Agents-Bridge-Request-ID", "test-request-1")
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
	if err := VerifyOwnerOnly(h.endpoint.descriptorPath); err != nil {
		t.Fatalf("descriptor is not owner-only: %v", err)
	}
	if err := verifyPrivateDir(filepath.Dir(h.endpoint.descriptorPath)); err != nil {
		t.Fatalf("descriptor directory is not private: %v", err)
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
	request.Header.Set("X-Agents-Bridge-Request-ID", "watch-request")
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
	ownerEndpoint, err := Start(owner, Options{Role: protocol.RoleOrchestrator, CWD: cwd, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer ownerEndpoint.Close()
	workerEndpoint, err := Start(worker, Options{Role: protocol.RoleExecutor, CWD: cwd, Root: root})
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

// TestDescriptorCarriesModeAndLastActivityAt covers spec §10.1: mode and
// last_activity_at are present on a freshly started endpoint, and heartbeat
// keeps last_activity_at in sync with the shared Activity.
func TestDescriptorCarriesModeAndLastActivityAt(t *testing.T) {
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	activity := NewActivity()
	endpoint, err := Start(client, Options{Role: protocol.RoleOrchestrator, Mode: ModeLocal, Root: privateRoot(t), Activity: activity})
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()

	descriptor := endpoint.Descriptor()
	if descriptor.Mode != ModeLocal {
		t.Fatalf("mode = %q, want %q", descriptor.Mode, ModeLocal)
	}
	if descriptor.LastActivityAt == nil || descriptor.LastActivityAt.IsZero() {
		t.Fatal("last_activity_at should be set at start")
	}

	stale := time.Now().Add(-time.Minute)
	activity.mu.Lock()
	activity.lastActivity = stale
	activity.mu.Unlock()

	deadline := time.Now().Add(4 * time.Second)
	for {
		data, err := os.ReadFile(endpoint.descriptorPath)
		if err != nil {
			t.Fatal(err)
		}
		var onDisk Descriptor
		if err := json.Unmarshal(data, &onDisk); err != nil {
			t.Fatal(err)
		}
		if onDisk.LastActivityAt != nil && onDisk.LastActivityAt.Truncate(time.Second).Equal(stale.UTC().Truncate(time.Second)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("descriptor last_activity_at never reflected activity: %v", onDisk.LastActivityAt)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestReadDescriptorToleratesMissingModeAndActivity covers a descriptor
// written before this fields existed: readers must not reject it.
func TestReadDescriptorToleratesMissingModeAndActivity(t *testing.T) {
	root := privateRoot(t)
	if _, err := descriptorDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "legacy.json")
	legacy := `{"descriptor_version":1,"instance_id":"legacy","pid":1,"local_role":"orchestrator","control_url":"http://127.0.0.1:1","capability":"cap","cwd":"/repo","started_at":"2026-01-01T00:00:00Z","heartbeat_at":"2026-01-01T00:00:00Z","expires_at":"2026-01-01T00:00:15Z"}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	descriptor, err := readDescriptor(path)
	if err != nil {
		t.Fatalf("legacy descriptor without mode/last_activity_at should still parse: %v", err)
	}
	if descriptor.Mode != "" {
		t.Fatalf("mode should default to empty, got %q", descriptor.Mode)
	}
	if descriptor.LastActivityAt != nil {
		t.Fatalf("last_activity_at should default to absent, got %v", descriptor.LastActivityAt)
	}
}

// TestStopForbiddenForExecutorAllowedForOrchestrator covers §4.2 and §9: the
// executor's endpoint in local mode must refuse stop, while the
// orchestrator's endpoint accepts it and calls Stop asynchronously after the
// 202 response.
func TestStopForbiddenForExecutorAllowedForOrchestrator(t *testing.T) {
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

	stopped := make(chan struct{})
	var once sync.Once
	stopFn := func() { once.Do(func() { close(stopped) }) }

	ownerEndpoint, err := Start(owner, Options{Role: protocol.RoleOrchestrator, Mode: ModeLocal, Root: root, CanStop: true, Stop: stopFn})
	if err != nil {
		t.Fatal(err)
	}
	defer ownerEndpoint.Close()
	workerEndpoint, err := Start(worker, Options{Role: protocol.RoleExecutor, Mode: ModeLocal, Root: root, CanStop: false})
	if err != nil {
		t.Fatal(err)
	}
	defer workerEndpoint.Close()

	response, data := controlHTTP(t, workerEndpoint, http.MethodPost, "/v1/stop", "", workerEndpoint.capability)
	if response.StatusCode != http.StatusForbidden || !strings.Contains(string(data), "FORBIDDEN") {
		t.Fatalf("executor stop should be FORBIDDEN, got %d %s", response.StatusCode, data)
	}
	select {
	case <-stopped:
		t.Fatal("Stop should not have been called by the forbidden executor request")
	default:
	}

	response, data = controlHTTP(t, ownerEndpoint, http.MethodPost, "/v1/stop", "", ownerEndpoint.capability)
	if response.StatusCode != http.StatusAccepted || !strings.Contains(string(data), `"status":"stopping"`) {
		t.Fatalf("orchestrator stop should be accepted, got %d %s", response.StatusCode, data)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop callback was never invoked")
	}
}

// TestPeerConnectedUsesCallbackNotOwnClient covers §4.1: PeerConnected must
// drive /v1/health, and default to the client's own Connected() when absent.
func TestPeerConnectedUsesCallbackNotOwnClient(t *testing.T) {
	h := newControlHarness(t)
	// The harness's default endpoint has no PeerConnected callback, so it
	// falls back to the client's own connection, which is up.
	response, data := controlHTTP(t, h.endpoint, http.MethodGet, "/v1/health", "", h.endpoint.capability)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(data), `"peer_connected":true`) {
		t.Fatalf("default PeerConnected should fall back to client.Connected(): %d %s", response.StatusCode, data)
	}

	h.endpoint.peerConnected = func() bool { return false }
	response, data = controlHTTP(t, h.endpoint, http.MethodGet, "/v1/health", "", h.endpoint.capability)
	if response.StatusCode != http.StatusOK || strings.Contains(string(data), `"peer_connected":true`) {
		t.Fatalf("PeerConnected callback should override the default: %d %s", response.StatusCode, data)
	}
}

// TestDescriptorJSONOmitsLastActivityAtWhenAbsent covers fix A: a *time.Time
// LastActivityAt must not serialize as the zero instant ("0001-01-01...")
// when no Activity has ever been recorded for that role — readers like ps
// need to tell "never recorded" apart from "recorded at the zero instant".
func TestDescriptorJSONOmitsLastActivityAtWhenAbsent(t *testing.T) {
	d := Descriptor{DescriptorVersion: DescriptorVersion, InstanceID: "x", LocalRole: protocol.RoleOrchestrator, ControlURL: "http://127.0.0.1:1", Capability: "cap"}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "last_activity_at") {
		t.Fatalf("nil LastActivityAt should be omitted from JSON, got %s", data)
	}
	round, err := readDescriptorFromBytesForTest(data)
	if err != nil {
		t.Fatal(err)
	}
	if round.LastActivityAt != nil {
		t.Fatalf("round trip should keep LastActivityAt nil, got %v", round.LastActivityAt)
	}
}

// readDescriptorFromBytesForTest mirrors readDescriptor's unmarshal step
// without requiring a file on disk.
func readDescriptorFromBytesForTest(data []byte) (Descriptor, error) {
	var d Descriptor
	err := json.Unmarshal(data, &d)
	return d, err
}

// TestSendSourceDefaultsToAgentControlAndValidatesValue covers §6.1 and
// §10.7: /v1/send defaults source to agent-control, accepts human-operator,
// and rejects anything else with MESSAGE_INVALID.
func TestSendSourceDefaultsToAgentControlAndValidatesValue(t *testing.T) {
	h := newControlHarness(t)

	response, data := controlHTTP(t, h.endpoint, http.MethodPost, "/v1/send", `{"v":1,"message_id":"m-default","body":"sin source"}`, h.endpoint.capability)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("default source send failed: %d %s", response.StatusCode, data)
	}
	events, _, _, err := h.client.ReadEvents(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEnvelopeSource(events, "m-default", "agent-control") {
		t.Fatalf("default source should be agent-control: %+v", events)
	}

	response, data = controlHTTP(t, h.endpoint, http.MethodPost, "/v1/send", `{"v":1,"message_id":"m-human","body":"del humano","source":"human-operator"}`, h.endpoint.capability)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("human-operator send failed: %d %s", response.StatusCode, data)
	}
	events, _, _, err = h.client.ReadEvents(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEnvelopeSource(events, "m-human", "human-operator") {
		t.Fatalf("explicit source should be preserved: %+v", events)
	}

	response, data = controlHTTP(t, h.endpoint, http.MethodPost, "/v1/send", `{"v":1,"message_id":"m-bad","body":"x","source":"made-up"}`, h.endpoint.capability)
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "MESSAGE_INVALID") {
		t.Fatalf("unsupported source should be MESSAGE_INVALID: %d %s", response.StatusCode, data)
	}
}

func hasEnvelopeSource(events []bridge.Event, messageID, source string) bool {
	for _, event := range events {
		if event.MessageID == messageID && event.Envelope != nil && event.Envelope.Source == source {
			return true
		}
	}
	return false
}

// TestWatchConcurrencyLimitReturnsBackpressure covers §8/§10.3: the 9th
// concurrent watcher on one endpoint gets CONTROL_BACKPRESSURE (429), and
// closing one frees a slot for the next.
func TestWatchConcurrencyLimitReturnsBackpressure(t *testing.T) {
	h := newControlHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type openWatch struct {
		cancel context.CancelFunc
		body   io.ReadCloser
	}
	var open []openWatch
	for i := 0; i < maxConcurrentWatch; i++ {
		wctx, wcancel := context.WithCancel(ctx)
		request, err := http.NewRequestWithContext(wctx, http.MethodGet, h.endpoint.descriptor.ControlURL+"/v1/watch?after_event_seq=0", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+h.endpoint.capability)
		request.Header.Set("X-Agents-Bridge-Request-ID", "watch-"+strconv.Itoa(i))
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("watch %d should open, got %d", i, response.StatusCode)
		}
		open = append(open, openWatch{cancel: wcancel, body: response.Body})
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.endpoint.watchCount.Load() < int32(maxConcurrentWatch) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	response, data := controlHTTP(t, h.endpoint, http.MethodGet, "/v1/watch?after_event_seq=0", "", h.endpoint.capability)
	if response.StatusCode != http.StatusTooManyRequests || !strings.Contains(string(data), "CONTROL_BACKPRESSURE") {
		t.Fatalf("9th watch should be CONTROL_BACKPRESSURE, got %d %s", response.StatusCode, data)
	}

	// Freeing one slot lets a new watch through.
	open[0].cancel()
	_ = open[0].body.Close()
	deadline = time.Now().Add(2 * time.Second)
	for h.endpoint.watchCount.Load() >= int32(maxConcurrentWatch) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	request, err := http.NewRequest(http.MethodGet, h.endpoint.descriptor.ControlURL+"/v1/watch?after_event_seq=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+h.endpoint.capability)
	request.Header.Set("X-Agents-Bridge-Request-ID", "watch-after-free")
	freedResponse, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer freedResponse.Body.Close()
	if freedResponse.StatusCode != http.StatusOK {
		t.Fatalf("watch after freeing a slot should open, got %d", freedResponse.StatusCode)
	}

	for _, o := range open[1:] {
		o.cancel()
		_ = o.body.Close()
	}
}
