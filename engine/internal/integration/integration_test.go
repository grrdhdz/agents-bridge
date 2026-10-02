package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallScopesPreserveBackupAndUninstall(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		for _, scope := range []string{"user", "project"} {
			t.Run(harness+scope, func(t *testing.T) {
				home, project := t.TempDir(), t.TempDir()
				o := Options{Home: home, Project: project, Scope: scope, Harness: harness, Executable: filepath.Join(t.TempDir(), "space & name", "agents-bridge")}
				path, err := o.Path()
				if err != nil {
					t.Fatal(err)
				}
				original, err := os.ReadFile("testdata/herdr.json")
				if err != nil {
					t.Fatal(err)
				}
				os.MkdirAll(filepath.Dir(path), 0700)
				os.WriteFile(path, original, 0600)
				r, err := Apply("install", o)
				if err != nil {
					t.Fatal(err)
				}
				if !r.Changed || len(r.Entries) != 5 || r.Backup == "" {
					t.Fatalf("%+v", r)
				}
				b, _ := os.ReadFile(r.Backup)
				if !bytes.Equal(b, original) {
					t.Fatal("backup differs")
				}
				installed, _ := os.ReadFile(path)
				if !json.Valid(installed) || !bytes.Contains(installed, []byte("9007199254740993")) {
					t.Fatal("JSON or number changed")
				}
				var d map[string]any
				json.Unmarshal(installed, &d)
				if d["env"].(map[string]any)["PRESERVE"] != "yes" {
					t.Fatal("foreign settings lost")
				}
				r, err = Apply("install", o)
				if err != nil || r.Changed || r.Backup != "" {
					t.Fatalf("not idempotent: %+v %v", r, err)
				}
				r, err = Apply("uninstall", o)
				if err != nil || len(r.Entries) != 0 || !r.Changed {
					t.Fatalf("%+v %v", r, err)
				}
				clean, _ := os.ReadFile(path)
				if !bytes.Contains(clean, []byte("herdr-agent-state.sh")) || bytes.Contains(clean, []byte(" hook ")) {
					t.Fatal("foreign hook removed or own retained")
				}
				r, err = Apply("uninstall", o)
				if err != nil || r.Changed {
					t.Fatal("uninstall not idempotent")
				}
			})
		}
	}
}
func TestInvalidJSONUntouched(t *testing.T) {
	o := Options{Home: t.TempDir(), Scope: "user", Harness: "claude", Executable: "/tmp/agents-bridge"}
	path, _ := o.Path()
	os.MkdirAll(filepath.Dir(path), 0700)
	for _, raw := range []string{"{", "null", "[]", `{"hooks":4}`} {
		os.WriteFile(path, []byte(raw), 0600)
		if _, err := Apply("install", o); err == nil {
			t.Fatal("accepted", raw)
		}
		got, _ := os.ReadFile(path)
		if string(got) != raw {
			t.Fatal("modified invalid")
		}
		files, _ := filepath.Glob(path + ".bak-*")
		if len(files) != 0 {
			t.Fatal("backup invalid")
		}
	}
}
func TestOwnershipAndCommandQuoting(t *testing.T) {
	for _, exe := range []string{"/tmp/a b/agents-bridge", `C:\Program Files\agents-bridge.exe`, "/tmp/a'$`\"/agents-bridge"} {
		command := HookCommand(exe, "claude", "Stop")
		if got, ok := OwnedCommand(command, "claude", "Stop"); !ok || got != exe {
			t.Fatalf("roundtrip %q => %q %v", command, got, ok)
		}
		if _, ok := OwnedCommand(command+" && echo foreign", "claude", "Stop"); ok {
			t.Fatal("owns compound")
		}
	}
	for _, c := range []string{"/tmp/codex-bridge hook claude Stop", "/tmp/foreign hook claude Stop", "agents-bridge hook claude Stop"} {
		if _, ok := OwnedCommand(c, "claude", "Stop"); ok {
			t.Fatal("owns", c)
		}
	}
}
func TestCodexReadOnlyTrust(t *testing.T) {
	o := Options{Home: t.TempDir(), Project: t.TempDir(), Scope: "project", Harness: "codex", Executable: "/tmp/agents-bridge"}
	r, err := Apply("install", o)
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(o.Home, ".codex", "config.toml")
	os.MkdirAll(filepath.Dir(cfg), 0700)
	var b strings.Builder
	b.WriteString("[features]\nhooks = true\n")
	for _, e := range r.Entries {
		fmtLine := "[hooks.state." + quoteTOML(e.Key) + "]\nenabled = true\ntrusted_hash = " + quoteTOML(e.CurrentHash) + "\n"
		b.WriteString(fmtLine)
	}
	b.WriteString("[projects." + quoteTOML(o.Project) + "]\ntrust_level = \"trusted\"\n")
	original := []byte(b.String())
	os.WriteFile(cfg, original, 0600)
	r, err = Apply("status", o)
	if err != nil {
		t.Fatal(err)
	}
	if r.HooksEnabled != "true" || r.ProjectTrusted != "true" {
		t.Fatalf("%+v", r)
	}
	for _, e := range r.Entries {
		if e.Trust != "trusted" || !e.Enabled {
			t.Fatalf("%+v", e)
		}
	}
	got, _ := os.ReadFile(cfg)
	if !bytes.Equal(original, got) {
		t.Fatal("wrote trust")
	}
	o.Executable = "/other/agents-bridge"
	Apply("install", o)
	r, _ = Apply("status", o)
	for _, e := range r.Entries {
		if e.Trust != "modified" {
			t.Fatalf("not modified %+v", e)
		}
	}
}

func TestMixedGroupPreservesForeignHandler(t *testing.T) {
	o := Options{Home: t.TempDir(), Scope: "user", Harness: "claude", Executable: "/old/agents-bridge"}
	r, err := Apply("install", o)
	if err != nil {
		t.Fatal(err)
	}
	d, _, _ := readObject(r.Path)
	g := d["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)
	g["hooks"] = append(g["hooks"].([]any), map[string]any{"type": "command", "command": "echo foreign", "timeout": 10})
	g["matcher"] = "custom"
	raw, _ := json.Marshal(d)
	os.WriteFile(r.Path, raw, 0600)
	o.Executable = "/new/agents-bridge"
	r, err = Apply("install", o)
	if err != nil || len(r.Entries) != 5 {
		t.Fatal(err, r)
	}
	r, err = Apply("uninstall", o)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(r.Path)
	if !bytes.Contains(raw, []byte("echo foreign")) || !bytes.Contains(raw, []byte("custom")) || bytes.Contains(raw, []byte("/old/agents-bridge")) {
		t.Fatal(string(raw))
	}
}

func TestCurrentExecutableCanHaveCustomFileName(t *testing.T) {
	o := Options{Home: t.TempDir(), Scope: "user", Harness: "claude", Executable: filepath.Join(t.TempDir(), "probe-engine")}
	r, err := Apply("install", o)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 5 {
		t.Fatal(r)
	}
	r, err = Apply("uninstall", o)
	if err != nil || len(r.Entries) != 0 {
		t.Fatal(err, r)
	}
}
