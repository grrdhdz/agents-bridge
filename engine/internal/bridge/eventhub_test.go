package bridge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

func testEventEnvelope(t *testing.T, id, body string, seq uint64) protocol.Envelope {
	t.Helper()
	e, err := protocol.NewEnvelope("instance-a", id, seq, protocol.ExpectedSenderID(protocol.RoleOrchestrator), protocol.RoleOrchestrator, body, time.Unix(int64(seq), 0))
	if err != nil {
		t.Fatal(err)
	}
	e.ServerSeq = seq
	return e
}

func TestEventHubUsesExclusiveEventCursorAndFansOut(t *testing.T) {
	hub := NewEventHub(EventHubOptions{MaxEvents: 32, MaxMetadataBytes: 1 << 20, MaxMessages: 32, MaxMessageBytes: 1 << 20, WatcherEvents: 8, WatcherBytes: 1 << 20})
	e := testEventEnvelope(t, "message-a", "hello", 1)
	hub.RememberEnvelope(e)
	first, created := hub.PublishFrame(protocol.Frame{Type: protocol.FrameMessage, InstanceID: e.InstanceID, Envelope: &e})
	if !created || first.EventSeq != 1 {
		t.Fatalf("unexpected first event: %+v, created=%v", first, created)
	}
	sub, err := hub.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	accepted, created := hub.PublishFrame(protocol.Frame{Type: protocol.FrameAccepted, InstanceID: e.InstanceID, MessageID: e.MessageID, ServerSeq: 1})
	if !created || accepted.EventSeq != 2 {
		t.Fatalf("unexpected accepted event: %+v, created=%v", accepted, created)
	}
	fromSub, err := sub.Next(context.Background())
	if err != nil || fromSub.EventSeq != first.EventSeq {
		t.Fatalf("subscriber lost first event: %+v, %v", fromSub, err)
	}
	fromSub, err = sub.Next(context.Background())
	if err != nil || fromSub.EventSeq != accepted.EventSeq {
		t.Fatalf("subscriber lost second event: %+v, %v", fromSub, err)
	}
	read, next, more, err := hub.Read(first.EventSeq, 10)
	if err != nil || len(read) != 1 || read[0].EventSeq != accepted.EventSeq || next != accepted.EventSeq || more {
		t.Fatalf("exclusive read cursor failed: events=%+v next=%d more=%v err=%v", read, next, more, err)
	}
}

func TestEventHubEvictsEnvelopeReferencesAndExpiresCursors(t *testing.T) {
	hub := NewEventHub(EventHubOptions{MaxEvents: 32, MaxMetadataBytes: 1 << 20, MaxMessages: 1, MaxMessageBytes: 64, WatcherEvents: 8, WatcherBytes: 1 << 20})
	first := testEventEnvelope(t, "message-a", "first", 1)
	hub.RememberEnvelope(first)
	firstEvent, _ := hub.PublishFrame(protocol.Frame{Type: protocol.FrameMessage, InstanceID: first.InstanceID, Envelope: &first})
	second := testEventEnvelope(t, "message-b", "second", 2)
	hub.RememberEnvelope(second)
	secondEvent, _ := hub.PublishFrame(protocol.Frame{Type: protocol.FrameMessage, InstanceID: second.InstanceID, Envelope: &second})
	if _, ok := hub.Envelope(first.MessageID); ok {
		t.Fatal("evicted body must not remain in the message journal")
	}
	if _, _, _, err := hub.Read(0, 10); err == nil {
		t.Fatal("cursor before coordinated eviction should expire")
	} else {
		var cursorErr *CursorExpiredError
		if !errors.As(err, &cursorErr) || cursorErr.OldestEventSeq != secondEvent.EventSeq {
			t.Fatalf("expected CURSOR_EXPIRED at second event, got %v", err)
		}
	}
	if _, err := hub.Subscribe(firstEvent.EventSeq - 1); err == nil {
		t.Fatal("watch cursor behind body eviction should expire")
	} else {
		var cursorErr *CursorExpiredError
		if !errors.As(err, &cursorErr) {
			t.Fatalf("expected cursor expiration, got %v", err)
		}
	}
}

func TestEventHubRepeatedStatusDoesNotConsumeEventSeq(t *testing.T) {
	hub := NewEventHub(EventHubOptions{MaxEvents: 32, MaxMetadataBytes: 1 << 20, MaxMessages: 32, MaxMessageBytes: 1 << 20, WatcherEvents: 8, WatcherBytes: 1 << 20})
	e := testEventEnvelope(t, "message-a", "hello", 1)
	hub.RememberEnvelope(e)
	hub.PublishFrame(protocol.Frame{Type: protocol.FrameMessage, InstanceID: e.InstanceID, Envelope: &e})
	accepted, created := hub.PublishFrame(protocol.Frame{Type: protocol.FrameAccepted, InstanceID: e.InstanceID, MessageID: e.MessageID, ServerSeq: e.ServerSeq})
	if !created {
		t.Fatal("first status should create an event")
	}
	repeated, created := hub.PublishFrame(protocol.Frame{Type: protocol.FrameAccepted, InstanceID: e.InstanceID, MessageID: e.MessageID, ServerSeq: e.ServerSeq})
	if created || repeated.EventSeq != accepted.EventSeq || hub.LatestEventSeq() != accepted.EventSeq {
		t.Fatalf("repeated status consumed event_seq: repeated=%+v created=%v latest=%d", repeated, created, hub.LatestEventSeq())
	}
}
