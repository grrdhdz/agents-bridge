package tui

import (
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"testing"
)

func TestRoleStateTextIncludesHeartbeatTool(t *testing.T) {
	roles := map[protocol.Role]control.RoleSnapshot{protocol.RoleExecutor: {State: control.StateWorking, Tool: "Bash"}}
	if got := roleStateText(roles, protocol.RoleExecutor); got != "trabajando (Bash)" {
		t.Fatalf("tool not visible: %q", got)
	}
}
