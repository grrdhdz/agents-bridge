package main

import (
	"bytes"
	"encoding/json"
	"github.com/grrdhdz/agents-bridge/engine/internal/integration"
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationEnsureCLIIsolated(t *testing.T) {
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "USERPROFILE": root, "LOCALAPPDATA": filepath.Join(root, "local"), "APPDATA": filepath.Join(root, "config"), "XDG_CONFIG_HOME": filepath.Join(root, "config"), "PATH": filepath.Join(root, "path")} {
		t.Setenv(k, v)
	}
	exe := filepath.Join(root, "source")
	os.WriteFile(exe, []byte("cli-engine"), 0700)
	var out, errs bytes.Buffer
	env := integrationEnv{stdout: &out, stderr: &errs, ensure: integration.EnsureOptions{Home: root, ConfigDir: filepath.Join(root, "config"), Executable: exe, Platform: "darwin"}}
	if code := runIntegration([]string{"ensure"}, env); code != 0 {
		t.Fatal(code, errs.String())
	}
	var result integration.EnsureResult
	if json.Unmarshal(out.Bytes(), &result) != nil || !result.Changed || !result.CLICurrent {
		t.Fatal(out.String())
	}
	out.Reset()
	if code := runIntegration([]string{"ensure"}, env); code != 0 {
		t.Fatal(code)
	}
	json.Unmarshal(out.Bytes(), &result)
	if result.Changed {
		t.Fatal("not idempotent")
	}
	if code := runIntegration([]string{"ensure", "--scope", "project"}, env); code != exitUsage {
		t.Fatal(code)
	}
}
