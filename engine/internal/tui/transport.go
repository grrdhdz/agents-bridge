package tui

import (
	"context"
	"os"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

// BridgeStatus is what an optional StatusProvider reports (spec §7): enough
// to fill the sidebar's "Puente" section beyond what Model already deduces
// from frame events alone.
type BridgeStatus struct {
	// PeerConnected reports whether the other role currently has a live
	// connection, independent of Model's own frame-derived connState.
	PeerConnected bool
	// StartedAt is when this side of the bridge came up, used for the
	// sidebar's "tiempo activo".
	StartedAt time.Time
	// Cwd is the project directory (base name shown in the status bar),
	// taken from the descriptor when one exists (§7: "toma el cwd del
	// descriptor en el lado cliente cuando exista"), or the process's own
	// working directory for host/join (they are that process). Empty when
	// neither is available.
	Cwd string
	// Roles is each role's state (§3.4), when the source knows it: the
	// control plane reports both roles in `local` and only its own in
	// host/join; a missing role means unknown.
	Roles map[protocol.Role]control.RoleSnapshot
}

// StatusProvider is spec §7's optional interface: a Transport that can also
// report BridgeStatus, checked with a type assertion so a Transport that
// does not implement it (there is none left un-implemented today, but the
// interface stays optional per spec) simply leaves the sidebar showing only
// what frame events already reveal.
type StatusProvider interface {
	Status(ctx context.Context) (BridgeStatus, error)
}

// Transport is the message channel Model needs, reduced to exactly the
// methods it calls (§6.2 of docs/superpowers/specs/2026-09-27-bridge-visibility-design.md):
// nothing here mirrors the whole of *bridge.Client. Two implementations
// exist: clientTransport, wrapping the direct TCP *bridge.Client used by the
// Mac/Windows TUI today, and the observing TUI's control-plane client
// (watch + send + health over control.Do), added alongside `agents-bridge tui`.
//
// The observing TUI never confirms a peer message on the agent's behalf —
// the agent's own ctl wait must still receive and ACK it — so its Ack is a
// documented no-op rather than an omitted method; that keeps Model's
// handleFrame identical for both transports instead of branching on which
// one is in play.
type Transport interface {
	InstanceID() string
	Connected() bool
	Reconnect(ctx context.Context) error
	Subscribe(afterEventSeq uint64) (EventSubscription, error)
	Publish(body string) (protocol.Envelope, error)
	Ack(messageID string, serverSeq uint64) error
	QueueStats() (messages int, bytes int)
	Close()
}

// EventSubscription is what Model needs from a subscription: pull the next
// event, and release it when done. *bridge.Subscription already satisfies
// this directly.
type EventSubscription interface {
	Next(ctx context.Context) (bridge.Event, error)
	Close()
}

// clientTransport adapts a *bridge.Client to Transport without changing any
// of its behavior: this is exactly what the TUI called before the
// interface existed. It also implements StatusProvider (§7): peerConnected
// is the mode-specific callback the caller wires in (e.g.
// *bridge.Server.WorkerConnected for the host, since a *bridge.Client alone
// has no notion of "is my peer connected" — only "is my own connection
// up"); startedAt and cwd are captured once at construction, since host and
// join are themselves the process whose directory and start time matter.
type clientTransport struct {
	client        *bridge.Client
	peerConnected func() bool
	roleStates    func() map[protocol.Role]control.RoleSnapshot
	startedAt     time.Time
	cwd           string
}

func newClientTransport(client *bridge.Client, peerConnected func() bool, roleStates func() map[protocol.Role]control.RoleSnapshot) clientTransport {
	cwd, _ := os.Getwd()
	return clientTransport{client: client, peerConnected: peerConnected, roleStates: roleStates, startedAt: time.Now(), cwd: cwd}
}

func (t clientTransport) Status(context.Context) (BridgeStatus, error) {
	connected := t.client.Connected()
	if t.peerConnected != nil {
		connected = t.peerConnected()
	}
	status := BridgeStatus{PeerConnected: connected, StartedAt: t.startedAt, Cwd: t.cwd}
	if t.roleStates != nil {
		status.Roles = t.roleStates()
	}
	return status, nil
}

func (t clientTransport) InstanceID() string { return t.client.InstanceID() }
func (t clientTransport) Connected() bool    { return t.client.Connected() }

func (t clientTransport) Reconnect(ctx context.Context) error {
	return t.client.Reconnect(ctx)
}

func (t clientTransport) Subscribe(afterEventSeq uint64) (EventSubscription, error) {
	sub, err := t.client.Subscribe(afterEventSeq)
	if err != nil {
		return nil, err
	}
	return sub, nil
}

func (t clientTransport) Publish(body string) (protocol.Envelope, error) {
	return t.client.Publish(body)
}

func (t clientTransport) Ack(messageID string, serverSeq uint64) error {
	return t.client.Ack(messageID, serverSeq)
}

func (t clientTransport) QueueStats() (int, int) { return t.client.QueueStats() }
func (t clientTransport) Close()                 { t.client.Close() }
