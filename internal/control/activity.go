package control

import (
	"sync"
	"time"
)

// Activity tracks the last moment something happened on a local bridge
// instance, for --idle-timeout (§5.1) and the ps command's IDLE column
// (§5.2). It combines two signals: any message published or received by
// either client (Touch), and orchestrator-side presence (Enter/Leave) — a
// wait or watch currently in flight on the orchestrator endpoint, or an
// observing TUI. While at least one presence is held, LastActivity reports
// "now", so a present orchestrator never looks idle between messages. The
// executor's own wait never registers presence: if the orchestrator
// disappears, the executor must not keep the instance alive forever.
type Activity struct {
	mu           sync.Mutex
	lastActivity time.Time
	presence     int
}

// NewActivity returns an Activity whose clock starts now, so a freshly
// created instance is never immediately considered idle.
func NewActivity() *Activity {
	return &Activity{lastActivity: time.Now()}
}

// Touch records activity at the current time.
func (a *Activity) Touch() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.lastActivity = time.Now()
	a.mu.Unlock()
}

// Enter registers one orchestrator-side connection. Every Enter must be
// paired with exactly one Leave, typically via defer.
func (a *Activity) Enter() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.presence++
	a.mu.Unlock()
}

// Leave releases one connection registered with Enter.
func (a *Activity) Leave() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.presence > 0 {
		a.presence--
	}
	a.mu.Unlock()
}

// LastActivity returns the last recorded activity, or the current time while
// at least one orchestrator-side connection is present.
func (a *Activity) LastActivity() time.Time {
	if a == nil {
		return time.Time{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.presence > 0 {
		return time.Now()
	}
	return a.lastActivity
}
