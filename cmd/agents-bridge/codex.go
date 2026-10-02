package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/grrdhdz/agents-bridge/internal/control"
	"github.com/grrdhdz/agents-bridge/internal/protocol"
)

// codexOpenPromptPrefix is always written into the Codex message box, with
// the target instance_id appended: it points the executor at the same skill
// this bridge already documents, and ties its activation to one instance so
// the executor never has to guess or switch instances on its own.
const codexOpenPromptPrefix = "usa la skill agents-bridge como ejecutor con --instance-id "

// maxCodexOpenPromptBytes bounds --prompt-file input so a runaway file
// cannot end up as an oversized deeplink.
const maxCodexOpenPromptBytes = 8 * 1024

var threadUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// codexEnv carries process I/O, CODEX_HOME, the descriptor root and the URL
// opener so tests can run codex without touching the real Codex app or the
// production descriptor directory. root is the descriptor root override
// (empty in production, a private test directory otherwise).
type codexEnv struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	codexHome      string
	root           string
	opener         func(string) error
}

// runCodex executes one `codex` operation, sharing runCtl's JSONL error
// convention on stderr and exit codes via reportFailure.
func runCodex(ctx context.Context, args []string, env codexEnv) int {
	return reportFailure(dispatchCodex(ctx, args, env), env.stderr)
}

func dispatchCodex(_ context.Context, args []string, env codexEnv) error {
	if len(args) == 0 {
		return failure("USAGE", "codex requires an operation: open")
	}
	operation, args := args[0], args[1:]
	if operation != "open" {
		return failure("USAGE", "unknown codex operation %q", operation)
	}

	flags := flag.NewFlagSet("agents-bridge codex open", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	thread := flags.String("thread", "", "codex://threads/<id> deeplink, or the bare thread id")
	instanceID := flags.String("instance-id", "", "agents-bridge instance_id the executor must activate against")
	promptFile := flags.String("prompt-file", "", "UTF-8 prompt file, or - for stdin")
	if err := flags.Parse(args); err != nil {
		return failure("USAGE", "%v", err)
	}
	if flags.NArg() > 0 {
		return failure("USAGE", "unexpected argument %q", flags.Arg(0))
	}
	if strings.TrimSpace(*thread) == "" {
		return failure("USAGE", "open requires --thread")
	}
	if strings.TrimSpace(*instanceID) == "" {
		return failure("USAGE", "--instance-id is required")
	}

	id, err := parseThreadRef(*thread)
	if err != nil {
		return err
	}

	found, err := threadExists(env.codexHome, id)
	if err != nil {
		return failure("INTERNAL", "read session_index.jsonl: %v", err)
	}
	if !found {
		return failure("THREAD_NOT_FOUND", "thread %s not found in session_index.jsonl", id)
	}

	if _, err := control.SelectDescriptor(env.root, *instanceID, protocol.RoleExecutor); err != nil {
		switch err.Error() {
		case "INSTANCE_NOT_FOUND", "INSTANCE_AMBIGUOUS":
			return failure("INSTANCE_NOT_FOUND", "no live executor instance %s: %v", *instanceID, err)
		default:
			return failure("INTERNAL", "%v", err)
		}
	}

	prompt, err := readCodexOpenPrompt(env, *instanceID, *promptFile)
	if err != nil {
		return err
	}

	deeplink := "codex://threads/" + id + "?" + (url.Values{"prompt": {prompt}}).Encode()

	opener := env.opener
	if opener == nil {
		opener = defaultCodexOpener
	}
	if err := opener(deeplink); err != nil {
		return failure("OPENER_ERROR", "open Codex: %v", err)
	}

	record, err := json.Marshal(map[string]any{"v": 1, "type": "response", "ok": true, "operation": "codex-open", "thread_id": id})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(env.stdout, string(record))
	return err
}

// parseThreadRef accepts a codex://threads/<id> deeplink (ignoring any
// query string) or a bare id, and normalizes it to a lowercase UUID.
func parseThreadRef(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", failure("THREAD_INVALID", "thread reference is empty")
	}
	if !strings.Contains(raw, "://") {
		if !threadUUIDPattern.MatchString(raw) {
			return "", failure("THREAD_INVALID", "thread id %q is not a UUID", raw)
		}
		return strings.ToLower(raw), nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", failure("THREAD_INVALID", "invalid deeplink: %v", err)
	}
	if parsed.Scheme != "codex" {
		return "", failure("THREAD_INVALID", "unsupported scheme %q", parsed.Scheme)
	}
	if parsed.Host != "threads" {
		return "", failure("THREAD_INVALID", "unsupported deeplink %q", raw)
	}
	segment := strings.Trim(parsed.Path, "/")
	if segment == "" || strings.Contains(segment, "/") {
		return "", failure("THREAD_INVALID", "deeplink must reference exactly one thread id")
	}
	if !threadUUIDPattern.MatchString(segment) {
		return "", failure("THREAD_INVALID", "thread id %q is not a UUID", segment)
	}
	return strings.ToLower(segment), nil
}

// threadExists scans CODEX_HOME/session_index.jsonl line by line (ignoring
// lines that are not valid JSON, or that have no "id" field) looking for id.
func threadExists(codexHome, id string) (bool, error) {
	path := filepath.Join(codexHome, "session_index.jsonl")
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var record struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		if strings.EqualFold(record.ID, id) {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

// readCodexOpenPrompt always starts with codexOpenPromptPrefix+instanceID, so
// the executor's activation always carries the target instance. With no
// --prompt-file, that line is the whole prompt; otherwise the file's
// trimmed, UTF-8 contents are appended after a blank line. The 8 KiB limit
// applies to the combined prompt.
func readCodexOpenPrompt(env codexEnv, instanceID, promptFile string) (string, error) {
	base := codexOpenPromptPrefix + instanceID
	if promptFile == "" {
		return base, nil
	}
	var raw []byte
	var err error
	if promptFile == "-" {
		raw, err = io.ReadAll(env.stdin)
	} else {
		raw, err = os.ReadFile(promptFile)
	}
	if err != nil {
		return "", failure("USAGE", "read prompt: %v", err)
	}
	if !utf8.Valid(raw) {
		return "", failure("USAGE", "prompt must be valid UTF-8")
	}
	extra := strings.TrimSpace(string(raw))
	if extra == "" {
		return "", failure("USAGE", "prompt is empty")
	}
	prompt := base + "\n\n" + extra
	if len(prompt) > maxCodexOpenPromptBytes {
		return "", failure("USAGE", "prompt exceeds %d bytes", maxCodexOpenPromptBytes)
	}
	return prompt, nil
}

// defaultCodexOpener shells out to the platform's URL handler. It always
// runs with separate argv entries (never a shell), so a prompt containing
// "&" or other shell metacharacters cannot be reinterpreted.
func defaultCodexOpener(deeplink string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", deeplink).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", deeplink).Run()
	default:
		return exec.Command("xdg-open", deeplink).Run()
	}
}

// resolveCodexHome mirrors the Codex CLI's own default: $CODEX_HOME, or
// ~/.codex when unset.
func resolveCodexHome() string {
	if home := os.Getenv("CODEX_HOME"); strings.TrimSpace(home) != "" {
		return home
	}
	if dir, err := os.UserHomeDir(); err == nil {
		return filepath.Join(dir, ".codex")
	}
	return ""
}
