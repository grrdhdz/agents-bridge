package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridges"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

// launchFunc starts a new bridge in the background and returns its
// instance_id once it is ready. Tests inject a fake: real processes are
// never launched from tests.
type launchFunc func(ctx context.Context) (string, error)

// homeSource is the real tui.HomeSource (spec §5): listing and stopping go
// through internal/bridges (the very code `ps` and `stop` use), opening
// attaches the observer transport `tui --instance-id` uses, and creating
// runs launch.
type homeSource struct {
	root   string
	launch launchFunc
}

func newHomeSource(root string, launch launchFunc) *homeSource {
	return &homeSource{root: root, launch: launch}
}

func (s *homeSource) List(ctx context.Context) ([]bridges.Info, error) {
	return bridges.List(ctx, s.root)
}

func (s *homeSource) Stop(ctx context.Context, instanceID string) error {
	return bridges.Stop(ctx, s.root, instanceID)
}

func (s *homeSource) Open(instanceID string) (tui.BridgeSession, error) {
	descriptor, err := selectObserverDescriptor(s.root, instanceID)
	if err != nil {
		return tui.BridgeSession{}, err
	}
	transport := tui.NewControlTransport(descriptor)
	return tui.BridgeSession{
		Transport: transport,
		LocalRole: descriptor.LocalRole,
		OnStop: func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = transport.Stop(ctx)
		},
	}, nil
}

func (s *homeSource) Create(ctx context.Context) (string, error) {
	if s.launch == nil {
		return "", fmt.Errorf("no hay lanzador de puentes configurado")
	}
	return s.launch(ctx)
}

// --- launching a background bridge --------------------------------------

// launchReadyTimeout bounds how long creating a bridge may wait for the
// child to publish its ready file.
const launchReadyTimeout = 20 * time.Second

// proc is what the launcher needs from a started child: a channel closed
// when it exits, and a way to kill it.
type proc struct {
	exited <-chan struct{}
	kill   func()
}

// buildLocalCommand is `<exe> local --headless --ready-file <path>`: this
// same executable, run directly with argv (never through a shell), with the
// default --idle-timeout, and with no stdio attached — the child must not
// touch the TUI's terminal.
func buildLocalCommand(exe, readyPath string) *exec.Cmd {
	return exec.Command(exe, "local", "--headless", "--ready-file", readyPath)
}

// startDetached is the real process starter: the child lives in its own
// session/process group (see detachAttrs) so it is decoupled from the TUI —
// it keeps running after the TUI exits and is not hit by the terminal's
// Ctrl+C — and a goroutine reaps it when it ends.
func startDetached(cmd *exec.Cmd) (proc, error) {
	detachAttrs(cmd)
	if err := cmd.Start(); err != nil {
		return proc{}, err
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	return proc{exited: exited, kill: func() { _ = cmd.Process.Kill() }}, nil
}

// launchLocalBridge starts a `local --headless` bridge with this
// executable and returns its instance_id.
func launchLocalBridge(ctx context.Context) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("no se pudo localizar el ejecutable: %w", err)
	}
	return launchLocalBridgeWith(ctx, exe, startDetached)
}

// launchLocalBridgeWith is launchLocalBridge with the executable and the
// process starter injected. The ready file lives in a private temp
// directory (os.MkdirTemp is owner-only) that always disappears when this
// returns; a child that has not published by then fails its own publish
// (the directory is gone) and exits, and one that is still around after a
// failed wait is killed, so no orphan is left behind by a failed creation.
func launchLocalBridgeWith(ctx context.Context, exe string, start func(*exec.Cmd) (proc, error)) (string, error) {
	dir, err := os.MkdirTemp("", "codex-bridge-ready-")
	if err != nil {
		return "", fmt.Errorf("directorio temporal: %w", err)
	}
	defer os.RemoveAll(dir)
	readyPath := filepath.Join(dir, "ready.json")
	child, err := start(buildLocalCommand(exe, readyPath))
	if err != nil {
		return "", fmt.Errorf("no se pudo lanzar el puente: %w", err)
	}
	id, err := waitReadyFile(ctx, readyPath, child.exited, launchReadyTimeout)
	if err != nil {
		child.kill()
		return "", err
	}
	return id, nil
}

// waitReadyFile polls path until the child has published its ready record
// (the file only ever appears complete, see control.ReadyFile) and returns
// its instance_id. It fails early if the child exits first, on timeout and
// when ctx ends.
func waitReadyFile(ctx context.Context, path string, exited <-chan struct{}, timeout time.Duration) (string, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	poll := time.NewTicker(30 * time.Millisecond)
	defer poll.Stop()
	for {
		if data, err := os.ReadFile(path); err == nil {
			var ready struct {
				InstanceID string `json:"instance_id"`
			}
			if json.Unmarshal(data, &ready) == nil && ready.InstanceID != "" {
				return ready.InstanceID, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("el puente no estuvo listo a tiempo")
		case <-exited:
			// The file may have been written just before it exited.
			if data, err := os.ReadFile(path); err == nil {
				var ready struct {
					InstanceID string `json:"instance_id"`
				}
				if json.Unmarshal(data, &ready) == nil && ready.InstanceID != "" {
					return ready.InstanceID, nil
				}
			}
			return "", fmt.Errorf("el proceso del puente terminó antes de estar listo")
		case <-poll.C:
		}
	}
}

// --- running the home TUI -----------------------------------------------

// runTUIHome runs `codex-bridge tui` without --instance-id: the App shell
// over source. run drives the program (production: bubbletea; tests: a
// fake). Once the TUI is gone and the terminal restored, it prints on
// stdout the bridges created in this session that are still alive and how
// to close them (it never closes them itself).
func runTUIHome(ctx context.Context, th theme.Theme, source tui.HomeSource, stdout io.Writer, run func(tea.Model) error) error {
	app := tui.NewApp(tui.AppOptions{Source: source, Theme: th})
	err := run(&app)
	app.Close()
	noticeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if notice := app.ExitNotice(noticeCtx); notice != "" {
		fmt.Fprint(stdout, notice)
	}
	return err
}

func runTeaModel(m tea.Model) error {
	_, err := tea.NewProgram(m).Run()
	return err
}
