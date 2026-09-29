// search.go implements §6.3's "Búsqueda": ctrl+f opens a query, matches are
// highlighted in the conversation and n/N jump between them.
package tui

import (
	"strings"

	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

// messageMatchesSearch reports whether e's body contains term,
// case-insensitively. An empty term matches nothing (search is inactive).
func messageMatchesSearch(e protocol.Envelope, term string) bool {
	if term == "" {
		return false
	}
	return strings.Contains(strings.ToLower(e.Body), strings.ToLower(term))
}

// searchMatches returns the indices of messages matching term, in the same
// order as messages, for n/N to walk through.
func searchMatches(messages []protocol.Envelope, term string) []int {
	if term == "" {
		return nil
	}
	var matches []int
	for i, e := range messages {
		if messageMatchesSearch(e, term) {
			matches = append(matches, i)
		}
	}
	return matches
}

// highlightSearch wraps every case-insensitive occurrence of term in
// rendered with th.SearchMatchStyle. rendered may already carry ANSI escape
// sequences from Markdown rendering; term itself is plain user text (search
// queries are never expected to contain ANSI), so a straightforward
// case-insensitive scan of the rendered string is safe in practice — the
// one edge case (a query that happens to match digits inside a color
// escape) is a cosmetic-only false highlight, not a correctness issue.
func highlightSearch(rendered, term string, th theme.Theme) string {
	if term == "" {
		return rendered
	}
	lowerRendered := strings.ToLower(rendered)
	lowerTerm := strings.ToLower(term)
	if !strings.Contains(lowerRendered, lowerTerm) {
		return rendered
	}
	style := th.SearchMatchStyle()
	var b strings.Builder
	rest := rendered
	lowerRest := lowerRendered
	for {
		idx := strings.Index(lowerRest, lowerTerm)
		if idx < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:idx])
		match := rest[idx : idx+len(term)]
		b.WriteString(style.Render(match))
		rest = rest[idx+len(term):]
		lowerRest = lowerRest[idx+len(term):]
	}
	return b.String()
}
