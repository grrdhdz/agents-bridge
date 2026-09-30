package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

func TestSplitLabelRecognizesUrgenteAndProgreso(t *testing.T) {
	for _, label := range []string{"URGENTE", "PROGRESO"} {
		got, rest := splitLabel(label + "\ncuerpo")
		if got != label || rest != "cuerpo" {
			t.Fatalf("splitLabel(%s) = (%q, %q)", label, got, rest)
		}
	}
	if got, _ := splitLabel("urgente\nx"); got != "" {
		t.Fatalf("labels stay case-sensitive, got %q", got)
	}
}

func TestComposerLabelsIncludeUrgenteAndProgreso(t *testing.T) {
	have := map[string]bool{}
	for _, l := range composerLabels {
		have[l] = true
	}
	if !have["URGENTE"] || !have["PROGRESO"] {
		t.Fatalf("ctrl+t must rotate through URGENTE and PROGRESO: %v", composerLabels)
	}
}

func TestCardBadgesForUrgenteAndProgresoUseTheirOwnStyles(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	when := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, label := range []string{"URGENTE", "PROGRESO"} {
		e := newTestEnvelope(t, "m-"+label, protocol.RoleExecutor, label+"\ncuerpo de "+label, protocol.SourceAgentControl, when)
		card := renderCard(e, "accepted", th, 80, false)
		badge := th.LabelStyle(label).Render(label)
		if !strings.Contains(card, badge) {
			t.Fatalf("%s card should carry its styled badge %q:\n%q", label, badge, card)
		}
		// The label line is a badge, not body text.
		if strings.Count(stripANSI(card), label) != 2 { // badge + "cuerpo de LABEL"
			t.Fatalf("%s should appear once as the badge and once in the body:\n%s", label, stripANSI(card))
		}
	}
}

func TestSidebarShowsRoleStatesAndOmitsUnknownOnes(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	out := stripANSI(renderSidebar(sidebarData{
		InstanceID: "abc", Mode: "local",
		OrchestratorState: "trabajando", ExecutorState: "esperando",
		Totals: map[protocol.Role]int{},
	}, th, sidebarWidth))
	if !strings.Contains(out, "trabajando") || !strings.Contains(out, "esperando") {
		t.Fatalf("the panel should show each participant's state:\n%s", out)
	}
	bare := stripANSI(renderSidebar(sidebarData{InstanceID: "abc", Mode: "local", ExecutorState: "—", Totals: map[protocol.Role]int{}}, th, sidebarWidth))
	for _, word := range []string{"trabajando", "esperando", "callado"} {
		if strings.Contains(bare, word) {
			t.Fatalf("unknown states must not print %q:\n%s", word, bare)
		}
	}
}

func TestBuildSidebarDataTakesRoleStatesFromStatusPoll(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(statusResultMsg{status: BridgeStatus{PeerConnected: true, Roles: map[protocol.Role]control.RoleSnapshot{
		protocol.RoleOrchestrator: {State: control.StateWorking},
		protocol.RoleExecutor:     {State: control.StateQuiet},
	}}})
	data := updated.(*Model).buildSidebarData()
	if data.OrchestratorState != "trabajando" || data.ExecutorState != "callado" {
		t.Fatalf("sidebar states = %q / %q", data.OrchestratorState, data.ExecutorState)
	}
	// Host/join only know their own role: the other stays unset.
	own := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleExecutor, Capabilities: CapabilitiesForJoin()})
	updated, _ = own.Update(statusResultMsg{status: BridgeStatus{Roles: map[protocol.Role]control.RoleSnapshot{protocol.RoleExecutor: {State: control.StateWaiting}}}})
	data = updated.(*Model).buildSidebarData()
	if data.ExecutorState != "esperando" || data.OrchestratorState != "" {
		t.Fatalf("own-role-only status = %q / %q", data.OrchestratorState, data.ExecutorState)
	}
}

func TestControlTransportStatusReportsRoleStates(t *testing.T) {
	h := newObserverHarness(t)
	roles := control.NewRoles(nil, protocol.RoleOrchestrator, protocol.RoleExecutor)
	// Restart the owner endpoint with a registry shared between roles.
	h.ownerEndpoint.Close()
	endpoint, err := control.Start(h.owner, control.Options{Role: protocol.RoleOrchestrator, Mode: control.ModeLocal, Root: t.TempDir() + "/instances", Roles: roles})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(endpoint.Close)
	roles.WaitStart(protocol.RoleExecutor)

	status, err := NewControlTransport(endpoint.Descriptor()).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Roles[protocol.RoleExecutor].State != control.StateWaiting {
		t.Fatalf("executor state from /v1/health = %+v", status.Roles)
	}
	if _, ok := status.Roles[protocol.RoleOrchestrator]; !ok {
		t.Fatalf("local health reports both roles: %+v", status.Roles)
	}
}

func TestClientTransportStatusUsesRoleStatesCallback(t *testing.T) {
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	client, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	want := map[protocol.Role]control.RoleSnapshot{protocol.RoleOrchestrator: {State: control.StateWaiting}}
	status, err := newClientTransport(client, nil, func() map[protocol.Role]control.RoleSnapshot { return want }).Status(context.Background())
	if err != nil || status.Roles[protocol.RoleOrchestrator].State != control.StateWaiting {
		t.Fatalf("direct transport status = %+v err=%v", status, err)
	}
	status, _ = newClientTransport(client, nil, nil).Status(context.Background())
	if status.Roles != nil {
		t.Fatalf("no callback means no roles, got %+v", status.Roles)
	}
}

// goldenCoordModel is goldenModel's twin for the v0.4.0 additions: an
// URGENTE card from the orchestrator, a PROGRESO card from the executor and
// both roles' states in the side panel.
func goldenCoordModel(t *testing.T, width int, mode theme.Mode) *Model {
	t.Helper()
	fixedNow := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	model := New(Options{
		Transport:    &fakeTransport{instanceID: "instancia-golden-0002"},
		LocalRole:    protocol.RoleOrchestrator,
		Capabilities: CapabilitiesForHost(),
		Theme:        theme.New(mode, false, nil),
		Now:          func() time.Time { return fixedNow },
	})
	when := fixedNow.Add(-2 * time.Minute)
	specs := []struct {
		id     string
		role   protocol.Role
		body   string
		status string
	}{
		{"u1", protocol.RoleOrchestrator, "URGENTE\nDetente: cambió el alcance.", "delivered"},
		{"p1", protocol.RoleExecutor, "PROGRESO\nVan 3 de 5 pruebas en verde.", "accepted"},
	}
	for _, spec := range specs {
		e, err := protocol.NewEnvelopeWithSource("instancia-golden-0002", spec.id, 1, protocol.ExpectedSenderID(spec.role), spec.role, spec.body, protocol.SourceAgentControl, when)
		if err != nil {
			t.Fatal(err)
		}
		model.messages = append(model.messages, e)
		model.byID[e.MessageID] = len(model.messages) - 1
		model.statuses[e.MessageID] = spec.status
		when = when.Add(30 * time.Second)
	}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	updated, _ = updated.(*Model).Update(statusResultMsg{status: BridgeStatus{PeerConnected: true, Roles: map[protocol.Role]control.RoleSnapshot{
		protocol.RoleOrchestrator: {State: control.StateWaiting},
		protocol.RoleExecutor:     {State: control.StateWorking},
	}}})
	return updated.(*Model)
}

func TestGoldenCoordinationLabelsAndRoleStates(t *testing.T) {
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		name := "coordination_w120_" + string(mode)
		t.Run(name, func(t *testing.T) {
			out := goldenCoordModel(t, 120, mode).View().Content
			if out != goldenCoordModel(t, 120, mode).View().Content {
				t.Fatal("view is not deterministic")
			}
			compareGolden(t, name, out)
		})
	}
}

func stripANSI(s string) string { return ansi.Strip(s) }

// compareGolden mirrors the other golden tests: UPDATE_GOLDEN=1 rewrites the
// fixture, otherwise the output must match it byte for byte.
func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("missing golden fixture %s (run with UPDATE_GOLDEN=1 once): %v", golden, err)
	}
	if string(want) != got {
		t.Fatalf("view no longer matches %s; if intentional, rerun with UPDATE_GOLDEN=1", golden)
	}
}
