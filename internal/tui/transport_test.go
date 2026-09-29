package tui

import (
	"context"
	"testing"
)

// TestClientTransportStatusUsesPeerConnectedCallback covers §7: host/join's
// clientTransport reports PeerConnected from the mode-specific callback the
// caller wires in (e.g. *bridge.Server.WorkerConnected for the host) rather
// than its own Client.Connected(), which only ever answers "is my own
// connection up" — not "is the other role connected".
func TestClientTransportStatusUsesPeerConnectedCallback(t *testing.T) {
	_, client := newTUITestClient(t)
	peerUp := false
	transport := newClientTransport(client, func() bool { return peerUp })

	status, err := transport.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.PeerConnected {
		t.Fatal("expected PeerConnected=false from the callback while it reports false, regardless of the client's own connection")
	}

	peerUp = true
	status, err = transport.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.PeerConnected {
		t.Fatal("expected PeerConnected=true once the callback flips")
	}
	if status.Cwd == "" {
		t.Fatal("expected a non-empty process cwd for host/join")
	}
}

// TestClientTransportStatusFallsBackWithoutCallback covers the nil case
// (join has nothing better to wire): it falls back to the client's own
// Connected().
func TestClientTransportStatusFallsBackWithoutCallback(t *testing.T) {
	_, client := newTUITestClient(t)
	transport := newClientTransport(client, nil)
	status, err := transport.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.PeerConnected != client.Connected() {
		t.Fatalf("expected fallback to Client.Connected()=%v, got %v", client.Connected(), status.PeerConnected)
	}
}
