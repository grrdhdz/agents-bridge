package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

// TestGoldenHomeScreens covers spec §10.2 for the home screen: empty and
// with three bridges, at 60/80/120 columns, in both themes, byte-for-byte
// deterministic (fixed clock, fixed start times, no live refresh timer),
// with no line wider than the terminal. Run with UPDATE_GOLDEN=1 to
// (re)write the fixtures after an intentional view change.
func TestGoldenHomeScreens(t *testing.T) {
	for _, withList := range []bool{false, true} {
		for _, width := range []int{60, 80, 120} {
			for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
				kind := "empty"
				if withList {
					kind = "three"
				}
				name := fmt.Sprintf("home_%s_w%d_%s", kind, width, mode)
				t.Run(name, func(t *testing.T) {
					build := func() string {
						src := &fakeSource{}
						if withList {
							src.set(threeBridges()...)
						}
						return newHomeTest(t, src, width, 24, mode).View().Content
					}
					out := build()
					if out != build() {
						t.Fatalf("%s is not deterministic across two identical renders", name)
					}
					for _, line := range strings.Split(out, "\n") {
						if w := lipgloss.Width(line); w > width {
							t.Fatalf("%s: line wider than the terminal (%d > %d): %q", name, w, width, line)
						}
					}
					golden := filepath.Join("testdata", name+".golden")
					if os.Getenv("UPDATE_GOLDEN") != "" {
						if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					want, err := os.ReadFile(golden)
					if err != nil {
						t.Fatalf("missing golden fixture %s (run `UPDATE_GOLDEN=1 go test ./internal/tui/...` once to create it): %v", golden, err)
					}
					if string(want) != out {
						t.Fatalf("%s no longer matches its golden fixture %s; if the change is intentional, rerun with UPDATE_GOLDEN=1", name, golden)
					}
				})
			}
		}
	}
}
