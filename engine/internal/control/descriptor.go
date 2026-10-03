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

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

const DescriptorVersion = 1

// Mode identifies the kind of process publishing a descriptor (§3). It is
// informational only: ps groups and labels instances by it, and cleanup and
// selection never depend on it, so an old descriptor without it still works.
type Mode string

const (
	ModeLocal         Mode = "local"
	ModeTailscaleHost Mode = "tailscale-host"
	ModeTailscaleJoin Mode = "tailscale-join"
)

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
	Mode              Mode          `json:"mode,omitempty"`
	// LastActivityAt is a pointer so a legacy descriptor without it, or one
	// from a role that reports no Activity, serializes as an absent field
	// instead of the zero time — ps (and any other reader) can then tell
	// "never recorded" apart from "recorded at the zero instant".
	LastActivityAt *time.Time `json:"last_activity_at,omitempty"`
	// Name is the human label given to the bridge; empty falls back to the
	// cwd's base name. It lives only as long as the bridge.
	Name string `json:"name,omitempty"`

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

	canStop       bool
	stop          func()
	peerConnected func() bool
	activity      *Activity
	roles         *Roles
	cursorMu      sync.Mutex // guards consumed and the send guard's check+publish
	scanMu        sync.Mutex
	scanned       uint64 // last journal event replayed into roles

	// waitMu admits one /v1/wait per endpoint. consumed is the RAM cursor of
	// the last peer message this role received through wait; it is guarded by
	// cursorMu (see loadConsumed), not by waitMu, so a guarded send never
	// queues behind a long wait.
	waitMu      sync.Mutex
	waiting     atomic.Bool
	consumed    uint64
	finReceived bool

	// watchCount admits at most maxConcurrentWatch concurrent /v1/watch
	// subscribers per endpoint (§8, §10.3).
	watchCount atomic.Int32
}

// maxConcurrentWatch caps /v1/watch subscribers per endpoint; the next one
// gets CONTROL_BACKPRESSURE (429, exit 7) instead of growing without bound.
const maxConcurrentWatch = 8

func listenLoopback() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }

// Options configures one control endpoint. It replaces the loose parameters
// Start used to take, since a process now needs to describe more about
// itself: its mode (§3), whether its role is allowed to stop the bridge
// (§4.2), how to report peer_connected correctly per mode (§4.1), and the
// shared Activity it contributes to and reads from (§5.1).
type Options struct {
	// Role is the local role this endpoint serves. It defaults to the
	// client's own role when left empty.
	Role protocol.Role
	// Mode identifies the surrounding process for the descriptor (§3).
	Mode Mode
	// CWD defaults to os.Getwd() when empty.
	CWD string
	// Root overrides the platform descriptor directory; empty uses it.
	Root string
	// CanStop authorizes POST /v1/stop for this endpoint (§4.2).
	CanStop bool
	// Stop is invoked asynchronously after a stop request is accepted. It is
	// required when CanStop is true.
	Stop func()
	// PeerConnected reports whether the other side of this bridge is
	// connected, computed correctly for this endpoint's mode (§4.1). Falls
	// back to the client's own Connected() when nil.
	PeerConnected func() bool
	// Activity is the shared activity tracker for this instance (§5.1). It
	// may be nil, in which case idle tracking and presence are no-ops.
	Activity *Activity
	// Roles is the per-role state registry (§3.4). `local` shares one between
	// both endpoints; nil makes the endpoint track just its own role.
	Roles *Roles
}

// Start creates one loopback control endpoint and its protected ephemeral
// descriptor. The caller owns client.Close; Endpoint.Close only tears down the
// local control surface and descriptor.
func Start(client *bridge.Client, opts Options) (*Endpoint, error) {
	if client == nil {
		return nil, errors.New("control endpoint requires a client")
	}
	role := opts.Role
	if role == "" {
		role = client.Role()
	}
	cwd := opts.CWD
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
	dir, err := descriptorDir(opts.Root)
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
		Mode:              opts.Mode,
		LastActivityAt:    &now,
	}
	roles := opts.Roles
	if roles == nil {
		roles = NewRoles(nil, role)
	}
	descriptor.Name = roles.Name()
	e := &Endpoint{
		client:         client,
		listener:       listener,
		descriptor:     descriptor,
		descriptorDir:  dir,
		descriptorPath: filepath.Join(dir, descriptorFileName(descriptor.InstanceID, role)),
		capability:     capability,
		done:           make(chan struct{}),
		canStop:        opts.CanStop,
		stop:           opts.Stop,
		peerConnected:  opts.PeerConnected,
		activity:       opts.Activity,
		roles:          roles,
	}
	mux := http.NewServeMux()
	e.registerHandlers(mux)
	e.server = &http.Server{Handler: mux}
	if err := writeDescriptor(e.descriptorPath, descriptor); err != nil {
		_ = listener.Close()
		return nil, err
	}
	// local shares Roles between both endpoints: a rename on either one
	// rewrites both descriptors at once instead of on the next heartbeat.
	roles.OnRename(func() { _ = e.heartbeatOnce() })
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
	if err := makePrivateDir(root); err != nil {
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
	suffix, err := randomCapability()
	if err != nil {
		return err
	}
	// CreatePrivateFile rather than os.CreateTemp: the temporary file must be
	// owner-only from the moment it exists (0600, or the owner-only DACL on
	// Windows), and the rename below keeps those permissions.
	tmpName := filepath.Join(filepath.Dir(path), ".descriptor-"+suffix[:16]+".tmp")
	tmp, err := CreatePrivateFile(tmpName)
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
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

// isPeerConnected reports whether the other role is connected, using the
// mode-correct callback from Options when given (§4.1), falling back to the
// client's own connection state otherwise.
func (e *Endpoint) isPeerConnected() bool {
	if e.peerConnected != nil {
		return e.peerConnected()
	}
	return e.client.Connected()
}

func (e *Endpoint) Close() {
	e.closeOnce.Do(func() {
		close(e.done)
		if e.server != nil {
			_ = e.server.Close()
		}
		if e.listener != nil {
			_ = e.listener.Close()
		}
		// Under descriptorMu so a concurrent refresh cannot recreate the file.
		e.descriptorMu.Lock()
		_ = os.Remove(e.descriptorPath)
		e.descriptorMu.Unlock()
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
		if err := e.heartbeatOnce(); err != nil {
			return
		}
	}
}

// heartbeatOnce refreshes the descriptor; it never writes after Close.
func (e *Endpoint) heartbeatOnce() error {
	e.descriptorMu.Lock()
	defer e.descriptorMu.Unlock()
	select {
	case <-e.done:
		return nil
	default:
	}
	e.descriptor.HeartbeatAt = time.Now().UTC()
	e.descriptor.ExpiresAt = e.descriptor.HeartbeatAt.Add(15 * time.Second)
	if e.activity != nil {
		t := e.activity.LastActivity().UTC()
		e.descriptor.LastActivityAt = &t
	}
	e.descriptor.Name = e.roles.Name()
	return writeDescriptor(e.descriptorPath, e.descriptor)
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
		return filepath.Join(base, "Temp", "agents-bridge", user, "instances"), nil
	}
	return filepath.Join(os.TempDir(), "agents-bridge", strconv.Itoa(os.Getuid()), "instances"), nil
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

// SelectDescriptor filters the current user's live descriptors by
// instance_id, required, and by role when given. It never falls back to cwd:
// an instance_id always accompanies commands, messages and activation, and is
// never inferred from the working directory. A local instance publishes one
// descriptor per role under the same instance_id, so with no role and more
// than one descriptor for that instance, the selection is ambiguous.
func SelectDescriptor(root, instanceID string, role protocol.Role) (Descriptor, error) {
	descriptors, err := ListDescriptors(root)
	if err != nil {
		return Descriptor{}, err
	}
	matches := make([]Descriptor, 0, 1)
	for _, descriptor := range descriptors {
		if descriptor.InstanceID != instanceID {
			continue
		}
		if role != "" && descriptor.LocalRole != role {
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
			request.Header.Set("X-Agents-Bridge-Request-ID", "stale-check")
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
