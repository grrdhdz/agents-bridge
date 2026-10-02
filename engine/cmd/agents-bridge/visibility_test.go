package main

import (
	"strings"
	"testing"
	"time"
)

func TestPSHookVisibility(t *testing.T) {
	beat := time.Date(2026, 10, 1, 12, 34, 56, 0, time.UTC)
	text := formatHeartbeatRole(psRoleState{State: "trabajando", Tool: "Bash", HookBound: true, LastHeartbeatAt: &beat})
	for _, want := range []string{"Bash", "vinculado por hook", "latido", "12:34:56"} {
		if !strings.Contains(text, want) {
			t.Fatal(text, want)
		}
	}
}
