package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

// defaultLocalIdleTimeout matches §5.1: a local instance with nobody present
// and no traffic for 30 minutes closes itself so it never lingers as an
// orphan process.
const defaultLocalIdleTimeout = 30 * time.Minute

// runLocal hosts one same-device instance: a loopback server plus an
// orchestrator and an executor client, each with its own control endpoint.
// It needs no Tailscale, pairing command, or, in headless mode, TUI, and it
// prints only a ready record without secrets. Everything is destroyed when
// ctx ends, the orchestrator endpoint receives POST /v1/stop, or idleTimeout
// elapses without activity (idleTimeout <= 0 disables the idle timer). When
// readyFile is non-empty, the same ready record is also written there (owner
// only — 0600, or the owner-only DACL on Windows — never over an existing
// file, and appearing only once complete, see control.ReadyFile) — §7.1:
// it lets an orchestrator that launched this in the user's own terminal
// learn instance_id without scraping stdout or shelling out to `ps`.
//
// §7.1: when headless is false and isTerminal() is true, runLocal shows its
// own embedded observer TUI instead of the plain ready/select loop — see
// runEmbeddedTUI. isTerminal is a parameter (not a direct term.IsTerminal
// call) so tests can exercise both paths without a real terminal. th is the
// already-resolved theme (flag/env/auto-detected, per --theme and
// CODEX_BRIDGE_THEME — see resolveTheme in main.go) the embedded TUI draws
// with.
func runLocal(ctx context.Context, stdout io.Writer, root, cwd string, idleTimeout time.Duration, readyFile string, headless bool, isTerminal func() bool, th theme.Theme) error {
	var readyOut *control.ReadyFile
	if readyFile != "" {
		// Checked up front, before anything else starts, so a stale file is
		// reported before any state exists to tear down.
		var err error
		readyOut, err = control.ReserveReadyFile(readyFile)
		if err != nil {
			return fmt.Errorf("--ready-file: %w", err)
		}
	}
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		return err
	}
	defer server.Close()
	addr := server.Addr().String()

	owner, _, err := bridge.Dial(ctx, addr, server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		return fmt.Errorf("conectar orquestador local: %w", err)
	}
	defer owner.Close()
	worker, _, err := bridge.Dial(ctx, addr, server.InstanceID(), protocol.RoleExecutor, server.JoinToken())
	if err != nil {
		return fmt.Errorf("conectar ejecutor local: %w", err)
	}
	defer worker.Close()

	activity := control.NewActivity()
	stop := newStopper()

	// peerConnected in local mode means both clients are connected: this
	// process hosts both roles, so neither client's own Connected() alone
	// says whether the other role is present (§4.1).
	peerConnected := func() bool { return owner.Connected() && worker.Connected() }

	ownerEndpoint, err := control.Start(owner, control.Options{
		Role:          protocol.RoleOrchestrator,
		Mode:          control.ModeLocal,
		CWD:           cwd,
		Root:          root,
		CanStop:       true,
		Stop:          stop.stop,
		PeerConnected: peerConnected,
		Activity:      activity,
	})
	if err != nil {
		return fmt.Errorf("ctl orquestador: %w", err)
	}
	defer ownerEndpoint.Close()
	workerEndpoint, err := control.Start(worker, control.Options{
		Role:          protocol.RoleExecutor,
		Mode:          control.ModeLocal,
		CWD:           cwd,
		Root:          root,
		CanStop:       false,
		PeerConnected: peerConnected,
		Activity:      activity,
	})
	if err != nil {
		return fmt.Errorf("ctl ejecutor: %w", err)
	}
	defer workerEndpoint.Close()

	go keepConnected(ctx, owner)
	go keepConnected(ctx, worker)
	go touchActivityOnMessages(ctx, owner, activity)
	go touchActivityOnMessages(ctx, worker, activity)
	if idleTimeout > 0 {
		go monitorIdle(ctx, activity, idleTimeout, stop.stop)
	}

	ready, err := json.Marshal(map[string]any{"v": 1, "type": "ready", "instance_id": server.InstanceID(), "mode": string(control.ModeLocal)})
	if err != nil {
		return err
	}
	if readyOut != nil {
		if err := readyOut.Publish(append(ready, '\n')); err != nil {
			return fmt.Errorf("--ready-file: %w", err)
		}
	}

	if shouldShowEmbeddedTUI(headless, isTerminal) {
		// The TUI owns the process from here: it never prints ready to
		// stdout (that would corrupt the terminal UI), and it — not the
		// select below — decides when the bridge closes.
		return runEmbeddedTUI(ctx, stop, ownerEndpoint, th, runTeaProgram)
	}

	if _, err := fmt.Fprintln(stdout, string(ready)); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-server.Done():
	case <-stop.done():
	}
	return nil
}

// shouldShowEmbeddedTUI is the pure decision point for §7.1: the embedded
// TUI shows only when nothing asked for headless mode and stdout is really
// a terminal. isTerminal is checked defensively (nil means "not a
// terminal") so a caller that forgets to wire it never accidentally shows a
// TUI in a script or CI job.
func shouldShowEmbeddedTUI(headless bool, isTerminal func() bool) bool {
	return !headless && isTerminal != nil && isTerminal()
}

// runTeaProgram is the default way to run a Bubble Tea program; it is a
// variable so tests can replace it for the duration of a test and exercise
// runEmbeddedTUI's wiring without a real terminal (Program.Run opens one).
var runTeaProgram = func(p *tea.Program) error {
	_, err := p.Run()
	return err
}

// runEmbeddedTUI implements §7.1's TUI-owns-the-process mode: local shows
// its own observer TUI (over the control plane, exactly like `codex-bridge
// tui`) attached to this instance's own orchestrator endpoint. Unlike a
// separately launched `codex-bridge tui`, where /quit only closes the
// window and the bridge lives on, here the TUI *is* the process: closing it
// (Ctrl+C, /quit, or a confirmed /stop) closes the whole bridge. The
// reverse also holds — an external stop (another terminal's
// `codex-bridge stop`, or --idle-timeout firing) must close this TUI too,
// which watchExternalStop below wires up.
func runEmbeddedTUI(ctx context.Context, stop *stopper, ownerEndpoint *control.Endpoint, th theme.Theme, run func(*tea.Program) error) error {
	model := tui.New(buildEmbeddedObserverOptions(ownerEndpoint, th))
	program := tea.NewProgram(&model)
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()
	go watchExternalStop(watchCtx, stop, program.Quit)
	return run(program)
}

// buildEmbeddedObserverOptions builds the embedded TUI's Options: it talks
// to its own orchestrator endpoint purely over the control plane (the same
// ControlTransport the standalone `codex-bridge tui` uses), and closing it
// asks that very endpoint to stop, which — because CanStop is true for the
// orchestrator in local mode — actually tears down the whole bridge.
// OwnsBridge is what tells the model so; the standalone TUI never sets it.
func buildEmbeddedObserverOptions(ownerEndpoint *control.Endpoint, th theme.Theme) tui.Options {
	transport := tui.NewControlTransport(ownerEndpoint.Descriptor())
	return tui.Options{
		Transport:    transport,
		LocalRole:    protocol.RoleOrchestrator,
		Capabilities: tui.CapabilitiesForLocal(),
		Theme:        th,
		OnStop: func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = transport.Stop(ctx)
		},
	}
}

// watchExternalStop calls quit once stop fires from outside the TUI itself
// (another terminal's `codex-bridge stop`, or --idle-timeout), or once ctx
// ends (process-level shutdown) — either way, the embedded TUI must not be
// left running against a bridge that is already gone or going away.
func watchExternalStop(ctx context.Context, stop *stopper, quit func()) {
	select {
	case <-ctx.Done():
	case <-stop.done():
	}
	quit()
}

// keepConnected replaces the TUI reconnect tick for headless clients.
func keepConnected(ctx context.Context, client *bridge.Client) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-client.Done():
			return
		case <-ticker.C:
			if !client.Connected() {
				attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
				_ = client.Reconnect(attempt)
				cancel()
			}
		}
	}
}

// touchActivityOnMessages records activity (§5.1) for every message this
// client publishes or receives, so both an orchestrator's task and an
// executor's result reset the idle clock.
func touchActivityOnMessages(ctx context.Context, client *bridge.Client, activity *control.Activity) {
	sub, err := client.Subscribe(client.EventHub().LatestEventSeq())
	if err != nil {
		return
	}
	defer sub.Close()
	for {
		event, err := sub.Next(ctx)
		if err != nil {
			return
		}
		if event.Kind == bridge.EventMessage {
			activity.Touch()
		}
	}
}

// monitorIdle closes the instance (via stop) once activity has been absent
// for idleTimeout. The poll interval scales with the timeout so short test
// timeouts still resolve promptly.
func monitorIdle(ctx context.Context, activity *control.Activity, idleTimeout time.Duration, stop func()) {
	interval := idleTimeout / 10
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	if interval > time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Since(activity.LastActivity()) >= idleTimeout {
				stop()
				return
			}
		}
	}
}

// stopper lets POST /v1/stop request shutdown of a headless instance whose
// lifecycle is otherwise driven by a plain select on channels. stop is
// idempotent and safe to call from any goroutine, including concurrently
// with itself.
type stopper struct {
	once sync.Once
	ch   chan struct{}
}

func newStopper() *stopper { return &stopper{ch: make(chan struct{})} }

func (s *stopper) stop() {
	s.once.Do(func() { close(s.ch) })
}

func (s *stopper) done() <-chan struct{} { return s.ch }
