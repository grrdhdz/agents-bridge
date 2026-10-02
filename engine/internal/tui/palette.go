// palette.go implements §6.6's command palette (ctrl+p): a fuzzy filter
// over the actions this mode's Capabilities actually allow.
package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"
)

// paletteItem is one action the palette can run.
type paletteItem struct {
	id    string
	label string
}

// paletteItems lists every action available in this Model's current
// Capabilities (spec §6.6): closing the bridge, copying instance_id,
// copying the join command (host only), returning home (phase 3 — never
// only when Capabilities.ReturnHome), toggling the
// sidebar, search, help and the theme.
func (m *Model) paletteItems() []paletteItem {
	items := make([]paletteItem, 0, 8)
	if m.onStop != nil {
		items = append(items, paletteItem{id: "close-bridge", label: "Cerrar puente"})
	}
	items = append(items, paletteItem{id: "copy-instance-id", label: "Copiar instance_id"})
	if m.caps.Pair {
		items = append(items, paletteItem{id: "copy-join-command", label: "Copiar comando de unión"})
	}
	items = append(items,
		paletteItem{id: "toggle-sidebar", label: "Alternar panel lateral"},
		paletteItem{id: "search", label: "Buscar en la conversación"},
		paletteItem{id: "help", label: "Ayuda"},
		paletteItem{id: "toggle-theme", label: "Cambiar tema"},
	)
	if m.caps.ReturnHome {
		items = append(items, paletteItem{id: "return-home", label: "Volver a inicio"})
	}
	return items
}

// fuzzyMatch is a simple ordered-subsequence match, case-insensitive: every
// rune of query must appear in target in order, not necessarily adjacent
// (spec §6.6 "filtro difuso"). An empty query matches everything.
func fuzzyMatch(query, target string) bool {
	if query == "" {
		return true
	}
	q := []rune(strings.ToLower(query))
	t := []rune(strings.ToLower(target))
	qi := 0
	for _, r := range t {
		if qi >= len(q) {
			break
		}
		if r == q[qi] {
			qi++
		}
	}
	return qi == len(q)
}

func (m *Model) filteredPaletteItems() []paletteItem {
	all := m.paletteItems()
	out := make([]paletteItem, 0, len(all))
	for _, item := range all {
		if fuzzyMatch(m.paletteQuery, item.label) {
			out = append(out, item)
		}
	}
	return out
}

func (m *Model) openPalette() {
	m.closeOverlays()
	m.paletteOpen = true
	m.paletteQuery = ""
	m.paletteIdx = 0
}

// handlePaletteKey routes every keypress while the palette is open: esc
// cancels, up/down move the highlighted item, enter runs it, and anything
// else is treated as filter text.
func (m *Model) handlePaletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	items := m.filteredPaletteItems()
	switch msg.String() {
	case "esc":
		m.paletteOpen = false
		return m, nil
	case "enter":
		m.paletteOpen = false
		if m.paletteIdx >= 0 && m.paletteIdx < len(items) {
			m.runPaletteItem(items[m.paletteIdx].id)
		}
		return m, m.takePendingCmd()
	case "up", "ctrl+k":
		if m.paletteIdx > 0 {
			m.paletteIdx--
		}
		return m, nil
	case "down", "ctrl+j":
		if m.paletteIdx < len(items)-1 {
			m.paletteIdx++
		}
		return m, nil
	case "backspace":
		if m.paletteQuery != "" {
			r := []rune(m.paletteQuery)
			m.paletteQuery = string(r[:len(r)-1])
			m.paletteIdx = 0
		}
		return m, nil
	}
	if msg.Text != "" {
		m.paletteQuery += msg.Text
		m.paletteIdx = 0
	}
	return m, nil
}

// runPaletteItem performs the action id names (§6.6). It is the palette's
// only entry point into the rest of Model's behavior, so every action here
// is exactly one already-tested primitive (copyText, toggleSidebar,
// openSearch, openConfirmCloseDialog, toggleTheme) — the palette adds no
// new behavior of its own, only a way to reach it.
func (m *Model) runPaletteItem(id string) {
	switch id {
	case "close-bridge":
		m.openConfirmCloseDialog()
	case "copy-instance-id":
		m.copyText(m.transport.InstanceID(), "instance_id copiado")
	case "copy-join-command":
		m.copyJoinCommand()
	case "toggle-sidebar":
		m.toggleSidebar()
	case "search":
		m.openSearch()
	case "help":
		m.openHelp()
	case "toggle-theme":
		m.toggleTheme()
	case "return-home":
		m.pendingCmd = returnHomeCmd("")
	}
}

// paletteWidth is spec §6.6's floating palette sizing: "ancho ~60% con
// mínimo 40 y máximo 80 columnas".
func paletteInnerWidth(termWidth int) int {
	w := clampInt(termWidth*6/10, 40, 80)
	if w > termWidth-4 {
		w = termWidth - 4
	}
	if w < 10 {
		w = 10
	}
	return w - 4 // border(2) + padding(2): callers want the *inner* width
}

// paletteWindow renders the palette as a floating, centered window (spec
// §6.6/mouse addendum, superseding the earlier full-screen treatment):
// title, filter field, then every matching item with the highlighted one
// marked. It registers one click region per item row in *absolute* screen
// coordinates (the window's own x/y plus its row/col — spec: "las regiones
// de clic ... deben registrarse con sus coordenadas reales en pantalla,
// desplazadas por la posición de la ventana flotante") — clicking a row
// selects *and* runs it in one click, rather than requiring a
// highlight-then-enter.
func (m *Model) paletteWindow() floatingWindow {
	innerWidth := paletteInnerWidth(m.width)
	title := "Paleta de comandos"
	lines := []string{
		padLine(title, innerWidth),
		padLine("", innerWidth),
		padLine("> "+m.paletteQuery, innerWidth),
		padLine("", innerWidth),
	}
	items := m.filteredPaletteItems()
	if len(items) == 0 {
		lines = append(lines, padLine(m.th.MutedStyle().Render("sin coincidencias"), innerWidth))
	}
	itemRows := make([]int, 0, len(items))
	for i, item := range items {
		marker := "  "
		style := m.th.MutedStyle()
		if i == m.paletteIdx {
			marker = "▶ "
			style = m.th.SelectionStyle()
		}
		itemRows = append(itemRows, len(lines))
		lines = append(lines, padLine(marker+style.Render(item.label), innerWidth))
	}
	// The window can only grow so tall before it would no longer fit;
	// clamp defensively (the item list is short and fixed in practice, so
	// this is a safety net, not the common case).
	maxHeight := m.height - 4
	if maxHeight < 5 {
		maxHeight = 5
	}
	if len(lines) > maxHeight {
		lines = lines[:maxHeight]
	}

	content, outerWidth, outerHeight := windowBox(m.th, true, innerWidth, lines)
	x, y := centerWindow(m.width, m.height, outerWidth, outerHeight)

	for i, item := range items {
		row := itemRows[i]
		if row >= len(lines) {
			continue // clipped by the height clamp above
		}
		itemID := item.id
		screenY := y + 1 + row
		screenX0 := x + 1 + 1 // border + padding
		screenX1 := screenX0 + innerWidth - 1
		m.addRegion(screenY, screenX0, screenX1, func(mm *Model) (tea.Model, tea.Cmd) {
			mm.paletteOpen = false
			mm.runPaletteItem(itemID)
			return mm, mm.takePendingCmd()
		})
	}

	return floatingWindow{content: content, x: x, y: y, width: outerWidth, height: outerHeight}
}

// padLine right-pads (or truncates) s to exactly width visible columns, so
// every line handed to windowBox is already uniform — matching the same
// discipline renderCardWithParams uses and for the same reason (a
// bordered box needs every line to actually be its declared width).
func padLine(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return ansi.Truncate(s, width, "")
	}
	return s + strings.Repeat(" ", width-w)
}
