package api

import (
	"strings"
	"testing"
)

func TestRenameShowsInListAndValidates(t *testing.T) {
	h := realBridge(t)
	secrets := []string{h.endpoint.Descriptor().Capability, h.workerEndpoint.Descriptor().Capability, h.endpoint.Descriptor().ControlURL}
	s := startSession(t, Options{Version: "test", Root: h.root})
	id := h.server.InstanceID()
	r := s.request(t, "rename", "rename", map[string]any{"instance_id": id, "name": "  Pagos  "})
	if r["ok"] != true || r["result"].(map[string]any)["name"] != "Pagos" {
		t.Fatal(r)
	}
	r = s.request(t, "list", "list", map[string]any{})
	instances := r["result"].(map[string]any)["instances"].([]any)
	if len(instances) != 1 || instances[0].(map[string]any)["name"] != "Pagos" {
		t.Fatal("name missing from list", r)
	}
	for _, args := range []map[string]any{{"instance_id": id}, {"instance_id": id, "name": "a\nb"}, {"instance_id": id, "name": strings.Repeat("x", 65)}} {
		if r = s.request(t, "bad", "rename", args); r["ok"] != false {
			t.Fatal("accepted invalid rename", args, r)
		}
	}
	r = s.request(t, "reset", "rename", map[string]any{"instance_id": id, "name": ""})
	if r["ok"] != true {
		t.Fatal(r)
	}
	r = s.request(t, "list2", "list", map[string]any{})
	if _, ok := r["result"].(map[string]any)["instances"].([]any)[0].(map[string]any)["name"]; ok {
		t.Fatal("reset kept a name", r)
	}
	for _, secret := range secrets {
		if strings.Contains(s.raw.String(), secret) {
			t.Fatal("private metadata leaked")
		}
	}
}
