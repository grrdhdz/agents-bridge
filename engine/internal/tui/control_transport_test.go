package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

type observerHarness struct {
	server         *bridge.Server
	owner, worker  *bridge.Client
	ownerEndpoint  *control.Endpoint
	workerEndpoint *control.Endpoint
}

func newObserverHarness(t *testing.T) observerHarness {
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
	// t.TempDir() itself is 0755; control.descriptorDir insists on a private
	// 0700 directory, so nest one more level exactly like the control
	// package's own tests do (see privateRoot in internal/control).
	root := t.TempDir() + "/instances"
	ownerEndpoint, err := control.Start(owner, control.Options{Role: protocol.RoleOrchestrator, Mode: control.ModeLocal, Root: root, CanStop: true, Stop: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ownerEndpoint.Close)
	workerEndpoint, err := control.Start(worker, control.Options{Role: protocol.RoleExecutor, Mode: control.ModeLocal, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workerEndpoint.Close)
	return observerHarness{server: server, owner: owner, worker: worker, ownerEndpoint: ownerEndpoint, workerEndpoint: workerEndpoint}
}

// TestObserverReplaysHistoryAndLivePeerMessagesWithoutConsuming covers §6's
// core promise, preserved from the pre-redesign observer tests: the
// observer sees a peer message (replay and live), never ACKs it, and the
// agent's own real `wait` on that same endpoint still receives and confirms
// it exactly as if nobody were watching.
func TestObserverReplaysHistoryAndLivePeerMessagesWithoutConsuming(t *testing.T) {
	h := newObserverHarness(t)
	if _, err := h.worker.PublishWithIDSource("before-observer", "mensaje anterior", protocol.SourceAgentControl); err != nil {
		t.Fatal(err)
	}

	observerTransport := NewControlTransport(h.ownerEndpoint.Descriptor())
	model := New(Options{Transport: observerTransport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForObserver()})
	sub, err := model.transport.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	drainUntil := func(messageID string) protocol.Frame {
		t.Helper()
		for {
			event, err := sub.Next(ctx)
			if err != nil {
				t.Fatalf("subscription ended before %q arrived: %v", messageID, err)
			}
			model.handleFrame(event.Frame)
			if event.Frame.Type == protocol.FrameMessage && event.Frame.Envelope != nil && event.Frame.Envelope.MessageID == messageID {
				return event.Frame
			}
		}
	}

	// Replay: the pre-existing message is delivered even though the
	// observer subscribed after it was sent.
	drainUntil("before-observer")
	if model.statuses["before-observer"] != "received" {
		t.Fatalf("replayed peer message should be marked received, got %q", model.statuses["before-observer"])
	}

	// Live: a message sent after the observer is watching also arrives.
	if _, err := h.worker.PublishWithIDSource("live-peer", "mensaje en vivo", protocol.SourceAgentControl); err != nil {
		t.Fatal(err)
	}
	drainUntil("live-peer")
	if model.statuses["live-peer"] != "received" {
		t.Fatalf("live peer message should be marked received, got %q", model.statuses["live-peer"])
	}

	// No consume: a real wait on the orchestrator endpoint (the agent's own
	// path) must still receive and confirm "live-peer" as if the observer
	// had never seen it.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer waitCancel()
	response, err := control.Do(waitCtx, h.ownerEndpoint.Descriptor(), http.MethodPost, "/v1/wait?timeout_ms=2000", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var record struct {
		Status  string             `json:"status"`
		Message *protocol.Envelope `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "message" || record.Message == nil || record.Message.MessageID != "before-observer" {
		t.Fatalf("agent's own wait should still receive the first unconsumed peer message, got %+v", record)
	}
}

// TestObserverPublishTagsHumanOperatorSource covers §6.1: whatever the
// observer sends is tagged source=human-operator, and lands on the wire as
// the endpoint's own role (here, the orchestrator's).
func TestObserverPublishTagsHumanOperatorSource(t *testing.T) {
	h := newObserverHarness(t)
	observerTransport := NewControlTransport(h.ownerEndpoint.Descriptor())
	envelope, err := observerTransport.Publish("intervención humana")
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Source != protocol.SourceHumanOperator {
		t.Fatalf("observer send should be tagged human-operator, got %q", envelope.Source)
	}
	if envelope.SenderRole != protocol.RoleOrchestrator {
		t.Fatalf("observer send through the orchestrator endpoint should carry that role, got %q", envelope.SenderRole)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, _, _, err := h.owner.ReadEvents(0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.MessageID == envelope.MessageID && event.Envelope != nil {
				if event.Envelope.Source != protocol.SourceHumanOperator || event.Envelope.Body != "intervención humana" {
					t.Fatalf("stored envelope mismatch: %+v", event.Envelope)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("observer's published message never reached the orchestrator's own event stream")
}

// TestFrameFromWatchRecordMapsEveryEventKind is a focused unit test for the
// inverse mapping used by controlSubscription, since a mismatch there would
// silently corrupt the observer's whole view of the conversation.
// TestControlTransportStatusReportsPeerConnectedAndDescriptorFields covers
// §7's StatusProvider: ControlTransport.Status must reflect /v1/health's
// peer_connected, and cwd/started_at must come from the descriptor already
// held locally, with no round trip needed for them.
func TestControlTransportStatusReportsPeerConnectedAndDescriptorFields(t *testing.T) {
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
	root := t.TempDir() + "/instances"
	// PeerConnected is wired exactly like production (§4.1: "computed
	// correctly for this endpoint's mode") — here, the orchestrator's own
	// peer is the worker's connection, mirroring *bridge.Server.WorkerConnected.
	ownerEndpoint, err := control.Start(owner, control.Options{Role: protocol.RoleOrchestrator, Mode: control.ModeLocal, Root: root, PeerConnected: worker.Connected})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ownerEndpoint.Close)

	transport := NewControlTransport(ownerEndpoint.Descriptor())

	status, err := transport.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.PeerConnected {
		t.Fatal("expected peer_connected=true while the worker is dialed in")
	}
	if status.Cwd != ownerEndpoint.Descriptor().CWD {
		t.Fatalf("expected cwd from the descriptor (%q), got %q", ownerEndpoint.Descriptor().CWD, status.Cwd)
	}
	if !status.StartedAt.Equal(ownerEndpoint.Descriptor().StartedAt) {
		t.Fatalf("expected started_at from the descriptor, got %v", status.StartedAt)
	}

	worker.Close()
	// Health is polled, not pushed: give the server a moment to notice the
	// worker's connection dropped before asserting the flip.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, err = transport.Status(context.Background())
		if err == nil && !status.PeerConnected {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected peer_connected to become false after the worker disconnected, last status=%+v err=%v", status, err)
}

func TestFrameFromWatchRecordMapsEveryEventKind(t *testing.T) {
	cases := []struct {
		name string
		in   controlWatchRecord
		want protocol.FrameType
	}{
		{"message", controlWatchRecord{Event: "message", Message: &protocol.Envelope{}}, protocol.FrameMessage},
		{"accepted", controlWatchRecord{Event: "delivery", Status: "accepted"}, protocol.FrameAccepted},
		{"delivered", controlWatchRecord{Event: "delivery", Status: "delivered"}, protocol.FrameAckConfirmed},
		{"rejected", controlWatchRecord{Event: "state", Status: "rejected"}, protocol.FrameError},
		{"connected", controlWatchRecord{Event: "state", State: "connected"}, protocol.FrameWelcome},
		{"transport", controlWatchRecord{Event: "transport"}, protocol.FrameTransportError},
		{"lifecycle", controlWatchRecord{Event: "lifecycle", State: "closed"}, protocol.FrameClose},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			frame, ok := frameFromWatchRecord(c.in)
			if !ok {
				t.Fatalf("expected a mapped frame for %+v", c.in)
			}
			if frame.Type != c.want {
				t.Fatalf("got frame type %q, want %q", frame.Type, c.want)
			}
		})
	}
	if _, ok := frameFromWatchRecord(controlWatchRecord{Event: "unknown-future-kind"}); ok {
		t.Fatal("an unrecognized event kind should be ignored (ok=false), not mapped to a zero-value frame")
	}
}

// TestReplayedAcceptedEventNeverRegressesADeliveredStatus covers a bug seen
// in a live demo of the observing TUI: a message the peer had already ACKed
// stayed shown as "accepted" instead of "delivered". The cause is that a
// control-plane watch can legitimately replay an event this Model already
// processed — e.g. a reconnect after CURSOR_EXPIRED re-requests the whole
// retained window — and handleFrame applied every delivery frame
// unconditionally, so a replayed "accepted" landing after "delivered" moved
// the status backwards. setStatusForward (app.go) only advances a message's
// status along queued-ram < accepted < delivered.
func TestReplayedAcceptedEventNeverRegressesADeliveredStatus(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForObserver()})

	env := protocol.Envelope{InstanceID: "abc", MessageID: "tarea-x", SenderRole: protocol.RoleOrchestrator, ServerSeq: 7}
	model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: "tarea-x", Envelope: &env})
	model.handleFrame(protocol.Frame{Type: protocol.FrameAccepted, MessageID: "tarea-x", ServerSeq: 7})
	model.handleFrame(protocol.Frame{Type: protocol.FrameAckConfirmed, MessageID: "tarea-x", ServerSeq: 7})
	if model.statuses["tarea-x"] != "delivered" {
		t.Fatalf("expected delivered before replay, got %q", model.statuses["tarea-x"])
	}

	// A replayed "accepted" event for the same message/serverSeq arrives again.
	model.handleFrame(protocol.Frame{Type: protocol.FrameAccepted, MessageID: "tarea-x", ServerSeq: 7})
	if model.statuses["tarea-x"] != "delivered" {
		t.Fatalf("a replayed accepted event regressed the status to %q, want delivered", model.statuses["tarea-x"])
	}
}
