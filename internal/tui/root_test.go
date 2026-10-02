package tui

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/internal/bridge"
	"github.com/grrdhdz/agents-bridge/internal/bridges"
	"github.com/grrdhdz/agents-bridge/internal/protocol"
	"github.com/grrdhdz/agents-bridge/internal/tui/theme"
)

// trackedSession is a session whose transport and subscription record that
// they were released.
type trackedSession struct {
	transport *subTransport
	sub       *blockingSub
}

func newTrackedSource(t *testing.T) (*fakeSource, map[string]*trackedSession) {
	t.Helper()
	src := &fakeSource{}
	src.set(threeBridges()...)
	sessions := map[string]*trackedSession{}
	src.session = func(id string) BridgeSession {
		sub := newBlockingSub()
		tr := &subTransport{fakeTransport: fakeTransport{instanceID: id}, sub: sub}
		sessions[id] = &trackedSession{transport: tr, sub: sub}
		return BridgeSession{Transport: tr, LocalRole: protocol.RoleOrchestrator, OnStop: func() {}}
	}
	t.Cleanup(func() {
		for _, s := range sessions {
			s.sub.Close()
		}
	})
	return src, sessions
}

func newAppTest(t *testing.T, src *fakeSource) *App {
	t.Helper()
	a := NewApp(AppOptions{Source: src, Theme: theme.New(theme.ModeDark, false, nil), Now: func() time.Time { return homeClock }})
	app := &a
	app.home.tick = noTick
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	feedApp(t, app, app.Init())
	return app
}

func feedApp(t *testing.T, a *App, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var out []tea.Msg
	for _, msg := range collect(cmd) {
		out = append(out, msg)
		_, next := a.Update(msg)
		out = append(out, feedApp(t, a, next)...)
	}
	return out
}

func appView(a *App) string { return plainOf(a.View().Content) }

func plainOf(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case !inEsc:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func enterBridge(t *testing.T, a *App, id string) {
	t.Helper()
	_, cmd := a.Update(openBridgeMsg{id: id})
	if !a.OnBridge() {
		t.Fatal("openBridgeMsg should enter the bridge view")
	}
	_ = cmd
}

func TestAppStartsOnHomeAndEntersABridgeAsObserver(t *testing.T) {
	src, _ := newTrackedSource(t)
	a := newAppTest(t, src)
	if a.OnBridge() || !strings.Contains(appView(a), "3 puentes") {
		t.Fatal("the app starts on the home screen")
	}
	press2 := func(text string) { a.Update(tea.KeyPressMsg{Text: text, Code: []rune(text)[0]}) }
	press2("j")
	_, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	feedApp(t, a, cmd)
	if !a.OnBridge() {
		t.Fatal("enter on the home screen opens the selected bridge")
	}
	if len(src.opened) != 1 || src.opened[0] != "bbbb2222-0000" {
		t.Fatalf("expected the selected bridge to be opened, got %v", src.opened)
	}
	view := appView(a)
	if !strings.Contains(view, "observador") || !strings.Contains(view, "bbbb2222") {
		t.Fatalf("the bridge view (observer mode) should be showing:\n%s", view)
	}
	if !a.bridge.caps.ReturnHome || a.bridge.caps.CloseOnQuit || a.bridge.caps.Ack {
		t.Fatalf("a bridge entered from home is an observer that can return home: %+v", a.bridge.caps)
	}
}

func TestAppOpenFailureStaysOnHomeWithNotice(t *testing.T) {
	src, _ := newTrackedSource(t)
	src.openErr = errors.New("INSTANCE_NOT_FOUND")
	a := newAppTest(t, src)
	a.Update(openBridgeMsg{id: "aaaa1111-0000"})
	if a.OnBridge() {
		t.Fatal("a failed open must not enter anything")
	}
	if !strings.Contains(appView(a), "INSTANCE_NOT_FOUND") {
		t.Fatalf("the failure is reported:\n%s", appView(a))
	}
}

func TestAppEscReturnsHomeAndReleasesEverything(t *testing.T) {
	src, sessions := newTrackedSource(t)
	a := newAppTest(t, src)
	_, initCmd := a.Update(openBridgeMsg{id: "aaaa1111-0000"})
	_ = initCmd
	s := sessions["aaaa1111-0000"]
	before := src.listCalls

	_, cmd := a.Update(escKey())
	msgs := feedApp(t, a, cmd)
	if a.OnBridge() {
		t.Fatal("esc in a bridge entered from home returns to the list")
	}
	if !s.transport.closed || !s.sub.isClosed() {
		t.Fatal("leaving a bridge must close its transport and subscription")
	}
	if src.listCalls == before {
		t.Fatalf("coming home refreshes the list immediately (msgs %#v)", msgs)
	}
	if !strings.Contains(appView(a), "3 puentes") {
		t.Fatalf("the home screen is showing again:\n%s", appView(a))
	}
}

func TestAppPaletteReturnHome(t *testing.T) {
	src, sessions := newTrackedSource(t)
	a := newAppTest(t, src)
	a.Update(openBridgeMsg{id: "aaaa1111-0000"})
	a.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	for _, r := range "volver" {
		a.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	_, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	feedApp(t, a, cmd)
	if a.OnBridge() || !sessions["aaaa1111-0000"].sub.isClosed() {
		t.Fatal("the palette's Volver a inicio returns home and releases the bridge")
	}
}

func TestAppBridgeClosedUnderYouReturnsHomeWithANotice(t *testing.T) {
	src, sessions := newTrackedSource(t)
	a := newAppTest(t, src)
	a.Update(openBridgeMsg{id: "cccc3333-0000"})
	closeEvent := bridge.Event{Kind: bridge.EventLifecycle, State: "closed", Frame: protocol.Frame{Type: protocol.FrameClose}}
	_, cmd := a.Update(eventMsg{event: closeEvent, owner: a.bridge.id})
	feedApp(t, a, cmd)
	if a.OnBridge() {
		t.Fatal("a bridge closing under the observer sends them home instead of exiting the TUI")
	}
	if !strings.Contains(appView(a), "se cerró") {
		t.Fatalf("the notice explains why:\n%s", appView(a))
	}
	if !sessions["cccc3333-0000"].sub.isClosed() {
		t.Fatal("the closed bridge's subscription is released")
	}
}

func TestAppDropsWhatWasInFlightFromABridgeAlreadyLeft(t *testing.T) {
	src, _ := newTrackedSource(t)
	a := newAppTest(t, src)
	a.Update(openBridgeMsg{id: "aaaa1111-0000"})
	oldID := a.bridge.id
	_, cmd := a.Update(escKey())
	feedApp(t, a, cmd)
	for _, stale := range []tea.Msg{
		eventMsg{owner: oldID},
		reconnectTickMsg{owner: oldID},
		statusPollTickMsg{owner: oldID},
		statusResultMsg{owner: oldID},
	} {
		if _, next := a.Update(stale); next != nil {
			t.Fatalf("%T from a bridge already left must be dropped without re-arming anything", stale)
		}
	}
	// A different bridge entered next does not inherit the old chains either.
	a.Update(openBridgeMsg{id: "bbbb2222-0000"})
	if _, next := a.Update(reconnectTickMsg{owner: oldID}); next != nil {
		t.Fatal("a stale tick must not re-arm in the next bridge")
	}
}

func TestAppHomeRefreshChainStopsWhileABridgeIsOpen(t *testing.T) {
	src, _ := newTrackedSource(t)
	a := newAppTest(t, src)
	gen := a.home.tickGen
	a.Update(openBridgeMsg{id: "aaaa1111-0000"})
	before := src.listCalls
	if _, next := a.Update(homeTickMsg{gen: gen}); next != nil {
		t.Fatal("a home tick arriving while a bridge is open must die: no polling behind the bridge view")
	}
	if src.listCalls != before {
		t.Fatal("no listing while a bridge is open")
	}
}

func TestAppThemeChosenInABridgeSurvivesTheTripHome(t *testing.T) {
	src, _ := newTrackedSource(t)
	a := newAppTest(t, src)
	a.Update(openBridgeMsg{id: "aaaa1111-0000"})
	a.bridge.toggleTheme()
	_, cmd := a.Update(escKey())
	feedApp(t, a, cmd)
	if a.home.th.Mode != theme.ModeLight {
		t.Fatalf("the home screen adopts the theme chosen in the bridge, got %q", a.home.th.Mode)
	}
}

func TestAppCtrlCInABridgeQuitsTheWholeTUI(t *testing.T) {
	src, _ := newTrackedSource(t)
	a := newAppTest(t, src)
	a.Update(openBridgeMsg{id: "aaaa1111-0000"})
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if _, ok := collectOne(cmd).(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c quits from the bridge view too")
	}
	open := a.bridge
	a.Close()
	if a.bridge != nil || !open.eventsClosedOrNil() {
		t.Fatal("Close releases the open bridge")
	}
}

func TestAppExitNoticeListsOnlyCreatedBridgesStillAlive(t *testing.T) {
	src, _ := newTrackedSource(t)
	src.createID = "dddd4444-0000"
	a := newAppTest(t, src)
	if a.ExitNotice(context.Background()) != "" {
		t.Fatal("nothing created: nothing to say")
	}
	_, cmd := a.Update(tea.KeyPressMsg{Text: "n", Code: 'n'})
	feedApp(t, a, cmd)
	if !a.OnBridge() {
		t.Fatal("setup: n creates and enters")
	}
	// Still listed => still alive.
	src.set(append(threeBridges(), bridges.Info{InstanceID: "dddd4444-0000", Mode: "local"})...)
	notice := a.ExitNotice(context.Background())
	for _, want := range []string{"dddd4444-0000", "agents-bridge stop --instance-id dddd4444-0000"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("exit notice should contain %q:\n%s", want, notice)
		}
	}
	// Gone => not mentioned.
	src.set(threeBridges()...)
	if got := a.ExitNotice(context.Background()); got != "" {
		t.Fatalf("a created bridge that already closed must not be reported:\n%s", got)
	}
	// If the list itself fails, warn anyway.
	src.mu.Lock()
	src.listErr = errors.New("boom")
	src.mu.Unlock()
	if !strings.Contains(a.ExitNotice(context.Background()), "dddd4444-0000") {
		t.Fatal("when liveness cannot be checked, warn about every created bridge")
	}
}

// TestAppReturningHomeLeaksNoGoroutinesOrConnections drives the real thing:
// a real bridge server, a real control endpoint and ControlTransport, a real
// /v1/watch stream and status polls. Entering and leaving a bridge must
// leave no goroutine behind (the watch wait, the stream reader, the status
// poll), or the TUI would slowly accumulate them every time someone goes
// back to the list.
func TestAppReturningHomeLeaksNoGoroutinesOrConnections(t *testing.T) {
	h := newObserverHarness(t)
	desc := h.ownerEndpoint.Descriptor()
	src := &fakeSource{}
	src.set(bridges.Info{InstanceID: desc.InstanceID, Mode: "local", Roles: []string{"orchestrator", "executor"}, StartedAt: time.Now()})
	src.session = func(id string) BridgeSession {
		return BridgeSession{Transport: NewControlTransport(desc), LocalRole: desc.LocalRole, OnStop: func() {}}
	}
	a := newAppTest(t, src)

	settle := func() int {
		http.DefaultTransport.(*http.Transport).CloseIdleConnections()
		var n int
		for i := 0; i < 50; i++ {
			time.Sleep(20 * time.Millisecond)
			n = runtime.NumGoroutine()
		}
		return n
	}
	visit := func() {
		r := newCmdRunner(a)
		_, initCmd := a.Update(openBridgeMsg{id: desc.InstanceID})
		// Run every command Init produced for real, like the runtime does:
		// the subscription wait blocks on the live watch stream.
		r.start(initCmd)
		r.pump(400 * time.Millisecond) // let the watch stream and the first status poll happen
		if _, err := h.worker.Publish("hola"); err != nil {
			t.Fatal(err)
		}
		r.pump(300 * time.Millisecond)
		if !strings.Contains(appView(a), "hola") {
			t.Fatalf("setup: the live message should have reached the bridge view:\n%s", appView(a))
		}
		_, cmd := a.Update(escKey())
		r.start(cmd)
		r.finish(t, 6*time.Second)
	}

	visit() // warm-up: lets lazily created runtime/http goroutines exist once
	baseline := settle()
	visit()
	after := settle()
	if after > baseline {
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Fatalf("goroutines grew from %d to %d after leaving a real bridge:\n%s", baseline, after, buf[:n])
	}
}

// cmdRunner executes tea.Cmds on their own goroutines like the bubbletea
// runtime does, but delivers every resulting message to the App on the
// test's goroutine (App is not goroutine-safe, and -race must stay clean).
type cmdRunner struct {
	a   *App
	wg  sync.WaitGroup
	out chan tea.Msg
}

func newCmdRunner(a *App) *cmdRunner { return &cmdRunner{a: a, out: make(chan tea.Msg, 256)} }

func (r *cmdRunner) start(c tea.Cmd) {
	if c == nil {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if msg := c(); msg != nil {
			r.out <- msg
		}
	}()
}

func (r *cmdRunner) deliver(msg tea.Msg) {
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			r.start(c)
		}
		return
	}
	_, next := r.a.Update(msg)
	r.start(next)
}

// pump delivers messages for d.
func (r *cmdRunner) pump(d time.Duration) {
	deadline := time.After(d)
	for {
		select {
		case msg := <-r.out:
			r.deliver(msg)
		case <-deadline:
			return
		}
	}
}

// finish waits until every started command has returned (delivering what
// they produce), failing if any is still blocked after timeout: a command
// that never returns is exactly a leaked goroutine.
func (r *cmdRunner) finish(t *testing.T, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-r.out:
			r.deliver(msg)
		case <-done:
			for {
				select {
				case msg := <-r.out:
					r.deliver(msg)
				default:
					return
				}
			}
		case <-deadline:
			t.Fatal("a command started for the bridge view never returned: leaked goroutine")
		}
	}
}
