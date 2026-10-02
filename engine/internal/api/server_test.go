package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

type harness struct {
	server                   *bridge.Server
	owner, worker            *bridge.Client
	endpoint, workerEndpoint *control.Endpoint
	root                     string
}

func realBridge(t *testing.T) harness {
	t.Helper()
	s, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	owner, _, err := bridge.Dial(context.Background(), s.Addr().String(), s.InstanceID(), protocol.RoleOrchestrator, s.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	worker, _, err := bridge.Dial(context.Background(), s.Addr().String(), s.InstanceID(), protocol.RoleExecutor, s.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	root := filepath.Join(t.TempDir(), "instances")
	roles := control.NewRoles(nil, protocol.RoleOrchestrator, protocol.RoleExecutor)
	e, err := control.Start(owner, control.Options{Root: root, Role: protocol.RoleOrchestrator, Mode: control.ModeLocal, Roles: roles, CanStop: true, Stop: s.Close, PeerConnected: worker.Connected})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	w, err := control.Start(worker, control.Options{Root: root, Role: protocol.RoleExecutor, Mode: control.ModeLocal, Roles: roles})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return harness{s, owner, worker, e, w, root}
}

type session struct {
	in     *io.PipeWriter
	out    *json.Decoder
	done   chan error
	raw    bytes.Buffer
	events []map[string]any
}

func startSession(t *testing.T, o Options) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{in: inW, done: make(chan error, 1)}
	s.out = json.NewDecoder(io.TeeReader(outR, &s.raw))
	go func() { s.done <- New(o).Run(context.Background(), inR, outW); outW.Close() }()
	t.Cleanup(func() {
		inW.Close()
		outR.Close()
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			t.Error("API did not close")
		}
	})
	return s
}
func (s *session) request(t *testing.T, id, op string, args any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"v": 1, "id": id, "op": op, "args": args})
	s.in.Write(append(raw, '\n'))
	for {
		r := s.next(t)
		if r["id"] == id {
			return r
		}
	}
}
func (s *session) next(t *testing.T) map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 1)
	go func() {
		var r map[string]any
		if err := s.out.Decode(&r); err != nil {
			ch <- map[string]any{"decode_error": err.Error()}
			return
		}
		ch <- r
	}()
	select {
	case r := <-ch:
		if r["decode_error"] != nil {
			t.Fatal(r)
		}
		validateOutput(t, r)
		if r["sub"] != nil {
			s.events = append(s.events, r)
		}
		return r
	case <-time.After(4 * time.Second):
		t.Fatal("API output timeout")
		return nil
	}
}
func TestHelloErrorsAndEOF(t *testing.T) {
	var out bytes.Buffer
	requests := `{"v":1,"id":"hello","op":"hello","args":{}}
{"v":2,"id":"version","op":"hello","args":{}}
{"v":1,"id":"unknown","op":"missing","args":{}}
{broken
{"v":1,"id":"args","op":"send","args":{"instance_id":"none","body":"hi","source":"agent-control"}}
`
	if err := New(Options{Version: "test-engine", Root: filepath.Join(t.TempDir(), "instances")}).Run(context.Background(), strings.NewReader(requests), &out); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&out)
	for n := 0; n < 5; n++ {
		var r map[string]any
		if err := dec.Decode(&r); err != nil {
			t.Fatal(err)
		}
		validateOutput(t, r)
		if n == 0 {
			if r["ok"] != true || r["result"].(map[string]any)["engine_version"] != "test-engine" {
				t.Fatal(r)
			}
		} else if r["ok"] != false {
			t.Fatal(r)
		}
	}
}
func TestRealOperationsReplayNoAckUnsubscribeAndPrivacy(t *testing.T) {
	h := realBridge(t)
	// Close borra los tokens; conserva los originales para comprobar toda la sesión.
	secrets := []string{h.endpoint.Descriptor().Capability, h.workerEndpoint.Descriptor().Capability, h.server.OwnerToken(), h.server.JoinToken(), h.endpoint.Descriptor().ControlURL, h.root, "control_url", "capability"}
	s := startSession(t, Options{Version: "test", Root: h.root})
	id := h.server.InstanceID()
	args := map[string]any{"instance_id": id}
	if _, err := h.worker.PublishWithID("prior", "RESULTADO\nantes de observar"); err != nil {
		t.Fatal(err)
	}
	r := s.request(t, "list", "list", map[string]any{})
	if r["ok"] != true || len(r["result"].(map[string]any)["instances"].([]any)) != 1 {
		t.Fatal(r)
	}
	r = s.request(t, "health", "health", args)
	if r["ok"] != true {
		t.Fatal(r)
	}
	r = s.request(t, "sub", "subscribe", args)
	sub := r["result"].(map[string]any)["sub"].(string)
	seq := float64(0)
	found := false
	for n := 0; n < 30 && !found; n++ {
		e := s.next(t)
		if e["sub"] != sub {
			t.Fatal(e)
		}
		d := e["data"].(map[string]any)
		if v, ok := d["event_seq"].(float64); ok {
			if v <= seq {
				t.Fatalf("unordered %v <= %v", v, seq)
			}
			seq = v
		}
		if m, ok := d["message"].(map[string]any); ok && m["message_id"] == "prior" {
			found = true
		}
	}
	if !found {
		t.Fatal("no replay")
	}
	res, err := control.Do(context.Background(), h.endpoint.Descriptor(), http.MethodGet, "/v1/peek", nil)
	if err != nil {
		t.Fatal(err)
	}
	var peek struct{ Unread int }
	json.NewDecoder(res.Body).Decode(&peek)
	res.Body.Close()
	if peek.Unread != 1 {
		t.Fatal("observer consumed", peek)
	}
	r = s.request(t, "send", "send", map[string]any{"instance_id": id, "label": "URGENTE", "body": "intervención"})
	if r["ok"] != true {
		t.Fatal(r)
	}
	sentID := r["result"].(map[string]any)["message_id"]
	res, err = control.Do(context.Background(), h.workerEndpoint.Descriptor(), http.MethodPost, "/v1/wait?timeout_ms=2000", nil)
	if err != nil {
		t.Fatal(err)
	}
	var wait struct{ Message *protocol.Envelope }
	json.NewDecoder(res.Body).Decode(&wait)
	res.Body.Close()
	if wait.Message == nil || wait.Message.Source != protocol.SourceHumanOperator || wait.Message.SenderRole != protocol.RoleOrchestrator || wait.Message.Body != "URGENTE\nintervención" {
		t.Fatal(wait)
	}
	hasDelivery := func(status string) bool {
		for _, e := range s.events {
			d := e["data"].(map[string]any)
			if e["event"] == "delivery" && d["message_id"] == sentID && d["status"] == status {
				return true
			}
		}
		return false
	}
	for n := 0; n < 30 && !hasDelivery("delivered"); n++ {
		s.next(t)
	}
	if !hasDelivery("accepted") || !hasDelivery("delivered") {
		t.Fatal("missing delivery events")
	}
	last := float64(0)
	for _, e := range s.events {
		if seq, ok := e["data"].(map[string]any)["event_seq"].(float64); ok {
			if seq <= last {
				t.Fatal("events out of order", seq, last)
			}
			last = seq
		}
	}
	r = s.request(t, "unsub", "unsubscribe", map[string]any{"sub": sub})
	if r["ok"] != true {
		t.Fatal(r)
	}
	eventCount := len(s.events)
	if _, err := h.worker.PublishWithID("after-unsubscribe", "RESULTADO\nsin observador"); err != nil {
		t.Fatal(err)
	}
	r = s.request(t, "missing", "health", map[string]any{"instance_id": "missing"})
	if r["ok"] != false {
		t.Fatal(r)
	}
	if len(s.events) != eventCount {
		t.Fatal("event after unsubscribe response")
	}
	r = s.request(t, "stop", "stop", args)
	if r["ok"] != true {
		t.Fatal(r)
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(s.raw.String(), secret) {
			t.Fatal("private metadata leaked")
		}
	}
}
func TestEOFWithActiveSubscriptionDoesNotStopBridge(t *testing.T) {
	h := realBridge(t)
	s := startSession(t, Options{Root: h.root, Version: "test"})
	s.request(t, "sub", "subscribe", map[string]any{"instance_id": h.server.InstanceID()})
	s.in.Close()
	select {
	case err := <-s.done:
		if err != nil {
			t.Fatal(err)
		}
		s.done <- nil
	case <-time.After(3 * time.Second):
		t.Fatal("EOF did not cancel watch")
	}
	res, err := control.Do(context.Background(), h.endpoint.Descriptor(), http.MethodGet, "/v1/health", nil)
	if err != nil {
		t.Fatal("EOF stopped bridge", err)
	}
	res.Body.Close()
}

func TestExportLiveDeliveryAndPrivateBodies(t *testing.T) {
	h := realBridge(t)
	s := startSession(t, Options{Root: h.root, Version: "test"})
	id := h.server.InstanceID()
	body := "RESULTADO\n" + h.endpoint.Descriptor().Capability + " " + h.server.OwnerToken() + " " + h.endpoint.Descriptor().ControlURL + " " + h.root
	if _, err := h.worker.PublishWithID("private-body", body); err != nil {
		t.Fatal(err)
	}
	r := s.request(t, "sub", "subscribe", map[string]any{"instance_id": id})
	sub := r["result"].(map[string]any)["sub"]
	for {
		e := s.next(t)
		if e["event"] == "message" {
			m := e["data"].(map[string]any)["message"].(map[string]any)
			if m["message_id"] == "private-body" {
				if strings.Contains(m["body"].(string), h.server.OwnerToken()) {
					t.Fatal("body credentials leaked")
				}
				break
			}
		}
	}
	for _, format := range []string{"md", "jsonl"} {
		output := filepath.Join(t.TempDir(), "chat."+format)
		r = s.request(t, "export-"+format, "export", map[string]any{"instance_id": id, "format": format, "output": output})
		if r["ok"] != true {
			t.Fatal(r)
		}
		raw, err := os.ReadFile(output)
		if err != nil || !bytes.Contains(raw, []byte("RESULTADO")) {
			t.Fatal(err, string(raw))
		}
		for _, secret := range []string{h.endpoint.Descriptor().Capability, h.server.OwnerToken(), h.endpoint.Descriptor().ControlURL, h.root} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("export leaked private data")
			}
		}
		if err = control.VerifyOwnerOnly(output); err != nil {
			t.Fatal(err)
		}
		r = s.request(t, "duplicate-"+format, "export", map[string]any{"instance_id": id, "format": format, "output": output})
		if r["ok"] != false {
			t.Fatal("export overwrote", r)
		}
	}
	if _, err := h.worker.PublishWithID("live", "RESULTADO\nen vivo"); err != nil {
		t.Fatal(err)
	}
	found := false
	for n := 0; n < 40 && !found; n++ {
		e := s.next(t)
		if e["sub"] != sub {
			t.Fatal(e)
		}
		if e["event"] == "message" {
			m := e["data"].(map[string]any)["message"].(map[string]any)
			found = m["message_id"] == "live"
		}
	}
	if !found {
		t.Fatal("no live message")
	}
	s.request(t, "unsub", "unsubscribe", map[string]any{"sub": sub})
	for _, secret := range []string{h.endpoint.Descriptor().Capability, h.workerEndpoint.Descriptor().Capability, h.server.OwnerToken(), h.endpoint.Descriptor().ControlURL, h.root} {
		if strings.Contains(s.raw.String(), secret) {
			t.Fatal("session leaked credential or private path")
		}
	}
}
func TestCreateTimeoutValidationAndHookState(t *testing.T) {
	h := realBridge(t)
	called := 0
	s := startSession(t, Options{Root: h.root, Version: "test", CreateLocal: func(ctx context.Context, idle string) (string, error) {
		called++
		if idle != "20s" {
			t.Fatal(idle)
		}
		return h.server.InstanceID(), nil
	}})
	r := s.request(t, "create", "create_local", map[string]any{"idle_timeout": "20s"})
	if r["ok"] != true || called != 1 {
		t.Fatal(r, called)
	}
	for _, value := range []string{"-1s", "nonsense"} {
		if s.request(t, "bad-"+value, "create_local", map[string]any{"idle_timeout": value})["ok"] != false {
			t.Fatal("invalid timeout accepted")
		}
	}
	raw := strings.NewReader(`{"tool":"Bash","hook_session":"test","hook_bound":true}`)
	res, err := control.Do(context.Background(), h.workerEndpoint.Descriptor(), http.MethodPost, "/v1/heartbeat", raw)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	r = s.request(t, "list", "list", map[string]any{})
	role := r["result"].(map[string]any)["instances"].([]any)[0].(map[string]any)["role_states"].(map[string]any)["executor"].(map[string]any)
	if role["hook_bound"] != true || role["tool"] != "Bash" || role["last_heartbeat_at"] == nil {
		t.Fatal(role)
	}
}
func TestFallbackToExecutorEndpoint(t *testing.T) {
	h := realBridge(t)
	h.endpoint.Close()
	s := startSession(t, Options{Root: h.root, Version: "test"})
	r := s.request(t, "send", "send", map[string]any{"instance_id": h.server.InstanceID(), "body": "solo ejecutor"})
	if r["ok"] != true || r["result"].(map[string]any)["role"] != "executor" {
		t.Fatal(r)
	}
}
