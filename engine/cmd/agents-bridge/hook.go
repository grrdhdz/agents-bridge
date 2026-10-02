package main

import (
	"context"
	"io"
	"os"

	"github.com/grrdhdz/agents-bridge/engine/internal/hooks"
)

// runHook includes stdin and stdout in the same budget as discovery and HTTP.
// On cancellation, closing process pipes releases pending I/O; errors never
// become harness failures or partial JSON responses.
func runHook(ctx context.Context, args []string, env ctlEnv) int {
	if len(args) != 2 || env.stdin == nil || env.stdout == nil || (args[0] != "claude" && args[0] != "codex") {
		return exitOK
	}
	ctx, cancel := context.WithTimeout(ctx, hooks.MaxBudget)
	defer cancel()
	type inputResult struct {
		data []byte
		err  error
	}
	input := make(chan inputResult, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(env.stdin, hooks.MaxInput+1))
		input <- inputResult{data, err}
	}()
	var data []byte
	select {
	case result := <-input:
		if result.err != nil {
			return exitOK
		}
		data = result.data
	case <-ctx.Done():
		if closer, ok := env.stdin.(io.Closer); ok {
			_ = closer.Close()
		}
		return exitOK
	}
	runner := hooks.Runner{Store: bindingStore(env), Diagnostic: os.Getenv("AGENTS_BRIDGE_HOOK_DIAGNOSTICS") == "1"}
	out := runner.Run(ctx, args[0], args[1], data)
	if len(out) == 0 || ctx.Err() != nil {
		return exitOK
	}
	written := make(chan struct{}, 1)
	go func() { _, _ = env.stdout.Write(append(out, '\n')); written <- struct{}{} }()
	select {
	case <-written:
	case <-ctx.Done():
		if closer, ok := env.stdout.(io.Closer); ok {
			_ = closer.Close()
		}
	}
	return exitOK
}
