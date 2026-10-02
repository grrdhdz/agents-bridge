package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestBindValidatesLiveBridgeAndListAndUnbind(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	var out, stderr bytes.Buffer
	env := ctlEnv{root: run.root, stdout: &out, stderr: &stderr}
	if code := runBind(context.Background(), []string{"--instance-id", id, "--role", "executor"}, env); code != 0 || !strings.Contains(out.String(), id) || !strings.Contains(out.String(), "hook") {
		t.Fatalf("bind: %d %s %s", code, &out, &stderr)
	}
	out.Reset()
	stderr.Reset()
	if code := runBind(context.Background(), []string{"--instance-id", "absent", "--role", "executor"}, env); code != exitNotFound {
		t.Fatalf("missing bridge: %d %s", code, &stderr)
	}
	out.Reset()
	stderr.Reset()
	if code := runBind(context.Background(), []string{"--list"}, env); code != 0 || !strings.Contains(out.String(), "bindings") {
		t.Fatalf("list: %d %s %s", code, &out, &stderr)
	}
	out.Reset()
	stderr.Reset()
	if code := runUnbind(context.Background(), nil, env); code != 0 || !strings.Contains(out.String(), "hook") {
		t.Fatalf("unbind: %d %s %s", code, &out, &stderr)
	}
	for _, args := range [][]string{nil, {"--instance-id", id}, {"--instance-id", id, "--role", "bad"}, {"--list", "--instance-id", id}, {"--list", "extra"}} {
		if code := runBind(context.Background(), args, env); code != exitUsage {
			t.Fatalf("accepted args %v: %d", args, code)
		}
	}
}
