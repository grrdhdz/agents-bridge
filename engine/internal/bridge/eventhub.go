package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

const (
	DefaultEventLimit    = 4096
	DefaultEventMetaMax  = 4 * 1024 * 1024
	DefaultWatcherEvents = 256
	DefaultWatcherBytes  = 2 * 1024 * 1024
)

var ErrControlBackpressure = errors.New("control watcher backpressure")

// CursorExpiredError is returned when the requested event cursor is older
// than the retained in-memory journal. The caller must resubscribe from
// OldestEventSeq and explicitly handle the gap.
type CursorExpiredError struct {
	OldestEventSeq uint64
}

func (e *CursorExpiredError) Error() string {
	return fmt.Sprintf("event cursor expired; oldest_event_seq=%d", e.OldestEventSeq)
}

type EventKind string

const (
	EventMessage   EventKind = "message"
	EventDelivery  EventKind = "delivery"
	EventState     EventKind = "state"
	EventTransport EventKind = "transport"
	EventLifecycle EventKind = "lifecycle"
)

// Event is the single in-memory representation shared by the TUI and local
// control subscribers. Envelope is a pointer so its body has one owner in the
// message journal rather than a second body allocation per event.
type Event struct {
	EventSeq   uint64
	Kind       EventKind
	InstanceID string
	MessageID  string
	ServerSeq  uint64
	Status     string
	State      string
	Detail     string
	Envelope   *protocol.Envelope
	Frame      protocol.Frame `json:"-"`
}

type EventHubOptions struct {
	MaxEvents        int
	MaxMetadataBytes int
	MaxMessages      int
	MaxMessageBytes  int
	WatcherEvents    int
	WatcherBytes     int
}

func DefaultEventHubOptions() EventHubOptions {
	return EventHubOptions{
		MaxEvents:        DefaultEventLimit,
		MaxMetadataBytes: DefaultEventMetaMax,
		MaxMessages:      protocol.MaxHistoryMessages,
		MaxMessageBytes:  protocol.MaxHistoryBytes,
		WatcherEvents:    DefaultWatcherEvents,
		WatcherBytes:     DefaultWatcherBytes,
	}
}

func normalizeEventHubOptions(options EventHubOptions) EventHubOptions {
	defaults := DefaultEventHubOptions()
	if options.MaxEvents <= 0 {
		options.MaxEvents = defaults.MaxEvents
	}
	if options.MaxMetadataBytes <= 0 {
		options.MaxMetadataBytes = defaults.MaxMetadataBytes
	}
	if options.MaxMessages <= 0 {
		options.MaxMessages = defaults.MaxMessages
	}
	if options.MaxMessageBytes <= 0 {
		options.MaxMessageBytes = defaults.MaxMessageBytes
	}
	if options.WatcherEvents <= 0 {
		options.WatcherEvents = defaults.WatcherEvents
	}
	if options.WatcherBytes <= 0 {
		options.WatcherBytes = defaults.WatcherBytes
	}
	return options
}

type EventHub struct {
	mu sync.Mutex

	options EventHubOptions

	nextSeq       uint64
	events        []Event
	metadataBytes int

	messages      map[string]*protocol.Envelope
	messageOrder  []string
	messageBytes  int
	eventByMsgKey map[string]uint64
	statusByKey   map[string]bool

	subs   map[*Subscription]struct{}
	closed bool
}

func NewEventHub(options EventHubOptions) *EventHub {
	options = normalizeEventHubOptions(options)
	return &EventHub{
		options:       options,
		messages:      make(map[string]*protocol.Envelope),
		eventByMsgKey: make(map[string]uint64),
		statusByKey:   make(map[string]bool),
		subs:          make(map[*Subscription]struct{}),
	}
}

// RememberEnvelope registers the one canonical body copy before a local
// publish is acknowledged by the remote server.
func (h *EventHub) RememberEnvelope(e protocol.Envelope) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rememberEnvelopeLocked(e)
}

func (h *EventHub) rememberEnvelopeLocked(e protocol.Envelope) {
	if _, exists := h.messages[e.MessageID]; exists {
		return
	}
	copyEnvelope := e
	h.messages[e.MessageID] = &copyEnvelope
	h.messageOrder = append(h.messageOrder, e.MessageID)
	h.messageBytes += len([]byte(e.Body))
	h.evictMessagesLocked()
}

func (h *EventHub) evictMessagesLocked() {
	for len(h.messageOrder) > h.options.MaxMessages || h.messageBytes > h.options.MaxMessageBytes {
		if len(h.messageOrder) == 0 {
			break
		}
		messageID := h.messageOrder[0]
		h.messageOrder = h.messageOrder[1:]
		e, exists := h.messages[messageID]
		if !exists {
			continue
		}
		delete(h.messages, messageID)
		h.messageBytes -= len([]byte(e.Body))
		h.evictEventsForMessageLocked(messageID, e.ServerSeq)
	}
}

func (h *EventHub) evictEventsForMessageLocked(messageID string, serverSeq uint64) {
	if len(h.events) == 0 {
		return
	}
	filtered := h.events[:0]
	for _, event := range h.events {
		if event.MessageID == messageID || (event.Envelope != nil && event.Envelope.MessageID == messageID) {
			h.metadataBytes -= eventMetadataSize(event)
			delete(h.eventByMsgKey, messageEventKey(messageID, event.ServerSeq, event.Kind))
			if event.Status != "" {
				delete(h.statusByKey, statusKey(messageID, event.ServerSeq, event.Status))
			}
			continue
		}
		filtered = append(filtered, event)
	}
	h.events = filtered
	for sub := range h.subs {
		if sub.referencesMessageLocked(messageID, serverSeq) {
			if len(sub.queue) >= sub.maxEvents || sub.queueBytes >= sub.maxBytes {
				sub.failLocked(ErrControlBackpressure)
			} else {
				sub.failLocked(&CursorExpiredError{OldestEventSeq: h.oldestEventSeqLocked()})
			}
		}
	}
}

func messageEventKey(messageID string, serverSeq uint64, kind EventKind) string {
	return fmt.Sprintf("%s\x00%d\x00%s", messageID, serverSeq, kind)
}

func statusKey(messageID string, serverSeq uint64, status string) string {
	return fmt.Sprintf("%s\x00%d\x00%s", messageID, serverSeq, status)
}

// PublishFrame converts one protocol frame into one logical Event. Duplicate
// replayed messages and repeated delivery states do not consume event_seq.
func (h *EventHub) PublishFrame(frame protocol.Frame) (Event, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.publishFrameLocked(frame)
}

func (h *EventHub) publishFrameLocked(frame protocol.Frame) (Event, bool) {
	if h.closed {
		return Event{}, false
	}
	event := Event{InstanceID: frame.InstanceID, MessageID: frame.MessageID, ServerSeq: frame.ServerSeq, Detail: frame.Detail, Frame: frame}
	switch frame.Type {
	case protocol.FrameMessage:
		event.Kind = EventMessage
		event.Status = "received"
		if frame.Envelope == nil {
			return Event{}, false
		}
		h.rememberEnvelopeLocked(*frame.Envelope)
		event.MessageID = frame.Envelope.MessageID
		event.ServerSeq = frame.Envelope.ServerSeq
		key := messageEventKey(event.MessageID, event.ServerSeq, EventMessage)
		if old, exists := h.eventByMsgKey[key]; exists {
			return h.eventBySeqLocked(old), false
		}
		event.Envelope = h.envelopePointerLocked(event.MessageID)
	case protocol.FrameAccepted:
		event.Kind = EventDelivery
		event.Status = "accepted"
	case protocol.FrameAckConfirmed, protocol.FrameDelivered:
		event.Kind = EventDelivery
		event.Status = "delivered"
	case protocol.FrameError:
		event.Kind = EventState
		event.Status = "rejected"
	case protocol.FrameTransportError:
		event.Kind = EventTransport
		event.State = "reconnecting"
	case protocol.FrameClose:
		event.Kind = EventLifecycle
		event.State = "closed"
	case protocol.FrameWelcome:
		event.Kind = EventState
		event.State = "connected"
	default:
		return Event{}, false
	}
	if event.MessageID != "" {
		if stored, ok := h.messages[event.MessageID]; ok {
			event.Envelope = stored
			event.Frame.Envelope = stored
		}
		if event.Status != "" {
			key := statusKey(event.MessageID, event.ServerSeq, event.Status)
			if h.statusByKey[key] {
				return h.findStatusEventLocked(event.MessageID, event.ServerSeq, event.Status), false
			}
			h.statusByKey[key] = true
		}
	}
	return h.appendLocked(event), true
}

func (h *EventHub) envelopePointerLocked(messageID string) *protocol.Envelope {
	return h.messages[messageID]
}

func (h *EventHub) Envelope(messageID string) (protocol.Envelope, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.messages[messageID]
	if !ok {
		return protocol.Envelope{}, false
	}
	return *e, true
}

func (h *EventHub) findStatusEventLocked(messageID string, serverSeq uint64, status string) Event {
	for i := len(h.events) - 1; i >= 0; i-- {
		e := h.events[i]
		if e.MessageID == messageID && e.ServerSeq == serverSeq && e.Status == status {
			return e
		}
	}
	return Event{}
}

func (h *EventHub) appendLocked(event Event) Event {
	h.nextSeq++
	event.EventSeq = h.nextSeq
	h.events = append(h.events, event)
	h.metadataBytes += eventMetadataSize(event)
	if event.MessageID != "" {
		h.eventByMsgKey[messageEventKey(event.MessageID, event.ServerSeq, event.Kind)] = event.EventSeq
	}
	h.evictEventsByLimitLocked()
	for sub := range h.subs {
		sub.enqueueLocked(event)
	}
	return event
}

func (h *EventHub) evictEventsByLimitLocked() {
	for len(h.events) > h.options.MaxEvents || h.metadataBytes > h.options.MaxMetadataBytes {
		if len(h.events) == 0 {
			break
		}
		event := h.events[0]
		h.events = h.events[1:]
		h.metadataBytes -= eventMetadataSize(event)
		if event.MessageID != "" {
			delete(h.eventByMsgKey, messageEventKey(event.MessageID, event.ServerSeq, event.Kind))
			if event.Status != "" {
				delete(h.statusByKey, statusKey(event.MessageID, event.ServerSeq, event.Status))
			}
		}
	}
}

func (h *EventHub) eventBySeqLocked(seq uint64) Event {
	for _, event := range h.events {
		if event.EventSeq == seq {
			return event
		}
	}
	return Event{}
}

func eventMetadataSize(event Event) int {
	copyEvent := event
	if copyEvent.Envelope != nil {
		copyEnvelope := *copyEvent.Envelope
		copyEnvelope.Body = ""
		copyEvent.Envelope = &copyEnvelope
	}
	b, _ := json.Marshal(copyEvent)
	return len(b)
}

func (h *EventHub) oldestEventSeqLocked() uint64 {
	if len(h.events) == 0 {
		return h.nextSeq + 1
	}
	return h.events[0].EventSeq
}

func (h *EventHub) OldestEventSeq() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.oldestEventSeqLocked()
}

func (h *EventHub) LatestEventSeq() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nextSeq
}

func (h *EventHub) Read(after uint64, limit int) ([]Event, uint64, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	if len(h.events) > 0 && after+1 < h.oldestEventSeqLocked() {
		return nil, h.oldestEventSeqLocked(), false, &CursorExpiredError{OldestEventSeq: h.oldestEventSeqLocked()}
	}
	result := make([]Event, 0, limit)
	for _, event := range h.events {
		if event.EventSeq <= after {
			continue
		}
		result = append(result, event)
		if len(result) >= limit {
			break
		}
	}
	var next uint64 = after
	if len(result) > 0 {
		next = result[len(result)-1].EventSeq
	}
	hasMore := false
	for _, event := range h.events {
		if event.EventSeq > next {
			hasMore = true
			break
		}
	}
	return result, next, hasMore, nil
}

func (h *EventHub) Subscribe(after uint64) (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrClosed
	}
	if len(h.events) > 0 && after+1 < h.oldestEventSeqLocked() {
		return nil, &CursorExpiredError{OldestEventSeq: h.oldestEventSeqLocked()}
	}
	sub := &Subscription{
		hub:       h,
		maxEvents: h.options.WatcherEvents,
		maxBytes:  h.options.WatcherBytes,
		signal:    make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
	for _, event := range h.events {
		if event.EventSeq > after {
			sub.queue = append(sub.queue, event)
			sub.queueBytes += eventMetadataSize(event)
		}
	}
	if len(sub.queue) > sub.maxEvents || sub.queueBytes > sub.maxBytes {
		return nil, ErrControlBackpressure
	}
	h.subs[sub] = struct{}{}
	if len(sub.queue) > 0 {
		sub.signal <- struct{}{}
	}
	return sub, nil
}

func (h *EventHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for sub := range h.subs {
		sub.failLocked(ErrClosed)
	}
	h.subs = make(map[*Subscription]struct{})
	h.events = nil
	h.messages = make(map[string]*protocol.Envelope)
	h.messageOrder = nil
	h.messageBytes = 0
	h.metadataBytes = 0
}

type Subscription struct {
	hub       *EventHub
	maxEvents int
	maxBytes  int

	queue      []Event
	queueBytes int
	signal     chan struct{}
	done       chan struct{}
	err        error
	closed     bool
}

func (s *Subscription) enqueueLocked(event Event) {
	if s.closed {
		return
	}
	size := eventMetadataSize(event)
	if len(s.queue) >= s.maxEvents || s.queueBytes+size > s.maxBytes {
		s.failLocked(ErrControlBackpressure)
		return
	}
	s.queue = append(s.queue, event)
	s.queueBytes += size
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

func (s *Subscription) referencesMessageLocked(messageID string, serverSeq uint64) bool {
	if s.closed {
		return false
	}
	for _, event := range s.queue {
		if event.MessageID == messageID || (event.Envelope != nil && event.Envelope.MessageID == messageID && event.ServerSeq == serverSeq) {
			return true
		}
	}
	return false
}

func (s *Subscription) failLocked(err error) {
	if s.closed {
		return
	}
	s.closed = true
	s.err = err
	s.queue = nil
	s.queueBytes = 0
	delete(s.hub.subs, s)
	close(s.done)
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

func (s *Subscription) Next(ctx context.Context) (Event, error) {
	for {
		s.hub.mu.Lock()
		if len(s.queue) > 0 {
			event := s.queue[0]
			s.queue = s.queue[1:]
			s.queueBytes -= eventMetadataSize(event)
			s.hub.mu.Unlock()
			return event, nil
		}
		if s.closed {
			err := s.err
			s.hub.mu.Unlock()
			if err == nil {
				err = ErrClosed
			}
			return Event{}, err
		}
		s.hub.mu.Unlock()
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-s.done:
		case <-s.signal:
		}
	}
}

func (s *Subscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	s.failLocked(ErrClosed)
}
