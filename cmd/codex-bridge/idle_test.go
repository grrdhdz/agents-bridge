package main

import (
	"testing"
	"time"
)

// TestIdleTimeoutClosesLocalWithoutActivity covers §10.5: with a short
// --idle-timeout and nothing happening, local closes itself like Ctrl+C.
func TestIdleTimeoutClosesLocalWithoutActivity(t *testing.T) {
	run := startLocalWithIdleTimeout(t, 150*time.Millisecond)
	select {
	case <-run.done:
		if run.err != nil {
			t.Fatalf("local returned error after idle timeout: %v", run.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local did not close itself after the idle timeout elapsed")
	}
}

// TestIdleTimeoutOrchestratorWaitKeepsInstanceAlive covers §10.5: a wait in
// flight on the orchestrator endpoint counts as presence, so the instance
// must not close while it is running even past the idle timeout.
func TestIdleTimeoutOrchestratorWaitKeepsInstanceAlive(t *testing.T) {
	idleTimeout := 150 * time.Millisecond
	run := startLocalWithIdleTimeout(t, idleTimeout)
	instanceID := run.ready["instance_id"].(string)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// This wait blocks for well longer than idleTimeout; while it is in
		// flight the instance must stay up.
		run.ctlID("", instanceID, "wait", "--role", "orchestrator", "--timeout", "1s")
	}()

	select {
	case <-run.done:
		t.Fatal("local closed itself while an orchestrator wait was still in flight")
	case <-time.After(idleTimeout * 3):
	}
	<-done
	run.cancel()
}

// TestIdleTimeoutExecutorWaitDoesNotKeepInstanceAlive covers §10.5: an
// executor's own wait must never count as presence, or an orchestrator-less
// bridge would live forever.
func TestIdleTimeoutExecutorWaitDoesNotKeepInstanceAlive(t *testing.T) {
	idleTimeout := 150 * time.Millisecond
	run := startLocalWithIdleTimeout(t, idleTimeout)
	instanceID := run.ready["instance_id"].(string)

	go run.ctlID("", instanceID, "wait", "--role", "executor", "--timeout", "5s")

	select {
	case <-run.done:
		if run.err != nil {
			t.Fatalf("local returned error after idle timeout: %v", run.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an in-flight executor wait must not keep the instance alive past --idle-timeout")
	}
}

// TestIdleTimeoutMessageResetsClock covers §10.5: publishing a message resets
// the idle clock, so the instance survives longer than idleTimeout when
// traffic keeps arriving, and only closes once it truly stops.
func TestIdleTimeoutMessageResetsClock(t *testing.T) {
	idleTimeout := 200 * time.Millisecond
	run := startLocalWithIdleTimeout(t, idleTimeout)
	instanceID := run.ready["instance_id"].(string)

	deadline := time.Now().Add(idleTimeout * 4)
	for time.Now().Before(deadline) {
		code, _, stderr := run.ctlID("keep alive", instanceID, "send", "--role", "orchestrator", "--body-file", "-")
		if code != 0 {
			t.Fatalf("send failed while keeping the instance alive: %s", stderr)
		}
		select {
		case <-run.done:
			t.Fatal("local closed itself despite recent message activity")
		case <-time.After(idleTimeout / 3):
		}
	}

	// Now let it actually go idle.
	select {
	case <-run.done:
		if run.err != nil {
			t.Fatalf("local returned error after idle timeout: %v", run.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local did not close itself once messages stopped")
	}
}

// TestParseIdleTimeoutFlag covers the small flag parser shared by `local`
// and the host command.
func TestParseIdleTimeoutFlag(t *testing.T) {
	got, err := parseIdleTimeoutFlag("codex-bridge local", []string{"--idle-timeout", "45s"}, defaultLocalIdleTimeout)
	if err != nil || got != 45*time.Second {
		t.Fatalf("expected 45s, got %v err=%v", got, err)
	}
	got, err = parseIdleTimeoutFlag("codex-bridge local", nil, defaultLocalIdleTimeout)
	if err != nil || got != defaultLocalIdleTimeout {
		t.Fatalf("expected default %v, got %v err=%v", defaultLocalIdleTimeout, got, err)
	}
	got, err = parseIdleTimeoutFlag("codex-bridge local", []string{"--idle-timeout", "0"}, defaultLocalIdleTimeout)
	if err != nil || got != 0 {
		t.Fatalf("0 should disable the idle timer, got %v err=%v", got, err)
	}
	if _, err := parseIdleTimeoutFlag("codex-bridge local", []string{"--idle-timeout", "-1s"}, defaultLocalIdleTimeout); err == nil {
		t.Fatal("negative idle timeout should be rejected")
	}
	if _, err := parseIdleTimeoutFlag("codex-bridge local", []string{"--bogus"}, defaultLocalIdleTimeout); err == nil {
		t.Fatal("unknown flag should be rejected")
	}
}

// TestIdleTimeoutOrchestratorWatchKeepsInstanceAlive covers §7.1: an
// observing TUI (the embedded one, or a standalone `codex-bridge tui`) holds
// its connection open with /v1/watch on the orchestrator endpoint, not
// /v1/wait. That must count as presence exactly like an orchestrator wait
// does, so --idle-timeout never closes a bridge someone is actively
// watching.
func TestIdleTimeoutOrchestratorWatchKeepsInstanceAlive(t *testing.T) {
	idleTimeout := 150 * time.Millisecond
	run := startLocalWithIdleTimeout(t, idleTimeout)
	instanceID := run.ready["instance_id"].(string)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Blocks until the instance itself closes (EOF on the watch
		// stream); while connected it must count as presence.
		run.ctlID("", instanceID, "watch", "--role", "orchestrator")
	}()

	select {
	case <-run.done:
		t.Fatal("local closed itself while an orchestrator watch was still connected")
	case <-time.After(idleTimeout * 3):
	}
	run.cancel()
	<-done
}
