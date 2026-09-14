// Package protocol defines the wire format used by one ephemeral bridge
// instance. It deliberately contains no persistence or project discovery.
package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Version      = 1
	MaxBodyBytes = 256 * 1024
	// MaxFrameBytes accounts for JSON escaping every body byte as a six-byte
	// unicode escape, plus the envelope and frame metadata.
	MaxFrameBytes      = MaxBodyBytes*6 + 256*1024
	MaxHistoryMessages = 1000
	MaxHistoryBytes    = 8 * 1024 * 1024
)

type Role string

const (
	RoleOrchestrator Role = "mac-orchestrator"
	RoleExecutor     Role = "win-executor"
)

type FrameType string

const (
	FrameHello          FrameType = "hello"
	FrameWelcome        FrameType = "welcome"
	FramePublish        FrameType = "publish"
	FrameMessage        FrameType = "message"
	FrameAccepted       FrameType = "accepted"
	FrameDelivered      FrameType = "delivered"
	FrameAck            FrameType = "ack"
	FrameAckConfirmed   FrameType = "ack_confirmed"
	FramePing           FrameType = "ping"
	FramePong           FrameType = "pong"
	FrameError          FrameType = "error"
	FrameClose          FrameType = "close"
	FrameTransportError FrameType = "transport_error"
)

type State string

const (
	StateRunning State = "running"
	StateClosed  State = "closed"
)

// Frame is a newline-delimited JSON envelope. A frame carries at most one
// payload field; unused fields are omitted to keep the protocol readable.
type Frame struct {
	Type            FrameType `json:"type"`
	ProtocolVersion int       `json:"protocol_version,omitempty"`
	InstanceID      string    `json:"instance_id,omitempty"`
	Role            Role      `json:"role,omitempty"`
	SenderID        string    `json:"sender_id,omitempty"`
	Token           string    `json:"token,omitempty"`
	LastServerSeq   uint64    `json:"last_server_seq,omitempty"`
	ReconnectToken  string    `json:"reconnect_token,omitempty"`
	State           State     `json:"state,omitempty"`
	CurrentSeq      uint64    `json:"current_seq,omitempty"`
	Envelope        *Envelope `json:"envelope,omitempty"`
	MessageID       string    `json:"message_id,omitempty"`
	ServerSeq       uint64    `json:"server_seq,omitempty"`
	Duplicate       bool      `json:"duplicate,omitempty"`
	Code            string    `json:"code,omitempty"`
	Detail          string    `json:"detail,omitempty"`
	LimitMessages   int       `json:"limit_messages,omitempty"`
	LimitBytes      int       `json:"limit_bytes,omitempty"`
	PingID          string    `json:"ping_id,omitempty"`
}

// Envelope is the canonical message. Body is preserved as UTF-8 text; UI
// metadata such as alignment and delivery labels is never added to Body.
type Envelope struct {
	ProtocolVersion int       `json:"protocol_version"`
	InstanceID      string    `json:"instance_id"`
	MessageID       string    `json:"message_id"`
	ClientSeq       uint64    `json:"client_seq"`
	ServerSeq       uint64    `json:"server_seq"`
	SenderID        string    `json:"sender_id"`
	SenderRole      Role      `json:"sender_role"`
	Kind            string    `json:"kind"`
	Body            string    `json:"body"`
	BodySHA256      string    `json:"body_sha256"`
	Source          string    `json:"source"`
	CreatedAt       time.Time `json:"created_at"`
	AcceptedAt      time.Time `json:"accepted_at"`
}

func NewEnvelope(instanceID, messageID string, clientSeq uint64, senderID string, senderRole Role, body string, now time.Time) (Envelope, error) {
	e := Envelope{
		ProtocolVersion: Version,
		InstanceID:      instanceID,
		MessageID:       messageID,
		ClientSeq:       clientSeq,
		SenderID:        senderID,
		SenderRole:      senderRole,
		Kind:            "chat",
		Body:            body,
		Source:          "manual-codex-copy",
		CreatedAt:       now.UTC(),
	}
	e.BodySHA256 = HashBody(body)
	if err := ValidateEnvelope(e); err != nil {
		return Envelope{}, err
	}
	return e, nil
}

func HashBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func ValidateEnvelope(e Envelope) error {
	if e.ProtocolVersion != Version {
		return fmt.Errorf("unsupported protocol version %d", e.ProtocolVersion)
	}
	if strings.TrimSpace(e.InstanceID) == "" {
		return errors.New("instance_id is required")
	}
	if strings.TrimSpace(e.MessageID) == "" {
		return errors.New("message_id is required")
	}
	if strings.TrimSpace(e.SenderID) == "" {
		return errors.New("sender_id is required")
	}
	if e.SenderRole != RoleOrchestrator && e.SenderRole != RoleExecutor {
		return fmt.Errorf("unsupported sender role %q", e.SenderRole)
	}
	if e.Kind != "chat" && e.Kind != "system" {
		return fmt.Errorf("unsupported message kind %q", e.Kind)
	}
	if !utf8.ValidString(e.Body) {
		return errors.New("body must be valid UTF-8")
	}
	if len([]byte(e.Body)) > MaxBodyBytes {
		return fmt.Errorf("body exceeds %d bytes", MaxBodyBytes)
	}
	if e.BodySHA256 != HashBody(e.Body) {
		return errors.New("body_sha256 does not match body")
	}
	return nil
}

func ExpectedSenderID(role Role) string {
	if role == RoleOrchestrator {
		return "mac-orchestrator"
	}
	return "win-executor"
}
