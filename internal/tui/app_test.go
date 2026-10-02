package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/internal/bridge"
	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

func newTUITestClient(t *testing.T) (*bridge.Server, *bridge.Client) {
	t.Helper()
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	client, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return server, client
}

// fakeTransport is a network-free Transport double for Model-level behavior
// that does not need a real endpoint: /stop confirmation, focus, history,
// labels and the security checks.
type fakeTransport struct {
	instanceID string
	published  []string
	closed     bool
}

func (f *fakeTransport) InstanceID() string              { return f.instanceID }
func (f *fakeTransport) Connected() bool                 { return true }
func (f *fakeTransport) Reconnect(context.Context) error { return nil }
func (f *fakeTransport) Ack(string, uint64) error        { return nil }
func (f *fakeTransport) QueueStats() (int, int)          { return 0, 0 }
func (f *fakeTransport) Close()                          { f.closed = true }
func (f *fakeTransport) Subscribe(uint64) (EventSubscription, error) {
	return nil, errors.New("not used in this test")
}

func (f *fakeTransport) Publish(body string) (protocol.Envelope, error) {
	f.published = append(f.published, body)
	return protocol.NewEnvelopeWithSource("instance-a", "id-"+strconv.Itoa(len(f.published)), uint64(len(f.published)), protocol.ExpectedSenderID(protocol.RoleOrchestrator), protocol.RoleOrchestrator, body, protocol.SourceHumanOperator, time.Now())
}

func TestModelStartsFocusedAndAcceptsImmediateInput(t *testing.T) {
	model := New(Options{Capabilities: CapabilitiesForHost(), Transport: &fakeTransport{instanceID: "abc"}})
	model.Init()

	updated, _ := model.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	got, ok := updated.(*Model)
	if !ok {
		t.Fatalf("expected pointer model after update, got %T", updated)
	}
	if !got.input.Focused() {
		t.Fatal("textarea should be focused after Init")
	}
	if got.input.Value() != "x" {
		t.Fatalf("expected immediate key input to be accepted, got %q", got.input.Value())
	}

	updated, _ = got.Update(tea.PasteMsg{Content: " línea 1\nlínea 2"})
	got = updated.(*Model)
	if got.input.Value() != "x línea 1\nlínea 2" {
		t.Fatalf("multiline paste was not preserved: %q", got.input.Value())
	}
}

func TestOrchestratorCopiesJoinCommandAndKeepsCredentialOutOfView(t *testing.T) {
	_, client := newTUITestClient(t)
	token := strings.Repeat("secret-token-", 4)
	joinCommand := "agents-bridge join --host mac.tailnet --port 4242 --instance instance-a --token " + token
	var copied []string
	model := New(Options{
		Client:       client,
		LocalRole:    protocol.RoleOrchestrator,
		JoinCommand:  joinCommand,
		Capabilities: CapabilitiesForHost(),
		CopyCommand: func(command string) error {
			copied = append(copied, command)
			return nil
		},
	})
	model.Init()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model = *updated.(*Model)
	viewModel := model.View()
	view := viewModel.Content
	if len(copied) != 1 || copied[0] != joinCommand {
		t.Fatalf("startup should copy the exact join command once, got %#v", copied)
	}
	if !strings.Contains(view, "agents-bridge") || !strings.Contains(view, "copiado") {
		t.Fatalf("status/copy instruction should be visible at 120x30: %q", view)
	}
	if strings.Contains(view, "--token") || strings.Contains(view, token) || strings.Contains(view, "mac.tailnet") {
		t.Fatalf("normal TUI view exposed pairing credentials: %q", view)
	}
	model.openHelp()
	if strings.Contains(model.View().Content, token) {
		t.Fatal("help overlay must never expose pairing credentials either")
	}
	model.showHelp = false
	if viewModel.MouseMode != tea.MouseModeCellMotion {
		t.Fatal("view should enable cell-motion mouse input")
	}
	if !viewModel.AltScreen {
		t.Fatal("every mode must use the alternate screen (spec §3)")
	}

	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyF5})
	model = *updated.(*Model)
	if len(copied) != 2 || copied[1] != joinCommand {
		t.Fatalf("F5 should recopy the exact join command, got %#v", copied)
	}
}

func TestJoinCannotPairAndF5DoesNothing(t *testing.T) {
	_, client := newTUITestClient(t)
	var copied []string
	model := New(Options{
		Client:       client,
		LocalRole:    protocol.RoleExecutor,
		Capabilities: CapabilitiesForJoin(),
		CopyCommand:  func(command string) error { copied = append(copied, command); return nil },
	})
	model.Init()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model = *updated.(*Model)
	view := model.View().Content
	if strings.Contains(view, "recopiar comando") {
		t.Fatalf("join's shortcuts bar should not advertise the host-only pair control: %q", view)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyF5})
	model = *updated.(*Model)
	if len(copied) != 0 {
		t.Fatal("F5 must do nothing when Capabilities.Pair is false")
	}
	model.command("/pair")
	if !strings.Contains(model.error, "PAIRING_FORBIDDEN") {
		t.Fatalf("join's /pair should be forbidden, got %q", model.error)
	}
}

func TestPairRegenerationRecopiesNewCommandAndCopyFailureHasFallback(t *testing.T) {
	_, client := newTUITestClient(t)
	oldCommand := "agents-bridge join --host mac.tailnet --port 4242 --instance instance-a --token old"
	newCommand := "agents-bridge join --host mac.tailnet --port 4242 --instance instance-a --token new"
	var copied []string
	model := New(Options{
		Client:       client,
		LocalRole:    protocol.RoleOrchestrator,
		JoinCommand:  oldCommand,
		Capabilities: CapabilitiesForHost(),
		CopyCommand:  func(command string) error { copied = append(copied, command); return nil },
		OnPair:       func() string { return newCommand },
	})
	model.Init()
	model.command("/pair")
	if len(copied) != 2 || copied[1] != newCommand {
		t.Fatalf("/pair should copy the fresh command, got %#v", copied)
	}

	failing := New(Options{
		Client:       client,
		LocalRole:    protocol.RoleOrchestrator,
		JoinCommand:  oldCommand,
		Capabilities: CapabilitiesForHost(),
		CopyCommand:  func(string) error { return errors.New("pbcopy unavailable") },
	})
	failing.Init()
	view := failing.View().Content
	if !strings.Contains(view, "No se pudo copiar") || !strings.Contains(view, "fallback") {
		t.Fatalf("copy failure should provide an actionable fallback: %q", view)
	}
	if strings.Contains(view, "--token") || strings.Contains(view, oldCommand) {
		t.Fatalf("copy failure view exposed credentials: %q", view)
	}
}

func TestHostAndJoinStopClosesImmediately(t *testing.T) {
	for _, caps := range []Capabilities{CapabilitiesForHost(), CapabilitiesForJoin()} {
		transport := &fakeTransport{instanceID: "abc"}
		stopCalls := 0
		model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: caps, OnStop: func() { stopCalls++ }})
		model.command("/stop")
		if stopCalls != 1 {
			t.Fatalf("%s: /stop should close on the first call, got %d calls", caps.Mode, stopCalls)
		}
		if model.state != "closed" || !transport.closed {
			t.Fatalf("%s: /stop should close the model and its transport", caps.Mode)
		}
	}
}

func TestLocalAndObserverStopRequiresSecondConfirmationAndNeverClosesItself(t *testing.T) {
	for _, caps := range []Capabilities{CapabilitiesForLocal(), CapabilitiesForObserver()} {
		transport := &fakeTransport{instanceID: "abc"}
		stopCalls := 0
		model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: caps, OnStop: func() { stopCalls++ }})

		model.command("/stop")
		if stopCalls != 0 {
			t.Fatalf("%s: first /stop should only ask for confirmation, got %d calls", caps.Mode, stopCalls)
		}
		if !strings.Contains(model.error, "confirma") {
			t.Fatalf("%s: expected a confirmation prompt, got %q", caps.Mode, model.error)
		}
		if model.state == "closed" {
			t.Fatalf("%s: first /stop must not close the model", caps.Mode)
		}

		model.command("/stop")
		if stopCalls != 1 {
			t.Fatalf("%s: second /stop should call OnStop exactly once, got %d calls", caps.Mode, stopCalls)
		}
		if model.state == "closed" || transport.closed {
			t.Fatalf("%s: /stop must not mark itself closed or close its own transport", caps.Mode)
		}
	}
}

func TestStopConfirmationResetsOnOtherCommand(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	stopCalls := 0
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForObserver(), OnStop: func() { stopCalls++ }})
	model.command("/stop")
	model.command("/status")
	model.command("/stop")
	if stopCalls != 0 {
		t.Fatalf("an intervening command should cancel the pending confirmation, got %d calls", stopCalls)
	}
}

func TestStandaloneObserverQuitLeavesBridgeRunning(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	stopCalls := 0
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForObserver(), OnStop: func() { stopCalls++ }})
	model.command("/quit")
	if stopCalls != 0 {
		t.Fatalf("standalone observer /quit must not stop the bridge, got %d OnStop calls", stopCalls)
	}
	if model.state != "closed" || !transport.closed {
		t.Fatal("standalone observer /quit should still close its own window and transport")
	}
}

func TestEmbeddedLocalQuitStopsBridge(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	stopCalls := 0
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForLocal(), OnStop: func() { stopCalls++ }})
	model.command("/quit")
	if stopCalls != 1 {
		t.Fatalf("embedded local /quit should stop the bridge once, got %d OnStop calls", stopCalls)
	}
}

func TestJoinQuitClosesItsOwnSideWithoutACallback(t *testing.T) {
	// join has CloseOnQuit=true but (like today) no OnStop callback of its
	// own: closing it must never panic on a nil OnStop, it just closes its
	// own transport (§4: "cerrar el puente al salir: sí (solo join)").
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleExecutor, Capabilities: CapabilitiesForJoin()})
	model.command("/quit")
	if model.state != "closed" || !transport.closed {
		t.Fatal("join /quit should close its own transport")
	}
}

func TestComposerLabelCyclesThroughFixedOrder(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	want := []string{"TAREA", "PREGUNTA", "RESPUESTA", "FIN", "URGENTE", "PROGRESO", ""}
	for _, label := range want {
		model.cycleLabel()
		if model.currentLabel() != label {
			t.Fatalf("expected label %q, got %q", label, model.currentLabel())
		}
	}
}

func TestSubmitPrependsCurrentLabelAndResetsAfterConsumingHistory(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	model.cycleLabel() // TAREA
	model.input.SetValue("revisa el PR")
	model.submit()
	if len(transport.published) != 1 || transport.published[0] != "TAREA\nrevisa el PR" {
		t.Fatalf("expected the label prepended to the body, got %#v", transport.published)
	}
	if len(model.history) != 1 || model.history[0] != "revisa el PR" {
		t.Fatalf("history should store the typed text without the label, got %#v", model.history)
	}
	if model.input.Value() != "" {
		t.Fatal("composer should reset after sending")
	}
}

func TestComposerHistoryNavigatesMostRecentFirstWhenEmpty(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	model.input.SetValue("primero")
	model.submit()
	model.input.SetValue("segundo")
	model.submit()

	model.historyPrev()
	if model.input.Value() != "segundo" {
		t.Fatalf("first history-up should recall the most recent send, got %q", model.input.Value())
	}
	model.historyPrev()
	if model.input.Value() != "primero" {
		t.Fatalf("second history-up should recall the one before, got %q", model.input.Value())
	}
	model.historyNext()
	if model.input.Value() != "segundo" {
		t.Fatalf("history-down should step forward again, got %q", model.input.Value())
	}
	model.historyNext()
	if model.input.Value() != "" {
		t.Fatalf("history-down past the newest entry should clear the composer, got %q", model.input.Value())
	}
}

func TestUnreadIndicatorAppearsWhenScrolledAwayFromBottom(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = *updated.(*Model)

	for i := 0; i < 20; i++ {
		e, err := protocol.NewEnvelope("instance-a", "m-"+strconv.Itoa(i), uint64(i+1), "peer", protocol.RoleExecutor, "línea "+strconv.Itoa(i), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	}
	// The viewport auto-follows while nothing has scrolled it away, so
	// scroll up manually before the next message arrives.
	model.viewport.GotoTop()
	e, err := protocol.NewEnvelope("instance-a", "late", 21, "peer", protocol.RoleExecutor, "tarde", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
	if model.unread == 0 {
		t.Fatal("a peer message arriving while scrolled up should increase the unread count")
	}
	view := model.View().Content
	if !strings.Contains(view, "mensajes nuevos") {
		t.Fatalf("view should show the unread indicator: %q", view)
	}

	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model = *updated.(*Model)
	updated, _ = model.Update(tea.KeyPressMsg{Text: "G", Code: 'G'})
	model = *updated.(*Model)
	if model.unread != 0 {
		t.Fatal("G (bottom) should clear the unread indicator")
	}
}

func TestFocusTabTogglesBetweenComposerAndConversation(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	if model.focus != focusComposer {
		t.Fatal("composer should have focus by default")
	}
	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model = *updated.(*Model)
	if model.focus != focusConversation {
		t.Fatal("tab should move focus to the conversation")
	}
	// While the conversation has focus, ordinary typing must not leak into
	// the composer.
	updated, _ = model.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	model = *updated.(*Model)
	if model.input.Value() != "" {
		t.Fatal("typing while the conversation has focus must not reach the composer")
	}
}

func TestHelpOverlayListsShortcutsAndIsDismissedByAnyKey(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	// Tall enough that the whole help listing fits without scrolling (see
	// TestHelpWindowScrollsWhenContentDoesNotFit for the scrolling case) —
	// this test is only about the *contents* and the dismiss-on-any-key
	// behavior.
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	model = *updated.(*Model)
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model = *updated.(*Model)
	updated, _ = model.Update(tea.KeyPressMsg{Text: "?", Code: '?'})
	model = *updated.(*Model)
	if !model.showHelp {
		t.Fatal("? while the conversation has focus should open help")
	}
	view := model.View().Content
	if !strings.Contains(view, "Composer") || !strings.Contains(view, "enviar") {
		t.Fatalf("help should list every context's bindings: %q", view)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = *updated.(*Model)
	if model.showHelp {
		t.Fatal("any key should dismiss the help overlay")
	}
}

// ackingTransport is a Transport double whose Ack actually records calls
// (unlike fakeTransport's no-op), to prove Model itself withholds the call
// when Capabilities.Ack is false rather than relying on the Transport
// implementation to behave (Part A §4: defense in depth).
type ackingTransport struct {
	fakeTransport
	ackCalls int
}

func (a *ackingTransport) Ack(string, uint64) error {
	a.ackCalls++
	return nil
}

// TestAckIsGovernedByCapabilitiesNotByTransport covers Part A §4: even
// wired to a Transport that would happily confirm a message, a mode whose
// Capabilities.Ack is false (local-embedded and standalone observers) must
// never call Ack, and a mode whose Capabilities.Ack is true must call it
// exactly once per peer message.
func TestAckIsGovernedByCapabilitiesNotByTransport(t *testing.T) {
	cases := []struct {
		name string
		caps Capabilities
		want int
	}{
		{"host acks", CapabilitiesForHost(), 1},
		{"join acks", CapabilitiesForJoin(), 1},
		{"local never acks", CapabilitiesForLocal(), 0},
		{"observer never acks", CapabilitiesForObserver(), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			transport := &ackingTransport{fakeTransport: fakeTransport{instanceID: "abc"}}
			model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: c.caps})
			e, err := protocol.NewEnvelope("instance-a", "peer-1", 1, "peer", protocol.RoleExecutor, "hola", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			model.handleFrame(protocol.Frame{Type: protocol.FrameMessage, MessageID: e.MessageID, Envelope: &e})
			if transport.ackCalls != c.want {
				t.Fatalf("%s: expected %d Ack calls, got %d", c.name, c.want, transport.ackCalls)
			}
		})
	}
}

func TestCompactModeDropsCardMargin(t *testing.T) {
	transport := &fakeTransport{instanceID: "abc"}
	model := New(Options{Transport: transport, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 50, Height: 24})
	model = *updated.(*Model)
	if !model.compact() {
		t.Fatal("50 columns should be compact (< 60)")
	}
	updated, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = *updated.(*Model)
	if model.compact() {
		t.Fatal("80 columns should not be compact")
	}
}
