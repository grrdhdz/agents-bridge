package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

// modeLabel names Capabilities.Mode for the status bar and window title.
func modeLabel(mode string) string {
	switch mode {
	case "host":
		return "host"
	case "join":
		return "join"
	case "local":
		return "local"
	case "tui":
		return "observador"
	default:
		return mode
	}
}

func peerRole(localRole protocol.Role) protocol.Role {
	if localRole == protocol.RoleExecutor {
		return protocol.RoleOrchestrator
	}
	return protocol.RoleExecutor
}

// connState maps Model.state (deduced purely from transport/frame events —
// §7's StatusProvider poll is phase 2) to the status bar's connection
// indicator.
func connState(state string) string {
	switch state {
	case "connected":
		return "connected"
	case "reconnecting":
		return "reconnecting"
	default:
		return "disconnected"
	}
}

// formatIdle renders an elapsed duration the way the status bar shows it
// (§6.2's "inactivo Xm"), rounded to whole seconds so it is stable for
// golden snapshots when the clock is injected.
func formatIdle(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	if minutes > 0 {
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// statusBar renders §6.2: short instance id, mode, the other role's
// connection indicator, and idle time since the last observed activity.
func (m *Model) statusBar() string {
	shortID := m.transport.InstanceID()
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	indicator := m.th.ConnIndicator(connState(m.state))
	idle := formatIdle(m.now().Sub(m.lastActivity))
	line := fmt.Sprintf("codex-bridge %s · %s · %s %s · inactivo %s", shortID, modeLabel(m.caps.Mode), indicator, roleName(peerRole(m.localRole)), idle)
	return m.th.StatusBarStyle().Width(m.width).Render(line)
}

// shortcutsBarLine is the shortcuts strip's plain text (§3 amended: always
// a single line, generated from the central keymap so it can never
// advertise a key Update does not actually handle, showing as many
// bindings as fit by priority and always ending in "? más"), with no side
// effects — footerLineCount (app.go) calls this just to measure height,
// before the real screen row it will land on is known, so it must never
// register click regions (shortcutsBar, below, does that once the row is
// final).
func (m *Model) shortcutsBarLine() string {
	groups := []string{"Global", "Composer"}
	if m.focus == focusConversation {
		groups = []string{"Global", "Conversación"}
	}
	var extra []string
	if m.caps.Pair {
		extra = append(extra, "f5 recopiar comando de unión")
	}
	return m.keymap.Bar(m.width, extra, groups...)
}

// shortcutsBar renders the shortcuts strip and registers a click region
// for every entry it actually managed to fit (spec's mouse addendum: "clic
// en un atajo ejecuta esa acción"). row is the absolute screen row this
// bar is drawn on.
func (m *Model) shortcutsBar(row int) string {
	line := m.shortcutsBarLine()
	m.registerShortcutRegions(row, line)
	return lipgloss.NewStyle().Width(m.width).Render(line)
}

// registerShortcutRegions locates each of shortcutActions' labels inside
// the already-rendered bar text and registers a click region over exactly
// that span — a label the bar had no room to show (Bar's own width
// budgeting cut it) is simply not found and gets no region, so a click can
// never run something that is not actually visible.
func (m *Model) registerShortcutRegions(row int, rendered string) {
	cursor := 0
	for _, action := range m.shortcutActions() {
		idx := strings.Index(rendered[cursor:], action.label)
		if idx < 0 {
			continue
		}
		start := cursor + idx
		end := start + len(action.label) - 1
		run := action.run
		m.addRegion(row, start, end, run)
		cursor = end + 1
	}
}

// unreadBar renders §6.3's "↓ N mensajes nuevos" follow indicator, shown
// only while there is an unread backlog below the viewport.
func (m *Model) unreadBar() string {
	if m.unread <= 0 {
		return ""
	}
	text := fmt.Sprintf("↓ %d mensajes nuevos (G para bajar)", m.unread)
	return m.th.NoticeStyle().Render(text)
}

// View renders the whole screen. Overlays (the command palette, the
// confirmation dialog, help) and toasts are *floating windows* composited
// over the normal screen with lipgloss v2's own layer compositor
// (overlay.go's compositeWindows), not full-screen replacements — the
// conversation and everything else stays visible behind them, like a real
// floating window manager (spec's overlay rewrite). Every click region is
// rebuilt from scratch on every call (m.regions = nil): the backdrop's own
// regions (cards, composer, shortcuts) are registered first, but if a
// modal overlay is open they are discarded before it registers its own —
// a click "behind" an open palette or dialog must never reach the card or
// button it happens to land on, only the overlay's own controls (or the
// generic "click missed everything closes the overlay" fallback in
// handleMouseClick).
func (m *Model) View() tea.View {
	m.regions = nil
	backdrop := m.backdropScreen()

	var windows []floatingWindow
	switch {
	case m.paletteOpen:
		m.regions = nil
		windows = append(windows, m.paletteWindow())
	case m.confirm != nil:
		m.regions = nil
		windows = append(windows, m.dialogWindow())
	case m.showHelp:
		m.regions = nil
		windows = append(windows, m.helpWindow())
	}
	// Toasts float over the conversation regardless of a modal overlay
	// (§6.7): they carry no click regions of their own, so they never
	// need the same "wipe regions first" treatment.
	if w, ok := m.toastWindow(); ok {
		windows = append(windows, w)
	}

	content := composeScreen(m.th, m.width, m.height, backdrop, windows)
	return newScreenView(content, m.th, "codex-bridge "+modeLabel(m.caps.Mode)+" "+m.transport.InstanceID())
}

// composeScreen turns one screen's backdrop plus its floating windows into
// the final frame, shared by every screen (bridge view and home).
// paintFrameBackground (backgroundpaint.go) is round 5's fix for a real
// report: a person testing --theme dark *inside the app's own terminal
// panel* (an xterm.js-based embedded terminal) saw the screen still all
// white, because that terminal ignores tea.View.BackgroundColor (see
// newScreenView). This pass explicitly paints every visible cell's
// background over the final, fully composited frame — Surface by default,
// SurfaceRaised for the status bar row and each floating window's own
// rectangle — so the result is correct even in a terminal that supports no
// background-setting mechanism beyond plain per-cell SGR codes, which every
// terminal supports. Never under NO_COLOR.
func composeScreen(th theme.Theme, width, height int, backdrop string, windows []floatingWindow) string {
	content := backdrop
	if len(windows) > 0 {
		content = compositeWindows(backdrop, width, height, windows...)
	}
	if !th.NoColor {
		zones := []bgZone{{x: 0, y: 0, w: width, h: 1, bg: th.SurfaceRaised}}
		for _, w := range windows {
			zones = append(zones, bgZone{x: w.x, y: w.y, w: w.width, h: w.height, bg: th.SurfaceRaised})
		}
		content = paintFrameBackground(content, width, height, th.Surface, zones)
	}
	return content
}

// newScreenView wraps a composed frame in the tea.View every screen returns.
func newScreenView(content string, th theme.Theme, title string) tea.View {
	view := tea.NewView(content)
	view.MouseMode = tea.MouseModeCellMotion
	view.AltScreen = true
	view.WindowTitle = title
	// The whole terminal's background is *also* set to the theme's own
	// Surface for as long as the program runs, for terminals that do
	// honor it (a real optimization there: no per-cell SGR codes needed at
	// all). bubbletea's renderer restores it automatically on shutdown
	// (ctrl+c, /quit, an external stop — anything that ends the Program's
	// normal Run loop): see cursedRenderer.close(), which emits the ANSI
	// reset exactly when BackgroundColor was set, with no extra cleanup
	// needed here. This is deliberately *not* the only mechanism (see
	// composeScreen) — a real report showed it does nothing in at least
	// one real terminal. NO_COLOR leaves it nil, matching every other
	// themed style in this package.
	if !th.NoColor {
		view.BackgroundColor = th.Surface
	}
	return view
}

// backdropScreen renders the normal screen (status bar, conversation,
// footer, composer) — always exactly m.height lines of exactly m.width
// columns each (layout_test.go), which compositeWindows' canvas requires.
// It always registers the backdrop's own click regions; View clears them
// again before adding a modal overlay's.
func (m *Model) backdropScreen() string {
	var b strings.Builder
	b.WriteString(m.statusBar())
	b.WriteString("\n\n")
	b.WriteString(m.middleRow())
	b.WriteString("\n")
	row := convBoxTop + m.convBoxHeight + 1
	if m.searchActive {
		b.WriteString(m.searchBar())
		b.WriteString("\n")
		row++
	}
	if bar := m.unreadBar(); bar != "" {
		b.WriteString(bar)
		b.WriteString("\n")
		row++
	}
	b.WriteString(m.shortcutsBar(row))
	row++
	if m.error != "" {
		b.WriteString("\n")
		b.WriteString(m.error)
		row++
	}
	if m.copyInfo != "" && m.caps.Pair {
		b.WriteString("\n")
		b.WriteString(m.copyInfo)
		row++
	}
	b.WriteString("\n")
	row++
	b.WriteString(m.composerView(row))
	return b.String()
}

// middleRow lays out §6.1's middle row: the conversation in its own
// bordered box (border color reflects focus — spec item 1's fix: focus was
// tracked internally but never actually *shown* on the conversation's own
// pane, only the composer's, which is why toggling it read as "nothing
// happened"), plus the sidebar (§6.4) in a second bordered, padded box
// with a real gap between them (spec item 3's fix), once the terminal is
// wide enough (or ctrl+b forces it open). It also registers one click
// region per visible conversation row (spec's mouse addendum, item 1b),
// mapping screen rows back to messages via resolveCardClick.
func (m *Model) middleRow() string {
	convActive := m.focus == focusConversation
	convBox := m.th.BorderStyle(convActive).Border(lipgloss.RoundedBorder()).Render(m.viewport.View())
	m.registerCardRegions()
	if !m.sidebarBoxOn {
		return convBox
	}
	data := m.buildSidebarData()
	sideContent := renderSidebar(data, m.th, sidebarWidth)
	// The content is padded/truncated to exactly the box's interior height
	// *before* the border is applied, rather than leaning on lipgloss's
	// own Height()+MaxHeight() to do it: MaxHeight truncates the whole
	// bordered block from the bottom, which — when the raw content
	// overflowed the interior height — chopped off the closing border row
	// itself instead of just the excess content, leaving the sidebar box
	// looking permanently "open". Fitting the content first sidesteps
	// that ordering problem entirely.
	sideContent = fitLines(sideContent, m.convBoxHeight-2)
	// Style.Width sets the box's *total* rendered width, border and
	// padding included (lipgloss subtracts them internally to get the
	// content's own wrap width) — so this must be the sidebar's outer
	// width (m.sidebarBoxWidth), not sidebarWidth (the *content* width
	// already baked into sideContent by renderSidebar).
	sideBox := m.th.BorderStyle(false).Border(lipgloss.RoundedBorder()).Padding(0, 1).
		Width(m.sidebarBoxWidth).MaxWidth(m.sidebarBoxWidth).
		Render(sideContent)
	gap := strings.Repeat(" ", 2)
	return lipgloss.JoinHorizontal(lipgloss.Top, convBox, gap, sideBox)
}

// registerCardRegions registers one click region per visible row of the
// conversation viewport (spec's mouse addendum): the region's row is the
// screen row that row is actually drawn at (the box's top-left corner is
// convBoxTop/convBoxLeft, plus 1 for the border), its columns span the
// viewport's own interior width, and its action is resolveCardClick's
// result run through onCardRowClick.
func (m *Model) registerCardRegions() {
	height := m.viewport.Height()
	width := m.viewport.Width()
	for r := 0; r < height; r++ {
		row, ok := m.resolveCardClick(r)
		if !ok {
			continue
		}
		screenY := convBoxTop + 1 + r
		screenX0 := convBoxLeft + 1
		screenX1 := screenX0 + width - 1
		m.addRegion(screenY, screenX0, screenX1, func(mm *Model) (tea.Model, tea.Cmd) {
			return mm.onCardRowClick(row)
		})
	}
}

// fitLines pads or truncates content to exactly height lines, so a box
// built around it (border, padding) always closes at a predictable row
// regardless of how much the content itself produced.
func fitLines(content string, height int) string {
	if height < 0 {
		height = 0
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// searchBar renders §6.3's search field while it is being edited.
func (m *Model) searchBar() string {
	return "Buscar: " + m.searchInput.View()
}

// composerView wraps the composer's textarea in a border (Part A §3: "el
// composer con borde de foco visible"), styled with the active/inactive
// border style so focus is visible independently of the textarea's own
// cursor, and registers a click region over its interior (spec's mouse
// addendum: "clic en el composer: le da el foco y coloca el cursor"). row
// is the absolute screen row the composer box's top border is drawn at.
func (m *Model) composerView(row int) string {
	active := m.focus == focusComposer
	style := m.th.BorderStyle(active).Border(lipgloss.RoundedBorder())
	rendered := style.Render(m.input.View())

	promptWidth := lipgloss.Width(m.input.Prompt)
	height := lipgloss.Height(rendered) - 2 // border top/bottom
	width := m.input.Width()
	for r := 0; r < height; r++ {
		screenY := row + 1 + r
		screenX0 := convBoxLeft + 1 + promptWidth
		screenX1 := screenX0 + width - 1
		clickRow := r
		m.addRegion(screenY, screenX0, screenX1, func(mm *Model) (tea.Model, tea.Cmd) {
			return mm.onComposerClick(clickRow, 0)
		})
	}
	return rendered
}
