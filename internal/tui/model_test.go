package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

func TestRenderMessagesUsesRelativeSenderAndPreservesBody(t *testing.T) {
	local, err := protocol.NewEnvelope("instance-a", "local", 1, "mac-orchestrator", protocol.RoleOrchestrator, "Mac\nreporte exacto", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	remote, err := protocol.NewEnvelope("instance-a", "remote", 1, "win-executor", protocol.RoleExecutor, "Windows\nresultado", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderMessages([]protocol.Envelope{local, remote}, map[string]string{"local": "accepted", "remote": "delivered"}, protocol.RoleOrchestrator, 80)
	for _, part := range []string{"Tú", "Windows", "Mac", "reporte exacto", "resultado"} {
		if !strings.Contains(rendered, part) {
			t.Fatalf("rendered view lost %q: %q", part, rendered)
		}
	}
	if strings.Index(rendered, "Tú · accepted") >= strings.Index(rendered, "Windows · delivered") {
		t.Fatal("expected messages to remain in server order")
	}
}

func TestRenderMessagesNamesRemoteMacFromWindowsPerspective(t *testing.T) {
	local, err := protocol.NewEnvelope("instance-a", "local", 1, "win-executor", protocol.RoleExecutor, "resultado", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	remote, err := protocol.NewEnvelope("instance-a", "remote", 1, "mac-orchestrator", protocol.RoleOrchestrator, "reporte", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderMessages([]protocol.Envelope{remote, local}, map[string]string{"remote": "delivered", "local": "accepted"}, protocol.RoleExecutor, 80)
	if !strings.Contains(rendered, "Mac · delivered") {
		t.Fatalf("Windows perspective should label remote as Mac: %q", rendered)
	}
	if !strings.Contains(rendered, "Tú · accepted") {
		t.Fatalf("Windows perspective should label local as Tú: %q", rendered)
	}
}
