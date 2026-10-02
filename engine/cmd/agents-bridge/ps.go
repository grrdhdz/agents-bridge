package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridges"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
)

// psRow is one line of `ps` output: every live bridge grouped by instance_id
// (§4.1), never exposing control_url, capability or cwd.
type psRow struct {
	InstanceID string   `json:"instance_id"`
	Mode       string   `json:"mode,omitempty"`
	Roles      []string `json:"roles"`
	PID        int      `json:"pid"`
	StartedAt  string   `json:"started_at"`
	// IdleSeconds is nil when no descriptor for this instance ever recorded
	// activity (e.g. a legacy process from before last_activity_at existed),
	// so ps can show "-" instead of a misleading "0s".
	IdleSeconds     *int64                 `json:"idle_seconds,omitempty"`
	PeerConnected   bool                   `json:"peer_connected"`
	LatestServerSeq uint64                 `json:"latest_server_seq"`
	RoleStates      map[string]psRoleState `json:"role_states,omitempty"`
}

type psRoleState struct {
	State                 string `json:"state"`
	LastMessageAgeSeconds *int64 `json:"last_message_age_seconds,omitempty"`
}

// formatRoleCell is a ps ORQ/EJEC cell: the role's state and, when known,
// the age of its last message ("trabajando 2m"); "—" when nothing is known
// (a host or join never sees the other role).
func formatRoleCell(state string, age *int64) string {
	if state == "" || state == string(control.StateUnknown) {
		return string(control.StateUnknown)
	}
	if age == nil {
		return state
	}
	return state + " " + humanizeIdle(time.Duration(*age)*time.Second)
}

func runPS(ctx context.Context, args []string, env ctlEnv) int {
	return reportFailure(dispatchPS(ctx, args, env), env.stderr)
}

func dispatchPS(ctx context.Context, args []string, env ctlEnv) error {
	flags := flag.NewFlagSet("agents-bridge ps", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("format", "table", "table o jsonl")
	if err := flags.Parse(args); err != nil {
		return failure("USAGE", "%v", err)
	}
	if flags.NArg() > 0 {
		return failure("USAGE", "unexpected argument %q", flags.Arg(0))
	}
	if *format != "table" && *format != "jsonl" {
		return failure("USAGE", "--format must be table or jsonl")
	}
	descriptors, err := control.ListDescriptors(env.root)
	if err != nil {
		return failure("INTERNAL", "%v", err)
	}
	rows := buildPSRows(ctx, descriptors)
	if *format == "jsonl" {
		return writePSJSONL(env.stdout, rows)
	}
	return writePSTable(env.stdout, rows)
}

// buildPSRows describes every live bridge (internal/bridges: the same
// source the TUI's home screen uses) as ps rows.
func buildPSRows(ctx context.Context, descriptors []control.Descriptor) []psRow {
	infos := bridges.FromDescriptors(ctx, descriptors)
	rows := make([]psRow, 0, len(infos))
	for _, info := range infos {
		var roleStates map[string]psRoleState
		if len(info.RoleStates) > 0 {
			roleStates = make(map[string]psRoleState, len(info.RoleStates))
			for key, role := range info.RoleStates {
				state := psRoleState{State: role.State}
				if role.LastMessageAt != nil {
					age := int64(time.Since(*role.LastMessageAt).Seconds())
					if age < 0 {
						age = 0
					}
					state.LastMessageAgeSeconds = &age
				}
				roleStates[key] = state
			}
		}
		rows = append(rows, psRow{
			RoleStates:      roleStates,
			InstanceID:      info.InstanceID,
			Mode:            info.Mode,
			Roles:           info.Roles,
			PID:             info.PID,
			StartedAt:       info.StartedAt.UTC().Format(time.RFC3339),
			IdleSeconds:     info.IdleSeconds,
			PeerConnected:   info.PeerConnected,
			LatestServerSeq: info.LatestServerSeq,
		})
	}
	return rows
}

func writePSJSONL(w io.Writer, rows []psRow) error {
	record, err := json.Marshal(map[string]any{"v": 1, "type": "response", "operation": "ps", "instances": rows})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(record))
	return err
}

func writePSTable(w io.Writer, rows []psRow) error {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tMODE\tROLES\tPID\tSTARTED\tIDLE\tPEER\tMSGS\tORQ\tEJEC")
	for _, row := range rows {
		peer := "no"
		if row.PeerConnected {
			peer = "sí"
		}
		started, err := time.Parse(time.RFC3339, row.StartedAt)
		startedDisplay := row.StartedAt
		if err == nil {
			startedDisplay = started.Local().Format("15:04")
		}
		roles := ""
		for i, r := range row.Roles {
			if i > 0 {
				roles += ","
			}
			roles += r
		}
		mode := row.Mode
		if mode == "" {
			mode = "-"
		}
		idle := "-"
		if row.IdleSeconds != nil {
			idle = humanizeIdle(time.Duration(*row.IdleSeconds) * time.Second)
		}
		orq := row.RoleStates["orchestrator"]
		ejec := row.RoleStates["executor"]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%d\t%s\t%s\n", row.InstanceID, mode, roles, row.PID, startedDisplay, idle, peer, row.LatestServerSeq, formatRoleCell(orq.State, orq.LastMessageAgeSeconds), formatRoleCell(ejec.State, ejec.LastMessageAgeSeconds))
	}
	return tw.Flush()
}

func humanizeIdle(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}
