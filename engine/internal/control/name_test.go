package control

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

// newSharedLocalHarness mirrors `local`: both endpoints share one Roles.
func newSharedLocalHarness(t *testing.T) (owner, worker *Endpoint, root string) {
	t.Helper()
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	oc, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(oc.Close)
	wc, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleExecutor, server.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wc.Close)
	root = privateRoot(t)
	roles := NewRoles(nil, protocol.RoleOrchestrator, protocol.RoleExecutor)
	owner, err = Start(oc, Options{Role: protocol.RoleOrchestrator, Mode: ModeLocal, CWD: "/repo", Root: root, Roles: roles})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	worker, err = Start(wc, Options{Role: protocol.RoleExecutor, Mode: ModeLocal, CWD: "/repo", Root: root, Roles: roles})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	return owner, worker, root
}

func TestRenameUpdatesBothLocalDescriptorsAndValidates(t *testing.T) {
	owner, worker, root := newSharedLocalHarness(t)
	res, data := controlHTTP(t, worker, http.MethodPost, "/v1/name", `{"name":"  Pagos  API  "}`, worker.capability)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("rename: %d %s", res.StatusCode, data)
	}
	var body struct {
		OK   bool   `json:"ok"`
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &body) != nil || !body.OK || body.Name != "Pagos  API" {
		t.Fatalf("response: %s", data)
	}
	if strings.Contains(string(data), worker.capability) {
		t.Fatal("rename response leaked the capability")
	}
	descriptors, err := ListDescriptors(root)
	if err != nil || len(descriptors) != 2 {
		t.Fatal(descriptors, err)
	}
	for _, d := range descriptors {
		if d.Name != "Pagos  API" {
			t.Fatalf("%s descriptor name = %q", d.LocalRole, d.Name)
		}
	}
	// The periodic descriptor refresh must keep the name.
	owner.heartbeatOnce()
	if d, _ := readDescriptor(owner.descriptorPath); d.Name != "Pagos  API" {
		t.Fatalf("heartbeat dropped name: %q", d.Name)
	}
	res, _ = controlHTTP(t, owner, http.MethodPost, "/v1/name", `{"name":""}`, owner.capability)
	if res.StatusCode != http.StatusOK {
		t.Fatal("reset rejected")
	}
	if d, _ := readDescriptor(worker.descriptorPath); d.Name != "" {
		t.Fatalf("reset kept %q", d.Name)
	}
	for _, test := range []struct {
		method, body, capability string
		want                     int
	}{
		{http.MethodGet, "", owner.capability, 400},
		{http.MethodPost, `{"name":"x"}`, "wrong", 401},
		{http.MethodPost, `{"name":"a\nb"}`, owner.capability, 400},
		{http.MethodPost, `{"name":"` + strings.Repeat("ñ", 65) + `"}`, owner.capability, 400},
		{http.MethodPost, `{"name":"x"} {}`, owner.capability, 400},
		{http.MethodPost, `{}`, owner.capability, 400},
	} {
		res, _ := controlHTTP(t, owner, test.method, "/v1/name", test.body, test.capability)
		if res.StatusCode != test.want {
			t.Fatalf("validation %q: %d want %d", test.body, res.StatusCode, test.want)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{"  a ": "a", "": "", strings.Repeat("ñ", 64): strings.Repeat("ñ", 64)} {
		if got, err := NormalizeName(in); err != nil || got != want {
			t.Fatalf("%q => %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"a\tb", "\x7f", strings.Repeat("a", 65), "\xff"} {
		if _, err := NormalizeName(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
