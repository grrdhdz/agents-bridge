package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

func TestFuzzyMatchIsOrderedSubsequenceCaseInsensitive(t *testing.T) {
	cases := []struct {
		query, target string
		want          bool
	}{
		{"", "cualquier cosa", true},
		{"cp", "Cerrar Puente", true},
		{"cierre", "Cerrar Puente", false}, // letters out of order/missing
		{"puente", "Cerrar Puente", true},
		{"zz", "Cerrar Puente", false},
	}
	for _, c := range cases {
		if got := fuzzyMatch(c.query, c.target); got != c.want {
			t.Fatalf("fuzzyMatch(%q, %q) = %v, want %v", c.query, c.target, got, c.want)
		}
	}
}

// TestPaletteItemsReflectCapabilities covers §6.6: the palette only offers
// actions this mode's Capabilities actually allow (e.g. only the host can
// copy a join command; only a mode with OnStop can close the bridge).
func TestPaletteItemsReflectCapabilities(t *testing.T) {
	host := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), OnStop: func() {}})
	hasItem := func(items []paletteItem, id string) bool {
		for _, it := range items {
			if it.id == id {
				return true
			}
		}
		return false
	}
	hostItems := host.paletteItems()
	if !hasItem(hostItems, "copy-join-command") {
		t.Fatal("host should offer copying the join command")
	}
	if !hasItem(hostItems, "close-bridge") {
		t.Fatal("host has OnStop, so it should offer closing the bridge")
	}

	join := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleExecutor, Capabilities: CapabilitiesForJoin()})
	joinItems := join.paletteItems()
	if hasItem(joinItems, "copy-join-command") {
		t.Fatal("join must never offer pairing/copy-join-command (host only)")
	}
	if hasItem(joinItems, "close-bridge") {
		t.Fatal("join has no OnStop wired in this test, so close-bridge should not be offered")
	}
}

// TestPaletteFilterNarrowsAndEnterRunsHighlightedItem exercises the whole
// open → type → confirm flow end to end.
func TestPaletteFilterNarrowsAndEnterRunsHighlightedItem(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = *updated.(*Model)
	model.openPalette()
	if !model.paletteOpen {
		t.Fatal("openPalette should open the overlay")
	}
	for _, r := range "tema" {
		updated, _ := model.handlePaletteKey(tea.KeyPressMsg{Text: string(r)})
		model = *updated.(*Model)
	}
	items := model.filteredPaletteItems()
	if len(items) != 1 || items[0].id != "toggle-theme" {
		t.Fatalf("filtering by \"tema\" should narrow to toggle-theme, got %#v", items)
	}
	beforeMode := model.th.Mode
	updated, _ = model.handlePaletteKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = *updated.(*Model)
	if model.paletteOpen {
		t.Fatal("enter should close the palette")
	}
	if model.th.Mode == beforeMode {
		t.Fatalf("enter should have run toggle-theme, mode is still %v", model.th.Mode)
	}
}

func TestPaletteEscCancelsWithoutRunningAnything(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost()})
	model.openPalette()
	updated, _ := model.handlePaletteKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = *updated.(*Model)
	if model.paletteOpen {
		t.Fatal("esc should close the palette")
	}
}

// TestPaletteViewShowsFilteredItems is a light rendering check.
func TestPaletteViewShowsFilteredItems(t *testing.T) {
	model := New(Options{Transport: &fakeTransport{instanceID: "abc"}, LocalRole: protocol.RoleOrchestrator, Capabilities: CapabilitiesForHost(), OnStop: func() {}})
	model.openPalette()
	view := model.View().Content
	if !strings.Contains(view, "Cerrar puente") {
		t.Fatalf("palette view should list its items: %q", view)
	}
}
