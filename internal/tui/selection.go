// selection.go implements §6.3's "Selección" and "Búsqueda": moving the
// conversation's keyboard focus between messages, folding/unfolding one,
// copying its body, and jumping between search matches. It also owns the
// sidebar's ctrl+b toggle (§6.4), which is simple enough not to need its
// own file.
package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/internal/protocol"
	"github.com/grrdhdz/agents-bridge/internal/tui/theme"
)

// selectedIndex resolves m.selected to a message index, defaulting to the
// last message (the common case: focus the conversation while following
// the tail) when nothing has been explicitly selected yet, or when the
// previously selected id no longer exists (e.g. a fresh Model in a test).
func (m *Model) selectedIndex() int {
	if len(m.messages) == 0 {
		return -1
	}
	if m.selected == "" {
		return len(m.messages) - 1
	}
	if idx, ok := m.byID[m.selected]; ok {
		return idx
	}
	return len(m.messages) - 1
}

func (m *Model) moveSelection(delta int) {
	idx := m.selectedIndex()
	if idx < 0 {
		return
	}
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx > len(m.messages)-1 {
		idx = len(m.messages) - 1
	}
	m.selected = m.messages[idx].MessageID
	m.refreshViewport(false)
	m.scrollSelectedIntoView()
}

func (m *Model) selectFirst() {
	if len(m.messages) == 0 {
		return
	}
	m.selected = m.messages[0].MessageID
	m.refreshViewport(false)
	m.scrollSelectedIntoView()
}

func (m *Model) selectLast() {
	if len(m.messages) == 0 {
		return
	}
	m.selected = m.messages[len(m.messages)-1].MessageID
	m.refreshViewport(false)
	m.scrollSelectedIntoView()
}

// scrollSelectedIntoView nudges the viewport's Y offset just enough to
// bring the selected card fully into view, using the line offsets
// refreshViewport last computed. It never re-centers gratuitously — if the
// card is already visible, the viewport does not move.
func (m *Model) scrollSelectedIntoView() {
	idx := m.selectedIndex()
	if idx < 0 || idx >= len(m.messageOffsets) {
		return
	}
	line := m.messageOffsets[idx]
	top := m.viewport.YOffset()
	height := m.viewport.Height()
	if height <= 0 {
		return
	}
	if line < top {
		m.viewport.SetYOffset(line)
	} else if line >= top+height {
		m.viewport.SetYOffset(line - height + 1)
	}
}

// cardContentWidth mirrors renderCardWithParams' own width bookkeeping, so
// toggleFold can render the same body it will actually display and decide
// its real (not guessed) line count. Every card — compact or not — is now
// a bordered box (round 5), so the border+padding overhead (cardBoxOverhead)
// always applies, not just in the non-compact case.
func (m *Model) cardContentWidth() int {
	width := cardMaxWidth(m.viewport.Width(), m.compact()) - cardBoxOverhead
	if width < 4 {
		width = 4
	}
	return width
}

// isCardFoldable/isCardFolded share one message's fold bookkeeping between
// the keyboard (toggleFold) and the mouse (handleCardClick, mouse.go):
// whether a message *can* fold at all (its rendered body exceeds
// foldThreshold lines, or it has an explicit expand override — a message
// once expanded past the threshold can still be re-collapsed even though
// collapsing it does not depend on measuring it again), and whether it
// *is* currently folded (the length-based default, overridden by an
// explicit expanded/collapsed entry).
func (m *Model) isCardFoldable(e protocol.Envelope) bool {
	_, body := splitLabel(e.Body)
	if body == "" {
		return false
	}
	if m.expanded[e.MessageID] || m.collapsed[e.MessageID] {
		return true
	}
	rendered := m.mdCache.render(e.MessageID, body, m.th, m.cardContentWidth(), e.SenderRole)
	return strings.Count(rendered, "\n")+1 > foldThreshold
}

func (m *Model) isCardFolded(e protocol.Envelope) bool {
	if !m.isCardFoldable(e) {
		return false
	}
	_, body := splitLabel(e.Body)
	rendered := m.mdCache.render(e.MessageID, body, m.th, m.cardContentWidth(), e.SenderRole)
	folded := strings.Count(rendered, "\n")+1 > foldThreshold
	if m.expanded[e.MessageID] {
		folded = false
	}
	if m.collapsed[e.MessageID] {
		folded = true
	}
	return folded
}

// setCardFolded pins e's fold state to want, via the same expanded/
// collapsed override maps toggleFold and the fold indicator's click both
// use.
func (m *Model) setCardFolded(messageID string, want bool) {
	if want {
		m.collapsed[messageID] = true
		delete(m.expanded, messageID)
	} else {
		m.expanded[messageID] = true
		delete(m.collapsed, messageID)
	}
	m.refreshViewport(false)
}

// toggleFold implements §6.3's "enter pliega/despliega" for the currently
// selected message.
func (m *Model) toggleFold() {
	idx := m.selectedIndex()
	if idx < 0 {
		return
	}
	e := m.messages[idx]
	if !m.isCardFoldable(e) {
		return
	}
	m.setCardFolded(e.MessageID, !m.isCardFolded(e))
}

// copySelected copies the selected message's body (label line already
// stripped) to the clipboard (§6.3 "y copia el cuerpo"), reporting success
// or failure as a toast (§6.7).
func (m *Model) copySelected() {
	idx := m.selectedIndex()
	if idx < 0 {
		return
	}
	_, body := splitLabel(m.messages[idx].Body)
	m.copyText(body, "mensaje copiado al portapapeles")
}

// copyText is the shared clipboard path for every copy action (message
// body, instance_id, join command from the palette): it always ends in a
// toast, success or failure, per §6.7.
func (m *Model) copyText(text, successToast string) {
	if m.copyCommand == nil {
		m.pushToast("no se pudo copiar: sin comando de portapapeles disponible")
		return
	}
	if err := m.copyCommand(text); err != nil {
		m.pushToast("no se pudo copiar: " + err.Error())
		return
	}
	m.pushToast(successToast)
}

func (m *Model) pushToast(text string) {
	m.toasts = append(m.toasts, toast{text: text, expiresAt: m.now().Add(toastTTL)})
	m.relayoutFooter()
}

// openSearch opens §6.3's search field, closing any other overlay first so
// at most one is ever active.
func (m *Model) openSearch() {
	m.closeOverlays()
	m.searchActive = true
	m.searchInput.SetValue(m.searchQuery)
	m.searchInput.Focus()
	m.relayoutFooter()
}

func (m *Model) closeOverlays() {
	m.paletteOpen = false
	m.showHelp = false
	if m.searchActive {
		m.searchActive = false
		m.searchInput.Blur()
	}
}

// handleSearchKey routes every keypress to the search field while it is
// active, except esc (cancel: clears the query and its highlights) and
// enter (commit: closes the field but keeps the query live for n/N and
// highlighting).
func (m *Model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searchActive = false
		m.searchInput.Blur()
		m.searchQuery = ""
		m.refreshViewport(false)
		m.relayoutFooter()
		return m, nil
	case "enter":
		m.searchActive = false
		m.searchInput.Blur()
		m.searchQuery = m.searchInput.Value()
		m.searchIdx = -1
		m.focus = focusConversation
		m.jumpSearch(1)
		m.relayoutFooter()
		return m, nil
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.searchQuery = m.searchInput.Value()
	m.refreshViewport(false)
	return m, cmd
}

// jumpSearch moves to the delta-th next/previous match, wrapping around,
// and selects (and scrolls to) it. delta is +1 for n, -1 for N; the very
// first jump after committing a search starts searchIdx at -1 so delta=1
// lands on match 0.
func (m *Model) jumpSearch(delta int) {
	matches := searchMatches(m.messages, m.searchQuery)
	if len(matches) == 0 {
		return
	}
	m.searchIdx = ((m.searchIdx+delta)%len(matches) + len(matches)) % len(matches)
	idx := matches[m.searchIdx]
	m.selected = m.messages[idx].MessageID
	m.refreshViewport(false)
	m.scrollSelectedIntoView()
}

// sidebarVisible reports whether the side panel (§6.4) is currently shown:
// ctrl+b's explicit override when set, otherwise the width breakpoint.
func (m *Model) sidebarVisible() bool {
	if m.sidebarOverride != nil {
		return *m.sidebarOverride
	}
	return m.width >= sidebarMinWidth
}

func (m *Model) toggleSidebar() {
	visible := !m.sidebarVisible()
	m.sidebarOverride = &visible
	m.resize()
}

// noColorThemeNotice explains why "Cambiar tema" has no visible effect: with
// NO_COLOR every theme renders without color, so there is nothing to switch
// to, and silently doing nothing looked like a broken light theme.
const noColorThemeNotice = "Colores desactivados por NO_COLOR: el tema no se puede cambiar"

// toggleTheme flips dark/light (the command palette's "cambiar tema";
// §6.8's own --theme/env var selection is unaffected, this is a live,
// in-session override).
func (m *Model) toggleTheme() {
	if m.th.NoColor {
		m.pushToast(noColorThemeNotice)
		return
	}
	mode := theme.ModeLight
	if m.th.Mode == theme.ModeLight {
		mode = theme.ModeDark
	}
	m.applyTheme(theme.New(mode, m.th.NoColor, nil))
}

// applyTheme installs th and re-styles everything that caches styles.
func (m *Model) applyTheme(th theme.Theme) {
	m.th = th
	m.input.SetStyles(textAreaStyles(m.th))
	m.searchInput.SetStyles(textInputStyles(m.th))
	m.refreshViewport(false)
}

// autoThemeFor settles an undecided auto theme with the terminal's answer.
func autoThemeFor(th theme.Theme, msg tea.BackgroundColorMsg) theme.Theme {
	return theme.New(theme.ModeAuto, th.NoColor, msg.IsDark)
}
