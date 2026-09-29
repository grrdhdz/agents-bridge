package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

// ControlTransport adapts one control-plane endpoint (an orchestrator's or
// executor's descriptor, reached only through /v1/watch, /v1/send and
// /v1/health) into a Transport, for the observing TUI (§6). It never
// confirms a peer message on the agent's behalf: Ack is a documented no-op,
// so the agent's own ctl wait still receives and ACKs everything itself.
// Every message this transport publishes carries source=human-operator
// (§6.1): this transport exists only for a human operator watching and
// occasionally typing into someone else's conversation.
type ControlTransport struct {
	descriptor control.Descriptor
}

// NewControlTransport wraps one descriptor: the loopback control endpoint
// (with its capability) to watch, send to and stop.
func NewControlTransport(descriptor control.Descriptor) *ControlTransport {
	return &ControlTransport{descriptor: descriptor}
}

func (t *ControlTransport) InstanceID() string { return t.descriptor.InstanceID }

// Connected always reports true: unlike a single persistent TCP connection,
// each control-plane call is its own HTTP request, and resilience lives
// entirely inside the watch subscription's own reconnect loop (see
// controlSubscription.Next), not in a reconnect tick against this method.
func (t *ControlTransport) Connected() bool { return true }

// Reconnect is a no-op: see Connected.
func (t *ControlTransport) Reconnect(context.Context) error { return nil }

func (t *ControlTransport) Subscribe(afterEventSeq uint64) (EventSubscription, error) {
	return &controlSubscription{descriptor: t.descriptor, after: afterEventSeq}, nil
}

// Publish sends body as the human operator's own message (§6.1): every
// observer send carries source=human-operator, regardless of which role's
// endpoint it went through.
func (t *ControlTransport) Publish(body string) (protocol.Envelope, error) {
	messageID, err := control.NewID()
	if err != nil {
		return protocol.Envelope{}, err
	}
	payload, err := json.Marshal(map[string]any{"v": 1, "message_id": messageID, "body": body, "source": protocol.SourceHumanOperator})
	if err != nil {
		return protocol.Envelope{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := control.Do(ctx, t.descriptor, http.MethodPost, "/v1/send", bytes.NewReader(payload))
	if err != nil {
		return protocol.Envelope{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return protocol.Envelope{}, err
	}
	if response.StatusCode != http.StatusAccepted {
		return protocol.Envelope{}, describeControlError(data)
	}
	now := time.Now()
	return protocol.NewEnvelopeWithSource(t.descriptor.InstanceID, messageID, 0, protocol.ExpectedSenderID(t.descriptor.LocalRole), t.descriptor.LocalRole, body, protocol.SourceHumanOperator, now)
}

// Ack is a documented no-op: the observing TUI never confirms a message on
// the agent's behalf (§6, "No consume").
func (t *ControlTransport) Ack(string, uint64) error { return nil }

// QueueStats has no meaning over the control plane (there is no persistent
// client-side send queue to report on); it always reports empty.
func (t *ControlTransport) QueueStats() (int, int) { return 0, 0 }

// Close is a no-op: this transport holds no long-lived connection of its
// own to release (the underlying subscription closes itself).
func (t *ControlTransport) Close() {}

// Status implements StatusProvider (§7) with GET /v1/health, which already
// reports peer_connected (internal/control/http.go's handleHealth); cwd and
// started_at come straight from the descriptor already held locally
// (control.Descriptor.CWD/StartedAt), with no need to add them to the
// health payload or touch internal/control at all.
func (t *ControlTransport) Status(ctx context.Context) (BridgeStatus, error) {
	response, err := control.Do(ctx, t.descriptor, http.MethodGet, "/v1/health", nil)
	if err != nil {
		return BridgeStatus{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return BridgeStatus{}, err
	}
	if response.StatusCode != http.StatusOK {
		return BridgeStatus{}, describeControlError(data)
	}
	var record struct {
		PeerConnected bool `json:"peer_connected"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return BridgeStatus{}, err
	}
	return BridgeStatus{PeerConnected: record.PeerConnected, StartedAt: t.descriptor.StartedAt, Cwd: t.descriptor.CWD}, nil
}

// Stop calls POST /v1/stop on this endpoint (§4.2), for the observing TUI's
// /stop command.
func (t *ControlTransport) Stop(ctx context.Context) error {
	response, err := control.Do(ctx, t.descriptor, http.MethodPost, "/v1/stop", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusAccepted {
		return describeControlError(data)
	}
	return nil
}

func describeControlError(data []byte) error {
	var record struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &record) == nil && record.Code != "" {
		return fmt.Errorf("%s: %s", record.Code, record.Message)
	}
	return fmt.Errorf("control endpoint error: %s", string(data))
}

// controlWatchRecord mirrors the unexported JSON shapes control/http.go
// writes on /v1/watch: either one eventRecord (type=="event") or one
// error responseEnvelope (type=="error"). Decoding this way keeps the
// observer independent of control's internal Go types, matching only the
// wire schema documented in the spec.
type controlWatchRecord struct {
	Type       string             `json:"type"`
	Event      string             `json:"event"`
	EventSeq   uint64             `json:"event_seq"`
	ServerSeq  uint64             `json:"server_seq"`
	MessageID  string             `json:"message_id"`
	Status     string             `json:"status"`
	State      string             `json:"state"`
	Detail     string             `json:"detail"`
	Message    *protocol.Envelope `json:"message,omitempty"`
	Code       string             `json:"code"`
	OldestSeq  uint64             `json:"oldest_event_seq"`
	InstanceID string             `json:"instance_id"`
}

// frameFromWatchRecord reconstructs the protocol.Frame a direct *bridge.Client
// would have produced for the same server-side event, so Model.handleFrame
// behaves identically regardless of transport. It is the inverse of
// bridge.EventHub.PublishFrame's switch (internal/bridge/eventhub.go).
func frameFromWatchRecord(r controlWatchRecord) (protocol.Frame, bool) {
	frame := protocol.Frame{InstanceID: r.InstanceID, MessageID: r.MessageID, ServerSeq: r.ServerSeq, Detail: r.Detail}
	switch r.Event {
	case "message":
		frame.Type = protocol.FrameMessage
		frame.Envelope = r.Message
	case "delivery":
		switch r.Status {
		case "accepted":
			frame.Type = protocol.FrameAccepted
		case "delivered":
			frame.Type = protocol.FrameAckConfirmed
		default:
			return protocol.Frame{}, false
		}
	case "state":
		switch {
		case r.Status == "rejected":
			frame.Type = protocol.FrameError
		case r.State == "connected":
			frame.Type = protocol.FrameWelcome
		default:
			return protocol.Frame{}, false
		}
	case "transport":
		frame.Type = protocol.FrameTransportError
	case "lifecycle":
		frame.Type = protocol.FrameClose
	default:
		return protocol.Frame{}, false
	}
	return frame, true
}

// controlSubscription is the EventSubscription behind ControlTransport. It
// opens /v1/watch from `after`, and on any stream break (network error,
// server restart, or CURSOR_EXPIRED) reconnects from the last event_seq it
// successfully delivered, so the observer's replay never repeats or skips
// an event under normal operation (§6, "Reconexión").
type controlSubscription struct {
	descriptor control.Descriptor
	after      uint64

	mu       sync.Mutex
	response *http.Response
	decoder  *json.Decoder
	closed   bool
}

func (s *controlSubscription) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.response != nil {
		_ = s.response.Body.Close()
		s.response = nil
	}
}

func (s *controlSubscription) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Next blocks until the next event, reconnecting the underlying watch
// stream as needed. It only returns an error when ctx is done or Close was
// called; a transient network problem is retried with a short backoff
// instead of surfacing as a terminal error, so the observer stays up
// through a brief server restart.
func (s *controlSubscription) Next(ctx context.Context) (bridge.Event, error) {
	backoff := 200 * time.Millisecond
	const maxBackoff = 5 * time.Second
	for {
		if s.isClosed() {
			return bridge.Event{}, bridge.ErrClosed
		}
		if err := ctx.Err(); err != nil {
			return bridge.Event{}, err
		}
		decoder, err := s.ensureStream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return bridge.Event{}, ctx.Err()
			}
			if !sleepOrDone(ctx, backoff) {
				return bridge.Event{}, ctx.Err()
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		var raw controlWatchRecord
		if err := decoder.Decode(&raw); err != nil {
			s.mu.Lock()
			if s.response != nil {
				_ = s.response.Body.Close()
			}
			s.response, s.decoder = nil, nil
			s.mu.Unlock()
			if s.isClosed() || ctx.Err() != nil {
				return bridge.Event{}, bridge.ErrClosed
			}
			if !sleepOrDone(ctx, backoff) {
				return bridge.Event{}, ctx.Err()
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		backoff = 200 * time.Millisecond
		if raw.Type == "error" {
			if raw.Code == "CURSOR_EXPIRED" && raw.OldestSeq > 0 {
				s.mu.Lock()
				s.after = raw.OldestSeq - 1
				if s.response != nil {
					_ = s.response.Body.Close()
				}
				s.response, s.decoder = nil, nil
				s.mu.Unlock()
				continue
			}
			// Any other terminal error (e.g. the instance is gone) is
			// reported once as a close, matching how a direct client
			// reports the end of its connection.
			return bridge.Event{Kind: bridge.EventLifecycle, State: "closed", Detail: raw.Code + ": " + raw.Detail, Frame: protocol.Frame{Type: protocol.FrameClose, Detail: raw.Code}}, nil
		}
		frame, ok := frameFromWatchRecord(raw)
		if !ok {
			continue
		}
		s.mu.Lock()
		if raw.EventSeq > s.after {
			s.after = raw.EventSeq
		}
		s.mu.Unlock()
		return bridge.Event{EventSeq: raw.EventSeq, InstanceID: raw.InstanceID, MessageID: raw.MessageID, ServerSeq: raw.ServerSeq, Status: raw.Status, State: raw.State, Detail: raw.Detail, Envelope: raw.Message, Frame: frame}, nil
	}
}

func (s *controlSubscription) ensureStream(ctx context.Context) (*json.Decoder, error) {
	s.mu.Lock()
	if s.decoder != nil {
		decoder := s.decoder
		s.mu.Unlock()
		return decoder, nil
	}
	after := s.after
	s.mu.Unlock()

	response, err := control.Do(ctx, s.descriptor, http.MethodGet, fmt.Sprintf("/v1/watch?after_event_seq=%d", after), nil)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		return nil, describeControlError(data)
	}
	decoder := json.NewDecoder(response.Body)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = response.Body.Close()
		return nil, bridge.ErrClosed
	}
	s.response, s.decoder = response, decoder
	s.mu.Unlock()
	return decoder, nil
}

func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}

// sleepOrDone waits for d or ctx's end, whichever comes first. It reports
// whether it slept the full duration (false means ctx ended first).
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
