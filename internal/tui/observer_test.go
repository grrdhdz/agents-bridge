package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

// fakeTransport is a network-free Transport double for the Model-level
// behavior that does not need a real endpoint: /stop confirmation, the
// observer header, and origin rendering.
type fakeTransport struct {
	instanceID string
	published  []string
	closed     bool
}

func (f *fakeTransport) InstanceID() string              { return f.instanceID }
func (f *fakeTransport) Connected() bool                 { return true }
func (f *fakeTransport) Reconnect(context.Context) error { return nil }
func (f *fakeTransport) Ack(string, uint64) error        { return nil }
func (f *fakeTransport) QueueStats() (int, int)          { return 0, 0 }
func (f *fakeTransport) Close()                          { f.closed = true }
func (f *fakeTransport) Subscribe(uint64) (EventSubscription, error) {
	return nil, errors.New("not used in this test")
}

func (f *fakeTransport) Publish(body string) (protocol.Envelope, error) {
	f.published = append(f.published, body)
	return protocol.NewEnvelopeWithSource("instance-a", "id-"+strconv.Itoa(len(f.published)), uint64(len(f.published)), protocol.ExpectedSenderID(protocol.RoleOrchestrator), protocol.RoleOrchestrator, body, protocol.SourceHumanOperator, time.Now())
}

// TestObserverStopRequiresSecondConfirmationAndNeverClosesItself covers §6:
// the observer's /stop needs a second /stop to actually call OnStop, and
// even then it must not mark the model closed or close its own transport —
// it is watching someone else's bridge, not its own.
func TestObserverStopRequiresSecondConfirmationAndNeverClosesItself(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	stopCalls := 0
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Observer: true, OnStop: func() { stopCalls++ }})

	model.command("/stop")
	if stopCalls != 0 {
		t.Fatalf("first /stop should only ask for confirmation, got %d calls", stopCalls)
	}
	if !strings.Contains(model.error, "confirma") {
		t.Fatalf("expected a confirmation prompt, got %q", model.error)
	}
	if model.state == "closed" {
		t.Fatal("first /stop must not close the observer")
	}

	model.command("/stop")
	if stopCalls != 1 {
		t.Fatalf("second /stop should call OnStop exactly once, got %d calls", stopCalls)
	}
	if model.state == "closed" || transport.closed {
		t.Fatal("observer /stop must not mark itself closed or close its own transport")
	}
}

// TestObserverStopConfirmationResetsOnOtherCommand covers the safety net: a
// stray /stop long before an unrelated command must not arm a later /stop.
func TestObserverStopConfirmationResetsOnOtherCommand(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	stopCalls := 0
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Observer: true, OnStop: func() { stopCalls++ }})
	model.command("/stop")
	model.command("/status")
	model.command("/stop")
	if stopCalls != 0 {
		t.Fatalf("an intervening command should cancel the pending confirmation, got %d calls", stopCalls)
	}
}

// TestDirectTUIStopStillClosesImmediately is a regression check: the
// original Mac TUI's /stop (Observer: false) must still close on the first
// call, unchanged by the observer's confirmation flow.
func TestDirectTUIStopStillClosesImmediately(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	stopCalls := 0
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, OnStop: func() { stopCalls++ }})
	model.command("/stop")
	if stopCalls != 1 {
		t.Fatalf("direct TUI /stop should close on the first call, got %d calls", stopCalls)
	}
	if model.state != "closed" || !transport.closed {
		t.Fatal("direct TUI /stop should close the model and its transport")
	}
}

// TestObserverViewHeaderIdentifiesItselfAndShowsOriginInMessages covers §6:
// the observer's header is visibly different, and each rendered message
// carries a time and an origin label (humano/agente).
func TestObserverViewHeaderIdentifiesItselfAndShowsOriginInMessages(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc123"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Observer: true})
	human, err := protocol.NewEnvelopeWithSource("instance-a", "m1", 1, protocol.ExpectedSenderID(protocol.RoleOrchestrator), protocol.RoleOrchestrator, "hola", protocol.SourceHumanOperator, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	agent, err := protocol.NewEnvelopeWithSource("instance-a", "m2", 2, protocol.ExpectedSenderID(protocol.RoleExecutor), protocol.RoleExecutor, "resultado", protocol.SourceAgentControl, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model.messages = []protocol.Envelope{human, agent}
	model.statuses = map[string]string{"m1": "delivered", "m2": "received"}
	model.byID = map[string]int{"m1": 0, "m2": 1}
	model.resize()

	view := model.View().Content
	if !strings.Contains(view, "OBSERVADOR") || !strings.Contains(view, "abc123") {
		t.Fatalf("observer header should identify itself and the instance: %q", view)
	}
	if !strings.Contains(view, "humano") || !strings.Contains(view, "agente") {
		t.Fatalf("observer view should label human vs agent origin: %q", view)
	}
	if strings.Contains(view, "F5") || strings.Contains(view, "/pair") {
		t.Fatalf("observer footer should not advertise pair/F5, which it does not support: %q", view)
	}
}

// --- end-to-end coverage against a real control endpoint ---

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
// core promise: the observer sees a peer message (replay and live), never
// ACKs it, and the agent's own real `wait` on that same endpoint still
// receives and confirms it exactly as if nobody were watching.
func TestObserverReplaysHistoryAndLivePeerMessagesWithoutConsuming(t *testing.T) {
	h := newObserverHarness(t)
	if _, err := h.worker.PublishWithIDSource("before-observer", "mensaje anterior", protocol.SourceAgentControl); err != nil {
		t.Fatal(err)
	}

	observerTransport := NewControlTransport(h.ownerEndpoint.Descriptor())
	model := New(Options{Transport: observerTransport, LocalRole: protocol.RoleOrchestrator, Observer: true})
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
// in a live demo of the observing TUI (§8 Parte A.3): a message the peer had
// already ACKed stayed shown as "accepted" instead of "delivered". The cause
// is that a control-plane watch can legitimately replay an event this Model
// already processed — e.g. a reconnect after CURSOR_EXPIRED re-requests the
// whole retained window — and handleFrame applied every delivery frame
// unconditionally, so a replayed "accepted" landing after "delivered" moved
// the status backwards, with no later "delivered" event guaranteed to arrive
// again and fix it. setStatusForward (model.go) now only advances a
// message's status along queued-ram < accepted < delivered.
func TestReplayedAcceptedEventNeverRegressesADeliveredStatus(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Observer: true})

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
