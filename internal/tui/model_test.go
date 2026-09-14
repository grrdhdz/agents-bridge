package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

func newTUITestClient(t *testing.T) (*bridge.Server, *bridge.Client) {
	t.Helper()
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	client, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return server, client
}

func TestModelStartsFocusedAndAcceptsImmediateInput(t *testing.T) {
	model := New(Options{})
	model.Init()

	updated, _ := model.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
	got, ok := updated.(*Model)
	if !ok {
		t.Fatalf("expected pointer model after update, got %T", updated)
	}
	if !got.input.Focused() {
		t.Fatal("textarea should be focused after Init")
	}
	if got.input.Value() != "x" {
		t.Fatalf("expected immediate key input to be accepted, got %q", got.input.Value())
	}

	updated, _ = got.Update(tea.PasteMsg{Content: " línea 1\nlínea 2"})
	got = updated.(*Model)
	if got.input.Value() != "x línea 1\nlínea 2" {
		t.Fatalf("multiline paste was not preserved: %q", got.input.Value())
	}
}

func TestRenderMessagesUsesRelativeSenderAndPreservesBody(t *testing.T) {
	local, err := protocol.NewEnvelope("instance-a", "local", 1, "mac-orchestrator", protocol.RoleOrchestrator, "Mac\nreporte exacto", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	remote, err := protocol.NewEnvelope("instance-a", "remote", 1, "win-executor", protocol.RoleExecutor, "Windows\nresultado", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderMessages([]protocol.Envelope{local, remote}, map[string]string{"local": "accepted", "remote": "delivered"}, protocol.RoleOrchestrator, 80)
	for _, part := range []string{"Tú", "Windows", "Mac", "reporte exacto", "resultado"} {
		if !strings.Contains(rendered, part) {
			t.Fatalf("rendered view lost %q: %q", part, rendered)
		}
	}
	if strings.Index(rendered, "Tú · accepted") >= strings.Index(rendered, "Windows · delivered") {
		t.Fatal("expected messages to remain in server order")
	}
}

func TestRenderMessagesNamesRemoteMacFromWindowsPerspective(t *testing.T) {
	local, err := protocol.NewEnvelope("instance-a", "local", 1, "win-executor", protocol.RoleExecutor, "resultado", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	remote, err := protocol.NewEnvelope("instance-a", "remote", 1, "mac-orchestrator", protocol.RoleOrchestrator, "reporte", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderMessages([]protocol.Envelope{remote, local}, map[string]string{"remote": "delivered", "local": "accepted"}, protocol.RoleExecutor, 80)
	if !strings.Contains(rendered, "Mac · delivered") {
		t.Fatalf("Windows perspective should label remote as Mac: %q", rendered)
	}
	if !strings.Contains(rendered, "Tú · accepted") {
		t.Fatalf("Windows perspective should label local as Tú: %q", rendered)
	}
}

func TestOrchestratorCopiesJoinCommandAndKeepsCredentialOutOfView(t *testing.T) {
	_, client := newTUITestClient(t)
	token := strings.Repeat("secret-token-", 4)
	joinCommand := "codex-bridge join --host mac.tailnet --port 4242 --instance instance-a --token " + token
	var copied []string
	model := New(Options{
		Client:      client,
		LocalRole:   protocol.RoleOrchestrator,
		JoinCommand: joinCommand,
		CopyCommand: func(command string) error {
			copied = append(copied, command)
			return nil
		},
	})
	model.Init()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model = *updated.(*Model)
	viewModel := model.View()
	view := viewModel.Content
	if len(copied) != 1 || copied[0] != joinCommand {
		t.Fatalf("startup should copy the exact join command once, got %#v", copied)
	}
	if !strings.Contains(view, "CODEX-BRIDGE") || !strings.Contains(view, "copiado") {
		t.Fatalf("title and copy instruction should be visible at 120x30: %q", view)
	}
	if strings.Contains(view, "--token") || strings.Contains(view, token) || strings.Contains(view, "mac.tailnet") {
		t.Fatalf("normal TUI view exposed pairing credentials: %q", view)
	}
	if viewModel.MouseMode != tea.MouseModeCellMotion {
		t.Fatal("Mac view should enable cell-motion mouse input")
	}

	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyF5})
	model = *updated.(*Model)
	if len(copied) != 2 || copied[1] != joinCommand {
		t.Fatalf("F5 should recopy the exact join command, got %#v", copied)
	}
}

func TestPairRegenerationRecopiesNewCommandAndCopyFailureHasFallback(t *testing.T) {
	_, client := newTUITestClient(t)
	oldCommand := "codex-bridge join --host mac.tailnet --port 4242 --instance instance-a --token old"
	newCommand := "codex-bridge join --host mac.tailnet --port 4242 --instance instance-a --token new"
	var copied []string
	model := New(Options{
		Client:      client,
		LocalRole:   protocol.RoleOrchestrator,
		JoinCommand: oldCommand,
		CopyCommand: func(command string) error {
			copied = append(copied, command)
			return nil
		},
		OnPair: func() string { return newCommand },
	})
	model.Init()
	model.command("/pair")
	if len(copied) != 2 || copied[1] != newCommand {
		t.Fatalf("/pair should copy the fresh command, got %#v", copied)
	}

	failing := New(Options{
		Client:      client,
		LocalRole:   protocol.RoleOrchestrator,
		JoinCommand: oldCommand,
		CopyCommand: func(string) error { return errors.New("pbcopy unavailable") },
	})
	failing.Init()
	view := failing.View().Content
	if !strings.Contains(view, "No se pudo copiar") || !strings.Contains(view, "fallback") {
		t.Fatalf("copy failure should provide an actionable fallback: %q", view)
	}
	if strings.Contains(view, "--token") || strings.Contains(view, "old") {
		t.Fatalf("copy failure view exposed credentials: %q", view)
	}
}

func TestLongMultilineHistoryScrollsWithin120x30Viewport(t *testing.T) {
	model := New(Options{LocalRole: protocol.RoleOrchestrator, JoinCommand: "join"})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model = *updated.(*Model)
	model.input.Focus()
	for i := 0; i < 12; i++ {
		e, err := protocol.NewEnvelope("instance-a", "message-"+string(rune('a'+i)), uint64(i+1), "sender", protocol.RoleExecutor, strings.Join([]string{
			"message-start-" + string(rune('a'+i)),
			"line-two-" + string(rune('a'+i)),
			"line-three-" + string(rune('a'+i)),
			"message-end-" + string(rune('a'+i)),
		}, "\n"), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		model.messages = append(model.messages, e)
		model.statuses[e.MessageID] = "received"
	}
	model.refreshViewport(true)
	if model.viewport.YOffset() <= 0 {
		t.Fatal("long history should have a positive scroll offset at the bottom")
	}
	bottom := model.viewport.View()
	if !strings.Contains(bottom, "message-end-l") {
		t.Fatalf("bottom of history should show the newest message: %q", bottom)
	}
	if strings.Count(bottom, "\n")+1 > model.viewport.Height() {
		t.Fatalf("viewport rendered beyond its height: %d lines > %d", strings.Count(bottom, "\n")+1, model.viewport.Height())
	}

	model.viewport.GotoTop()
	top := model.viewport.View()
	if !strings.Contains(top, "message-start-a") {
		t.Fatalf("top of history should show the oldest message: %q", top)
	}
	if strings.Contains(top, "message-end-l") {
		t.Fatal("top of history should not include the newest message")
	}
	before := model.viewport.YOffset()
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	model = *updated.(*Model)
	if model.viewport.YOffset() <= before {
		t.Fatal("PgDn should advance the history viewport")
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	model = *updated.(*Model)
	if model.viewport.YOffset() != 0 {
		t.Fatal("Home should return the history viewport to the top")
	}
	model.input.SetValue("draft")
	updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	model = *updated.(*Model)
	if model.viewport.YOffset() <= 0 {
		t.Fatal("mouse wheel down should advance the history viewport")
	}
	if !model.input.Focused() || model.input.Value() != "draft" {
		t.Fatal("mouse wheel must not steal textarea focus or insert characters")
	}
	updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	model = *updated.(*Model)
	if model.viewport.YOffset() != 0 {
		t.Fatal("mouse wheel up should return to the top after one matching step")
	}
	// Trackpad vertical gestures arrive as repeated wheel messages in Bubble
	// Tea, so exercise the same path more than once.
	for i := 0; i < 2; i++ {
		updated, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		model = *updated.(*Model)
	}
	if model.viewport.YOffset() <= 0 {
		t.Fatal("repeated trackpad-style wheel events should scroll the history")
	}
}

func TestLongParagraphSoftWrapsAndCreatesVerticalScroll(t *testing.T) {
	model := New(Options{LocalRole: protocol.RoleOrchestrator, JoinCommand: "join"})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model = *updated.(*Model)
	body := strings.Repeat("párrafo-largo-sin-saltos ", 700)
	e, err := protocol.NewEnvelope("instance-a", "long-message", 1, "sender", protocol.RoleExecutor, body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model.messages = []protocol.Envelope{e}
	model.statuses[e.MessageID] = "received"
	model.refreshViewport(true)
	if !model.viewport.SoftWrap {
		t.Fatal("message viewport must enable SoftWrap")
	}
	if model.viewport.YOffset() <= 0 {
		t.Fatal("a long paragraph without manual newlines should wrap and scroll vertically")
	}
	if strings.Contains(model.viewport.View(), "\n\n") {
		t.Fatal("soft-wrapped paragraph should not gain blank lines from hard wrapping")
	}
}

func TestExecutorFooterOmitsMacOnlyCopyControl(t *testing.T) {
	_, client := newTUITestClient(t)
	model := New(Options{Client: client, LocalRole: protocol.RoleExecutor})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model = *updated.(*Model)
	view := model.View().Content
	if strings.Contains(view, "F5 copiar") {
		t.Fatalf("Windows footer should not advertise the Mac-only copy control: %q", view)
	}
	if !strings.Contains(view, "rueda/trackpad scroll") {
		t.Fatalf("footer should document wheel/trackpad scrolling: %q", view)
	}
	if model.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("Windows view should enable cell-motion mouse input")
	}
}
