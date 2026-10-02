package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memoryPath struct {
	value           string
	writes, notices int
}

func (p *memoryPath) Read() (string, error) { return p.value, nil }
func (p *memoryPath) Write(s string) error  { p.value = s; p.writes++; return nil }
func (p *memoryPath) Notify() error         { p.notices++; return nil }
func isolatedEnsure(t *testing.T) EnsureOptions {
	t.Helper()
	root := t.TempDir()
	for k, v := range map[string]string{"HOME": root, "USERPROFILE": root, "APPDATA": filepath.Join(root, "config"), "XDG_CONFIG_HOME": filepath.Join(root, "config"), "LOCALAPPDATA": filepath.Join(root, "local"), "PATH": filepath.Join(root, "path")} {
		t.Setenv(k, v)
	}
	exe := filepath.Join(root, "source")
	os.WriteFile(exe, []byte("engine-one"), 0700)
	return EnsureOptions{Home: root, ConfigDir: filepath.Join(root, "config"), LocalAppData: filepath.Join(root, "local"), Executable: exe, Version: "v0.5.1", Platform: "darwin", Path: filepath.Join(root, ".local", "bin"), CheckVersion: func(context.Context, string) (string, error) { return "agents-bridge v0.5.1", nil }}
}
func TestEnsureInstallIdempotenceUpgradeRepairAndExclusion(t *testing.T) {
	o := isolatedEnsure(t)
	ctx := context.Background()
	for _, h := range []string{"claude", "codex"} {
		p, _ := (Options{Home: o.Home, Scope: "user", Harness: h}).Path()
		os.MkdirAll(filepath.Dir(p), 0700)
		os.WriteFile(p, []byte(`{"env":{"KEEP":"yes"},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/frozen/codex-bridge hook claude Stop"}]}]}}`), 0600)
	}
	r, err := Ensure(ctx, o)
	if err != nil || !r.Changed || !r.CLICurrent || !r.CLIOnPath || !r.Harnesses["claude"].Installed || !r.Harnesses["codex"].Installed {
		t.Fatal(r, err)
	}
	snap := map[string]string{}
	filepath.Walk(o.Home, func(p string, i os.FileInfo, e error) error {
		if e == nil && !i.IsDir() {
			b, _ := os.ReadFile(p)
			snap[p] = string(b) + i.ModTime().String()
		}
		return nil
	})
	r, err = Ensure(ctx, o)
	if err != nil || r.Changed {
		t.Fatal(r, err)
	}
	filepath.Walk(o.Home, func(p string, i os.FileInfo, e error) error {
		if e == nil && !i.IsDir() {
			b, _ := os.ReadFile(p)
			if snap[p] != string(b)+i.ModTime().String() {
				t.Errorf("wrote on no-op: %s", p)
			}
		}
		return nil
	})
	before, _ := os.ReadFile(r.Harnesses["claude"].Path)
	o.Version = "v0.5.2"
	os.WriteFile(o.Executable, []byte("engine-two"), 0700)
	r, err = Ensure(ctx, o)
	after, _ := os.ReadFile(r.Harnesses["claude"].Path)
	if err != nil || !r.Changed || string(before) != string(after) {
		t.Fatal("upgrade changed command", r, err)
	}
	os.Remove(r.Harnesses["codex"].Path)
	r, err = Ensure(ctx, o)
	if err != nil || !r.Harnesses["codex"].Installed || !r.Changed {
		t.Fatal(r, err)
	}
	r, err = Set(ctx, o, "claude", false)
	if err != nil || !r.Harnesses["claude"].OptedOut || r.Harnesses["claude"].Installed {
		t.Fatal(r, err)
	}
	o.Version = "v0.5.3"
	r, err = Ensure(ctx, o)
	if err != nil || r.Harnesses["claude"].Installed || !r.Harnesses["claude"].OptedOut {
		t.Fatal(r, err)
	}
	raw, _ := os.ReadFile(r.Harnesses["claude"].Path)
	if !strings.Contains(string(raw), "/frozen/codex-bridge") {
		t.Fatal("foreign removed")
	}
	r, err = Set(ctx, o, "claude", true)
	if err != nil || !r.Harnesses["claude"].Installed || r.Harnesses["claude"].OptedOut {
		t.Fatal(r, err)
	}
	var state State
	raw, _ = os.ReadFile(filepath.Join(o.ConfigDir, "agents-bridge", "integration-state.json"))
	if json.Unmarshal(raw, &state) != nil || state.Version != o.Version || state.CLIPath != r.CLIPath {
		t.Fatal(string(raw))
	}
}
func TestEnsureForeignCLIInvalidJSONAndWrongHookPath(t *testing.T) {
	o := isolatedEnsure(t)
	r, _ := Ensure(context.Background(), o)
	os.WriteFile(r.CLIPath, []byte("foreign"), 0700)
	o.CheckVersion = func(context.Context, string) (string, error) { return "not-agents-bridge", nil }
	got, err := Ensure(context.Background(), o)
	b, _ := os.ReadFile(r.CLIPath)
	if err != nil || got.CLIError == "" || string(b) != "foreign" {
		t.Fatal(got, err)
	}
	os.Remove(r.CLIPath)
	os.WriteFile(r.Harnesses["claude"].Path, []byte("{bad"), 0600)
	got, err = Ensure(context.Background(), o)
	b, _ = os.ReadFile(r.Harnesses["claude"].Path)
	if err != nil || got.Harnesses["claude"].Error == "" || string(b) != "{bad" || !got.Harnesses["codex"].Installed {
		t.Fatal(got, err)
	}
	os.Remove(r.Harnesses["claude"].Path)
	Apply("install", Options{Home: o.Home, ConfigDir: o.ConfigDir, Harness: "claude", Scope: "user", Executable: filepath.Join(o.Home, "old", "agents-bridge")})
	got, err = Ensure(context.Background(), o)
	if err != nil || !got.Harnesses["claude"].Installed || !got.Changed {
		t.Fatal(got, err)
	}
	raw, _ := json.Marshal(got)
	for _, secret := range []string{"capability", "control_url", "trusted_hash", "token"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal(string(raw))
		}
	}
}
func TestManualUninstallOptOutAndInstallReset(t *testing.T) {
	o := isolatedEnsure(t)
	Ensure(context.Background(), o)
	opts := Options{Home: o.Home, ConfigDir: o.ConfigDir, Harness: "codex", Scope: "user", Executable: o.Executable}
	if _, err := Apply("uninstall", opts); err != nil {
		t.Fatal(err)
	}
	r, err := Ensure(context.Background(), o)
	if err != nil || r.Harnesses["codex"].Installed || !r.Harnesses["codex"].OptedOut {
		t.Fatal(r, err)
	}
	if _, err := Apply("install", opts); err != nil {
		t.Fatal(err)
	}
	r, err = Ensure(context.Background(), o)
	if err != nil || !r.Harnesses["codex"].Installed || r.Harnesses["codex"].OptedOut {
		t.Fatal(r, err)
	}
}
func TestWindowsPathPreservesAndDeduplicates(t *testing.T) {
	o := isolatedEnsure(t)
	o.Platform = "windows"
	store := &memoryPath{value: `C:\tools;%SystemRoot%\System32`}
	o.UserPath = store
	r, err := Ensure(context.Background(), o)
	if err != nil || store.writes != 1 || store.notices != 1 || !strings.HasSuffix(r.CLIPath, "agents-bridge.exe") || !strings.HasPrefix(store.value, `C:\tools;%SystemRoot%\System32;`) {
		t.Fatal(r, err, store)
	}
	Ensure(context.Background(), o)
	if store.writes != 1 || store.notices != 1 {
		t.Fatal(store)
	}
	for _, value := range []string{`C:\Tools;"C:/USERS/ME/AppData/Local/agents-bridge/bin/"`, `C:\Tools;%LOCALAPPDATA%\agents-bridge\bin`} {
		t.Setenv("LOCALAPPDATA", `C:\Users\me\AppData\Local`)
		p := &memoryPath{value: value}
		changed, err := ensureUserPath(p, `C:\Users\me\AppData\Local\agents-bridge\bin`)
		if err != nil || changed || p.writes != 0 {
			t.Fatal(value, p, err)
		}
	}
}
func TestEnsureRepairsExecutePermissionAndStateIsPrivate(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX permissions")
	}
	o := isolatedEnsure(t)
	r, e := Ensure(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	os.Chmod(r.CLIPath, 0600)
	r, e = Ensure(context.Background(), o)
	i, _ := os.Stat(r.CLIPath)
	if e != nil || !r.Changed || i.Mode().Perm() != 0755 {
		t.Fatal(r, e, i.Mode())
	}
	i, _ = os.Stat(o.statePath())
	if i.Mode().Perm() != 0600 {
		t.Fatal(i.Mode())
	}
	i, _ = os.Stat(filepath.Dir(o.statePath()))
	if i.Mode().Perm() != 0700 {
		t.Fatal(i.Mode())
	}
}
func TestInvalidStateAndSymlinkDestinationUntouched(t *testing.T) {
	o := isolatedEnsure(t)
	os.MkdirAll(filepath.Dir(o.statePath()), 0700)
	os.WriteFile(o.statePath(), []byte("{bad"), 0600)
	if _, e := Ensure(context.Background(), o); e == nil {
		t.Fatal("invalid state accepted")
	}
	if _, e := os.Stat(o.cliPath()); !os.IsNotExist(e) {
		t.Fatal("wrote CLI despite invalid state")
	}
	os.Remove(o.statePath())
	os.MkdirAll(filepath.Dir(o.cliPath()), 0700)
	target := filepath.Join(o.Home, "foreign")
	os.WriteFile(target, []byte("foreign"), 0600)
	if e := os.Symlink(target, o.cliPath()); e != nil {
		t.Skip(e)
	}
	r, e := Ensure(context.Background(), o)
	b, _ := os.ReadFile(target)
	if e != nil || r.CLIError == "" || string(b) != "foreign" {
		t.Fatal(r, e)
	}
}

func TestConcurrentReplacementDoesNotOverwriteForeignCLI(t *testing.T) {
	o := isolatedEnsure(t)
	r, _ := Ensure(context.Background(), o)
	os.WriteFile(r.CLIPath, []byte("old-engine"), 0700)
	o.CheckVersion = func(context.Context, string) (string, error) {
		os.WriteFile(r.CLIPath, []byte("concurrent-foreign"), 0700)
		return "agents-bridge v0.5.0", nil
	}
	got, e := Ensure(context.Background(), o)
	raw, _ := os.ReadFile(r.CLIPath)
	if e != nil || got.CLIError == "" || string(raw) != "concurrent-foreign" {
		t.Fatal(got, e, string(raw))
	}
}
