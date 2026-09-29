package tui

import (
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
)

// returnHomeMsg is what a bridge Model produces (instead of tea.Quit) when
// it was entered from the home screen and the person, or the bridge itself
// closing, means "go back to the list" (spec §5): the App shell (root.go)
// handles it by shutting the Model down and showing the home screen again,
// with notice (possibly empty) as a toast.
type returnHomeMsg struct{ notice string }

func returnHomeCmd(notice string) tea.Cmd {
	return func() tea.Msg { return returnHomeMsg{notice: notice} }
}

var modelCounter atomic.Int64

// nextModelID numbers Models so the async messages one App session's
// successive bridge views produce (events, ticks, status polls) can be told
// apart: without it, a tick or event still in flight from the bridge just
// left would land in the next one and re-arm a second timer/wait chain.
func nextModelID() int { return int(modelCounter.Add(1)) }

// staleOwner reports whether an async message belongs to a different Model
// than m. A zero owner (messages built by tests, or by code that predates
// ownership) is never stale.
func (m *Model) staleOwner(owner int) bool { return owner != 0 && owner != m.id }

// Shutdown releases everything this Model started, so leaving a bridge for
// the home screen leaves no goroutine or connection behind (spec §5, "sin
// fugas"): it cancels the context the pending subscription wait runs under,
// closes the subscription (which closes the underlying /v1/watch response
// body) and closes the transport. It is idempotent.
func (m *Model) Shutdown() {
	if m.cancelEvents != nil {
		m.cancelEvents()
	}
	if m.events != nil {
		m.events.Close()
		m.events = nil
	}
	if m.transport != nil {
		m.transport.Close()
	}
}

// takePendingCmd returns (and clears) the tea.Cmd a synchronous action (a
// palette item) asked for — runPaletteItem itself has no way to return one.
func (m *Model) takePendingCmd() tea.Cmd {
	cmd := m.pendingCmd
	m.pendingCmd = nil
	return cmd
}

// shortInstance abbreviates an instance_id for display (8 characters, like
// the status bar).
func shortInstance(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// eventsClosedOrNil reports whether the model no longer holds a live
// subscription (after Shutdown).
func (m *Model) eventsClosedOrNil() bool { return m.events == nil }
