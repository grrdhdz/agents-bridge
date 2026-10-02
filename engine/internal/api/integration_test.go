package api

import (
	"github.com/grrdhdz/agents-bridge/engine/internal/integration"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrationOperationsIsolatedAndNoSecrets(t *testing.T) {
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "USERPROFILE": root, "LOCALAPPDATA": filepath.Join(root, "local"), "APPDATA": filepath.Join(root, "config"), "XDG_CONFIG_HOME": filepath.Join(root, "config"), "PATH": filepath.Join(root, "path")} {
		t.Setenv(k, v)
	}
	source := filepath.Join(root, "source")
	os.WriteFile(source, []byte("api-engine"), 0700)
	s := startSession(t, Options{Root: filepath.Join(root, "runtime"), Version: "v0.5.1", Integration: integration.EnsureOptions{Home: root, ConfigDir: filepath.Join(root, "config"), Executable: source, Platform: "darwin"}})
	r := s.request(t, "status", "integration_status", map[string]any{})
	if r["ok"] != true {
		t.Fatal(r)
	}
	if _, e := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(e) {
		t.Fatal("status wrote files")
	}
	r = s.request(t, "ensure", "integration_ensure", map[string]any{})
	if r["ok"] != true || r["result"].(map[string]any)["changed"] != true {
		t.Fatal(r)
	}
	r = s.request(t, "off", "integration_set", map[string]any{"harness": "claude", "enabled": false})
	if r["ok"] != true {
		t.Fatal(r)
	}
	r = s.request(t, "again", "integration_ensure", map[string]any{})
	hs := r["result"].(map[string]any)["harnesses"].(map[string]any)["claude"].(map[string]any)
	if hs["installed"] != false || hs["opted_out"] != true {
		t.Fatal(r)
	}
	r = s.request(t, "on", "integration_set", map[string]any{"harness": "claude", "enabled": true})
	if r["ok"] != true {
		t.Fatal(r)
	}
	for _, args := range []map[string]any{{"harness": "other", "enabled": true}, {"harness": "claude", "enabled": "yes"}, {"harness": "claude", "enabled": true, "home": "outside"}} {
		if r = s.request(t, "bad", "integration_set", args); r["ok"] != false {
			t.Fatal(r)
		}
	}
	for _, secret := range []string{"capability", "control_url", "token", "descriptors"} {
		if strings.Contains(s.raw.String(), secret) {
			t.Fatal("secret in output", secret)
		}
	}
}
