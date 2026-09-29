// toast.go implements §6.7's "Avisos": stackable, bottom-right, 4-second
// notices for the other role connecting/disconnecting, a rejected message,
// a clipboard copy, a watch reconnection or the bridge closing.
package tui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

// toastTTL is spec §6.7's fixed notice lifetime.
const toastTTL = 4 * time.Second

// toast is one stacked notice; expiresAt is computed once at creation from
// the model's injected clock, so notices expire deterministically in tests
// without a real timer.
type toast struct {
	text      string
	expiresAt time.Time
}

// pruneToasts drops every toast whose expiresAt is at or before now,
// keeping the rest in their original (oldest-first) order.
func pruneToasts(toasts []toast, now time.Time) []toast {
	if len(toasts) == 0 {
		return toasts
	}
	kept := toasts[:0:0]
	for _, t := range toasts {
		if t.expiresAt.After(now) {
			kept = append(kept, t)
		}
	}
	return kept
}

// toastWindow builds the floating window for every live toast (§6.7:
// "los avisos deben flotar en la esquina inferior derecha sobre la
// conversación, no ocupar líneas del pie"), anchored to the conversation
// box's own bottom-right interior corner. false means there is nothing
// live to show right now.
func (m *Model) toastWindow() (floatingWindow, bool) {
	live := pruneToasts(m.toasts, m.now())
	if len(live) == 0 {
		return floatingWindow{}, false
	}
	toastWidth := clampInt(m.convBoxWidth-4, 20, 40)
	content := renderToastStack(live, m.th, toastWidth)
	height := len(live)
	x := convBoxLeft + m.convBoxWidth - 1 - toastWidth
	if x < convBoxLeft {
		x = convBoxLeft
	}
	y := convBoxTop + m.convBoxHeight - 1 - height
	if y < convBoxTop+1 {
		y = convBoxTop + 1
	}
	return floatingWindow{content: content, x: x, y: y, width: toastWidth, height: height}, true
}

// renderToastStack renders every live toast right-aligned within width,
// stacked oldest-on-top, newest-on-bottom (so the most recent notice sits
// closest to the corner it stacks from).
func renderToastStack(toasts []toast, th theme.Theme, width int) string {
	if len(toasts) == 0 {
		return ""
	}
	style := th.NoticeStyle().Padding(0, 1)
	if !th.NoColor {
		style = style.Reverse(true)
	}
	lines := make([]string, 0, len(toasts))
	for _, t := range toasts {
		card := style.Render(t.text)
		lines = append(lines, lipgloss.PlaceHorizontal(width, lipgloss.Right, card))
	}
	return strings.Join(lines, "\n")
}
