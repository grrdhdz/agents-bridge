package tui

// Capabilities is what the shell (Model) consults instead of branching
// Update on which of the four modes it is running in (spec §4 and §3:
// "las diferencias entre modos se expresan como capacidades, nunca con
// ramas de Update por modo"). Each mode in cmd/agents-bridge builds one of
// these with the constructors below; Model itself never asks "which mode
// am I" — only "can I do X".
type Capabilities struct {
	// Mode names the mode for display purposes only (status bar, window
	// title) — it drives no behavior by itself.
	Mode string
	// Ack is informational (shown in help/status): whether a peer message
	// this endpoint receives is actually confirmed on the wire. The real
	// behavior comes from which Transport is wired in (clientTransport
	// acks for real; ControlTransport's Ack is a documented no-op) — this
	// field never gates a branch in Model.
	Ack bool
	// CloseOnQuit: ctrl+c/ctrl+q and /quit close the whole bridge (calling
	// OnStop), not just this window.
	CloseOnQuit bool
	// StopConfirm: /stop (and the equivalent palette/help action) requires
	// a second confirmation before calling OnStop.
	StopConfirm bool
	// Pair: /pair and F5 (regenerate and copy the join command) are
	// available. Only the Mac host can ever pair a new executor.
	Pair bool
	// ReturnHome: esc returns to the bridge list (phase 3; unused for now
	// — the home screen does not exist yet — but the capability is wired
	// so the shell can grow it without another mode branch).
	ReturnHome bool
}

// CapabilitiesForHost is the Mac/tailscale-host orchestrator (§4 col. 1):
// it owns the bridge outright, so /stop and /quit close it immediately, and
// it is the only mode that can mint a new pairing command.
func CapabilitiesForHost() Capabilities {
	return Capabilities{Mode: "host", Ack: true, CloseOnQuit: true, StopConfirm: false, Pair: true, ReturnHome: false}
}

// CapabilitiesForJoin is `agents-bridge join`'s executor (§4 col. 2): it
// also owns its side of the bridge (closing the window closes the bridge,
// per §4 "sí (solo join)"), but it can never pair — only Mac can.
func CapabilitiesForJoin() Capabilities {
	return Capabilities{Mode: "join", Ack: true, CloseOnQuit: true, StopConfirm: false, Pair: false, ReturnHome: false}
}

// CapabilitiesForLocal is the observer TUI embedded in `agents-bridge local`
// (§4 col. 3, §7.1): it watches over the control plane like the standalone
// observer (no ACK, stop needs confirmation), but here the TUI *is* the
// process, so /quit closes the whole bridge.
func CapabilitiesForLocal() Capabilities {
	return Capabilities{Mode: "local", Ack: false, CloseOnQuit: true, StopConfirm: true, Pair: false, ReturnHome: false}
}

// CapabilitiesForObserver is standalone `agents-bridge tui --instance-id`
// (§4 col. 4): pure observer, never confirms, never closes the bridge on
// exit, and — once phase 3 adds the home screen — can return to it.
func CapabilitiesForObserver() Capabilities {
	return Capabilities{Mode: "tui", Ack: false, CloseOnQuit: false, StopConfirm: true, Pair: false, ReturnHome: true}
}
