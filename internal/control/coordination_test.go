package control

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/internal/bridge"
	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

// sendJSON posts one /v1/send with the given extra fields through the
// endpoint's real HTTP surface.
func sendJSON(t *testing.T, endpoint *Endpoint, messageID, body string, extra map[string]any) (int, map[string]any) {
	t.Helper()
	payload := map[string]any{"v": 1, "message_id": messageID, "body": body}
	for k, v := range extra {
		payload[k] = v
	}
	raw, _ := json.Marshal(payload)
	response, data := controlHTTP(t, endpoint, http.MethodPost, "/v1/send", string(raw), endpoint.capability)
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("send response is not JSON: %q", data)
	}
	return response.StatusCode, record
}

func peekJSON(t *testing.T, endpoint *Endpoint) (int, map[string]any) {
	t.Helper()
	response, data := controlHTTP(t, endpoint, http.MethodGet, "/v1/peek", "", endpoint.capability)
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("peek response is not JSON: %q", data)
	}
	return response.StatusCode, record
}

func healthRoles(t *testing.T, endpoint *Endpoint) map[string]map[string]any {
	t.Helper()
	_, data := controlHTTP(t, endpoint, http.MethodGet, "/v1/health", "", endpoint.capability)
	var record struct {
		Roles map[string]map[string]any `json:"roles"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record.Roles
}

// waitPeerUnread blocks until the endpoint's own journal holds n messages
// from the other role (the relay through the server is asynchronous).
func waitPeerUnread(t *testing.T, endpoint *Endpoint, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events, _, _, _ := endpoint.client.ReadEvents(0, 1000)
		count := 0
		for _, event := range events {
			if endpoint.isPeerMessage(event) {
				count++
			}
		}
		if count >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("peer messages never reached %d", n)
}

func ownMessageCount(endpoint *Endpoint) int {
	events, _, _, _ := endpoint.client.ReadEvents(0, 1000)
	count := 0
	for _, event := range events {
		if event.Kind == bridge.EventMessage && event.Envelope != nil && event.Envelope.SenderRole == endpoint.client.Role() {
			count++
		}
	}
	return count
}

func TestSendGuardBlocksWithUnreadPeerMessages(t *testing.T) {
	h := newLocalHarness(t)
	if _, err := h.owner.PublishWithID("r1", "RESPUESTA\nusa otro diseño"); err != nil {
		t.Fatal(err)
	}
	waitPeerUnread(t, h.workerEndpoint, 1)
	status, record := sendJSON(t, h.workerEndpoint, "res-1", "RESULTADO\nhecho", map[string]any{"require_inbox_empty": true})
	if status != http.StatusConflict || record["code"] != "INBOX_NOT_EMPTY" {
		t.Fatalf("expected 409 INBOX_NOT_EMPTY, got %d %+v", status, record)
	}
	if record["unread"] != float64(1) {
		t.Fatalf("unread = %v, want 1", record["unread"])
	}
	if msg, _ := record["message"].(string); !strings.Contains(msg, "RESPUESTA") {
		t.Fatalf("message should name the latest label: %q", msg)
	}
	if n := ownMessageCount(h.workerEndpoint); n != 0 {
		t.Fatalf("a blocked send published %d message(s)", n)
	}
}

func TestSendGuardPassesWhenInboxEmptyOrAlreadyRead(t *testing.T) {
	h := newLocalHarness(t)
	if status, record := sendJSON(t, h.workerEndpoint, "a", "PROGRESO\nva bien", map[string]any{"require_inbox_empty": true}); status != http.StatusAccepted {
		t.Fatalf("empty inbox must not block: %d %+v", status, record)
	}
	if _, err := h.owner.PublishWithID("t1", "TAREA\nx"); err != nil {
		t.Fatal(err)
	}
	waitPeerUnread(t, h.workerEndpoint, 1)
	if body := messageBody(t, postWait(context.Background(), h.workerEndpoint, 2000)); body != "TAREA\nx" {
		t.Fatalf("unexpected body %q", body)
	}
	if status, record := sendJSON(t, h.workerEndpoint, "b", "RESULTADO\nlisto", map[string]any{"require_inbox_empty": true}); status != http.StatusAccepted {
		t.Fatalf("read inbox must not block: %d %+v", status, record)
	}
}

func TestSendGuardIsOffWhenFieldAbsentOrFalse(t *testing.T) {
	h := newLocalHarness(t)
	if _, err := h.owner.PublishWithID("r1", "RESPUESTA\nx"); err != nil {
		t.Fatal(err)
	}
	waitPeerUnread(t, h.workerEndpoint, 1)
	if status, record := sendJSON(t, h.workerEndpoint, "old-client", "RESULTADO\na", nil); status != http.StatusAccepted {
		t.Fatalf("absent field must keep the old behavior: %d %+v", status, record)
	}
	if status, record := sendJSON(t, h.workerEndpoint, "forced", "RESULTADO\nb", map[string]any{"require_inbox_empty": false}); status != http.StatusAccepted {
		t.Fatalf("--force (false) must publish: %d %+v", status, record)
	}
}

func TestSendGuardNeverBlocksHumanOperator(t *testing.T) {
	h := newLocalHarness(t)
	if _, err := h.owner.PublishWithID("r1", "URGENTE\nalto"); err != nil {
		t.Fatal(err)
	}
	waitPeerUnread(t, h.workerEndpoint, 1)
	status, record := sendJSON(t, h.workerEndpoint, "human", "hola", map[string]any{"require_inbox_empty": true, "source": protocol.SourceHumanOperator})
	if status != http.StatusAccepted {
		t.Fatalf("human-operator must never be blocked: %d %+v", status, record)
	}
}

func TestSendGuardIgnoresOwnMessages(t *testing.T) {
	h := newLocalHarness(t)
	if _, err := h.worker.PublishWithID("own", "PROGRESO\nx"); err != nil {
		t.Fatal(err)
	}
	if status, record := sendJSON(t, h.workerEndpoint, "next", "RESULTADO\ny", map[string]any{"require_inbox_empty": true}); status != http.StatusAccepted {
		t.Fatalf("own messages are not unread: %d %+v", status, record)
	}
}

// TestGuardedSendSerializesWithConsumptionCursor pins the design: the unread
// check and the publication run under the same lock that guards the wait
// cursor, so a consuming wait can never interleave between them.
func TestGuardedSendSerializesWithConsumptionCursor(t *testing.T) {
	h := newLocalHarness(t)
	h.workerEndpoint.cursorMu.Lock()
	done := make(chan int, 1)
	go func() {
		status, _ := sendJSON(t, h.workerEndpoint, "locked", "RESULTADO\nz", map[string]any{"require_inbox_empty": true})
		done <- status
	}()
	select {
	case <-done:
		t.Fatal("guarded send completed while the cursor lock was held")
	case <-time.After(150 * time.Millisecond):
	}
	h.workerEndpoint.cursorMu.Unlock()
	select {
	case status := <-done:
		if status != http.StatusAccepted {
			t.Fatalf("status = %d", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("guarded send never completed")
	}
}

// TestGuardedSendsRaceWithWaitsAndPeerTraffic hammers the guard, wait and the
// peer's publications concurrently; -race is the assertion, plus the
// invariant that every accepted guarded send saw an empty inbox at its
// moment: the executor only ever sends after fully consuming.
func TestGuardedSendsRaceWithWaitsAndPeerTraffic(t *testing.T) {
	h := newLocalHarness(t)
	const rounds = 20
	var wg sync.WaitGroup
	var blocked, accepted atomic.Int32
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			_, _ = h.owner.Publish("TAREA\nn")
			time.Sleep(2 * time.Millisecond)
		}
		close(stop)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			postWait(context.Background(), h.workerEndpoint, 20)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			i++
			status, _ := sendJSON(t, h.workerEndpoint, "g"+string(rune('a'+i%26))+strings.Repeat("x", i/26), "PROGRESO\np", map[string]any{"require_inbox_empty": true})
			switch status {
			case http.StatusAccepted:
				accepted.Add(1)
			case http.StatusConflict:
				blocked.Add(1)
			default:
				t.Errorf("unexpected status %d", status)
				return
			}
		}
	}()
	wg.Wait()
	if accepted.Load()+blocked.Load() == 0 {
		t.Fatal("no send ran")
	}
}

func TestPeekReportsUnreadWithoutConsuming(t *testing.T) {
	h := newLocalHarness(t)
	status, record := peekJSON(t, h.workerEndpoint)
	if status != http.StatusOK || record["unread"] != float64(0) || record["urgent"] != false {
		t.Fatalf("empty peek = %d %+v", status, record)
	}
	if _, err := h.owner.PublishWithID("t1", "TAREA\nuno"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.owner.PublishWithID("t2", "PREGUNTA\ndos"); err != nil {
		t.Fatal(err)
	}
	waitPeerUnread(t, h.workerEndpoint, 2)
	for i := 0; i < 2; i++ { // peek is repeatable: it never moves the cursor
		_, record = peekJSON(t, h.workerEndpoint)
		if record["unread"] != float64(2) || record["urgent"] != false || record["latest_label"] != "PREGUNTA" || record["latest_message_id"] != "t2" {
			t.Fatalf("peek #%d = %+v", i, record)
		}
	}
	if body := messageBody(t, postWait(context.Background(), h.workerEndpoint, 2000)); body != "TAREA\nuno" {
		t.Fatalf("wait after peek lost the first message: %q", body)
	}
	if body := messageBody(t, postWait(context.Background(), h.workerEndpoint, 2000)); body != "PREGUNTA\ndos" {
		t.Fatalf("wait after peek lost the second message: %q", body)
	}
	_, record = peekJSON(t, h.workerEndpoint)
	if record["unread"] != float64(0) {
		t.Fatalf("after consuming both, unread = %v", record["unread"])
	}
}

func TestPeekFlagsUrgentAnywhereInUnread(t *testing.T) {
	h := newLocalHarness(t)
	for i, body := range []string{"TAREA\na", "URGENTE\ndetente", "PROGRESO\nb"} {
		if _, err := h.owner.PublishWithID("m"+string(rune('0'+i)), body); err != nil {
			t.Fatal(err)
		}
	}
	waitPeerUnread(t, h.workerEndpoint, 3)
	_, record := peekJSON(t, h.workerEndpoint)
	if record["urgent"] != true || record["unread"] != float64(3) || record["latest_label"] != "PROGRESO" {
		t.Fatalf("peek = %+v", record)
	}
}

func TestPeekIsNotPresenceNorWaitState(t *testing.T) {
	h := newLocalHarness(t)
	activity := NewActivity()
	h.ownerEndpoint.activity = activity
	stale := time.Now().Add(-time.Hour)
	activity.mu.Lock()
	activity.lastActivity = stale
	activity.mu.Unlock()
	if status, _ := peekJSON(t, h.ownerEndpoint); status != http.StatusOK {
		t.Fatalf("peek status %d", status)
	}
	if !activity.LastActivity().Equal(stale) {
		t.Fatalf("peek must not count as presence: LastActivity=%v", activity.LastActivity())
	}
	roles := healthRoles(t, h.ownerEndpoint)
	if _, has := roles["orchestrator"]["last_wait_at"]; has {
		t.Fatalf("peek must not register a wait: %+v", roles)
	}
}

func TestPeekRequiresGET(t *testing.T) {
	h := newLocalHarness(t)
	response, _ := controlHTTP(t, h.workerEndpoint, http.MethodPost, "/v1/peek", "", h.workerEndpoint.capability)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /v1/peek = %d", response.StatusCode)
	}
}

// fakeClock is an injectable, goroutine-safe clock for state tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Now()} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestRolesDerivedStates(t *testing.T) {
	clock := newFakeClock()
	roles := NewRoles(clock.Now, protocol.RoleOrchestrator, protocol.RoleExecutor)
	snap := roles.Snapshot()
	if snap[protocol.RoleExecutor].State != "—" {
		t.Fatalf("fresh role should be unknown, got %q", snap[protocol.RoleExecutor].State)
	}
	roles.WaitStart(protocol.RoleExecutor)
	if got := roles.Snapshot()[protocol.RoleExecutor].State; got != "esperando" {
		t.Fatalf("in-flight wait = %q", got)
	}
	clock.Advance(time.Hour) // a wait in flight stays esperando however long
	if got := roles.Snapshot()[protocol.RoleExecutor].State; got != "esperando" {
		t.Fatalf("long wait = %q", got)
	}
	roles.WaitEnd(protocol.RoleExecutor)
	if got := roles.Snapshot()[protocol.RoleExecutor]; got.State != "trabajando" || got.LastWaitAt == nil {
		t.Fatalf("just after wait = %+v", got)
	}
	clock.Advance(14*time.Minute + 59*time.Second)
	if got := roles.Snapshot()[protocol.RoleExecutor].State; got != "trabajando" {
		t.Fatalf("under 15 min = %q", got)
	}
	clock.Advance(2 * time.Second)
	if got := roles.Snapshot()[protocol.RoleExecutor].State; got != "callado" {
		t.Fatalf("over 15 min = %q", got)
	}
	roles.Message(protocol.RoleExecutor, clock.Now())
	got := roles.Snapshot()[protocol.RoleExecutor]
	if got.State != "trabajando" || got.LastMessageAt == nil {
		t.Fatalf("a message is activity: %+v", got)
	}
	// Untouched role: unknown until 15 minutes of silence prove it callado.
	if got := roles.Snapshot()[protocol.RoleOrchestrator].State; got != "callado" {
		t.Fatalf("role silent for over 15 min since tracking = %q", got)
	}
}

func TestRolesTwoWaitsDoNotEndEarlyAndUntrackedIgnored(t *testing.T) {
	clock := newFakeClock()
	roles := NewRoles(clock.Now, protocol.RoleExecutor)
	roles.WaitStart(protocol.RoleExecutor)
	roles.WaitStart(protocol.RoleExecutor)
	roles.WaitEnd(protocol.RoleExecutor)
	if got := roles.Snapshot()[protocol.RoleExecutor].State; got != "esperando" {
		t.Fatalf("one wait still open = %q", got)
	}
	roles.WaitStart(protocol.RoleOrchestrator)
	roles.Message(protocol.RoleOrchestrator, clock.Now())
	if _, ok := roles.Snapshot()[protocol.RoleOrchestrator]; ok {
		t.Fatal("an untracked role must not appear")
	}
}

func startWithRoles(t *testing.T, client *bridge.Client, roles *Roles) *Endpoint {
	t.Helper()
	endpoint, err := Start(client, Options{Role: client.Role(), CWD: "/repo", Root: privateRoot(t), Roles: roles})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(endpoint.Close)
	return endpoint
}

func TestHealthRolesSharedRegistryInLocal(t *testing.T) {
	h := newLocalHarness(t)
	clock := newFakeClock()
	roles := NewRoles(clock.Now, protocol.RoleOrchestrator, protocol.RoleExecutor)
	h.ownerEndpoint.roles = roles
	h.workerEndpoint.roles = roles

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan waitResult, 1)
	go func() { done <- postWait(ctx, h.workerEndpoint, 10000) }()
	waitUntilWaiting(t, h.workerEndpoint)
	// The orchestrator endpoint sees the executor's state through the
	// shared registry.
	got := healthRoles(t, h.ownerEndpoint)
	if got["executor"]["state"] != "esperando" {
		t.Fatalf("executor seen from orchestrator endpoint: %+v", got)
	}
	cancel()
	<-done
	deadline := time.Now().Add(2 * time.Second)
	for h.workerEndpoint.waiting.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got = healthRoles(t, h.ownerEndpoint)
	if got["executor"]["state"] != "trabajando" || got["executor"]["last_wait_at"] == nil {
		t.Fatalf("after wait: %+v", got)
	}
	// A message published by the orchestrator (through any path) shows as
	// its last_message_at from either endpoint.
	if _, err := h.owner.PublishWithID("t1", "TAREA\nx"); err != nil {
		t.Fatal(err)
	}
	waitPeerUnread(t, h.workerEndpoint, 1) // the relay to the other journal is asynchronous
	got = healthRoles(t, h.workerEndpoint)
	if got["orchestrator"]["last_message_at"] == nil || got["orchestrator"]["state"] != "trabajando" {
		t.Fatalf("orchestrator after publishing: %+v", got)
	}
	clock.Advance(16 * time.Minute)
	got = healthRoles(t, h.workerEndpoint)
	if got["orchestrator"]["state"] != "callado" || got["executor"]["state"] != "callado" {
		t.Fatalf("after 16 quiet minutes: %+v", got)
	}
}

func TestHealthRolesHostAndJoinReportOnlyOwnRole(t *testing.T) {
	h := newLocalHarness(t)
	// No injected registry: an endpoint tracks just its own role.
	ownerRoles := healthRoles(t, h.ownerEndpoint)
	if len(ownerRoles) != 1 || ownerRoles["orchestrator"] == nil {
		t.Fatalf("host endpoint should report only orchestrator: %+v", ownerRoles)
	}
	workerRoles := healthRoles(t, h.workerEndpoint)
	if len(workerRoles) != 1 || workerRoles["executor"] == nil {
		t.Fatalf("join endpoint should report only executor: %+v", workerRoles)
	}
	// The peer's traffic never leaks into the own-role-only registry.
	if _, err := h.owner.PublishWithID("t1", "TAREA\nx"); err != nil {
		t.Fatal(err)
	}
	waitPeerUnread(t, h.workerEndpoint, 1)
	workerRoles = healthRoles(t, h.workerEndpoint)
	if len(workerRoles) != 1 || workerRoles["executor"]["last_message_at"] != nil {
		t.Fatalf("executor-only registry recorded the peer: %+v", workerRoles)
	}
}

// TestSendGuardExemptsUrgenteAndFinForBothRoles: interruptions and closings
// must get through even when the sender has unread messages; every other
// label stays blocked.
func TestSendGuardExemptsUrgenteAndFinForBothRoles(t *testing.T) {
	h := newLocalHarness(t)
	if _, err := h.worker.PublishWithID("res", "RESULTADO\nhecho"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.owner.PublishWithID("ask", "PREGUNTA\n¿?"); err != nil {
		t.Fatal(err)
	}
	waitPeerUnread(t, h.ownerEndpoint, 1)
	waitPeerUnread(t, h.workerEndpoint, 1)
	guard := map[string]any{"require_inbox_empty": true}
	for i, endpoint := range []*Endpoint{h.ownerEndpoint, h.workerEndpoint} {
		for _, body := range []string{"TAREA\nx", "RESPUESTA\nx", "RESULTADO\nx", "PREGUNTA\nx", "PROGRESO\nx", "sin etiqueta"} {
			if status, record := sendJSON(t, endpoint, "blk"+string(rune('a'+i))+body[:3], body, guard); status != http.StatusConflict {
				t.Fatalf("%q must stay blocked: %d %+v", body, status, record)
			}
		}
		for _, body := range []string{"URGENTE\ndetente", "FIN"} {
			if status, record := sendJSON(t, endpoint, "ok"+string(rune('a'+i))+body[:3], body, guard); status != http.StatusAccepted {
				t.Fatalf("%q must bypass the guard: %d %+v", body, status, record)
			}
		}
	}
}
