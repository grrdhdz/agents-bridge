package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
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
	IdleSeconds     *int64 `json:"idle_seconds,omitempty"`
	PeerConnected   bool   `json:"peer_connected"`
	LatestServerSeq uint64 `json:"latest_server_seq"`
}

func runPS(ctx context.Context, args []string, env ctlEnv) int {
	return reportFailure(dispatchPS(ctx, args, env), env.stderr)
}

func dispatchPS(ctx context.Context, args []string, env ctlEnv) error {
	flags := flag.NewFlagSet("codex-bridge ps", flag.ContinueOnError)
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

// buildPSRows groups live descriptors by instance_id, so a local instance's
// two role descriptors become one row, while separate instances stay
// separate rows even if a process crashed and left only one role behind.
func buildPSRows(ctx context.Context, descriptors []control.Descriptor) []psRow {
	byInstance := make(map[string][]control.Descriptor, len(descriptors))
	order := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		if _, seen := byInstance[d.InstanceID]; !seen {
			order = append(order, d.InstanceID)
		}
		byInstance[d.InstanceID] = append(byInstance[d.InstanceID], d)
	}
	sort.Strings(order)
	rows := make([]psRow, 0, len(order))
	for _, id := range order {
		rows = append(rows, buildPSRow(ctx, byInstance[id]))
	}
	return rows
}

func buildPSRow(ctx context.Context, ds []control.Descriptor) psRow {
	row := psRow{InstanceID: ds[0].InstanceID}
	var chosen control.Descriptor
	haveOrchestrator, haveExecutor := false, false
	var startedAt time.Time
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
		if i == 0 || d.StartedAt.Before(startedAt) {
			startedAt = d.StartedAt
		}
		if d.LastActivityAt != nil && (lastActivity == nil || d.LastActivityAt.After(*lastActivity)) {
			lastActivity = d.LastActivityAt
		}
		if row.Mode == "" {
			row.Mode = string(d.Mode)
		}
		row.PID = d.PID
	}
	if haveOrchestrator {
		row.Roles = append(row.Roles, "orchestrator")
	}
	if haveExecutor {
		row.Roles = append(row.Roles, "executor")
	}
	row.StartedAt = startedAt.UTC().Format(time.RFC3339)
	if lastActivity != nil {
		idle := int64(time.Since(*lastActivity).Seconds())
		if idle < 0 {
			idle = 0
		}
		row.IdleSeconds = &idle
	}

	healthCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if response, err := control.Do(healthCtx, chosen, http.MethodGet, "/v1/health", nil); err == nil {
		var health struct {
			PeerConnected   bool   `json:"peer_connected"`
			LatestServerSeq uint64 `json:"latest_server_seq"`
		}
		if response.StatusCode == http.StatusOK && json.NewDecoder(response.Body).Decode(&health) == nil {
			row.PeerConnected = health.PeerConnected
			row.LatestServerSeq = health.LatestServerSeq
		}
		_ = response.Body.Close()
	}
	return row
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
	fmt.Fprintln(tw, "INSTANCE\tMODE\tROLES\tPID\tSTARTED\tIDLE\tPEER\tMSGS")
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%d\n", row.InstanceID, mode, roles, row.PID, startedDisplay, idle, peer, row.LatestServerSeq)
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
