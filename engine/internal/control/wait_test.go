package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

type localHarness struct {
	server         *bridge.Server
	owner, worker  *bridge.Client
	ownerEndpoint  *Endpoint
	workerEndpoint *Endpoint
}

func newLocalHarness(t *testing.T) localHarness {
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
	root := privateRoot(t)
	ownerEndpoint, err := Start(owner, Options{Role: protocol.RoleOrchestrator, CWD: "/repo", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ownerEndpoint.Close)
	workerEndpoint, err := Start(worker, Options{Role: protocol.RoleExecutor, CWD: "/repo", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workerEndpoint.Close)
	return localHarness{server: server, owner: owner, worker: worker, ownerEndpoint: ownerEndpoint, workerEndpoint: workerEndpoint}
}

type waitResult struct {
	status int
	record map[string]any
	err    error
}

func postWait(ctx context.Context, endpoint *Endpoint, timeoutMS int) waitResult {
	url := endpoint.descriptor.ControlURL + "/v1/wait?timeout_ms=" + strconv.Itoa(timeoutMS)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return waitResult{err: err}
	}
	request.Header.Set("Authorization", "Bearer "+endpoint.capability)
	request.Header.Set("X-Agents-Bridge-Request-ID", "wait-request")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return waitResult{err: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return waitResult{status: response.StatusCode, err: err}
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		return waitResult{status: response.StatusCode, err: err}
	}
	return waitResult{status: response.StatusCode, record: record}
}

func messageBody(t *testing.T, r waitResult) string {
	t.Helper()
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status != http.StatusOK || r.record["status"] != "message" {
		t.Fatalf("expected a message, got %d %+v", r.status, r.record)
	}
	message, _ := r.record["message"].(map[string]any)
	body, _ := message["body"].(string)
	return body
}

func waitForStatus(t *testing.T, client *bridge.Client, messageID, status string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events, _, _, _ := client.ReadEvents(0, 1000)
		for _, event := range events {
			if event.MessageID == messageID && event.Status == status {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("message %s never reached status %q", messageID, status)
}

func TestWaitReturnsPeerMessageAndAcknowledgesIt(t *testing.T) {
	h := newLocalHarness(t)
	sent, err := h.owner.PublishWithID("task-1", "TAREA\nlínea 2")
	if err != nil {
		t.Fatal(err)
	}
	if body := messageBody(t, postWait(context.Background(), h.workerEndpoint, 2000)); body != "TAREA\nlínea 2" {
		t.Fatalf("body changed: %q", body)
	}
	waitForStatus(t, h.owner, sent.MessageID, "delivered")

	if _, err := h.owner.PublishWithID("task-2", "segunda"); err != nil {
		t.Fatal(err)
	}
	if body := messageBody(t, postWait(context.Background(), h.workerEndpoint, 2000)); body != "segunda" {
		t.Fatalf("consumed message was redelivered or skipped: %q", body)
	}
}

func TestWaitIgnoresOwnMessagesAndTimesOut(t *testing.T) {
	h := newLocalHarness(t)
	if _, err := h.worker.PublishWithID("own", "RESULTADO propio"); err != nil {
		t.Fatal(err)
	}
	r := postWait(context.Background(), h.workerEndpoint, 200)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status != http.StatusOK || r.record["status"] != "timeout" {
		t.Fatalf("expected timeout, got %d %+v", r.status, r.record)
	}
}

func TestCanceledWaitDoesNotConsumeAndReleasesSlot(t *testing.T) {
	h := newLocalHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan waitResult, 1)
	go func() { done <- postWait(ctx, h.workerEndpoint, 10000) }()
	waitUntilWaiting(t, h.workerEndpoint)
	cancel()
	<-done
	deadline := time.Now().Add(2 * time.Second)
	for h.workerEndpoint.waiting.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := h.owner.PublishWithID("after-cancel", "llega después"); err != nil {
		t.Fatal(err)
	}
	if body := messageBody(t, postWait(context.Background(), h.workerEndpoint, 2000)); body != "llega después" {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestSecondConcurrentWaitIsRejected(t *testing.T) {
	h := newLocalHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go postWait(ctx, h.workerEndpoint, 10000)
	waitUntilWaiting(t, h.workerEndpoint)
	r := postWait(context.Background(), h.workerEndpoint, 100)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status != http.StatusConflict || r.record["code"] != "WAIT_IN_PROGRESS" {
		t.Fatalf("expected WAIT_IN_PROGRESS, got %d %+v", r.status, r.record)
	}
}

func TestWaitReportsClosedInstance(t *testing.T) {
	h := newLocalHarness(t)
	done := make(chan waitResult, 1)
	go func() { done <- postWait(context.Background(), h.workerEndpoint, 10000) }()
	waitUntilWaiting(t, h.workerEndpoint)
	h.worker.Close()
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status != http.StatusGone || r.record["code"] != "INSTANCE_CLOSED" {
		t.Fatalf("expected INSTANCE_CLOSED, got %d %+v", r.status, r.record)
	}
}

func waitUntilWaiting(t *testing.T, endpoint *Endpoint) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !endpoint.waiting.Load() {
		if time.Now().After(deadline) {
			t.Fatal("wait never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestOrchestratorWaitRegistersPresenceExecutorDoesNot covers §5.1: a wait in
// flight on the orchestrator endpoint counts as presence (Activity reports
// "now"), while a wait on the executor endpoint never does.
func TestOrchestratorWaitRegistersPresenceExecutorDoesNot(t *testing.T) {
	h := newLocalHarness(t)
	activity := NewActivity()
	h.ownerEndpoint.activity = activity
	h.workerEndpoint.activity = activity
	stale := time.Now().Add(-time.Hour)
	activity.mu.Lock()
	activity.lastActivity = stale
	activity.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go postWait(ctx, h.workerEndpoint, 10000)
	waitUntilWaiting(t, h.workerEndpoint)
	if !activity.LastActivity().Equal(stale) {
		t.Fatalf("executor wait must not register presence, LastActivity=%v want %v", activity.LastActivity(), stale)
	}
	cancel()

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go postWait(ctx2, h.ownerEndpoint, 10000)
	waitUntilWaiting(t, h.ownerEndpoint)
	if time.Since(activity.LastActivity()) > time.Second {
		t.Fatalf("orchestrator wait should register presence (now), got %v", activity.LastActivity())
	}
}
