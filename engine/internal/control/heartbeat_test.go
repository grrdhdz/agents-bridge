package control

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

func TestHeartbeatMarksToolAndTouchesExecutorActivity(t *testing.T) {
	h := newLocalHarness(t)
	activity := NewActivity()
	h.workerEndpoint.activity = activity
	before := activity.LastActivity()
	time.Sleep(time.Millisecond)
	res, data := controlHTTP(t, h.workerEndpoint, http.MethodPost, "/v1/heartbeat", `{"tool":"Bash"}`, h.workerEndpoint.capability)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", res.StatusCode, data)
	}
	if !activity.LastActivity().After(before) {
		t.Fatal("executor heartbeat did not count as activity")
	}
	last := activity.LastActivity()
	time.Sleep(time.Millisecond)
	if !activity.LastActivity().Equal(last) {
		t.Fatal("heartbeat created permanent presence")
	}
	role := healthRoles(t, h.workerEndpoint)["executor"]
	if role["state"] != "trabajando" || role["tool"] != "Bash" || role["last_heartbeat_at"] == nil {
		t.Fatalf("heartbeat state: %+v", role)
	}
	for _, test := range []struct {
		method, body, capability string
		want                     int
	}{{http.MethodGet, "", h.workerEndpoint.capability, 400}, {http.MethodPost, `{"tool":"x"}`, "wrong", 401}, {http.MethodPost, `{"tool":`, h.workerEndpoint.capability, 400}, {http.MethodPost, `{"tool":"x"} {}`, h.workerEndpoint.capability, 400}} {
		res, _ := controlHTTP(t, h.workerEndpoint, test.method, "/v1/heartbeat", test.body, test.capability)
		if res.StatusCode != test.want {
			t.Fatalf("validation: %d want %d", res.StatusCode, test.want)
		}
	}
}
func TestHeartbeatStateExpiresAndWaitTakesPrecedence(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	roles := NewRoles(func() time.Time { return now }, protocol.RoleExecutor)
	roles.Heartbeat(protocol.RoleExecutor, "shell")
	snap := roles.Snapshot()[protocol.RoleExecutor]
	if snap.State != StateWorking || snap.Tool != "shell" || snap.LastHeartbeatAt == nil {
		t.Fatalf("snapshot: %+v", snap)
	}
	roles.WaitStart(protocol.RoleExecutor)
	if snap = roles.Snapshot()[protocol.RoleExecutor]; snap.State != StateWaiting || snap.Tool != "" {
		t.Fatalf("wait precedence: %+v", snap)
	}
	roles.WaitEnd(protocol.RoleExecutor)
	now = now.Add(roleQuietAfter + time.Second)
	if snap = roles.Snapshot()[protocol.RoleExecutor]; snap.State != StateQuiet || snap.Tool != "" {
		t.Fatalf("heartbeat never expires: %+v", snap)
	}
}
func TestFINIsRecordedOnlyAfterWaitConsumesPeerFIN(t *testing.T) {
	h := newLocalHarness(t)
	_, peek := peekJSON(t, h.workerEndpoint)
	if peek["fin_received"] != false {
		t.Fatalf("initial FIN: %+v", peek)
	}
	if _, err := h.owner.PublishWithID("close", "FIN\ntermina"); err != nil {
		t.Fatal(err)
	}
	waitPeerUnread(t, h.workerEndpoint, 1)
	_, peek = peekJSON(t, h.workerEndpoint)
	if peek["fin_received"] != false {
		t.Fatal("peek consumed FIN")
	}
	if body := messageBody(t, postWait(context.Background(), h.workerEndpoint, 1000)); body != "FIN\ntermina" {
		t.Fatal(body)
	}
	deadline := time.Now().Add(time.Second)
	for {
		_, peek = peekJSON(t, h.workerEndpoint)
		if peek["fin_received"] == true {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("FIN not recorded: %+v", peek)
		}
		time.Sleep(time.Millisecond)
	}
	_, data := controlHTTP(t, h.workerEndpoint, http.MethodGet, "/v1/health", "", h.workerEndpoint.capability)
	var health map[string]any
	_ = json.Unmarshal(data, &health)
	if health["fin_received"] != true {
		t.Fatalf("health lost FIN: %+v", health)
	}
	_, peek = peekJSON(t, h.ownerEndpoint)
	if peek["fin_received"] != false {
		t.Fatal("FIN leaked to other role")
	}
}

func TestHookBindingVisibleAndIndependentSessions(t *testing.T) {
	h := newLocalHarness(t)
	for _, body := range []string{`{"tool":"Bash","hook_session":"one","hook_bound":true}`, `{"tool":"shell","hook_session":"two","hook_bound":true}`, `{"hook_session":"one","hook_bound":false}`} {
		res, _ := controlHTTP(t, h.workerEndpoint, http.MethodPost, "/v1/heartbeat", body, h.workerEndpoint.capability)
		if res.StatusCode != 200 {
			t.Fatal(res.StatusCode)
		}
	}
	if role := healthRoles(t, h.workerEndpoint)["executor"]; role["hook_bound"] != true {
		t.Fatal("hook binding missing", role)
	}
	controlHTTP(t, h.workerEndpoint, http.MethodPost, "/v1/heartbeat", `{"hook_session":"two","hook_bound":false}`, h.workerEndpoint.capability)
	if role := healthRoles(t, h.workerEndpoint)["executor"]; role["hook_bound"] != false {
		t.Fatal("binding not cleared", role)
	}
}
