package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

// AppOptions configures the App shell.
type AppOptions struct {
	Source HomeSource
	Theme  theme.Theme
	// Now is the clock the home screen and the bridge views use; nil
	// defaults to time.Now.
	Now func() time.Time
}

// App is the shell of `codex-bridge tui` without --instance-id (spec §3,
// §5): it owns the home screen and, while one is entered, one bridge view
// (a Model with the observer capabilities), and moves between them:
//
//   - enter/double click/n on the home screen opens a bridge (observer);
//   - esc, the palette's "Volver a inicio" or the bridge closing under it
//     bring the person back to the list. Going back Shutdown()s the bridge
//     view first, so its watch subscription, its pending wait and its
//     transport are released and no goroutine or connection outlives the
//     visit; anything still in flight from it (events, ticks, status polls)
//     is dropped by owner id and by screen, so it can never leak into the
//     next bridge or re-arm a timer chain.
type App struct {
	source HomeSource
	now    func() time.Time

	home   HomeModel
	bridge *Model

	width, height int
}

// NewApp builds the shell, starting on the home screen.
func NewApp(o AppOptions) App {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return App{
		source: o.Source,
		now:    now,
		home:   NewHome(HomeOptions{Source: o.Source, Theme: o.Theme, Now: now}),
		width:  80,
		height: 24,
	}
}

// OnBridge reports whether a bridge view is currently open.
func (a *App) OnBridge() bool { return a.bridge != nil }

func (a *App) Init() tea.Cmd { return a.home.Init() }

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.home.Update(msg)
		if a.bridge != nil {
			a.bridge.Update(msg)
		}
		return a, nil
	case tea.BackgroundColorMsg:
		a.home.Update(msg)
		if a.bridge != nil {
			a.bridge.Update(msg)
		}
		return a, nil
	case openBridgeMsg:
		if a.bridge != nil {
			return a, nil
		}
		return a, a.openBridge(msg.id)
	case returnHomeMsg:
		if a.bridge == nil {
			return a, nil
		}
		return a, a.returnHome(msg.notice)
	case homeListMsg, homeTickMsg, homeStopDoneMsg, homeCreateDoneMsg:
		_, cmd := a.home.Update(msg)
		return a, cmd
	case eventMsg, reconnectTickMsg, statusPollTickMsg, statusResultMsg:
		if a.bridge == nil {
			return a, nil // in flight from a bridge already left
		}
		_, cmd := a.bridge.Update(msg)
		return a, cmd
	}
	if a.bridge != nil {
		_, cmd := a.bridge.Update(msg)
		return a, cmd
	}
	_, cmd := a.home.Update(msg)
	return a, cmd
}

func (a *App) View() tea.View {
	if a.bridge != nil {
		return a.bridge.View()
	}
	return a.home.View()
}

// openBridge enters id's bridge view as an observer that can go back home.
func (a *App) openBridge(id string) tea.Cmd {
	session, err := a.source.Open(id)
	if err != nil {
		a.home.Notify("No se pudo abrir " + shortInstance(id) + ": " + err.Error())
		return nil
	}
	m := New(Options{
		Transport:    session.Transport,
		LocalRole:    session.LocalRole,
		Capabilities: CapabilitiesForObserver(),
		Theme:        a.home.th,
		OnStop:       session.OnStop,
		Now:          a.now,
	})
	a.bridge = &m
	// The home screen's refresh chain stops while a bridge is open (its
	// pending tick carries the old generation and dies on arrival).
	a.home.tickGen++
	a.bridge.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height})
	return a.bridge.Init()
}

// returnHome leaves the bridge view for the list.
func (a *App) returnHome(notice string) tea.Cmd {
	a.bridge.Shutdown()
	a.home.SetTheme(a.bridge.th)
	a.bridge = nil
	a.home.Notify(notice)
	return a.home.Resume()
}

// Close releases whatever the App still holds (the open bridge view, if
// any); the runner calls it once the program ends.
func (a *App) Close() {
	if a.bridge != nil {
		a.bridge.Shutdown()
		a.bridge = nil
	}
}

// ExitNotice is the message printed on stdout once the TUI has restored the
// terminal: the bridges this session created with n that are still alive
// (they keep running after the TUI exits; spec §5), with how to close them.
// It returns "" when there is nothing to warn about. If the listing itself
// fails, every created bridge is assumed alive (a warning too many is
// better than an orphan nobody knows about).
func (a *App) ExitNotice(ctx context.Context) string {
	created := a.home.Created()
	if len(created) == 0 {
		return ""
	}
	alive := created
	if infos, err := a.source.List(ctx); err == nil {
		present := map[string]bool{}
		for _, info := range infos {
			present[info.InstanceID] = true
		}
		alive = alive[:0:0]
		for _, id := range created {
			if present[id] {
				alive = append(alive, id)
			}
		}
	}
	if len(alive) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Puentes creados en esta sesión que siguen activos (no se cierran al salir):\n")
	for _, id := range alive {
		fmt.Fprintf(&b, "  %s   cierra con: codex-bridge stop --instance-id %s\n", id, id)
	}
	b.WriteString("También se cierran solos tras un rato sin actividad.\n")
	return b.String()
}
