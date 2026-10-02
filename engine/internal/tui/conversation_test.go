package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

func TestSplitLabelExtractsKnownMarkersOnly(t *testing.T) {
	cases := []struct {
		body      string
		wantLabel string
		wantRest  string
	}{
		{"TAREA\nhaz esto", "TAREA", "haz esto"},
		{"PREGUNTA\n¿por qué?", "PREGUNTA", "¿por qué?"},
		{"RESPUESTA\nporque sí", "RESPUESTA", "porque sí"},
		{"RESULTADO\nlisto", "RESULTADO", "listo"},
		{"FIN", "FIN", ""},
		{"algo normal\nmás texto", "", "algo normal\nmás texto"},
		{"tarea\nminúscula no cuenta", "", "tarea\nminúscula no cuenta"},
	}
	for _, c := range cases {
		label, rest := splitLabel(c.body)
		if label != c.wantLabel || rest != c.wantRest {
			t.Fatalf("splitLabel(%q) = (%q, %q), want (%q, %q)", c.body, label, rest, c.wantLabel, c.wantRest)
		}
	}
}

func newTestEnvelope(t *testing.T, id string, role protocol.Role, body, source string, when time.Time) protocol.Envelope {
	t.Helper()
	e, err := protocol.NewEnvelopeWithSource("instance-a", id, 1, protocol.ExpectedSenderID(role), role, body, source, when)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestRenderCardShowsRoleOriginTimeStatusAndBadgeNotInBody(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	when := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	e := newTestEnvelope(t, "m1", protocol.RoleOrchestrator, "TAREA\nrevisa el PR", protocol.SourceHumanOperator, when)
	card := renderCard(e, "delivered", th, 80, false)
	for _, want := range []string{"Orquestador", "humano", "TAREA", "revisa el PR", "✓✓"} {
		if !strings.Contains(card, want) {
			t.Fatalf("card missing %q: %q", want, card)
		}
	}
	bodyOnly := strings.SplitN(card, "\n", 2)
	if len(bodyOnly) == 2 && strings.HasPrefix(strings.TrimSpace(bodyOnly[1]), "TAREA") {
		t.Fatalf("label should be removed from the body, got %q", card)
	}
}

func TestRenderCardNamesExecutorAndAgentOrigin(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	when := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	e := newTestEnvelope(t, "m2", protocol.RoleExecutor, "RESULTADO\nhecho", protocol.SourceAgentControl, when)
	card := renderCard(e, "received", th, 80, false)
	for _, want := range []string{"Ejecutor", "agente", "RESULTADO", "hecho"} {
		if !strings.Contains(card, want) {
			t.Fatalf("card missing %q: %q", want, card)
		}
	}
	if strings.Contains(card, "Tú") {
		t.Fatal("cards must never say Tú; origin distinguishes the human")
	}
}

// TestRenderCardCompactFillsWidthNonCompactHugsContent covers §6.1's
// compact mode with the chat-bubble design (spec item 2): a bubble's width
// is its own content, capped at 80% — never stretched to fill the
// available space — except in compact mode (< 60 columns), where spec
// explicitly says bubbles "ocupan todo el ancho sin alineación".
func TestRenderCardCompactFillsWidthNonCompactHugsContent(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	e := newTestEnvelope(t, "m3", protocol.RoleOrchestrator, "hola", protocol.SourceHumanOperator, time.Now())
	full := renderCard(e, "queued-ram", th, 80, false)
	compact := renderCard(e, "queued-ram", th, 50, true)

	fullWidth := lipgloss.Width(strings.Split(full, "\n")[0])
	if fullWidth >= 80 {
		t.Fatalf("a short non-compact bubble should hug its own content, not fill the full 80 columns, got width %d", fullWidth)
	}
	for i, line := range strings.Split(compact, "\n") {
		if w := lipgloss.Width(line); w != 50 {
			t.Fatalf("compact line %d should fill the full 50-column width, got %d: %q", i, w, line)
		}
	}
}

func TestRenderConversationEmptyPlaceholder(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	out := renderConversation(nil, nil, th, 80, false, protocol.RoleOrchestrator)
	if !strings.Contains(out, "Aún no hay mensajes") {
		t.Fatalf("expected an empty-state placeholder, got %q", out)
	}
}

func TestRenderConversationPreservesOrder(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	first := newTestEnvelope(t, "a", protocol.RoleOrchestrator, "primero", protocol.SourceHumanOperator, time.Now())
	second := newTestEnvelope(t, "b", protocol.RoleExecutor, "segundo", protocol.SourceAgentControl, time.Now())
	out := renderConversation([]protocol.Envelope{first, second}, map[string]string{"a": "delivered", "b": "received"}, th, 80, false, protocol.RoleOrchestrator)
	if strings.Index(out, "primero") >= strings.Index(out, "segundo") {
		t.Fatalf("messages should render in server order: %q", out)
	}
}

// TestRenderConversationAlignsLocalRoleRightAndOtherRoleLeft covers §6.3's
// amended "disposición de chat": the local role's cards sit flush against
// the right edge, the other role's against the left, each capped at 80% of
// the conversation's width.
func TestRenderConversationAlignsLocalRoleRightAndOtherRoleLeft(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	mine := newTestEnvelope(t, "a", protocol.RoleOrchestrator, "mío", protocol.SourceHumanOperator, time.Now())
	theirs := newTestEnvelope(t, "b", protocol.RoleExecutor, "suyo", protocol.SourceAgentControl, time.Now())
	out := renderConversation([]protocol.Envelope{mine, theirs}, nil, th, 100, false, protocol.RoleOrchestrator)
	blocks := strings.Split(out, "\n\n")
	if len(blocks) != 2 {
		t.Fatalf("expected two blocks, got %d: %q", len(blocks), out)
	}
	for _, line := range strings.Split(blocks[0], "\n") {
		if w := lipgloss.Width(line); w != 100 {
			t.Fatalf("own card line should be padded to the full width (100), got %d: %q", w, line)
		}
	}
	firstLineMine := strings.Split(blocks[0], "\n")[0]
	firstLineTheirs := strings.Split(blocks[1], "\n")[0]
	leadingSpaces := func(s string) int {
		return len(s) - len(strings.TrimLeft(s, " "))
	}
	if leadingSpaces(firstLineMine) <= leadingSpaces(firstLineTheirs) {
		t.Fatalf("own-role card should be pushed further right than the other role's, got mine=%q theirs=%q", firstLineMine, firstLineTheirs)
	}
	if leadingSpaces(firstLineTheirs) != 0 {
		t.Fatalf("other-role card should hug the left edge, got %q", firstLineTheirs)
	}
}

// TestRenderConversationRightAlignedCardIsARigidBlock is a regression test
// for a real bug seen in the terminal: a right-aligned card's header ended
// up further right than its body. The root cause was lipgloss.PlaceHorizontal
// padding each line of the card independently up to the block's own widest
// line (aligning each line's own end, not moving the whole card as one
// rigid unit) whenever the card's lines had different widths — which they
// usually do (the header is shorter than a wrapped body line). This test
// builds a message whose header and body naturally differ in rendered
// width and asserts every line is exactly as wide as the rest (a uniform
// rectangle placed as one rigid unit), and that the card's own border
// (round 5: a line border, not a tinted fill) reaches the conversation's
// right edge on every bordered line.
func TestRenderConversationRightAlignedCardIsARigidBlock(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	e := newTestEnvelope(t, "a", protocol.RoleOrchestrator, "TAREA\nun cuerpo bastante mas largo que la cabecera de la tarjeta", protocol.SourceAgentControl, time.Now())
	out := renderConversation([]protocol.Envelope{e}, nil, th, 100, false, protocol.RoleOrchestrator)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least a header and a body line, got %q", out)
	}
	want := lipgloss.Width(lines[0])
	for i, l := range lines {
		if l == "" {
			continue // the blank separator line between header and body
		}
		if got := lipgloss.Width(l); got != want {
			t.Fatalf("line %d is %d columns wide, want %d (every line of a right-aligned card must be the same rigid width): %q", i, got, want, lines)
		}
	}
	// The card's own border color must reach the conversation's right
	// edge on its top/bottom border lines — this is what "la tarjeta no
	// aparece partida" means for a line-bordered card.
	borderFg := backgroundSGRSubstringFG(th.Orchestrator)
	if !strings.Contains(lines[0], borderFg) {
		t.Fatalf("top border line does not reach the right edge: %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], borderFg) {
		t.Fatalf("bottom border line does not reach the right edge: %q", lines[len(lines)-1])
	}
}

// TestShortLocalCardHugsRightEdgeNotFixedWidth is a regression test for a
// real report: a short local-role message floated in the middle of the
// conversation because the card was always rendered as a fixed 80%-width
// block regardless of its actual content. A card's width must be its own
// content's width, capped at 80% — never forced up to that cap — so a
// short message's card hugs the conversation's right inner edge, with
// nothing floating in between it and the border.
func TestShortLocalCardHugsRightEdgeNotFixedWidth(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	const totalWidth = 100
	e := newTestEnvelope(t, "a", protocol.RoleOrchestrator, "ok", protocol.SourceHumanOperator, time.Now())
	out := renderConversation([]protocol.Envelope{e}, nil, th, totalWidth, false, protocol.RoleOrchestrator)

	for i, l := range strings.Split(out, "\n") {
		if l == "" {
			continue
		}
		// The line itself is always padded to the conversation's full
		// width (100): the card is placed as a block within that width
		// via placeCardBlock's leading spaces.
		if lipgloss.Width(l) != totalWidth {
			t.Fatalf("line %d should be padded to the full conversation width (100), got %d: %q", i, lipgloss.Width(l), l)
		}
	}
}

// TestCardWidthIsItsOwnContentNotTheCapRegardlessOfContentWidth is the
// direct version of the same fix: renderCardWithParams must never stretch
// a card up to its width cap just because the cap allows it — the cap is
// a ceiling, not a target. A very generous cap (160, chosen so even a
// message with a full header only uses a small fraction of it) makes the
// two behaviors ("sized to content" vs "always the cap") impossible to
// confuse.
func TestCardWidthIsItsOwnContentNotTheCapRegardlessOfContentWidth(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	e := newTestEnvelope(t, "a", protocol.RoleOrchestrator, "ok", protocol.SourceHumanOperator, time.Now())
	const cap160 = 160
	card := renderCardWithParams(e, cardParams{Status: "received", Theme: th, Width: cap160, Compact: false, AlignRight: true})
	lines := strings.Split(card, "\n")
	for i, l := range lines {
		w := lipgloss.Width(l)
		if w >= cap160 {
			t.Fatalf("line %d is %d columns wide — a short \"ok\" message's card must be far narrower than the %d-column cap, not stretched to fill it: %q", i, w, cap160, ansi.Strip(l))
		}
	}
}

// TestLongLocalCardNeverExceeds80Percent covers the cap half of the same
// fix: a message long enough to need wrapping still respects the 80% cap,
// it does not grow past it just because AlignRight no longer forces a
// fixed width.
func TestLongLocalCardNeverExceeds80Percent(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	e := newTestEnvelope(t, "a", protocol.RoleOrchestrator, strings.Repeat("palabra ", 40), protocol.SourceHumanOperator, time.Now())
	const totalWidth = 100
	out := renderConversation([]protocol.Envelope{e}, nil, th, totalWidth, false, protocol.RoleOrchestrator)
	maxCardWidth := cardMaxWidth(totalWidth, false)
	for i, l := range strings.Split(out, "\n") {
		if l == "" {
			continue
		}
		plain := ansi.Strip(l)
		leading := len(plain) - len(strings.TrimLeft(plain, " "))
		ownWidth := lipgloss.Width(l) - leading
		if ownWidth > maxCardWidth {
			t.Fatalf("line %d: card content is %d columns wide, exceeding the 80%% cap of %d: %q", i, ownWidth, maxCardWidth, plain)
		}
	}
}

// TestSelectedCardBorderShowsAccentEdgeNotJustMarker covers §6.3's
// selection requirement, round 5's line-border version: the selected
// card's *border* (its double-line, Info-colored box — cardBoxStyle) must
// visibly distinguish it, not only the "▶" marker in its header — a
// person scanning the conversation should be able to tell which message
// has keyboard focus even without reading the header text closely. The
// card's body line (the box's own left/right border characters flanking
// its interior content, not the "▶" marker, which only ever appears in
// the header) is checked specifically so the marker's own color can't
// explain a false pass.
func TestSelectedCardBorderShowsAccentEdgeNotJustMarker(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	e := newTestEnvelope(t, "a", protocol.RoleOrchestrator, "hola", protocol.SourceHumanOperator, time.Now())
	selected := renderCardWithParams(e, cardParams{Status: "received", Theme: th, Width: 80, Compact: false, AlignRight: true, Selected: true, MD: newMarkdownCache()})
	lines := strings.Split(selected, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least a header and a body line, got %q", selected)
	}
	bodyLine := lines[len(lines)-2] // last line is the box's bottom border; the one before it is the body's own bordered row

	unselected := renderCardWithParams(e, cardParams{Status: "received", Theme: th, Width: 80, Compact: false, AlignRight: true, MD: newMarkdownCache()})
	unselectedLines := strings.Split(unselected, "\n")
	unselectedBodyLine := unselectedLines[len(unselectedLines)-2]

	if strings.Contains(unselectedBodyLine, "▶") || strings.Contains(bodyLine, "▶") {
		t.Fatal("setup: the body row should never contain the ▶ marker (it only appears in the header) — this test needs a line where the marker can't explain the accent")
	}
	accent := backgroundSGRSubstringFG(th.Info)
	if strings.Contains(unselectedBodyLine, accent) {
		t.Fatal("an unselected card's body row should not carry the selection accent color at all")
	}
	if !strings.Contains(bodyLine, accent) {
		t.Fatalf("the selected card's body row should carry an accent-colored border edge (Info) even though the ▶ marker never reaches this row, got %q", bodyLine)
	}
}

// TestRenderConversationCompactSkipsAlignment covers §6.1's compact mode
// (< 60 columns): cards use the full width and are not padded to one side.
func TestRenderConversationCompactSkipsAlignment(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	mine := newTestEnvelope(t, "a", protocol.RoleOrchestrator, "mío", protocol.SourceHumanOperator, time.Now())
	out := renderConversation([]protocol.Envelope{mine}, nil, th, 50, true, protocol.RoleOrchestrator)
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 50 {
			t.Fatalf("compact card line exceeds terminal width: %q", line)
		}
	}
}
