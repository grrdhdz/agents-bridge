package tui

import (
	"context"

	"github.com/grrdhdz/agents-bridge/internal/bridges"
	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

// BridgeSession is what entering a bridge from the home screen needs: the
// observer's transport (the control-plane client for that instance), the
// role it writes as, and how to ask the bridge to stop.
type BridgeSession struct {
	Transport Transport
	LocalRole protocol.Role
	OnStop    func()
}

// HomeSource is everything the home screen (spec §5) needs from the outside
// world, so tests drive it with a fake and the real thing lives in
// cmd/agents-bridge (descriptors + /v1/health via internal/bridges, the
// `agents-bridge stop` path, and a background `local --headless` launcher).
type HomeSource interface {
	// List describes this user's live bridges (the same source as `ps`).
	List(ctx context.Context) ([]bridges.Info, error)
	// Stop asks the bridge to close (the same path as `agents-bridge stop`).
	Stop(ctx context.Context, instanceID string) error
	// Open prepares the observing session for instanceID: the orchestrator's
	// endpoint when it lives on this machine, otherwise the executor's.
	Open(instanceID string) (BridgeSession, error)
	// Create starts a new `local --headless` bridge in the background and
	// returns its instance_id once it is ready.
	Create(ctx context.Context) (string, error)
}
