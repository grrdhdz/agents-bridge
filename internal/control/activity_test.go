package control

import (
	"testing"
	"time"
)

func TestActivityTouchAdvancesLastActivity(t *testing.T) {
	a := NewActivity()
	first := a.LastActivity()
	time.Sleep(5 * time.Millisecond)
	a.Touch()
	if !a.LastActivity().After(first) {
		t.Fatalf("Touch should advance LastActivity: first=%v second=%v", first, a.LastActivity())
	}
}

func TestActivityPresenceReportsNowUntilLeave(t *testing.T) {
	a := NewActivity()
	stale := time.Now().Add(-time.Hour)
	a.mu.Lock()
	a.lastActivity = stale
	a.mu.Unlock()

	if !a.LastActivity().Equal(stale) {
		t.Fatalf("with no presence, LastActivity should report the recorded time")
	}
	a.Enter()
	if time.Since(a.LastActivity()) > time.Second {
		t.Fatalf("while present, LastActivity should report now, got %v", a.LastActivity())
	}
	a.Leave()
	if !a.LastActivity().Equal(stale) {
		t.Fatalf("after Leave, LastActivity should report the stale time again, got %v", a.LastActivity())
	}
}

func TestActivityLeaveWithoutEnterNeverGoesNegative(t *testing.T) {
	a := NewActivity()
	a.Leave()
	a.Leave()
	a.Enter()
	if time.Since(a.LastActivity()) > time.Second {
		t.Fatalf("presence should still work after unmatched Leave calls")
	}
	a.Leave()
	if time.Since(a.LastActivity()) > time.Second {
		t.Fatalf("presence dropped below zero and stopped tracking correctly")
	}
}

func TestActivityNilIsSafe(t *testing.T) {
	var a *Activity
	a.Touch()
	a.Enter()
	a.Leave()
	if !a.LastActivity().IsZero() {
		t.Fatalf("nil Activity should report zero time, got %v", a.LastActivity())
	}
}
