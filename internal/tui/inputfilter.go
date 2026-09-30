package tui

import (
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// escapeSeqRe matches whole terminal escape sequences that can reach a text
// field as plain text when input arrives split or mangled: CSI (ESC [ ... final
// byte, which includes SGR mouse reports ESC [ < b ; x ; y M), OSC (ESC ] ...
// BEL or ST) and two-byte ESC sequences.
var escapeSeqRe = regexp.MustCompile("\x1b(\\[[0-?]*[ -/]*[@-~]|\\][^\x07\x1b]*(\x07|\x1b\\\\)|[@-_])")

// sanitizeInput removes what must never be inserted into a text field:
// escape sequences as a whole, then every remaining C0 control character
// (except newline and tab), DEL and the C1 range. keepCR leaves carriage
// returns alone (a key press may legitimately carry one); otherwise CRLF and
// lone CR become a newline, so pasted Windows text keeps its line breaks.
func sanitizeInput(s string, keepCR bool) string {
	if s == "" {
		return s
	}
	if !keepCR {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
	}
	if strings.ContainsRune(s, 0x1b) {
		s = escapeSeqRe.ReplaceAllString(s, "")
	}
	clean := strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r' && keepCR:
			return r
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			return -1
		}
		return r
	}, s)
	return clean
}

// filterInput applies sanitizeInput to typed and pasted text before any
// field sees it. It returns nil when nothing printable is left of a key
// press, so the message is dropped instead of reaching the composer as an
// empty key.
func filterInput(msg tea.Msg) tea.Msg {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		if m.Text == "" {
			return msg
		}
		clean := sanitizeInput(m.Text, true)
		if clean == m.Text {
			return msg
		}
		if clean == "" {
			return nil
		}
		m.Text = clean
		return m
	case tea.PasteMsg:
		clean := sanitizeInput(m.Content, false)
		if clean == m.Content {
			return msg
		}
		return tea.PasteMsg{Content: clean}
	}
	return msg
}
