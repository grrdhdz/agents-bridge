package control

import (
	"sync"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

// RoleState is what a role is doing, as far as this bridge can tell (§3.4 of
// the 2026-09-29 spec). The values are the Spanish words shown to users, so
// ps, /v1/health and the TUI all print exactly the same thing.
type RoleState string

const (
	// StateWaiting: a wait is in flight for the role.
	StateWaiting RoleState = "esperando"
	// StateWorking: no wait in flight, but the role waited or sent a
	// message within roleQuietAfter.
	StateWorking RoleState = "trabajando"
	// StateQuiet: no wait in flight and no activity for roleQuietAfter.
	StateQuiet RoleState = "callado"
	// StateUnknown: not enough is known yet (or the role is not tracked).
	StateUnknown RoleState = "—"
)

// roleQuietAfter is how long a role without a wait in flight may go without
// activity before it stops counting as "trabajando".
const roleQuietAfter = 15 * time.Minute

// RoleKey is the stable JSON key for a role in /v1/health and ps.
func RoleKey(role protocol.Role) string {
	if role == protocol.RoleOrchestrator {
		return "orchestrator"
	}
	return "executor"
}

// RoleFromKey is RoleKey's inverse; ok is false for an unknown key.
func RoleFromKey(key string) (protocol.Role, bool) {
	switch key {
	case "orchestrator":
		return protocol.RoleOrchestrator, true
	case "executor":
		return protocol.RoleExecutor, true
	}
	return "", false
}

// RoleSnapshot is one role's derived state plus the two timestamps it came
// from; the pointers are nil while nothing was recorded.
type RoleSnapshot struct {
	HookBound       bool       `json:"hook_bound"`
	State           RoleState  `json:"state"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	Tool            string     `json:"tool,omitempty"`
	LastMessageAt   *time.Time `json:"last_message_at,omitempty"`
	LastWaitAt      *time.Time `json:"last_wait_at,omitempty"`
}

type roleRecord struct {
	hookSessions map[string]bool
	waiting      int
	lastWait     time.Time
	lastMsg      time.Time
	lastBeat     time.Time
	tool         string
}

// Roles is the per-role state registry of one bridge instance (§3.4). In
// `local` both control endpoints share one Roles (like Activity), so each
// knows the state of both roles; in host and join each process tracks only
// its own role. Only tracked roles are recorded, and only in RAM. The clock
// is injectable so the "trabajando"/"callado" boundary is testable.
type Roles struct {
	mu      sync.Mutex
	now     func() time.Time
	since   time.Time
	tracked map[protocol.Role]*roleRecord
	// name is the bridge's human label. Roles is the one object `local` shares
	// between both endpoints, so the label lives here.
	name     string
	onRename []func()
}

// Name returns the bridge's label ("" when unnamed).
func (r *Roles) Name() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.name
}

// SetName stores an already normalized label and refreshes every endpoint's
// descriptor. Listeners run outside the lock: they read Name themselves.
func (r *Roles) SetName(name string) {
	r.mu.Lock()
	r.name = name
	listeners := append([]func(){}, r.onRename...)
	r.mu.Unlock()
	for _, f := range listeners {
		f()
	}
}

// OnRename registers f to run after each SetName.
func (r *Roles) OnRename(f func()) {
	r.mu.Lock()
	r.onRename = append(r.onRename, f)
	r.mu.Unlock()
}

// NewRoles tracks the given roles. A nil now uses time.Now. The tracking
// start time is what separates "unknown" from "callado" for a role that has
// never done anything: silence shorter than 15 minutes proves nothing yet.
func NewRoles(now func() time.Time, tracked ...protocol.Role) *Roles {
	if now == nil {
		now = time.Now
	}
	r := &Roles{now: now, since: now(), tracked: make(map[protocol.Role]*roleRecord, len(tracked))}
	for _, role := range tracked {
		r.tracked[role] = &roleRecord{}
	}
	return r
}

// WaitStart records that a wait began for role. Every WaitStart must be
// paired with one WaitEnd. Untracked roles are ignored.
func (r *Roles) WaitStart(role protocol.Role) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.tracked[role]; rec != nil {
		rec.waiting++
		rec.tool = ""
		rec.lastWait = r.now()
	}
}

// WaitEnd records that a wait ended, however it ended.
func (r *Roles) WaitEnd(role protocol.Role) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.tracked[role]; rec != nil {
		if rec.waiting > 0 {
			rec.waiting--
		}
		rec.lastWait = r.now()
	}
}

// Message records that role sent a message at at. The latest time wins, so
// replaying the same journal twice (both local endpoints do) is harmless.
func (r *Roles) Message(role protocol.Role, at time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.tracked[role]; rec != nil && at.After(rec.lastMsg) {
		rec.lastMsg = at
	}
}

// Heartbeat records finite activity for either role; it never holds presence.
func (r *Roles) Heartbeat(role protocol.Role, tool string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.tracked[role]; rec != nil {
		rec.lastBeat = r.now()
		rec.tool = tool
	}
}

// Snapshot derives every tracked role's state at the registry's clock.
func (r *Roles) Snapshot() map[protocol.Role]RoleSnapshot {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	out := make(map[protocol.Role]RoleSnapshot, len(r.tracked))
	for role, rec := range r.tracked {
		snap := RoleSnapshot{HookBound: len(rec.hookSessions) > 0}
		latest := time.Time{}
		if !rec.lastMsg.IsZero() {
			t := rec.lastMsg
			snap.LastMessageAt = &t
			latest = t
		}
		if !rec.lastWait.IsZero() {
			t := rec.lastWait
			snap.LastWaitAt = &t
			if t.After(latest) {
				latest = t
			}
		}
		if !rec.lastBeat.IsZero() {
			t := rec.lastBeat
			snap.LastHeartbeatAt = &t
			if t.After(latest) {
				latest = t
			}
		}
		switch {
		case rec.waiting > 0:
			snap.State = StateWaiting
		case !latest.IsZero() && now.Sub(latest) < roleQuietAfter:
			snap.State = StateWorking
			if latest.Equal(rec.lastBeat) {
				snap.Tool = rec.tool
			}
		case !latest.IsZero() || now.Sub(r.since) >= roleQuietAfter:
			snap.State = StateQuiet
		default:
			snap.State = StateUnknown
		}
		out[role] = snap
	}
	return out
}

// HookBinding tracks independent bound sessions without persisting identities.
func (r *Roles) HookBinding(role protocol.Role, session string, bound bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.tracked[role]; rec != nil {
		if rec.hookSessions == nil {
			rec.hookSessions = map[string]bool{}
		}
		if bound {
			rec.hookSessions[session] = true
		} else {
			delete(rec.hookSessions, session)
		}
	}
}
