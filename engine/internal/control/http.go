package control

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

const controlContentType = "application/x-ndjson; charset=utf-8"

type responseEnvelope struct {
	V               int              `json:"v"`
	Type            string           `json:"type"`
	RequestID       string           `json:"request_id,omitempty"`
	OK              bool             `json:"ok"`
	Operation       string           `json:"operation,omitempty"`
	InstanceID      string           `json:"instance_id,omitempty"`
	AfterEventSeq   uint64           `json:"after_event_seq,omitempty"`
	Events          []eventRecord    `json:"events,omitempty"`
	NextAfterSeq    uint64           `json:"next_after_event_seq,omitempty"`
	HasMore         bool             `json:"has_more,omitempty"`
	Status          string           `json:"status,omitempty"`
	MessageID       string           `json:"message_id,omitempty"`
	ClientSeq       uint64           `json:"client_seq,omitempty"`
	ServerSeq       uint64           `json:"server_seq,omitempty"`
	QueuedEventSeq  uint64           `json:"queued_event_seq,omitempty"`
	Code            string           `json:"code,omitempty"`
	Message         string           `json:"message,omitempty"`
	Retryable       bool             `json:"retryable,omitempty"`
	OldestEventSeq  uint64           `json:"oldest_event_seq,omitempty"`
	Instances       []ListedInstance `json:"instances,omitempty"`
	State           string           `json:"state,omitempty"`
	PID             int              `json:"pid,omitempty"`
	Heartbeat       string           `json:"heartbeat_at,omitempty"`
	LatestServerSeq uint64           `json:"latest_server_seq,omitempty"`
	PeerConnected   bool             `json:"peer_connected,omitempty"`
	// Roles is the per-role state (§3.4), keyed by RoleKey; health only.
	Roles       map[string]RoleSnapshot `json:"roles,omitempty"`
	FinReceived bool                    `json:"fin_received"`
	// Unread accompanies INBOX_NOT_EMPTY (§3.1).
	Unread int `json:"unread,omitempty"`
}

// peekResponse is GET /v1/peek's body. Unlike responseEnvelope its fields
// have no omitempty: "unread":0 and "urgent":false are answers, not absences.
type peekResponse struct {
	V               int    `json:"v"`
	Type            string `json:"type"`
	RequestID       string `json:"request_id,omitempty"`
	OK              bool   `json:"ok"`
	Operation       string `json:"operation"`
	InstanceID      string `json:"instance_id"`
	Unread          int    `json:"unread"`
	Urgent          bool   `json:"urgent"`
	LatestLabel     string `json:"latest_label"`
	LatestMessageID string `json:"latest_message_id"`
	FinReceived     bool   `json:"fin_received"`
}

// waitResponse is separate from responseEnvelope because there "message" is
// the error text, while wait returns the canonical envelope under that key.
type waitResponse struct {
	V          int                `json:"v"`
	Type       string             `json:"type"`
	RequestID  string             `json:"request_id,omitempty"`
	OK         bool               `json:"ok"`
	Operation  string             `json:"operation"`
	Status     string             `json:"status"`
	InstanceID string             `json:"instance_id"`
	EventSeq   uint64             `json:"event_seq,omitempty"`
	Message    *protocol.Envelope `json:"message,omitempty"`
}

type eventRecord struct {
	V          int                `json:"v"`
	Type       string             `json:"type"`
	RequestID  string             `json:"request_id,omitempty"`
	Event      string             `json:"event"`
	InstanceID string             `json:"instance_id"`
	EventSeq   uint64             `json:"event_seq"`
	ServerSeq  uint64             `json:"server_seq,omitempty"`
	MessageID  string             `json:"message_id,omitempty"`
	Status     string             `json:"status,omitempty"`
	State      string             `json:"state,omitempty"`
	Detail     string             `json:"detail,omitempty"`
	Message    *protocol.Envelope `json:"message,omitempty"`
}

func (e *Endpoint) registerHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/v1/health", e.handleHealth)
	mux.HandleFunc("/v1/heartbeat", e.handleHeartbeat)
	mux.HandleFunc("/v1/read", e.handleRead)
	mux.HandleFunc("/v1/watch", e.handleWatch)
	mux.HandleFunc("/v1/send", e.handleSend)
	mux.HandleFunc("/v1/wait", e.handleWait)
	mux.HandleFunc("/v1/peek", e.handlePeek)
	mux.HandleFunc("/v1/stop", e.handleStop)
}

func (e *Endpoint) authorize(w http.ResponseWriter, r *http.Request) (string, bool) {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "127.0.0.1" && host != "::1" {
		writeError(w, "", "UNAUTHORIZED", "control endpoint only accepts loopback clients", false, http.StatusUnauthorized, e.descriptor.InstanceID, 0)
		return "", false
	}
	requestID := r.Header.Get("X-Agents-Bridge-Request-ID")
	if !validRequestID(requestID) {
		writeError(w, requestID, "INVALID_REQUEST_ID", "X-Agents-Bridge-Request-ID must be 1-128 ASCII characters", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return "", false
	}
	authorization := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(authorization, prefix)), []byte(e.capability)) != 1 {
		writeError(w, requestID, "UNAUTHORIZED", "capability is absent or invalid", false, http.StatusUnauthorized, e.descriptor.InstanceID, 0)
		return "", false
	}
	return requestID, true
}

func validRequestID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for i := range value {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func setJSONLHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", controlContentType)
}

func writeJSON(w http.ResponseWriter, status int, value any) error {
	setJSONLHeaders(w)
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	return encoder.Encode(value)
}

func writeError(w http.ResponseWriter, requestID, code, message string, retryable bool, status int, instanceID string, oldest uint64) {
	_ = writeJSON(w, status, responseEnvelope{
		V:              1,
		Type:           "error",
		RequestID:      requestID,
		OK:             false,
		Code:           code,
		Message:        message,
		Retryable:      retryable,
		InstanceID:     instanceID,
		OldestEventSeq: oldest,
	})
}

func (e *Endpoint) handleHealth(w http.ResponseWriter, r *http.Request) {
	requestID, ok := e.authorize(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, requestID, "INVALID_METHOD", "health requires GET", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	e.descriptorMu.RLock()
	descriptor := e.descriptor
	e.descriptorMu.RUnlock()
	roles := make(map[string]RoleSnapshot)
	for role, snap := range e.RoleSnapshots() {
		roles[RoleKey(role)] = snap
	}
	_ = writeJSON(w, http.StatusOK, responseEnvelope{
		Roles:           roles,
		FinReceived:     e.hasReceivedFIN(),
		V:               1,
		Type:            "response",
		RequestID:       requestID,
		OK:              true,
		Operation:       "health",
		InstanceID:      e.descriptor.InstanceID,
		State:           "running",
		PID:             descriptor.PID,
		Heartbeat:       descriptor.HeartbeatAt.UTC().Format(time.RFC3339Nano),
		LatestServerSeq: e.client.LastServerSeq(),
		OldestEventSeq:  e.client.EventHub().OldestEventSeq(),
		QueuedEventSeq:  e.client.EventHub().LatestEventSeq(),
		PeerConnected:   e.isPeerConnected(),
	})
}

// handleHeartbeat records the endpoint role, never a caller-supplied role.
func (e *Endpoint) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	requestID, ok := e.authorize(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, requestID, "INVALID_METHOD", "heartbeat requires POST", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	var input struct {
		Tool string `json:"tool"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4097))
	err := decoder.Decode(&input)
	var extra any
	if err != nil || decoder.Decode(&extra) != io.EOF || len(input.Tool) > 128 || strings.IndexFunc(input.Tool, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		writeError(w, requestID, "INVALID_JSON", "invalid heartbeat tool", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	e.roles.Heartbeat(e.descriptor.LocalRole, input.Tool)
	e.activity.Touch()
	_ = writeJSON(w, http.StatusOK, responseEnvelope{V: 1, Type: "response", RequestID: requestID, OK: true, Operation: "heartbeat", InstanceID: e.descriptor.InstanceID})
}

// handleStop implements POST /v1/stop (§4.2). Only a CanStop endpoint accepts
// it; the response is written and flushed before Stop runs asynchronously, so
// the caller always sees the 202 even if Stop tears down this very endpoint.
func (e *Endpoint) handleStop(w http.ResponseWriter, r *http.Request) {
	requestID, ok := e.authorize(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, requestID, "INVALID_METHOD", "stop requires POST", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	if !e.canStop || e.stop == nil {
		writeError(w, requestID, "FORBIDDEN", "this role cannot stop the bridge", false, http.StatusForbidden, e.descriptor.InstanceID, 0)
		return
	}
	if err := writeJSON(w, http.StatusAccepted, responseEnvelope{V: 1, Type: "response", RequestID: requestID, OK: true, Operation: "stop", Status: "stopping", InstanceID: e.descriptor.InstanceID}); err != nil {
		return
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	stop := e.stop
	go stop()
}

func parseCursor(r *http.Request) (uint64, int, error) {
	after := uint64(0)
	limit := 100
	var err error
	if value := r.URL.Query().Get("after_event_seq"); value != "" {
		after, err = strconv.ParseUint(value, 10, 64)
		if err != nil {
			return 0, 0, errors.New("after_event_seq must be an unsigned integer")
		}
	}
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 1000 {
			return 0, 0, errors.New("limit must be between 1 and 1000")
		}
	}
	return after, limit, nil
}

func eventRecordFrom(event bridge.Event, requestID string) eventRecord {
	return eventRecord{
		V:          1,
		Type:       "event",
		RequestID:  requestID,
		Event:      string(event.Kind),
		InstanceID: event.InstanceID,
		EventSeq:   event.EventSeq,
		ServerSeq:  event.ServerSeq,
		MessageID:  event.MessageID,
		Status:     event.Status,
		State:      event.State,
		Detail:     event.Detail,
		Message:    event.Envelope,
	}
}

func (e *Endpoint) handleRead(w http.ResponseWriter, r *http.Request) {
	requestID, ok := e.authorize(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, requestID, "INVALID_METHOD", "read requires GET", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	after, limit, err := parseCursor(r)
	if err != nil {
		writeError(w, requestID, "INVALID_JSON", err.Error(), false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	events, next, more, err := e.client.ReadEvents(after, limit)
	if err != nil {
		e.writeBridgeError(w, requestID, err)
		return
	}
	records := make([]eventRecord, 0, len(events))
	for _, event := range events {
		records = append(records, eventRecordFrom(event, requestID))
	}
	_ = writeJSON(w, http.StatusOK, responseEnvelope{V: 1, Type: "response", RequestID: requestID, OK: true, Operation: "read", InstanceID: e.descriptor.InstanceID, AfterEventSeq: after, Events: records, NextAfterSeq: next, HasMore: more})
}

func (e *Endpoint) handleWatch(w http.ResponseWriter, r *http.Request) {
	requestID, ok := e.authorize(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, requestID, "INVALID_METHOD", "watch requires GET", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	after, _, err := parseCursor(r)
	if err != nil {
		writeError(w, requestID, "INVALID_JSON", err.Error(), false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	// §8/§10.3: at most maxConcurrentWatch watchers per endpoint; the next one
	// gets CONTROL_BACKPRESSURE instead of an unbounded number of goroutines
	// and subscriptions.
	if e.watchCount.Add(1) > maxConcurrentWatch {
		e.watchCount.Add(-1)
		writeError(w, requestID, "CONTROL_BACKPRESSURE", "too many concurrent watchers for this endpoint", true, http.StatusTooManyRequests, e.descriptor.InstanceID, 0)
		return
	}
	defer e.watchCount.Add(-1)
	if e.descriptor.LocalRole == protocol.RoleOrchestrator {
		e.activity.Enter()
		defer e.activity.Leave()
	}
	sub, err := e.client.Subscribe(after)
	if err != nil {
		e.writeBridgeError(w, requestID, err)
		return
	}
	defer sub.Close()
	setJSONLHeaders(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, requestID, "INTERNAL", "watch requires an HTTP flusher", false, http.StatusInternalServerError, e.descriptor.InstanceID, 0)
		return
	}
	w.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(w)
	for {
		event, err := sub.Next(r.Context())
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, bridge.ErrClosed) {
				return
			}
			code, status, retryable, oldest := bridgeError(err)
			_ = encoder.Encode(responseEnvelope{V: 1, Type: "error", RequestID: requestID, OK: false, Code: code, Message: err.Error(), Retryable: retryable, InstanceID: e.descriptor.InstanceID, OldestEventSeq: oldest})
			flusher.Flush()
			_ = status
			return
		}
		if err := encoder.Encode(eventRecordFrom(event, requestID)); err != nil {
			return
		}
		flusher.Flush()
	}
}

type sendRequest struct {
	V         int    `json:"v"`
	MessageID string `json:"message_id"`
	Body      string `json:"body"`
	// Source is optional (§6.1): "agent-control" (the default, used by ctl
	// send) or "human-operator" (the observing TUI). Any other value is
	// rejected so a typo cannot silently mislabel a message's origin.
	Source string `json:"source,omitempty"`
	// RequireInboxEmpty (§3.1) makes the send fail with INBOX_NOT_EMPTY while
	// the other role has messages this role has not consumed with wait. Absent
	// means false, so a client written before the guard behaves as always;
	// human-operator sends ignore it.
	RequireInboxEmpty bool `json:"require_inbox_empty,omitempty"`
}

func resolveSendSource(raw string) (string, bool) {
	switch raw {
	case "":
		return protocol.SourceAgentControl, true
	case protocol.SourceAgentControl, protocol.SourceHumanOperator:
		return raw, true
	default:
		return "", false
	}
}

func (e *Endpoint) handleSend(w http.ResponseWriter, r *http.Request) {
	requestID, ok := e.authorize(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, requestID, "INVALID_METHOD", "send requires POST", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	limited := http.MaxBytesReader(w, r.Body, protocol.MaxBodyBytes+64*1024)
	decoder := json.NewDecoder(limited)
	var request sendRequest
	if err := decoder.Decode(&request); err != nil {
		writeError(w, requestID, "INVALID_JSON", "send body must be one JSON object", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, requestID, "INVALID_JSON", "send body must contain exactly one JSON object", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	if request.V != 1 || strings.TrimSpace(request.MessageID) == "" {
		writeError(w, requestID, "MESSAGE_INVALID", "v=1 and message_id are required", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	source, ok := resolveSendSource(request.Source)
	if !ok {
		writeError(w, requestID, "MESSAGE_INVALID", fmt.Sprintf("unsupported source %q", request.Source), false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	envelope, blocked, err := e.publishSend(request, source)
	if blocked != nil {
		_ = writeJSON(w, http.StatusConflict, responseEnvelope{V: 1, Type: "error", RequestID: requestID, OK: false, Code: "INBOX_NOT_EMPTY", Message: blocked.message(), InstanceID: e.descriptor.InstanceID, Unread: blocked.unread})
		return
	}
	if err != nil {
		e.writeBridgeError(w, requestID, err)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, responseEnvelope{V: 1, Type: "response", RequestID: requestID, OK: true, Operation: "send", Status: "queued", InstanceID: e.descriptor.InstanceID, MessageID: envelope.MessageID, ClientSeq: envelope.ClientSeq, ServerSeq: envelope.ServerSeq, QueuedEventSeq: e.client.EventHub().LatestEventSeq()})
}

const (
	defaultWaitTimeout = 5 * time.Minute
	maxWaitTimeout     = 30 * time.Minute
)

func parseWaitTimeout(r *http.Request) (time.Duration, error) {
	value := r.URL.Query().Get("timeout_ms")
	if value == "" {
		return defaultWaitTimeout, nil
	}
	ms, err := strconv.ParseInt(value, 10, 64)
	if err != nil || ms < 1 || time.Duration(ms)*time.Millisecond > maxWaitTimeout {
		return 0, errors.New("timeout_ms must be between 1 and 1800000")
	}
	return time.Duration(ms) * time.Millisecond, nil
}

// handleWait returns the next peer message this role has not consumed yet.
// The cursor advances and the message is ACKed only after the response was
// flushed to a caller that is still connected; otherwise the next wait
// delivers the same message again (at-least-once).
func (e *Endpoint) handleWait(w http.ResponseWriter, r *http.Request) {
	requestID, ok := e.authorize(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, requestID, "INVALID_METHOD", "wait requires POST", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	timeout, err := parseWaitTimeout(r)
	if err != nil {
		writeError(w, requestID, "INVALID_JSON", err.Error(), false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	if !e.waitMu.TryLock() {
		writeError(w, requestID, "WAIT_IN_PROGRESS", "another wait is active for this role", false, http.StatusConflict, e.descriptor.InstanceID, 0)
		return
	}
	defer e.waitMu.Unlock()
	// Only the orchestrator side's presence counts toward activity (§5.1): if
	// the orchestrator vanishes, an executor's own wait must not keep a
	// --idle-timeout instance alive forever.
	if e.descriptor.LocalRole == protocol.RoleOrchestrator {
		e.activity.Enter()
		defer e.activity.Leave()
	}
	e.waiting.Store(true)
	defer e.waiting.Store(false)
	e.roles.WaitStart(e.descriptor.LocalRole)
	defer e.roles.WaitEnd(e.descriptor.LocalRole)

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	event, err := e.nextPeerMessage(ctx)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			_ = writeJSON(w, http.StatusOK, waitResponse{V: 1, Type: "response", RequestID: requestID, OK: true, Operation: "wait", Status: "timeout", InstanceID: e.descriptor.InstanceID})
			return
		}
		var cursorErr *bridge.CursorExpiredError
		if errors.As(err, &cursorErr) && cursorErr.OldestEventSeq > 0 {
			// Report the gap once; the next wait continues from what remains.
			e.setConsumed(cursorErr.OldestEventSeq - 1)
		}
		e.writeBridgeError(w, requestID, err)
		return
	}
	if err := writeJSON(w, http.StatusOK, waitResponse{V: 1, Type: "response", RequestID: requestID, OK: true, Operation: "wait", Status: "message", InstanceID: e.descriptor.InstanceID, EventSeq: event.EventSeq, Message: event.Envelope}); err != nil {
		return
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	if r.Context().Err() != nil {
		return
	}
	e.cursorMu.Lock()
	e.consumed = event.EventSeq
	if event.Envelope != nil && bodyLabel(event.Envelope.Body) == "FIN" {
		e.finReceived = true
	}
	e.cursorMu.Unlock()
	_ = e.client.Ack(event.MessageID, event.ServerSeq)
}

func (e *Endpoint) isPeerMessage(event bridge.Event) bool {
	return event.Kind == bridge.EventMessage && event.Envelope != nil && event.ServerSeq > 0 && event.Envelope.SenderRole != e.client.Role()
}

// nextPeerMessage scans the retained journal after the consumed cursor and,
// if nothing is pending, subscribes from the last scanned event so no event
// published between the scan and the subscription is missed.
func (e *Endpoint) nextPeerMessage(ctx context.Context) (bridge.Event, error) {
	after := e.loadConsumed()
	for {
		events, next, more, err := e.client.ReadEvents(after, 1000)
		if err != nil {
			return bridge.Event{}, err
		}
		for _, event := range events {
			if e.isPeerMessage(event) {
				return event, nil
			}
		}
		after = next
		if !more {
			break
		}
	}
	sub, err := e.client.Subscribe(after)
	if err != nil {
		return bridge.Event{}, err
	}
	defer sub.Close()
	for {
		event, err := sub.Next(ctx)
		if err != nil {
			return bridge.Event{}, err
		}
		if e.isPeerMessage(event) {
			return event, nil
		}
	}
}

func (e *Endpoint) writeBridgeError(w http.ResponseWriter, requestID string, err error) {
	code, status, retryable, oldest := bridgeError(err)
	writeError(w, requestID, code, err.Error(), retryable, status, e.descriptor.InstanceID, oldest)
}

func bridgeError(err error) (string, int, bool, uint64) {
	var cursorErr *bridge.CursorExpiredError
	if errors.As(err, &cursorErr) {
		return "CURSOR_EXPIRED", http.StatusGone, false, cursorErr.OldestEventSeq
	}
	if errors.Is(err, bridge.ErrControlBackpressure) {
		return "CONTROL_BACKPRESSURE", http.StatusTooManyRequests, true, 0
	}
	if errors.Is(err, bridge.ErrClosed) {
		return "INSTANCE_CLOSED", http.StatusGone, false, 0
	}
	if strings.Contains(err.Error(), "message_id already exists") {
		return "ID_CONFLICT", http.StatusConflict, false, 0
	}
	if strings.Contains(err.Error(), "body exceeds") {
		return "MESSAGE_INVALID", http.StatusRequestEntityTooLarge, false, 0
	}
	if strings.Contains(err.Error(), "queue limit") || strings.Contains(err.Error(), "memory limit") {
		return "CONTROL_BACKPRESSURE", http.StatusTooManyRequests, true, 0
	}
	return "TRANSPORT_ERROR", http.StatusServiceUnavailable, true, 0
}

// consumed is the RAM cursor of the last peer message this role received
// through wait. It is guarded by cursorMu, a short lock held only for a load,
// a store, or (send guard) the unread check plus the publication that
// depends on it. It is deliberately not waitMu, which a wait holds for up to
// 30 minutes.
func (e *Endpoint) hasReceivedFIN() bool {
	e.cursorMu.Lock()
	defer e.cursorMu.Unlock()
	return e.finReceived
}

func (e *Endpoint) loadConsumed() uint64 {
	e.cursorMu.Lock()
	defer e.cursorMu.Unlock()
	return e.consumed
}

func (e *Endpoint) setConsumed(seq uint64) {
	e.cursorMu.Lock()
	e.consumed = seq
	e.cursorMu.Unlock()
}

// knownLabels are the first-line markers the control plane recognizes
// (mirrors the TUI's badges).
var knownLabels = map[string]bool{
	"TAREA": true, "PREGUNTA": true, "RESPUESTA": true, "RESULTADO": true,
	"FIN": true, "URGENTE": true, "PROGRESO": true,
}

func bodyLabel(body string) string {
	first, _, _ := strings.Cut(body, "\n")
	first = strings.TrimSuffix(first, "\r")
	if knownLabels[first] {
		return first
	}
	return ""
}

// inbox summarizes the peer messages after a cursor.
type inbox struct {
	unread      int
	urgent      bool
	latestLabel string
	latestID    string
}

func (i inbox) message() string {
	latest := i.latestLabel
	if latest == "" {
		latest = "sin etiqueta"
	}
	return fmt.Sprintf("hay %d mensaje(s) sin leer del otro rol (el más reciente: %s); léelos con ctl wait antes de enviar, o usa --force", i.unread, latest)
}

// unreadAfter counts the peer messages after cursor in the retained journal.
// If the cursor already fell out of the journal, the evicted messages are
// gone and only what remains is counted.
func (e *Endpoint) unreadAfter(cursor uint64) (inbox, error) {
	var sum inbox
	after := cursor
	for {
		events, next, more, err := e.client.ReadEvents(after, 1000)
		if err != nil {
			var expired *bridge.CursorExpiredError
			if errors.As(err, &expired) && expired.OldestEventSeq > 0 && after < expired.OldestEventSeq-1 {
				after = expired.OldestEventSeq - 1
				continue
			}
			return inbox{}, err
		}
		for _, event := range events {
			if !e.isPeerMessage(event) {
				continue
			}
			sum.unread++
			label := bodyLabel(event.Envelope.Body)
			if label == "URGENTE" {
				sum.urgent = true
			}
			sum.latestLabel, sum.latestID = label, event.Envelope.MessageID
		}
		after = next
		if !more {
			return sum, nil
		}
	}
}

// guardExempt: URGENTE and FIN are interruptions and closings, so they skip
// the inbox guard for both roles (otherwise an unread RESULTADO would stop
// the orchestrator from interrupting).
func guardExempt(body string) bool {
	label := bodyLabel(body)
	return label == "URGENTE" || label == "FIN"
}

// publishSend publishes a send request. With the guard on, the unread check
// and the publication run under cursorMu, the lock every advance of the wait
// cursor takes: a concurrent wait can therefore not consume (and so change
// the answer) between the check and the publish, and the check reads the
// cursor value the publish is justified by. A retry of an already accepted
// message_id skips the guard, since it publishes nothing new.
func (e *Endpoint) publishSend(request sendRequest, source string) (protocol.Envelope, *inbox, error) {
	if !request.RequireInboxEmpty || source == protocol.SourceHumanOperator || guardExempt(request.Body) {
		envelope, err := e.client.PublishWithIDSource(request.MessageID, request.Body, source)
		return envelope, nil, err
	}
	e.cursorMu.Lock()
	defer e.cursorMu.Unlock()
	if _, exists := e.client.EventHub().Envelope(request.MessageID); !exists {
		sum, err := e.unreadAfter(e.consumed)
		if err != nil {
			return protocol.Envelope{}, nil, err
		}
		if sum.unread > 0 {
			return protocol.Envelope{}, &sum, nil
		}
	}
	envelope, err := e.client.PublishWithIDSource(request.MessageID, request.Body, source)
	return envelope, nil, err
}

// handlePeek implements GET /v1/peek (§3.2): a read-only look at the unread
// peer messages. It never moves the cursor, never ACKs, and touches neither
// Activity nor the role registry, so it is not presence.
func (e *Endpoint) handlePeek(w http.ResponseWriter, r *http.Request) {
	requestID, ok := e.authorize(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, requestID, "INVALID_METHOD", "peek requires GET", false, http.StatusBadRequest, e.descriptor.InstanceID, 0)
		return
	}
	sum, err := e.unreadAfter(e.loadConsumed())
	if err != nil {
		e.writeBridgeError(w, requestID, err)
		return
	}
	_ = writeJSON(w, http.StatusOK, peekResponse{V: 1, Type: "response", RequestID: requestID, OK: true, Operation: "peek", InstanceID: e.descriptor.InstanceID, Unread: sum.unread, Urgent: sum.urgent, LatestLabel: sum.latestLabel, LatestMessageID: sum.latestID, FinReceived: e.hasReceivedFIN()})
}

// RoleSnapshots first replays new journal messages into the registry (so a
// message sent by any path, including a direct TUI, counts), then derives the
// state of every role this endpoint tracks. The direct host/join TUI reads it
// too, to fill its side panel without a control round trip.
func (e *Endpoint) RoleSnapshots() map[protocol.Role]RoleSnapshot {
	e.scanMu.Lock()
	after := e.scanned
	for {
		events, next, more, err := e.client.ReadEvents(after, 1000)
		if err != nil {
			var expired *bridge.CursorExpiredError
			if errors.As(err, &expired) && expired.OldestEventSeq > 0 && after < expired.OldestEventSeq-1 {
				after = expired.OldestEventSeq - 1
				continue
			}
			break
		}
		for _, event := range events {
			if event.Kind == bridge.EventMessage && event.Envelope != nil {
				e.roles.Message(event.Envelope.SenderRole, event.Envelope.CreatedAt)
			}
		}
		after = next
		if !more {
			break
		}
	}
	e.scanned = after
	e.scanMu.Unlock()
	return e.roles.Snapshot()
}
