package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

// compactWidthThreshold is spec §6.1's "< 60 columnas" breakpoint: below it
// cards drop their margin and color bar.
const compactWidthThreshold = 60

// messageLabels are the markers §6.3 recognizes on a message's first line
// and renders as a colored badge instead of body text.
var messageLabels = map[string]bool{
	"TAREA":     true,
	"PREGUNTA":  true,
	"RESPUESTA": true,
	"RESULTADO": true,
	"FIN":       true,
}

// composerLabels is the subset a human composes with (§6.5's ctrl+t
// rotation: ninguna → TAREA → PREGUNTA → RESPUESTA → FIN). RESULTADO is
// recognized on receive (typically an executor's result) but is not one of
// the labels a human rotates through when composing.
var composerLabels = []string{"", "TAREA", "PREGUNTA", "RESPUESTA", "FIN"}

// splitLabel extracts a leading label marker from body's first line
// (§6.3), returning the label (or "" if none) and the remaining body with
// that line removed. Only an exact, case-sensitive match against
// messageLabels counts — anything else is left as ordinary body text.
func splitLabel(body string) (label, rest string) {
	first, remainder, hasNewline := strings.Cut(body, "\n")
	if !messageLabels[first] {
		return "", body
	}
	if !hasNewline {
		return first, ""
	}
	return first, remainder
}

func roleIcon(role protocol.Role) string {
	if role == protocol.RoleExecutor {
		return "▲"
	}
	return "◆"
}

func roleName(role protocol.Role) string {
	if role == protocol.RoleExecutor {
		return "Ejecutor"
	}
	return "Orquestador"
}

// originLabel classifies Envelope.Source (§6.1/§6.3) for the card header:
// a human operator (whether typed in the observer or pasted in the direct
// TUI) vs. an agent driving the endpoint itself.
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

// cardParams bundles renderCardWithParams' phase-2 options (Markdown
// rendering/cache, fold state and selection/search highlighting) so the
// core renderCard signature phase-1 tests already call stays untouched.
type cardParams struct {
	Status  string
	Theme   theme.Theme
	Width   int // the *maximum* the card may grow to (§6.3's 80% cap) — the card's real width is its own content, up to this cap.
	Compact bool
	// AlignRight is whether this card belongs to the conversation's local
	// role (§6.3's "disposición de chat"): its bar mirrors to the card's
	// right edge instead of its left, so it reads correctly once the
	// whole card is pushed flush against the conversation's own right
	// edge (renderConversationDetailed's placeCardBlock).
	AlignRight     bool
	Selected       bool
	ForceExpanded  bool
	ForceCollapsed bool
	Search         string
	MD             *markdownCache
}

// renderCard renders one message as a card (§6.3): header (icon, role
// name, origin, time, delivery status, optional label badge) followed by
// the body with any label line removed. width is the total space available
// to the card, including its color bar and margin when not compact. It is
// the phase-1 entry point: plain body, no Markdown, no fold, no selection —
// exactly what the phase-1 card tests exercise. Phase-2 features go through
// renderCardWithParams.
func renderCard(e protocol.Envelope, status string, th theme.Theme, width int, compact bool) string {
	return renderCardWithParams(e, cardParams{Status: status, Theme: th, Width: width, Compact: compact})
}

// cardBoxOverhead is a bordered card's own border (1 column each side) plus
// its 1-column interior padding (each side): 4 columns total that
// renderCardWithParams' content never gets to use for its own text.
const cardBoxOverhead = 4

// cardBoxStyle builds a message card's container (round 5: "cada mensaje
// va en un contenedor con borde ... no relleno"): a thin rounded border in
// the sender's own role color (or Human's, for a human-authored message —
// spec: "humano con su color si el origen es humano"), or, when selected,
// a double-line border in the accent color instead (spec: "borde grueso o
// doble en el color de acento") — a structural shape change any NO_COLOR
// terminal shows too, on top of the "▶ " marker already in the header, so
// selection is never color-only. No background: the card's interior sits
// on the ambient/painted terminal background exactly like everything else
// (view.go's paintFrameBackground) — round 4's tinted fill is gone.
func cardBoxStyle(th theme.Theme, borderColor color.Color, selected bool) lipgloss.Style {
	b := lipgloss.RoundedBorder()
	accent := borderColor
	if selected {
		b = lipgloss.DoubleBorder()
		accent = th.Info
	}
	style := lipgloss.NewStyle().Border(b).Padding(0, 1)
	if th.NoColor {
		return style
	}
	return style.BorderForeground(accent)
}

// renderCardWithParams is renderCard plus phase-2/3 content (§6.3): header
// (icon, role name, origin, time, delivery status, optional label badge)
// followed by the body, both inside one bordered container (cardBoxStyle)
// — round 5's line-border card design, replacing round 4's tinted-fill
// bubble. Markdown rendering (through MD when non-nil, otherwise plain
// word-wrap, matching renderCard's phase-1 behavior), folding long bodies
// and a search-match highlight all render inside that same container;
// lipgloss.Style.Width (border and padding included, same convention as
// windowBox/sideBox elsewhere in this package) does the rest of the
// layout, so there is no manual per-line rectangle painting to get wrong.
func renderCardWithParams(e protocol.Envelope, p cardParams) string {
	width := p.Width
	if width < 10 {
		width = 10
	}
	th := p.Theme
	origin := originLabel(e.Source)
	isHuman := origin == "humano"
	borderColor := th.RoleColor(e.SenderRole)
	if isHuman {
		borderColor = th.Human
	}

	header := th.RoleStyle(e.SenderRole).Render(fmt.Sprintf("%s %s", roleIcon(e.SenderRole), roleName(e.SenderRole)))
	originStyle := th.MutedStyle()
	if isHuman {
		originStyle = th.HumanStyle()
	}
	header += "  " + originStyle.Render(origin)
	header += "  " + th.MutedStyle().Render(e.CreatedAt.Local().Format("15:04:05"))
	header += "  " + th.TextStyle().Render(th.StatusSymbol(p.Status))
	label, body := splitLabel(e.Body)
	if label != "" {
		header += "  " + th.LabelStyle(label).Render(label)
	}
	if p.Selected {
		header = th.SelectionStyle().Render("▶ ") + header
	}

	contentWidth := width - cardBoxOverhead
	if contentWidth < 4 {
		contentWidth = 4
	}

	content := header
	if body != "" {
		rendered := p.MD.render(e.MessageID, body, th, contentWidth, e.SenderRole)
		rendered = applyFold(rendered, th, th.MutedStyle(), p.ForceExpanded, p.ForceCollapsed)
		if p.Search != "" {
			rendered = highlightSearch(rendered, p.Search, th)
		}
		content += "\n" + rendered
	}

	style := cardBoxStyle(th, borderColor, p.Selected)

	if p.Compact {
		// Compact cards fill the whole available width regardless of
		// their own content (spec §6.1: "ocupan todo el ancho sin
		// alineación") — the one case where the box is *not* sized to
		// its content, since there is no room for alignment either way
		// below the compact breakpoint.
		return style.Width(width).Render(content)
	}

	// The card's real width is its own content's widest line plus the
	// border/padding overhead — not always the 80% cap (spec: "el ancho
	// de la tarjeta es el de su contenido ... con un máximo del 80%").
	// Content was already word-wrapped to contentWidth above, so it can
	// exceed that only if a single "word" (no break opportunity) is
	// wider — the clamp below still holds that line to the cap
	// regardless.
	lines := strings.Split(content, "\n")
	natural := 0
	for _, line := range lines {
		if w := lipgloss.Width(line); w > natural {
			natural = w
		}
	}
	cardWidth := natural + cardBoxOverhead
	if cardWidth > width {
		cardWidth = width
	}
	if cardWidth < 10 {
		cardWidth = 10
	}
	return style.Width(cardWidth).Render(content)
}

// cardMaxWidth returns how wide one card is allowed to be (spec §6.3's
// "disposición de chat"): 80% of the conversation's width normally, 100% in
// compact mode (< 60 columns), where cards already drop their margin and
// color bar and alignment would only waste space.
func cardMaxWidth(totalWidth int, compact bool) int {
	if compact || totalWidth <= 0 {
		return totalWidth
	}
	w := totalWidth * 8 / 10
	if w < 20 {
		w = 20
	}
	if w > totalWidth {
		w = totalWidth
	}
	return w
}

// renderConversation renders the whole message list in server order,
// separated by a blank line, or an empty-state placeholder when there are
// no messages yet. localRole decides which side of the conversation a
// message's card is aligned to (§6.3): cards whose sender is localRole (the
// role this TUI writes as — orchestrator on host/local, the endpoint's role
// in the observer, executor in join) align right; the other role's cards
// align left. Human-sent messages follow their sender's role, distinguished
// instead by the card's origin label. This is the phase-1 entry point (no
// Markdown, fold, selection or search); app.go's live view goes through
// renderConversationDetailed.
func renderConversation(messages []protocol.Envelope, statuses map[string]string, th theme.Theme, width int, compact bool, localRole protocol.Role) string {
	content, _ := renderConversationDetailed(messages, statuses, conversationParams{Theme: th, Width: width, Compact: compact, LocalRole: localRole})
	return content
}

// conversationParams bundles renderConversationDetailed's phase-2 options.
type conversationParams struct {
	Theme     theme.Theme
	Width     int
	Compact   bool
	LocalRole protocol.Role
	// Selected is the id of the message the conversation's keyboard focus
	// is on, or "" when the conversation does not have focus.
	Selected string
	// Expanded/Collapsed are explicit per-message fold overrides (§6.3):
	// enter toggles a message between them, overriding the >30-line
	// length-based default.
	Expanded  map[string]bool
	Collapsed map[string]bool
	Search    string
	MD        *markdownCache
}

// renderConversationDetailed is renderConversation plus phase-2 content: it
// also returns, for each message in order, the 0-based line offset (within
// the returned content) where that message's card begins — Model uses this
// to keep the selected card in view (§6.3 "Selección") without re-deriving
// line counts from rendered strings elsewhere.
func renderConversationDetailed(messages []protocol.Envelope, statuses map[string]string, p conversationParams) (string, []int) {
	if len(messages) == 0 {
		return "\n  Aún no hay mensajes.", nil
	}
	cardCap := cardMaxWidth(p.Width, p.Compact)
	blocks := make([]string, 0, len(messages))
	offsets := make([]int, len(messages))
	line := 0
	for i, e := range messages {
		status := statuses[e.MessageID]
		if status == "" {
			status = "received"
		}
		alignRight := e.SenderRole == p.LocalRole
		card := renderCardWithParams(e, cardParams{
			Status:         status,
			Theme:          p.Theme,
			Width:          cardCap,
			Compact:        p.Compact,
			AlignRight:     alignRight,
			Selected:       p.Selected != "" && p.Selected == e.MessageID,
			ForceExpanded:  p.Expanded[e.MessageID],
			ForceCollapsed: p.Collapsed[e.MessageID],
			Search:         p.Search,
			MD:             p.MD,
		})
		if !p.Compact && p.Width > 0 {
			// The card is already a rigid, uniform-width rectangle
			// (renderCardWithParams) sized to its own content up to the
			// 80% cap — not necessarily cardCap itself — so its actual
			// width is measured from what it rendered to, not assumed.
			ownWidth := lipgloss.Width(strings.SplitN(card, "\n", 2)[0])
			card = placeCardBlock(card, p.Width, ownWidth, alignRight)
		}
		offsets[i] = line
		line += strings.Count(card, "\n") + 1
		if i < len(messages)-1 {
			line++ // the "\n\n" block separator joined below
		}
		blocks = append(blocks, card)
	}
	return strings.Join(blocks, "\n\n"), offsets
}

// placeCardBlock moves a card (already a uniform cardWidth-wide rectangle —
// see renderCardWithParams) to one side of the full conversation width, by
// prefixing every line with the *same* run of spaces. This is deliberately
// not lipgloss.PlaceHorizontal: that function pads each line of a
// multi-line string independently up to the block's own widest line, which
// only produces a rigid, uniformly-shifted block when every line already
// has equal width — exactly what renderCardWithParams now guarantees, but
// worth keeping this explicit and simple rather than leaning on a general
// helper that does more than needed.
func placeCardBlock(card string, totalWidth, cardWidth int, alignRight bool) string {
	if !alignRight {
		return card
	}
	pad := totalWidth - cardWidth
	if pad <= 0 {
		return card
	}
	prefix := strings.Repeat(" ", pad)
	lines := strings.Split(card, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
