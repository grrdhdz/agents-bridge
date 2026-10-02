package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"github.com/grrdhdz/agents-bridge/engine/internal/tui/theme"
)

// goldenModel builds a fully deterministic bridge-screen Model: a fixed
// instance id, a fixed clock (spec §10.2: "sin horas reales: inyecta el
// reloj") and a fixed pair of messages exercising both roles, both origins,
// a TAREA/RESULTADO label and more than one delivery status.
func goldenModel(t *testing.T, width int, mode theme.Mode) *Model {
	t.Helper()
	fixedNow := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	th := theme.New(mode, false, nil)
	transport := &fakeTransport{instanceID: "instancia-golden-0001"}
	model := New(Options{
		Transport:    transport,
		LocalRole:    protocol.RoleOrchestrator,
		Capabilities: CapabilitiesForHost(),
		Theme:        th,
		Now:          func() time.Time { return fixedNow },
	})

	when := fixedNow.Add(-2 * time.Minute)
	specs := []struct {
		id     string
		role   protocol.Role
		body   string
		source string
		status string
	}{
		{"m1", protocol.RoleOrchestrator, "TAREA\nRevisa el PR #42 y confirma que los tests pasan.", protocol.SourceHumanOperator, "delivered"},
		{"m2", protocol.RoleExecutor, "RESULTADO\nTests verdes, listo para hacer merge.", protocol.SourceAgentControl, "accepted"},
	}
	for _, spec := range specs {
		e, err := protocol.NewEnvelopeWithSource("instancia-golden-0001", spec.id, 1, protocol.ExpectedSenderID(spec.role), spec.role, spec.body, spec.source, when)
		if err != nil {
			t.Fatal(err)
		}
		model.messages = append(model.messages, e)
		model.byID[e.MessageID] = len(model.messages) - 1
		model.statuses[e.MessageID] = spec.status
		when = when.Add(30 * time.Second)
	}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	return updated.(*Model)
}

// TestGoldenViewsAreDeterministicAcrossWidthsAndThemes covers spec §10.2:
// the bridge screen at 60/80/120 columns, in both themes, with a fixed
// color profile (ANSI truecolor escape sequences are emitted the same way
// regardless of the real terminal since Model.View never re-detects the
// terminal itself) and an injected clock, must render byte-for-byte the
// same output every time and match the checked-in fixture in testdata/.
//
// Run with UPDATE_GOLDEN=1 to (re)write the fixtures after an intentional
// view change.
func TestGoldenViewsAreDeterministicAcrossWidthsAndThemes(t *testing.T) {
	for _, width := range []int{60, 80, 120} {
		for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
			name := fmt.Sprintf("w%d_%s", width, mode)
			t.Run(name, func(t *testing.T) {
				out1 := goldenModel(t, width, mode).View().Content
				out2 := goldenModel(t, width, mode).View().Content
				if out1 != out2 {
					t.Fatalf("view for %s is not deterministic across two otherwise-identical renders", name)
				}
				for _, line := range strings.Split(out1, "\n") {
					if w := lipgloss.Width(line); w > width {
						t.Fatalf("%s: line wider than the terminal (%d > %d): %q", name, w, width, line)
					}
				}

				golden := filepath.Join("testdata", name+".golden")
				if os.Getenv("UPDATE_GOLDEN") != "" {
					if err := os.MkdirAll("testdata", 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(golden, []byte(out1), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("missing golden fixture %s (run `UPDATE_GOLDEN=1 go test ./internal/tui/...` once to create it): %v", golden, err)
				}
				if string(want) != out1 {
					t.Fatalf("view for %s no longer matches its golden fixture %s; if the change is intentional, rerun with UPDATE_GOLDEN=1", name, golden)
				}
			})
		}
	}
}

// TestGoldenPaletteOverlayIsDeterministic covers the floating command
// palette specifically (spec item 4's overlay rewrite): golden at 80
// columns, both themes, with the palette open over the same fixed
// conversation — checking that the backdrop remains visible behind the
// floating window and that compositing itself is exactly as deterministic
// as the plain screen.
func TestGoldenPaletteOverlayIsDeterministic(t *testing.T) {
	for _, mode := range []theme.Mode{theme.ModeDark, theme.ModeLight} {
		name := fmt.Sprintf("palette_w80_%s", mode)
		t.Run(name, func(t *testing.T) {
			build := func() *Model {
				m := goldenModel(t, 80, mode)
				m.openPalette()
				return m
			}
			out1 := build().View().Content
			out2 := build().View().Content
			if out1 != out2 {
				t.Fatalf("palette overlay view for %s is not deterministic across two otherwise-identical renders", name)
			}
			for _, line := range strings.Split(out1, "\n") {
				if w := lipgloss.Width(line); w > 80 {
					t.Fatalf("%s: line wider than the terminal (%d > 80): %q", name, w, line)
				}
			}

			golden := filepath.Join("testdata", name+".golden")
			if os.Getenv("UPDATE_GOLDEN") != "" {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(out1), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden fixture %s (run `UPDATE_GOLDEN=1 go test ./internal/tui/...` once to create it): %v", golden, err)
			}
			if string(want) != out1 {
				t.Fatalf("palette overlay view for %s no longer matches its golden fixture %s; if the change is intentional, rerun with UPDATE_GOLDEN=1", name, golden)
			}
		})
	}
}
