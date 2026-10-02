package tui

import "testing"

// TestCapabilitiesMatchSpecTable pins each mode's constructor to spec §4's
// table so a future edit to one mode can never silently drift from another.
func TestCapabilitiesMatchSpecTable(t *testing.T) {
	cases := []struct {
		name string
		got  Capabilities
		want Capabilities
	}{
		{"host", CapabilitiesForHost(), Capabilities{Mode: "host", Ack: true, CloseOnQuit: true, StopConfirm: false, Pair: true, ReturnHome: false}},
		{"join", CapabilitiesForJoin(), Capabilities{Mode: "join", Ack: true, CloseOnQuit: true, StopConfirm: false, Pair: false, ReturnHome: false}},
		{"local", CapabilitiesForLocal(), Capabilities{Mode: "local", Ack: false, CloseOnQuit: true, StopConfirm: true, Pair: false, ReturnHome: false}},
		{"observer", CapabilitiesForObserver(), Capabilities{Mode: "tui", Ack: false, CloseOnQuit: false, StopConfirm: true, Pair: false, ReturnHome: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Fatalf("got %+v, want %+v", c.got, c.want)
			}
		})
	}
}

// TestOnlyHostCanPair covers §4's "Emparejar" row: exactly one mode may
// pair.
func TestOnlyHostCanPair(t *testing.T) {
	pairing := 0
	for _, caps := range []Capabilities{CapabilitiesForHost(), CapabilitiesForJoin(), CapabilitiesForLocal(), CapabilitiesForObserver()} {
		if caps.Pair {
			pairing++
		}
	}
	if pairing != 1 {
		t.Fatalf("expected exactly one mode able to pair, got %d", pairing)
	}
}
