package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/grrdhdz/agents-bridge/engine/internal/integration"
	"io"
	"os"
)

type integrationEnv struct {
	home, cwd, executable string
	stdout, stderr        io.Writer
}

func runIntegration(args []string, env integrationEnv) int {
	if len(args) < 2 {
		return reportFailure(failure("USAGE", "integration install|uninstall|status claude|codex [--scope user|project] [--project DIR]"), env.stderr)
	}
	flags := flag.NewFlagSet("agents-bridge integration", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	scope := flags.String("scope", "user", "")
	project := flags.String("project", env.cwd, "")
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
		return reportFailure(failure("USAGE", "opciones de integración inválidas"), env.stderr)
	}
	if env.home == "" {
		env.home, _ = os.UserHomeDir()
	}
	if env.cwd == "" {
		env.cwd, _ = os.Getwd()
		if *project == "" {
			*project = env.cwd
		}
	}
	if env.executable == "" {
		env.executable, _ = os.Executable()
	}
	result, err := integration.Apply(args[0], integration.Options{Home: env.home, Project: *project, Scope: *scope, Harness: args[1], Executable: env.executable})
	if err != nil {
		return reportFailure(failure("USAGE", "%s", err), env.stderr)
	}
	if args[1] == "codex" && args[0] == "install" {
		fmt.Fprintln(env.stderr, "Codex: habilita hooks si tu versión lo requiere; reinicia Codex y revisa los hooks para marcarlos como confiables (Review Hooks / Trust All and Continue, o /hooks y tecla t). No se escribió trusted_hash ni [hooks.state].")
		if *scope == "project" {
			fmt.Fprintln(env.stderr, "El proyecto debe estar marcado como trusted en Codex para cargar sus hooks.")
		}
	}
	return reportFailure(json.NewEncoder(env.stdout).Encode(result), env.stderr)
}
