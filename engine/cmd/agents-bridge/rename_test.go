package main

import (
	"context"
	"strings"
	"testing"
)

func (r *localRun) rename(args ...string) (int, string, string) {
	var stdout, stderr strings.Builder
	code := runRename(context.Background(), args, ctlEnv{stdout: &stdout, stderr: &stderr, root: r.root})
	return code, stdout.String(), stderr.String()
}

func TestRenameLocalBridgeShowsInPS(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	code, stdout, stderr := run.rename("--instance-id", id, "Pagos API")
	if code != 0 || !strings.Contains(stdout, `"name":"Pagos API"`) {
		t.Fatalf("rename: %d %s %s", code, stdout, stderr)
	}
	var out, errOut strings.Builder
	if code := runPS(context.Background(), nil, ctlEnv{stdout: &out, stderr: &errOut, root: run.root}); code != 0 {
		t.Fatal(errOut.String())
	}
	if !strings.Contains(out.String(), "NOMBRE") || !strings.Contains(out.String(), "Pagos API") {
		t.Fatalf("ps without name:\n%s", out.String())
	}
	out.Reset()
	runPS(context.Background(), []string{"--format", "jsonl"}, ctlEnv{stdout: &out, stderr: &errOut, root: run.root})
	if !strings.Contains(out.String(), `"name":"Pagos API"`) {
		t.Fatalf("ps jsonl without name: %s", out.String())
	}
	for _, args := range [][]string{{"--instance-id", id}, {"Nombre"}, {"--instance-id", id, "a\tb"}} {
		if code, _, _ := run.rename(args...); code != exitUsage {
			t.Fatalf("accepted %v: %d", args, code)
		}
	}
	if code, _, stderr := run.rename("--instance-id", "missing", "x"); code != exitNotFound {
		t.Fatalf("unknown instance: %d %s", code, stderr)
	}
	run.cancel()
}
