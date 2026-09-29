// help.go implements §6.6's help overlay as a floating, scrollable window
// (spec's overlay rewrite: "la ayuda (flotante, desplazable si no cabe)"),
// replacing the earlier full-screen treatment.
package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// helpWindowMaxWidth caps the help window's width so its lines stay
// readable even on a very wide terminal.
const helpWindowMaxWidth = 72

// bridgeHelpContexts are the keymap contexts the bridge view's help lists.
var bridgeHelpContexts = []string{"Global", "Conversación", "Composer"}

func (m *Model) openHelp() {
	m.closeOverlays()
	m.showHelp = true
	m.helpScroll = 0
}

// handleHelpKey routes every keypress while help is open: when its
// content fits entirely on screen, any key closes it (the original,
// simplest behavior); once it needs to scroll, only esc/q/enter/? close
// it, and up/down/j/k/pgup/pgdown scroll instead, so a person paging
// through it doesn't close it by accident on the very key they're using to
// read the rest of it.
func (m *Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	lines := strings.Split(m.keymap.HelpFor(bridgeHelpContexts...), "\n")
	maxHeight := m.height - 6
	if maxHeight < 3 {
		maxHeight = 3
	}
	scrollable := len(lines) > maxHeight
	if !scrollable {
		m.showHelp = false
		return m, nil
	}
	maxScroll := len(lines) - maxHeight
	switch msg.String() {
	case "esc", "q", "enter", "?":
		m.showHelp = false
		return m, nil
	case "up", "k":
		if m.helpScroll > 0 {
			m.helpScroll--
		}
	case "down", "j":
		if m.helpScroll < maxScroll {
			m.helpScroll++
		}
	case "pgup":
		m.helpScroll -= maxHeight
		if m.helpScroll < 0 {
			m.helpScroll = 0
		}
	case "pgdown":
		m.helpScroll += maxHeight
		if m.helpScroll > maxScroll {
			m.helpScroll = maxScroll
		}
	}
	return m, nil
}

// helpWindow renders the help overlay as a floating window (§6.6),
// scrolled to m.helpScroll and centered like every other overlay.
func (m *Model) helpWindow() floatingWindow {
	full := strings.Split(m.keymap.HelpFor(bridgeHelpContexts...), "\n")
	innerWidth := clampInt(m.width-4, 20, helpWindowMaxWidth)
	maxHeight := m.height - 6
	if maxHeight < 3 {
		maxHeight = 3
	}

	title := "Ayuda"
	scrollable := len(full) > maxHeight
	if scrollable {
		title = "Ayuda (↑/↓ desplaza · esc cierra)"
	} else {
		title = "Ayuda (cualquier tecla o clic cierra)"
	}

	visible := full
	if scrollable {
		end := m.helpScroll + maxHeight
		if end > len(full) {
			end = len(full)
		}
		visible = full[m.helpScroll:end]
	}

	lines := make([]string, 0, len(visible)+2)
	lines = append(lines, padLine(title, innerWidth), padLine("", innerWidth))
	for _, l := range visible {
		lines = append(lines, padLine(l, innerWidth))
	}

	content, outerWidth, outerHeight := windowBox(m.th, true, innerWidth, lines)
	x, y := centerWindow(m.width, m.height, outerWidth, outerHeight)
	return floatingWindow{content: content, x: x, y: y, width: outerWidth, height: outerHeight}
}
