package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

type testWire struct {
	conn    net.Conn
	decoder *json.Decoder
	encoder *json.Encoder
}

func openWire(t *testing.T, s *Server, role protocol.Role, token string, last uint64) (*testWire, protocol.Frame) {
	t.Helper()
	conn, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	w := &testWire{conn: conn, decoder: json.NewDecoder(bufio.NewReader(conn)), encoder: json.NewEncoder(conn)}
	if err := w.encoder.Encode(protocol.Frame{Type: protocol.FrameHello, ProtocolVersion: protocol.Version, InstanceID: s.InstanceID(), Role: role, Token: token, LastServerSeq: last}); err != nil {
		t.Fatal(err)
	}
	frame := readFrame(t, w)
	if frame.Type != protocol.FrameWelcome {
		t.Fatalf("expected welcome, got %+v", frame)
	}
	return w, frame
}

func readFrame(t *testing.T, w *testWire) protocol.Frame {
	t.Helper()
	_ = w.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var frame protocol.Frame
	if err := w.decoder.Decode(&frame); err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestServerPublishAckReconnectAndDeduplicate(t *testing.T) {
	s, err := NewServer("127.0.0.1", Options{MaxMessages: 10, MaxBytes: 4096, MaxFrame: 512 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	owner, _ := openWire(t, s, protocol.RoleOrchestrator, s.OwnerToken(), 0)
	worker, welcome := openWire(t, s, protocol.RoleExecutor, s.JoinToken(), 0)
	if welcome.ReconnectToken == "" {
		t.Fatal("expected reconnect token")
	}

	e, err := protocol.NewEnvelope(s.InstanceID(), "message-a", 1, "mac-orchestrator", protocol.RoleOrchestrator, "reporte exacto\nsegunda línea", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.encoder.Encode(protocol.Frame{Type: protocol.FramePublish, InstanceID: s.InstanceID(), Envelope: &e}); err != nil {
		t.Fatal(err)
	}
	accepted := readFrame(t, owner)
	if accepted.Type != protocol.FrameAccepted || accepted.ServerSeq != 1 {
		t.Fatalf("unexpected accepted frame: %+v", accepted)
	}
	message := readFrame(t, worker)
	if message.Type != protocol.FrameMessage || message.Envelope.Body != e.Body {
		t.Fatalf("message body changed: %+v", message)
	}
	if err := worker.encoder.Encode(protocol.Frame{Type: protocol.FrameAck, InstanceID: s.InstanceID(), MessageID: e.MessageID, ServerSeq: message.Envelope.ServerSeq}); err != nil {
		t.Fatal(err)
	}
	ackConfirmed := readFrame(t, worker)
	if ackConfirmed.Type != protocol.FrameAckConfirmed || ackConfirmed.MessageID != e.MessageID {
		t.Fatalf("unexpected ack confirmation: %+v", ackConfirmed)
	}
	delivered := readFrame(t, owner)
	if delivered.Type != protocol.FrameDelivered || delivered.MessageID != e.MessageID {
		t.Fatalf("unexpected delivery frame: %+v", delivered)
	}

	if err := owner.encoder.Encode(protocol.Frame{Type: protocol.FramePublish, InstanceID: s.InstanceID(), Envelope: &e}); err != nil {
		t.Fatal(err)
	}
	duplicate := readFrame(t, owner)
	if duplicate.Type != protocol.FrameAccepted || !duplicate.Duplicate || duplicate.ServerSeq != 1 {
		t.Fatalf("expected idempotent duplicate: %+v", duplicate)
	}

	conflict := e
	conflict.Body = "different"
	conflict.BodySHA256 = protocol.HashBody(conflict.Body)
	if err := owner.encoder.Encode(protocol.Frame{Type: protocol.FramePublish, InstanceID: s.InstanceID(), Envelope: &conflict}); err != nil {
		t.Fatal(err)
	}
	conflictFrame := readFrame(t, owner)
	if conflictFrame.Code != "ID_CONFLICT" {
		t.Fatalf("expected ID_CONFLICT, got %+v", conflictFrame)
	}

	resumeToken := welcome.ReconnectToken
	_ = worker.conn.Close()
	worker2, _ := openWire(t, s, protocol.RoleExecutor, resumeToken, 0)
	replayed := readFrame(t, worker2)
	if replayed.Type != protocol.FrameMessage || replayed.Envelope.MessageID != e.MessageID {
		t.Fatalf("expected replay after reconnect, got %+v", replayed)
	}
	s.Close()
	closed := readFrame(t, worker2)
	if closed.Type != protocol.FrameClose || closed.State != protocol.StateClosed {
		t.Fatalf("expected explicit close notification, got %+v", closed)
	}
	_ = worker2.conn.Close()
	_ = owner.conn.Close()
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition did not become true before timeout")
}

func TestServerHeartbeatReleasesPeerThatStopsResponding(t *testing.T) {
	s, err := NewServer("127.0.0.1", Options{
		MaxMessages:       10,
		MaxBytes:          4096,
		MaxFrame:          512 * 1024,
		HeartbeatInterval: 5 * time.Millisecond,
		HeartbeatTimeout:  25 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, _ := openWire(t, s, protocol.RoleOrchestrator, s.OwnerToken(), 0)
	defer w.conn.Close()
	waitUntil(t, 500*time.Millisecond, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.owner == nil
	})
}

func TestAckConfirmationKeepsReplayCursorUntilAckAndIsIdempotent(t *testing.T) {
	s, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _ := openWire(t, s, protocol.RoleOrchestrator, s.OwnerToken(), 0)
	worker, welcome := openWire(t, s, protocol.RoleExecutor, s.JoinToken(), 0)

	e, err := protocol.NewEnvelope(s.InstanceID(), "ack-replay", 1, "mac-orchestrator", protocol.RoleOrchestrator, "replay me", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.encoder.Encode(protocol.Frame{Type: protocol.FramePublish, InstanceID: s.InstanceID(), Envelope: &e}); err != nil {
		t.Fatal(err)
	}
	_ = readFrame(t, owner)
	first := readFrame(t, worker)
	if first.Type != protocol.FrameMessage {
		t.Fatalf("expected initial message, got %+v", first)
	}
	_ = worker.conn.Close() // Simulate an ACK/confirmation lost before the peer saw it.
	waitUntil(t, 500*time.Millisecond, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.worker == nil
	})

	worker2, _ := openWire(t, s, protocol.RoleExecutor, welcome.ReconnectToken, 0)
	defer worker2.conn.Close()
	replayed := readFrame(t, worker2)
	if replayed.Type != protocol.FrameMessage || replayed.Envelope.MessageID != e.MessageID {
		t.Fatalf("expected replay after unconfirmed ACK, got %+v", replayed)
	}
	ack := protocol.Frame{Type: protocol.FrameAck, InstanceID: s.InstanceID(), MessageID: e.MessageID, ServerSeq: replayed.Envelope.ServerSeq}
	if err := worker2.encoder.Encode(ack); err != nil {
		t.Fatal(err)
	}
	confirmed := readFrame(t, worker2)
	if confirmed.Type != protocol.FrameAckConfirmed {
		t.Fatalf("expected ack confirmation, got %+v", confirmed)
	}
	if err := worker2.encoder.Encode(ack); err != nil {
		t.Fatal(err)
	}
	confirmedAgain := readFrame(t, worker2)
	if confirmedAgain.Type != protocol.FrameAckConfirmed {
		t.Fatalf("duplicate ACK should remain idempotent, got %+v", confirmedAgain)
	}
}

func TestReplayIncludesPeerOwnMessagesWithoutInvalidAck(t *testing.T) {
	s, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _ := openWire(t, s, protocol.RoleOrchestrator, s.OwnerToken(), 0)
	worker, _ := openWire(t, s, protocol.RoleExecutor, s.JoinToken(), 0)
	ownerMessage, err := protocol.NewEnvelope(s.InstanceID(), "owner-message", 1, "mac-orchestrator", protocol.RoleOrchestrator, "owner", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.encoder.Encode(protocol.Frame{Type: protocol.FramePublish, InstanceID: s.InstanceID(), Envelope: &ownerMessage}); err != nil {
		t.Fatal(err)
	}
	_ = readFrame(t, owner)
	_ = readFrame(t, worker)
	workerMessage, err := protocol.NewEnvelope(s.InstanceID(), "worker-message", 1, "win-executor", protocol.RoleExecutor, "worker", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.encoder.Encode(protocol.Frame{Type: protocol.FramePublish, InstanceID: s.InstanceID(), Envelope: &workerMessage}); err != nil {
		t.Fatal(err)
	}
	_ = readFrame(t, worker)
	remote := readFrame(t, owner)
	if remote.Type != protocol.FrameMessage || remote.Envelope.MessageID != workerMessage.MessageID {
		t.Fatalf("owner should receive worker message, got %+v", remote)
	}
	_ = owner.conn.Close()
	waitUntil(t, 500*time.Millisecond, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.owner == nil
	})
	owner2, _ := openWire(t, s, protocol.RoleOrchestrator, s.OwnerToken(), 0)
	defer owner2.conn.Close()
	replayedOwner := readFrame(t, owner2)
	replayedWorker := readFrame(t, owner2)
	if replayedOwner.Type != protocol.FrameMessage || replayedOwner.Envelope.MessageID != ownerMessage.MessageID {
		t.Fatalf("owner replay should include its own message, got %+v", replayedOwner)
	}
	if replayedWorker.Type != protocol.FrameMessage || replayedWorker.Envelope.MessageID != workerMessage.MessageID {
		t.Fatalf("owner replay should include worker message, got %+v", replayedWorker)
	}
	if err := owner2.encoder.Encode(protocol.Frame{Type: protocol.FrameAck, InstanceID: s.InstanceID(), MessageID: workerMessage.MessageID, ServerSeq: replayedWorker.Envelope.ServerSeq}); err != nil {
		t.Fatal(err)
	}
	if confirmed := readFrame(t, owner2); confirmed.Type != protocol.FrameAckConfirmed {
		t.Fatalf("remote replay ACK should be confirmed, got %+v", confirmed)
	}
}

func TestRegeneratePairingInvalidatesResumeToken(t *testing.T) {
	s, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	worker, welcome := openWire(t, s, protocol.RoleExecutor, s.JoinToken(), 0)
	if _, err := s.RegeneratePairingToken(); !errors.Is(err, ErrWorkerConnected) {
		t.Fatalf("expected connected-worker rotation error, got %v", err)
	}
	_ = worker.conn.Close()
	waitUntil(t, 500*time.Millisecond, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.worker == nil
	})
	newJoin, err := s.RegeneratePairingToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, frame := openWireExpectError(t, s, protocol.RoleExecutor, welcome.ReconnectToken, 0); frame.Code != "PAIRING_INVALID" {
		t.Fatalf("old resume token should be invalid, got %+v", frame)
	}
	worker2, fresh := openWire(t, s, protocol.RoleExecutor, newJoin, 0)
	defer worker2.conn.Close()
	if fresh.ReconnectToken == "" || fresh.ReconnectToken == welcome.ReconnectToken {
		t.Fatal("new pairing must issue a fresh resume token")
	}
}

func openWireExpectError(t *testing.T, s *Server, role protocol.Role, token string, last uint64) (*testWire, protocol.Frame) {
	t.Helper()
	conn, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	w := &testWire{conn: conn, decoder: json.NewDecoder(bufio.NewReader(conn)), encoder: json.NewEncoder(conn)}
	if err := w.encoder.Encode(protocol.Frame{Type: protocol.FrameHello, ProtocolVersion: protocol.Version, InstanceID: s.InstanceID(), Role: role, Token: token, LastServerSeq: last}); err != nil {
		t.Fatal(err)
	}
	return w, readFrame(t, w)
}

func TestServerAcceptsMaximumEscapedBody(t *testing.T) {
	s, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _ := openWire(t, s, protocol.RoleOrchestrator, s.OwnerToken(), 0)
	defer owner.conn.Close()
	body := strings.Repeat("\x00\x01\x02\x03\x04\x05\x06\x07\x08\n\r\t\\\"", protocol.MaxBodyBytes/15)
	body += strings.Repeat("\x01", protocol.MaxBodyBytes-len([]byte(body)))
	e, err := protocol.NewEnvelope(s.InstanceID(), "large-wire", 1, "mac-orchestrator", protocol.RoleOrchestrator, body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.encoder.Encode(protocol.Frame{Type: protocol.FramePublish, InstanceID: s.InstanceID(), Envelope: &e}); err != nil {
		t.Fatal(err)
	}
	accepted := readFrame(t, owner)
	if accepted.Type != protocol.FrameAccepted || accepted.MessageID != e.MessageID {
		t.Fatalf("maximum escaped body should be accepted, got %+v", accepted)
	}
}

func TestServerRejectsCrossInstancePairing(t *testing.T) {
	a, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if a.InstanceID() == b.InstanceID() || a.JoinToken() == b.JoinToken() {
		t.Fatal("instances must not share identity or token")
	}

	conn, err := net.Dial("tcp", b.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(bufio.NewReader(conn))
	if err := enc.Encode(protocol.Frame{Type: protocol.FrameHello, ProtocolVersion: protocol.Version, InstanceID: a.InstanceID(), Role: protocol.RoleExecutor, Token: a.JoinToken()}); err != nil {
		t.Fatal(err)
	}
	var frame protocol.Frame
	if err := dec.Decode(&frame); err != nil {
		t.Fatal(err)
	}
	if frame.Code != "INSTANCE_MISMATCH" {
		t.Fatalf("expected pairing rejection, got %+v", frame)
	}
}

func TestServerRejectsWhenRAMLimitReached(t *testing.T) {
	s, err := NewServer("127.0.0.1", Options{MaxMessages: 1, MaxBytes: 32, MaxFrame: 512 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, _ := openWire(t, s, protocol.RoleOrchestrator, s.OwnerToken(), 0)
	for i, body := range []string{"first", strings.Repeat("x", 33)} {
		e, err := protocol.NewEnvelope(s.InstanceID(), string(rune('a'+i)), uint64(i+1), "mac-orchestrator", protocol.RoleOrchestrator, body, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := owner.encoder.Encode(protocol.Frame{Type: protocol.FramePublish, InstanceID: s.InstanceID(), Envelope: &e}); err != nil {
			t.Fatal(err)
		}
		frame := readFrame(t, owner)
		if i == 0 && frame.Type != protocol.FrameAccepted {
			t.Fatalf("first message should fit: %+v", frame)
		}
		if i == 1 && frame.Code != "BUFFER_FULL" {
			t.Fatalf("expected BUFFER_FULL: %+v", frame)
		}
	}
}

func TestClientQueueSurvivesNetworkGapInMemory(t *testing.T) {
	s, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	owner, _, err := Dial(ctx, s.Addr().String(), s.InstanceID(), protocol.RoleOrchestrator, s.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err := owner.Publish("queued before peer joins"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		messages, _, _ := s.Stats()
		if messages == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected one in-memory server message, got %d", messages)
		}
		time.Sleep(5 * time.Millisecond)
	}
	worker, welcome, err := Dial(ctx, s.Addr().String(), s.InstanceID(), protocol.RoleExecutor, s.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	_ = welcome
	if frame := nextClientEvent(t, worker); frame.Type != protocol.FrameWelcome {
		t.Fatalf("expected welcome, got %+v", frame)
	}
	if frame := nextClientEvent(t, worker); frame.Type != protocol.FrameMessage || frame.Envelope.Body != "queued before peer joins" {
		t.Fatalf("expected queued replay, got %+v", frame)
	}
}

func TestProductionOrderKeepsJoinTokenForFirstWindowsClient(t *testing.T) {
	s, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Production prints this token before the Mac owner client dials locally.
	joinToken := s.JoinToken()
	owner, _, err := Dial(context.Background(), s.Addr().String(), s.InstanceID(), protocol.RoleOrchestrator, s.OwnerToken())
	if err != nil {
		t.Fatalf("Mac owner handshake failed: %v", err)
	}
	defer owner.Close()
	worker, _, err := Dial(context.Background(), s.Addr().String(), s.InstanceID(), protocol.RoleExecutor, joinToken)
	if err != nil {
		t.Fatalf("first Windows join rejected after local Mac dial: %v", err)
	}
	defer worker.Close()
}

func TestClientReportsInstanceMismatchSeparately(t *testing.T) {
	s, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, _, err = Dial(context.Background(), s.Addr().String(), "different-instance", protocol.RoleExecutor, s.JoinToken())
	if err == nil || !strings.Contains(err.Error(), "INSTANCE_MISMATCH") {
		t.Fatalf("expected explicit instance mismatch, got %v", err)
	}
}

var testSubscriptions sync.Map // *Client -> *Subscription

// nextClientEvent returns the next frame the server sent to c, read from the
// client's EventHub. Locally queued own messages (server_seq 0) are skipped
// because they never crossed the wire.
func nextClientEvent(t *testing.T, c *Client) protocol.Frame {
	t.Helper()
	value, ok := testSubscriptions.Load(c)
	if !ok {
		sub, err := c.Subscribe(0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(sub.Close)
		value, _ = testSubscriptions.LoadOrStore(c, sub)
	}
	sub := value.(*Subscription)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		event, err := sub.Next(ctx)
		if err != nil {
			t.Fatalf("timed out waiting for client event: %v", err)
		}
		if event.Kind == EventMessage && event.Envelope != nil && event.Envelope.SenderRole == c.Role() && event.ServerSeq == 0 {
			continue
		}
		return event.Frame
	}
}

func TestClientAdvancesReplayCursorOnlyAfterAckConfirmation(t *testing.T) {
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
	worker, _, err := Dial(context.Background(), s.Addr().String(), s.InstanceID(), protocol.RoleExecutor, s.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	_ = nextClientEvent(t, owner)
	_ = nextClientEvent(t, worker)
	e, err := owner.Publish("cursor waits for ACK")
	if err != nil {
		t.Fatal(err)
	}
	accepted := nextClientEvent(t, owner)
	if accepted.Type != protocol.FrameAccepted {
		t.Fatalf("expected accepted, got %+v", accepted)
	}
	message := nextClientEvent(t, worker)
	if message.Type != protocol.FrameMessage {
		t.Fatalf("expected message, got %+v", message)
	}
	if worker.LastServerSeq() != 0 {
		t.Fatalf("message receipt must not advance cursor before ACK, got %d", worker.LastServerSeq())
	}
	if err := worker.Ack(e.MessageID, message.Envelope.ServerSeq); err != nil {
		t.Fatal(err)
	}
	confirmed := nextClientEvent(t, worker)
	if confirmed.Type != protocol.FrameAckConfirmed {
		t.Fatalf("expected ack confirmation, got %+v", confirmed)
	}
	if worker.LastServerSeq() != message.Envelope.ServerSeq {
		t.Fatalf("ACK confirmation should advance cursor to %d, got %d", message.Envelope.ServerSeq, worker.LastServerSeq())
	}
}

func TestNewPairingReplaysCompleteHistoryAndKeepsCursorContiguous(t *testing.T) {
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
	worker, _, err := Dial(context.Background(), s.Addr().String(), s.InstanceID(), protocol.RoleExecutor, s.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	_ = nextClientEvent(t, owner)
	_ = nextClientEvent(t, worker)

	first, err := worker.Publish("Windows primero")
	if err != nil {
		t.Fatal(err)
	}
	if accepted := nextClientEvent(t, worker); accepted.Type != protocol.FrameAccepted {
		t.Fatalf("expected Windows accepted, got %+v", accepted)
	}
	firstRemote := nextClientEvent(t, owner)
	if firstRemote.Type != protocol.FrameMessage || firstRemote.Envelope.MessageID != first.MessageID {
		t.Fatalf("Mac should receive first Windows message, got %+v", firstRemote)
	}
	if err := owner.Ack(first.MessageID, firstRemote.Envelope.ServerSeq); err != nil {
		t.Fatal(err)
	}
	if confirmed := nextClientEvent(t, owner); confirmed.Type != protocol.FrameAckConfirmed {
		t.Fatalf("expected Mac ACK confirmation, got %+v", confirmed)
	}
	if delivered := nextClientEvent(t, worker); delivered.Type != protocol.FrameDelivered || delivered.MessageID != first.MessageID {
		t.Fatalf("expected Windows delivery status, got %+v", delivered)
	}

	second, err := owner.Publish("Mac después")
	if err != nil {
		t.Fatal(err)
	}
	if accepted := nextClientEvent(t, owner); accepted.Type != protocol.FrameAccepted {
		t.Fatalf("expected Mac accepted, got %+v", accepted)
	}
	secondRemote := nextClientEvent(t, worker)
	if secondRemote.Type != protocol.FrameMessage || secondRemote.Envelope.MessageID != second.MessageID {
		t.Fatalf("Windows should receive second Mac message, got %+v", secondRemote)
	}
	if err := worker.Ack(second.MessageID, secondRemote.Envelope.ServerSeq); err != nil {
		t.Fatal(err)
	}
	if confirmed := nextClientEvent(t, worker); confirmed.Type != protocol.FrameAckConfirmed {
		t.Fatalf("expected Windows ACK confirmation, got %+v", confirmed)
	}

	_ = worker.LastServerSeq() // The old session is intentionally discarded.
	worker.Close()
	waitUntil(t, 500*time.Millisecond, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.worker == nil
	})
	newJoin, err := s.RegeneratePairingToken()
	if err != nil {
		t.Fatal(err)
	}
	newWorker, _, err := Dial(context.Background(), s.Addr().String(), s.InstanceID(), protocol.RoleExecutor, newJoin)
	if err != nil {
		t.Fatal(err)
	}
	defer newWorker.Close()
	if welcome := nextClientEvent(t, newWorker); welcome.Type != protocol.FrameWelcome {
		t.Fatalf("expected new worker welcome, got %+v", welcome)
	}
	replayedFirst := nextClientEvent(t, newWorker)
	replayedSecond := nextClientEvent(t, newWorker)
	if replayedFirst.Type != protocol.FrameMessage || replayedSecond.Type != protocol.FrameMessage {
		t.Fatalf("new pairing should receive both history messages: %+v / %+v", replayedFirst, replayedSecond)
	}
	if replayedFirst.Envelope.MessageID != first.MessageID || replayedSecond.Envelope.MessageID != second.MessageID {
		t.Fatalf("history order changed: %s then %s", replayedFirst.Envelope.MessageID, replayedSecond.Envelope.MessageID)
	}
	if replayedFirst.Envelope.SenderRole != protocol.RoleExecutor || replayedSecond.Envelope.SenderRole != protocol.RoleOrchestrator {
		t.Fatalf("new worker perspective lost sender roles: %+v / %+v", replayedFirst.Envelope.SenderRole, replayedSecond.Envelope.SenderRole)
	}
	if newWorker.LastServerSeq() != firstRemote.Envelope.ServerSeq {
		t.Fatalf("own replay should confirm only the first contiguous sequence, got %d", newWorker.LastServerSeq())
	}
	if err := newWorker.Ack(second.MessageID, replayedSecond.Envelope.ServerSeq); err != nil {
		t.Fatal(err)
	}
	confirmed := nextClientEvent(t, newWorker)
	if confirmed.Type != protocol.FrameAckConfirmed || confirmed.MessageID != second.MessageID {
		t.Fatalf("expected remote ACK confirmation, got %+v", confirmed)
	}
	if newWorker.LastServerSeq() != secondRemote.Envelope.ServerSeq {
		t.Fatalf("cursor should advance to latest sequence after remote ACK, got %d", newWorker.LastServerSeq())
	}
}

func TestWorkerConnectedReflectsExecutorPresenceNotOwnClient(t *testing.T) {
	s, err := NewServer("127.0.0.1", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.WorkerConnected() {
		t.Fatal("WorkerConnected should be false before anyone joins")
	}
	owner, _, err := Dial(context.Background(), s.Addr().String(), s.InstanceID(), protocol.RoleOrchestrator, s.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	// The orchestrator's own connection must never make WorkerConnected true:
	// on the Mac side, PeerConnected in tailscale-host mode needs to know
	// whether the remote Windows executor joined, not whether the local
	// client (itself) is connected.
	if s.WorkerConnected() {
		t.Fatal("WorkerConnected should stay false with only the orchestrator connected")
	}
	worker, _, err := Dial(context.Background(), s.Addr().String(), s.InstanceID(), protocol.RoleExecutor, s.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	if !s.WorkerConnected() {
		t.Fatal("WorkerConnected should be true once the executor joins")
	}
	worker.Close()
	deadline := time.Now().Add(2 * time.Second)
	for s.WorkerConnected() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.WorkerConnected() {
		t.Fatal("WorkerConnected should become false after the executor disconnects")
	}
}
