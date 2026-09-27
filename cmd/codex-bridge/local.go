package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

// runLocal hosts one same-device instance: a loopback server plus an
// orchestrator and an executor client, each with its own control endpoint.
// It needs no Tailscale, TUI or pairing command, and it prints only a ready
// record without secrets. Everything is destroyed when ctx ends.
func runLocal(ctx context.Context, stdout io.Writer, root, cwd string) error {
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

	ownerEndpoint, err := control.StartWithRoot(owner, protocol.RoleOrchestrator, cwd, root)
	if err != nil {
		return fmt.Errorf("ctl orquestador: %w", err)
	}
	defer ownerEndpoint.Close()
	workerEndpoint, err := control.StartWithRoot(worker, protocol.RoleExecutor, cwd, root)
	if err != nil {
		return fmt.Errorf("ctl ejecutor: %w", err)
	}
	defer workerEndpoint.Close()

	go keepConnected(ctx, owner)
	go keepConnected(ctx, worker)

	ready, err := json.Marshal(map[string]any{"v": 1, "type": "ready", "instance_id": server.InstanceID(), "mode": "local"})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, string(ready)); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-server.Done():
	}
	return nil
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
