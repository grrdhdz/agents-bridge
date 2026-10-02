// Package bridgeexport shares non-consuming journal export between CLI and API.
package bridgeexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	Now    func() time.Time
	Redact func(string) string
}
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func failure(code, format string, args ...any) error {
	return &Error{code, fmt.Sprintf(format, args...)}
}
func failureFromBody(status int, raw []byte) error {
	var e struct{ Code, Message string }
	if json.Unmarshal(raw, &e) == nil && e.Code != "" {
		return &Error{e.Code, e.Message}
	}
	return &Error{"CONTROL_UNREACHABLE", "export control request rejected"}
}

// exportedMessage is one conversation message with the last delivery status
// the journal knows for it.
type exportedMessage struct {
	envelope protocol.Envelope
	status   string
}

// ctlExport implements `ctl export` (§3.5): it pages /v1/read from the start
// of the retained journal, so it neither confirms nor moves anything, and
// writes the messages to a new owner-only file. When the journal already
// dropped older events (CURSOR_EXPIRED), the file says so at the top.
func Write(ctx context.Context, d control.Descriptor, output, format string, options Options) error {
	if strings.TrimSpace(output) == "" {
		return failure("USAGE", "export requires --output FILE")
	}
	if format != "md" && format != "jsonl" {
		return failure("USAGE", "--format must be md or jsonl")
	}
	if info, err := os.Stat(filepath.Dir(output)); err != nil || !info.IsDir() {
		return failure("USAGE", "cannot write %s: its directory does not exist", output)
	}
	// Reserved before reading anything: an existing file is a usage error,
	// and nothing is fetched for it.
	file, err := control.ReserveReadyFile(output)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return failure("USAGE", "%s already exists; export never overwrites", output)
		}
		return failure("USAGE", "cannot write %s: %v", output, err)
	}

	var messages []exportedMessage
	index := map[string]int{}
	statuses := map[string]string{}
	var oldestKept uint64
	after := uint64(0)
	for {
		query := "/v1/read?after_event_seq=" + strconv.FormatUint(after, 10) + "&limit=1000"
		response, err := control.Do(ctx, d, http.MethodGet, query, nil)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK {
			var gap struct {
				Code   string `json:"code"`
				Oldest uint64 `json:"oldest_event_seq"`
			}
			if json.Unmarshal(data, &gap) == nil && gap.Code == "CURSOR_EXPIRED" && gap.Oldest > 0 && after < gap.Oldest-1 {
				// The journal evicted events before our cursor: continue
				// from what remains and remember the gap for the header.
				oldestKept = gap.Oldest
				after = gap.Oldest - 1
				continue
			}
			return failureFromBody(response.StatusCode, data)
		}
		var page struct {
			Events []struct {
				MessageID string             `json:"message_id"`
				Status    string             `json:"status"`
				Message   *protocol.Envelope `json:"message"`
			} `json:"events"`
			Next    uint64 `json:"next_after_event_seq"`
			HasMore bool   `json:"has_more"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return failure("INTERNAL", "decode read response: %v", err)
		}
		for _, event := range page.Events {
			if event.MessageID != "" && event.Status != "" {
				statuses[event.MessageID] = event.Status
			}
			if event.Message != nil {
				if _, seen := index[event.Message.MessageID]; !seen {
					index[event.Message.MessageID] = len(messages)
					messages = append(messages, exportedMessage{envelope: *event.Message})
				}
			}
		}
		if !page.HasMore {
			break
		}
		after = page.Next
	}
	for i := range messages {
		messages[i].status = statuses[messages[i].envelope.MessageID]
		if options.Redact != nil {
			messages[i].envelope.Body = options.Redact(messages[i].envelope.Body)
		}
	}

	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	var content []byte
	if format == "jsonl" {
		content, err = renderExportJSONL(d.InstanceID, oldestKept, messages)
	} else {
		content = []byte(renderExportMarkdown(d.InstanceID, now().UTC(), oldestKept, messages))
	}
	if err != nil {
		return failure("INTERNAL", "render export: %v", err)
	}
	if err := file.Publish(content); err != nil {
		if errors.Is(err, os.ErrExist) {
			return failure("USAGE", "%s already exists; export never overwrites", output)
		}
		return failure("USAGE", "cannot write %s: %v", output, err)
	}
	return nil
}

func evictedNotice(oldest uint64) string {
	return fmt.Sprintf("el journal en RAM ya expulsó los eventos anteriores (oldest_event_seq=%d); esta exportación solo contiene lo retenido", oldest)
}

func renderExportJSONL(instanceID string, oldest uint64, messages []exportedMessage) ([]byte, error) {
	var out []byte
	if oldest > 0 {
		notice, err := json.Marshal(map[string]any{"v": 1, "type": "export_notice", "instance_id": instanceID, "truncated": true, "oldest_event_seq": oldest, "message": evictedNotice(oldest)})
		if err != nil {
			return nil, err
		}
		out = append(append(out, notice...), '\n')
	}
	for _, m := range messages {
		line, err := json.Marshal(m.envelope)
		if err != nil {
			return nil, err
		}
		out = append(append(out, line...), '\n')
	}
	return out, nil
}

func renderExportMarkdown(instanceID string, at time.Time, oldest uint64, messages []exportedMessage) string {
	var b strings.Builder
	b.WriteString("# Conversación agents-bridge\n\n")
	if oldest > 0 {
		fmt.Fprintf(&b, "> Aviso: %s.\n\n", evictedNotice(oldest))
	}
	fmt.Fprintf(&b, "- Instancia: %s\n- Exportada: %s\n- Mensajes: %d\n", instanceID, at.Format(time.RFC3339), len(messages))
	for i, m := range messages {
		e := m.envelope
		label := Label(e.Body)
		if label == "" {
			label = "ninguna"
		}
		status := m.status
		if status == "" {
			status = "desconocido"
		}
		fmt.Fprintf(&b, "\n## %d. %s\n\n", i+1, exportRoleName(e.SenderRole))
		fmt.Fprintf(&b, "- Rol: %s\n- Origen: %s\n- Hora: %s\n- Estado: %s\n- Etiqueta: %s\n- ID: %s\n\n", control.RoleKey(e.SenderRole), e.Source, e.CreatedAt.UTC().Format(time.RFC3339), status, label, e.MessageID)
		fence := markdownFence(e.Body)
		b.WriteString(fence + "\n" + e.Body)
		if !strings.HasSuffix(e.Body, "\n") {
			b.WriteString("\n")
		}
		b.WriteString(fence + "\n")
	}
	return b.String()
}

func exportRoleName(role protocol.Role) string {
	if role == protocol.RoleOrchestrator {
		return "orquestador"
	}
	return "ejecutor"
}

var exportLabels = map[string]bool{"TAREA": true, "PREGUNTA": true, "RESPUESTA": true, "RESULTADO": true, "FIN": true, "URGENTE": true, "PROGRESO": true}

func Label(body string) string {
	first, _, _ := strings.Cut(body, "\n")
	first = strings.TrimSuffix(first, "\r")
	if exportLabels[first] {
		return first
	}
	return ""
}

// markdownFence returns a backtick fence longer than any backtick run in
// body, so the body can never close its own code block early.
func markdownFence(body string) string {
	longest, run := 0, 0
	for _, r := range body {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	if longest < 3 {
		longest = 2
	}
	return strings.Repeat("`", longest+1)
}
