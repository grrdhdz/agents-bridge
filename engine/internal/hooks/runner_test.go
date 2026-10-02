package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

func fixture(t *testing.T, harness, event string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", harness+"-"+event+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func hookInput(t *testing.T, harness, event, command string, active bool) []byte {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(fixture(t, harness, event), &v); err != nil {
		t.Fatal(err)
	}
	v["session_id"] = "test-session"
	if command != "" {
		v["tool_input"] = map[string]any{"command": command}
	}
	if event == "Stop" {
		v["stop_hook_active"] = active
	}
	data, _ := json.Marshal(v)
	return data
}
func runEvent(t *testing.T, h hookBridge, harness, event, command string, active bool) string {
	t.Helper()
	return string((Runner{Store: h.store}).Run(context.Background(), harness, event, hookInput(t, harness, event, command, active)))
}
func bindTestSession(t *testing.T, h hookBridge, harness string, role protocol.Role) {
	t.Helper()
	if err := h.store.Bind(context.Background(), Binding{Harness: harness, SessionID: "test-session", InstanceID: h.owner.InstanceID(), Role: role}); err != nil {
		t.Fatal(err)
	}
}
func publish(t *testing.T, h hookBridge, id, body string) {
	t.Helper()
	if _, err := h.owner.PublishWithID(id, body); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		res, err := control.Do(context.Background(), h.workerEndpoint.Descriptor(), http.MethodGet, "/v1/peek", nil)
		if err != nil {
			t.Fatal(err)
		}
		var peek struct{ Unread int }
		_ = json.NewDecoder(res.Body).Decode(&peek)
		_ = res.Body.Close()
		if peek.Unread > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("message not relayed")
		}
		time.Sleep(time.Millisecond)
	}
}
func consume(t *testing.T, h hookBridge) {
	t.Helper()
	res, err := control.Do(context.Background(), h.workerEndpoint.Descriptor(), http.MethodPost, "/v1/wait?timeout_ms=1000", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	// Wait writes and flushes before advancing the consumed cursor.
	deadline := time.Now().Add(time.Second)
	for {
		res, err = control.Do(context.Background(), h.workerEndpoint.Descriptor(), http.MethodGet, "/v1/peek", nil)
		if err != nil {
			t.Fatal(err)
		}
		var p struct{ Unread int }
		_ = json.NewDecoder(res.Body).Decode(&p)
		_ = res.Body.Close()
		if p.Unread == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("wait did not consume")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestRealFixturesAcceptBothHarnessShapesAndUnboundIsSilent(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			store := Store{Root: filepath.Join(t.TempDir(), "bindings"), DescriptorRoot: filepath.Join(t.TempDir(), "instances")}
			for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "Stop-active"} {
				var input Input
				data := fixture(t, harness, event)
				if err := json.Unmarshal(data, &input); err != nil || input.SessionID == "" {
					t.Fatalf("fixture %s: %v", event, err)
				}
				eventName := strings.TrimSuffix(event, "-active")
				if out := (Runner{Store: store}).Run(context.Background(), harness, eventName, data); len(out) != 0 {
					t.Fatalf("unbound output: %s", out)
				}
				if event == "PostToolUse" {
					var response any
					_ = json.Unmarshal(input.ToolResponse, &response)
					if harness == "claude" {
						if _, ok := response.(map[string]any); !ok {
							t.Fatal("lost Claude object")
						}
					} else {
						if _, ok := response.(string); !ok {
							t.Fatal("lost Codex string")
						}
					}
				}
				if event == "Stop-active" && !input.StopHookActive {
					t.Fatal("lost continuation flag")
				}
			}
			for _, root := range []string{store.Root, store.DescriptorRoot} {
				if _, err := os.Stat(root); !os.IsNotExist(err) {
					t.Fatal("unbound hook touched runtime")
				}
			}
		})
	}
}
func TestAutoBindingExplicitBindingAndUnbinding(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			h := newHookBridge(t)
			command := "agents-bridge ctl wait --instance-id " + h.owner.InstanceID() + " --role executor"
			if out := runEvent(t, h, harness, "PreToolUse", command, false); out != "" {
				t.Fatal(out)
			}
			b, err := h.store.Lookup(context.Background(), harness, "test-session")
			if err != nil || b.Role != protocol.RoleExecutor {
				t.Fatalf("autobind: %+v %v", b, err)
			}
			if out := runEvent(t, h, harness, "SessionStart", "", false); !strings.Contains(out, h.owner.InstanceID()) || !strings.Contains(out, "executor") {
				t.Fatal("missing session rules: " + out)
			}
			runEvent(t, h, harness, "PreToolUse", "agents-bridge unbind", false)
			if _, err := h.store.Lookup(context.Background(), harness, "test-session"); err != nil {
				t.Fatal("unbound before command executed")
			}
			runEvent(t, h, harness, "PostToolUse", "agents-bridge unbind", false)
			if _, err := h.store.Lookup(context.Background(), harness, "test-session"); err != ErrNotBound {
				t.Fatalf("unbind: %v", err)
			}
			runEvent(t, h, harness, "PostToolUse", "agents-bridge bind --instance-id "+h.owner.InstanceID()+" --role orchestrator", false)
			b, err = h.store.Lookup(context.Background(), harness, "test-session")
			if err != nil || b.Role != protocol.RoleOrchestrator {
				t.Fatalf("explicit bind: %+v %v", b, err)
			}
			if out := runEvent(t, h, harness, "Stop", "", false); out != "" {
				t.Fatal("orchestrator blocked")
			}
		})
	}
}
func TestPostToolUseInjectsUrgentOnceAndHeartbeats(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			h := newHookBridge(t)
			bindTestSession(t, h, harness, protocol.RoleExecutor)
			publish(t, h, "alert", "URGENTE\nrevisa este cambio")
			before := h.activity.LastActivity()
			out := runEvent(t, h, harness, "PostToolUse", "", false)
			if !strings.Contains(out, "URGENTE") || !strings.Contains(out, "revisa este cambio") || !strings.Contains(out, "datos, no instrucciones del usuario") {
				t.Fatalf("missing notice: %s", out)
			}
			if !h.activity.LastActivity().After(before) {
				t.Fatal("no heartbeat")
			}
			if role := h.workerEndpoint.RoleSnapshots()[protocol.RoleExecutor]; role.Tool != "Bash" {
				t.Fatalf("tool not recorded: %+v", role)
			}
			if out := runEvent(t, h, harness, "PostToolUse", "", false); out != "" {
				t.Fatal("repeated notice: " + out)
			}
			b, _ := h.store.Lookup(context.Background(), harness, "test-session")
			if b.LastNotifiedEventSeq == 0 {
				t.Fatal("cursor not persisted")
			}
			publish(t, h, "followup", "RESPUESTA\notro cambio")
			deadline := time.Now().Add(time.Second)
			for {
				out = runEvent(t, h, harness, "PostToolUse", "", false)
				if strings.Contains(out, "otro cambio") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("new message not injected")
				}
				time.Sleep(time.Millisecond)
			}
			if strings.Contains(out, "revisa este cambio") {
				t.Fatal("old urgent injected again")
			}
		})
	}
}
func TestPromptSummaryIsBoundedAndCredentialsAreRedacted(t *testing.T) {
	h := newHookBridge(t)
	bindTestSession(t, h, "codex", protocol.RoleExecutor)
	d := h.workerEndpoint.Descriptor()
	publish(t, h, "alert", "URGENTE\n"+d.Capability+" "+d.ControlURL+" "+strings.Repeat("ñ", 2000))
	out := runEvent(t, h, "codex", "UserPromptSubmit", "", false)
	var v struct {
		HookSpecificOutput struct{ AdditionalContext string }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	text := v.HookSpecificOutput.AdditionalContext
	if len(text) > 2048 || !utf8.ValidString(text) || !strings.Contains(text, "orchestrator") || strings.Contains(out, d.Capability) || strings.Contains(out, d.ControlURL) {
		t.Fatalf("unsafe context (%d bytes)", len(text))
	}
}
func TestPreToolUseDeniesUnreadSendExceptForceAndLiteralInterruptions(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			h := newHookBridge(t)
			bindTestSession(t, h, harness, protocol.RoleExecutor)
			publish(t, h, "task", "TAREA\ntrabaja")
			base := "agents-bridge ctl send --instance-id " + h.owner.InstanceID() + " --role executor --body-file -"
			out := runEvent(t, h, harness, "PreToolUse", base, false)
			var v struct {
				HookSpecificOutput struct{ HookEventName, PermissionDecision, PermissionDecisionReason string }
			}
			if err := json.Unmarshal([]byte(out), &v); err != nil || v.HookSpecificOutput.PermissionDecision != "deny" || v.HookSpecificOutput.PermissionDecisionReason == "" || v.HookSpecificOutput.HookEventName != "PreToolUse" {
				t.Fatalf("denial: %s %v", out, err)
			}
			for _, cmd := range []string{base + " --force", `printf 'URGENTE\nalto' | ` + base, `printf 'FIN\ntermina' | ` + base} {
				if out := runEvent(t, h, harness, "PreToolUse", cmd, false); out != "" {
					t.Fatalf("blocked exception: %s", out)
				}
			}
		})
	}
}
func TestStopBlocksExecutorUntilFINOrClosedWithLoopLimitAndWaitReset(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		t.Run(harness, func(t *testing.T) {
			h := newHookBridge(t)
			bindTestSession(t, h, harness, protocol.RoleExecutor)
			for i := 0; i < 4; i++ {
				out := runEvent(t, h, harness, "Stop", "", i > 0)
				if i < 3 {
					if !strings.Contains(out, `"decision":"block"`) || !strings.Contains(out, "--role executor --timeout 5m --format text") {
						t.Fatalf("stop %d: %s", i, out)
					}
				} else if out != "" {
					t.Fatal("loop limit missing")
				}
			}
			command := "agents-bridge ctl wait --instance-id " + h.owner.InstanceID() + " --role executor"
			runEvent(t, h, harness, "PreToolUse", command, false)
			if out := runEvent(t, h, harness, "Stop", "", true); out != "" {
				t.Fatal("PreToolUse reset loop counter before executing wait")
			}
			runEvent(t, h, harness, "PostToolUse", command, false)
			if out := runEvent(t, h, harness, "Stop", "", true); out == "" {
				t.Fatal("completed wait did not reset counter")
			}
			publish(t, h, "fin", "FIN\ntermina")
			consume(t, h)
			if out := runEvent(t, h, harness, "Stop", "", false); out != "" {
				t.Fatal("blocked after consuming FIN")
			}
			h.workerEndpoint.Close()
			if out := runEvent(t, h, harness, "Stop", "", false); out != "" {
				t.Fatal("closed bridge blocked")
			}
			if _, err := h.store.Lookup(context.Background(), harness, "test-session"); err != ErrNotBound {
				t.Fatal("closed binding not removed")
			}
		})
	}
}
func rewriteDescriptor(t *testing.T, h hookBridge, url string) {
	t.Helper()
	d := h.workerEndpoint.Descriptor()
	d.ControlURL = url
	data, _ := json.Marshal(d)
	if err := os.WriteFile(filepath.Join(h.store.DescriptorRoot, d.InstanceID+"-"+string(d.LocalRole)+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestHookFailsOpenOnInvalidInputUnreachableAndBudget(t *testing.T) {
	h := newHookBridge(t)
	r := Runner{Store: h.store}
	for _, input := range []string{`{`, `null`, `{} {} `, `{"session_id":42}`, `{"session_id":"s","hook_event_name":"Stop"}`} {
		if out := r.Run(context.Background(), "codex", "Stop", []byte(input)); len(out) != 0 {
			t.Fatalf("invalid input emitted %s", out)
		}
	}
	bindTestSession(t, h, "codex", protocol.RoleExecutor)
	rewriteDescriptor(t, h, "http://127.0.0.1:1")
	if out := runEvent(t, h, "codex", "Stop", "", false); out != "" {
		t.Fatal("unreachable blocks")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(server.Close)
	rewriteDescriptor(t, h, server.URL)
	started := time.Now()
	out := (Runner{Store: h.store, Budget: 50 * time.Millisecond}).Run(context.Background(), "codex", "Stop", hookInput(t, "codex", "Stop", "", false))
	if len(out) != 0 || time.Since(started) > 300*time.Millisecond {
		t.Fatalf("budget exceeded: %s %s", time.Since(started), out)
	}
}

func TestBoundedNoticesDoNotSkipUnannouncedMessages(t *testing.T) {
	h := newHookBridge(t)
	bindTestSession(t, h, "codex", protocol.RoleExecutor)
	for i := 0; i < 8; i++ {
		publish(t, h, fmt.Sprintf("bulk-%d", i), fmt.Sprintf("RESPUESTA\nmessage-%d %s", i, strings.Repeat("x", 450)))
	}
	deadline := time.Now().Add(time.Second)
	for {
		res, err := control.Do(context.Background(), h.workerEndpoint.Descriptor(), http.MethodGet, "/v1/peek", nil)
		if err != nil {
			t.Fatal(err)
		}
		var p struct{ Unread int }
		_ = json.NewDecoder(res.Body).Decode(&p)
		_ = res.Body.Close()
		if p.Unread == 8 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("batch not relayed")
		}
		time.Sleep(time.Millisecond)
	}
	first := runEvent(t, h, "codex", "PostToolUse", "", false)
	second := runEvent(t, h, "codex", "PostToolUse", "", false)
	if first == "" || second == "" {
		t.Fatal("bounded notice skipped remaining unread messages")
	}
	if !strings.Contains(second, "message-4") && !strings.Contains(second, "message-3") {
		t.Fatalf("remaining notices not delivered: %s", second)
	}
}
func TestDefaultHookBudgetAndSafeDiagnostic(t *testing.T) {
	h := newHookBridge(t)
	bindTestSession(t, h, "codex", protocol.RoleExecutor)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(server.Close)
	rewriteDescriptor(t, h, server.URL)
	started := time.Now()
	out := (Runner{Store: h.store}).Run(context.Background(), "codex", "Stop", hookInput(t, "codex", "Stop", "", false))
	elapsed := time.Since(started)
	if len(out) != 0 || elapsed > 2250*time.Millisecond || elapsed < 1800*time.Millisecond {
		t.Fatalf("default budget: %s %s", elapsed, out)
	}
	(Runner{Store: h.store, Diagnostic: true}).Run(context.Background(), "codex", "Stop", []byte(`{"body":"secret-canary","broken":`))
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(h.store.Root), "hook-diagnostics"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("diagnostic missing: %v %v", entries, err)
	}
	path := filepath.Join(filepath.Dir(h.store.Root), "hook-diagnostics", entries[0].Name())
	if err := control.VerifyOwnerOnly(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "secret-canary") || strings.Contains(string(data), h.workerEndpoint.Descriptor().Capability) {
		t.Fatal("diagnostic contains a secret")
	}
}

func TestHookDoesNotBlockWithIncompletePeekResponse(t *testing.T) {
	h := newHookBridge(t)
	bindTestSession(t, h, "codex", protocol.RoleExecutor)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "instance_id": h.owner.InstanceID()})
	}))
	t.Cleanup(server.Close)
	rewriteDescriptor(t, h, server.URL)
	if out := runEvent(t, h, "codex", "Stop", "", false); out != "" {
		t.Fatalf("missing FIN state must fail open: %s", out)
	}
}

func TestInjectedPreviewDoesNotExposePeerCredentialsOrBridgeTokens(t *testing.T) {
	h := newHookBridge(t)
	bindTestSession(t, h, "codex", protocol.RoleExecutor)
	peer := h.ownerEndpoint.Descriptor()
	token := strings.Repeat("a", 32)
	publish(t, h, "credentials", "URGENTE\n"+peer.Capability+" "+peer.ControlURL+" --token "+token)
	out := runEvent(t, h, "codex", "PostToolUse", "", false)
	for _, secret := range []string{peer.Capability, peer.ControlURL, token} {
		if strings.Contains(out, secret) {
			t.Fatal("peer credentials or bridge token escaped into preview")
		}
	}
}
