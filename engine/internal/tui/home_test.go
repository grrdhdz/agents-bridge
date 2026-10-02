package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridges"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

// fakeSource is the injectable HomeSource: tests script the bridges it
// lists and observe what the home screen asks it to do. It never starts a
// process.
type fakeSource struct {
	mu        sync.Mutex
	infos     []bridges.Info
	listErr   error
	listCalls int
	stopped   []string
	stopErr   error
	opened    []string
	openErr   error
	creates   int
	createID  string
	createErr error
	session   func(id string) BridgeSession
}

func (f *fakeSource) List(context.Context) ([]bridges.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	return append([]bridges.Info(nil), f.infos...), f.listErr
}

func (f *fakeSource) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	return f.stopErr
}

func (f *fakeSource) Open(id string) (BridgeSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, id)
	if f.openErr != nil {
		return BridgeSession{}, f.openErr
	}
	if f.session != nil {
		return f.session(id), nil
	}
	return BridgeSession{Transport: &fakeTransport{instanceID: id}, LocalRole: protocol.RoleOrchestrator, OnStop: func() {}}, nil
}

func (f *fakeSource) Create(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	return f.createID, f.createErr
}

func (f *fakeSource) set(infos ...bridges.Info) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.infos = infos
}

// noTick replaces the refresh timer: tests deliver ticks by hand, so no real
// 2 s timer is ever left pending.
func noTick(int) tea.Cmd { return nil }

func idle(s int64) *int64 { return &s }

func threeBridges() []bridges.Info {
	t0 := time.Date(2026, 9, 27, 9, 30, 0, 0, time.Local)
	return []bridges.Info{
		{InstanceID: "aaaa1111-0000", Mode: "local", Roles: []string{"orchestrator", "executor"}, StartedAt: t0, IdleSeconds: idle(12), PeerConnected: true, LatestServerSeq: 7, Project: "api-server"},
		{InstanceID: "bbbb2222-0000", Mode: "tailscale-host", Roles: []string{"orchestrator"}, StartedAt: t0.Add(time.Hour), IdleSeconds: idle(3700), PeerConnected: false, LatestServerSeq: 0, Project: "web"},
		{InstanceID: "cccc3333-0000", Mode: "tailscale-join", Roles: []string{"executor"}, StartedAt: t0.Add(2 * time.Hour), PeerConnected: true, LatestServerSeq: 42},
	}
}

var homeClock = time.Date(2026, 9, 27, 12, 0, 0, 0, time.Local)

func newHomeTest(t *testing.T, src *fakeSource, width, height int, mode theme.Mode) *HomeModel {
	t.Helper()
	h := NewHome(HomeOptions{Source: src, Theme: theme.New(mode, false, nil), Now: func() time.Time { return homeClock }})
	m := &h
	m.tick = noTick
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	// Deliver the first listing exactly like the runtime does: run the
	// command Init returned and feed its message back.
	feed(t, m, m.Init())
	return m
}

// feed runs cmd (recursing into batches, but never into tickers, which only
// produce a message after a real timer) and delivers every resulting
// message to h.
func feed(t *testing.T, h *HomeModel, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var out []tea.Msg
	for _, msg := range collect(cmd) {
		out = append(out, msg)
		_, next := h.Update(msg)
		out = append(out, feed(t, h, next)...)
	}
	return out
}

// collect executes a command; batch messages are expanded. Commands that
// sleep on a timer (tea.Tick) are skipped: tests deliver ticks by hand.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(300 * time.Millisecond):
		return nil // a tea.Tick waiting on its timer
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collect(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func press(h *HomeModel, text string) tea.Cmd {
	code := rune(0)
	if len([]rune(text)) == 1 {
		code = []rune(text)[0]
	}
	_, cmd := h.Update(tea.KeyPressMsg{Text: text, Code: code})
	return cmd
}

func pressCode(h *HomeModel, code rune) tea.Cmd {
	_, cmd := h.Update(tea.KeyPressMsg{Code: code})
	return cmd
}

func plainView(h *HomeModel) string { return ansi.Strip(h.View().Content) }

func TestHomeEmptyStateShowsInstructions(t *testing.T) {
	h := newHomeTest(t, &fakeSource{}, 100, 24, theme.ModeDark)
	view := plainView(h)
	for _, want := range []string{"0 puentes", "No hay puentes", "n  crea", "agents-bridge local"} {
		if !strings.Contains(view, want) {
			t.Fatalf("empty state should mention %q:\n%s", want, view)
		}
	}
}

func TestHomeListsBridgesWithTheirColumns(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	view := plainView(h)
	for _, want := range []string{
		"agents-bridge · 3 puentes",
		"aaaa1111", "api-server", "local", "●", "12s", "09:30",
		"bbbb2222", "web", "host", "○", "1h", "10:30",
		"cccc3333", "join",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("list should show %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "-0000") {
		t.Fatalf("the instance must be abbreviated:\n%s", view)
	}
}

func TestHomeRefreshDoesNotBlockUpdate(t *testing.T) {
	src := &fakeSource{}
	h := newHomeTest(t, src, 100, 24, theme.ModeDark)
	before := src.listCalls
	_, cmd := h.Update(homeTickMsg{gen: h.tickGen})
	if src.listCalls != before {
		t.Fatal("Update must never call the source itself: the listing runs in a tea.Cmd")
	}
	if cmd == nil {
		t.Fatal("a tick should schedule the next refresh")
	}
	feed(t, h, cmd)
	if src.listCalls == before {
		t.Fatal("the tick's command should have listed the bridges")
	}
}

func TestHomeRefreshDropsABridgeThatDisappearsAndKeepsSelectionByID(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	press(h, "j") // select bbbb
	if got := h.selectedID(); got != "bbbb2222-0000" {
		t.Fatalf("selected %q", got)
	}
	// A new bridge appears before it and cccc is the one that vanishes:
	// the selection follows the instance, not the row index.
	all := threeBridges()
	src.set(bridges.Info{InstanceID: "0000zzzz", Mode: "local", Roles: []string{"orchestrator"}, StartedAt: all[0].StartedAt}, all[0], all[1])
	feed(t, h, func() tea.Msg { return homeTickMsg{gen: h.tickGen} })
	if got := h.selectedID(); got != "bbbb2222-0000" {
		t.Fatalf("the selection should follow its instance across a refresh, got %q", got)
	}
	if strings.Contains(plainView(h), "cccc3333") {
		t.Fatal("a bridge that disappeared must leave the list")
	}
	// The selected bridge itself vanishes: selection falls to a neighbour.
	src.set(all[0])
	feed(t, h, func() tea.Msg { return homeTickMsg{gen: h.tickGen} })
	if got := h.selectedID(); got != "aaaa1111-0000" {
		t.Fatalf("with its bridge gone the selection should land on a neighbour, got %q", got)
	}
}

func TestHomeStaleListResultIsDropped(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	h.Update(homeListMsg{seq: h.listSeq - 1, infos: nil})
	if !strings.Contains(plainView(h), "aaaa1111") {
		t.Fatal("a listing older than the latest applied one must be ignored")
	}
}

func TestHomeListErrorKeepsTheLastList(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	src.mu.Lock()
	src.listErr = errors.New("boom")
	src.mu.Unlock()
	feed(t, h, func() tea.Msg { return homeTickMsg{gen: h.tickGen} })
	view := plainView(h)
	if !strings.Contains(view, "aaaa1111") {
		t.Fatalf("a failed refresh must not empty the list:\n%s", view)
	}
}

func TestHomeKeyboardSelectionAndEnter(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	if h.selectedID() != "aaaa1111-0000" {
		t.Fatalf("first row selected by default, got %q", h.selectedID())
	}
	pressCode(h, tea.KeyUp)
	if h.selectedID() != "aaaa1111-0000" {
		t.Fatal("up at the top must clamp")
	}
	pressCode(h, tea.KeyDown)
	press(h, "j")
	press(h, "j")
	if h.selectedID() != "cccc3333-0000" {
		t.Fatalf("down at the bottom must clamp, got %q", h.selectedID())
	}
	press(h, "k")
	msg, ok := collectOne(pressCode(h, tea.KeyEnter)).(openBridgeMsg)
	if !ok || msg.id != "bbbb2222-0000" {
		t.Fatalf("enter should open the selected bridge, got %#v", msg)
	}
}

func collectOne(cmd tea.Cmd) tea.Msg {
	msgs := collect(cmd)
	if len(msgs) == 0 {
		return nil
	}
	return msgs[0]
}

func TestHomeEnterOnEmptyListDoesNothing(t *testing.T) {
	h := newHomeTest(t, &fakeSource{}, 100, 24, theme.ModeDark)
	if cmd := pressCode(h, tea.KeyEnter); collectOne(cmd) != nil {
		t.Fatal("enter with nothing selected must do nothing")
	}
}

func TestHomeFilterByInstanceProjectAndMode(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	press(h, "/")
	for _, r := range "web" {
		press(h, string(r))
	}
	view := plainView(h)
	if strings.Contains(view, "aaaa1111") || !strings.Contains(view, "bbbb2222") || !strings.Contains(view, "1 de 3 puentes") {
		t.Fatalf("filtering by project should keep only the match:\n%s", view)
	}
	pressCode(h, tea.KeyEnter) // commit
	if plainView(h) == "" || h.filterActive {
		t.Fatal("enter commits the filter and closes the field")
	}
	if !strings.Contains(plainView(h), "1 de 3") {
		t.Fatal("a committed filter stays applied")
	}
	if got := h.selectedID(); got != "bbbb2222-0000" {
		t.Fatalf("selection must land on a visible row, got %q", got)
	}
	// esc clears it.
	press(h, "/")
	pressCode(h, tea.KeyEscape)
	if !strings.Contains(plainView(h), "3 puentes") || h.filter != "" {
		t.Fatal("esc clears the filter")
	}
	// instance and mode matches, case-insensitively.
	press(h, "/")
	for _, r := range "CCCC" {
		press(h, string(r))
	}
	if !strings.Contains(plainView(h), "cccc3333") || strings.Contains(plainView(h), "aaaa1111") {
		t.Fatal("filter should match the instance id case-insensitively")
	}
	pressCode(h, tea.KeyEscape)
	press(h, "/")
	for _, r := range "tailscale" {
		press(h, string(r))
	}
	if strings.Contains(plainView(h), "aaaa1111") || !strings.Contains(plainView(h), "2 de 3") {
		t.Fatal("filter should match the mode")
	}
}

func TestHomeQKeyTypesIntoTheFilterInsteadOfQuitting(t *testing.T) {
	h := newHomeTest(t, &fakeSource{}, 100, 24, theme.ModeDark)
	press(h, "/")
	if msg := collectOne(press(h, "q")); msg != nil {
		if _, quit := msg.(tea.QuitMsg); quit {
			t.Fatal("q must type into the open filter, not quit")
		}
	}
	if h.filterInput.Value() != "q" {
		t.Fatalf("filter should contain q, got %q", h.filterInput.Value())
	}
}

func TestHomeQuitKeys(t *testing.T) {
	h := newHomeTest(t, &fakeSource{}, 100, 24, theme.ModeDark)
	if _, ok := collectOne(press(h, "q")).(tea.QuitMsg); !ok {
		t.Fatal("q quits")
	}
	if _, ok := collectOne(func() tea.Cmd { _, c := h.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); return c }()).(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c quits")
	}
}

func TestHomeStopAsksForConfirmationThenStops(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	press(h, "j")
	press(h, "s")
	view := plainView(h)
	if !strings.Contains(view, "bbbb2222") || !strings.Contains(view, "Cerrar") || !strings.Contains(view, "Cancelar") {
		t.Fatalf("s should open the floating confirmation dialog:\n%s", view)
	}
	if len(src.stopped) != 0 {
		t.Fatal("nothing may stop before confirming")
	}
	// Cancel with n and with esc.
	press(h, "n")
	if h.confirm != nil || len(src.stopped) != 0 {
		t.Fatal("n cancels")
	}
	press(h, "s")
	pressCode(h, tea.KeyEscape)
	if h.confirm != nil || len(src.stopped) != 0 {
		t.Fatal("esc cancels")
	}
	// A stray key does not confirm.
	press(h, "s")
	press(h, "x")
	if h.confirm == nil || len(src.stopped) != 0 {
		t.Fatal("an unrelated key must neither confirm nor dismiss")
	}
	feed(t, h, press(h, "y"))
	if len(src.stopped) != 1 || src.stopped[0] != "bbbb2222-0000" {
		t.Fatalf("y should stop the selected bridge, got %v", src.stopped)
	}
	if h.confirm != nil {
		t.Fatal("the dialog closes on confirm")
	}
}

func TestHomeStopFailureIsReportedNotSwallowed(t *testing.T) {
	src := &fakeSource{stopErr: errors.New("CONTROL_UNREACHABLE")}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	press(h, "s")
	feed(t, h, press(h, "y"))
	if !strings.Contains(plainView(h), "CONTROL_UNREACHABLE") {
		t.Fatalf("a failed stop must surface as a notice:\n%s", plainView(h))
	}
}

func TestHomeCreateLaunchesThenEnters(t *testing.T) {
	src := &fakeSource{createID: "dddd4444-0000"}
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	cmd := press(h, "n")
	if src.creates != 0 {
		t.Fatal("Update must not launch anything itself; the launch runs in a tea.Cmd")
	}
	if !strings.Contains(plainView(h), "Creando") {
		t.Fatal("creating takes a moment: show progress")
	}
	if again := press(h, "n"); again != nil {
		t.Fatal("a second n while creating must be ignored")
	}
	msgs := feed(t, h, cmd)
	if src.creates != 1 {
		t.Fatalf("expected exactly one launch, got %d", src.creates)
	}
	var opened bool
	for _, m := range msgs {
		if o, ok := m.(openBridgeMsg); ok && o.id == "dddd4444-0000" {
			opened = true
		}
	}
	if !opened {
		t.Fatalf("a created bridge should be entered, got %#v", msgs)
	}
	if got := h.Created(); len(got) != 1 || got[0] != "dddd4444-0000" {
		t.Fatalf("created bridges are remembered for the exit notice, got %v", got)
	}
}

func TestHomeCreateFailureStaysOnHomeWithNotice(t *testing.T) {
	src := &fakeSource{createErr: errors.New("no se pudo lanzar")}
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	msgs := feed(t, h, press(h, "n"))
	for _, m := range msgs {
		if _, ok := m.(openBridgeMsg); ok {
			t.Fatal("a failed creation must not enter anything")
		}
	}
	if !strings.Contains(plainView(h), "no se pudo lanzar") || len(h.Created()) != 0 {
		t.Fatalf("the failure should be shown:\n%s", plainView(h))
	}
	if h.creating {
		t.Fatal("creating must reset so n works again")
	}
}

func TestHomeRefreshKey(t *testing.T) {
	src := &fakeSource{}
	h := newHomeTest(t, src, 100, 24, theme.ModeDark)
	before := src.listCalls
	feed(t, h, press(h, "r"))
	if src.listCalls != before+1 {
		t.Fatal("r refreshes immediately")
	}
}

func TestHomeMouseClickSelectsAndDoubleClickEnters(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	now := homeClock
	h := NewHome(HomeOptions{Source: src, Theme: theme.New(theme.ModeDark, false, nil), Now: func() time.Time { return now }})
	m := &h
	m.tick = noTick
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	feed(t, m, m.Init())
	m.View() // registers the click regions

	row := m.rowScreenY(1)
	m.Update(click(10, row))
	if m.selectedID() != "bbbb2222-0000" {
		t.Fatalf("click should select the row, got %q", m.selectedID())
	}
	now = now.Add(150 * time.Millisecond)
	_, cmd := m.Update(click(10, row))
	if msg, ok := collectOne(cmd).(openBridgeMsg); !ok || msg.id != "bbbb2222-0000" {
		t.Fatalf("a quick second click on the same row should enter, got %#v", msg)
	}

	// Slow second click, or a click on a different row, only selects.
	m.View()
	now = now.Add(2 * time.Second)
	_, cmd = m.Update(click(10, row))
	if collectOne(cmd) != nil {
		t.Fatal("two slow clicks are not a double click")
	}
	now = now.Add(100 * time.Millisecond)
	_, cmd = m.Update(click(10, m.rowScreenY(0)))
	if collectOne(cmd) != nil || m.selectedID() != "aaaa1111-0000" {
		t.Fatal("clicks on different rows are not a double click")
	}
}

func TestHomeWheelScrollsALongList(t *testing.T) {
	src := &fakeSource{}
	var many []bridges.Info
	for i := 0; i < 30; i++ {
		many = append(many, bridges.Info{InstanceID: fmt.Sprintf("id%02d0000", i), Mode: "local", Roles: []string{"orchestrator"}, StartedAt: homeClock})
	}
	src.set(many...)
	h := newHomeTest(t, src, 100, 14, theme.ModeDark)
	if strings.Contains(plainView(h), "id29") {
		t.Fatal("setup: the last row should be off-screen")
	}
	for i := 0; i < 40; i++ {
		h.Update(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelDown})
	}
	if !strings.Contains(plainView(h), "id29") {
		t.Fatalf("the wheel should scroll the list to the end:\n%s", plainView(h))
	}
	if h.selectedID() != "id000000" {
		t.Fatalf("scrolling with the wheel must not move the selection, got %q", h.selectedID())
	}
}

func TestHomeKeyboardSelectionScrollsIntoView(t *testing.T) {
	src := &fakeSource{}
	var many []bridges.Info
	for i := 0; i < 30; i++ {
		many = append(many, bridges.Info{InstanceID: fmt.Sprintf("id%02d0000", i), Mode: "local", Roles: []string{"orchestrator"}, StartedAt: homeClock})
	}
	src.set(many...)
	h := newHomeTest(t, src, 100, 14, theme.ModeDark)
	for i := 0; i < 29; i++ {
		press(h, "j")
	}
	if !strings.Contains(plainView(h), "id29") {
		t.Fatalf("moving the selection must keep it visible:\n%s", plainView(h))
	}
}

func TestHomePaletteRunsItsActions(t *testing.T) {
	src := &fakeSource{createID: "eeee5555"}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	h.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	view := plainView(h)
	for _, want := range []string{"Entrar", "Cerrar puente", "Crear puente", "Refrescar", "Filtrar", "Cambiar tema", "Ayuda"} {
		if !strings.Contains(view, want) {
			t.Fatalf("home palette should list %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Volver a inicio") {
		t.Fatal("the home palette has nothing to return to")
	}
	// Filter then run "Crear puente" with enter.
	for _, r := range "crear" {
		press(h, string(r))
	}
	feed(t, h, pressCode(h, tea.KeyEnter))
	if src.creates != 1 {
		t.Fatal("the palette's Crear puente should launch a bridge")
	}
	// The theme item toggles dark/light.
	h.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	for _, r := range "tema" {
		press(h, string(r))
	}
	pressCode(h, tea.KeyEnter)
	if h.th.Mode != theme.ModeLight {
		t.Fatalf("Cambiar tema should switch to light, got %q", h.th.Mode)
	}
}

func TestHomePaletteEnterItemAndClickOutsideCloses(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	h.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	msg, ok := collectOne(pressCode(h, tea.KeyEnter)).(openBridgeMsg)
	if !ok || msg.id != "aaaa1111-0000" {
		t.Fatalf("the first palette item (Entrar) opens the selected bridge, got %#v", msg)
	}
	h.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	h.View()
	h.Update(click(0, h.height-1))
	if h.paletteOpen {
		t.Fatal("a click outside the palette closes it")
	}
}

func TestHomeHelpOpensAndCloses(t *testing.T) {
	h := newHomeTest(t, &fakeSource{}, 100, 24, theme.ModeDark)
	press(h, "?")
	view := plainView(h)
	for _, want := range []string{"Ayuda", "crear puente", "filtrar"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help should list %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "plegar") {
		t.Fatal("home help must not list bridge-view keys")
	}
	press(h, "x")
	if h.showHelp {
		t.Fatal("any key closes help")
	}
}

func TestHomeShortcutsBarIsOneLineAndClickable(t *testing.T) {
	src := &fakeSource{createID: "ffff6666"}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	lines := strings.Split(plainView(h), "\n")
	bar := lines[len(lines)-1]
	if !strings.Contains(bar, "enter entrar") || !strings.HasSuffix(strings.TrimSpace(bar), "? más") {
		t.Fatalf("the shortcuts bar comes from keys/ and ends in \"? más\": %q", bar)
	}
	idx := strings.Index(bar, "crear puente")
	if idx < 0 {
		t.Fatalf("expected crear puente in the bar at 140 columns: %q", bar)
	}
	_, cmd := h.Update(click(idx+1, len(lines)-1))
	feed(t, h, cmd)
	if src.creates != 1 {
		t.Fatal("clicking a shortcut runs its action")
	}
}

func TestHomeViewFillsExactlyTheTerminalAndNeverOverflows(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	for _, size := range [][2]int{{60, 24}, {80, 24}, {120, 40}, {40, 12}, {59, 20}} {
		for _, withList := range []bool{false, true} {
			s := &fakeSource{}
			if withList {
				s.set(threeBridges()...)
			}
			h := newHomeTest(t, s, size[0], size[1], theme.ModeDark)
			lines := strings.Split(h.View().Content, "\n")
			if len(lines) != size[1] {
				t.Fatalf("%v list=%v: view has %d lines, want exactly %d", size, withList, len(lines), size[1])
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Fatalf("%v: line %d is %d wide: %q", size, i, w, ansi.Strip(l))
				}
			}
		}
	}
}

func TestHomeStatusBarIsFirstRow(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 100, 24, theme.ModeDark)
	first := strings.Split(plainView(h), "\n")[0]
	if !strings.Contains(first, "agents-bridge · 3 puentes") {
		t.Fatalf("status bar title, got %q", first)
	}
}

func TestHomeCompactModeStillShowsTheEssentials(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 50, 20, theme.ModeDark)
	view := plainView(h)
	for _, want := range []string{"aaaa1111", "api-server", "●"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact list should keep %q:\n%s", want, view)
		}
	}
}

func TestHomeSelectedRowIsMarkedAndUsesTheAccent(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	h := newHomeTest(t, src, 140, 24, theme.ModeDark)
	view := h.View().Content
	var selected, other string
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "aaaa1111") {
			selected = l
		}
		if strings.Contains(l, "bbbb2222") {
			other = l
		}
	}
	if !strings.Contains(ansi.Strip(selected), "▶") || strings.Contains(ansi.Strip(other), "▶") {
		t.Fatalf("only the selected row carries the marker:\n%q\n%q", ansi.Strip(selected), ansi.Strip(other))
	}
	accent := backgroundSGRSubstringFG(h.th.Info)
	if !strings.Contains(selected, accent) || strings.Contains(other, accent) {
		t.Fatal("the selected row uses the accent color, the others do not")
	}
}

func TestHomeEveryCellHasABackgroundInBothThemesAndUnderOverlays(t *testing.T) {
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		for _, width := range []int{60, 80, 120} {
			for _, withList := range []bool{false, true} {
				name := fmt.Sprintf("%s_w%d_list%v", mode, width, withList)
				t.Run(name, func(t *testing.T) {
					src := &fakeSource{}
					if withList {
						src.set(threeBridges()...)
					}
					h := newHomeTest(t, src, width, 24, mode)
					assertFullBackgroundCoverage(t, name, h.View().Content, width)
					h.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
					assertFullBackgroundCoverage(t, name+" palette", h.View().Content, width)
					pressCode(h, tea.KeyEscape)
					press(h, "?")
					assertFullBackgroundCoverage(t, name+" help", h.View().Content, width)
					press(h, "x")
					if withList {
						press(h, "s")
						assertFullBackgroundCoverage(t, name+" dialog", h.View().Content, width)
					}
				})
			}
		}
	}
}

func TestHomeNoColorPaintsNoBackgroundAndKeepsTheContent(t *testing.T) {
	src := &fakeSource{}
	src.set(threeBridges()...)
	th := theme.New(theme.ModeDark, true, nil)
	h := NewHome(HomeOptions{Source: src, Theme: th, Now: func() time.Time { return homeClock }})
	m := &h
	m.tick = noTick
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	feed(t, m, m.Init())
	view := m.View()
	if hasExplicitBackground(view.Content) || view.BackgroundColor != nil {
		t.Fatal("NO_COLOR paints no background")
	}
	if strings.Contains(view.Content, "38;2;") {
		t.Fatal("NO_COLOR emits no foreground colors either")
	}
	if !strings.Contains(ansi.Strip(view.Content), "aaaa1111") || !strings.Contains(ansi.Strip(view.Content), "▶") {
		t.Fatal("the list and the selection marker survive without color")
	}
}

func TestHomeViewSetsThemeBackgroundColor(t *testing.T) {
	h := newHomeTest(t, &fakeSource{}, 80, 24, theme.ModeLight)
	if h.View().BackgroundColor != h.th.Surface {
		t.Fatal("the home screen sets the terminal background like the bridge view")
	}
}
