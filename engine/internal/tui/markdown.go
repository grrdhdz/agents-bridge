// markdown.go renders a message body as Markdown with syntax-highlighted
// code (spec §6.3: "cuerpo en Markdown con glamour ... y resaltado de
// código, con el estilo del tema"), cached by (message_id, width, theme) so
// re-rendering the conversation (e.g. after a status change or a resize)
// never re-runs glamour for a message whose rendered form cannot have
// changed. NO_COLOR degrades to glamour's ASCII style, which drops color and
// keeps only structural characters.
package tui

import (
	"fmt"
	"image/color"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

// colorHex renders any color.Color as "#rrggbb", for handing to glamour's
// ansi.StyleConfig (which stores colors as strings, parsed via
// lipgloss.Color — hex strings work directly there).
func colorHex(c interface{ RGBA() (r, g, b, a uint32) }) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

// mdKey is the cache key: a message's rendered Markdown depends only on its
// own id (bodies are immutable once received), the width it wraps to, the
// theme (mode + NO_COLOR) it renders under, and its sender's role — which
// bubble background (Theme.BubbleBackground) the body is painted on.
type mdKey struct {
	messageID string
	width     int
	mode      theme.Mode
	noColor   bool
	role      protocol.Role
}

// markdownCache renders and memoizes Markdown bodies. A nil *markdownCache
// falls back to plain, unstyled word-wrapping (used by phase-1 tests that
// exercise card layout without pulling in glamour).
type markdownCache struct {
	mu      sync.Mutex
	entries map[mdKey]string
}

func newMarkdownCache() *markdownCache {
	return &markdownCache{entries: make(map[mdKey]string)}
}

func (c *markdownCache) render(messageID, body string, th theme.Theme, width int, role protocol.Role) string {
	if width < 4 {
		width = 4
	}
	if c == nil {
		return th.TextStyle().Render(lipgloss.Wrap(body, width, ""))
	}
	key := mdKey{messageID: messageID, width: width, mode: th.Mode, noColor: th.NoColor, role: role}
	c.mu.Lock()
	if cached, ok := c.entries[key]; ok {
		c.mu.Unlock()
		return cached
	}
	c.mu.Unlock()

	rendered := renderMarkdown(body, th, width, role)

	c.mu.Lock()
	c.entries[key] = rendered
	c.mu.Unlock()
	return rendered
}

// glamourStyle picks glamour's bundled style closest to th: its own
// dark/light styles carry a semantic-enough palette for a chat body, and
// the ASCII style is glamour's own NO_COLOR-equivalent degradation (no
// color, plain structural characters for headings/lists/code fences).
// Headings keep glamour's bold/color treatment but drop the literal
// "#"/"##"/... prefix glamour's bundled styles print by default — in a
// narrow chat card that prefix reads as leftover Markdown syntax rather
// than a heading, so it is stripped here instead (spec §6.3: no "##"
// visible).
func glamourStyle(th theme.Theme, role protocol.Role) ansi.StyleConfig {
	var base ansi.StyleConfig
	switch {
	case th.NoColor:
		base = styles.ASCIIStyleConfig
	case th.Mode == theme.ModeLight:
		base = styles.LightStyleConfig
	default:
		base = styles.DarkStyleConfig
	}
	base = stripHeadingPrefixes(base)
	if !th.NoColor {
		base = applyThemeColors(base, th, role)
	}
	return base
}

// applyThemeColors overrides glamour's own bundled colors with this
// theme's own (spec item 1: "el estilo de glamour en claro debe usar
// colores con el mismo criterio ... código en línea, encabezados,
// bloques de código con fondo claro") — glamour's bundled dark/light
// styles were never checked against *this* theme's Surface, so body text
// rendered through them was exactly the kind of low-contrast content a
// real report complained about.
//
// Round 4 also painted every inline/paragraph-level element with a
// per-role message-bubble tint here; round 5 reverted that fill entirely
// (message cards are now distinguished by a line border, not a
// background — conversation.go's cardBoxStyle) so this function no longer
// sets any background outside Code/CodeBlock, which intentionally keep a
// different, fixed background (SurfaceRaised) — a deliberate lift, not a
// tint of the sender's role, so code reads the same regardless of which
// side sent it, and unlike round 4's per-role tint that lift genuinely
// needs no gap-filling here: markdown.go already gives it to every one of
// Code/CodeBlock's own spans directly, and view.go's paintFrameBackground
// (round 5) fills whatever plain text around it does not already color.
func applyThemeColors(cfg ansi.StyleConfig, th theme.Theme, role protocol.Role) ansi.StyleConfig {
	hex := func(c interface{ RGBA() (r, g, b, a uint32) }) *string {
		s := colorHex(c)
		return &s
	}
	textColor := hex(th.Text)
	headingColor := hex(th.Orchestrator)
	codeFg := hex(th.Human)
	codeBg := hex(th.SurfaceRaised)

	cfg.Document.Color = textColor
	cfg.Text.Color = textColor

	for _, h := range []*ansi.StyleBlock{&cfg.H1, &cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
		h.Color = headingColor
	}

	cfg.Code.Color = codeFg
	cfg.Code.BackgroundColor = codeBg
	cfg.CodeBlock.Color = textColor
	cfg.CodeBlock.BackgroundColor = codeBg
	if cfg.CodeBlock.Chroma != nil {
		cfg.CodeBlock.Chroma.Text.Color = textColor
		cfg.CodeBlock.Chroma.Background.BackgroundColor = codeBg
		// Comments default to a low-contrast gray tuned for glamour's own
		// reference background, not this theme's — a real report's
		// explicit ask ("tokens como comentarios cumplan ≥4.5:1"). Muted
		// already clears that against SurfaceRaised in both themes (see
		// TestPaletteContrastMeetsWCAGMinimums).
		muted := hex(th.Muted)
		cfg.CodeBlock.Chroma.Comment.Color = muted
		cfg.CodeBlock.Chroma.CommentPreproc.Color = muted
		ensureChromaContrast(cfg.CodeBlock.Chroma, th)
	}
	return cfg
}

// chromaMinContrast is the WCAG AA floor for normal text every syntax
// color of a code block must reach against the background it is painted on.
const chromaMinContrast = 4.5

// chromaErrorInk is the dark foreground used on glamour's own red Error
// background: the bundled near-white (#F1F1F1) reads 2.8-2.9:1 on it, and
// a mid-tone red cannot be fixed by tinting the foreground of the same hue.
const chromaErrorInk = "#0f172a"

// ensureChromaContrast repairs every chroma foreground that falls below
// chromaMinContrast against its effective background (its own
// BackgroundColor if it has one, else the theme's SurfaceRaised). Glamour's
// bundled palettes were tuned for its own reference background, so most
// light-mode tokens (and a few dark-mode ones) miss the floor. A failing
// color keeps its hue and is only shifted toward black (light theme) or
// white (dark theme) by the smallest step that clears the floor, so tokens
// stay distinguishable from each other; colors that already pass are
// untouched.
func ensureChromaContrast(ch *ansi.Chroma, th theme.Theme) {
	v := reflect.ValueOf(ch).Elem()
	for i := 0; i < v.NumField(); i++ {
		prim, ok := v.Field(i).Addr().Interface().(*ansi.StylePrimitive)
		if !ok || prim.Color == nil || v.Type().Field(i).Name == "Background" {
			continue
		}
		bg := color.Color(th.SurfaceRaised)
		if prim.BackgroundColor != nil {
			bg = lipgloss.Color(*prim.BackgroundColor)
		}
		fg := lipgloss.Color(*prim.Color)
		if contrastRatio(fg, bg) >= chromaMinContrast {
			continue
		}
		var fixed string
		if prim.BackgroundColor != nil {
			fixed = chromaErrorInk
		} else {
			fixed = colorHex(shiftForContrast(fg, bg, th.Mode == theme.ModeDark))
		}
		prim.Color = &fixed
	}
}

// shiftForContrast mixes fg toward white (lighten) or black by the
// smallest 1% step whose contrast against bg reaches chromaMinContrast.
func shiftForContrast(fg, bg color.Color, lighten bool) color.Color {
	r, g, b, _ := fg.RGBA()
	target := 0.0
	if lighten {
		target = 255
	}
	for step := 1; step <= 100; step++ {
		t := float64(step) / 100
		mix := func(c uint32) uint8 {
			v := float64(c>>8)*(1-t) + target*t
			return uint8(math.Round(v))
		}
		out := color.RGBA{R: mix(r), G: mix(g), B: mix(b), A: 0xff}
		if contrastRatio(out, bg) >= chromaMinContrast {
			return out
		}
	}
	return color.RGBA{R: uint8(target), G: uint8(target), B: uint8(target), A: 0xff}
}

func srgbLinear(c uint32) float64 {
	v := float64(c>>8) / 255.0
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func contrastRatio(a, b color.Color) float64 {
	lum := func(c color.Color) float64 {
		r, g, bl, _ := c.RGBA()
		return 0.2126*srgbLinear(r) + 0.7152*srgbLinear(g) + 0.0722*srgbLinear(bl)
	}
	la, lb := lum(a), lum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// stripHeadingPrefixes returns a copy of style with every heading level's
// (H1–H6) Prefix cleared, keeping every other attribute (bold, color,
// block spacing) untouched.
func stripHeadingPrefixes(style ansi.StyleConfig) ansi.StyleConfig {
	style.H1.Prefix = ""
	style.H2.Prefix = ""
	style.H3.Prefix = ""
	style.H4.Prefix = ""
	style.H5.Prefix = ""
	style.H6.Prefix = ""
	return style
}

// renderMarkdown runs one message body through glamour. Any failure (glamour
// is defensive about malformed input, but a renderer can still fail to
// construct) falls back to the plain word-wrapped body rather than losing
// the message.
func renderMarkdown(body string, th theme.Theme, width int, role protocol.Role) string {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(glamourStyle(th, role)),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return th.TextStyle().Render(lipgloss.Wrap(body, width, ""))
	}
	out, err := renderer.Render(body)
	if err != nil {
		return th.TextStyle().Render(lipgloss.Wrap(body, width, ""))
	}
	out = strings.TrimRight(out, "\n")
	if !th.NoColor {
		out = paintCodeLinesSurfaceRaised(out, body, th.SurfaceRaised)
	}
	return out
}

// fenceCodeLines extracts the trimmed text of every line inside every
// fenced (```) code block in body — the raw source glamour will hand to
// chroma for syntax highlighting.
func fenceCodeLines(body string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			if t := strings.TrimSpace(line); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// paintCodeLinesSurfaceRaised is round 5's fix for a real gap found while
// implementing "código en línea y bloques de código conservan su fondo
// SurfaceRaised sin huecos": glamour's chroma-based code-block highlighter
// (charm.land/glamour/v2/ansi's CodeBlockElement, chromaFormatter =
// "terminal256") always calls chroma's own clearBackground() before
// formatting a single token — an upstream chroma design choice that
// strips every token's background unconditionally, so cfg.CodeBlock's own
// BackgroundColor (applyThemeColors, above) never reaches the actual
// highlighted glyphs, only the code fence's surrounding margin. Since
// there is no glamour/chroma option to keep per-token backgrounds, this
// recognizes a rendered line as "part of a fenced code block" by matching
// its own (ANSI-stripped, trimmed) text against the fence's known source
// lines — robust because glamour does not reflow/word-wrap fenced code —
// and gap-fills that whole line with SurfaceRaised via the same
// centralized SGR-aware injector view.go's paintFrameBackground uses,
// just without padding to a fixed width (the card/screen-level passes
// still do that afterward).
func paintCodeLinesSurfaceRaised(rendered, body string, codeBg color.Color) string {
	codeLines := fenceCodeLines(body)
	if len(codeLines) == 0 {
		return rendered
	}
	want := make(map[string]bool, len(codeLines))
	for _, l := range codeLines {
		want[l] = true
	}
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		if want[strings.TrimSpace(xansi.Strip(line))] {
			width := lipgloss.Width(line)
			lines[i] = normalizeLineBackground(line, 0, width, codeBg, nil)
		}
	}
	return strings.Join(lines, "\n")
}

// foldPreviewLines is how many lines of a folded (>30 rendered lines, spec
// §6.3) message stay visible above the "▸ N líneas más" indicator.
const (
	foldThreshold    = 30
	foldPreviewLines = 3
)

// applyFold truncates rendered (already word-wrapped Markdown or plain
// text) to its fold preview plus a trailer when it is over foldThreshold
// lines and not explicitly expanded, and leaves it untouched otherwise.
// expanded/collapsed distinguish an explicit user override (enter toggles
// it) from the length-based default. The trailer line lives inside the
// bubble too, so it carries the same background (bg) every other span in
// the card does.
func applyFold(rendered string, th theme.Theme, bg lipgloss.Style, forceExpanded, forceCollapsed bool) string {
	lines := strings.Split(rendered, "\n")
	folded := len(lines) > foldThreshold
	if forceExpanded {
		folded = false
	} else if forceCollapsed {
		folded = true
	}
	if !folded {
		return rendered
	}
	hidden := len(lines) - foldPreviewLines
	if hidden < 1 {
		return rendered
	}
	preview := strings.Join(lines[:foldPreviewLines], "\n")
	trailer := bg.Render(foldIndicator(hidden))
	return preview + "\n" + trailer
}

func foldIndicator(hidden int) string {
	if hidden == 1 {
		return "▸ 1 línea más (enter para ver todo)"
	}
	return "▸ " + strconv.Itoa(hidden) + " líneas más (enter para ver todo)"
}
