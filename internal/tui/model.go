// Package tui provides the small chat-like terminal UI for both roles.
package tui

import (
	"context"
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
	Client      *bridge.Client
	LocalRole   protocol.Role
	JoinCommand string
	OnStop      func()
	OnPair      func() string
}

type frameMsg struct{ frame protocol.Frame }
type reconnectTickMsg struct{}

type Model struct {
	client      *bridge.Client
	localRole   protocol.Role
	joinCommand string
	onStop      func()
	onPair      func() string

	input    textarea.Model
	viewport viewport.Model

	messages []protocol.Envelope
	byID     map[string]int
	statuses map[string]string
	state    string
	error    string
	width    int
	height   int
}

func New(options Options) Model {
	input := textarea.New()
	input.Prompt = "│ "
	input.Placeholder = "Pega el mensaje exacto de Codex · Ctrl+Enter o Ctrl+S envía"
	input.CharLimit = protocol.MaxBodyBytes
	input.SetHeight(4)
	input.SetWidth(80)
	input.ShowLineNumbers = false

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(18))
	return Model{
		client:      options.Client,
		localRole:   options.LocalRole,
		joinCommand: options.JoinCommand,
		onStop:      options.OnStop,
		onPair:      options.OnPair,
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
	return tea.Batch(m.input.Focus(), waitForFrame(m.client), reconnectTick())
}

func waitForFrame(client *bridge.Client) tea.Cmd {
	return func() tea.Msg {
		select {
		case frame := <-client.Events():
			return frameMsg{frame: frame}
		case <-client.Done():
			return frameMsg{frame: protocol.Frame{Type: protocol.FrameClose, State: protocol.StateClosed, Detail: "cliente cerrado"}}
		}
	}
}

func reconnectTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return reconnectTickMsg{} })
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case frameMsg:
		cmds := []tea.Cmd{waitForFrame(m.client)}
		m.handleFrame(msg.frame)
		if m.state == "closed" {
			return m, tea.Quit
		}
		return m, tea.Batch(cmds...)
	case reconnectTickMsg:
		if m.state != "closed" && !m.client.Connected() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := m.client.Reconnect(ctx)
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
	m.viewport.SetHeight(m.height - inputHeight - 5)
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
			if isOwn {
				m.statuses[frame.Envelope.MessageID] = "accepted"
			} else {
				m.statuses[frame.Envelope.MessageID] = "received"
			}
		}
		if !isOwn {
			_ = m.client.Ack(frame.Envelope.MessageID, frame.Envelope.ServerSeq)
		}
		m.state = "connected"
	case protocol.FrameAckConfirmed:
		if frame.MessageID != "" {
			m.statuses[frame.MessageID] = "delivered"
		}
	case protocol.FrameAccepted:
		if frame.MessageID != "" {
			m.statuses[frame.MessageID] = "accepted"
		}
		m.state = "connected"
	case protocol.FrameDelivered:
		if frame.MessageID != "" {
			m.statuses[frame.MessageID] = "delivered"
		}
	case protocol.FrameError:
		if frame.MessageID != "" {
			m.statuses[frame.MessageID] = "rejected"
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
	e, err := m.client.Publish(body)
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
	switch strings.TrimSpace(command) {
	case "/status":
		pending, bytes := m.client.QueueStats()
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
		m.error = "nuevo comando de unión generado; cópialo al ejecutor"
	case "/stop":
		if m.localRole != protocol.RoleOrchestrator || m.onStop == nil {
			m.error = "STOP_FORBIDDEN: solo Mac puede cerrar la instancia"
			return
		}
		m.close()
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
	m.client.Close()
	m.state = "closed"
}

func (m *Model) refreshViewport(toBottom bool) {
	content := renderMessages(m.messages, m.statuses, m.localRole, m.viewport.Width())
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

func (m Model) View() tea.View {
	header := fmt.Sprintf("CODEX-BRIDGE  %s  |  %s  |  %s", m.client.InstanceID(), m.localRole, m.state)
	if m.joinCommand != "" && m.localRole == protocol.RoleOrchestrator {
		header += "\nÚnete desde Windows: " + m.joinCommand
	}
	footer := "Ctrl+Enter/Ctrl+S enviar · Enter nueva línea · PgUp/PgDn scroll · /status · /quit"
	if m.error != "" {
		footer += "\n" + m.error
	}
	return tea.NewView(header + "\n\n" + m.viewport.View() + "\n\n" + footer + "\n" + m.input.View())
}
