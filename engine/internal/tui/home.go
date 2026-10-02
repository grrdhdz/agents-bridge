// home.go is the home screen (spec §5, `agents-bridge tui` without
// --instance-id): the list of this user's live bridges, refreshed every 2 s
// through a tea.Cmd (never inside Update), with the same visual language as
// the bridge view — status bar, bordered container, one-line shortcuts bar
// generated from keys/, floating palette/dialog/help/toasts over a fully
// painted background (composeScreen) — plus the actions enter, s (stop,
// confirmed), n (create), r, / (filter) and q.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridges"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/keys"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

const (
	// homeRefreshEvery is spec §5's refresh period.
	homeRefreshEvery = 2 * time.Second
	// homeDoubleClick is the window in which a second click on the same row
	// counts as a double click.
	homeDoubleClick = 400 * time.Millisecond
	// homeBoxTop is the screen row the list's border is drawn at (status
	// bar, blank line — same anchor as the bridge view's conversation box).
	homeBoxTop = 2
)

// HomeOptions configures a HomeModel.
type HomeOptions struct {
	Source HomeSource
	Theme  theme.Theme
	// Now is the clock used for double-click detection and toast expiry; nil
	// defaults to time.Now.
	Now func() time.Time
}

type homeConfirm struct{ id string }

// HomeModel is the home screen. It is a tea.Model on its own (so it is
// testable in isolation) but is normally driven by the App shell (root.go),
// which handles openBridgeMsg.
type HomeModel struct {
	source HomeSource
	th     theme.Theme
	keymap keys.Map
	now    func() time.Time
	// tick schedules the next refresh tick; tests replace it with a no-op
	// so no real 2 s timer is ever left pending.
	tick func(gen int) tea.Cmd

	width, height int

	infos   []bridges.Info
	listErr error
	// listReq numbers the listings this model has asked for; listSeq is the
	// last one applied, so a slow, older answer can never overwrite a newer
	// one.
	listReq, listSeq int
	// tickGen invalidates the timer chain of a previous Resume: only ticks
	// carrying the current generation re-arm.
	tickGen int

	selected string
	selIdx   int
	offset   int

	filterActive bool
	filter       string
	filterInput  textinput.Model

	confirm      *homeConfirm
	paletteOpen  bool
	paletteQuery string
	paletteIdx   int
	showHelp     bool

	creating bool
	created  []string
	toasts   []toast

	regions     []hitRegionOf[*HomeModel]
	lastClickID string
	lastClickAt time.Time
}

type homeListMsg struct {
	seq   int
	infos []bridges.Info
	err   error
}
type homeTickMsg struct{ gen int }
type homeStopDoneMsg struct {
	id  string
	err error
}
type homeCreateDoneMsg struct {
	id  string
	err error
}

// openBridgeMsg asks the App shell to enter id's bridge view.
type openBridgeMsg struct{ id string }

// NewHome builds the home screen.
func NewHome(o HomeOptions) HomeModel {
	th := o.Theme
	if th.Mode == "" {
		th = theme.New(theme.ModeDark, false, nil)
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "instancia, proyecto o modo…"
	input.SetWidth(40) // never 0: see the note in New() about textinput's placeholder.
	input.SetStyles(textInputStyles(th))
	return HomeModel{
		source:      o.Source,
		th:          th,
		keymap:      keys.New(),
		now:         now,
		width:       80,
		height:      24,
		filterInput: input,
		tick: func(gen int) tea.Cmd {
			return tea.Tick(homeRefreshEvery, func(time.Time) tea.Msg { return homeTickMsg{gen: gen} })
		},
	}
}

// Init starts the refresh loop.
func (h *HomeModel) Init() tea.Cmd {
	if h.th.Auto && !h.th.NoColor {
		return tea.Batch(h.Resume(), tea.RequestBackgroundColor)
	}
	return h.Resume()
}

// Resume (re)starts the refresh loop: one immediate listing plus a fresh
// tick chain. Any tick from an earlier chain (say, from before the person
// entered a bridge) carries an older generation and dies on arrival.
func (h *HomeModel) Resume() tea.Cmd {
	h.tickGen++
	return tea.Batch(h.fetchCmd(), h.tick(h.tickGen))
}

// Created lists the bridges this session created with n, in order.
func (h *HomeModel) Created() []string { return append([]string(nil), h.created...) }

// SetTheme switches the palette (the bridge view's theme toggle is carried
// back here so the two screens never disagree).
func (h *HomeModel) SetTheme(th theme.Theme) {
	h.th = th
	h.filterInput.SetStyles(textInputStyles(th))
}

// Notify shows a toast (e.g. "the bridge you were in closed").
func (h *HomeModel) Notify(text string) {
	if text != "" {
		h.toasts = append(h.toasts, toast{text: text, expiresAt: h.now().Add(toastTTL)})
	}
}

// fetchCmd lists the bridges in its own goroutine (spec §5: "sin bloquear
// Update"); the numbering happens here, in Update's goroutine.
func (h *HomeModel) fetchCmd() tea.Cmd {
	h.listReq++
	seq := h.listReq
	source := h.source
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		infos, err := source.List(ctx)
		return homeListMsg{seq: seq, infos: infos, err: err}
	}
}

// --- selection, filter -------------------------------------------------

func (h *HomeModel) visible() []bridges.Info {
	if h.filter == "" {
		return h.infos
	}
	q := strings.ToLower(h.filter)
	var out []bridges.Info
	for _, info := range h.infos {
		hay := strings.ToLower(info.InstanceID + " " + info.Project + " " + info.Mode)
		if strings.Contains(hay, q) {
			out = append(out, info)
		}
	}
	return out
}

// reconcile keeps the selection on the same instance across list and
// filter changes; when that instance is gone it falls to the row now at
// (or nearest to) the old position.
func (h *HomeModel) reconcile() {
	vis := h.visible()
	if len(vis) == 0 {
		h.selected, h.selIdx, h.offset = "", 0, 0
		return
	}
	for i, info := range vis {
		if info.InstanceID == h.selected {
			h.selIdx = i
			h.clampOffset(len(vis))
			return
		}
	}
	idx := h.selIdx
	if idx >= len(vis) {
		idx = len(vis) - 1
	}
	if idx < 0 {
		idx = 0
	}
	h.selIdx, h.selected = idx, vis[idx].InstanceID
	h.ensureVisible()
}

func (h *HomeModel) selectedID() string {
	h.reconcile()
	return h.selected
}

func (h *HomeModel) moveSelection(delta int) {
	h.reconcile()
	vis := h.visible()
	if len(vis) == 0 {
		return
	}
	idx := h.selIdx + delta
	if idx < 0 {
		idx = 0
	}
	if idx > len(vis)-1 {
		idx = len(vis) - 1
	}
	h.selIdx, h.selected = idx, vis[idx].InstanceID
	h.ensureVisible()
}

func (h *HomeModel) selectIndex(idx int) {
	vis := h.visible()
	if idx < 0 || idx >= len(vis) {
		return
	}
	h.selIdx, h.selected = idx, vis[idx].InstanceID
	h.ensureVisible()
}

func (h *HomeModel) clampOffset(n int) {
	rows := h.geometry().rows
	max := n - rows
	if max < 0 {
		max = 0
	}
	if h.offset > max {
		h.offset = max
	}
	if h.offset < 0 {
		h.offset = 0
	}
}

func (h *HomeModel) ensureVisible() {
	rows := h.geometry().rows
	if rows < 1 {
		return
	}
	if h.selIdx < h.offset {
		h.offset = h.selIdx
	} else if h.selIdx >= h.offset+rows {
		h.offset = h.selIdx - rows + 1
	}
	h.clampOffset(len(h.visible()))
}

// homeGeometry is the screen layout, derived in one place for View and for
// Update's mouse/scroll handling alike.
type homeGeometry struct {
	boxH      int // bordered box outer height
	rows      int // list rows visible (box interior minus the header line)
	filterBar bool
}

func (h *HomeModel) geometry() homeGeometry {
	g := homeGeometry{filterBar: h.filterActive || h.filter != ""}
	g.boxH = h.height - 3 // status, blank, shortcuts bar
	if g.filterBar {
		g.boxH--
	}
	if g.boxH < 3 {
		g.boxH = 3
	}
	g.rows = g.boxH - 2 - 1
	if g.rows < 1 {
		g.rows = 1
	}
	return g
}

// rowScreenY is the screen row filtered row i is drawn at.
func (h *HomeModel) rowScreenY(i int) int { return homeBoxTop + 2 + (i - h.offset) }

// --- update ------------------------------------------------------------

func (h *HomeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg = filterInput(msg); msg == nil {
		return h, nil
	}
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		if h.th.Auto {
			h.SetTheme(autoThemeFor(h.th, msg))
		}
		return h, nil
	case tea.WindowSizeMsg:
		h.width, h.height = msg.Width, msg.Height
		if h.width < 20 {
			h.width = 20
		}
		if h.height < 10 {
			h.height = 10
		}
		w := h.width - len("Filtrar: ") - 2
		if w < 8 {
			w = 8
		}
		h.filterInput.SetWidth(w)
		h.reconcile()
		return h, nil
	case homeTickMsg:
		if msg.gen != h.tickGen {
			return h, nil
		}
		h.toasts = pruneToasts(h.toasts, h.now())
		return h, tea.Batch(h.fetchCmd(), h.tick(h.tickGen))
	case homeListMsg:
		if msg.seq <= h.listSeq {
			return h, nil
		}
		h.listSeq = msg.seq
		h.listErr = msg.err
		if msg.err == nil {
			h.infos = msg.infos
		}
		h.reconcile()
		return h, nil
	case homeStopDoneMsg:
		if msg.err != nil {
			h.Notify("No se pudo cerrar " + shortInstance(msg.id) + ": " + msg.err.Error())
		} else {
			h.Notify("Puente " + shortInstance(msg.id) + " cerrado")
		}
		return h, h.fetchCmd()
	case homeCreateDoneMsg:
		h.creating = false
		if msg.err != nil {
			h.Notify("No se pudo crear el puente: " + msg.err.Error())
			return h, nil
		}
		h.created = append(h.created, msg.id)
		h.Notify("Puente " + shortInstance(msg.id) + " creado")
		id := msg.id
		return h, func() tea.Msg { return openBridgeMsg{id: id} }
	case tea.MouseWheelMsg:
		return h.handleWheel(msg)
	case tea.MouseClickMsg:
		return h.handleClick(msg)
	case tea.KeyPressMsg:
		return h.handleKey(msg)
	}
	return h, nil
}

func (h *HomeModel) overlayOpen() bool { return h.confirm != nil || h.paletteOpen || h.showHelp }

func (h *HomeModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case h.confirm != nil:
		return h.handleConfirmKey(msg)
	case h.paletteOpen:
		return h.handlePaletteKey(msg)
	case h.filterActive:
		return h.handleFilterKey(msg)
	case h.showHelp:
		h.showHelp = false
		return h, nil
	}
	switch {
	case key.Matches(msg, h.keymap.Home.Quit):
		return h, tea.Quit
	case key.Matches(msg, h.keymap.Home.Up):
		h.moveSelection(-1)
	case key.Matches(msg, h.keymap.Home.Down):
		h.moveSelection(1)
	case key.Matches(msg, h.keymap.Home.Enter):
		return h, h.enterSelected()
	case key.Matches(msg, h.keymap.Home.Stop):
		h.askStop()
	case key.Matches(msg, h.keymap.Home.New):
		return h, h.createBridge()
	case key.Matches(msg, h.keymap.Home.Refresh):
		return h, h.fetchCmd()
	case key.Matches(msg, h.keymap.Home.Filter):
		h.openFilter()
	case key.Matches(msg, h.keymap.Home.Palette):
		h.openHomePalette()
	case key.Matches(msg, h.keymap.Home.Help):
		h.showHelp = true
	}
	return h, nil
}

func (h *HomeModel) enterSelected() tea.Cmd {
	id := h.selectedID()
	if id == "" {
		return nil
	}
	return func() tea.Msg { return openBridgeMsg{id: id} }
}

func (h *HomeModel) askStop() {
	if id := h.selectedID(); id != "" {
		h.confirm = &homeConfirm{id: id}
	}
}

func (h *HomeModel) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		id := h.confirm.id
		h.confirm = nil
		source := h.source
		return h, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return homeStopDoneMsg{id: id, err: source.Stop(ctx, id)}
		}
	case "n", "esc":
		h.confirm = nil
	}
	return h, nil
}

// createBridge launches a new local --headless bridge through the source,
// in a tea.Cmd: launching waits for the child's ready file, which must never
// block Update.
func (h *HomeModel) createBridge() tea.Cmd {
	if h.creating {
		return nil
	}
	h.creating = true
	h.Notify("Creando puente…")
	source := h.source
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		id, err := source.Create(ctx)
		return homeCreateDoneMsg{id: id, err: err}
	}
}

func (h *HomeModel) openFilter() {
	h.filterActive = true
	h.filterInput.SetValue(h.filter)
	h.filterInput.Focus()
}

func (h *HomeModel) handleFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		h.filterActive = false
		h.filterInput.Blur()
		h.filterInput.SetValue("")
		h.filter = ""
		h.reconcile()
		return h, nil
	case "enter":
		h.filterActive = false
		h.filterInput.Blur()
		h.reconcile()
		return h, nil
	}
	var cmd tea.Cmd
	h.filterInput, cmd = h.filterInput.Update(msg)
	h.filter = h.filterInput.Value()
	h.reconcile()
	return h, cmd
}

// --- palette -----------------------------------------------------------

func (h *HomeModel) homePaletteItems() []paletteItem {
	return []paletteItem{
		{id: "enter", label: "Entrar"},
		{id: "stop", label: "Cerrar puente"},
		{id: "new", label: "Crear puente"},
		{id: "refresh", label: "Refrescar"},
		{id: "filter", label: "Filtrar"},
		{id: "theme", label: "Cambiar tema"},
		{id: "help", label: "Ayuda"},
	}
}

func (h *HomeModel) filteredHomePalette() []paletteItem {
	var out []paletteItem
	for _, item := range h.homePaletteItems() {
		if fuzzyMatch(h.paletteQuery, item.label) {
			out = append(out, item)
		}
	}
	return out
}

func (h *HomeModel) openHomePalette() {
	h.paletteOpen, h.paletteQuery, h.paletteIdx = true, "", 0
}

func (h *HomeModel) runHomePaletteItem(id string) tea.Cmd {
	switch id {
	case "enter":
		return h.enterSelected()
	case "stop":
		h.askStop()
	case "new":
		return h.createBridge()
	case "refresh":
		return h.fetchCmd()
	case "filter":
		h.openFilter()
	case "theme":
		if h.th.NoColor {
			h.Notify(noColorThemeNotice)
			return nil
		}
		mode := theme.ModeLight
		if h.th.Mode == theme.ModeLight {
			mode = theme.ModeDark
		}
		h.SetTheme(theme.New(mode, h.th.NoColor, nil))
	case "help":
		h.showHelp = true
	}
	return nil
}

func (h *HomeModel) handlePaletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	items := h.filteredHomePalette()
	switch msg.String() {
	case "esc":
		h.paletteOpen = false
	case "enter":
		h.paletteOpen = false
		if h.paletteIdx >= 0 && h.paletteIdx < len(items) {
			return h, h.runHomePaletteItem(items[h.paletteIdx].id)
		}
	case "up", "ctrl+k":
		if h.paletteIdx > 0 {
			h.paletteIdx--
		}
	case "down", "ctrl+j":
		if h.paletteIdx < len(items)-1 {
			h.paletteIdx++
		}
	case "backspace":
		if h.paletteQuery != "" {
			r := []rune(h.paletteQuery)
			h.paletteQuery = string(r[:len(r)-1])
			h.paletteIdx = 0
		}
	default:
		if msg.Text != "" {
			h.paletteQuery += msg.Text
			h.paletteIdx = 0
		}
	}
	return h, nil
}

// --- mouse -------------------------------------------------------------

func (h *HomeModel) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseLeft {
		return h, nil
	}
	if region := findRegion(h.regions, mouse.X, mouse.Y); region != nil {
		return region.onClick(h)
	}
	// Nothing drawn there: an open overlay treats it as "close me".
	switch {
	case h.confirm != nil:
		h.confirm = nil
	case h.paletteOpen:
		h.paletteOpen = false
	case h.showHelp:
		h.showHelp = false
	}
	return h, nil
}

func (h *HomeModel) handleWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	if h.overlayOpen() {
		return h, nil
	}
	switch msg.Mouse().Button {
	case tea.MouseWheelDown:
		h.offset += 3
	case tea.MouseWheelUp:
		h.offset -= 3
	default:
		return h, nil
	}
	h.clampOffset(len(h.visible()))
	return h, nil
}

// clickRow selects filtered row i; a second click on the same row within
// homeDoubleClick enters it.
func (h *HomeModel) clickRow(i int) (tea.Model, tea.Cmd) {
	vis := h.visible()
	if i < 0 || i >= len(vis) {
		return h, nil
	}
	id := vis[i].InstanceID
	now := h.now()
	double := id == h.lastClickID && now.Sub(h.lastClickAt) <= homeDoubleClick
	h.selectIndex(i)
	if double {
		h.lastClickID = ""
		return h, func() tea.Msg { return openBridgeMsg{id: id} }
	}
	h.lastClickID, h.lastClickAt = id, now
	return h, nil
}

// --- view --------------------------------------------------------------

func (h *HomeModel) addRegion(y, x0, x1 int, onClick func(*HomeModel) (tea.Model, tea.Cmd)) {
	h.regions = appendRegion(h.regions, y, x0, x1, onClick)
}

func (h *HomeModel) title() string {
	total, shown := len(h.infos), len(h.visible())
	noun := "puentes"
	if total == 1 {
		noun = "puente"
	}
	count := strconv.Itoa(total)
	if shown != total {
		count = fmt.Sprintf("%d de %d", shown, total)
	}
	title := fmt.Sprintf("agents-bridge · %s %s", count, noun)
	if h.listErr != nil {
		title += " · sin actualizar"
	}
	return title
}

func (h *HomeModel) View() tea.View {
	h.regions = nil
	backdrop := h.backdrop()

	var windows []floatingWindow
	switch {
	case h.paletteOpen:
		h.regions = nil
		windows = append(windows, h.paletteWindow())
	case h.confirm != nil:
		h.regions = nil
		windows = append(windows, h.dialogWindow())
	case h.showHelp:
		h.regions = nil
		windows = append(windows, h.helpWindow())
	}
	if w, ok := h.toastWindow(); ok {
		windows = append(windows, w)
	}
	content := composeScreen(h.th, h.width, h.height, backdrop, windows)
	return newScreenView(content, h.th, "agents-bridge inicio")
}

func (h *HomeModel) backdrop() string {
	g := h.geometry()
	var b strings.Builder
	b.WriteString(h.th.StatusBarStyle().Width(h.width).Render(h.title()))
	b.WriteString("\n\n")
	b.WriteString(h.listBox(g))
	b.WriteString("\n")
	row := homeBoxTop + g.boxH
	if g.filterBar {
		b.WriteString(h.filterBarLine())
		b.WriteString("\n")
		row++
	}
	b.WriteString(h.shortcutsBar(row))
	return b.String()
}

func (h *HomeModel) filterBarLine() string {
	if h.filterActive {
		return "Filtrar: " + h.filterInput.View()
	}
	return h.th.MutedStyle().Render("Filtro: " + h.filter + " (/ edita, esc en el campo lo quita)")
}

// homeColumn is one list column. prio orders which columns survive a
// narrow terminal (lower = kept first); flex marks the one that takes the
// leftover width.
type homeColumn struct {
	title string
	width int
	prio  int
	flex  bool
	cell  func(bridges.Info) string
}

func homeColumns() []homeColumn {
	return []homeColumn{
		{title: "INSTANCIA", width: 9, prio: 0, cell: func(i bridges.Info) string { return shortInstance(i.InstanceID) }},
		{title: "PROYECTO", width: 8, prio: 2, flex: true, cell: func(i bridges.Info) string { return dashIfEmpty(i.Project) }},
		{title: "MODO", width: 6, prio: 5, cell: func(i bridges.Info) string { return homeMode(i.Mode) }},
		{title: "ROLES", width: 8, prio: 6, cell: func(i bridges.Info) string { return homeRoles(i.Roles) }},
		{title: "CON", width: 3, prio: 1, cell: func(i bridges.Info) string {
			if i.PeerConnected {
				return "●"
			}
			return "○"
		}},
		{title: "INACTIVO", width: 8, prio: 3, cell: func(i bridges.Info) string {
			if i.IdleSeconds == nil {
				return "-"
			}
			return humanIdle(time.Duration(*i.IdleSeconds) * time.Second)
		}},
		{title: "MSGS", width: 5, prio: 4, cell: func(i bridges.Info) string { return strconv.FormatUint(i.LatestServerSeq, 10) }},
		{title: "INICIO", width: 6, prio: 7, cell: func(i bridges.Info) string { return i.StartedAt.Local().Format("15:04") }},
	}
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func homeMode(mode string) string {
	switch mode {
	case "tailscale-host":
		return "host"
	case "tailscale-join":
		return "join"
	case "":
		return "-"
	}
	return mode
}

func homeRoles(roles []string) string {
	var short []string
	for _, r := range roles {
		if r == "orchestrator" {
			short = append(short, "orq")
		} else {
			short = append(short, "ejec")
		}
	}
	return dashIfEmpty(strings.Join(short, "+"))
}

func humanIdle(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// layoutColumns keeps as many columns as fit in avail (by priority), then
// hands the leftover to the flexible one, and returns them in display order.
func layoutColumns(avail int) []homeColumn {
	const gap = 2
	all := homeColumns()
	keep := map[int]bool{}
	used := -gap
	for prio := 0; prio < len(all); prio++ {
		for i, c := range all {
			if c.prio != prio {
				continue
			}
			if used+gap+c.width <= avail {
				keep[i] = true
				used += gap + c.width
			}
		}
	}
	var out []homeColumn
	for i, c := range all {
		if keep[i] {
			out = append(out, c)
		}
	}
	extra := avail - used
	for i := range out {
		if out[i].flex && extra > 0 {
			grow := extra
			if out[i].width+grow > 28 {
				grow = 28 - out[i].width
			}
			out[i].width += grow
		}
	}
	return out
}

// formatRow renders one row's columns into exactly width cells.
func formatRow(cols []homeColumn, cells []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = padLine(cells[i], c.width)
	}
	return strings.Join(parts, "  ")
}

func (h *HomeModel) listBox(g homeGeometry) string {
	innerW := h.width - 2
	innerH := g.boxH - 2
	const marker = 2 // "▶ " or two spaces
	lines := make([]string, 0, innerH)
	vis := h.visible()
	if len(vis) == 0 {
		lines = h.emptyState(innerW, innerH)
	} else {
		cols := layoutColumns(innerW - marker - 1)
		titles := make([]string, len(cols))
		for i, c := range cols {
			titles[i] = c.title
		}
		lines = append(lines, padLine("  "+h.th.MutedStyle().Render(formatRow(cols, titles)), innerW))
		h.clampOffset(len(vis))
		for r := 0; r < g.rows; r++ {
			idx := h.offset + r
			if idx >= len(vis) {
				break
			}
			info := vis[idx]
			cells := make([]string, len(cols))
			for i, c := range cols {
				cells[i] = c.cell(info)
			}
			text := formatRow(cols, cells)
			var line string
			if idx == h.selIdx {
				line = h.th.SelectionStyle().Render("▶ " + text)
			} else {
				line = "  " + h.th.TextStyle().Render(text)
			}
			lines = append(lines, padLine(line, innerW))
			screenY := homeBoxTop + 1 + 1 + r
			row := idx
			h.addRegion(screenY, 1, innerW, func(hh *HomeModel) (tea.Model, tea.Cmd) { return hh.clickRow(row) })
		}
	}
	content := fitLines(strings.Join(lines, "\n"), innerH)
	return h.th.BorderStyle(!h.overlayOpen()).Border(lipgloss.RoundedBorder()).Render(content)
}

// emptyState is the instructions shown when there is nothing to list (or
// nothing matches the filter).
func (h *HomeModel) emptyState(innerW, innerH int) []string {
	var text []string
	if h.filter != "" && len(h.infos) > 0 {
		text = []string{"", "Ningún puente coincide con el filtro.", "esc en el campo de filtro lo quita."}
	} else {
		text = []string{
			"",
			"No hay puentes activos.",
			"",
			"n  crea uno nuevo (un puente local en segundo plano) y entra en él.",
			"o lánzalo tú:  agents-bridge local",
			"(los puentes de otros equipos no aparecen aquí: solo los de este usuario.)",
		}
	}
	lines := make([]string, 0, len(text))
	for _, t := range text {
		lines = append(lines, padLine(" "+ansi.Truncate(t, innerW-2, "…"), innerW))
	}
	return lines
}

// --- shortcuts bar -----------------------------------------------------

type homeShortcut struct {
	label string
	run   func(*HomeModel) (tea.Model, tea.Cmd)
}

func (h *HomeModel) shortcutActions() []homeShortcut {
	var out []homeShortcut
	add := func(b key.Binding, run func(*HomeModel) (tea.Model, tea.Cmd)) {
		help := b.Help()
		out = append(out, homeShortcut{label: help.Key + " " + help.Desc, run: run})
	}
	k := h.keymap.Home
	add(k.Enter, func(hh *HomeModel) (tea.Model, tea.Cmd) { return hh, hh.enterSelected() })
	add(k.New, func(hh *HomeModel) (tea.Model, tea.Cmd) { return hh, hh.createBridge() })
	add(k.Stop, func(hh *HomeModel) (tea.Model, tea.Cmd) { hh.askStop(); return hh, nil })
	add(k.Refresh, func(hh *HomeModel) (tea.Model, tea.Cmd) { return hh, hh.fetchCmd() })
	add(k.Filter, func(hh *HomeModel) (tea.Model, tea.Cmd) { hh.openFilter(); return hh, nil })
	add(k.Palette, func(hh *HomeModel) (tea.Model, tea.Cmd) { hh.openHomePalette(); return hh, nil })
	add(k.Quit, func(hh *HomeModel) (tea.Model, tea.Cmd) { return hh, tea.Quit })
	add(k.Up, func(hh *HomeModel) (tea.Model, tea.Cmd) { hh.moveSelection(-1); return hh, nil })
	add(k.Down, func(hh *HomeModel) (tea.Model, tea.Cmd) { hh.moveSelection(1); return hh, nil })
	out = append(out, homeShortcut{label: "? más", run: func(hh *HomeModel) (tea.Model, tea.Cmd) { hh.showHelp = true; return hh, nil }})
	return out
}

func (h *HomeModel) shortcutsBar(row int) string {
	line := h.keymap.Bar(h.width, nil, "Inicio")
	cursor := 0
	for _, action := range h.shortcutActions() {
		idx := strings.Index(line[cursor:], action.label)
		if idx < 0 {
			continue
		}
		start := cursor + idx
		end := start + len(action.label) - 1
		run := action.run
		h.addRegion(row, start, end, run)
		cursor = end + 1
	}
	// Muted for the same reason as Model.shortcutsBar: never the terminal's
	// default foreground on the theme's own Surface.
	return h.th.MutedStyle().Width(h.width).Render(line)
}

// --- floating windows --------------------------------------------------

func (h *HomeModel) paletteWindow() floatingWindow {
	innerWidth := paletteInnerWidth(h.width)
	lines := []string{
		padLine("Paleta de comandos", innerWidth),
		padLine("", innerWidth),
		padLine("> "+h.paletteQuery, innerWidth),
		padLine("", innerWidth),
	}
	items := h.filteredHomePalette()
	if len(items) == 0 {
		lines = append(lines, padLine(h.th.MutedStyle().Render("sin coincidencias"), innerWidth))
	}
	itemRows := make([]int, 0, len(items))
	for i, item := range items {
		marker, style := "  ", h.th.MutedStyle()
		if i == h.paletteIdx {
			marker, style = "▶ ", h.th.SelectionStyle()
		}
		itemRows = append(itemRows, len(lines))
		lines = append(lines, padLine(marker+style.Render(item.label), innerWidth))
	}
	maxHeight := h.height - 4
	if maxHeight < 5 {
		maxHeight = 5
	}
	if len(lines) > maxHeight {
		lines = lines[:maxHeight]
	}
	content, outerWidth, outerHeight := windowBox(h.th, true, innerWidth, lines)
	x, y := centerWindow(h.width, h.height, outerWidth, outerHeight)
	for i, item := range items {
		if itemRows[i] >= len(lines) {
			continue
		}
		id := item.id
		screenY := y + 1 + itemRows[i]
		x0 := x + 2
		h.addRegion(screenY, x0, x0+innerWidth-1, func(hh *HomeModel) (tea.Model, tea.Cmd) {
			hh.paletteOpen = false
			return hh, hh.runHomePaletteItem(id)
		})
	}
	return floatingWindow{content: content, x: x, y: y, width: outerWidth, height: outerHeight}
}

func (h *HomeModel) dialogWindow() floatingWindow {
	closeLabel, cancelLabel := "[ Cerrar puente ]", "[ Cancelar ]"
	message := "¿Cerrar el puente " + shortInstance(h.confirm.id) + "? Esta acción es irreversible."
	buttons := closeLabel + "    " + cancelLabel
	help := "y/enter confirma · n/esc cancela"
	innerWidth := lipgloss.Width(message)
	for _, s := range []string{buttons, help} {
		if w := lipgloss.Width(s); w > innerWidth {
			innerWidth = w
		}
	}
	innerWidth = clampInt(innerWidth, 30, h.width-4)
	lines := []string{
		padLine(message, innerWidth), padLine("", innerWidth),
		padLine(buttons, innerWidth), padLine("", innerWidth), padLine(help, innerWidth),
	}
	content, outerWidth, outerHeight := windowBox(h.th, true, innerWidth, lines)
	x, y := centerWindow(h.width, h.height, outerWidth, outerHeight)
	closeStart := x + 2
	closeEnd := closeStart + len(closeLabel) - 1
	cancelStart := closeEnd + 1 + 4
	cancelEnd := cancelStart + len(cancelLabel) - 1
	screenY := y + 1 + 2
	h.addRegion(screenY, closeStart, closeEnd, func(hh *HomeModel) (tea.Model, tea.Cmd) {
		return hh.handleConfirmKey(tea.KeyPressMsg{Text: "y", Code: 'y'})
	})
	h.addRegion(screenY, cancelStart, cancelEnd, func(hh *HomeModel) (tea.Model, tea.Cmd) {
		return hh.handleConfirmKey(tea.KeyPressMsg{Text: "n", Code: 'n'})
	})
	return floatingWindow{content: content, x: x, y: y, width: outerWidth, height: outerHeight}
}

func (h *HomeModel) helpWindow() floatingWindow {
	full := strings.Split(h.keymap.HelpFor("Inicio"), "\n")
	innerWidth := clampInt(h.width-4, 20, helpWindowMaxWidth)
	maxHeight := h.height - 6
	if maxHeight < 3 {
		maxHeight = 3
	}
	if len(full) > maxHeight {
		full = full[:maxHeight]
	}
	lines := []string{padLine("Ayuda (cualquier tecla o clic cierra)", innerWidth), padLine("", innerWidth)}
	for _, l := range full {
		lines = append(lines, padLine(l, innerWidth))
	}
	content, outerWidth, outerHeight := windowBox(h.th, true, innerWidth, lines)
	x, y := centerWindow(h.width, h.height, outerWidth, outerHeight)
	return floatingWindow{content: content, x: x, y: y, width: outerWidth, height: outerHeight}
}

func (h *HomeModel) toastWindow() (floatingWindow, bool) {
	live := pruneToasts(h.toasts, h.now())
	if len(live) == 0 {
		return floatingWindow{}, false
	}
	g := h.geometry()
	toastWidth := clampInt(h.width-4, 20, 60)
	content := renderToastStack(live, h.th, toastWidth)
	height := len(live)
	x := h.width - 1 - toastWidth
	if x < 0 {
		x = 0
	}
	y := homeBoxTop + g.boxH - 1 - height
	if y < homeBoxTop+1 {
		y = homeBoxTop + 1
	}
	return floatingWindow{content: content, x: x, y: y, width: toastWidth, height: height}, true
}
