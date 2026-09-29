package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridges"
	"github.com/grrdhdz/codex-agents-bridge/internal/control"
)

func runStop(ctx context.Context, args []string, env ctlEnv) int {
	return reportFailure(dispatchStop(ctx, args, env), env.stderr)
}

func dispatchStop(ctx context.Context, args []string, env ctlEnv) error {
	flags := flag.NewFlagSet("codex-bridge stop", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	instanceID := flags.String("instance-id", "", "instance_id to stop")
	if err := flags.Parse(args); err != nil {
		return failure("USAGE", "%v", err)
	}
	if flags.NArg() > 0 {
		return failure("USAGE", "unexpected argument %q", flags.Arg(0))
	}
	if strings.TrimSpace(*instanceID) == "" {
		return failure("USAGE", "--instance-id is required")
	}
	descriptors, err := control.ListDescriptors(env.root)
	if err != nil {
		return failure("INTERNAL", "%v", err)
	}
	target, err := selectStopDescriptor(descriptors, *instanceID)
	if err != nil {
		return err
	}
	response, err := control.Do(ctx, target, http.MethodPost, "/v1/stop", nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return failure("CONTROL_UNREACHABLE", "control endpoint unreachable for instance %s; pid=%d (use kill %d if it must be removed manually)", target.InstanceID, target.PID, target.PID)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return failure("TRANSPORT_ERROR", "%v", err)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return failureFromBody(response.StatusCode, data)
	}
	record, err := json.Marshal(map[string]any{"v": 1, "type": "response", "ok": true, "operation": "stop", "instance_id": target.InstanceID})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(env.stdout, string(record))
	return err
}

// selectStopDescriptor maps internal/bridges' selection (shared with the
// TUI's home screen) onto this command's error codes: an instance not found
// in this user's descriptor directory is INSTANCE_NOT_FOUND (exit 3).
func selectStopDescriptor(descriptors []control.Descriptor, instanceID string) (control.Descriptor, error) {
	d, err := bridges.SelectStopDescriptor(descriptors, instanceID)
	if errors.Is(err, bridges.ErrNotFound) {
		return control.Descriptor{}, failure("INSTANCE_NOT_FOUND", "instance %q not found", instanceID)
	}
	return d, err
}
