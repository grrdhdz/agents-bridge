// Package hooks holds harness session metadata; it does not depend on clients
// such as the TUI or desktop API. No message bodies or credentials are persisted.
package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

var ErrNotBound = errors.New("session is not bound")

type Binding struct {
	LastNotifiedEventSeq uint64        `json:"last_notified_event_seq,omitempty"`
	StopBlocks           int           `json:"stop_blocks,omitempty"`
	Harness              string        `json:"harness"`
	SessionID            string        `json:"session_id"`
	InstanceID           string        `json:"instance_id"`
	Role                 protocol.Role `json:"role"`
	BoundAt              time.Time     `json:"bound_at"`
}

type Store struct {
	Root           string
	DescriptorRoot string
}

func validIdentity(harness, session string) bool {
	return (harness == "claude" || harness == "codex") && session != "" && len(session) <= 512 && !strings.ContainsRune(session, 0)
}
func (s Store) root() (string, error) {
	if s.Root != "" {
		return s.Root, nil
	}
	root, err := control.RuntimeRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "bindings"), nil
}
func (s Store) path(harness, session string) (string, error) {
	if !validIdentity(harness, session) {
		return "", errors.New("invalid harness or session")
	}
	root, err := s.root()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(session))
	return filepath.Join(root, harness+"-"+hex.EncodeToString(hash[:])+".json"), nil
}
func (s Store) lock(ctx context.Context, path string) (func(), error) {
	if err := control.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	lockPath := path + ".lock"
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := control.CreatePrivateFile(lockPath)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, err := os.Lstat(lockPath); err == nil && time.Since(info.ModTime()) > time.Minute && control.VerifyOwnerOnly(lockPath) == nil {
			_ = os.Remove(lockPath)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}
func (s Store) read(path, harness, session string) (Binding, error) {
	if err := control.VerifyOwnerOnly(path); err != nil {
		if os.IsNotExist(err) {
			return Binding{}, ErrNotBound
		}
		return Binding{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Binding{}, err
	}
	var b Binding
	if err := json.Unmarshal(data, &b); err != nil {
		return b, err
	}
	if b.Harness != harness || b.SessionID != session || !validIdentity(b.Harness, b.SessionID) || (b.Role != protocol.RoleExecutor && b.Role != protocol.RoleOrchestrator) || b.InstanceID == "" {
		return b, errors.New("invalid binding metadata")
	}
	return b, nil
}
func (s Store) write(path string, b Binding) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	id, err := control.NewID()
	if err != nil {
		return err
	}
	tmp := path + "." + id + ".tmp"
	f, err := control.CreatePrivateFile(tmp)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(tmp) }()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func (s Store) Bind(ctx context.Context, b Binding) error {
	path, err := s.path(b.Harness, b.SessionID)
	if err != nil {
		return err
	}
	if b.Role != protocol.RoleExecutor && b.Role != protocol.RoleOrchestrator {
		return errors.New("invalid role")
	}
	if _, err := control.FindDescriptor(s.DescriptorRoot, b.InstanceID, b.Role); err != nil {
		return err
	}
	unlock, err := s.lock(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	old, err := s.read(path, b.Harness, b.SessionID)
	if err == nil && old.InstanceID == b.InstanceID && old.Role == b.Role {
		return nil
	}
	if err != nil && !errors.Is(err, ErrNotBound) {
		return err
	}
	b.BoundAt = time.Now().UTC()
	return s.write(path, b)
}
func (s Store) Lookup(ctx context.Context, harness, session string) (Binding, error) {
	path, err := s.path(harness, session)
	if err != nil {
		return Binding{}, err
	}
	// An unbound session never creates runtime metadata and never uses network.
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return Binding{}, ErrNotBound
	}
	unlock, err := s.lock(ctx, path)
	if err != nil {
		return Binding{}, err
	}
	defer unlock()
	b, err := s.read(path, harness, session)
	if err != nil {
		return b, err
	}
	alive, err := s.bridgeAlive(ctx, b)
	if err != nil {
		return b, err
	}
	if !alive {
		_ = os.Remove(path)
		return Binding{}, ErrNotBound
	}
	return b, nil
}
func (s Store) Unbind(ctx context.Context, harness, session string) error {
	path, err := s.path(harness, session)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	}
	unlock, err := s.lock(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	if err := control.VerifyOwnerOnly(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Remove(path)
}
func (s Store) List(ctx context.Context) ([]Binding, error) {
	root, err := s.root()
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return []Binding{}, nil
	}
	if err := control.EnsurePrivateDir(root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := []Binding{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if err := control.VerifyOwnerOnly(path); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var b Binding
		if err := json.Unmarshal(data, &b); err != nil {
			return nil, err
		}
		expected, err := s.path(b.Harness, b.SessionID)
		if err != nil || expected != path {
			return nil, errors.New("invalid binding filename")
		}
		b, err = s.Lookup(ctx, b.Harness, b.SessionID)
		if errors.Is(err, ErrNotBound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Harness != out[j].Harness {
			return out[i].Harness < out[j].Harness
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out, nil
}

// Update serializes session decisions across independent hook processes. Only
// cursors and counters are stored; context-injected message text stays in RAM.
func (s Store) Update(ctx context.Context, harness, session string, fn func(*Binding) error) error {
	path, err := s.path(harness, session)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return ErrNotBound
	}
	unlock, err := s.lock(ctx, path)
	if err != nil {
		return err
	}
	defer unlock()
	b, err := s.read(path, harness, session)
	if err != nil {
		return err
	}
	if err = fn(&b); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return s.write(path, b)
}

// A crashed process can leave an expired descriptor. Probe only that target,
// under the caller's deadline; a timeout is uncertainty, not proof of closure.
func (s Store) bridgeAlive(ctx context.Context, b Binding) (bool, error) {
	d, err := control.FindDescriptor(s.DescriptorRoot, b.InstanceID, b.Role)
	if errors.Is(err, control.ErrInstanceNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if time.Now().Before(d.ExpiresAt) {
		return true, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	res, err := control.Do(probeCtx, d, http.MethodGet, "/v1/health", nil)
	if err != nil {
		var op *net.OpError
		if ctx.Err() == nil && errors.As(err, &op) && !op.Timeout() {
			return false, nil
		}
		return false, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false, errors.New("expired binding health rejected")
	}
	return true, nil
}
