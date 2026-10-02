package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookCLIUsesFailOpenExitStatus(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "hook", "codex", "Stop")
	cmd.Env = append(os.Environ(), runMainEnv+"=1", "TMPDIR="+t.TempDir(), "LOCALAPPDATA="+t.TempDir())
	cmd.Stdin = strings.NewReader(`{"broken":`)
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("hook must exit 0 silently: %s %v", out, err)
	}
}
func TestRunHookBindAndStopWithRealLoopbackBridge(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	var out, stderr bytes.Buffer
	input := func(event, command string) io.Reader {
		data, err := os.ReadFile(filepath.Join("../../internal/hooks/testdata", "codex-"+event+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		_ = json.Unmarshal(data, &v)
		v["session_id"] = "cli-session"
		if command != "" {
			v["tool_input"] = map[string]string{"command": command}
		}
		raw, _ := json.Marshal(v)
		return bytes.NewReader(raw)
	}
	env := ctlEnv{root: run.root, stdout: &out, stderr: &stderr, stdin: input("PreToolUse", "agents-bridge ctl wait --instance-id "+id+" --role executor")}
	if code := runHook(context.Background(), []string{"codex", "PreToolUse"}, env); code != 0 || out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("bind hook: %d %s %s", code, &out, &stderr)
	}
	env.stdin = input("Stop", "")
	if code := runHook(context.Background(), []string{"codex", "Stop"}, env); code != 0 || !strings.Contains(out.String(), `"decision":"block"`) {
		t.Fatalf("stop hook: %d %s %s", code, &out, &stderr)
	}
}
func TestHookInputBudgetClosesBlockedReaderAndIgnoresInvalidArgs(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	var out, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	code := runHook(ctx, []string{"claude", "Stop"}, ctlEnv{stdin: reader, stdout: &out, stderr: &stderr})
	if code != 0 || out.Len() != 0 || stderr.Len() != 0 || time.Since(started) > 200*time.Millisecond {
		t.Fatalf("stdin budget: %d %s", code, time.Since(started))
	}
	if _, err := writer.Write([]byte("late")); err == nil {
		t.Fatal("blocked reader not closed")
	}
	if code := runHook(context.Background(), nil, ctlEnv{stdin: strings.NewReader(""), stdout: &out, stderr: &stderr}); code != 0 || out.Len() != 0 {
		t.Fatal("invalid args were not fail-open")
	}
}
