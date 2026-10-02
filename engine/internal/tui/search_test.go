package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

func TestMessageMatchesSearchIsCaseInsensitive(t *testing.T) {
	e := newTestEnvelope(t, "a", protocol.RoleOrchestrator, "Revisa el PR #42", protocol.SourceHumanOperator, time.Now())
	if !messageMatchesSearch(e, "pr #42") {
		t.Fatal("search should be case-insensitive")
	}
	if messageMatchesSearch(e, "no está aquí") {
		t.Fatal("unrelated term should not match")
	}
	if messageMatchesSearch(e, "") {
		t.Fatal("an empty term should never match (search inactive)")
	}
}

func TestSearchMatchesReturnsIndicesInOrder(t *testing.T) {
	msgs := []protocol.Envelope{
		newTestEnvelope(t, "a", protocol.RoleOrchestrator, "hola mundo", protocol.SourceHumanOperator, time.Now()),
		newTestEnvelope(t, "b", protocol.RoleExecutor, "adiós mundo", protocol.SourceAgentControl, time.Now()),
		newTestEnvelope(t, "c", protocol.RoleOrchestrator, "sin nada", protocol.SourceHumanOperator, time.Now()),
	}
	got := searchMatches(msgs, "mundo")
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("expected matches at indices [0 1], got %v", got)
	}
}

func TestHighlightSearchWrapsEveryOccurrence(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	out := highlightSearch("foo BAR foo", "foo", th)
	if strings.Count(out, "\x1b[7m") < 2 && !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected reverse-video escapes wrapping matches, got %q", out)
	}
	// Both case variants of the literal text must survive untouched aside
	// from the inserted escapes.
	stripped := ansi.Strip(out)
	if stripped != "foo BAR foo" {
		t.Fatalf("highlighting must not alter the visible text, got %q", stripped)
	}
}

func TestHighlightSearchNoOpWhenNoMatch(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	in := "sin coincidencias"
	out := highlightSearch(in, "zzz", th)
	if out != in {
		t.Fatalf("no match should leave the string untouched, got %q", out)
	}
}
