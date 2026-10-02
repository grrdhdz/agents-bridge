package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
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
	return control.NewSubscription(t.descriptor, afterEventSeq), nil
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
		PeerConnected bool                            `json:"peer_connected"`
		Roles         map[string]control.RoleSnapshot `json:"roles"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return BridgeStatus{}, err
	}
	status := BridgeStatus{PeerConnected: record.PeerConnected, StartedAt: t.descriptor.StartedAt, Cwd: t.descriptor.CWD}
	for key, snap := range record.Roles {
		if role, ok := control.RoleFromKey(key); ok {
			if status.Roles == nil {
				status.Roles = make(map[protocol.Role]control.RoleSnapshot, len(record.Roles))
			}
			status.Roles[role] = snap
		}
	}
	return status, nil
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

// Retain the adapter names used by the TUI mapping tests. All observation
// and reconnection logic is shared with the stdio API in control.
type controlWatchRecord = control.WatchRecord

func frameFromWatchRecord(r controlWatchRecord) (protocol.Frame, bool) {
	return control.FrameFromWatchRecord(r)
}
