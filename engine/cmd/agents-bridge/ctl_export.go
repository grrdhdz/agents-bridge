package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/grrdhdz/agents-bridge/engine/internal/bridgeexport"
	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"net/http"
)

// ctlPeek implements `ctl peek` (§3.2): a read-only look at the unread
// messages from the other role. It never consumes anything.
func ctlPeek(ctx context.Context, env ctlEnv, d control.Descriptor, format string) error {
	response, err := request(ctx, d, http.MethodGet, "/v1/peek", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := readAll(response)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return failureFromBody(response.StatusCode, data)
	}
	if format == "jsonl" {
		_, err = env.stdout.Write(data)
		return err
	}
	var record struct {
		InstanceID  string `json:"instance_id"`
		Unread      int    `json:"unread"`
		Urgent      bool   `json:"urgent"`
		LatestLabel string `json:"latest_label"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return failure("INTERNAL", "decode peek response: %v", err)
	}
	urgent, latest := "no", record.LatestLabel
	if record.Urgent {
		urgent = "sí"
	}
	if latest == "" {
		latest = "ninguna"
	}
	_, err = fmt.Fprintf(env.stdout, "--- agents-bridge instance=%s unread=%d urgent=%s latest=%s\n", record.InstanceID, record.Unread, urgent, latest)
	return err
}

func ctlExport(ctx context.Context, env ctlEnv, d control.Descriptor, output, format string) error {
	err := bridgeexport.Write(ctx, d, output, format, bridgeexport.Options{Now: env.now})
	var failureData *bridgeexport.Error
	if errors.As(err, &failureData) {
		return failure(failureData.Code, "%s", failureData.Message)
	}
	return err
}
