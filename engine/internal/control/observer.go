package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"io"
	"net/http"
	"sync"
	"time"
)

func NewSubscription(d Descriptor, after uint64) *Subscription {
	return &Subscription{descriptor: d, after: after}
}

// WatchRecord mirrors the unexported JSON shapes control/http.go
// writes on /v1/watch: either one eventRecord (type=="event") or one
// error responseEnvelope (type=="error"). Decoding this way keeps the
// observer independent of control's internal Go types, matching only the
// wire schema documented in the spec.
type WatchRecord struct {
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

// FrameFromWatchRecord reconstructs the protocol.Frame a direct *bridge.Client
// would have produced for the same server-side event, so Model.handleFrame
// behaves identically regardless of transport. It is the inverse of
// bridge.EventHub.PublishFrame's switch (internal/bridge/eventhub.go).
func FrameFromWatchRecord(r WatchRecord) (protocol.Frame, bool) {
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

// Subscription is the EventSubscription behind ControlTransport. It
// opens /v1/watch from `after`, and on any stream break (network error,
// server restart, or CURSOR_EXPIRED) reconnects from the last event_seq it
// successfully delivered, so the observer's replay never repeats or skips
// an event under normal operation (§6, "Reconexión").
type Subscription struct {
	descriptor Descriptor
	after      uint64

	mu       sync.Mutex
	response *http.Response
	decoder  *json.Decoder
	closed   bool
}

func (s *Subscription) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.response != nil {
		_ = s.response.Body.Close()
		s.response = nil
	}
}

func (s *Subscription) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Next blocks until the next event, reconnecting the underlying watch
// stream as needed. It only returns an error when ctx is done or Close was
// called; a transient network problem is retried with a short backoff
// instead of surfacing as a terminal error, so the observer stays up
// through a brief server restart.
func (s *Subscription) Next(ctx context.Context) (bridge.Event, error) {
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
		var raw WatchRecord
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
		frame, ok := FrameFromWatchRecord(raw)
		if !ok {
			continue
		}
		s.mu.Lock()
		if raw.EventSeq > s.after {
			s.after = raw.EventSeq
		}
		s.mu.Unlock()
		return bridge.Event{Kind: bridge.EventKind(raw.Event), EventSeq: raw.EventSeq, InstanceID: raw.InstanceID, MessageID: raw.MessageID, ServerSeq: raw.ServerSeq, Status: raw.Status, State: raw.State, Detail: raw.Detail, Envelope: raw.Message, Frame: frame}, nil
	}
}

func (s *Subscription) ensureStream(ctx context.Context) (*json.Decoder, error) {
	s.mu.Lock()
	if s.decoder != nil {
		decoder := s.decoder
		s.mu.Unlock()
		return decoder, nil
	}
	after := s.after
	s.mu.Unlock()

	response, err := Do(ctx, s.descriptor, http.MethodGet, fmt.Sprintf("/v1/watch?after_event_seq=%d", after), nil)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return nil, errors.New("control watch rejected")
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
