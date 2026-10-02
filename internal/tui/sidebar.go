// sidebar.go implements §6.4's side panel: the bridge itself, both
// participants' presence and last activity, this side's undelivered
// messages (highlighted past 5 minutes) and totals per role. It only shows
// with the terminal at least sidebarMinWidth columns wide (§6.1), toggled
// with ctrl+b.
package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/agents-bridge/internal/control"
	"github.com/grrdhdz/agents-bridge/internal/protocol"
	"github.com/grrdhdz/agents-bridge/internal/tui/theme"
)

// sidebarMinWidth is spec §6.1's "≥ 110 columnas" breakpoint for showing
// the panel by default.
const sidebarMinWidth = 110

// sidebarWidth is how many columns the panel itself occupies once shown.
const sidebarWidth = 30

// pendingAgeWarning is spec §6.4's "> 5 minutos" highlight threshold for an
// undelivered message.
const pendingAgeWarning = 5 * time.Minute

// pendingItem is one of this side's own messages still short of
// "delivered".
type pendingItem struct {
	messageID string
	status    string
	age       time.Duration
}

// sidebarData is everything renderSidebar needs, pre-extracted from Model
// so the render function itself stays a pure, easily-tested transform.
type sidebarData struct {
	InstanceID    string
	Mode          string
	StartedAt     time.Time
	HaveStartedAt bool
	Idle          time.Duration
	PeerConnected bool
	HavePeer      bool

	OrchestratorLastActivity time.Time
	HaveOrchestratorActivity bool
	ExecutorLastActivity     time.Time
	HaveExecutorActivity     bool

	// OrchestratorState/ExecutorState are the participants' derived states
	// (§3.4), "" when unknown.
	OrchestratorState string
	ExecutorState     string

	Pending []pendingItem
	Totals  map[protocol.Role]int
}

// buildSidebarData extracts sidebarData from Model's own state (messages,
// statuses, last known BridgeStatus poll) at render time.
func (m *Model) buildSidebarData() sidebarData {
	data := sidebarData{
		InstanceID: m.transport.InstanceID(),
		Mode:       modeLabel(m.caps.Mode),
		Idle:       m.now().Sub(m.lastActivity),
		Totals:     map[protocol.Role]int{},
	}
	if m.haveStatus {
		data.HaveStartedAt = !m.status.StartedAt.IsZero()
		data.StartedAt = m.status.StartedAt
		data.HavePeer = true
		data.PeerConnected = m.status.PeerConnected
		data.OrchestratorState = roleStateText(m.status.Roles, protocol.RoleOrchestrator)
		data.ExecutorState = roleStateText(m.status.Roles, protocol.RoleExecutor)
	}
	now := m.now()
	for _, e := range m.messages {
		data.Totals[e.SenderRole]++
		if e.SenderRole == protocol.RoleOrchestrator {
			if !data.HaveOrchestratorActivity || e.CreatedAt.After(data.OrchestratorLastActivity) {
				data.OrchestratorLastActivity = e.CreatedAt
				data.HaveOrchestratorActivity = true
			}
		} else {
			if !data.HaveExecutorActivity || e.CreatedAt.After(data.ExecutorLastActivity) {
				data.ExecutorLastActivity = e.CreatedAt
				data.HaveExecutorActivity = true
			}
		}
		if e.SenderRole == m.localRole {
			status := m.statuses[e.MessageID]
			if status != "" && status != "delivered" {
				data.Pending = append(data.Pending, pendingItem{messageID: e.MessageID, status: status, age: now.Sub(e.CreatedAt)})
			}
		}
	}
	return data
}

// roleStateText is a role's state word for the panel, or "" when the source
// does not know it (a host/join never learns the other role's state).
func roleStateText(roles map[protocol.Role]control.RoleSnapshot, role protocol.Role) string {
	snap, ok := roles[role]
	if !ok || snap.State == control.StateUnknown {
		return ""
	}
	return string(snap.State)
}

// projectName is the status bar/sidebar's project label (§6.2/§6.4): the
// base name of the bridge's cwd when known, empty otherwise (observer/local
// get it from the descriptor via StatusProvider; host/join from the
// process's own working directory — see clientTransport.Status).
func projectName(cwd string) string {
	if cwd == "" {
		return ""
	}
	base := filepath.Base(cwd)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

func renderSidebar(data sidebarData, th theme.Theme, width int) string {
	if width < 10 {
		width = 10
	}
	var b strings.Builder
	section := func(title string) {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(th.MutedStyle().Bold(true).Render(title))
		b.WriteString("\n")
	}

	shortID := data.InstanceID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	section("PUENTE")
	fmt.Fprintf(&b, "%s\n%s\n", shortID, data.Mode)
	if data.HaveStartedAt {
		fmt.Fprintf(&b, "activo %s\n", formatIdle(time.Since(data.StartedAt)))
	}
	fmt.Fprintf(&b, "inactivo %s\n", formatIdle(data.Idle))

	section("PARTICIPANTES")
	writeParticipant(&b, th, "Orquestador", data.OrchestratorState, data.HaveOrchestratorActivity, data.OrchestratorLastActivity)
	writeParticipant(&b, th, "Ejecutor", data.ExecutorState, data.HaveExecutorActivity, data.ExecutorLastActivity)

	section("PENDIENTES")
	if len(data.Pending) == 0 {
		b.WriteString(th.MutedStyle().Render("ninguno"))
		b.WriteString("\n")
	} else {
		for _, p := range data.Pending {
			line := fmt.Sprintf("%s %s hace %s", th.StatusSymbol(p.status), shortMessageID(p.messageID), formatIdle(p.age))
			if p.age > pendingAgeWarning {
				line = th.NoticeStyle().Render(line)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	section("TOTALES")
	fmt.Fprintf(&b, "Orquestador: %d\n", data.Totals[protocol.RoleOrchestrator])
	fmt.Fprintf(&b, "Ejecutor: %d\n", data.Totals[protocol.RoleExecutor])

	return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(strings.TrimRight(b.String(), "\n"))
}

func writeParticipant(b *strings.Builder, th theme.Theme, name, state string, have bool, at time.Time) {
	indicator := "○"
	activity := "sin actividad"
	if have {
		indicator = "●"
		activity = "última " + at.Local().Format("15:04:05")
	}
	fmt.Fprintf(b, "%s %s\n", indicator, name)
	if state != "" {
		fmt.Fprintf(b, "  %s\n", th.TextStyle().Render(state))
	}
	fmt.Fprintf(b, "  %s\n", th.MutedStyle().Render(activity))
}

func shortMessageID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
