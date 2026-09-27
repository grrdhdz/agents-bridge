// Package tui provides the small chat-like terminal UI for both roles.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

type Options struct {
	// Client is the direct TCP path: the Mac/Windows TUI's usual case. It is
	// wrapped in clientTransport automatically. Ignored when Transport is set.
	Client *bridge.Client
	// Transport lets a caller supply a non-*bridge.Client message channel,
	// namely the observing TUI's control-plane client. When both are unset,
	// the model has no transport at all (used by a few tests that only
	// exercise input/layout).
	Transport   Transport
	LocalRole   protocol.Role
	JoinCommand string
	CopyCommand func(string) error
	OnStop      func()
	OnPair      func() string
	// Observer marks this Model as the observing TUI (§6), driving every
	// difference from the direct Mac/Windows TUI through this one flag
	// rather than a second Update: it shows each message's time and origin
	// (human/agent), requires typing /stop twice to confirm before calling
	// OnStop, and never marks itself closed when OnStop succeeds — the
	// bridge is someone else's to close; this TUI keeps watching until the
	// watch stream itself reports the instance is gone.
	Observer bool
}

type eventMsg struct{ event bridge.Event }
type reconnectTickMsg struct{}

type Model struct {
	transport   Transport
	localRole   protocol.Role
	joinCommand string
	copyCommand func(string) error
	onStop      func()
	onPair      func() string
	observer    bool
	stopConfirm bool
	events      EventSubscription

	input    textarea.Model
	viewport viewport.Model

	messages []protocol.Envelope
	byID     map[string]int
	statuses map[string]string
	state    string
	error    string
	copyInfo string
	width    int
	height   int
}

func New(options Options) Model {
	input := textarea.New()
	input.Prompt = "│ "
	if options.Observer {
		// The observer never pastes a Codex report; it is the human operator
		// intervening in someone else's conversation (§6).
		input.Placeholder = "Escribe para intervenir como humano · Ctrl+S envía"
	} else {
		input.Placeholder = "Pega el mensaje exacto de Codex · Ctrl+Enter o Ctrl+S envía"
	}
	input.CharLimit = protocol.MaxBodyBytes
	input.SetHeight(4)
	input.SetWidth(80)
	input.ShowLineNumbers = false

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(18))
	vp.SoftWrap = true
	vp.MouseWheelEnabled = true
	transport := options.Transport
	if transport == nil && options.Client != nil {
		transport = clientTransport{client: options.Client}
	}
	return Model{
		transport:   transport,
		localRole:   options.LocalRole,
		joinCommand: options.JoinCommand,
		copyCommand: options.CopyCommand,
		onStop:      options.OnStop,
		onPair:      options.OnPair,
		observer:    options.Observer,
		input:       input,
		viewport:    vp,
		byID:        make(map[string]int),
		statuses:    make(map[string]string),
		state:       "connecting",
		width:       80,
		height:      24,
	}
}

func (m *Model) Init() tea.Cmd {
	if m.localRole == protocol.RoleOrchestrator && m.joinCommand != "" {
		m.copyJoinCommand()
	}
	cmds := []tea.Cmd{m.input.Focus(), reconnectTick()}
	if m.transport != nil {
		sub, err := m.transport.Subscribe(0)
		if err == nil {
			m.events = sub
			cmds = append(cmds, waitForEvent(sub))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) copyJoinCommand() {
	if m.localRole != protocol.RoleOrchestrator || m.joinCommand == "" {
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
	m.copyInfo = "Comando Windows copiado. Pégalo en Codex o PowerShell; F5 vuelve a copiar."
}

func waitForEvent(sub EventSubscription) tea.Cmd {
	return func() tea.Msg {
		if sub == nil {
			return nil
		}
		event, err := sub.Next(context.Background())
		if err != nil {
			frameType := protocol.FrameTransportError
			if errors.Is(err, bridge.ErrClosed) {
				frameType = protocol.FrameClose
			}
			return eventMsg{event: bridge.Event{Kind: bridge.EventLifecycle, State: "closed", Detail: err.Error(), Frame: protocol.Frame{Type: frameType, Detail: err.Error()}}}
		}
		return eventMsg{event: event}
	}
}

func reconnectTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return reconnectTickMsg{} })
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case eventMsg:
		if m.events == nil {
			return m, nil
		}
		m.handleFrame(msg.event.Frame)
		if msg.event.Frame.Type == protocol.FrameClose || m.state == "closed" {
			return m, tea.Quit
		}
		return m, waitForEvent(m.events)
	case reconnectTickMsg:
		if m.state != "closed" && !m.transport.Connected() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := m.transport.Reconnect(ctx)
			cancel()
			if err != nil {
				m.state = "reconnecting"
				m.error = "reconexión pendiente"
			}
		}
		return m, reconnectTick()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case tea.MouseWheelMsg:
		var viewportCmd tea.Cmd
		m.viewport, viewportCmd = m.viewport.Update(msg)
		return m, viewportCmd
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			m.close()
			return m, tea.Quit
		case "ctrl+enter", "ctrl+s":
			m.submit()
			if m.state == "closed" {
				return m, tea.Quit
			}
			return m, nil
		case "f5":
			m.copyJoinCommand()
			return m, nil
		case "pgup":
			m.viewport.PageUp()
			return m, nil
		case "pgdown":
			m.viewport.PageDown()
			return m, nil
		case "home":
			m.viewport.GotoTop()
			return m, nil
		case "end":
			m.viewport.GotoBottom()
			return m, nil
		}
	}

	var cmds []tea.Cmd
	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	if inputCmd != nil {
		cmds = append(cmds, inputCmd)
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) resize() {
	if m.width < 20 {
		m.width = 20
	}
	if m.height < 10 {
		m.height = 10
	}
	m.input.SetWidth(m.width)
	inputHeight := 4
	if m.height < 16 {
		inputHeight = 3
	}
	m.input.SetHeight(inputHeight)
	m.viewport.SetWidth(m.width)
	headerLines := 1
	if m.localRole == protocol.RoleOrchestrator && m.joinCommand != "" {
		headerLines++
	}
	footerLines := 1
	if m.error != "" {
		footerLines++
	}
	viewportHeight := m.height - inputHeight - headerLines - footerLines - 2
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	m.viewport.SetHeight(viewportHeight)
	m.refreshViewport(false)
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
		if m.appendMessage(*frame.Envelope) || isOwn {
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
			_ = m.transport.Ack(frame.Envelope.MessageID, frame.Envelope.ServerSeq)
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
	m.refreshViewport(true)
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
// carries a status further along messageStatusRank (§6, bug A3): a control-
// plane watch can legitimately replay an event this Model has already
// processed — for example after a reconnect following CURSOR_EXPIRED
// re-requests the whole retained window — and a replayed "accepted" arriving
// after "delivered" must never move the displayed status backwards, even if
// the corresponding later "delivered" event is never seen again.
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
	body := m.input.Value()
	if body == "" {
		return
	}
	if strings.HasPrefix(body, "/") && !strings.Contains(body, "\n") && isCommand(body) {
		m.command(body)
		m.input.Reset()
		return
	}
	m.stopConfirm = false
	e, err := m.transport.Publish(body)
	if err != nil {
		m.error = err.Error()
		return
	}
	m.appendMessage(e)
	m.statuses[e.MessageID] = "queued-ram"
	m.input.Reset()
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
		if m.localRole != protocol.RoleOrchestrator || m.onPair == nil {
			m.error = "PAIRING_FORBIDDEN: solo Mac puede generar un token"
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

func (m *Model) close() {
	if m.localRole == protocol.RoleOrchestrator && m.onStop != nil {
		m.onStop()
	}
	m.transport.Close()
	m.state = "closed"
}

// handleStopCommand implements /stop for both TUI kinds (§4.2, §6). The
// direct Mac TUI closes immediately, as before. The observer requires typing
// /stop a second time to confirm, then only asks the endpoint to stop —
// unlike close(), it does not mark itself closed or stop its own transport:
// this TUI is watching someone else's bridge, and keeps watching until the
// watch stream itself reports the instance is gone (a FrameClose event).
func (m *Model) handleStopCommand() {
	if m.onStop == nil {
		m.error = "STOP_FORBIDDEN: este endpoint no puede cerrar el puente"
		return
	}
	if !m.observer {
		if m.localRole != protocol.RoleOrchestrator {
			m.error = "STOP_FORBIDDEN: solo Mac puede cerrar la instancia"
			return
		}
		m.close()
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

func (m *Model) refreshViewport(toBottom bool) {
	var content string
	if m.observer {
		content = renderMessagesWithOrigin(m.messages, m.statuses, m.localRole, m.viewport.Width())
	} else {
		content = renderMessages(m.messages, m.statuses, m.localRole, m.viewport.Width())
	}
	m.viewport.SetContent(content)
	if toBottom || m.viewport.AtBottom() {
		m.viewport.GotoBottom()
	}
}

func renderMessages(messages []protocol.Envelope, statuses map[string]string, localRole protocol.Role, width int) string {
	if len(messages) == 0 {
		return "\n  Aún no hay mensajes. Pega un reporte de Codex y pulsa Ctrl+Enter o Ctrl+S."
	}
	if width < 20 {
		width = 20
	}
	own := lipgloss.NewStyle().Width(width).Align(lipgloss.Right).Foreground(lipgloss.Color("86efac"))
	remote := lipgloss.NewStyle().Width(width).Align(lipgloss.Left).Foreground(lipgloss.Color("93c5fd"))
	blocks := make([]string, 0, len(messages))
	for _, e := range messages {
		who := "Windows"
		if localRole == protocol.RoleExecutor {
			who = "Mac"
		}
		style := remote
		if e.SenderRole == localRole {
			who = "Tú"
			style = own
		}
		status := statuses[e.MessageID]
		if status == "" {
			status = "received"
		}
		block := fmt.Sprintf("%s · %s\n%s", who, status, e.Body)
		blocks = append(blocks, style.Render(block))
	}
	return strings.Join(blocks, "\n\n")
}

// renderMessagesWithOrigin is renderMessages plus each message's time and
// origin (§6: "rol, hora, estado ... y origen"), used only by the observing
// TUI. It is a separate function so the direct TUI's renderMessages and its
// tests never change shape.
func renderMessagesWithOrigin(messages []protocol.Envelope, statuses map[string]string, localRole protocol.Role, width int) string {
	if len(messages) == 0 {
		return "\n  Sin mensajes todavía."
	}
	if width < 20 {
		width = 20
	}
	own := lipgloss.NewStyle().Width(width).Align(lipgloss.Right).Foreground(lipgloss.Color("86efac"))
	remote := lipgloss.NewStyle().Width(width).Align(lipgloss.Left).Foreground(lipgloss.Color("93c5fd"))
	blocks := make([]string, 0, len(messages))
	for _, e := range messages {
		who := "Ejecutor"
		if localRole == protocol.RoleExecutor {
			who = "Orquestador"
		}
		style := remote
		if e.SenderRole == localRole {
			who = "Tú"
			style = own
		}
		status := statuses[e.MessageID]
		if status == "" {
			status = "received"
		}
		when := e.CreatedAt.Local().Format("15:04:05")
		block := fmt.Sprintf("%s · %s · %s · %s\n%s", who, status, when, originLabel(e.Source), e.Body)
		blocks = append(blocks, style.Render(block))
	}
	return strings.Join(blocks, "\n\n")
}

// originLabel classifies Envelope.Source (§6.1) for the observer's display.
func originLabel(source string) string {
	switch source {
	case protocol.SourceHumanOperator, protocol.SourceManualCodexCopy:
		return "humano"
	case protocol.SourceAgentControl:
		return "agente"
	case "":
		return "?"
	default:
		return source
	}
}

func (m Model) View() tea.View {
	if m.observer {
		return m.observerView()
	}
	header := fmt.Sprintf("CODEX-BRIDGE  %s  |  %s  |  %s", m.transport.InstanceID(), m.localRole, m.state)
	if m.joinCommand != "" && m.localRole == protocol.RoleOrchestrator {
		if m.copyInfo == "" {
			m.copyInfo = "Comando Windows listo; F5 lo copia al portapapeles."
		}
		header += "\n" + m.copyInfo
	}
	footer := "Ctrl+Enter/Ctrl+S enviar · Enter nueva línea · PgUp/PgDn/Home/End + rueda/trackpad scroll · /status · /quit"
	if m.localRole == protocol.RoleOrchestrator && m.joinCommand != "" {
		footer += " · F5 copiar"
	}
	if m.error != "" {
		footer += "\n" + m.error
	}
	view := tea.NewView(header + "\n\n" + m.viewport.View() + "\n\n" + footer + "\n" + m.input.View())
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

// observerView is View() for the observing TUI (§6): no join command / pair
// affordances (this TUI never owns a pairing), and a header that identifies
// it as an observer rather than as either role's own terminal.
func (m Model) observerView() tea.View {
	header := fmt.Sprintf("CODEX-BRIDGE OBSERVADOR  %s  |  %s", m.transport.InstanceID(), m.state)
	footer := "Ctrl+Enter/Ctrl+S enviar (como human-operator) · Enter nueva línea · PgUp/PgDn/Home/End + rueda/trackpad scroll · /status · /stop (dos veces confirma) · /quit"
	if m.error != "" {
		footer += "\n" + m.error
	}
	view := tea.NewView(header + "\n\n" + m.viewport.View() + "\n\n" + footer + "\n" + m.input.View())
	view.MouseMode = tea.MouseModeCellMotion
	return view
}
