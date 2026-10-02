package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/grrdhdz/agents-bridge/engine/internal/control"
	"github.com/grrdhdz/agents-bridge/engine/internal/hooks"
)

func bindingStore(env ctlEnv) hooks.Store {
	s := hooks.Store{DescriptorRoot: env.root}
	if env.root != "" {
		s.Root = filepath.Join(filepath.Dir(env.root), "bindings")
	}
	return s
}
func runBind(ctx context.Context, args []string, env ctlEnv) int {
	return reportFailure(dispatchBind(ctx, args, env), env.stderr)
}
func dispatchBind(ctx context.Context, args []string, env ctlEnv) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	flags := flag.NewFlagSet("agents-bridge bind", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.String("instance-id", "", "")
	roleKey := flags.String("role", "", "")
	list := flags.Bool("list", false, "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return failure("USAGE", "bind --instance-id ID --role ROL | --list")
	}
	if *list {
		if *id != "" || *roleKey != "" {
			return failure("USAGE", "--list cannot be combined with an instance or role")
		}
		bindings, err := bindingStore(env).List(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(env.stdout).Encode(map[string]any{"v": 1, "ok": true, "operation": "bind-list", "bindings": bindings})
	}
	role, ok := control.RoleFromKey(*roleKey)
	if !ok || *id == "" {
		return failure("USAGE", "bind requires --instance-id and --role")
	}
	d, err := control.FindDescriptor(env.root, *id, role)
	if errors.Is(err, control.ErrInstanceNotFound) {
		return failure("INSTANCE_NOT_FOUND", "bridge not found")
	}
	if err != nil {
		return err
	}
	res, err := control.Do(ctx, d, http.MethodGet, "/v1/health", nil)
	if err != nil {
		return failure("CONTROL_UNREACHABLE", "bridge unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return failure("CONTROL_UNREACHABLE", "bridge health rejected")
	}
	// Only the hook has the real harness session_id. The command validates the
	// target; observing this tool command is what records that session's binding.
	return json.NewEncoder(env.stdout).Encode(map[string]any{"v": 1, "ok": true, "operation": "bind", "instance_id": *id, "role": role, "pending_hook": true, "message": "puente validado; el hook registra el vínculo de la sesión"})
}
func runUnbind(ctx context.Context, args []string, env ctlEnv) int {
	flags := flag.NewFlagSet("agents-bridge unbind", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	harness := flags.String("harness", "", "")
	session := flags.String("session-id", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || (*harness == "") != (*session == "") {
		return reportFailure(failure("USAGE", "unbind [--harness claude|codex --session-id ID]"), env.stderr)
	}
	if *harness != "" {
		if err := bindingStore(env).Unbind(ctx, *harness, *session); err != nil {
			return reportFailure(err, env.stderr)
		}
	}
	return reportFailure(json.NewEncoder(env.stdout).Encode(map[string]any{"v": 1, "ok": true, "operation": "unbind", "pending_hook": *harness == "", "message": "el hook elimina el vínculo de esta sesión"}), env.stderr)
}
