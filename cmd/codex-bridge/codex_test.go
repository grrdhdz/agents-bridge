package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSessionIndex creates CODEX_HOME/session_index.jsonl with one line per
// id (plus a trailing malformed line, to prove it is ignored) and returns
// the CODEX_HOME directory.
func writeSessionIndex(t *testing.T, ids ...string) string {
	t.Helper()
	home := t.TempDir()
	var buf bytes.Buffer
	for _, id := range ids {
		fmt.Fprintf(&buf, `{"id":%q,"title":"demo"}`+"\n", id)
	}
	buf.WriteString("not json at all\n")
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write session_index.jsonl: %v", err)
	}
	return home
}

const testThreadID = "01a0e3fd-1cdb-7ed2-89d1-6559c0a0b32a"

type codexRun struct {
	code           int
	stdout, stderr string
	openedURL      string
	openerCalled   bool
}

func runCodexTest(t *testing.T, stdin string, codexHome string, openerErr error, args ...string) codexRun {
	t.Helper()
	var stdout, stderr bytes.Buffer
	var run codexRun
	opener := func(link string) error {
		run.openerCalled = true
		run.openedURL = link
		return openerErr
	}
	env := codexEnv{stdin: strings.NewReader(stdin), stdout: &stdout, stderr: &stderr, codexHome: codexHome, opener: opener}
	run.code = runCodex(context.Background(), args, env)
	run.stdout, run.stderr = stdout.String(), stderr.String()
	return run
}

func expectSuccess(t *testing.T, run codexRun, wantID string) {
	t.Helper()
	if run.code != 0 {
		t.Fatalf("expected success, got code=%d stderr=%s", run.code, run.stderr)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(run.stdout)), &record); err != nil {
		t.Fatalf("stdout is not JSON: %q (%v)", run.stdout, err)
	}
	if record["type"] != "response" || record["ok"] != true || record["operation"] != "codex-open" {
		t.Fatalf("unexpected response record: %+v", record)
	}
	if record["thread_id"] != wantID {
		t.Fatalf("thread_id = %v, want %s", record["thread_id"], wantID)
	}
	if strings.Contains(run.stdout, "prompt") {
		t.Fatalf("stdout must not leak the prompt: %q", run.stdout)
	}
	if !run.openerCalled {
		t.Fatalf("opener was not invoked")
	}
}

func promptFromURL(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("opener received unparseable URL %q: %v", raw, err)
	}
	if u.Scheme != "codex" || u.Host != "threads" {
		t.Fatalf("opener received unexpected URL %q", raw)
	}
	return u.Query().Get("prompt")
}

func TestCodexOpenValidDeeplink(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "", home, nil, "open", "--thread", "codex://threads/"+testThreadID)
	expectSuccess(t, run, testThreadID)
	if !strings.HasPrefix(run.openedURL, "codex://threads/"+testThreadID+"?") {
		t.Fatalf("opener url = %q", run.openedURL)
	}
	if got := promptFromURL(t, run.openedURL); got != "usa la skill codex-bridge como ejecutor" {
		t.Fatalf("default prompt = %q", got)
	}
}

func TestCodexOpenDeeplinkWithExtraQuery(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "", home, nil, "open", "--thread", "codex://threads/"+testThreadID+"?hostId=abc123")
	expectSuccess(t, run, testThreadID)
}

func TestCodexOpenBareID(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "", home, nil, "open", "--thread", testThreadID)
	expectSuccess(t, run, testThreadID)
}

func TestCodexOpenUppercaseIDNormalizes(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "", home, nil, "open", "--thread", strings.ToUpper(testThreadID))
	expectSuccess(t, run, testThreadID)
	if !strings.Contains(run.openedURL, testThreadID) {
		t.Fatalf("opener url should use lowercase id: %q", run.openedURL)
	}
}

func TestCodexOpenRejectsOtherScheme(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "", home, nil, "open", "--thread", "https://threads/"+testThreadID)
	if run.code != exitUsage || !strings.Contains(run.stderr, "THREAD_INVALID") {
		t.Fatalf("expected THREAD_INVALID/2, got code=%d stderr=%s", run.code, run.stderr)
	}
	if run.openerCalled {
		t.Fatalf("opener must not run on invalid thread ref")
	}
}

func TestCodexOpenRejectsOtherPath(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "", home, nil, "open", "--thread", "codex://review")
	if run.code != exitUsage || !strings.Contains(run.stderr, "THREAD_INVALID") {
		t.Fatalf("expected THREAD_INVALID/2, got code=%d stderr=%s", run.code, run.stderr)
	}
}

func TestCodexOpenRejectsExtraSegments(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "", home, nil, "open", "--thread", "codex://threads/"+testThreadID+"/extra")
	if run.code != exitUsage || !strings.Contains(run.stderr, "THREAD_INVALID") {
		t.Fatalf("expected THREAD_INVALID/2, got code=%d stderr=%s", run.code, run.stderr)
	}
}

func TestCodexOpenRejectsNonUUID(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "", home, nil, "open", "--thread", "not-a-uuid")
	if run.code != exitUsage || !strings.Contains(run.stderr, "THREAD_INVALID") {
		t.Fatalf("expected THREAD_INVALID/2, got code=%d stderr=%s", run.code, run.stderr)
	}
}

func TestCodexOpenUnknownIDNotFound(t *testing.T) {
	home := writeSessionIndex(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	run := runCodexTest(t, "", home, nil, "open", "--thread", testThreadID)
	if run.code != exitNotFound || !strings.Contains(run.stderr, "THREAD_NOT_FOUND") {
		t.Fatalf("expected THREAD_NOT_FOUND/3, got code=%d stderr=%s", run.code, run.stderr)
	}
	if run.openerCalled {
		t.Fatalf("opener must not run when thread is not found")
	}
}

func TestCodexOpenMissingSessionIndex(t *testing.T) {
	home := t.TempDir() // no session_index.jsonl written
	run := runCodexTest(t, "", home, nil, "open", "--thread", testThreadID)
	if run.code != exitNotFound || !strings.Contains(run.stderr, "THREAD_NOT_FOUND") {
		t.Fatalf("expected THREAD_NOT_FOUND/3, got code=%d stderr=%s", run.code, run.stderr)
	}
}

func TestCodexOpenMultilinePromptRoundTrips(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	prompt := "línea uno & dos ? tres # cuatro\nsegunda línea con ñ y acentos áéí"
	run := runCodexTest(t, prompt, home, nil, "open", "--thread", testThreadID, "--prompt-file", "-")
	expectSuccess(t, run, testThreadID)
	if got := promptFromURL(t, run.openedURL); got != prompt {
		t.Fatalf("prompt round-trip mismatch:\n got=%q\nwant=%q", got, prompt)
	}
}

func TestCodexOpenPromptFromFile(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	dir := t.TempDir()
	path := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(path, []byte("revisa el módulo de control"), 0o600); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}
	run := runCodexTest(t, "", home, nil, "open", "--thread", testThreadID, "--prompt-file", path)
	expectSuccess(t, run, testThreadID)
	if got := promptFromURL(t, run.openedURL); got != "revisa el módulo de control" {
		t.Fatalf("prompt from file = %q", got)
	}
}

func TestCodexOpenEmptyPromptIsUsageError(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "   \n\t  \n", home, nil, "open", "--thread", testThreadID, "--prompt-file", "-")
	if run.code != exitUsage || !strings.Contains(run.stderr, "USAGE") {
		t.Fatalf("expected USAGE/2 for empty prompt, got code=%d stderr=%s", run.code, run.stderr)
	}
	if run.openerCalled {
		t.Fatalf("opener must not run for an invalid prompt")
	}
}

func TestCodexOpenOversizedPromptIsUsageError(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	big := strings.Repeat("a", 8*1024+1)
	run := runCodexTest(t, big, home, nil, "open", "--thread", testThreadID, "--prompt-file", "-")
	if run.code != exitUsage || !strings.Contains(run.stderr, "USAGE") {
		t.Fatalf("expected USAGE/2 for oversized prompt, got code=%d stderr=%s", run.code, run.stderr)
	}
}

func TestCodexOpenOpenerFailureExitsInternal(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "", home, fmt.Errorf("boom"), "open", "--thread", testThreadID)
	if run.code != exitInternal {
		t.Fatalf("expected exit 9 on opener failure, got code=%d stderr=%s", run.code, run.stderr)
	}
	if run.code == exitUsage || run.code == exitNotFound {
		t.Fatalf("opener failure must not reuse USAGE/THREAD_NOT_FOUND codes")
	}
}

func TestCodexOpenMissingThreadFlagIsUsage(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "", home, nil, "open", "--thread", "")
	if run.code != exitUsage {
		t.Fatalf("expected USAGE/2 for missing --thread, got code=%d stderr=%s", run.code, run.stderr)
	}
}

func TestCodexUnknownOperationIsUsage(t *testing.T) {
	home := writeSessionIndex(t, testThreadID)
	run := runCodexTest(t, "", home, nil, "close", "--thread", testThreadID)
	if run.code != exitUsage {
		t.Fatalf("expected USAGE/2 for unknown operation, got code=%d stderr=%s", run.code, run.stderr)
	}
}
