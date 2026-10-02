package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

func TestInboxNotEmptyExitsWithForbiddenCode(t *testing.T) {
	if got := exitCodeFor("INBOX_NOT_EMPTY"); got != 5 {
		t.Fatalf("INBOX_NOT_EMPTY exit = %d, want 5", got)
	}
}

// peekUnread polls ctl peek until the relay delivered n messages.
func (r *localRun) waitUnread(t *testing.T, instanceID, role string, n int) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		code, stdout, stderr := r.ctlID("", instanceID, "peek", "--role", role)
		if code != 0 {
			t.Fatalf("peek failed: %d %s", code, stderr)
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(stdout), &record); err != nil {
			t.Fatalf("peek output is not JSON: %q", stdout)
		}
		if record["unread"] == float64(n) {
			return record
		}
		if time.Now().After(deadline) {
			t.Fatalf("unread never reached %d: %s", n, stdout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCtlSendGuardsInboxByDefaultAndForceOverrides(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	if code, _, stderr := run.ctlID("RESPUESTA\ncambia el diseño", id, "send", "--role", "orchestrator", "--body-file", "-"); code != 0 {
		t.Fatalf("orchestrator send failed: %d %s", code, stderr)
	}
	run.waitUnread(t, id, "executor", 1)

	code, _, stderr := run.ctlID("RESULTADO\nhecho", id, "send", "--role", "executor", "--body-file", "-")
	if code != exitForbidden || !strings.Contains(stderr, "INBOX_NOT_EMPTY") || !strings.Contains(stderr, "RESPUESTA") {
		t.Fatalf("send with unread inbox should exit 5 INBOX_NOT_EMPTY: %d %s", code, stderr)
	}
	// Nothing was published: the orchestrator has nothing to read.
	if code, stdout, _ := run.ctlID("", id, "wait", "--role", "orchestrator", "--timeout", "100ms"); code != 0 || !strings.Contains(stdout, `"status":"timeout"`) {
		t.Fatalf("a blocked send must publish nothing: %d %s", code, stdout)
	}
	if code, _, stderr := run.ctlID("RESULTADO\nforzado", id, "send", "--role", "executor", "--force", "--body-file", "-"); code != 0 {
		t.Fatalf("--force must publish: %d %s", code, stderr)
	}
}

func TestCtlSendAfterWaitPassesGuard(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	run.ctlID("TAREA\nx", id, "send", "--role", "orchestrator", "--body-file", "-")
	if code, _, stderr := run.ctlID("", id, "wait", "--role", "executor", "--timeout", "3s"); code != 0 {
		t.Fatalf("wait failed: %d %s", code, stderr)
	}
	if code, _, stderr := run.ctlID("RESULTADO\nlisto", id, "send", "--role", "executor", "--body-file", "-"); code != 0 {
		t.Fatalf("send after reading must pass the guard: %d %s", code, stderr)
	}
}

func TestCtlPeekDoesNotConsumeAndFormatsText(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	run.ctlID("URGENTE\ndetente", id, "send", "--role", "orchestrator", "--body-file", "-")
	record := run.waitUnread(t, id, "executor", 1)
	if record["urgent"] != true || record["latest_label"] != "URGENTE" || record["latest_message_id"] == "" {
		t.Fatalf("peek record = %+v", record)
	}
	code, stdout, stderr := run.ctlID("", id, "peek", "--role", "executor", "--format", "text")
	want := "--- agents-bridge instance=" + id + " unread=1 urgent=sí latest=URGENTE\n"
	if code != 0 || stdout != want {
		t.Fatalf("text peek = %d %q (%s), want %q", code, stdout, stderr, want)
	}
	code, stdout, _ = run.ctlID("", id, "wait", "--role", "executor", "--timeout", "3s", "--format", "text")
	if code != 0 || !strings.Contains(stdout, "URGENTE\ndetente") {
		t.Fatalf("wait after peek must still receive the message: %d %q", code, stdout)
	}
	_, stdout, _ = run.ctlID("", id, "peek", "--role", "executor", "--format", "text")
	if stdout != "--- agents-bridge instance="+id+" unread=0 urgent=no latest=ninguna\n" {
		t.Fatalf("peek after consuming = %q", stdout)
	}
}

func TestCtlPeekRejectsUnknownFormat(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	if code, _, stderr := run.ctlID("", id, "peek", "--role", "executor", "--format", "xml"); code != exitUsage {
		t.Fatalf("bad peek format = %d %s", code, stderr)
	}
}

func TestFormatRoleCell(t *testing.T) {
	age := int64(125)
	cases := []struct {
		state string
		age   *int64
		want  string
	}{
		{"", nil, "—"},
		{"—", nil, "—"},
		{"esperando", nil, "esperando"},
		{"trabajando", &age, "trabajando 2m"},
		{"callado", &age, "callado 2m"},
	}
	for _, c := range cases {
		if got := formatRoleCell(c.state, c.age); got != c.want {
			t.Errorf("formatRoleCell(%q,%v) = %q, want %q", c.state, c.age, got, c.want)
		}
	}
}

func psRows(t *testing.T, r *localRun) []psRow {
	t.Helper()
	code, stdout, stderr := r.ps("--format", "jsonl")
	if code != 0 {
		t.Fatalf("ps failed: %d %s", code, stderr)
	}
	var record struct {
		Instances []psRow `json:"instances"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &record); err != nil {
		t.Fatal(err)
	}
	return record.Instances
}

func TestPSShowsRoleStatesFromSharedLocalRegistry(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	done := make(chan struct{})
	go func() {
		run.ctlID("", id, "wait", "--role", "executor", "--timeout", "10s")
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	var row psRow
	for {
		rows := psRows(t, run)
		if len(rows) == 1 {
			row = rows[0]
			if row.RoleStates["executor"].State == "esperando" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("executor never showed as esperando: %+v", row)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// One endpoint (the orchestrator's) knows both roles: shared registry.
	if _, ok := row.RoleStates["orchestrator"]; !ok {
		t.Fatalf("local ps should know both roles: %+v", row.RoleStates)
	}
	_, table, _ := run.ps("--format", "table")
	header, rest, _ := strings.Cut(table, "\n")
	fields := strings.Fields(header)
	if len(fields) < 2 || fields[len(fields)-2] != "ORQ" || fields[len(fields)-1] != "EJEC" {
		t.Fatalf("ps table header should end with ORQ EJEC: %q", header)
	}
	if !strings.Contains(rest, "esperando") {
		t.Fatalf("ps table should show the executor esperando: %q", rest)
	}
	run.ctlID("TAREA\nlibera", id, "send", "--role", "orchestrator", "--body-file", "-")
	<-done
}

func TestPSRoleStatesForJoinShowOnlyExecutor(t *testing.T) {
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	worker, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleExecutor, server.JoinToken())
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	root := filepath.Join(t.TempDir(), "instances")
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runJoinHeadless(ctx, worker, writer, root, "", reconnectPolicy{}) }()
	if _, err := bufio.NewReader(reader).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, reader) }()
	defer func() { cancel(); <-done }()

	run := &localRun{root: root}
	rows := psRows(t, run)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if _, ok := rows[0].RoleStates["executor"]; !ok || len(rows[0].RoleStates) != 1 {
		t.Fatalf("join ps must know only the executor: %+v", rows[0].RoleStates)
	}
	_, table, _ := run.ps("--format", "table")
	if !strings.Contains(table, "—") {
		t.Fatalf("the unknown orchestrator cell should be —: %q", table)
	}
}

func exportFile(t *testing.T) string { return filepath.Join(t.TempDir(), "conversacion.out") }

func TestCtlExportMarkdownAndJSONLWithoutConsuming(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	run.ctlID("TAREA\nhaz `algo` ```x```", id, "send", "--role", "orchestrator", "--message-id", "m1", "--body-file", "-")
	run.waitUnread(t, id, "executor", 1)

	md := exportFile(t)
	if code, _, stderr := run.ctlID("", id, "export", "--role", "executor", "--output", md); code != 0 {
		t.Fatalf("export md failed: %d %s", code, stderr)
	}
	data, err := os.ReadFile(md)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{id, "Exportada", "orquestador", "agent-control", "TAREA", "haz `algo` ```x```", "m1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("markdown export lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "truncad") || strings.Contains(text, "expuls") {
		t.Fatalf("nothing was evicted, the export must not say so:\n%s", text)
	}
	// Body sits in a fence longer than any backtick run inside it.
	if !strings.Contains(text, "````\nTAREA") {
		t.Fatalf("body fence must outgrow the body's own backticks:\n%s", text)
	}

	jsonl := exportFile(t)
	if code, _, stderr := run.ctlID("", id, "export", "--role", "executor", "--output", jsonl, "--format", "jsonl"); code != 0 {
		t.Fatalf("export jsonl failed: %d %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(mustRead(t, jsonl)), "\n")
	if len(lines) != 1 {
		t.Fatalf("jsonl export should hold one envelope, got %d lines", len(lines))
	}
	var env protocol.Envelope
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil || env.MessageID != "m1" || env.Body != "TAREA\nhaz `algo` ```x```" {
		t.Fatalf("jsonl line is not the envelope: %v %+v", err, env)
	}

	// Exporting confirms nothing.
	if code, stdout, _ := run.ctlID("", id, "wait", "--role", "executor", "--timeout", "3s", "--format", "text"); code != 0 || !strings.Contains(stdout, "TAREA") {
		t.Fatalf("export must not consume: %d %q", code, stdout)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCtlExportRefusesExistingFileAndIsOwnerOnly(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	path := exportFile(t)
	if code, _, stderr := run.ctlID("", id, "export", "--role", "executor", "--output", path); code != 0 {
		t.Fatalf("export failed: %d %s", code, stderr)
	}
	if err := control.VerifyOwnerOnly(path); err != nil {
		t.Fatalf("export must be owner-only: %v", err)
	}
	before := mustRead(t, path)
	code, _, stderr := run.ctlID("", id, "export", "--role", "executor", "--output", path)
	if code != exitUsage || !strings.Contains(stderr, "USAGE") {
		t.Fatalf("existing output should be a usage error: %d %s", code, stderr)
	}
	if mustRead(t, path) != before {
		t.Fatal("existing file was modified")
	}
}

func TestCtlExportUsageErrors(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	if code, _, stderr := run.ctlID("", id, "export", "--role", "executor"); code != exitUsage || !strings.Contains(stderr, "export requires --output") {
		t.Fatalf("missing --output = %d %s", code, stderr)
	}
	if code, _, stderr := run.ctlID("", id, "export", "--role", "executor", "--output", exportFile(t), "--format", "text"); code != exitUsage || !strings.Contains(stderr, "--format must be md or jsonl") {
		t.Fatalf("bad export format = %d %s", code, stderr)
	}
	missing := filepath.Join(t.TempDir(), "no", "dir", "x")
	if code, _, stderr := run.ctlID("", id, "export", "--role", "executor", "--output", missing); code != exitUsage || !strings.Contains(stderr, "cannot write") {
		t.Fatalf("missing parent dir = %d %s", code, stderr)
	}
}

// TestCtlExportNotesEvictedJournal drives export against a stand-in control
// endpoint whose journal already dropped events 1..49: the first read answers
// CURSOR_EXPIRED, export continues from what remains across two pages, and
// the file says so at the top. (A real journal needs >4096 events to get
// there, and the server caps a bridge at 1000 messages.)
func TestCtlExportNotesEvictedJournal(t *testing.T) {
	var reads []string
	mux := http.NewServeMux()
	page := func(seq uint64, id, body string) map[string]any {
		return map[string]any{"v": 1, "type": "event", "event": "message", "instance_id": "inst", "event_seq": seq, "message_id": id, "message": map[string]any{
			"v": 1, "instance_id": "inst", "message_id": id, "sender_role": "mac-orchestrator", "body": body, "source": "agent-control", "created_at": "2026-09-29T10:00:00Z"}}
	}
	mux.HandleFunc("/v1/read", func(w http.ResponseWriter, r *http.Request) {
		after := r.URL.Query().Get("after_event_seq")
		reads = append(reads, after)
		w.Header().Set("Content-Type", "application/x-ndjson")
		switch after {
		case "0":
			w.WriteHeader(http.StatusGone)
			_, _ = io.WriteString(w, `{"v":1,"type":"error","ok":false,"code":"CURSOR_EXPIRED","message":"expired","oldest_event_seq":50}`+"\n")
		case "49":
			_ = json.NewEncoder(w).Encode(map[string]any{"v": 1, "type": "response", "ok": true, "operation": "read", "events": []any{page(50, "old", "TAREA\nvieja")}, "next_after_event_seq": 50, "has_more": true})
		case "50":
			_ = json.NewEncoder(w).Encode(map[string]any{"v": 1, "type": "response", "ok": true, "operation": "read", "events": []any{page(51, "new", "RESPUESTA\nnueva")}, "next_after_event_seq": 51, "has_more": false})
		default:
			t.Errorf("unexpected cursor %q", after)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	d := control.Descriptor{InstanceID: "inst", ControlURL: srv.URL, Capability: "cap"}
	for _, format := range []string{"md", "jsonl"} {
		out := exportFile(t)
		fixed := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
		err := ctlExport(context.Background(), ctlEnv{stdout: io.Discard, stderr: io.Discard, now: func() time.Time { return fixed }}, d, out, format)
		if err != nil {
			t.Fatalf("export %s: %v", format, err)
		}
		text := mustRead(t, out)
		head := text
		if len(head) > 500 {
			head = head[:500]
		}
		if !strings.Contains(head, "expuls") || !strings.Contains(head, "oldest_event_seq=50") {
			t.Fatalf("%s export must flag the evicted journal at the top:\n%s", format, head)
		}
		if !strings.Contains(text, "vieja") || !strings.Contains(text, "nueva") {
			t.Fatalf("%s export lost retained messages:\n%s", format, text)
		}
		if format == "md" && !strings.Contains(text, "2026-09-29T12:00:00Z") {
			t.Fatalf("markdown header should carry the injected export time:\n%s", text)
		}
	}
}

func TestExportExitsThroughSharedFailureMapping(t *testing.T) {
	var stderr bytes.Buffer
	code := runCtl(context.Background(), []string{"export", "--instance-id", "nope", "--role", "executor", "--output", exportFile(t)}, ctlEnv{stdout: io.Discard, stderr: &stderr, root: filepath.Join(t.TempDir(), "instances")})
	if code != exitNotFound {
		t.Fatalf("unknown instance = %d %s", code, stderr.String())
	}
}
