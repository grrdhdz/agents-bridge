package tui

import (
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestSidebarHookVisibility(t *testing.T) {
	beat := time.Date(2026, 10, 1, 12, 34, 56, 0, time.UTC)
	text := roleStateText(map[protocol.Role]control.RoleSnapshot{protocol.RoleExecutor: {State: control.StateWorking, HookBound: true, LastHeartbeatAt: &beat, Tool: "shell"}}, protocol.RoleExecutor)
	for _, want := range []string{"shell", "vinculado por hook", "latido", "12:34:56"} {
		if !strings.Contains(text, want) {
			t.Fatal(text, want)
		}
	}
}
