package api

import (
	"context"
	"github.com/grrdhdz/agents-bridge/engine/internal/bridges"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"net/http"
	"time"
)

type Instance struct {
	InstanceID      string                          `json:"instance_id"`
	Mode            string                          `json:"mode"`
	Roles           []string                        `json:"roles"`
	PID             int                             `json:"pid"`
	StartedAt       time.Time                       `json:"started_at"`
	Project         string                          `json:"project"`
	Name            string                          `json:"name,omitempty"`
	IdleSeconds     *int64                          `json:"idle_seconds,omitempty"`
	PeerConnected   bool                            `json:"peer_connected"`
	LatestServerSeq uint64                          `json:"latest_server_seq"`
	RoleStates      map[string]control.RoleSnapshot `json:"role_states"`
}

func instance(i bridges.Info) Instance {
	states := map[string]control.RoleSnapshot{}
	for key, role := range i.RoleStates {
		states[key] = control.RoleSnapshot{State: control.RoleState(role.State), HookBound: role.HookBound, Tool: role.Tool, LastHeartbeatAt: role.LastHeartbeatAt, LastMessageAt: role.LastMessageAt}
	}
	return Instance{i.InstanceID, i.Mode, i.Roles, i.PID, i.StartedAt, i.Project, i.Name, i.IdleSeconds, i.PeerConnected, i.LatestServerSeq, states}
}

type Health struct {
	Unread          *int                            `json:"unread,omitempty"`
	InstanceID      string                          `json:"instance_id"`
	State           string                          `json:"state"`
	PID             int                             `json:"pid"`
	PeerConnected   bool                            `json:"peer_connected"`
	FinReceived     bool                            `json:"fin_received"`
	LatestServerSeq uint64                          `json:"latest_server_seq"`
	RoleStates      map[string]control.RoleSnapshot `json:"role_states"`
}

func (s *Server) health(ctx context.Context, d control.Descriptor) (Health, error) {
	var wire struct {
		InstanceID      string                          `json:"instance_id"`
		State           string                          `json:"state"`
		PID             int                             `json:"pid"`
		PeerConnected   bool                            `json:"peer_connected"`
		FinReceived     bool                            `json:"fin_received"`
		LatestServerSeq uint64                          `json:"latest_server_seq"`
		Roles           map[string]control.RoleSnapshot `json:"roles"`
	}
	err := s.call(ctx, d, http.MethodGet, "/v1/health", nil, &wire)
	if wire.Roles == nil {
		wire.Roles = map[string]control.RoleSnapshot{}
	}
	h := Health{InstanceID: wire.InstanceID, State: wire.State, PID: wire.PID, PeerConnected: wire.PeerConnected, FinReceived: wire.FinReceived, LatestServerSeq: wire.LatestServerSeq, RoleStates: wire.Roles}
	if err == nil {
		var peek struct {
			Unread int `json:"unread"`
		}
		if s.call(ctx, d, http.MethodGet, "/v1/peek", nil, &peek) == nil {
			h.Unread = &peek.Unread
		}
	}
	return h, err
}
