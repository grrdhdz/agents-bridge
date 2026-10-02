package main

import (
	"context"
	"github.com/grrdhdz/agents-bridge/engine/internal/api"
	"os"
)

func runAPI(ctx context.Context, args []string, env ctlEnv) int {
	if len(args) != 0 {
		return reportFailure(failure("USAGE", "agents-bridge api no recibe argumentos; peticiones JSONL por stdin"), env.stderr)
	}
	server := api.New(api.Options{Root: env.root, Version: appVersion, CreateLocal: func(ctx context.Context, idle string) (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		return launchLocalBridgeWithIdle(ctx, exe, idle, startDetached)
	}})
	return reportFailure(server.Run(ctx, env.stdin, env.stdout), env.stderr)
}
