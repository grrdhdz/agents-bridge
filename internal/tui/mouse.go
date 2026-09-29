// mouse.go dispatches every mouse event Update receives to the region
// hitregion.go recorded while View() last rendered the screen (spec's
// mouse addendum): a click runs whatever that region's onClick closure
// does, a wheel event scrolls whichever pane the pointer is over, and a
// click that lands on nothing closes whichever overlay is currently open
// (palette, dialog or help) — "clic fuera... los cierra".
package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// shortcutAction pairs one shortcuts-bar entry's exact rendered label (the
// same "key desc" text keys.Map.Bar renders — see shortcutsBar, view.go)
// with the action a click on it runs, so the two can never drift apart:
// whatever text is visible is exactly what runs.
type shortcutAction struct {
	label string
	run   func(*Model) (tea.Model, tea.Cmd)
}

// shortcutActions lists every binding shortcutsBar can show, in the same
// order and context (Global + whichever pane has focus) it uses — a
// region is only ever registered for one that Bar() actually rendered
// (view.go looks each label up in the rendered text and skips any that
// were cut for space), so a click can never run something invisible.
func (m *Model) shortcutActions() []shortcutAction {
	var actions []shortcutAction
	add := func(b key.Binding, run func(*Model) (tea.Model, tea.Cmd)) {
		h := b.Help()
		actions = append(actions, shortcutAction{label: h.Key + " " + h.Desc, run: run})
	}
	add(m.keymap.Global.FocusToggle, func(mm *Model) (tea.Model, tea.Cmd) { mm.toggleFocus(); return mm, nil })
	add(m.keymap.Global.HelpToggle, func(mm *Model) (tea.Model, tea.Cmd) { mm.openHelp(); return mm, nil })
	add(m.keymap.Global.Quit, func(mm *Model) (tea.Model, tea.Cmd) { mm.close(); return mm, tea.Quit })
	if m.caps.ReturnHome {
		add(m.keymap.Global.Home, func(mm *Model) (tea.Model, tea.Cmd) { return mm, returnHomeCmd("") })
	}
	add(m.keymap.Global.SidebarToggle, func(mm *Model) (tea.Model, tea.Cmd) { mm.toggleSidebar(); return mm, nil })
	add(m.keymap.Global.Search, func(mm *Model) (tea.Model, tea.Cmd) { mm.openSearch(); return mm, nil })
	add(m.keymap.Global.Palette, func(mm *Model) (tea.Model, tea.Cmd) { mm.openPalette(); return mm, nil })
	if m.focus == focusConversation {
		add(m.keymap.Conversation.Help, func(mm *Model) (tea.Model, tea.Cmd) { mm.openHelp(); return mm, nil })
		add(m.keymap.Conversation.ToggleFold, func(mm *Model) (tea.Model, tea.Cmd) { mm.toggleFold(); return mm, nil })
		add(m.keymap.Conversation.Copy, func(mm *Model) (tea.Model, tea.Cmd) { mm.copySelected(); return mm, nil })
		add(m.keymap.Conversation.SearchNext, func(mm *Model) (tea.Model, tea.Cmd) { mm.jumpSearch(1); return mm, nil })
		add(m.keymap.Conversation.SearchPrev, func(mm *Model) (tea.Model, tea.Cmd) { mm.jumpSearch(-1); return mm, nil })
	} else {
		add(m.keymap.Composer.Send, func(mm *Model) (tea.Model, tea.Cmd) {
			mm.submit()
			mm.relayoutFooter()
			if mm.state == "closed" {
				return mm, tea.Quit
			}
			return mm, nil
		})
		add(m.keymap.Composer.CycleLabel, func(mm *Model) (tea.Model, tea.Cmd) { mm.cycleLabel(); return mm, nil })
	}
	if m.caps.Pair {
		actions = append(actions, shortcutAction{label: "f5 recopiar comando de unión", run: func(mm *Model) (tea.Model, tea.Cmd) {
			mm.copyJoinCommand()
			return mm, nil
		}})
	}
	actions = append(actions, shortcutAction{label: "? más", run: func(mm *Model) (tea.Model, tea.Cmd) { mm.openHelp(); return mm, nil }})
	return actions
}

func (m *Model) handleMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseLeft {
		return m, nil
	}
	if region := m.regionAt(mouse.X, mouse.Y); region != nil {
		return region.onClick(m)
	}
	// Nothing was drawn at that point: an open overlay treats any other
	// click as "close me" (its own buttons/items are already handled by a
	// region above, so reaching here means the click missed all of them).
	switch {
	case m.confirm != nil:
		m.confirm = nil
	case m.paletteOpen:
		m.paletteOpen = false
	case m.showHelp:
		m.showHelp = false
	}
	return m, nil
}

// handleMouseWheel scrolls whichever pane the pointer is over, without
// changing keyboard focus (spec: "sin cambiar el foco"). The sidebar has
// no scroll state of its own yet (§6.4's panel is short, fixed content),
// so a wheel over it is a documented no-op rather than silently scrolling
// the conversation underneath the pointer.
func (m *Model) handleMouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if !m.pointInConvBox(mouse.X, mouse.Y) {
		return m, nil
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// pointInConvBox reports whether (x, y) falls inside the conversation
// box's interior (i.e. the viewport itself), using the same geometry
// resize()/View() already computed this frame.
func (m *Model) pointInConvBox(x, y int) bool {
	top := convBoxTop
	left := convBoxLeft
	return x > left && x < left+m.convBoxWidth-1 && y > top && y < top+m.convBoxHeight-1
}

// convBoxTop/convBoxLeft are the screen row/column the conversation box's
// own top-left corner (the border itself) is drawn at. They are constants,
// not fields, because every screen this Model draws places the status bar
// at row 0, a blank row at row 1, and the conversation box flush against
// the left edge starting at row 2 — nothing in this layout ever moves that
// anchor point, only the box's own width/height (already tracked in
// m.convBoxWidth/Height).
const (
	convBoxTop  = 2
	convBoxLeft = 0
)

// cardClickLine is what a click needs to know about the conversation row
// it landed on: which message it belongs to, and the row's own rendered
// text (to tell a fold indicator line and a header line apart).
type cardClickLine struct {
	messageIndex int
	isHeader     bool
	text         string
}

// resolveCardClick maps a viewport-relative row (0-based, i.e. already
// adjusted for the box's border and the pointer's offset within it) to the
// message it belongs to, using the same messageOffsets refreshViewport
// last computed plus the viewport's own scroll position — the single
// source of truth for "which message is at this line" (no separate click
// math duplicating what was rendered).
func (m *Model) resolveCardClick(viewportRow int) (cardClickLine, bool) {
	if len(m.messageOffsets) == 0 {
		return cardClickLine{}, false
	}
	sourceLine := m.viewport.YOffset() + viewportRow
	idx := -1
	for i, offset := range m.messageOffsets {
		if offset <= sourceLine {
			idx = i
		} else {
			break
		}
	}
	if idx < 0 {
		return cardClickLine{}, false
	}
	var text string
	if sourceLine >= 0 && sourceLine < len(m.conversationLines) {
		text = m.conversationLines[sourceLine]
	}
	return cardClickLine{messageIndex: idx, isHeader: sourceLine == m.messageOffsets[idx], text: text}, true
}

// onCardRowClick is what every card-row region (registered while View
// renders the conversation) runs: it always selects that message and
// gives the conversation focus (spec item 1: "clic en una tarjeta la
// selecciona y pasa el foco"), and additionally toggles its fold state
// when the click landed on the fold indicator (expand) or on an already
// expanded message's header (collapse) — spec's mouse addendum, second
// bullet.
func (m *Model) onCardRowClick(row cardClickLine) (tea.Model, tea.Cmd) {
	if row.messageIndex < 0 || row.messageIndex >= len(m.messages) {
		return m, nil
	}
	e := m.messages[row.messageIndex]
	m.focus = focusConversation
	m.selected = e.MessageID
	switch {
	case ansiContainsFoldIndicator(row.text):
		m.setCardFolded(e.MessageID, false)
	case row.isHeader && m.isCardFoldable(e) && !m.isCardFolded(e):
		m.setCardFolded(e.MessageID, true)
	default:
		m.refreshViewport(false)
	}
	return m, nil
}

// ansiContainsFoldIndicator reports whether a rendered conversation line is
// the "▸ N líneas más" fold trailer (applyFold, markdown.go). Matching on
// the leading glyph rather than the whole localized phrase keeps this
// robust to the exact wording.
func ansiContainsFoldIndicator(line string) bool {
	return containsRune(line, '▸')
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// onComposerClick focuses the composer and best-effort places the cursor
// at the clicked row/column (spec item "clic en el composer: le da el foco
// y coloca el cursor"). Row/col are relative to the textarea's own content
// (border and prompt already subtracted by the caller). textarea.Model has
// no direct "set cursor to (line, col)": CursorUp/Down move relative to
// its *current* line, so this walks from wherever the cursor already is.
func (m *Model) onComposerClick(row, col int) (tea.Model, tea.Cmd) {
	if m.focus != focusComposer {
		m.focus = focusComposer
		m.input.Focus()
	}
	delta := row - m.input.Line()
	for ; delta > 0; delta-- {
		m.input.CursorDown()
	}
	for ; delta < 0; delta++ {
		m.input.CursorUp()
	}
	if col < 0 {
		col = 0
	}
	m.input.SetCursorColumn(col)
	m.refreshViewport(false)
	return m, nil
}
