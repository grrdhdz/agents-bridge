package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grrdhdz/agents-bridge/engine/internal/bridge"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

func (r *localRun) ps(args ...string) (int, string, string) {
	var stdout, stderr strings.Builder
	code := runPS(context.Background(), append([]string{}, args...), ctlEnv{stdout: &stdout, stderr: &stderr, root: r.root})
	return code, stdout.String(), stderr.String()
}

// TestPSGroupsBothRolesOfOneInstanceAndSeparatesInstances covers §10.2: a
// local instance's two role descriptors become one row, and a second,
// unrelated instance in the same descriptor root gets its own row, without
// leaking any secret.
func TestPSGroupsBothRolesOfOneInstanceAndSeparatesInstances(t *testing.T) {
	runA := startLocal(t)
	instanceA := runA.ready["instance_id"].(string)

	// A second, independent instance sharing the same descriptor root (as
	// two bridges on the same machine would).
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	client, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	endpoint, err := control.Start(client, control.Options{Role: protocol.RoleOrchestrator, Mode: control.ModeTailscaleHost, Root: runA.root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(endpoint.Close)
	instanceB := server.InstanceID()

	code, stdout, stderr := runA.ps("--format", "jsonl")
	if code != 0 {
		t.Fatalf("ps failed: %d %s", code, stderr)
	}
	var record struct {
		Instances []psRow `json:"instances"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &record); err != nil {
		t.Fatalf("ps jsonl output invalid: %v: %s", err, stdout)
	}
	byID := map[string]psRow{}
	for _, row := range record.Instances {
		byID[row.InstanceID] = row
	}
	rowA, ok := byID[instanceA]
	if !ok {
		t.Fatalf("instance A missing from ps output: %+v", record.Instances)
	}
	if len(rowA.Roles) != 2 || rowA.Roles[0] != "orchestrator" || rowA.Roles[1] != "executor" {
		t.Fatalf("local instance should group both roles into one row, got %+v", rowA)
	}
	if rowA.Mode != "local" {
		t.Fatalf("mode should be local, got %q", rowA.Mode)
	}
	rowB, ok := byID[instanceB]
	if !ok {
		t.Fatalf("instance B missing from ps output: %+v", record.Instances)
	}
	if len(rowB.Roles) != 1 || rowB.Roles[0] != "orchestrator" {
		t.Fatalf("second instance should have only its own role, got %+v", rowB)
	}
	if rowB.Mode != "tailscale-host" {
		t.Fatalf("mode should be tailscale-host, got %q", rowB.Mode)
	}

	for _, secret := range []string{"capability", "control_url", "cwd", "token"} {
		if strings.Contains(stdout, secret) {
			t.Fatalf("ps jsonl output leaked %q: %s", secret, stdout)
		}
	}

	codeTable, stdoutTable, stderrTable := runA.ps("--format", "table")
	if codeTable != 0 {
		t.Fatalf("ps table failed: %d %s", codeTable, stderrTable)
	}
	if !strings.Contains(stdoutTable, "INSTANCE") || !strings.Contains(stdoutTable, instanceA) || !strings.Contains(stdoutTable, instanceB) {
		t.Fatalf("table output missing header or instances: %s", stdoutTable)
	}
	for _, secret := range []string{"capability", "control_url"} {
		if strings.Contains(stdoutTable, secret) {
			t.Fatalf("ps table output leaked %q: %s", secret, stdoutTable)
		}
	}
}

// TestPSDefaultFormatIsTable covers the default --format when omitted.
func TestPSDefaultFormatIsTable(t *testing.T) {
	run := startLocal(t)
	code, stdout, stderr := run.ps()
	if code != 0 {
		t.Fatalf("ps failed: %d %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "INSTANCE") {
		t.Fatalf("default ps output should be a table, got: %s", stdout)
	}
}

// TestPSRejectsUnknownFormat is a small usage-error check reusing the shared
// exit code machinery.
func TestPSRejectsUnknownFormat(t *testing.T) {
	run := startLocal(t)
	code, _, stderr := run.ps("--format", "xml")
	if code != exitUsage || !strings.Contains(stderr, "USAGE") {
		t.Fatalf("unknown format should be a usage error: %d %s", code, stderr)
	}
}

// TestPSShowsDashForLegacyDescriptorWithoutModeOrActivity covers fix A: a
// descriptor from before mode/last_activity_at existed must show "-" in both
// columns, not an empty MODE or a misleading "0s" IDLE.
func TestPSShowsDashForLegacyDescriptorWithoutModeOrActivity(t *testing.T) {
	server, err := bridge.NewServer("127.0.0.1", bridge.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	client, _, err := bridge.Dial(context.Background(), server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	root := filepath.Join(t.TempDir(), "instances")
	endpoint, err := control.Start(client, control.Options{Role: protocol.RoleOrchestrator, Mode: control.ModeLocal, Root: root, Activity: control.NewActivity()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(endpoint.Close)
	instanceID := server.InstanceID()

	// Overwrite the descriptor on disk to look exactly like one from before
	// fase 1: same control_url/capability (so the live health call still
	// authenticates), but no "mode" or "last_activity_at" keys at all — not
	// merely a zero value, since that's what a genuinely old binary wrote.
	rewriteDescriptorWithoutModeOrActivity(t, root, instanceID, "mac-orchestrator")

	var stdoutTable, stderrTable strings.Builder
	code := runPS(context.Background(), []string{"--format", "table"}, ctlEnv{stdout: &stdoutTable, stderr: &stderrTable, root: root})
	if code != 0 {
		t.Fatalf("ps failed: %d %s", code, stderrTable.String())
	}
	lines := strings.Split(strings.TrimRight(stdoutTable.String(), "\n"), "\n")
	var row string
	for _, line := range lines {
		if strings.Contains(line, instanceID) {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("instance row missing from table output: %s", stdoutTable.String())
	}
	fields := strings.Fields(row)
	if len(fields) < 7 {
		t.Fatalf("unexpected row shape: %q", row)
	}
	mode, idle := fields[2], fields[6]
	if mode != "-" {
		t.Fatalf("legacy descriptor should show MODE \"-\", got %q (row=%q)", mode, row)
	}
	if idle != "-" {
		t.Fatalf("legacy descriptor should show IDLE \"-\", got %q (row=%q)", idle, row)
	}

	var stdoutJSONL, stderrJSONL strings.Builder
	code = runPS(context.Background(), []string{"--format", "jsonl"}, ctlEnv{stdout: &stdoutJSONL, stderr: &stderrJSONL, root: root})
	if code != 0 {
		t.Fatalf("ps jsonl failed: %d %s", code, stderrJSONL.String())
	}
	if strings.Contains(stdoutJSONL.String(), `"idle_seconds"`) {
		t.Fatalf("jsonl should omit idle_seconds when never recorded: %s", stdoutJSONL.String())
	}
}

// rewriteDescriptorWithoutModeOrActivity replaces a just-started descriptor
// file with one carrying the same control_url/capability (so live health
// calls still authenticate) but omitting "mode" and "last_activity_at"
// entirely, matching a descriptor written by a binary from before those
// fields existed.
func rewriteDescriptorWithoutModeOrActivity(t *testing.T, root, instanceID, roleSuffix string) {
	t.Helper()
	path := filepath.Join(root, instanceID+"-"+roleSuffix+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "mode")
	delete(fields, "last_activity_at")
	rewritten, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
}
