package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/internal/tui/theme"
)

// TestPruneToastsDropsExpiredKeepsLive covers §6.7's 4-second lifetime with
// an injected clock: a toast created at t0 must still show at t0+3s and be
// gone by t0+5s.
func TestPruneToastsDropsExpiredKeepsLive(t *testing.T) {
	t0 := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	toasts := []toast{
		{text: "otro rol conectado", expiresAt: t0.Add(toastTTL)},
		{text: "mensaje rechazado", expiresAt: t0.Add(toastTTL).Add(2 * time.Second)},
	}
	stillLive := pruneToasts(toasts, t0.Add(3*time.Second))
	if len(stillLive) != 2 {
		t.Fatalf("both toasts should still be live at t0+3s, got %d", len(stillLive))
	}
	oneExpired := pruneToasts(toasts, t0.Add(toastTTL).Add(time.Second))
	if len(oneExpired) != 1 || oneExpired[0].text != "mensaje rechazado" {
		t.Fatalf("only the second toast should survive past the first's expiry, got %+v", oneExpired)
	}
	allExpired := pruneToasts(toasts, t0.Add(10*time.Second))
	if len(allExpired) != 0 {
		t.Fatalf("both toasts should be gone by t0+10s, got %+v", allExpired)
	}
}

// TestRenderToastStackShowsEveryLiveToastStacked covers the "apilables"
// requirement: every live toast renders, one per line.
func TestRenderToastStackShowsEveryLiveToastStacked(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	toasts := []toast{{text: "puente cerrado"}, {text: "copiado"}}
	out := renderToastStack(toasts, th, 40)
	for _, want := range []string{"puente cerrado", "copiado"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected toast stack to contain %q, got %q", want, out)
		}
	}
	if len(strings.Split(out, "\n")) != 2 {
		t.Fatalf("expected one line per toast, got %q", out)
	}
}

func TestRenderToastStackEmptyIsEmptyString(t *testing.T) {
	th := theme.New(theme.ModeDark, false, nil)
	if out := renderToastStack(nil, th, 40); out != "" {
		t.Fatalf("no toasts should render nothing, got %q", out)
	}
}
