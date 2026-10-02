// Package integration manages harness configuration without touching trust state.
package integration

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
)

var Events = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop"}
var labels = map[string]string{"SessionStart": "session_start", "UserPromptSubmit": "user_prompt_submit", "PreToolUse": "pre_tool_use", "PostToolUse": "post_tool_use", "Stop": "stop"}

type Options struct{ Home, Project, Scope, Harness, Executable string }
type Entry struct {
	Event       string `json:"event"`
	Binary      string `json:"binary"`
	Key         string `json:"key"`
	CurrentHash string `json:"current_hash,omitempty"`
	Enabled     bool   `json:"enabled"`
	Trust       string `json:"trust,omitempty"`
}
type Result struct {
	Path           string  `json:"path"`
	Changed        bool    `json:"changed"`
	Backup         string  `json:"backup,omitempty"`
	Entries        []Entry `json:"entries"`
	HooksEnabled   string  `json:"hooks_enabled,omitempty"`
	ProjectTrusted string  `json:"project_trusted,omitempty"`
	TrustNote      string  `json:"trust_note,omitempty"`
}

func (o Options) Path() (string, error) {
	if o.Harness != "claude" && o.Harness != "codex" {
		return "", errors.New("harness debe ser claude o codex")
	}
	base := o.Home
	switch o.Scope {
	case "user":
	case "project":
		base = o.Project
	default:
		return "", errors.New("scope debe ser user o project")
	}
	if base == "" {
		return "", errors.New("falta directorio de configuración")
	}
	file := "hooks.json"
	if o.Harness == "claude" {
		file = "settings.json"
	}
	return filepath.Abs(filepath.Join(base, "."+o.Harness, file))
}

// Quote a single executable for the harness command shell. Windows paths use
// double quotes; POSIX paths use single quotes so shell metacharacters stay data.
func HookCommand(exe, harness, event string) string {
	quoted := "'" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "'"
	if windowsAbsolute(exe) {
		quoted = "\"" + exe + "\""
	}
	return quoted + " hook " + harness + " " + event
}
func windowsAbsolute(s string) bool {
	return len(s) > 2 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') || strings.HasPrefix(s, `\\`)
}
func OwnedCommand(command, harness, event string) (string, bool) {
	suffix := " hook " + harness + " " + event
	if !strings.HasSuffix(command, suffix) {
		return "", false
	}
	prefix := strings.TrimSuffix(command, suffix)
	var exe string
	if strings.HasPrefix(prefix, "'") && strings.HasSuffix(prefix, "'") {
		exe = strings.ReplaceAll(prefix[1:len(prefix)-1], "'\"'\"'", "'")
	} else if strings.HasPrefix(prefix, "\"") && strings.HasSuffix(prefix, "\"") {
		exe = prefix[1 : len(prefix)-1]
	} else {
		exe = prefix
		if strings.ContainsAny(exe, " \t\n'\";$`|&<>") {
			return "", false
		}
	}
	if !filepath.IsAbs(exe) && !windowsAbsolute(exe) {
		return "", false
	}
	base := filepath.Base(strings.ReplaceAll(exe, "\\", "/"))
	if base != "agents-bridge" && base != "agents-bridge.exe" {
		return "", false
	}
	// Re-encoding protects against compound shell expressions disguised as paths.
	if prefix != strings.TrimSuffix(HookCommand(exe, harness, event), suffix) && prefix != exe {
		return "", false
	}
	return exe, true
}

func readObject(path string) (map[string]any, []byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var d map[string]any
	if err = dec.Decode(&d); err != nil || d == nil {
		return nil, nil, errors.New("configuración JSON inválida; archivo intacto")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, nil, errors.New("configuración JSON inválida; archivo intacto")
	}
	return d, raw, nil
}
func Apply(action string, o Options) (Result, error) {
	path, err := o.Path()
	r := Result{Path: path, Entries: []Entry{}}
	if err != nil {
		return r, err
	}
	if action != "install" && action != "uninstall" && action != "status" {
		return r, errors.New("acción debe ser install, uninstall o status")
	}
	if action == "install" && (!filepath.IsAbs(o.Executable) && !windowsAbsolute(o.Executable)) {
		return r, errors.New("el binario debe tener ruta absoluta")
	}
	// Ownership must remain identifiable on the next installation.
	if action == "install" {
		if _, ok := OwnedCommand(HookCommand(o.Executable, o.Harness, "Stop"), o.Harness, "Stop"); !ok {
			return r, errors.New("el binario debe llamarse agents-bridge o agents-bridge.exe")
		}
	}
	d, original, err := readObject(path)
	if err != nil {
		return r, err
	}
	before, _ := json.Marshal(d)
	hooks, ok := d["hooks"].(map[string]any)
	if !ok {
		if _, exists := d["hooks"]; exists {
			return r, errors.New("hooks debe ser un objeto; archivo intacto")
		}
		hooks = map[string]any{}
	}
	for _, event := range Events {
		raw, exists := hooks[event]
		groups, valid := raw.([]any)
		if exists && !valid {
			return r, errors.New("las entradas de hooks deben ser arrays; archivo intacto")
		}
		kept := []any{}
		for gi, g := range groups {
			group, valid := g.(map[string]any)
			if !valid {
				return r, errors.New("grupo de hook inválido; archivo intacto")
			}
			hs, valid := group["hooks"].([]any)
			if !valid {
				return r, errors.New("handlers de hook inválidos; archivo intacto")
			}
			remaining := []any{}
			for hi, h := range hs {
				handler, valid := h.(map[string]any)
				if !valid {
					return r, errors.New("handler de hook inválido; archivo intacto")
				}
				cmd, _ := handler["command"].(string)
				exe, owned := OwnedCommand(cmd, o.Harness, event)
				if typ, _ := handler["type"].(string); typ != "command" {
					owned = false
				}
				if !owned || action == "status" {
					remaining = append(remaining, h)
				}
				if owned && action == "status" {
					r.Entries = append(r.Entries, entry(path, event, gi, hi, exe, group, handler))
				}
			}
			if action == "status" || len(remaining) > 0 || len(hs) == 0 {
				group["hooks"] = remaining
				kept = append(kept, group)
			}
		}
		if action == "install" {
			group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": HookCommand(o.Executable, o.Harness, event), "timeout": 5}}}
			if event == "PreToolUse" || event == "PostToolUse" {
				group["matcher"] = "*"
			}
			kept = append(kept, group)
		}
		if action != "status" {
			if len(kept) > 0 {
				hooks[event] = kept
			} else {
				delete(hooks, event)
			}
		}
	}
	if action != "status" {
		if len(hooks) > 0 {
			d["hooks"] = hooks
		} else {
			delete(d, "hooks")
		}
		after, _ := json.Marshal(d)
		if !bytes.Equal(before, after) {
			encoded, err := json.MarshalIndent(d, "", "  ")
			if err != nil || !json.Valid(encoded) {
				return r, errors.New("resultado JSON inválido")
			}
			encoded = append(encoded, '\n')
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return r, err
			}
			if original != nil {
				r.Backup = path + ".bak-" + time.Now().UTC().Format("20060102T150405.000000000Z")
				if err = writePrivate(r.Backup, original); err != nil {
					return r, err
				}
			}
			nonce := make([]byte, 12)
			if _, err = rand.Read(nonce); err != nil {
				return r, err
			}
			tmp := fmt.Sprintf("%s.tmp-%x", path, nonce)
			defer os.Remove(tmp)
			if err = writePrivate(tmp, encoded); err != nil {
				return r, err
			}
			// Detect edits made by another integration before replacing its config.
			latest, readErr := os.ReadFile(path)
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				return r, readErr
			}
			if !bytes.Equal(latest, original) {
				return r, errors.New("configuración cambió durante la edición; no se reemplazó")
			}
			if err = os.Rename(tmp, path); err != nil {
				return r, err
			}
			r.Changed = true
		}
		changed, backup := r.Changed, r.Backup
		r, err = Apply("status", o)
		r.Changed = changed
		r.Backup = backup
		return r, err
	}
	if o.Harness == "codex" {
		readTrust(&r, o)
	}
	return r, nil
}
func writePrivate(path string, data []byte) error {
	f, err := control.CreatePrivateFile(path)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}
func entry(path, event string, gi, hi int, exe string, group, handler map[string]any) Entry {
	// Codex hashes sorted JSON of the normalized TOML identity. Optional None
	// fields disappear; async=false is explicit in the normalized command.
	normalized := map[string]any{"type": "command", "command": handler["command"], "async": false}
	for _, k := range []string{"commandWindows", "timeout", "async", "statusMessage", "additionalContextLimit"} {
		if v, ok := handler[k]; ok && v != nil {
			normalized[k] = v
		}
	}
	if v, ok := handler["command_windows"]; ok {
		normalized["commandWindows"] = v
	}
	identity := map[string]any{"event_name": labels[event], "hooks": []any{normalized}}
	if matcher, ok := group["matcher"]; ok && matcher != nil {
		identity["matcher"] = matcher
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(identity)
	sum := sha256.Sum256(bytes.TrimSuffix(b.Bytes(), []byte{'\n'}))
	return Entry{Event: event, Binary: exe, Key: fmt.Sprintf("%s:%s:%d:%d", path, labels[event], gi, hi), CurrentHash: fmt.Sprintf("sha256:%x", sum), Enabled: true, Trust: "untrusted"}
}
func quoteTOML(s string) string                         { b, _ := json.Marshal(s); return string(b) }
func table(d map[string]any, key string) map[string]any { v, _ := d[key].(map[string]any); return v }
func readTrust(r *Result, o Options) {
	r.HooksEnabled = "default (depende de la versión de Codex)"
	r.ProjectTrusted = "no aplica"
	if o.Scope == "project" {
		r.ProjectTrusted = "false"
	}
	raw, err := os.ReadFile(filepath.Join(o.Home, ".codex", "config.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		r.TrustNote = "no se pudo leer config.toml"
		return
	}
	var d map[string]any
	if _, err = toml.Decode(string(raw), &d); err != nil {
		r.HooksEnabled = "unknown"
		r.ProjectTrusted = "unknown"
		r.TrustNote = "config.toml inválido; no se modificó"
		for i := range r.Entries {
			r.Entries[i].Trust = "unknown"
		}
		return
	}
	if enabled, ok := table(d, "features")["hooks"].(bool); ok {
		r.HooksEnabled = fmt.Sprint(enabled)
	}
	if o.Scope == "project" {
		project, _ := filepath.Abs(o.Project)
		if table(table(d, "projects"), project)["trust_level"] == "trusted" {
			r.ProjectTrusted = "true"
		}
	}
	state := table(table(d, "hooks"), "state")
	for i := range r.Entries {
		e := &r.Entries[i]
		s := table(state, e.Key)
		if s == nil {
			s = table(state, "file:"+e.Key)
		}
		if enabled, ok := s["enabled"].(bool); ok {
			e.Enabled = enabled
		}
		if hash, ok := s["trusted_hash"].(string); ok {
			e.Trust = "modified"
			if reflect.DeepEqual(hash, e.CurrentHash) {
				e.Trust = "trusted"
			}
		}
	}
	r.TrustNote = "solo configuración de usuario; políticas administradas y flags de sesión pueden cambiar el estado efectivo"
}
