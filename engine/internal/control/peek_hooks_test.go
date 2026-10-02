package control

import (
	"net/http"
	"testing"
)

func TestPeekProvidesUnreadSummariesWithoutConsuming(t *testing.T) {
	h := newLocalHarness(t)
	if _, err := h.owner.PublishWithID("alert", "URGENTE\nrevisa el cambio"); err != nil {
		t.Fatal(err)
	}
	waitPeerUnread(t, h.workerEndpoint, 1)
	code, peek := peekJSON(t, h.workerEndpoint)
	if code != http.StatusOK {
		t.Fatal(code)
	}
	messages, ok := peek["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("missing summaries: %+v", peek)
	}
	m := messages[0].(map[string]any)
	if m["label"] != "URGENTE" || m["sender_role"] != "orchestrator" || m["preview"] != "revisa el cambio" || m["event_seq"].(float64) <= 0 {
		t.Fatalf("summary: %+v", m)
	}
	_, again := peekJSON(t, h.workerEndpoint)
	if again["unread"] != float64(1) {
		t.Fatal("summary consumed message")
	}
}
