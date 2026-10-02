package tui

import (
	"fmt"
	"image/color"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/internal/protocol"
	"github.com/grrdhdz/agents-bridge/internal/tui/theme"
)

func autoModel(t *testing.T, nocolor bool) *Model {
	t.Helper()
	th := theme.New(theme.ModeAuto, nocolor, nil)
	m := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), Theme: th})
	return &m
}

// initMessages runs every command of a tea.Batch with a short timeout and
// returns what the non-blocking ones produced.
func initMessages(t *testing.T, cmd tea.Cmd) []string {
	t.Helper()
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []string{fmt.Sprintf("%T", msg)}
	}
	var out []string
	for _, sub := range batch {
		if sub == nil {
			continue
		}
		done := make(chan string, 1)
		go func() { done <- fmt.Sprintf("%T", sub()) }()
		select {
		case name := <-done:
			out = append(out, name)
		case <-time.After(150 * time.Millisecond):
		}
	}
	return out
}

func requestsBackground(names []string) bool {
	for _, n := range names {
		if strings.Contains(n, "backgroundColorMsg") {
			return true
		}
	}
	return false
}

func TestAutoThemeAsksTheTerminalFromInsideTheProgram(t *testing.T) {
	m := autoModel(t, false)
	if !requestsBackground(initMessages(t, m.Init())) {
		t.Fatal("an auto theme must request the background through Bubble Tea's own command")
	}
	for name, th := range map[string]theme.Theme{
		"dark":    theme.New(theme.ModeDark, false, nil),
		"light":   theme.New(theme.ModeLight, false, nil),
		"nocolor": theme.New(theme.ModeAuto, true, nil),
	} {
		m := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), Theme: th})
		if requestsBackground(initMessages(t, m.Init())) {
			t.Fatalf("%s: an explicit (or colorless) theme must not query the terminal", name)
		}
	}
}

func TestBackgroundColorReplyPicksLightOrKeepsDark(t *testing.T) {
	light := autoModel(t, false)
	light.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 250, G: 250, B: 250, A: 255}})
	if light.th.Mode != theme.ModeLight {
		t.Fatalf("a light background should switch to the light theme, got %q", light.th.Mode)
	}
	if light.th.Auto {
		t.Fatal("the answer settles the theme; it must not stay undecided")
	}
	dark := autoModel(t, false)
	dark.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 5, G: 5, B: 20, A: 255}})
	if dark.th.Mode != theme.ModeDark || dark.th.Auto {
		t.Fatalf("a dark background keeps dark and settles: %q auto=%v", dark.th.Mode, dark.th.Auto)
	}
}

func TestBackgroundColorReplyNeverOverridesAnExplicitTheme(t *testing.T) {
	m := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), Theme: theme.New(theme.ModeDark, false, nil)})
	m.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 250, G: 250, B: 250, A: 255}})
	if m.th.Mode != theme.ModeDark {
		t.Fatal("--theme dark must win over the terminal's answer")
	}
}

func TestNoAnswerLeavesAutoThemeDark(t *testing.T) {
	m := autoModel(t, false)
	if m.th.Mode != theme.ModeDark || !m.th.Auto {
		t.Fatalf("before any answer: %q auto=%v", m.th.Mode, m.th.Auto)
	}
}

func TestHomeScreenAlsoResolvesAutoTheme(t *testing.T) {
	th := theme.New(theme.ModeAuto, false, nil)
	app := NewApp(AppOptions{Source: &fakeSource{}, Theme: th})
	if !requestsBackground(initMessages(t, app.Init())) {
		t.Fatal("the home screen must request the background too")
	}
	app.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 250, G: 250, B: 250, A: 255}})
	if app.home.th.Mode != theme.ModeLight {
		t.Fatalf("home theme after a light reply = %q", app.home.th.Mode)
	}
}

// TestAutoThemeNeverLeavesAStdinReader: nothing outside Bubble Tea may read
// stdin, or it eats bytes of mouse reports (the v0.3.3 xterm.js bug). With a
// stdin that never answers, building the theme and starting the model must
// leave every byte written afterwards for the real reader.
func TestAutoThemeNeverLeavesAStdinReader(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()

	m := autoModel(t, false)
	initMessages(t, m.Init())
	time.Sleep(100 * time.Millisecond)
	if _, err := w.Write([]byte("\x1b[<32;56;57M")); err != nil {
		t.Fatal(err)
	}
	_ = r.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 32)
	n, err := r.Read(buf)
	if err != nil || string(buf[:n]) != "\x1b[<32;56;57M" {
		t.Fatalf("a stray reader consumed stdin: read %q err=%v", buf[:n], err)
	}
}
