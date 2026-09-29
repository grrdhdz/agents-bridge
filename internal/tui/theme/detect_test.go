package theme

import (
	"testing"
	"time"
)

// TestDetectBackgroundNeverQueriesWithoutATerminal: with redirected stdin or
// stdout nothing can answer the query, so it must not even be attempted.
func TestDetectBackgroundNeverQueriesWithoutATerminal(t *testing.T) {
	queried := false
	if !detectBackground(false, time.Second, func() bool { queried = true; return false }) {
		t.Fatal("without a terminal the answer should be dark")
	}
	if queried {
		t.Fatal("the terminal must not be queried when stdin/stdout is not a terminal")
	}
}

// TestDetectBackgroundGivesUpOnATerminalThatNeverAnswers is the Windows hang
// of v0.3.0–v0.3.2: a query that blocks forever must not block the caller.
func TestDetectBackgroundGivesUpOnATerminalThatNeverAnswers(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	start := time.Now()
	dark := detectBackground(true, 50*time.Millisecond, func() bool { <-block; return false })
	if !dark {
		t.Fatal("a query that times out should answer dark")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("detectBackground blocked for %v", elapsed)
	}
}

func TestDetectBackgroundUsesTheTerminalsAnswer(t *testing.T) {
	if detectBackground(true, time.Second, func() bool { return false }) {
		t.Fatal("a light terminal should give a light theme")
	}
}

// TestDetectBackgroundPanicMeansDark: the previous recover() made a panic
// return false, i.e. the light theme, contrary to its own comment.
func TestDetectBackgroundPanicMeansDark(t *testing.T) {
	if !detectBackground(true, time.Second, func() bool { panic("boom") }) {
		t.Fatal("a panicking query should answer dark")
	}
}
