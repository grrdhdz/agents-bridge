package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/grrdhdz/agents-bridge/engine/internal/control"
)

func runRename(ctx context.Context, args []string, env ctlEnv) int {
	return reportFailure(dispatchRename(ctx, args, env), env.stderr)
}

// dispatchRename sets a bridge's label: `rename --instance-id ID NAME`.
// NAME "" removes it, so listings fall back to the project directory.
func dispatchRename(ctx context.Context, args []string, env ctlEnv) error {
	flags := flag.NewFlagSet("agents-bridge rename", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	instanceID := flags.String("instance-id", "", "instance_id to rename")
	if err := flags.Parse(args); err != nil {
		return failure("USAGE", "%v", err)
	}
	if strings.TrimSpace(*instanceID) == "" {
		return failure("USAGE", "--instance-id is required")
	}
	if flags.NArg() != 1 {
		return failure("USAGE", "rename requires exactly one NAME argument (use \"\" to remove the name)")
	}
	name, err := control.NormalizeName(flags.Arg(0))
	if err != nil {
		return failure("USAGE", "%v", err)
	}
	descriptors, err := control.ListDescriptors(env.root)
	if err != nil {
		return failure("INTERNAL", "%v", err)
	}
	target, err := selectStopDescriptor(descriptors, *instanceID)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"name": name})
	response, err := control.Do(ctx, target, http.MethodPost, "/v1/name", bytes.NewReader(body))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return failure("CONTROL_UNREACHABLE", "control endpoint unreachable for instance %s", target.InstanceID)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return failure("TRANSPORT_ERROR", "%v", err)
	}
	if response.StatusCode != http.StatusOK {
		return failureFromBody(response.StatusCode, data)
	}
	record, err := json.Marshal(map[string]any{"v": 1, "type": "response", "ok": true, "operation": "rename", "instance_id": target.InstanceID, "name": name})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(env.stdout, string(record))
	return err
}
