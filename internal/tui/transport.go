package tui

import (
	"context"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

// Transport is the message channel Model needs, reduced to exactly the
// methods it calls (§6.2 of docs/superpowers/specs/2026-09-27-bridge-visibility-design.md):
// nothing here mirrors the whole of *bridge.Client. Two implementations
// exist: clientTransport, wrapping the direct TCP *bridge.Client used by the
// Mac/Windows TUI today, and the observing TUI's control-plane client
// (watch + send + health over control.Do), added alongside `codex-bridge tui`.
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
// interface existed.
type clientTransport struct{ client *bridge.Client }

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
