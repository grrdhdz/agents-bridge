// dialog.go implements §6.7's confirmation modal for closing the bridge: it
// supersedes /stop's own double-keypress prompt as the primary way to
// reach that action (from the command palette), while /stop's double
// keypress keeps working exactly as before (spec: "sustituyen la doble
// pulsación de /stop, que se mantiene como atajo equivalente").
package tui

import (
	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"
)

// confirmDialog is one pending yes/no confirmation. onYes runs against the
// *Model when confirmed; there is currently only ever one kind (closing
// the bridge), but the type stays generic so a future destructive action
// can reuse it without inventing a second mechanism.
type confirmDialog struct {
	message string
	onYes   func(*Model)
}

// openConfirmCloseDialog is the palette's "Cerrar puente" action. Unlike
// /stop's mode-dependent confirmation (§4: host/join own their bridge
// outright and close immediately), this explicit entry point always asks
// first, since choosing it from the palette is already a deliberate,
// separate action from typing /stop.
func (m *Model) openConfirmCloseDialog() {
	if m.onStop == nil {
		m.pushToast("este endpoint no puede cerrar el puente")
		return
	}
	m.confirm = &confirmDialog{
		message: "¿Cerrar el puente? Esta acción es irreversible.",
		onYes: func(mm *Model) {
			if mm.caps.CloseOnQuit {
				mm.close()
				return
			}
			mm.onStop()
			mm.pushToast("puente cerrado")
		},
	}
}

// handleDialogKey routes every keypress while a confirmation is open: y/
// enter confirms, n/esc cancels, anything else is ignored so a stray
// keystroke can never accidentally confirm a destructive action.
func (m *Model) handleDialogKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		dialog := m.confirm
		m.confirm = nil
		if dialog != nil && dialog.onYes != nil {
			dialog.onYes(m)
		}
		if m.state == "closed" {
			return m, tea.Quit
		}
		return m, nil
	case "n", "esc":
		m.confirm = nil
		return m, nil
	}
	return m, nil
}

// dialogWindow renders the confirmation modal (§6.7) as a small, centered
// floating window with two clickable buttons (spec's mouse addendum:
// "clic en los botones \"Cerrar\"/\"Cancelar\""), registering one region
// per button in absolute screen coordinates. Clicking anywhere else on the
// screen closes it without confirming (handleMouseClick's generic "click
// missed everything" fallback).
func (m *Model) dialogWindow() floatingWindow {
	closeLabel := "[ Cerrar puente ]"
	cancelLabel := "[ Cancelar ]"
	buttons := closeLabel + "    " + cancelLabel
	help := "y/enter confirma · n/esc cancela"

	innerWidth := lipgloss.Width(m.confirm.message)
	for _, s := range []string{buttons, help} {
		if w := lipgloss.Width(s); w > innerWidth {
			innerWidth = w
		}
	}
	innerWidth = clampInt(innerWidth, 30, m.width-4)

	lines := []string{
		padLine(m.confirm.message, innerWidth),
		padLine("", innerWidth),
		padLine(buttons, innerWidth),
		padLine("", innerWidth),
		padLine(help, innerWidth),
	}
	const buttonsRow = 2

	content, outerWidth, outerHeight := windowBox(m.th, true, innerWidth, lines)
	x, y := centerWindow(m.width, m.height, outerWidth, outerHeight)

	closeStart := x + 1 + 1
	closeEnd := closeStart + len(closeLabel) - 1
	cancelStart := closeEnd + 1 + 4
	cancelEnd := cancelStart + len(cancelLabel) - 1
	screenY := y + 1 + buttonsRow
	m.addRegion(screenY, closeStart, closeEnd, func(mm *Model) (tea.Model, tea.Cmd) {
		return mm.handleDialogKey(tea.KeyPressMsg{Text: "y", Code: 'y'})
	})
	m.addRegion(screenY, cancelStart, cancelEnd, func(mm *Model) (tea.Model, tea.Cmd) {
		return mm.handleDialogKey(tea.KeyPressMsg{Text: "n", Code: 'n'})
	})

	return floatingWindow{content: content, x: x, y: y, width: outerWidth, height: outerHeight}
}
