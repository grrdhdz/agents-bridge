package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

// TestMarkdownCacheRendersOnceAndReusesResult covers spec §6.3/§10.6: a
// cache hit for the same (message_id, width, theme) must not re-render, and
// a change to any one of those three dimensions must produce a fresh render
// (proven here by width, which is easy to force to differ visibly).
func TestMarkdownCacheRendersOnceAndReusesResult(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	cache := newMarkdownCache()

	first := cache.render("m1", "hola **mundo**", th, 40, protocol.RoleOrchestrator)
	second := cache.render("m1", "hola **mundo**", th, 40, protocol.RoleOrchestrator)
	if first != second {
		t.Fatalf("identical (message_id, width, theme) should hit the cache and return the same string, got %q vs %q", first, second)
	}
	if len(cache.entries) != 1 {
		t.Fatalf("expected exactly one cache entry after two identical renders, got %d", len(cache.entries))
	}

	wider := cache.render("m1", "hola **mundo**", th, 80, protocol.RoleOrchestrator)
	if len(cache.entries) != 2 {
		t.Fatalf("a different width should be a distinct cache entry, got %d entries", len(cache.entries))
	}
	if wider == first {
		t.Fatal("a wider render should not be byte-identical to the narrower one for a wrapped paragraph this short is fine, but the cache entries themselves must be distinct")
	}
}

// TestMarkdownCacheDistinguishesTheme covers the theme dimension of the
// cache key: dark and light must not collide even for the same message and
// width, since glamour's style output differs (color codes).
func TestMarkdownCacheDistinguishesTheme(t *testing.T) {
	cache := newMarkdownCache()
	dark := theme.New(theme.ModeDark, false, nil)
	light := theme.New(theme.ModeLight, false, nil)
	cache.render("m1", "**bold**", dark, 40, protocol.RoleOrchestrator)
	cache.render("m1", "**bold**", light, 40, protocol.RoleOrchestrator)
	if len(cache.entries) != 2 {
		t.Fatalf("dark and light should be distinct cache entries, got %d", len(cache.entries))
	}
}

// TestMarkdownRendersCodeBlockWithFences covers "resaltado de código": a
// fenced code block's content must survive rendering (glamour may restyle
// it, but the source text itself must still be present).
func TestMarkdownRendersCodeBlockWithFences(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	body := "texto\n\n```go\nfunc main() {}\n```\n"
	out := renderMarkdown(body, th, 80, protocol.RoleOrchestrator)
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "func") || !strings.Contains(plain, "main") {
		t.Fatalf("rendered markdown should still contain the code once ANSI is stripped, got %q", plain)
	}
}

// TestMarkdownDegradesUnderNoColor covers §8/§6.3: NO_COLOR must not emit
// ANSI color escapes.
func TestMarkdownDegradesUnderNoColor(t *testing.T) {
	th := theme.New(theme.ModeDark, true, nil)
	out := renderMarkdown("# Título\n\ntexto **fuerte**", th, 80, protocol.RoleOrchestrator)
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("NO_COLOR render should carry no ANSI escapes, got %q", out)
	}
	if !strings.Contains(out, "fuerte") {
		t.Fatalf("degraded render should still contain the text, got %q", out)
	}
}

// TestMarkdownHeadingsHaveNoLiteralHashPrefix is a regression test: the
// user reported seeing literal "##" before headings in a real terminal.
// glamour's bundled dark/light styles print "# "/"## "/etc. by default;
// this project strips that prefix (stripHeadingPrefixes) while keeping the
// heading's bold/color treatment, in both themes.
func TestMarkdownHeadingsHaveNoLiteralHashPrefix(t *testing.T) {
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		th := theme.New(mode, false, nil)
		out := renderMarkdown("## Título\n\ntexto normal", th, 80, protocol.RoleOrchestrator)
		plain := ansi.Strip(out)
		if strings.Contains(plain, "#") {
			t.Fatalf("%s: heading prefix should be stripped, got %q", mode, plain)
		}
		if !strings.Contains(plain, "Título") {
			t.Fatalf("%s: heading text itself should survive, got %q", mode, plain)
		}
	}
}

// TestApplyFoldTruncatesLongBodiesWithIndicator covers §6.3 "Mensajes
// largos": more than 30 rendered lines collapses to a preview plus a
// "▸ N líneas más" indicator, and an explicit expand override shows
// everything regardless of length.
func TestApplyFoldTruncatesLongBodiesWithIndicator(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "línea")
	}
	body := strings.Join(lines, "\n")

	folded := applyFold(body, th, th.MutedStyle(), false, false)
	if strings.Count(folded, "\n") >= 39 {
		t.Fatalf("a 40-line body should be folded down, got %d newlines", strings.Count(folded, "\n"))
	}
	if !strings.Contains(folded, "líneas más") {
		t.Fatalf("folded body should show the fold indicator, got %q", folded)
	}

	expanded := applyFold(body, th, th.MutedStyle(), true, false)
	if strings.Contains(expanded, "líneas más") {
		t.Fatalf("an explicit expand override should show everything, got %q", expanded)
	}
	if strings.Count(expanded, "\n") != 39 {
		t.Fatalf("expanded body should keep all 40 lines, got %d newlines", strings.Count(expanded, "\n"))
	}
}

// TestApplyFoldLeavesShortBodiesAlone covers the common case: a body under
// the threshold is never folded, even without an override.
func TestApplyFoldLeavesShortBodiesAlone(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	body := "una\ndos\ntres"
	out := applyFold(body, th, th.MutedStyle(), false, false)
	if out != body {
		t.Fatalf("a short body should be unchanged, got %q", out)
	}
}
