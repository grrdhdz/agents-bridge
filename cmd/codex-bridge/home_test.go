package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridges"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui/theme"
)

// TestBuildLocalCommandIsAnArgvNeverAShell covers spec §5's launch rules:
// the bridge is started by re-running this very executable with argv
// (nothing goes through a shell), headless, with the ready file, and with
// no --idle-timeout so the default applies.
func TestBuildLocalCommandIsAnArgvNeverAShell(t *testing.T) {
	cmd := buildLocalCommand("/opt/codex-bridge", "/tmp/private/ready.json")
	want := []string{"/opt/codex-bridge", "local", "--headless", "--ready-file", "/tmp/private/ready.json"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q, want %q", cmd.Args, want)
	}
	if cmd.Path != "/opt/codex-bridge" {
		t.Fatalf("the executable is run directly, got Path %q", cmd.Path)
	}
	for _, arg := range cmd.Args {
		if strings.Contains(arg, "idle-timeout") {
			t.Fatal("the default --idle-timeout must apply: do not pass one")
		}
	}
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Fatal("the background bridge must not inherit the TUI's terminal")
	}
}

func TestWaitReadyFileReturnsTheInstanceID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ready.json")
	exited := make(chan struct{})
	go func() {
		time.Sleep(80 * time.Millisecond)
		tmp := path + ".tmp"
		_ = os.WriteFile(tmp, []byte(`{"v":1,"type":"ready","instance_id":"abc123","mode":"local"}`+"\n"), 0o600)
		_ = os.Rename(tmp, path)
	}()
	id, err := waitReadyFile(context.Background(), path, exited, 5*time.Second)
	if err != nil || id != "abc123" {
		t.Fatalf("got %q, %v", id, err)
	}
}

func TestWaitReadyFileFailsWhenTheProcessExitsFirst(t *testing.T) {
	exited := make(chan struct{})
	close(exited)
	_, err := waitReadyFile(context.Background(), filepath.Join(t.TempDir(), "never.json"), exited, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "terminó") {
		t.Fatalf("a bridge that dies before it is ready must be reported, got %v", err)
	}
}

func TestWaitReadyFileTimesOutAndHonoursContext(t *testing.T) {
	never := make(chan struct{})
	if _, err := waitReadyFile(context.Background(), filepath.Join(t.TempDir(), "never.json"), never, 150*time.Millisecond); err == nil {
		t.Fatal("expected a timeout")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitReadyFile(ctx, filepath.Join(t.TempDir(), "never.json"), never, 5*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled context stops the wait, got %v", err)
	}
}

// fakeProc is the injected process starter: it never runs anything. Like
// the real child it publishes the ready file it was told about.
type fakeProc struct {
	started []*exec.Cmd
	killed  bool
	id      string
	die     bool
}

func (f *fakeProc) start(cmd *exec.Cmd) (proc, error) {
	f.started = append(f.started, cmd)
	exited := make(chan struct{})
	readyPath := cmd.Args[len(cmd.Args)-1]
	if f.die {
		close(exited)
	} else if f.id != "" {
		_ = os.WriteFile(readyPath, []byte(`{"v":1,"type":"ready","instance_id":"`+f.id+`","mode":"local"}`+"\n"), 0o600)
	}
	return proc{exited: exited, kill: func() { f.killed = true }}, nil
}

func TestLaunchLocalBridgeUsesAPrivateTempDirAndCleansUp(t *testing.T) {
	f := &fakeProc{id: "dddd4444"}
	id, err := launchLocalBridgeWith(context.Background(), "/opt/codex-bridge", f.start)
	if err != nil || id != "dddd4444" {
		t.Fatalf("got %q, %v", id, err)
	}
	if len(f.started) != 1 {
		t.Fatalf("exactly one process, got %d", len(f.started))
	}
	readyPath := f.started[0].Args[len(f.started[0].Args)-1]
	if _, err := os.Stat(filepath.Dir(readyPath)); !os.IsNotExist(err) {
		t.Fatal("the private temp dir must be removed once the instance_id is known")
	}
	if runtime.GOOS != "windows" {
		// The dir was 0700 when it existed: MkdirTemp guarantees it; assert
		// the intent through the base name instead.
		if !strings.HasPrefix(filepath.Base(filepath.Dir(readyPath)), "codex-bridge-ready-") {
			t.Fatalf("unexpected temp dir %q", readyPath)
		}
	}
	if f.killed {
		t.Fatal("a successful launch is not killed")
	}
}

func TestLaunchLocalBridgeFailureKillsTheChildAndReports(t *testing.T) {
	dies := &fakeProc{die: true}
	if _, err := launchLocalBridgeWith(context.Background(), "/x", dies.start); err == nil {
		t.Fatal("a child that exits before ready is an error")
	}
	stuck := &fakeProc{}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := launchLocalBridgeWith(ctx, "/x", stuck.start); err == nil {
		t.Fatal("a child that never becomes ready is an error")
	}
	if !stuck.killed {
		t.Fatal("a child that never became ready must be killed, not left as an orphan")
	}
}

func TestHomeSourceCreateUsesTheInjectedLauncher(t *testing.T) {
	called := 0
	src := newHomeSource(t.TempDir(), func(context.Context) (string, error) { called++; return "abc", nil })
	id, err := src.Create(context.Background())
	if err != nil || id != "abc" || called != 1 {
		t.Fatalf("got %q %v (%d calls)", id, err, called)
	}
}

func TestHomeSourceListStopAndOpenUseTheSharedCode(t *testing.T) {
	run := startLocal(t)
	id := run.ready["instance_id"].(string)
	src := newHomeSource(run.root, nil)

	infos, err := src.List(context.Background())
	if err != nil || len(infos) != 1 || infos[0].InstanceID != id {
		t.Fatalf("List: %+v %v", infos, err)
	}
	session, err := src.Open(id)
	if err != nil || session.Transport == nil || session.Transport.InstanceID() != id || session.OnStop == nil {
		t.Fatalf("Open: %+v %v", session, err)
	}
	if _, err := src.Open("nope"); err == nil {
		t.Fatal("opening an unknown instance is an error")
	}
	if err := src.Stop(context.Background(), "nope"); !errors.Is(err, bridges.ErrNotFound) {
		t.Fatalf("Stop of an unknown instance: %v", err)
	}
}

// TestRunTUIHomePrintsTheExitNoticeAfterTheTUIEnds drives the App through a
// fake program runner (no terminal, no process): the person creates a
// bridge with n, quits, and the notice is written to stdout afterwards.
func TestRunTUIHomePrintsTheExitNoticeAfterTheTUIEnds(t *testing.T) {
	src := &recordingSource{createID: "dddd4444-0000"}
	var out bytes.Buffer
	order := []string{}
	err := runTUIHome(context.Background(), theme.New(theme.ModeDark, false, nil), src, &out, func(m tea.Model) error {
		order = append(order, "program")
		_, cmd := m.Update(tea.KeyPressMsg{Text: "n", Code: 'n'})
		msg := cmd()
		m.Update(msg) // the create result
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || src.creates != 1 {
		t.Fatalf("setup: expected one program run and one creation, got %v / %d", order, src.creates)
	}
	text := out.String()
	for _, want := range []string{"dddd4444-0000", "codex-bridge stop --instance-id dddd4444-0000"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stdout should mention %q:\n%s", want, text)
		}
	}
}

func TestRunTUIHomeIsSilentWhenNothingWasCreated(t *testing.T) {
	var out bytes.Buffer
	if err := runTUIHome(context.Background(), theme.New(theme.ModeDark, false, nil), &recordingSource{}, &out, func(tea.Model) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("no notice without created bridges, got %q", out.String())
	}
}

type recordingSource struct {
	createID string
	creates  int
	created  bool
}

func (r *recordingSource) List(context.Context) ([]bridges.Info, error) {
	if r.created {
		return []bridges.Info{{InstanceID: r.createID}}, nil
	}
	return nil, nil
}
func (r *recordingSource) Stop(context.Context, string) error { return nil }
func (r *recordingSource) Open(string) (tui.BridgeSession, error) {
	return tui.BridgeSession{}, errors.New("not used")
}
func (r *recordingSource) Create(context.Context) (string, error) {
	r.creates++
	r.created = true
	return r.createID, nil
}

var _ = json.Marshal
