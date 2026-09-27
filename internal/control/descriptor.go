package control

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

const DescriptorVersion = 1

type Descriptor struct {
	DescriptorVersion int           `json:"descriptor_version"`
	InstanceID        string        `json:"instance_id"`
	PID               int           `json:"pid"`
	LocalRole         protocol.Role `json:"local_role"`
	ControlURL        string        `json:"control_url"`
	Capability        string        `json:"capability"`
	CWD               string        `json:"cwd"`
	StartedAt         time.Time     `json:"started_at"`
	HeartbeatAt       time.Time     `json:"heartbeat_at"`
	ExpiresAt         time.Time     `json:"expires_at"`

	// path is where the descriptor was read from; it is never serialized.
	path string
}

// descriptorFileName keeps one file per role, so a local instance can publish
// both its orchestrator and executor endpoints under one instance_id.
func descriptorFileName(instanceID string, role protocol.Role) string {
	return instanceID + "-" + string(role) + ".json"
}

type Endpoint struct {
	client         *bridge.Client
	listener       net.Listener
	server         *http.Server
	descriptor     Descriptor
	descriptorDir  string
	descriptorPath string
	capability     string
	closeOnce      sync.Once
	done           chan struct{}
	descriptorMu   sync.RWMutex

	// waitMu admits one /v1/wait per endpoint and guards consumed, the RAM
	// cursor of the last peer message this role received through wait.
	waitMu   sync.Mutex
	waiting  atomic.Bool
	consumed uint64
}

func listenLoopback() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }

// Start creates one loopback control endpoint and its protected ephemeral
// descriptor. The caller owns client.Close; Endpoint.Close only tears down the
// local control surface and descriptor.
func Start(client *bridge.Client, role protocol.Role, cwd string) (*Endpoint, error) {
	return startWithRoot(client, role, cwd, "")
}

func StartWithRoot(client *bridge.Client, role protocol.Role, cwd, root string) (*Endpoint, error) {
	return startWithRoot(client, role, cwd, root)
}

func startWithRoot(client *bridge.Client, role protocol.Role, cwd, root string) (*Endpoint, error) {
	if client == nil {
		return nil, errors.New("control endpoint requires a client")
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	capability, err := randomCapability()
	if err != nil {
		return nil, fmt.Errorf("create control capability: %w", err)
	}
	listener, err := listenLoopback()
	if err != nil {
		return nil, fmt.Errorf("listen control endpoint: %w", err)
	}
	dir, err := descriptorDir(root)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	now := time.Now().UTC()
	descriptor := Descriptor{
		DescriptorVersion: DescriptorVersion,
		InstanceID:        client.InstanceID(),
		PID:               os.Getpid(),
		LocalRole:         role,
		ControlURL:        "http://" + listener.Addr().String(),
		Capability:        capability,
		CWD:               cwd,
		StartedAt:         now,
		HeartbeatAt:       now,
		ExpiresAt:         now.Add(15 * time.Second),
	}
	e := &Endpoint{
		client:         client,
		listener:       listener,
		descriptor:     descriptor,
		descriptorDir:  dir,
		descriptorPath: filepath.Join(dir, descriptorFileName(descriptor.InstanceID, role)),
		capability:     capability,
		done:           make(chan struct{}),
	}
	mux := http.NewServeMux()
	e.registerHandlers(mux)
	e.server = &http.Server{Handler: mux}
	if err := writeDescriptor(e.descriptorPath, descriptor); err != nil {
		_ = listener.Close()
		return nil, err
	}
	go func() {
		_ = e.server.Serve(listener)
	}()
	go e.heartbeat()
	return e, nil
}

func randomCapability() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func descriptorDir(override string) (string, error) {
	root := override
	if root == "" {
		var err error
		root, err = platformDescriptorRoot()
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create descriptor directory: %w", err)
	}
	// MkdirAll leaves an existing directory untouched, so a pre-created or
	// tampered directory must be checked before a capability is written to it.
	if err := verifyPrivateDir(root); err != nil {
		return "", fmt.Errorf("ctl disabled: descriptor directory %s: %w", root, err)
	}
	return root, nil
}

func writeDescriptor(path string, descriptor Descriptor) error {
	data, err := json.Marshal(descriptor)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".descriptor-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}

func (e *Endpoint) Descriptor() Descriptor { return e.descriptor }

func (e *Endpoint) Close() {
	e.closeOnce.Do(func() {
		close(e.done)
		if e.server != nil {
			_ = e.server.Close()
		}
		if e.listener != nil {
			_ = e.listener.Close()
		}
		_ = os.Remove(e.descriptorPath)
	})
}

func (e *Endpoint) heartbeat() {
	// Descriptor heartbeats are deliberately best-effort. The endpoint remains
	// usable if the metadata refresh is temporarily unavailable.
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		select {
		case <-e.done:
			return
		default:
		}
		if e.server == nil {
			return
		}
		e.descriptorMu.Lock()
		e.descriptor.HeartbeatAt = time.Now().UTC()
		e.descriptor.ExpiresAt = e.descriptor.HeartbeatAt.Add(15 * time.Second)
		descriptor := e.descriptor
		e.descriptorMu.Unlock()
		if err := writeDescriptor(e.descriptorPath, descriptor); err != nil {
			return
		}
	}
}

type ListedInstance struct {
	InstanceID  string        `json:"instance_id"`
	LocalRole   protocol.Role `json:"local_role"`
	State       string        `json:"state"`
	PID         int           `json:"pid"`
	StartedAt   time.Time     `json:"started_at"`
	HeartbeatAt time.Time     `json:"heartbeat_at"`
	EventSeq    uint64        `json:"event_seq"`
	LatestSeq   uint64        `json:"latest_server_seq"`
	OldestSeq   uint64        `json:"oldest_event_seq"`
	Connected   bool          `json:"peer_connected"`
}

func descriptorRootForTests(root string) string { return root }

func platformDescriptorRoot() (string, error) {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = os.TempDir()
		}
		user := os.Getenv("USERNAME")
		if user == "" {
			user = "current-user"
		}
		return filepath.Join(base, "Temp", "codex-bridge", user, "instances"), nil
	}
	return filepath.Join(os.TempDir(), "codex-bridge", strconv.Itoa(os.Getuid()), "instances"), nil
}

func readDescriptor(path string) (Descriptor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Descriptor{}, err
	}
	var descriptor Descriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return Descriptor{}, err
	}
	if descriptor.DescriptorVersion != DescriptorVersion || descriptor.InstanceID == "" || descriptor.Capability == "" {
		return Descriptor{}, errors.New("invalid descriptor")
	}
	return descriptor, nil
}

func listDescriptors(root string) ([]Descriptor, error) {
	dir, err := descriptorDir(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	result := make([]Descriptor, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		descriptor, err := readDescriptor(path)
		if err != nil {
			continue
		}
		descriptor.path = path
		result = append(result, descriptor)
	}
	return result, nil
}

// ListDescriptors performs best-effort stale cleanup before returning the
// current user's live descriptors. It never exposes capabilities to callers
// that only need discovery metadata.
func ListDescriptors(root string) ([]Descriptor, error) {
	if err := cleanupStaleDescriptors(root, time.Now().UTC()); err != nil {
		return nil, err
	}
	return listDescriptors(root)
}

// SelectDescriptor filters by instance_id, then role, and falls back to cwd
// equality only when no instance_id was given. A local instance publishes one
// descriptor per role with the same cwd, so callers there must pass a role.
func SelectDescriptor(root, instanceID string, role protocol.Role, cwd string) (Descriptor, error) {
	descriptors, err := ListDescriptors(root)
	if err != nil {
		return Descriptor{}, err
	}
	matches := make([]Descriptor, 0, 1)
	for _, descriptor := range descriptors {
		if instanceID != "" && descriptor.InstanceID != instanceID {
			continue
		}
		if role != "" && descriptor.LocalRole != role {
			continue
		}
		if instanceID == "" && descriptor.CWD != cwd {
			continue
		}
		matches = append(matches, descriptor)
	}
	switch len(matches) {
	case 0:
		return Descriptor{}, errors.New("INSTANCE_NOT_FOUND")
	case 1:
		return matches[0], nil
	default:
		return Descriptor{}, errors.New("INSTANCE_AMBIGUOUS")
	}
}

func cleanupStaleDescriptors(root string, now time.Time) error {
	descriptors, err := listDescriptors(root)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 500 * time.Millisecond, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	for _, descriptor := range descriptors {
		if now.Before(descriptor.ExpiresAt) {
			continue
		}
		request, err := http.NewRequest(http.MethodGet, descriptor.ControlURL+"/v1/health", nil)
		if err == nil && isLoopbackURL(descriptor.ControlURL) {
			request.Header.Set("Authorization", "Bearer "+descriptor.Capability)
			request.Header.Set("X-Codex-Bridge-Request-ID", "stale-check")
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
				continue
			}
		}
		_ = os.Remove(descriptor.path)
	}
	return nil
}

func isLoopbackURL(raw string) bool {
	return strings.HasPrefix(raw, "http://127.0.0.1:") || strings.HasPrefix(raw, "http://localhost:")
}

var _ = http.ErrServerClosed
