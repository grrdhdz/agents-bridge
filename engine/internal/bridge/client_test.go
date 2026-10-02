package bridge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

func TestClientEmitNeverBlocksWithoutLegacyReader(t *testing.T) {
	c := &Client{instanceID: "instance", hub: NewEventHub(DefaultEventHubOptions()), closedCh: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 3000; i++ {
			c.emit(protocol.Frame{Type: protocol.FrameAccepted, InstanceID: "instance", MessageID: "m", ServerSeq: uint64(i + 1)})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emit blocked without a legacy Events reader")
	}
}

func TestClientCloseEndsSubscriptions(t *testing.T) {
	s, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _, err := Dial(context.Background(), s.Addr().String(), s.InstanceID(), protocol.RoleOrchestrator, s.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	sub, err := owner.Subscribe(owner.EventHub().LatestEventSeq())
	if err != nil {
		t.Fatal(err)
	}
	owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := sub.Next(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed after client close, got %v", err)
	}
}

func TestPublishWithIDIsAtomicUnderConcurrency(t *testing.T) {
	s, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _, err := Dial(context.Background(), s.Addr().String(), s.InstanceID(), protocol.RoleOrchestrator, s.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()

	const workers = 32
	results := make([]protocol.Envelope, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = owner.PublishWithID("same-id", "same body")
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("publish %d failed: %v", i, errs[i])
		}
		if results[i].ClientSeq != results[0].ClientSeq {
			t.Fatalf("same message_id produced distinct envelopes: seq %d vs %d", results[i].ClientSeq, results[0].ClientSeq)
		}
	}
	if messages, _ := owner.QueueStats(); messages > 1 {
		t.Fatalf("expected at most one pending envelope, got %d", messages)
	}
}

// TestClientResendsAckAfterReplayWhenAckNeverReachedServer reproduces the
// scenario from internal/control/http.go: nextPeerMessage delivers the
// "received" event and advances e.consumed *before* calling client.Ack, and
// that call's error is deliberately ignored (`_ = e.client.Ack(...)`). If the
// connection is already dead at that point, the ACK never reaches the server,
// yet the local EventHub has already marked this message consumed. On
// reconnect the server replays the still-unacknowledged message, but the
// EventHub dedupes the replay (same message_id/server_seq) so no new
// "received" event fires — nothing would call Ack again without the Client's
// own automatic resend, and the orchestrator would never see "delivered".
func TestClientResendsAckAfterReplayWhenAckNeverReachedServer(t *testing.T) {
	s, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	owner, _ := openWire(t, s, protocol.RoleOrchestrator, s.OwnerToken(), 0)
	defer owner.conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker, _, err := Dial(ctx, s.Addr().String(), s.InstanceID(), protocol.RoleExecutor, s.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	const messageID = "task-1"
	e, err := protocol.NewEnvelope(s.InstanceID(), messageID, 1, "mac-orchestrator", protocol.RoleOrchestrator, "reporte", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.encoder.Encode(protocol.Frame{Type: protocol.FramePublish, InstanceID: s.InstanceID(), Envelope: &e}); err != nil {
		t.Fatal(err)
	}
	accepted := readFrame(t, owner)
	if accepted.Type != protocol.FrameAccepted {
		t.Fatalf("unexpected accepted frame: %+v", accepted)
	}

	sub, err := worker.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	var serverSeq uint64
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			t.Fatalf("waiting for message event: %v", err)
		}
		if ev.Kind == EventMessage && ev.MessageID == messageID {
			serverSeq = ev.ServerSeq
			break
		}
	}
	sub.Close()

	// Sever the worker's connection before it ever gets a chance to ACK, just
	// like a network blip between receiving the message and the Ack call.
	worker.mu.Lock()
	conn := worker.conn
	worker.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	waitUntil(t, 2*time.Second, func() bool { return !worker.Connected() })

	// Mirrors internal/control/http.go: the caller ignores Ack's error. The
	// write fails because the connection is down, so the server never
	// receives this ACK — but the Client must still remember it was asked to
	// send one, so a later reconnect can finish the job on its own.
	_ = worker.Ack(messageID, serverSeq)

	// Reconnect: the server replays the still-unacknowledged message. Nothing
	// here calls Ack a second time — only the Client's own resend logic can
	// make the orchestrator see "delivered".
	if err := worker.Reconnect(ctx); err != nil {
		t.Fatalf("reconnect failed: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		_ = owner.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		var frame protocol.Frame
		err := owner.decoder.Decode(&frame)
		if err == nil {
			if frame.Type == protocol.FrameDelivered && frame.MessageID == messageID {
				return
			}
			continue
		}
		if time.Now().After(deadline) {
			t.Fatalf("orchestrator never saw delivered for %s after reconnect: last err %v", messageID, err)
		}
	}
}

func TestClientServerClosedSignalsOnlyOnServerClose(t *testing.T) {
	server, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	worker, _, err := Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleExecutor, server.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	select {
	case <-worker.ServerClosed():
		t.Fatal("ServerClosed fired while the server is alive")
	default:
	}
	server.Close()
	select {
	case <-worker.ServerClosed():
	case <-time.After(3 * time.Second):
		t.Fatal("ServerClosed never fired after the server closed the instance")
	}
	select {
	case <-worker.Done():
		t.Fatal("Done keeps its meaning: only an explicit Close ends it")
	default:
	}
	if err := worker.Reconnect(context.Background()); err == nil {
		t.Fatal("Reconnect must refuse after the server closed the instance")
	}
}

func TestIsDefinitiveRejection(t *testing.T) {
	for code, want := range map[string]bool{"PAIRING_INVALID": true, "INSTANCE_MISMATCH": true, "ROLE_ALREADY_BOUND": false, "WORKER_CONNECTED": false, "HELLO_REJECTED": false, "INSTANCE_CLOSED": false} {
		err := fmt.Errorf("wrapped: %w", &RejectedError{Code: code, Detail: "x"})
		if got := IsDefinitiveRejection(err); got != want {
			t.Errorf("%s: got %v want %v", code, got, want)
		}
	}
	if IsDefinitiveRejection(errors.New("dial tcp: refused")) || IsDefinitiveRejection(nil) {
		t.Error("network errors are not definitive rejections")
	}
}
