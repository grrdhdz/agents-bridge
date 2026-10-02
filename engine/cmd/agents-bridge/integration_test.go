package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationCLI(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	var out, errs bytes.Buffer
	env := integrationEnv{home: home, cwd: project, executable: "/tmp/agents-bridge", stdout: &out, stderr: &errs}
	if code := runIntegration([]string{"install", "claude", "--scope", "project"}, env); code != 0 {
		t.Fatalf("%d %s", code, errs.String())
	}
	var r map[string]any
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r["path"] != filepath.Join(project, ".claude", "settings.json") {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatal("wrote user scope")
	}
	out.Reset()
	errs.Reset()
	if code := runIntegration([]string{"install", "codex"}, env); code != 0 {
		t.Fatal(code, errs.String())
	}
	if !bytes.Contains(errs.Bytes(), []byte("confiable")) {
		t.Fatal("no trust guidance")
	}
	for _, args := range [][]string{{"install"}, {"bad", "claude"}, {"install", "unknown"}, {"install", "claude", "--scope", "all"}, {"install", "claude", "extra"}} {
		out.Reset()
		errs.Reset()
		if c := runIntegration(args, env); c != exitUsage {
			t.Fatal(args, c)
		}
	}
}
