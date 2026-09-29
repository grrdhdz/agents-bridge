// hitregion.go is the single source of truth mapping mouse clicks to
// actions: every clickable rectangle View() draws is registered here, in
// the same pass that renders it, so Update's click handling never derives
// a coordinate independently of what was actually drawn (the redesign's
// mouse addendum: "el mapeo de coordenadas debe salir de la misma
// información de layout que usa View").
package tui

import tea "charm.land/bubbletea/v2"

// hitRegionOf is one clickable rectangle in absolute screen coordinates
// (0-based, matching tea.Mouse's own X/Y), spanning a single row — every
// clickable element in this UI (a card's line, a shortcut, a palette row,
// a dialog button, a bridge-list row) is naturally one row tall, so a list
// of single-row spans is enough and stays simple. It is generic over the
// screen model its click closures act on: the bridge view (*Model) and the
// home screen (*HomeModel) share this one registry mechanism.
type hitRegionOf[M any] struct {
	y       int
	x0, x1  int // inclusive
	onClick func(M) (tea.Model, tea.Cmd)
}

func (r hitRegionOf[M]) contains(x, y int) bool {
	return y == r.y && x >= r.x0 && x <= r.x1
}

// hitRegion is the bridge view's region type.
type hitRegion = hitRegionOf[*Model]

// appendRegion registers one clickable rectangle in regions. x1 < x0 (an
// empty/negative span, e.g. a label the shortcuts bar had no room to show)
// is silently ignored rather than asserted, since callers derive spans from
// strings.Index results that can legitimately come back "not found".
func appendRegion[M any](regions []hitRegionOf[M], y, x0, x1 int, onClick func(M) (tea.Model, tea.Cmd)) []hitRegionOf[M] {
	if x1 < x0 {
		return regions
	}
	return append(regions, hitRegionOf[M]{y: y, x0: x0, x1: x1, onClick: onClick})
}

// findRegion returns the last-registered region containing (x, y), or nil.
// "Last registered wins" matters only when two regions legitimately
// overlap; nothing in this UI currently overlaps, but the rule is simple
// and keeps later (logically "on top") registrations authoritative if that
// ever changes.
func findRegion[M any](regions []hitRegionOf[M], x, y int) *hitRegionOf[M] {
	var found *hitRegionOf[M]
	for i := range regions {
		if regions[i].contains(x, y) {
			r := regions[i]
			found = &r
		}
	}
	return found
}

func (m *Model) addRegion(y, x0, x1 int, onClick func(*Model) (tea.Model, tea.Cmd)) {
	m.regions = appendRegion(m.regions, y, x0, x1, onClick)
}

func (m *Model) regionAt(x, y int) *hitRegion { return findRegion(m.regions, x, y) }
