// Package tui provides agents-bridge's terminal UI, shared by all four modes
// (host, join, local's embedded observer and standalone `agents-bridge tui`).
// A single Model drives every mode; the differences between them are
// expressed as Capabilities (capabilities.go), never as branches keyed on
// which mode is running (spec §3, §4).
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/agents-bridge/internal/bridge"
	"github.com/grrdhdz/agents-bridge/internal/control"
	"github.com/grrdhdz/agents-bridge/internal/protocol"
	"github.com/grrdhdz/agents-bridge/internal/tui/keys"
	"github.com/grrdhdz/agents-bridge/internal/tui/theme"
)

// focusTarget is which pane receives keyboard input (§6.1: "tab alterna
// entre composer y conversación").
type focusTarget int

const (
	focusComposer focusTarget = iota
	focusConversation
)

// Options configures a new Model. Capabilities (built by one of the
// CapabilitiesFor* constructors) is what actually distinguishes the four
// modes; every other field is plumbing the same for all of them.
type Options struct {
	// Client is the direct TCP path: the Mac/Windows TUI's usual case. It is
	// wrapped in clientTransport automatically. Ignored when Transport is set.
	Client *bridge.Client
	// PeerConnected optionally reports whether the other role has a live
	// connection right now, for the sidebar's StatusProvider (§7). Host
	// wires *bridge.Server.WorkerConnected; join has nothing better than
	// its own Client.Connected (the peer is the same server process it is
	// itself talking to), so it can leave this nil. Ignored unless Client
	// is set (Transport already covers its own status).
	PeerConnected func() bool
	// RoleStates optionally reports each role's state (§3.4) for the side
	// panel when the direct client path is used; host and join wire their
	// own control endpoint's RoleSnapshots (their own role only). Ignored
	// unless Client is set.
	RoleStates func() map[protocol.Role]control.RoleSnapshot
	// Transport lets a caller supply a non-*bridge.Client message channel,
	// namely the observing TUI's control-plane client (host's and join's own
	// embedded views also accept one for tests). When both are unset, the
	// model has no transport at all (used by a few tests that only exercise
	// input/layout).
	Transport    Transport
	LocalRole    protocol.Role
	JoinCommand  string
	CopyCommand  func(string) error
	OnStop       func()
	OnPair       func() string
	Capabilities Capabilities
	Theme        theme.Theme
	// Now is the clock Model uses for message timestamps and the status
	// bar's activity readout; nil defaults to time.Now. Tests (including
	// the golden view snapshots) inject a fixed clock so output is
	// deterministic.
	Now func() time.Time
}

type eventMsg struct {
	event bridge.Event
	owner int
}
type reconnectTickMsg struct{ owner int }
type statusPollTickMsg struct{ owner int }
type statusResultMsg struct {
	status BridgeStatus
	err    error
	owner  int
}

// Model is the whole TUI shell (spec §3): status bar, conversation and
// composer, driven by one Capabilities value instead of per-mode branches.
type Model struct {
	transport   Transport
	caps        Capabilities
	localRole   protocol.Role
	joinCommand string
	copyCommand func(string) error
	onStop      func()
	onPair      func() string
	th          theme.Theme
	keymap      keys.Map
	now         func() time.Time

	focus       focusTarget
	showHelp    bool
	helpScroll  int // §6.6: the floating help window scrolls when its content is taller than fits.
	stopConfirm bool
	events      EventSubscription
	// id identifies this Model among the bridges one App session opens in
	// turn; eventsCtx/cancelEvents bound the goroutine waiting on the
	// subscription (see Shutdown).
	id           int
	eventsCtx    context.Context
	cancelEvents context.CancelFunc
	pendingCmd   tea.Cmd

	input    textarea.Model
	viewport viewport.Model

	messages []protocol.Envelope
	byID     map[string]int
	statuses map[string]string
	unread   int

	history         []string
	historyIdx      int
	browsingHistory bool
	labelIdx        int

	state        string
	error        string
	copyInfo     string
	lastActivity time.Time

	width, height int
	footerHeight  int

	// mdCache renders and memoizes Markdown bodies (§6.3), keyed by
	// (message_id, width, theme).
	mdCache *markdownCache
	// messageOffsets is the 0-based line where each m.messages[i]'s card
	// begins in the viewport's current content, recomputed by
	// refreshViewport — used to scroll the selected card into view.
	messageOffsets []int
	// conversationLines is the same content refreshViewport just handed
	// the viewport, split into lines — used by mouse click handling
	// (mouse.go) to inspect the exact text a click landed on (e.g. to
	// recognize the "▸ N líneas más" fold indicator) without re-rendering
	// anything.
	conversationLines []string

	// selected is the id of the message the conversation's keyboard focus
	// is on (§6.3 "Selección"); "" means nothing has been selected yet
	// (defaults to the last message once the conversation gains focus).
	selected string
	// expanded/collapsed are explicit per-message fold overrides: enter
	// toggles a message between them, overriding the length-based default.
	expanded  map[string]bool
	collapsed map[string]bool

	// searchActive is true while the query field itself has keyboard focus
	// (§6.3 "Búsqueda"); searchQuery survives after committing so
	// highlighting and n/N keep working once the field closes.
	searchActive bool
	searchQuery  string
	searchIdx    int
	searchInput  textinput.Model

	// toasts are §6.7's stacked, expiring notices.
	toasts []toast

	// sidebarOverride is ctrl+b's explicit toggle (§6.4); nil means "follow
	// the width breakpoint" (sidebarVisible's default).
	sidebarOverride *bool

	// paletteOpen/paletteQuery/paletteIdx drive §6.6's command palette.
	paletteOpen  bool
	paletteQuery string
	paletteIdx   int

	// confirm is the active confirmation dialog (§6.7), or nil when none is
	// open.
	confirm *confirmDialog

	// status/haveStatus is the last successful StatusProvider poll (§7);
	// peerKnown/peerWasConnected track the previous poll's PeerConnected to
	// raise a connect/disconnect toast exactly on the transition.
	status           BridgeStatus
	haveStatus       bool
	peerKnown        bool
	peerWasConnected bool

	// regions is every clickable rectangle View() drew this frame (see
	// hitregion.go): rebuilt from scratch at the start of every View call,
	// it is the single source of truth a mouse click is matched against.
	regions []hitRegion
	// convBoxOn/convBox*, sidebarBoxOn/sidebarBoxWidth and composerBoxTop
	// record this frame's box geometry (also computed once, in resize),
	// so View can place the boxes and Update's mouse handling (which runs
	// on a *later* message, after the frame that drew them) can tell which
	// box a click or wheel event landed in without re-deriving layout math
	// in a second place.
	convBoxWidth, convBoxHeight int
	sidebarBoxOn                bool
	sidebarBoxWidth             int
}

func New(options Options) Model {
	th := options.Theme
	if th.Mode == "" {
		th = theme.New(theme.ModeDark, false, nil)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}

	input := textarea.New()
	input.Prompt = "│ "
	input.CharLimit = protocol.MaxBodyBytes
	input.SetHeight(4)
	input.SetWidth(80)
	input.ShowLineNumbers = false
	// textarea.New() hard-codes dark-mode styles internally regardless of
	// theme — see widgetstyle.go's doc comment for the real bug this fixes
	// (a leftover dark cursor-line background showing through the light
	// theme's composer).
	input.SetStyles(textAreaStyles(th))

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(18))
	vp.SoftWrap = true
	vp.MouseWheelEnabled = true

	search := textinput.New()
	// No prompt glyph here: "Buscar: " (searchBar, view.go) already labels
	// the field, and an emoji prompt (the original had "🔎 ") is exactly
	// the kind of ambiguous-width character a terminal can measure
	// differently than Go's rune count does. That mismatch also exposed a
	// real bug in bubbles' textinput: its placeholder rendering sizes a
	// scratch buffer off Width(), which defaults to 0 until something
	// calls SetWidth — and with Width() < 1 it only ever allocates room
	// for the placeholder's *first rune*, which it then renders as if it
	// were the cursor's own character. The visible result was a lone "b"
	// (from "buscar…") that looked exactly like a stray keystroke and
	// resisted backspace, because it was never actually part of the
	// value. SetWidth below (and again in resize(), kept in sync with the
	// terminal) is the real fix; dropping the emoji only removes one
	// source of width ambiguity, not this one.
	search.Prompt = ""
	search.Placeholder = "buscar…"
	search.SetWidth(40)
	search.SetStyles(textInputStyles(th))

	transport := options.Transport
	if transport == nil && options.Client != nil {
		transport = newClientTransport(options.Client, options.PeerConnected, options.RoleStates)
	}

	km := keys.New()
	km.Global.Home.SetEnabled(options.Capabilities.ReturnHome)
	m := Model{
		transport:   transport,
		caps:        options.Capabilities,
		localRole:   options.LocalRole,
		joinCommand: options.JoinCommand,
		copyCommand: options.CopyCommand,
		onStop:      options.OnStop,
		onPair:      options.OnPair,
		th:          th,
		keymap:      km,
		now:         now,
		focus:       focusComposer,
		input:       input,
		viewport:    vp,
		byID:        make(map[string]int),
		statuses:    make(map[string]string),
		state:       "connecting",
		width:       80,
		height:      24,
		mdCache:     newMarkdownCache(),
		expanded:    make(map[string]bool),
		collapsed:   make(map[string]bool),
		searchInput: search,
	}
	m.lastActivity = now()
	m.updateComposerPlaceholder()
	m.id = nextModelID()
	m.eventsCtx, m.cancelEvents = context.WithCancel(context.Background())
	return m
}

func (m *Model) Init() tea.Cmd {
	if m.caps.Pair && m.joinCommand != "" {
		m.copyJoinCommand()
	}
	cmds := []tea.Cmd{m.input.Focus(), reconnectTick(m.id)}
	if m.th.Auto && !m.th.NoColor {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	if m.transport != nil {
		sub, err := m.transport.Subscribe(0)
		if err == nil {
			m.events = sub
			cmds = append(cmds, waitForEvent(m.eventsCtx, sub, m.id))
		}
	}
	if provider, ok := m.transport.(StatusProvider); ok {
		cmds = append(cmds, fetchStatusCmd(provider, m.id), statusPollTick(m.id))
	}
	return tea.Batch(cmds...)
}

func (m *Model) copyJoinCommand() {
	if !m.caps.Pair || m.joinCommand == "" {
		return
	}
	if m.copyCommand == nil {
		m.copyInfo = "No se pudo copiar automáticamente; pega el comando completo de la salida de la terminal. F5 reintenta."
		return
	}
	if err := m.copyCommand(m.joinCommand); err != nil {
		m.copyInfo = fmt.Sprintf("No se pudo copiar automáticamente (%v); usa el fallback completo de la salida de la terminal. F5 reintenta.", err)
		return
	}
	m.copyInfo = "Comando de unión copiado. Pégalo en Codex o PowerShell; F5 vuelve a copiar."
}

func waitForEvent(ctx context.Context, sub EventSubscription, owner int) tea.Cmd {
	return func() tea.Msg {
		if sub == nil {
			return nil
		}
		event, err := sub.Next(ctx)
		if err != nil {
			frameType := protocol.FrameTransportError
			if errors.Is(err, bridge.ErrClosed) {
				frameType = protocol.FrameClose
			}
			return eventMsg{owner: owner, event: bridge.Event{Kind: bridge.EventLifecycle, State: "closed", Detail: err.Error(), Frame: protocol.Frame{Type: frameType, Detail: err.Error()}}}
		}
		return eventMsg{owner: owner, event: event}
	}
}

func reconnectTick(owner int) tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return reconnectTickMsg{owner: owner} })
}

// statusPollTick schedules the next StatusProvider poll (§7: "sondeo cada
// 2 s"). It only ever produces the tick message itself, never the network
// call — fetchStatusCmd does that separately, so a slow poll never delays
// the next tick from firing (both run as independent tea.Cmds; Update never
// blocks on either).
func statusPollTick(owner int) tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return statusPollTickMsg{owner: owner} })
}

// fetchStatusCmd runs one StatusProvider.Status call in its own tea.Cmd
// goroutine (§7: "sin bloquear Update: usa tea.Cmd"). Tests exercise the
// polling logic by invoking the returned func directly with a fake
// provider, without any real timer.
func fetchStatusCmd(provider StatusProvider, owner int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		status, err := provider.Status(ctx)
		return statusResultMsg{status: status, err: err, owner: owner}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg = filterInput(msg); msg == nil {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		// The answer to the auto theme's request (Init): the terminal's own
		// background, read by Bubble Tea's input reader. Only an undecided
		// auto theme reacts; an explicit --theme always wins.
		if m.th.Auto {
			m.applyTheme(autoThemeFor(m.th, msg))
		}
		return m, nil
	case eventMsg:
		if m.events == nil || m.staleOwner(msg.owner) {
			return m, nil
		}
		m.handleFrame(msg.event.Frame)
		m.relayoutFooter()
		if msg.event.Frame.Type == protocol.FrameClose || m.state == "closed" {
			if m.caps.ReturnHome {
				// Entered from the home screen: the bridge closing (external
				// stop, idle timeout) sends the person back to the list with
				// a notice instead of leaving the whole TUI (spec §5).
				return m, returnHomeCmd("El puente " + shortInstance(m.transport.InstanceID()) + " se cerró")
			}
			return m, tea.Quit
		}
		return m, waitForEvent(m.eventsCtx, m.events, m.id)
	case reconnectTickMsg:
		if m.staleOwner(msg.owner) {
			return m, nil
		}
		if m.state != "closed" && m.transport != nil && !m.transport.Connected() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := m.transport.Reconnect(ctx)
			cancel()
			if err != nil {
				m.state = "reconnecting"
				m.error = "reconexión pendiente"
			}
		}
		// Toasts have no timer of their own (§6.7): piggyback their expiry
		// check on this existing 2s tick so an idle screen still clears
		// them without a dedicated ticker.
		if live := pruneToasts(m.toasts, m.now()); len(live) != len(m.toasts) {
			m.toasts = live
			m.relayoutFooter()
		}
		return m, reconnectTick(m.id)
	case statusPollTickMsg:
		if m.staleOwner(msg.owner) {
			return m, nil
		}
		if provider, ok := m.transport.(StatusProvider); ok {
			return m, tea.Batch(fetchStatusCmd(provider, m.id), statusPollTick(m.id))
		}
		return m, statusPollTick(m.id)
	case statusResultMsg:
		if m.staleOwner(msg.owner) {
			return m, nil
		}
		if msg.err == nil {
			if m.peerKnown && m.peerWasConnected != msg.status.PeerConnected {
				if msg.status.PeerConnected {
					m.pushToast(roleName(peerRole(m.localRole)) + " conectado")
				} else {
					m.pushToast(roleName(peerRole(m.localRole)) + " desconectado")
				}
			}
			m.peerKnown = true
			m.peerWasConnected = msg.status.PeerConnected
			m.status = msg.status
			m.haveStatus = true
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg)
	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)
	case tea.MouseReleaseMsg, tea.MouseMotionMsg:
		// Releases and plain motion (no button held) carry no action of
		// their own here — every click is handled on the press, and
		// nothing in this UI needs drag tracking.
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	if m.focus == focusComposer {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// handleKey routes one keypress through the central keymap (internal/tui/keys):
// global bindings first, then whichever pane has focus. Anything left
// unmatched falls through to the composer's textarea when it has focus, so
// ordinary typing keeps working exactly as before.
func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Overlays take priority over everything else, in the order a person
	// can open them (a dialog can appear from the palette, so it must win
	// over the palette's own keys; the palette and search both suspend the
	// rest of the shell while open). Only one of them is ever open at a
	// time — opening one closes the others (see openPalette/openSearch).
	if m.confirm != nil {
		return m.handleDialogKey(msg)
	}
	if m.paletteOpen {
		return m.handlePaletteKey(msg)
	}
	if m.searchActive {
		return m.handleSearchKey(msg)
	}
	if m.showHelp {
		return m.handleHelpKey(msg)
	}
	switch {
	case msg.String() == "esc" && m.caps.ReturnHome:
		// Every overlay and the search field already consumed esc above, so
		// reaching here means nothing is open: back to the bridge list.
		return m, returnHomeCmd("")
	case key.Matches(msg, m.keymap.Global.Quit) || msg.String() == "ctrl+q":
		m.close()
		return m, tea.Quit
	case key.Matches(msg, m.keymap.Global.FocusToggle):
		m.toggleFocus()
		return m, nil
	case key.Matches(msg, m.keymap.Global.HelpToggle):
		m.openHelp()
		return m, nil
	case key.Matches(msg, m.keymap.Global.SidebarToggle):
		m.toggleSidebar()
		return m, nil
	case key.Matches(msg, m.keymap.Global.Search):
		m.openSearch()
		return m, nil
	case key.Matches(msg, m.keymap.Global.Palette):
		m.openPalette()
		return m, nil
	case msg.String() == "f5" && m.caps.Pair:
		m.copyJoinCommand()
		return m, nil
	}

	if m.focus == focusConversation {
		switch {
		case key.Matches(msg, m.keymap.Conversation.Help):
			m.openHelp()
		case key.Matches(msg, m.keymap.Conversation.Up):
			m.moveSelection(-1)
		case key.Matches(msg, m.keymap.Conversation.Down):
			m.moveSelection(1)
		case key.Matches(msg, m.keymap.Conversation.Top):
			m.selectFirst()
		case key.Matches(msg, m.keymap.Conversation.Bottom):
			m.selectLast()
			m.unread = 0
		case key.Matches(msg, m.keymap.Conversation.PageUp):
			m.viewport.PageUp()
		case key.Matches(msg, m.keymap.Conversation.PageDown):
			m.viewport.PageDown()
		case key.Matches(msg, m.keymap.Conversation.ToggleFold):
			m.toggleFold()
		case key.Matches(msg, m.keymap.Conversation.Copy):
			m.copySelected()
		case key.Matches(msg, m.keymap.Conversation.SearchNext) && m.searchQuery != "":
			m.jumpSearch(1)
		case key.Matches(msg, m.keymap.Conversation.SearchPrev) && m.searchQuery != "":
			m.jumpSearch(-1)
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keymap.Composer.Send):
		m.submit()
		m.relayoutFooter()
		if m.state == "closed" {
			return m, tea.Quit
		}
		return m, nil
	case key.Matches(msg, m.keymap.Composer.CycleLabel):
		m.cycleLabel()
		return m, nil
	case key.Matches(msg, m.keymap.Composer.HistoryUp) && (m.input.Value() == "" || m.browsingHistory):
		m.historyPrev()
		return m, nil
	case key.Matches(msg, m.keymap.Composer.HistoryDown) && m.browsingHistory:
		m.historyNext()
		return m, nil
	}

	m.browsingHistory = false
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) toggleFocus() {
	if m.focus == focusComposer {
		m.focus = focusConversation
		m.input.Blur()
	} else {
		m.focus = focusComposer
		m.input.Focus()
	}
	// The selection marker (§6.3) only shows while the conversation has
	// focus, so every focus change must re-render it in or out.
	m.refreshViewport(false)
}

// cycleLabel rotates the composer's label selector (§6.5): ninguna → TAREA
// → PREGUNTA → RESPUESTA → FIN → URGENTE → PROGRESO → ninguna.
func (m *Model) cycleLabel() {
	m.labelIdx = (m.labelIdx + 1) % len(composerLabels)
	m.updateComposerPlaceholder()
}

func (m *Model) currentLabel() string {
	return composerLabels[m.labelIdx]
}

func (m *Model) updateComposerPlaceholder() {
	who := roleName(m.localRole)
	label := m.currentLabel()
	if label == "" {
		label = "ninguna"
	}
	m.input.Placeholder = fmt.Sprintf("Escribir como %s (humano) · etiqueta: %s · Ctrl+T rota · Ctrl+S envía", who, label)
}

// historyPrev/historyNext implement §6.5's send history: with the composer
// empty, ↑/↓ recalls what this session has sent, most recent first.
func (m *Model) historyPrev() {
	if m.historyIdx <= 0 {
		return
	}
	m.historyIdx--
	m.input.SetValue(m.history[m.historyIdx])
	m.browsingHistory = true
}

func (m *Model) historyNext() {
	if m.historyIdx >= len(m.history)-1 {
		m.historyIdx = len(m.history)
		m.input.SetValue("")
		m.browsingHistory = false
		return
	}
	m.historyIdx++
	m.input.SetValue(m.history[m.historyIdx])
}

func (m *Model) resize() {
	if m.width < 20 {
		m.width = 20
	}
	if m.height < 10 {
		m.height = 10
	}
	// The composer is wrapped in a border (Part A §3: "el composer con
	// borde de foco visible"), which costs 2 extra columns and 2 extra
	// rows beyond the textarea's own content.
	const composerBorder = 2
	inputWidth := m.width - composerBorder
	if inputWidth < 1 {
		inputWidth = 1
	}
	m.input.SetWidth(inputWidth)
	inputHeight := 4
	if m.height < 16 {
		inputHeight = 3
	}
	m.input.SetHeight(inputHeight)

	// The conversation always gets its own border too now (spec's mouse
	// addendum, item 1: focus must be *visible*, not just tracked
	// internally — a border on both panes, not only the composer, is what
	// actually makes tab's effect legible). The sidebar (§6.4) gets its
	// own border, padding and a real gap from the conversation (item 3):
	// no more tarjetas touching the panel directly.
	const convBorder = 2 // rounded border: 1 column/row each side
	const sidebarGap = 2 // spec: "un hueco de al menos 2 columnas"
	const sidebarBorder = 2
	const sidebarPadding = 2 // spec: "padding interno" — 1 column each side

	sidebarOn := m.sidebarVisible()
	sidebarBoxWidth := 0
	reserveForSidebar := 0
	if sidebarOn {
		sidebarBoxWidth = sidebarWidth + sidebarBorder + sidebarPadding
		reserveForSidebar = sidebarBoxWidth + sidebarGap
		if m.width-reserveForSidebar-convBorder < 20 {
			// The conversation always wins the room: ctrl+b can force the
			// sidebar open below the usual breakpoint, but never at the
			// cost of an unreadably narrow conversation.
			sidebarOn = false
			sidebarBoxWidth = 0
			reserveForSidebar = 0
		}
	}
	convBoxWidth := m.width - reserveForSidebar
	convContentWidth := convBoxWidth - convBorder
	if convContentWidth < 1 {
		convContentWidth = 1
	}
	m.viewport.SetWidth(convContentWidth)

	// The search field's own Width must always be a real, positive number
	// (see the note in New()): below 1, bubbles' textinput can only ever
	// show the placeholder's first rune, rendered as if it were the
	// cursor's own character — indistinguishable from a stray keystroke
	// that resists backspace, which is exactly the bug report this fixes.
	// Kept in sync with the terminal for the same reason New() gives it an
	// initial width at all: bubbles' textinput placeholder rendering
	// breaks down below Width() 1 (see New()'s comment).
	searchWidth := m.width - len("Buscar: ") - 2
	if searchWidth < 8 {
		searchWidth = 8
	}
	m.searchInput.SetWidth(searchWidth)

	footerLines := m.footerLineCount()
	m.footerHeight = footerLines
	// heightOverhead is everything View() always draws that is not the
	// conversation box, the footer, or the composer's own content: the
	// status bar (1 row), the blank line right after it (1 row), and the
	// composer's border (2 rows) — 4 rows total. Nothing else in View()
	// adds a row of its own beyond what footerLines and inputHeight
	// already count: the conversation box sits directly against the
	// footer, and the footer sits directly against the composer's border,
	// with no extra blank line at either junction. This is checked
	// directly against View()'s own line count (layout_test.go) rather
	// than re-derived by eye: an earlier version of this formula
	// double-subtracted the composer's border (once here, once already
	// folded into an unrelated "-2" fudge factor) and, separately,
	// footerLineCount never counted the copy-info line (host pairing
	// instructions) that View() prints whenever it is set — either
	// mismatch alone silently makes the real content one or more rows
	// taller or shorter than resize() budgeted for, and in a real
	// terminal that is exactly how the status bar can end up scrolled
	// off the top.
	const heightOverhead = 4
	convBoxHeight := m.height - inputHeight - heightOverhead - footerLines
	if convBoxHeight < 3 {
		convBoxHeight = 3
	}
	convContentHeight := convBoxHeight - convBorder
	if convContentHeight < 1 {
		convContentHeight = 1
	}
	m.viewport.SetHeight(convContentHeight)

	m.convBoxWidth = convBoxWidth
	m.convBoxHeight = convBoxHeight
	m.sidebarBoxOn = sidebarOn
	m.sidebarBoxWidth = sidebarBoxWidth

	m.refreshViewport(false)
}

// footerLineCount is every extra line the footer can grow by beyond the
// always-present shortcuts bar: the error line, the copy-info line (host
// pairing), the unread indicator and the search field (while active).
// Toasts float over the conversation (toastWindow, toast.go) and never
// occupy a footer line. resize() and relayoutFooter both call this so the
// two can never drift apart from what View() actually draws.
func (m *Model) footerLineCount() int {
	footerLines := lipgloss.Height(m.shortcutsBarLine())
	if m.error != "" {
		footerLines++
	}
	if m.copyInfo != "" && m.caps.Pair {
		footerLines++
	}
	if m.unread > 0 {
		footerLines++
	}
	if m.searchActive {
		footerLines++
	}
	return footerLines
}

// relayoutFooter resizes the viewport when the footer's height changed (an
// error line, the unread indicator or search appeared or went away), so
// the input box is never pushed below the bottom of the terminal.
func (m *Model) relayoutFooter() {
	if m.footerLineCount() != m.footerHeight {
		m.resize()
	}
}

func (m *Model) handleFrame(frame protocol.Frame) {
	switch frame.Type {
	case protocol.FrameWelcome:
		m.state = "connected"
		m.error = ""
	case protocol.FrameMessage:
		if frame.Envelope == nil {
			return
		}
		isOwn := frame.Envelope.SenderRole == m.localRole
		wasAtBottom := m.viewport.AtBottom()
		added := m.appendMessage(*frame.Envelope)
		if added || isOwn {
			if isOwn && frame.Envelope.ServerSeq == 0 {
				// A local publish (TUI or ctl) is echoed by the EventHub before the
				// server accepts it; it stays queued until FrameAccepted arrives.
				if _, known := m.statuses[frame.Envelope.MessageID]; !known {
					m.setStatusForward(frame.Envelope.MessageID, "queued-ram")
				}
			} else if isOwn {
				m.setStatusForward(frame.Envelope.MessageID, "accepted")
			} else {
				m.setStatusForward(frame.Envelope.MessageID, "received")
			}
		}
		if !isOwn {
			// Capabilities.Ack is the real gate now (Part A §4): a mode
			// whose capability says it never confirms a peer message (the
			// local-embedded and standalone observers) must not call Ack
			// even if the wired Transport would honor it — this is
			// defense in depth on top of ControlTransport's own
			// documented no-op, so a future Transport that does confirm
			// can never be plugged into an observer mode by accident.
			if m.caps.Ack {
				_ = m.transport.Ack(frame.Envelope.MessageID, frame.Envelope.ServerSeq)
			}
			// §6.3 "Seguimiento": a peer message while scrolled away from the
			// bottom does not yank the view — it counts toward the "↓ N
			// mensajes nuevos" indicator instead.
			if added && !wasAtBottom {
				m.unread++
			}
		}
		m.state = "connected"
	case protocol.FrameAckConfirmed:
		m.setStatusForward(frame.MessageID, "delivered")
	case protocol.FrameAccepted:
		m.setStatusForward(frame.MessageID, "accepted")
		m.state = "connected"
	case protocol.FrameDelivered:
		m.setStatusForward(frame.MessageID, "delivered")
	case protocol.FrameError:
		if frame.MessageID != "" {
			m.setStatusForward(frame.MessageID, "rejected")
		}
		m.error = frame.Code + ": " + frame.Detail
	case protocol.FrameTransportError:
		m.state = "reconnecting"
		m.error = frame.Detail
	case protocol.FrameClose:
		m.state = "closed"
		m.error = "instancia cerrada por el orquestador"
	}
	m.lastActivity = m.now()
	m.refreshViewport(false)
}

// messageStatusRank orders the statuses a message can move through so a
// message's displayed status only ever advances. Statuses not listed here
// (e.g. "received", "rejected") are always applied unguarded.
var messageStatusRank = map[string]int{
	"queued-ram": 0,
	"accepted":   1,
	"delivered":  2,
}

// setStatusForward assigns status to messageID unless the message already
// carries a status further along messageStatusRank: a control-plane watch
// can legitimately replay an event this Model has already processed (e.g.
// after a reconnect following CURSOR_EXPIRED re-requests the whole retained
// window), and a replayed "accepted" arriving after "delivered" must never
// move the displayed status backwards.
func (m *Model) setStatusForward(messageID, status string) {
	if messageID == "" {
		return
	}
	if newRank, ranked := messageStatusRank[status]; ranked {
		if current, exists := m.statuses[messageID]; exists {
			if currentRank, ok := messageStatusRank[current]; ok && currentRank > newRank {
				return
			}
		}
	}
	m.statuses[messageID] = status
}

func (m *Model) appendMessage(e protocol.Envelope) bool {
	if _, exists := m.byID[e.MessageID]; exists {
		return false
	}
	m.byID[e.MessageID] = len(m.messages)
	m.messages = append(m.messages, e)
	return true
}

func (m *Model) submit() {
	typed := m.input.Value()
	if typed == "" {
		return
	}
	if strings.HasPrefix(typed, "/") && !strings.Contains(typed, "\n") && isCommand(typed) {
		m.command(typed)
		m.input.Reset()
		return
	}
	body := typed
	if label := m.currentLabel(); label != "" {
		body = label + "\n" + typed
	}
	m.stopConfirm = false
	e, err := m.transport.Publish(body)
	if err != nil {
		m.error = err.Error()
		return
	}
	m.appendMessage(e)
	m.statuses[e.MessageID] = "queued-ram"
	m.history = append(m.history, typed)
	m.historyIdx = len(m.history)
	m.browsingHistory = false
	m.input.Reset()
	m.unread = 0
	m.refreshViewport(true)
}

func isCommand(body string) bool {
	switch strings.TrimSpace(body) {
	case "/status", "/pair", "/stop", "/quit":
		return true
	default:
		return false
	}
}

func (m *Model) command(command string) {
	trimmed := strings.TrimSpace(command)
	if trimmed != "/stop" {
		// Any other command cancels a pending confirmation, so a stray
		// second /stop long after the first can never fire by accident.
		m.stopConfirm = false
	}
	switch trimmed {
	case "/status":
		pending, bytes := m.transport.QueueStats()
		m.error = fmt.Sprintf("estado=%s · cola RAM=%d mensajes/%d bytes", m.state, pending, bytes)
	case "/pair":
		if !m.caps.Pair || m.onPair == nil {
			m.error = "PAIRING_FORBIDDEN: solo el host Mac puede generar un token"
			return
		}
		command := m.onPair()
		if strings.HasPrefix(command, "error:") {
			m.error = command
			return
		}
		m.joinCommand = command
		m.copyJoinCommand()
		m.error = "nuevo comando generado; ya fue copiado para Codex/PowerShell"
		m.resize()
	case "/stop":
		m.handleStopCommand()
	case "/quit":
		m.close()
	default:
		m.error = "comando desconocido; usa /status, /pair, /stop o /quit"
	}
}

// close ends this Model's own window. It also asks the endpoint to tear
// down the whole bridge exactly when Capabilities.CloseOnQuit says so
// (§4's "cerrar el puente al salir"): true for host, join and the local
// embedded observer, false for the standalone `agents-bridge tui`, which
// only ever closes its own view.
func (m *Model) close() {
	if m.caps.CloseOnQuit && m.onStop != nil {
		m.onStop()
	}
	if m.transport != nil {
		m.transport.Close()
	}
	m.state = "closed"
}

// handleStopCommand implements /stop (§4's "stop desde la paleta", every
// mode). When Capabilities.StopConfirm is false (host, join) it behaves
// exactly like /quit: this side owns its own connection outright, so there
// is nothing to confirm. When it is true (the local-embedded and
// standalone observers) a second /stop is required, and even then it only
// asks the endpoint to stop — unlike close(), it does not mark itself
// closed or stop its own transport: this Model is watching someone else's
// bridge, and keeps watching until the watch stream itself reports the
// instance is gone (a FrameClose event).
func (m *Model) handleStopCommand() {
	if !m.caps.StopConfirm {
		m.close()
		return
	}
	if m.onStop == nil {
		m.error = "STOP_FORBIDDEN: este endpoint no puede cerrar el puente"
		return
	}
	if !m.stopConfirm {
		m.stopConfirm = true
		m.error = "¿Cerrar el puente? escribe /stop otra vez para confirmar"
		return
	}
	m.stopConfirm = false
	m.onStop()
	m.error = "solicitud de cierre enviada"
}

func (m *Model) compact() bool { return m.width < compactWidthThreshold }

// selectionForRender is the message id renderConversationDetailed should
// mark as selected: only while the conversation actually has focus, since
// the selection marker is meaningless (and would be confusing) while
// typing in the composer.
func (m *Model) selectionForRender() string {
	if m.focus != focusConversation {
		return ""
	}
	idx := m.selectedIndex()
	if idx < 0 {
		return ""
	}
	return m.messages[idx].MessageID
}

func (m *Model) refreshViewport(toBottom bool) {
	content, offsets := renderConversationDetailed(m.messages, m.statuses, conversationParams{
		Theme:     m.th,
		Width:     m.viewport.Width(),
		Compact:   m.compact(),
		LocalRole: m.localRole,
		Selected:  m.selectionForRender(),
		Expanded:  m.expanded,
		Collapsed: m.collapsed,
		Search:    m.searchQuery,
		MD:        m.mdCache,
	})
	m.messageOffsets = offsets
	m.conversationLines = strings.Split(content, "\n")
	atBottomBefore := m.viewport.AtBottom()
	m.viewport.SetContent(content)
	if toBottom || atBottomBefore {
		m.viewport.GotoBottom()
	}
}
