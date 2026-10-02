package tui

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/internal/bridge"
	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

// blockingSub is an EventSubscription whose Next blocks until an event is
// pushed, its context ends or it is closed — like controlSubscription
// waiting on a quiet /v1/watch stream.
type blockingSub struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	events chan bridge.Event
}

func newBlockingSub() *blockingSub {
	return &blockingSub{done: make(chan struct{}), events: make(chan bridge.Event, 4)}
}

func (s *blockingSub) Next(ctx context.Context) (bridge.Event, error) {
	select {
	case ev := <-s.events:
		return ev, nil
	case <-ctx.Done():
		return bridge.Event{}, ctx.Err()
	case <-s.done:
		return bridge.Event{}, bridge.ErrClosed
	}
}

func (s *blockingSub) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
}

func (s *blockingSub) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

type subTransport struct {
	fakeTransport
	sub EventSubscription
}

func (t *subTransport) Subscribe(uint64) (EventSubscription, error) { return t.sub, nil }

func homeEnteredModel(t *testing.T, sub EventSubscription) (*Model, *subTransport) {
	t.Helper()
	tr := &subTransport{fakeTransport: fakeTransport{instanceID: "abc"}, sub: sub}
	m := New(Options{Transport: tr, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForObserver()})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return updated.(*Model), tr
}

func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	return cmd()
}

func escKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEscape} }

func TestEscReturnsHomeOnlyWithTheCapability(t *testing.T) {
	m, _ := homeEnteredModel(t, newBlockingSub())
	_, cmd := m.Update(escKey())
	if _, ok := runCmd(t, cmd).(returnHomeMsg); !ok {
		t.Fatal("esc with ReturnHome and no overlay should produce a returnHomeMsg")
	}

	direct := New(Options{Transport: &fakeTransport{instanceID: "abc"}, Capabilities: CapabilitiesForHost()})
	_, cmd = direct.Update(escKey())
	if _, ok := runCmd(t, cmd).(returnHomeMsg); ok {
		t.Fatal("without ReturnHome (entered with --instance-id, or host/join/local) esc must not return home")
	}
}

func TestEscClosesAnOverlayInsteadOfReturningHome(t *testing.T) {
	m, _ := homeEnteredModel(t, newBlockingSub())
	m.openPalette()
	_, cmd := m.Update(escKey())
	if m.paletteOpen {
		t.Fatal("esc should close the palette")
	}
	if _, ok := runCmd(t, cmd).(returnHomeMsg); ok {
		t.Fatal("esc that closes an overlay must not also return home")
	}
	m.openSearch()
	_, cmd = m.Update(escKey())
	if m.searchActive {
		t.Fatal("esc should close the search field")
	}
	if _, ok := runCmd(t, cmd).(returnHomeMsg); ok {
		t.Fatal("esc that cancels search must not also return home")
	}
}

func TestPaletteOffersReturnHomeOnlyWithTheCapability(t *testing.T) {
	has := func(m *Model) bool {
		for _, it := range m.paletteItems() {
			if it.id == "return-home" {
				return true
			}
		}
		return false
	}
	m, _ := homeEnteredModel(t, newBlockingSub())
	if !has(m) {
		t.Fatal("palette should offer \"Volver a inicio\" when ReturnHome is set")
	}
	plain := New(Options{Capabilities: CapabilitiesForHost()})
	if has(&plain) {
		t.Fatal("palette must not offer \"Volver a inicio\" without ReturnHome")
	}
	m.runPaletteItem("return-home")
	if m.pendingCmd == nil {
		t.Fatal("running the palette item should request the return")
	}
}

func TestClosedBridgeReturnsHomeWithNoticeInsteadOfQuitting(t *testing.T) {
	sub := newBlockingSub()
	m, _ := homeEnteredModel(t, sub)
	m.Init()
	closeEvent := bridge.Event{Kind: bridge.EventLifecycle, State: "closed", Frame: protocol.Frame{Type: protocol.FrameClose}}
	_, cmd := m.Update(eventMsg{event: closeEvent})
	msg, ok := runCmd(t, cmd).(returnHomeMsg)
	if !ok {
		t.Fatalf("a closed bridge entered from home should return home, not quit; got %#v", runCmd(t, cmd))
	}
	if msg.notice == "" {
		t.Fatal("returning home because the bridge closed should carry a notice")
	}

	direct := New(Options{Transport: &fakeTransport{instanceID: "abc"}, Capabilities: CapabilitiesForLocal()})
	direct.events = sub
	_, cmd = direct.Update(eventMsg{event: closeEvent})
	if _, isQuit := runCmd(t, cmd).(tea.QuitMsg); !isQuit {
		t.Fatal("without ReturnHome the current behavior (quit) must not change")
	}
}

func TestShutdownClosesSubscriptionAndUnblocksPendingWait(t *testing.T) {
	sub := newBlockingSub()
	m, tr := homeEnteredModel(t, sub)
	cmds := m.Init()
	_ = cmds
	waiter := waitForEvent(m.eventsCtx, sub, m.id)
	finished := make(chan tea.Msg, 1)
	go func() { finished <- waiter() }()
	select {
	case <-finished:
		t.Fatal("the wait should be blocked before shutdown")
	case <-time.After(50 * time.Millisecond):
	}
	m.Shutdown()
	select {
	case msg := <-finished:
		if em, ok := msg.(eventMsg); !ok || em.owner != m.id {
			t.Fatalf("the unblocked wait should report an owner-tagged eventMsg, got %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown must unblock the goroutine waiting on the subscription (leak)")
	}
	if !sub.isClosed() {
		t.Fatal("Shutdown must close the subscription")
	}
	if !tr.closed {
		t.Fatal("Shutdown must close the transport")
	}
	m.Shutdown() // idempotent
}

func TestMessagesFromAShutDownBridgeAreIgnoredByTheNextOne(t *testing.T) {
	first, _ := homeEnteredModel(t, newBlockingSub())
	second, _ := homeEnteredModel(t, newBlockingSub())
	second.Init()
	if first.id == second.id {
		t.Fatal("each Model needs its own identity")
	}
	closeEvent := bridge.Event{Kind: bridge.EventLifecycle, State: "closed", Frame: protocol.Frame{Type: protocol.FrameClose}}
	_, cmd := second.Update(eventMsg{event: closeEvent, owner: first.id})
	if cmd != nil {
		t.Fatal("an event tagged for another Model must be dropped without re-arming a wait")
	}
	if _, cmd = second.Update(reconnectTickMsg{owner: first.id}); cmd != nil {
		t.Fatal("a stale tick must not re-arm itself in the new Model")
	}
	if _, cmd = second.Update(statusPollTickMsg{owner: first.id}); cmd != nil {
		t.Fatal("a stale status tick must not re-arm itself in the new Model")
	}
}
