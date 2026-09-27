package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

// Exit codes from the 2026-09-13 spec (§6), extended by 2026-09-27 (§6).
const (
	exitOK           = 0
	exitUsage        = 2
	exitNotFound     = 3
	exitUnauthorized = 4
	exitForbidden    = 5
	exitConflict     = 6
	exitBackpressure = 7
	exitTransport    = 8
	exitInternal     = 9
)

func exitCodeFor(code string) int {
	switch code {
	case "INVALID_JSON", "INVALID_REQUEST_ID", "INVALID_METHOD", "MESSAGE_INVALID", "INSTANCE_AMBIGUOUS", "USAGE", "THREAD_INVALID":
		return exitUsage
	case "INSTANCE_NOT_FOUND", "INSTANCE_CLOSED", "THREAD_NOT_FOUND":
		return exitNotFound
	case "UNAUTHORIZED":
		return exitUnauthorized
	case "FORBIDDEN", "WAIT_IN_PROGRESS":
		return exitForbidden
	case "ID_CONFLICT":
		return exitConflict
	case "CONTROL_BACKPRESSURE", "CURSOR_EXPIRED":
		return exitBackpressure
	case "TRANSPORT_ERROR", "CONTROL_UNREACHABLE":
		return exitTransport
	default:
		return exitInternal
	}
}

// ctlEnv carries process I/O and the descriptor root so tests can run ctl
// in-process against a private runtime directory. There is no cwd: an
// instance is always addressed by --instance-id, never inferred from the
// working directory.
type ctlEnv struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	root           string
}

type ctlFailure struct {
	code, message string
}

func (f *ctlFailure) Error() string { return f.code + ": " + f.message }

func failure(code, format string, args ...any) *ctlFailure {
	return &ctlFailure{code: code, message: fmt.Sprintf(format, args...)}
}

// runCtl executes one ctl operation. Successful records go to stdout as JSONL
// (or text for wait --format text); every error is one JSONL record on stderr.
func runCtl(ctx context.Context, args []string, env ctlEnv) int {
	return reportFailure(dispatchCtl(ctx, args, env), env.stderr)
}

// reportFailure turns a dispatch error into the shared JSONL-on-stderr error
// record and matching exit code (or exitOK when err is nil). Both ctl and
// codex operations share this so their error reporting never drifts apart.
func reportFailure(err error, stderr io.Writer) int {
	if err == nil {
		return exitOK
	}
	var f *ctlFailure
	if !errors.As(err, &f) {
		f = &ctlFailure{code: "INTERNAL", message: err.Error()}
	}
	record, _ := json.Marshal(map[string]any{"v": 1, "type": "error", "ok": false, "code": f.code, "message": f.message})
	fmt.Fprintln(stderr, string(record))
	return exitCodeFor(f.code)
}

func dispatchCtl(ctx context.Context, args []string, env ctlEnv) error {
	if len(args) == 0 {
		return failure("USAGE", "ctl requires one of: list, read, watch, send, wait")
	}
	operation, args := args[0], args[1:]
	flags := flag.NewFlagSet("codex-bridge ctl "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	instanceID := flags.String("instance-id", "", "instance_id to control")
	roleName := flags.String("role", "", "orchestrator or executor")
	after := flags.Uint64("after-event-seq", 0, "exclusive event cursor")
	limit := flags.Int("limit", 100, "maximum events for read")
	messageID := flags.String("message-id", "", "idempotency key for send")
	bodyFile := flags.String("body-file", "", "UTF-8 body file, or - for stdin")
	timeout := flags.Duration("timeout", 5*time.Minute, "wait timeout")
	format := flags.String("format", "jsonl", "wait output: jsonl or text")
	if err := flags.Parse(args); err != nil {
		return failure("USAGE", "%v", err)
	}
	if flags.NArg() > 0 {
		return failure("USAGE", "unexpected argument %q", flags.Arg(0))
	}
	if operation == "list" {
		return ctlList(ctx, env)
	}
	role, err := parseRole(*roleName)
	if err != nil {
		return err
	}
	switch operation {
	case "read", "watch", "send", "wait":
	default:
		return failure("USAGE", "unknown ctl operation %q", operation)
	}
	if strings.TrimSpace(*instanceID) == "" {
		return failure("USAGE", "--instance-id is required")
	}
	descriptor, err := control.SelectDescriptor(env.root, *instanceID, role)
	if err != nil {
		switch err.Error() {
		case "INSTANCE_NOT_FOUND", "INSTANCE_AMBIGUOUS":
			return failure(err.Error(), "select an instance with --role and/or --instance-id")
		}
		return failure("INTERNAL", "%v", err)
	}
	switch operation {
	case "read":
		query := url.Values{"after_event_seq": {strconv.FormatUint(*after, 10)}, "limit": {strconv.Itoa(*limit)}}
		return ctlSimple(ctx, env, descriptor, http.MethodGet, "/v1/read?"+query.Encode(), nil)
	case "watch":
		return ctlWatch(ctx, env, descriptor, *after)
	case "send":
		return ctlSend(ctx, env, descriptor, *messageID, *bodyFile)
	default:
		if *format != "jsonl" && *format != "text" {
			return failure("USAGE", "--format must be jsonl or text")
		}
		return ctlWait(ctx, env, descriptor, *timeout, *format)
	}
}

func parseRole(name string) (protocol.Role, error) {
	switch name {
	case "":
		return "", nil
	case "orchestrator", string(protocol.RoleOrchestrator):
		return protocol.RoleOrchestrator, nil
	case "executor", string(protocol.RoleExecutor):
		return protocol.RoleExecutor, nil
	}
	return "", failure("USAGE", "--role must be orchestrator or executor")
}

func roleAlias(role protocol.Role) string {
	if role == protocol.RoleOrchestrator {
		return "orchestrator"
	}
	return "executor"
}

func request(ctx context.Context, d control.Descriptor, method, path string, body io.Reader) (*http.Response, error) {
	response, err := control.Do(ctx, d, method, path, body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure("CONTROL_UNREACHABLE", "control endpoint unreachable on loopback; if this runs inside a sandbox without network (e.g. Codex workspace-write), run codex-bridge ctl outside the sandbox")
	}
	return response, nil
}

// failureFromBody turns a non-2xx JSONL error record into a ctlFailure.
func failureFromBody(status int, data []byte) error {
	var record struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &record) != nil || record.Code == "" {
		return failure("INTERNAL", "unexpected HTTP status %d", status)
	}
	return failure(record.Code, "%s", record.Message)
}

func ctlSimple(ctx context.Context, env ctlEnv, d control.Descriptor, method, path string, body io.Reader) error {
	response, err := request(ctx, d, method, path, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return failure("TRANSPORT_ERROR", "%v", err)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return failureFromBody(response.StatusCode, data)
	}
	_, err = env.stdout.Write(data)
	return err
}

func ctlSend(ctx context.Context, env ctlEnv, d control.Descriptor, messageID, bodyFile string) error {
	var body []byte
	var err error
	switch bodyFile {
	case "":
		return failure("USAGE", "send requires --body-file FILE or --body-file -")
	case "-":
		body, err = io.ReadAll(env.stdin)
	default:
		body, err = os.ReadFile(bodyFile)
	}
	if err != nil {
		return failure("USAGE", "read body: %v", err)
	}
	if messageID == "" {
		if messageID, err = control.NewID(); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(map[string]any{"v": 1, "message_id": messageID, "body": string(body)})
	if err != nil {
		return err
	}
	return ctlSimple(ctx, env, d, http.MethodPost, "/v1/send", bytes.NewReader(append(payload, '\n')))
}

func ctlWatch(ctx context.Context, env ctlEnv, d control.Descriptor, after uint64) error {
	response, err := request(ctx, d, http.MethodGet, "/v1/watch?after_event_seq="+strconv.FormatUint(after, 10), nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		return failureFromBody(response.StatusCode, data)
	}
	decoder := json.NewDecoder(response.Body)
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) {
				return nil
			}
			return failure("TRANSPORT_ERROR", "%v", err)
		}
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &head)
		if head.Type == "error" {
			return failureFromBody(http.StatusOK, raw)
		}
		if _, err := fmt.Fprintln(env.stdout, string(raw)); err != nil {
			return err
		}
	}
}

func ctlWait(ctx context.Context, env ctlEnv, d control.Descriptor, timeout time.Duration, format string) error {
	if timeout < time.Millisecond {
		return failure("USAGE", "--timeout must be at least 1ms")
	}
	path := "/v1/wait?timeout_ms=" + strconv.FormatInt(timeout.Milliseconds(), 10)
	response, err := request(ctx, d, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return failure("TRANSPORT_ERROR", "%v", err)
	}
	if response.StatusCode != http.StatusOK {
		return failureFromBody(response.StatusCode, data)
	}
	if format == "jsonl" {
		_, err = env.stdout.Write(data)
		return err
	}
	var record struct {
		InstanceID string             `json:"instance_id"`
		Status     string             `json:"status"`
		EventSeq   uint64             `json:"event_seq"`
		Message    *protocol.Envelope `json:"message"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return failure("INTERNAL", "decode wait response: %v", err)
	}
	if record.Status != "message" || record.Message == nil {
		_, err = fmt.Fprintf(env.stdout, "--- codex-bridge instance=%s timeout\n", record.InstanceID)
		return err
	}
	body := record.Message.Body
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	_, err = fmt.Fprintf(env.stdout, "--- codex-bridge instance=%s message_id=%s from=%s event_seq=%d\n%s", record.InstanceID, record.Message.MessageID, roleAlias(record.Message.SenderRole), record.EventSeq, body)
	return err
}

type listedInstance struct {
	InstanceID      string `json:"instance_id"`
	LocalRole       string `json:"local_role"`
	State           string `json:"state"`
	PID             int    `json:"pid"`
	StartedAt       string `json:"started_at"`
	HeartbeatAt     string `json:"heartbeat_at,omitempty"`
	EventSeq        uint64 `json:"event_seq"`
	LatestServerSeq uint64 `json:"latest_server_seq"`
	OldestEventSeq  uint64 `json:"oldest_event_seq"`
	PeerConnected   bool   `json:"peer_connected"`
}

// ctlList reports every live descriptor of the current user without cwd,
// control_url or capability.
func ctlList(ctx context.Context, env ctlEnv) error {
	descriptors, err := control.ListDescriptors(env.root)
	if err != nil {
		return failure("INTERNAL", "%v", err)
	}
	instances := make([]listedInstance, 0, len(descriptors))
	for _, d := range descriptors {
		item := listedInstance{InstanceID: d.InstanceID, LocalRole: roleAlias(d.LocalRole), State: "unresponsive", PID: d.PID, StartedAt: d.StartedAt.UTC().Format(time.RFC3339)}
		healthCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		response, err := control.Do(healthCtx, d, http.MethodGet, "/v1/health", nil)
		if err == nil {
			var health struct {
				State           string `json:"state"`
				Heartbeat       string `json:"heartbeat_at"`
				LatestServerSeq uint64 `json:"latest_server_seq"`
				OldestEventSeq  uint64 `json:"oldest_event_seq"`
				EventSeq        uint64 `json:"queued_event_seq"`
				PeerConnected   bool   `json:"peer_connected"`
			}
			if response.StatusCode == http.StatusOK && json.NewDecoder(response.Body).Decode(&health) == nil {
				item.State, item.HeartbeatAt = health.State, health.Heartbeat
				item.EventSeq, item.LatestServerSeq, item.OldestEventSeq = health.EventSeq, health.LatestServerSeq, health.OldestEventSeq
				item.PeerConnected = health.PeerConnected
			}
			_ = response.Body.Close()
		}
		cancel()
		instances = append(instances, item)
	}
	record, err := json.Marshal(map[string]any{"v": 1, "type": "response", "ok": true, "operation": "list", "instances": instances})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(env.stdout, string(record))
	return err
}
