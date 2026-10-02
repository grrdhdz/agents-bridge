package tui

import (
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
	"os"
	"path/filepath"
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

func TestGoldenHookSidebar(t *testing.T) {
	m := goldenModel(t, 120, theme.ModeDark)
	beat := time.Date(2026, 9, 27, 11, 59, 58, 0, time.UTC)
	m.haveStatus = true
	m.status.Roles = map[protocol.Role]control.RoleSnapshot{protocol.RoleExecutor: {State: control.StateWorking, HookBound: true, LastHeartbeatAt: &beat, Tool: "shell"}}
	got := m.View().Content
	for _, want := range []string{"vinculado por hook", "latido 11:59:58Z", "shell"} {
		if !strings.Contains(got, want) {
			t.Fatal("view missing", want)
		}
	}
	path := filepath.Join("testdata", "w120_hook_dark.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatal("hook sidebar golden changed")
	}
}
