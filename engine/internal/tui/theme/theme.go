// Package theme provides the TUI's dark/light palettes, NO_COLOR
// degradation and the small set of symbols the conversation cards and
// status bar use to convey state without relying on color alone (spec
// §8: docs/superpowers/specs/2026-09-27-tui-redesign-design.md).
//
// Every foreground color is chosen to meet a measured WCAG 2 contrast
// ratio against the surface it is actually painted on (see
// TestPaletteContrastMeetsWCAGMinimums, theme_test.go): primary text
// ≥7:1, secondary/muted text and semantic (role/label/status) colors
// ≥4.5:1, active borders ≥3:1, inactive borders ≥2:1 — in both themes,
// not just dark, and against every surface a span can actually render on
// (Surface, SurfaceRaised, and each role's message-bubble tint).
//
// Backgrounds are not painted span-by-span in this package (an earlier
// pass did that and produced visible seams wherever a span's own ANSI
// reset broke the surrounding background). Model.View sets the whole
// terminal's background to Surface for the program's lifetime
// (tea.View.BackgroundColor) as a first line of defense — but a real
// report showed that mechanism does nothing in at least one real terminal
// (an embedded xterm.js panel that ignores it entirely), so view.go's own
// paintFrameBackground pass is the mechanism that actually guarantees
// every visible cell has an explicit background, centrally, over the
// fully composited frame — not here. Explicit Background() only belongs
// here where a *different* surface is a deliberate design choice: code
// blocks (markdown.go), the status bar and floating overlays (whose own
// rectangle paintFrameBackground fills with SurfaceRaised too). Message
// cards (conversation.go) are no longer tinted per role at all (round 5:
// "revierte el relleno de color") — they are distinguished by a
// role-colored line border instead, via BorderStyle/BorderForeground
// below.
package theme

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

// Mode selects which palette a Theme uses.
type Mode string

const (
	ModeDark  Mode = "dark"
	ModeLight Mode = "light"
	ModeAuto  Mode = "auto"
)

// ResolveMode picks the theme mode from an explicit --theme flag value
// (highest priority) or AGENTS_BRIDGE_THEME; "auto" is the default when
// neither is set. An unrecognized value is rejected so a typo in the flag
// or the environment variable is never silently ignored.
func ResolveMode(flagValue, envValue string) (Mode, error) {
	value := strings.TrimSpace(flagValue)
	if value == "" {
		value = strings.TrimSpace(envValue)
	}
	if value == "" {
		return ModeAuto, nil
	}
	switch Mode(value) {
	case ModeDark, ModeLight, ModeAuto:
		return Mode(value), nil
	default:
		return "", fmt.Errorf("tema desconocido %q: usa dark, light o auto", value)
	}
}

// IsNoColor reports whether NO_COLOR is set to a non-empty value, per
// https://no-color.org/.
func IsNoColor(environ []string) bool {
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok && name == "NO_COLOR" {
			return value != ""
		}
	}
	return false
}

// Theme carries the semantic colors and symbols every component renders
// with, resolved once at startup so no component re-detects the terminal.
type Theme struct {
	Mode    Mode
	NoColor bool
	// Auto is true while the theme came from "auto" with no answer yet: it
	// is dark for now and the TUI asks the terminal for its background from
	// inside the program (tea.RequestBackgroundColor), never by reading
	// stdin itself.
	Auto bool

	Orchestrator color.Color
	Executor     color.Color
	Human        color.Color

	Success color.Color
	Info    color.Color
	Warning color.Color
	Danger  color.Color

	BorderActive   color.Color
	BorderInactive color.Color
	Notice         color.Color
	Muted          color.Color
	Text           color.Color

	// Surface is the program's whole-terminal background (set once via
	// View.BackgroundColor — app.go/view.go — not painted per span); every
	// unstyled cell is this color automatically. SurfaceRaised is a
	// somewhat more marked surface, still light in the light theme (never
	// a dark block), used only where a deliberate lift is wanted: the
	// status bar, code blocks and floating overlays.
	Surface       color.Color
	SurfaceRaised color.Color
}

// New builds a Theme for mode. For ModeAuto, detectDark (when given) supplies
// the answer; with nil the theme is dark and Auto stays true — New never
// queries the terminal, because reading stdin from anywhere but Bubble Tea's
// own input reader steals bytes from it (v0.3.3: an abandoned OSC 11 query
// ate pieces of mouse reports in a terminal that never answers). The TUI
// finishes the job from inside the program with tea.RequestBackgroundColor.
func New(mode Mode, noColor bool, detectDark func() bool) Theme {
	resolved := mode
	undecided := false
	if resolved == ModeAuto {
		dark := true
		if detectDark != nil {
			dark = detectDark()
		} else {
			undecided = true
		}
		if dark {
			resolved = ModeDark
		} else {
			resolved = ModeLight
		}
	}
	var t Theme
	if resolved == ModeLight {
		t = lightTheme(noColor)
	} else {
		t = darkTheme(noColor)
	}
	t.Auto = undecided
	return t
}

func darkTheme(noColor bool) Theme {
	return Theme{
		Mode:           ModeDark,
		NoColor:        noColor,
		Orchestrator:   lipgloss.Color("#93c5fd"),
		Executor:       lipgloss.Color("#86efac"),
		Human:          lipgloss.Color("#fbbf24"),
		Success:        lipgloss.Color("#4ade80"),
		Info:           lipgloss.Color("#38bdf8"),
		Warning:        lipgloss.Color("#facc15"),
		Danger:         lipgloss.Color("#f87171"),
		BorderActive:   lipgloss.Color("#e2e8f0"),
		BorderInactive: lipgloss.Color("#64748b"),
		Notice:         lipgloss.Color("#c4b5fd"),
		Muted:          lipgloss.Color("#94a3b8"),
		Text:           lipgloss.Color("#e2e8f0"),
		Surface:        lipgloss.Color("#0b1220"),
		SurfaceRaised:  lipgloss.Color("#16233a"),
	}
}

// lightTheme: every color is chosen to clear its required WCAG ratio
// against Surface (see TestPaletteContrastMeetsWCAGMinimums for the exact
// numbers), never the pale #999/#aaa-style grays a real report called out
// specifically. Surface is the command palette's bluish tone, so the whole
// light TUI reads as it instead of white; SurfaceRaised (status bar,
// overlays, code blocks) is a slightly deeper shade of the same blue.
func lightTheme(noColor bool) Theme {
	return Theme{
		Mode:           ModeLight,
		NoColor:        noColor,
		Orchestrator:   lipgloss.Color("#1e40af"),
		Executor:       lipgloss.Color("#166534"),
		Human:          lipgloss.Color("#92400e"),
		Success:        lipgloss.Color("#166534"),
		Info:           lipgloss.Color("#075985"),
		Warning:        lipgloss.Color("#854d0e"),
		Danger:         lipgloss.Color("#b91c1c"),
		BorderActive:   lipgloss.Color("#334155"),
		BorderInactive: lipgloss.Color("#64748b"),
		Notice:         lipgloss.Color("#5b21b6"),
		Muted:          lipgloss.Color("#334155"),
		Text:           lipgloss.Color("#0f172a"),
		Surface:        lipgloss.Color("#dce3ee"),
		SurfaceRaised:  lipgloss.Color("#d0d9e7"),
	}
}

// RoleColor returns the semantic color for role, regardless of NoColor (use
// RoleStyle for a style that already degrades correctly).
func (t Theme) RoleColor(role protocol.Role) color.Color {
	if role == protocol.RoleExecutor {
		return t.Executor
	}
	return t.Orchestrator
}

// RoleStyle returns the style a role's card accent/header uses: colored
// foreground normally, or bold-only text under NO_COLOR (§8: "degradar a
// atributos y símbolos"). It never sets a background of its own — callers
// that need one (a message bubble's tint) add .Background() on top; every
// other caller relies on the ambient terminal background (Surface).
func (t Theme) RoleStyle(role protocol.Role) lipgloss.Style {
	style := lipgloss.NewStyle().Bold(true)
	if t.NoColor {
		return style
	}
	return style.Foreground(t.RoleColor(role))
}

// HumanStyle marks a message whose origin is a human operator rather than
// an agent (§6.3): colored under normal themes, italic-only under NO_COLOR.
func (t Theme) HumanStyle() lipgloss.Style {
	style := lipgloss.NewStyle().Italic(true)
	if t.NoColor {
		return style
	}
	return style.Foreground(t.Human)
}

// BorderStyle returns the style used for a focused/unfocused pane border:
// colored under normal themes, bold-only under NO_COLOR so focus is still
// visible without color. It sets *both* Foreground (any unstyled content
// rendered through it, e.g. a border-only box with no styled child) and
// BorderForeground — lipgloss v2's applyBorder reads only the latter
// (borderTopForegroundKey/etc.) to color the border glyphs themselves, a
// real bug found while building round 5's bordered message cards: every
// bordered box in this package had been rendering with colorless border
// glyphs regardless of focus, because Foreground alone never reaches them
// (see TestBorderStyleColorsTheBorderGlyphsThemselves). No background is
// set here: conversation/composer/sidebar/overlay borders sit on the
// ambient/painted terminal background (view.go's paintFrameBackground),
// not a background of their own.
func (t Theme) BorderStyle(active bool) lipgloss.Style {
	style := lipgloss.NewStyle()
	if active {
		style = style.Bold(true)
	}
	if t.NoColor {
		return style
	}
	c := t.BorderInactive
	if active {
		c = t.BorderActive
	}
	return style.Foreground(c).BorderForeground(c)
}

// StatusSymbol renders a message's delivery status (§6.3): queued/in-flight
// (·), accepted or simply received (✓), delivered/ack-confirmed (✓✓), or
// rejected (✗). It never depends on color, so it is identical under
// NO_COLOR.
func (t Theme) StatusSymbol(status string) string {
	switch status {
	case "queued-ram":
		return "·"
	case "delivered":
		return "✓✓"
	case "rejected":
		return "✗"
	case "accepted", "received":
		return "✓"
	default:
		return "·"
	}
}

// ConnIndicator renders the other role's presence (§6.2): connected (●),
// reconnecting (◌) or disconnected (○).
func (t Theme) ConnIndicator(state string) string {
	switch state {
	case "connected":
		return "●"
	case "reconnecting":
		return "◌"
	default:
		return "○"
	}
}

// LabelStyle returns the style for a message's TAREA/PREGUNTA/... badge
// (§6.3): colored per kind normally, bold-only under NO_COLOR. No
// background of its own — see RoleStyle's doc comment.
func (t Theme) LabelStyle(label string) lipgloss.Style {
	style := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	if t.NoColor {
		// Attributes only: URGENTE reads as a reversed block, PROGRESO
		// recedes like other secondary text.
		switch label {
		case "URGENTE":
			return style.Reverse(true)
		case "PROGRESO":
			return style.Faint(true)
		}
		return style
	}
	c := t.Info
	switch label {
	case "TAREA":
		c = t.Orchestrator
	case "PREGUNTA":
		c = t.Info
	case "RESPUESTA":
		c = t.Executor
	case "RESULTADO":
		c = t.Success
	case "FIN":
		c = t.Warning
	case "URGENTE":
		c = t.Danger
	case "PROGRESO":
		c = t.Muted
	}
	return style.Foreground(c)
}

// TextStyle renders plain primary-text content (message bodies that
// bypassed Markdown, delivery-status symbols).
func (t Theme) TextStyle() lipgloss.Style {
	if t.NoColor {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(t.Text)
}

// MutedStyle is used for secondary text (timestamps, hints).
func (t Theme) MutedStyle() lipgloss.Style {
	style := lipgloss.NewStyle()
	if t.NoColor {
		return style.Faint(true)
	}
	return style.Foreground(t.Muted)
}

// SelectionStyle marks the message the conversation's keyboard focus is on
// (§6.3 "Selección"): colored normally, reverse-video under NO_COLOR so the
// marker still reads without color.
func (t Theme) SelectionStyle() lipgloss.Style {
	style := lipgloss.NewStyle().Bold(true)
	if t.NoColor {
		return style
	}
	return style.Foreground(t.Info)
}

// SearchMatchStyle highlights a search hit within a card's body (§6.3
// "Búsqueda"): an explicit fg/bg swap (Surface-on-Notice) rather than
// Style.Reverse — Reverse only flips whatever fg/bg a span already
// carries, which inside an already-tinted bubble would reverse *that*
// pair back to plain colors instead of standing out. NO_COLOR still uses
// Reverse, exactly what a colorless terminal needs (its own default pair,
// flipped).
func (t Theme) SearchMatchStyle() lipgloss.Style {
	style := lipgloss.NewStyle().Bold(true)
	if t.NoColor {
		return style.Reverse(true)
	}
	return style.Foreground(t.Surface).Background(t.Notice)
}

// StatusBarStyle renders the status bar (§6.2) on SurfaceRaised — a
// deliberately distinct surface (spec: status bar is one of the few
// places an explicit background belongs), still light in the light theme
// (never a dark block). Under NO_COLOR it falls back to reverse video,
// which conveys the same separation without relying on color.
func (t Theme) StatusBarStyle() lipgloss.Style {
	style := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	if t.NoColor {
		return style.Reverse(true)
	}
	return style.Foreground(t.Text).Background(t.SurfaceRaised)
}

// NoticeStyle is used for transient notices/warnings (§6.7).
func (t Theme) NoticeStyle() lipgloss.Style {
	style := lipgloss.NewStyle().Bold(true)
	if t.NoColor {
		return style
	}
	return style.Foreground(t.Notice)
}
