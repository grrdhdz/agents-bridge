// Package bridges is the one place that turns this user's live control
// descriptors into "the list of running bridges" (plus the two things you
// can do to one from outside: pick the endpoint to stop it and stop it). It
// exists so `codex-bridge ps`, `codex-bridge stop` and the TUI's home screen
// (spec §5: "misma fuente que ps") share one implementation instead of two
// copies that could drift apart. It only ever reads descriptor metadata and
// /v1/health, and never exposes control_url or capability; cwd is reduced
// to its base name (Info.Project).
package bridges

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
)

// ErrNotFound reports that no live descriptor of this user has the
// requested instance_id.
var ErrNotFound = errors.New("instance not found")

// healthTimeout bounds each instance's /v1/health probe, so one wedged
// endpoint can never stall the whole listing.
const healthTimeout = 500 * time.Millisecond

// Info is one live bridge (every role descriptor of one instance_id,
// grouped).
type Info struct {
	InstanceID string
	Mode       string
	Roles      []string
	PID        int
	StartedAt  time.Time
	// IdleSeconds is nil when no descriptor for this instance ever recorded
	// activity (e.g. a legacy process from before last_activity_at
	// existed), so a reader can show "-" instead of a misleading "0s".
	IdleSeconds     *int64
	PeerConnected   bool
	LatestServerSeq uint64
	// RoleStates is each role's state as the probed endpoint reports it (§3.4),
	// keyed "orchestrator"/"executor". A local instance reports both; a host
	// or join only its own role, so a missing key means unknown.
	RoleStates map[string]RoleInfo
	// Project is the base name of the descriptor's cwd ("" when unknown).
	Project string
}

// RoleInfo is one role's derived state and when it last sent a message.
type RoleInfo struct {
	State         string
	LastMessageAt *time.Time
}

// List reads this user's live descriptors under root ("" means the
// platform default) and describes each bridge.
func List(ctx context.Context, root string) ([]Info, error) {
	descriptors, err := control.ListDescriptors(root)
	if err != nil {
		return nil, err
	}
	return FromDescriptors(ctx, descriptors), nil
}

// FromDescriptors groups descriptors by instance_id (a local instance's
// two role descriptors become one row, while separate instances stay
// separate rows even if a crashed process left only one role behind),
// sorted by instance_id, probing every instance's health concurrently.
func FromDescriptors(ctx context.Context, descriptors []control.Descriptor) []Info {
	byInstance := make(map[string][]control.Descriptor, len(descriptors))
	order := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		if _, seen := byInstance[d.InstanceID]; !seen {
			order = append(order, d.InstanceID)
		}
		byInstance[d.InstanceID] = append(byInstance[d.InstanceID], d)
	}
	sort.Strings(order)
	infos := make([]Info, len(order))
	var wg sync.WaitGroup
	for i, id := range order {
		wg.Add(1)
		go func() {
			defer wg.Done()
			infos[i] = describe(ctx, byInstance[id])
		}()
	}
	wg.Wait()
	return infos
}

func describe(ctx context.Context, ds []control.Descriptor) Info {
	info := Info{InstanceID: ds[0].InstanceID}
	var chosen control.Descriptor
	haveOrchestrator, haveExecutor := false, false
	var lastActivity *time.Time
	for i, d := range ds {
		if d.LocalRole == protocol.RoleOrchestrator {
			haveOrchestrator = true
			chosen = d
		} else if d.LocalRole == protocol.RoleExecutor {
			haveExecutor = true
			if !haveOrchestrator {
				chosen = d
			}
		}
		if i == 0 || d.StartedAt.Before(info.StartedAt) {
			info.StartedAt = d.StartedAt
		}
		if d.LastActivityAt != nil && (lastActivity == nil || d.LastActivityAt.After(*lastActivity)) {
			lastActivity = d.LastActivityAt
		}
		if info.Mode == "" {
			info.Mode = string(d.Mode)
		}
		if info.Project == "" && d.CWD != "" {
			info.Project = filepath.Base(d.CWD)
		}
		info.PID = d.PID
	}
	if haveOrchestrator {
		info.Roles = append(info.Roles, "orchestrator")
	}
	if haveExecutor {
		info.Roles = append(info.Roles, "executor")
	}
	if lastActivity != nil {
		idle := int64(time.Since(*lastActivity).Seconds())
		if idle < 0 {
			idle = 0
		}
		info.IdleSeconds = &idle
	}

	healthCtx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	if response, err := control.Do(healthCtx, chosen, http.MethodGet, "/v1/health", nil); err == nil {
		var health struct {
			PeerConnected   bool   `json:"peer_connected"`
			LatestServerSeq uint64 `json:"latest_server_seq"`
			Roles           map[string]struct {
				State         string     `json:"state"`
				LastMessageAt *time.Time `json:"last_message_at"`
			} `json:"roles"`
		}
		if response.StatusCode == http.StatusOK && json.NewDecoder(response.Body).Decode(&health) == nil {
			info.PeerConnected = health.PeerConnected
			info.LatestServerSeq = health.LatestServerSeq
			if len(health.Roles) > 0 {
				info.RoleStates = make(map[string]RoleInfo, len(health.Roles))
				for key, role := range health.Roles {
					info.RoleStates[key] = RoleInfo{State: role.State, LastMessageAt: role.LastMessageAt}
				}
			}
		}
		_ = response.Body.Close()
	}
	return info
}

// SelectStopDescriptor picks the one endpoint on this machine allowed to
// stop the instance (§4.2): the orchestrator's in local and
// tailscale-host, the executor's in tailscale-join, where it is the only
// descriptor present here. It never falls back across machines: an
// instance not found in this user's descriptor directory is ErrNotFound.
func SelectStopDescriptor(descriptors []control.Descriptor, instanceID string) (control.Descriptor, error) {
	var matches []control.Descriptor
	for _, d := range descriptors {
		if d.InstanceID == instanceID {
			matches = append(matches, d)
		}
	}
	if len(matches) == 0 {
		return control.Descriptor{}, ErrNotFound
	}
	for _, d := range matches {
		if d.LocalRole == protocol.RoleOrchestrator {
			return d, nil
		}
	}
	return matches[0], nil
}

// ObserverDescriptor picks the orchestrator's descriptor for instanceID
// when it lives on this machine, otherwise the executor's (§6): a
// join-side machine only ever has the executor's descriptor to attach
// through, and that is enough to watch and intervene.
func ObserverDescriptor(root, instanceID string) (control.Descriptor, error) {
	descriptor, err := control.SelectDescriptor(root, instanceID, protocol.RoleOrchestrator)
	if err == nil {
		return descriptor, nil
	}
	return control.SelectDescriptor(root, instanceID, protocol.RoleExecutor)
}

// Stop asks the instance to close through its control endpoint (POST
// /v1/stop, §4.2), the same request `codex-bridge stop` makes. It returns
// ErrNotFound when this user has no live descriptor for instanceID.
func Stop(ctx context.Context, root, instanceID string) error {
	descriptors, err := control.ListDescriptors(root)
	if err != nil {
		return err
	}
	target, err := SelectStopDescriptor(descriptors, instanceID)
	if err != nil {
		return err
	}
	response, err := control.Do(ctx, target, http.MethodPost, "/v1/stop", nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("control endpoint unreachable for instance %s (pid %d)", target.InstanceID, target.PID)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusAccepted {
		return nil
	}
	data, _ := io.ReadAll(response.Body)
	var record struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &record) == nil && record.Code != "" {
		return fmt.Errorf("%s: %s", record.Code, record.Message)
	}
	return fmt.Errorf("stop rejected (HTTP %d)", response.StatusCode)
}
