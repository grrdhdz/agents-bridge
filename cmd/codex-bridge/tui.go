package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridges"
	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

// runTUIObserver implements `codex-bridge tui` (§5, §6): without
// --instance-id the home screen (list of live bridges); with it, an
// observing TUI that attaches to a live bridge purely through the control
// plane (watch/send/health), never the TCP protocol, so it works the same
// for local, tailscale-host and tailscale-join.
func runTUIObserver(args []string, root string) error {
	flags := flag.NewFlagSet("codex-bridge tui", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	instanceID := flags.String("instance-id", "", "instance_id to observe")
	themeValue := flags.String("theme", "", "tema de la TUI: dark, light o auto (por defecto CODEX_BRIDGE_THEME o auto)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	th, err := resolveTheme(*themeValue, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*instanceID) == "" {
		// No --instance-id: the home screen, the list of live bridges (§5).
		return startHomeTUI(th, root)
	}
	descriptor, err := selectObserverDescriptor(root, *instanceID)
	if err != nil {
		return err
	}
	transport := tui.NewControlTransport(descriptor)
	// Entered directly with --instance-id: there is no home screen behind
	// this view, so esc never "returns" anywhere and closing the bridge ends
	// the TUI (the behavior before phase 3).
	caps := directObserverCapabilities()
	model := tui.New(tui.Options{
		Transport:    transport,
		LocalRole:    descriptor.LocalRole,
		Capabilities: caps,
		Theme:        th,
		OnStop: func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = transport.Stop(ctx)
		},
	})
	_, err = tea.NewProgram(&model).Run()
	return err
}

// selectObserverDescriptor picks the orchestrator's descriptor for this
// instance when it lives on this machine, otherwise the executor's (§6): a
// join-side machine only ever has the executor's descriptor to attach
// through, and that is enough to watch and intervene in the conversation.
func selectObserverDescriptor(root, instanceID string) (control.Descriptor, error) {
	return bridges.ObserverDescriptor(root, instanceID)
}

// startHomeTUI runs the home screen against the real world; it is a
// variable so tests can check the dispatch without opening a terminal.
var startHomeTUI = func(th theme.Theme, root string) error {
	return runTUIHome(context.Background(), th, newHomeSource(root, launchLocalBridge), os.Stdout, runTeaModel)
}

// directObserverCapabilities is the observer's capability set when entered
// with --instance-id: identical to the home-screen observer's except that
// there is no home to return to.
func directObserverCapabilities() tui.Capabilities {
	caps := tui.CapabilitiesForObserver()
	caps.ReturnHome = false
	return caps
}
