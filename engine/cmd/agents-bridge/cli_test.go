package main

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// runMainEnv makes the test binary run the real CLI entry point instead of
// the tests, so a test can exercise everything main() does before reaching
// runLocal and friends (theme resolution, flag parsing, signal setup) —
// the path the in-process tests skip, and where v0.3.0–v0.3.2 hung.
const runMainEnv = "AGENTS_BRIDGE_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runCLI starts this test binary as `agents-bridge args...` with its
// descriptors in a temporary directory, and kills it when the test ends.
func runCLI(t *testing.T, args ...string) *bufio.Reader {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	cmd := exec.Command(exe, args...)
	// AGENTS_BRIDGE_THEME empty leaves the theme on "auto", the default that
	// used to query the terminal's background even without a TUI.
	cmd.Env = append(os.Environ(), runMainEnv+"=1", "LOCALAPPDATA="+tmp, "TMPDIR="+tmp, "AGENTS_BRIDGE_THEME=")
	detachConsole(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return bufio.NewReader(stdout)
}

// TestLocalHeadlessCLIPrintsReadyWithoutATerminal is the regression for
// v0.3.0–v0.3.2 on Windows: resolving the "auto" theme asked the console for
// its background color even under --headless, and in a background process
// nothing ever answered, so `local --headless` never printed ready.
func TestLocalHeadlessCLIPrintsReadyWithoutATerminal(t *testing.T) {
	stdout := runCLI(t, "local", "--headless", "--idle-timeout", "0")
	lines := make(chan string, 1)
	go func() {
		line, _ := stdout.ReadString('\n')
		lines <- line
	}()
	select {
	case line := <-lines:
		if !strings.Contains(line, `"type":"ready"`) || !strings.Contains(line, `"mode":"local"`) {
			t.Fatalf("unexpected first line: %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("local --headless printed no ready line within 10s: startup is blocked")
	}
}
