package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/control"
)

var managerMu sync.Mutex
var harnesses = []string{"claude", "codex"}

type UserPath interface {
	Read() (string, error)
	Write(string) error
	Notify() error
}
type EnsureOptions struct {
	Home, ConfigDir, LocalAppData, Executable, Version, Platform, Path string
	UserPath                                                           UserPath
	CheckVersion                                                       func(context.Context, string) (string, error)
}
type State struct {
	Version  string          `json:"version"`
	CLIPath  string          `json:"cli_path"`
	OptedOut map[string]bool `json:"opted_out"`
	original []byte
}
type HarnessStatus struct {
	Installed bool   `json:"installed"`
	OptedOut  bool   `json:"opted_out"`
	Path      string `json:"path"`
	TrustNote string `json:"trust_note,omitempty"`
	Error     string `json:"error,omitempty"`
}
type StatusResult struct {
	Harnesses  map[string]HarnessStatus `json:"harnesses"`
	CLIPath    string                   `json:"cli_path"`
	CLIOnPath  bool                     `json:"cli_on_path"`
	CLICurrent bool                     `json:"cli_current"`
	CLIError   string                   `json:"cli_error,omitempty"`
	PathNote   string                   `json:"path_note,omitempty"`
	Skill      SkillStatus              `json:"skill"`
}
type EnsureResult struct {
	StatusResult
	Changed bool `json:"changed"`
}

func (o EnsureOptions) resolved() (EnsureOptions, error) {
	var err error
	if o.Home == "" {
		o.Home, err = os.UserHomeDir()
		if err != nil {
			return o, err
		}
	}
	if o.ConfigDir == "" {
		o.ConfigDir, err = os.UserConfigDir()
		if err != nil {
			return o, err
		}
	}
	if o.Executable == "" {
		o.Executable, err = os.Executable()
		if err != nil {
			return o, err
		}
	}
	if o.LocalAppData == "" {
		o.LocalAppData = os.Getenv("LOCALAPPDATA")
	}
	if o.Platform == "" {
		o.Platform = runtime.GOOS
	}
	if o.Path == "" {
		o.Path = os.Getenv("PATH")
	}
	if o.Platform == "windows" && o.UserPath == nil {
		o.UserPath = platformUserPath()
	}
	if o.CheckVersion == nil {
		o.CheckVersion = checkVersion
	}
	if o.Platform == "windows" && o.LocalAppData == "" {
		return o, errors.New("LOCALAPPDATA no está disponible")
	}
	return o, nil
}
func (o EnsureOptions) cliPath() string {
	if o.Platform == "windows" {
		return filepath.Join(o.LocalAppData, "agents-bridge", "bin", "agents-bridge.exe")
	}
	return filepath.Join(o.Home, ".local", "bin", "agents-bridge")
}
func (o EnsureOptions) statePath() string {
	return filepath.Join(o.ConfigDir, "agents-bridge", "integration-state.json")
}
func loadState(o EnsureOptions) (State, error) {
	s := State{OptedOut: map[string]bool{"claude": false, "codex": false}}
	b, e := os.ReadFile(o.statePath())
	if errors.Is(e, os.ErrNotExist) {
		return s, nil
	}
	if e != nil {
		return s, e
	}
	if json.Unmarshal(b, &s) != nil {
		return s, errors.New("estado de integración inválido; no se modificó")
	}
	if s.OptedOut == nil {
		s.OptedOut = map[string]bool{}
	}
	for _, h := range harnesses {
		if _, ok := s.OptedOut[h]; !ok {
			s.OptedOut[h] = false
		}
	}
	s.original = b
	return s, nil
}
func saveState(o EnsureOptions, s *State) (bool, error) {
	b, e := json.MarshalIndent(s, "", "  ")
	if e != nil {
		return false, e
	}
	b = append(b, '\n')
	if bytes.Equal(b, s.original) {
		return false, nil
	}
	if e = control.EnsurePrivateDir(filepath.Dir(o.statePath())); e != nil {
		return false, e
	}
	latest, e := os.ReadFile(o.statePath())
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return false, e
	}
	if !bytes.Equal(latest, s.original) {
		return false, errors.New("estado cambió durante la edición; no se reemplazó")
	}
	if e = atomicFile(o.statePath(), b, 0600); e != nil {
		return false, e
	}
	s.original = b
	return true, nil
}
func atomicFile(path string, data []byte, mode os.FileMode) error {
	return atomicFileChecked(path, data, mode, nil)
}
func atomicFileChecked(path string, data []byte, mode os.FileMode, before func() error) error {
	nonce := make([]byte, 12)
	if _, e := rand.Read(nonce); e != nil {
		return e
	}
	tmp := fmt.Sprintf("%s.tmp-%x", path, nonce)
	defer os.Remove(tmp)
	if e := writePrivate(tmp, data); e != nil {
		return e
	}
	if e := os.Chmod(tmp, mode); e != nil {
		return e
	}
	if before != nil {
		if e := before(); e != nil {
			return e
		}
	}
	return os.Rename(tmp, path)
}
func legacyConfig(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming")
	default:
		return filepath.Join(home, ".config")
	}
}
func recordChoice(o Options, out bool) error {
	cfg := o.ConfigDir
	if cfg == "" {
		cfg = legacyConfig(o.Home)
	}
	settings := EnsureOptions{Home: o.Home, ConfigDir: cfg}
	s, e := loadState(settings)
	if e != nil {
		return e
	}
	s.OptedOut[o.Harness] = out
	_, e = saveState(settings, &s)
	return e
}
func configured(r Result, path string) bool {
	if len(r.Entries) != len(Events) {
		return false
	}
	seen := map[string]bool{}
	for _, e := range r.Entries {
		if e.Binary != path || seen[e.Event] {
			return false
		}
		seen[e.Event] = true
	}
	for _, e := range Events {
		if !seen[e] {
			return false
		}
	}
	return true
}
func status(o EnsureOptions, s State) StatusResult {
	r := StatusResult{CLIPath: o.cliPath(), Harnesses: map[string]HarnessStatus{}, Skill: skillStatus(o)}
	source, se := os.ReadFile(o.Executable)
	dest, de := os.ReadFile(r.CLIPath)
	info, ie := os.Lstat(r.CLIPath)
	r.CLICurrent = se == nil && de == nil && ie == nil && info.Mode().IsRegular() && sha256.Sum256(source) == sha256.Sum256(dest) && (runtime.GOOS == "windows" || info.Mode().Perm()&0111 != 0)
	r.CLIOnPath = onPath(o, r.CLIPath)
	if !r.CLIOnPath {
		r.PathNote = "La CLI no está en el PATH de esta sesión; los hooks usan su ruta absoluta."
	}
	if o.Platform == "windows" {
		r.PathNote = "Windows: abre una terminal nueva para usar el PATH actualizado."
	}
	for _, h := range harnesses {
		opts := Options{Home: o.Home, ConfigDir: o.ConfigDir, Scope: "user", Harness: h, Executable: r.CLIPath, PreserveOptOut: true}
		result, e := apply("status", opts)
		hs := HarnessStatus{Path: result.Path, OptedOut: s.OptedOut[h], Installed: e == nil && configured(result, r.CLIPath)}
		if e != nil {
			hs.Error = e.Error()
		}
		if h == "codex" {
			for _, entry := range result.Entries {
				if entry.Trust != "trusted" || !entry.Enabled {
					hs.TrustNote = "Codex: acepta los hooks como confiables y marca cada proyecto como trusted."
					break
				}
			}
			if result.HooksEnabled == "false" {
				hs.TrustNote = "Codex: habilita hooks, acepta su confianza y confía en el proyecto."
			}
			if !hs.Installed {
				hs.TrustNote = "Codex: acepta los hooks como confiables y marca cada proyecto como trusted."
			}
		}
		r.Harnesses[h] = hs
	}
	return r
}
func Status(o EnsureOptions) (StatusResult, error) {
	managerMu.Lock()
	defer managerMu.Unlock()
	o, e := o.resolved()
	if e != nil {
		return StatusResult{}, e
	}
	s, e := loadState(o)
	if e != nil {
		return StatusResult{}, e
	}
	return status(o, s), nil
}
func Ensure(ctx context.Context, o EnsureOptions) (EnsureResult, error) {
	managerMu.Lock()
	defer managerMu.Unlock()
	o, e := o.resolved()
	if e != nil {
		return EnsureResult{}, e
	}
	s, e := loadState(o)
	if e != nil {
		return EnsureResult{}, e
	}
	return ensure(ctx, o, &s)
}
func ensure(ctx context.Context, o EnsureOptions, s *State) (EnsureResult, error) {
	r := EnsureResult{StatusResult: status(o, *s)}
	// The skill does not depend on the CLI copy, so a busy CLI cannot block it.
	skill, skillChanged := ensureSkill(o)
	r.Skill = skill
	changed, e := installCLI(ctx, o)
	r.Changed = changed || skillChanged
	if e != nil {
		r.CLIError = e.Error()
		return r, nil
	}
	if o.Platform == "windows" {
		changed, e = ensureUserPath(o.UserPath, filepath.Dir(r.CLIPath))
		r.Changed = r.Changed || changed
		if e != nil {
			r.CLIError = "No se pudo actualizar el PATH de usuario: " + e.Error()
		}
	}
	failures := map[string]string{}
	for _, h := range harnesses {
		if s.OptedOut[h] {
			continue
		}
		if !r.Harnesses[h].Installed || s.Version != o.Version {
			result, e := apply("install", Options{Home: o.Home, ConfigDir: o.ConfigDir, Scope: "user", Harness: h, Executable: r.CLIPath, PreserveOptOut: true})
			r.Changed = r.Changed || result.Changed
			if e != nil {
				failures[h] = e.Error()
			}
		}
	}
	refreshed := status(o, *s)
	refreshed.CLIError = r.CLIError
	refreshed.Skill = skill
	r.StatusResult = refreshed
	for h, e := range failures {
		hs := r.Harnesses[h]
		hs.Error = e
		r.Harnesses[h] = hs
	}
	if len(failures) == 0 && r.CLIError == "" {
		s.Version = o.Version
		s.CLIPath = r.CLIPath
		changed, e = saveState(o, s)
		if e != nil {
			return r, e
		}
		r.Changed = r.Changed || changed
	}
	return r, nil
}
func Set(ctx context.Context, o EnsureOptions, h string, enabled bool) (EnsureResult, error) {
	managerMu.Lock()
	defer managerMu.Unlock()
	if h != "claude" && h != "codex" {
		return EnsureResult{}, errors.New("harness inválido")
	}
	o, e := o.resolved()
	if e != nil {
		return EnsureResult{}, e
	}
	s, e := loadState(o)
	if e != nil {
		return EnsureResult{}, e
	}
	s.OptedOut[h] = !enabled
	changed, e := saveState(o, &s)
	if e != nil {
		return EnsureResult{}, e
	}
	if enabled {
		r, e := ensure(ctx, o, &s)
		r.Changed = r.Changed || changed
		return r, e
	}
	result, err := apply("uninstall", Options{Home: o.Home, ConfigDir: o.ConfigDir, Harness: h, Scope: "user", Executable: o.cliPath(), PreserveOptOut: true})
	r := EnsureResult{StatusResult: status(o, s), Changed: changed || result.Changed}
	if err != nil {
		hs := r.Harnesses[h]
		hs.Error = err.Error()
		r.Harnesses[h] = hs
	}
	return r, nil
}
func installCLI(ctx context.Context, o EnsureOptions) (bool, error) {
	dest := o.cliPath()
	source, e := os.ReadFile(o.Executable)
	if e != nil {
		return false, errors.New("no se pudo leer la CLI del motor")
	}
	info, e := os.Lstat(dest)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return false, e
	}
	present := e == nil
	var original []byte
	if e == nil {
		if !info.Mode().IsRegular() {
			return false, errors.New("destino de CLI ajeno o enlace; no se reemplazó")
		}
		existing, e := os.ReadFile(dest)
		if e != nil {
			return false, e
		}
		original = existing
		if sha256.Sum256(existing) == sha256.Sum256(source) {
			if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
				return true, os.Chmod(dest, 0755)
			}
			return false, nil
		}
		version, e := o.CheckVersion(ctx, dest)
		if e != nil || !strings.HasPrefix(strings.TrimSpace(version), "agents-bridge ") {
			return false, errors.New("destino de CLI ajeno; no responde --version como agents-bridge")
		}
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
		return false, e
	}
	e = atomicFileChecked(dest, source, 0755, func() error {
		latestInfo, err := os.Lstat(dest)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if (err == nil) != present || (err == nil && !latestInfo.Mode().IsRegular()) {
			return errors.New("destino de CLI cambió durante la copia; no se reemplazó")
		}
		latest, err := os.ReadFile(dest)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if !bytes.Equal(latest, original) {
			return errors.New("destino de CLI cambió durante la copia; no se reemplazó")
		}
		return nil
	})
	return e == nil, e
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 1024 {
		limit := 1024 - b.Len()
		if len(p) > limit {
			p = p[:limit]
		}
		b.Buffer.Write(p)
	}
	return n, nil
}
func checkVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.WaitDelay = 250 * time.Millisecond
	out := &boundedOutput{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	e := cmd.Run()
	return out.String(), e
}
func onPath(o EnsureOptions, target string) bool {
	separator := string(os.PathListSeparator)
	if o.Platform == "windows" {
		separator = ";"
	}
	for _, dir := range strings.Split(o.Path, separator) {
		candidate := filepath.Join(strings.Trim(dir, "\""), filepath.Base(target))
		if i, e := os.Stat(candidate); e == nil && i.Mode().IsRegular() {
			a, _ := filepath.EvalSymlinks(candidate)
			b, _ := filepath.EvalSymlinks(target)
			if o.Platform == "windows" {
				return strings.EqualFold(a, b)
			}
			return a == b
		}
	}
	return false
}
func normalizedWindowsDir(s string) string {
	s = strings.Trim(strings.TrimSpace(s), "\"")
	s = strings.ReplaceAll(s, "/", `\`)
	// Expand variables without executing shell commands or changing their spelling in PATH.
	seen := map[string]bool{}
	for !seen[s] {
		seen[s] = true
		start := strings.Index(s, "%")
		if start < 0 {
			break
		}
		end := strings.Index(s[start+1:], "%")
		if end < 0 {
			break
		}
		end += start + 1
		key := s[start+1 : end]
		v := os.Getenv(key)
		if v == "" {
			break
		}
		s = s[:start] + v + s[end+1:]
	}
	return strings.ToLower(strings.TrimRight(s, `\`))
}
func ensureUserPath(store UserPath, dir string) (bool, error) {
	if store == nil {
		return false, errors.New("registro de PATH no disponible")
	}
	value, e := store.Read()
	if e != nil {
		return false, e
	}
	for _, part := range strings.Split(value, ";") {
		if normalizedWindowsDir(part) == normalizedWindowsDir(dir) {
			return false, nil
		}
	}
	updated := value
	if updated != "" && !strings.HasSuffix(updated, ";") {
		updated += ";"
	}
	updated += dir
	if e = store.Write(updated); e != nil {
		return false, e
	}
	return true, store.Notify()
}
