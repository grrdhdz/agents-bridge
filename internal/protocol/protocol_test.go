package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEnvelopePreservesBodyAndHash(t *testing.T) {
	body := "  reporte\n\ncon emojis 🚀  "
	e, err := NewEnvelope("instance-a", "message-a", 1, ExpectedSenderID(RoleOrchestrator), RoleOrchestrator, body, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if e.Body != body {
		t.Fatalf("body changed: %q", e.Body)
	}
	if e.BodySHA256 != HashBody(body) {
		t.Fatalf("unexpected body hash: %s", e.BodySHA256)
	}
}

func TestValidateEnvelopeRejectsOversizeAndTampering(t *testing.T) {
	e, err := NewEnvelope("instance-a", "message-a", 1, "win-executor", RoleExecutor, "ok", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e.BodySHA256 = "tampered"
	if err := ValidateEnvelope(e); err == nil {
		t.Fatal("expected hash mismatch")
	}

	e.BodySHA256 = HashBody(e.Body)
	e.Body = strings.Repeat("x", MaxBodyBytes+1)
	if err := ValidateEnvelope(e); err == nil {
		t.Fatal("expected size error")
	}
}

func TestMaxEscapedFrameFitsConfiguredLimit(t *testing.T) {
	body := strings.Repeat("\x00\x01\x02\x03\x04\x05\x06\x07\x08\n\r\t\\\"", MaxBodyBytes/15)
	body += strings.Repeat("\x01", MaxBodyBytes-len([]byte(body)))
	e, err := NewEnvelope("instance-a", "large", 1, ExpectedSenderID(RoleOrchestrator), RoleOrchestrator, body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(Frame{Type: FramePublish, InstanceID: "instance-a", Envelope: &e})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxFrameBytes {
		t.Fatalf("valid maximum body requires %d bytes, limit is %d", len(encoded), MaxFrameBytes)
	}
}
