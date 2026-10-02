package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

// TestViewSetsBackgroundColorToThemeSurface covers spec item 1's first
// mechanism: the whole terminal's background is set to the theme's own
// Surface, in both themes, for the lifetime of the program (bubbletea's
// own renderer resets it on shutdown — see view.go's comment) — and
// NO_COLOR must never set it at all (spec §8's "degradar a atributos y
// símbolos"). Round 5's real report showed this mechanism alone is not
// enough (at least one real terminal ignores it outright) — see
// TestEveryVisibleCellHasAnExplicitBackgroundColor below for the
// mechanism that actually covers that case — but it still helps in
// terminals that do honor it, so it stays.
func TestViewSetsBackgroundColorToThemeSurface(t *testing.T) {
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		th := theme.New(mode, false, nil)
		model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, Theme: th})
		updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		m := updated.(*Model)
		got := m.View().BackgroundColor
		if got != th.Surface {
			t.Fatalf("%s: View.BackgroundColor = %#v, want the theme's own Surface %#v", mode, got, th.Surface)
		}
	}

	noColorTheme := theme.New(theme.ModeDark, true, nil)
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, Theme: noColorTheme})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m := updated.(*Model)
	if bg := m.View().BackgroundColor; bg != nil {
		t.Fatalf("NO_COLOR must never set View.BackgroundColor, got %#v", bg)
	}
}

// backgroundSGR matches an SGR "set background" sequence in its 24-bit
// ("48;2;r;g;b") form, so a test can tell "this text has some explicit
// background" without a full ANSI parser.
var backgroundSGR = regexp.MustCompile(`\x1b\[[0-9;]*48;2;(\d+);(\d+);(\d+)m`)

// hasExplicitBackground reports whether s contains any SGR background-color
// sequence.
func hasExplicitBackground(s string) bool {
	return backgroundSGR.MatchString(s)
}

// backgroundSGRSubstring returns the "48;2;r;g;b" fragment lipgloss emits
// when it sets c as a background — a substring search for this is a
// simple, deterministic way to check whether a rendered line's explicit
// background reaches a particular point, without a full ANSI parser.
func backgroundSGRSubstring(c interface{ RGBA() (r, g, b, a uint32) }) string {
	r, g, b, _ := c.RGBA()
	return "48;2;" + strconv.Itoa(int(r>>8)) + ";" + strconv.Itoa(int(g>>8)) + ";" + strconv.Itoa(int(b>>8))
}

// backgroundSGRSubstringFG is backgroundSGRSubstring's foreground
// equivalent ("38;2;r;g;b"), for colors set as a foreground — every card
// border color (round 5) and the selection accent are always set this way,
// never as a background.
func backgroundSGRSubstringFG(c interface{ RGBA() (r, g, b, a uint32) }) string {
	r, g, b, _ := c.RGBA()
	return "38;2;" + strconv.Itoa(int(r>>8)) + ";" + strconv.Itoa(int(g>>8)) + ";" + strconv.Itoa(int(b>>8))
}

// effectiveBackgroundCoverage decodes one already-rendered screen line
// (SGR codes and plain text only — this package's renderer never emits
// cursor-movement sequences) into a []bool of length width, true wherever
// an explicit 24-bit background is active at that visible column. It is
// deliberately re-derived here rather than calling into
// backgroundpaint.go's own normalizeLineBackground, so this test exercises
// the actual rendered bytes View() produced, not the production function's
// own internal bookkeeping.
func effectiveBackgroundCoverage(width int, line string) []bool {
	covered := make([]bool, width)
	active := false
	col := 0
	pos := 0
	mark := func(n int) {
		for i := 0; i < n; i++ {
			if col < width {
				covered[col] = active
			}
			col++
		}
	}
	for _, m := range ansiSGRRe.FindAllStringIndex(line, -1) {
		start, end := m[0], m[1]
		if start > pos {
			mark(len([]rune(line[pos:start])))
		}
		code := line[start:end]
		params := strings.Split(code[2:len(code)-1], ";")
		i := 0
		for i < len(params) {
			switch params[i] {
			case "", "0", "49":
				active = false
				i++
			case "38":
				i += 1 + skipColorParams(params, i+1)
			case "48":
				active = true
				i += 1 + skipColorParams(params, i+1)
			default:
				i++
			}
		}
		pos = end
	}
	if pos < len(line) {
		mark(len([]rune(line[pos:])))
	}
	return covered
}

// assertFullBackgroundCoverage fails t if any visible cell in content (up
// to width columns, and however many lines it has) lacks an explicit
// background — the deterministic check round 5 asked for: "no hay ninguna
// celda visible cuyo fondo efectivo sea el por defecto de la terminal".
func assertFullBackgroundCoverage(t *testing.T, label, content string, width int) {
	t.Helper()
	for y, line := range strings.Split(content, "\n") {
		for x, ok := range effectiveBackgroundCoverage(width, line) {
			if !ok {
				t.Fatalf("%s: cell (row %d, col %d) has no explicit background — it would show the real terminal's own default there: %q", label, y, x, line)
			}
		}
	}
}

// TestEveryVisibleCellHasAnExplicitBackgroundColor is round 5's central
// regression test for the real report: a person running --theme dark
// *inside the app's own terminal panel* (an embedded xterm.js terminal
// that does not honor tea.View.BackgroundColor at all) saw a white screen
// with dark patches and white gaps, because round 4 relied solely on that
// mechanism plus per-role tinted spans. This checks the actual rendered
// frame, at every width/theme combination the golden tests also cover,
// with no floating window open.
func TestEveryVisibleCellHasAnExplicitBackgroundColor(t *testing.T) {
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		for _, width := range []int{60, 80, 120} {
			t.Run(fmt.Sprintf("%s_w%d", mode, width), func(t *testing.T) {
				th := theme.New(mode, false, nil)
				model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), Theme: th})
				updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: 30})
				m := updated.(*Model)
				assertFullBackgroundCoverage(t, fmt.Sprintf("%s w%d", mode, width), m.View().Content, width)
			})
		}
	}
}

// TestEveryVisibleCellHasExplicitBackgroundWithOverlayOpen covers the same
// requirement with a floating window (the command palette) open: its own
// rectangle must be fully covered (SurfaceRaised, via view.go's zones),
// and so must the backdrop still visible around it.
func TestEveryVisibleCellHasExplicitBackgroundWithOverlayOpen(t *testing.T) {
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		th := theme.New(mode, false, nil)
		model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), Theme: th})
		updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
		m := updated.(*Model)
		m.openPalette()
		assertFullBackgroundCoverage(t, string(mode)+" with palette open", m.View().Content, 80)
	}
}

// TestNoColorNeverPaintsAnExplicitBackground covers spec §8: NO_COLOR must
// never emit a background SGR anywhere in the frame, not even the
// round-5 gap-filling pass.
func TestNoColorNeverPaintsAnExplicitBackground(t *testing.T) {
	th := theme.New(theme.ModeDark, true, nil)
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), Theme: th})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m := updated.(*Model)
	if content := m.View().Content; hasExplicitBackground(content) {
		t.Fatalf("NO_COLOR render should carry no explicit background anywhere, got a match in %q", content)
	}
}

// TestCardRendersWithNoBackgroundOfItsOwnExceptCode covers round 5's
// reversal of round 4's tinted-fill bubble ("revierte el relleno de
// color"): a card with plain text carries no explicit background at all —
// it is distinguished only by its border — while a card containing a code
// block still paints SurfaceRaised behind the code (spec: "código en
// línea y bloques de código conservan su fondo SurfaceRaised").
func TestCardRendersWithNoBackgroundOfItsOwnExceptCode(t *testing.T) {
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		t.Run(string(mode), func(t *testing.T) {
			th := theme.New(mode, false, nil)
			e := newTestEnvelope(t, "a", protocol.RoleOrchestrator, "hola mundo", protocol.SourceHumanOperator, time.Now())
			card := renderCardWithParams(e, cardParams{Status: "received", Theme: th, Width: 80, Compact: false, AlignRight: true, MD: newMarkdownCache()})
			if hasExplicitBackground(card) {
				t.Fatalf("%s: a plain-text card should carry no explicit background of its own, got %q", mode, card)
			}
		})
	}
}

func TestCardWithCodeStillPaintsSurfaceRaisedBehindIt(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	e := newTestEnvelope(t, "a", protocol.RoleOrchestrator, "texto\n\n```go\nfunc main() {}\n```\n", protocol.SourceHumanOperator, time.Now())
	card := renderCardWithParams(e, cardParams{Status: "received", Theme: th, Width: 80, Compact: false, AlignRight: true, MD: newMarkdownCache()})
	want := backgroundSGRSubstring(th.SurfaceRaised)
	if !strings.Contains(card, want) {
		t.Fatalf("a card with a code block should still paint SurfaceRaised behind the code, got %q", card)
	}
}

// TestCardBorderUsesRoleOrHumanColorAndSelectedUsesAccent covers round 5's
// message-card design: a role-colored border normally, the Human color
// instead when the message's origin is human (regardless of its role),
// and a double-line border in the Info accent color when selected — a
// structural shape change any NO_COLOR terminal shows too, not just a
// color swap.
func TestCardBorderUsesRoleOrHumanColorAndSelectedUsesAccent(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	agentMsg := newTestEnvelope(t, "a", protocol.RoleExecutor, "hola", protocol.SourceAgentControl, time.Now())
	card := renderCardWithParams(agentMsg, cardParams{Status: "received", Theme: th, Width: 80, Compact: false, AlignRight: true, MD: newMarkdownCache()})
	if !strings.Contains(card, backgroundSGRSubstringFG(th.Executor)) {
		t.Fatalf("an agent-authored executor card's border should use the Executor color, got %q", card)
	}

	humanMsg := newTestEnvelope(t, "b", protocol.RoleExecutor, "hola", protocol.SourceHumanOperator, time.Now())
	humanCard := renderCardWithParams(humanMsg, cardParams{Status: "received", Theme: th, Width: 80, Compact: false, AlignRight: true, MD: newMarkdownCache()})
	if !strings.Contains(humanCard, backgroundSGRSubstringFG(th.Human)) {
		t.Fatalf("a human-authored card's border should use the Human color even though its role is Executor, got %q", humanCard)
	}

	selected := renderCardWithParams(agentMsg, cardParams{Status: "received", Theme: th, Width: 80, Compact: false, AlignRight: true, Selected: true, MD: newMarkdownCache()})
	if !strings.Contains(selected, backgroundSGRSubstringFG(th.Info)) {
		t.Fatalf("a selected card's border should use the Info accent color, got %q", selected)
	}
	if !strings.ContainsRune(selected, '╔') {
		t.Fatalf("a selected card should use a double-line border, got %q", selected)
	}
	if strings.ContainsRune(card, '╔') {
		t.Fatalf("an unselected card should not use the double-line border, got %q", card)
	}
}
