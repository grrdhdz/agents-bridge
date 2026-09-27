package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui"
)

// runTUIObserver implements `codex-bridge tui --instance-id ID` (§6): an
// observing TUI that attaches to a live bridge purely through the control
// plane (watch/send/health), never the TCP protocol, so it works the same
// for local, tailscale-host and tailscale-join.
func runTUIObserver(args []string, root string) error {
	flags := flag.NewFlagSet("codex-bridge tui", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	instanceID := flags.String("instance-id", "", "instance_id to observe")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if strings.TrimSpace(*instanceID) == "" {
		return fmt.Errorf("--instance-id is required")
	}
	descriptor, err := selectObserverDescriptor(root, *instanceID)
	if err != nil {
		return err
	}
	transport := tui.NewControlTransport(descriptor)
	model := tui.New(tui.Options{
		Transport: transport,
		LocalRole: descriptor.LocalRole,
		Observer:  true,
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
	descriptor, err := control.SelectDescriptor(root, instanceID, protocol.RoleOrchestrator)
	if err == nil {
		return descriptor, nil
	}
	return control.SelectDescriptor(root, instanceID, protocol.RoleExecutor)
}
