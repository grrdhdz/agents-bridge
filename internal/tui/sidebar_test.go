package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/internal/protocol"
	"github.com/grrdhdz/agents-bridge/internal/tui/theme"
)

func TestProjectNameTakesBaseOfCwd(t *testing.T) {
	cases := map[string]string{
		"/Users/x/proyectos/agents-bridge": "agents-bridge",
		"":                                 "",
		"/":                                "",
		".":                                "",
	}
	for in, want := range cases {
		if got := projectName(in); got != want {
			t.Fatalf("projectName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSidebarShowsPendingOnlyForLocalRoleAndHighlightsStale covers §6.4:
// only this side's own undelivered messages count as "pendientes", and one
// older than 5 minutes is highlighted.
func TestSidebarShowsPendingOnlyForLocalRoleAndHighlightsStale(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	data := sidebarData{
		InstanceID: "instance-abc",
		Mode:       "host",
		Pending: []pendingItem{
			{messageID: "fresh-message-id", status: "accepted", age: 1 * time.Minute},
			{messageID: "stale-message-id", status: "accepted", age: 6 * time.Minute},
		},
		Totals: map[protocol.Role]int{protocol.RoleOrchestrator: 3, protocol.RoleExecutor: 2},
	}
	_ = now
	out := renderSidebar(data, th, sidebarWidth)
	if !strings.Contains(out, "PENDIENTES") {
		t.Fatalf("expected a PENDIENTES section: %q", out)
	}
	if !strings.Contains(out, "Orquestador: 3") || !strings.Contains(out, "Ejecutor: 2") {
		t.Fatalf("expected totals per role: %q", out)
	}
	// The stale entry should carry the notice style (an ANSI escape absent
	// from the fresh entry's own line).
	lines := strings.Split(out, "\n")
	var freshLine, staleLine string
	for _, l := range lines {
		if strings.Contains(l, "fresh-me") {
			freshLine = l
		}
		if strings.Contains(l, "stale-me") {
			staleLine = l
		}
	}
	if freshLine == "" || staleLine == "" {
		t.Fatalf("expected both pending entries rendered, got %q", out)
	}
	if !strings.Contains(staleLine, "\x1b[") {
		t.Fatalf("stale (>5min) entry should be highlighted: %q", staleLine)
	}
}

// TestBuildSidebarDataCountsOnlyLocalRolePending covers Model's own
// extraction: a peer message never counts as "pending" (only this side's
// deliveries matter), and totals count every message regardless of role.
func TestBuildSidebarDataCountsOnlyLocalRolePending(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	own, err := protocol.NewEnvelope("instance-a", "own-1", 1, protocol.ExpectedSenderID(protocol.RoleOrchestrator), protocol.RoleOrchestrator, "mío", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: own.MessageID, Envelope: &own})
	model.statuses[own.MessageID] = "accepted"

	peer, err := protocol.NewEnvelope("instance-a", "peer-1", 2, "peer", protocol.RoleExecutor, "suyo", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: peer.MessageID, Envelope: &peer})

	data := model.buildSidebarData()
	if len(data.Pending) != 1 || data.Pending[0].messageID != "own-1" {
		t.Fatalf("expected exactly one pending entry (the local role's own undelivered message), got %+v", data.Pending)
	}
	if data.Totals[protocol.RoleOrchestrator] != 1 || data.Totals[protocol.RoleExecutor] != 1 {
		t.Fatalf("expected totals of 1/1, got %+v", data.Totals)
	}
}

func TestSidebarDataPendingIgnoresDelivered(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	for i := 0; i < 2; i++ {
		e, err := protocol.NewEnvelope("instance-a", "m-"+strconv.Itoa(i), uint64(i+1), protocol.ExpectedSenderID(protocol.RoleOrchestrator), protocol.RoleOrchestrator, "x", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	}
	model.statuses["m-0"] = "delivered"
	model.statuses["m-1"] = "accepted"
	data := model.buildSidebarData()
	if len(data.Pending) != 1 || data.Pending[0].messageID != "m-1" {
		t.Fatalf("a delivered message should not count as pending, got %+v", data.Pending)
	}
}
