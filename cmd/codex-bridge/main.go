package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	term "github.com/charmbracelet/x/term"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/clipboard"
	"github.com/grrdhdz/codex-agents-bridge/internal/control"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
	"github.com/grrdhdz/codex-agents-bridge/internal/tailscale"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui"
)

const (
	commandName = "codex-bridge"
	appVersion  = "v0.2.0"
)

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "ctl" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := runCtl(ctx, os.Args[2:], ctlEnv{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr})
		stop()
		os.Exit(code)
	} else if len(os.Args) > 1 && os.Args[1] == "ps" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := runPS(ctx, os.Args[2:], ctlEnv{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr})
		stop()
		os.Exit(code)
	} else if len(os.Args) > 1 && os.Args[1] == "stop" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := runStop(ctx, os.Args[2:], ctlEnv{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr})
		stop()
		os.Exit(code)
	} else if len(os.Args) > 1 && os.Args[1] == "local" {
		lf, parseErr := parseLocalFlags(os.Args[2:])
		if parseErr != nil {
			fmt.Fprintln(os.Stderr, commandName+":", parseErr)
			os.Exit(2)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		cwd, _ := os.Getwd()
		err = runLocal(ctx, os.Stdout, "", cwd, lf.idleTimeout, lf.readyFile, lf.headless, isStdoutTerminal)
		stop()
	} else if len(os.Args) > 1 && os.Args[1] == "codex" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := runCodex(ctx, os.Args[2:], codexEnv{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, codexHome: resolveCodexHome()})
		stop()
		os.Exit(code)
	} else if len(os.Args) > 1 && os.Args[1] == "join" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err = runJoin(ctx, os.Args[2:], os.Stdout, "")
		stop()
	} else if len(os.Args) > 1 && os.Args[1] == "tui" {
		err = runTUIObserver(os.Args[2:], "")
	} else if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println(appVersion)
		return
	} else if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		printUsage()
		return
	} else {
		hf, parseErr := parseHostFlags(os.Args[1:])
		if parseErr != nil {
			fmt.Fprintln(os.Stderr, commandName+":", parseErr)
			os.Exit(2)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err = runOrchestrator(ctx, os.Stdout, "", hf.idleTimeout, hf.headless, hf.readyFile, tailscale.Detect)
		stop()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, commandName+":", err)
		os.Exit(1)
	}
}

// parseIdleTimeoutFlag reads --idle-timeout from args without disturbing
// other flags a command may add later; unknown flags are ignored here so
// this only owns --idle-timeout. def is the default when the flag is absent;
// 0 disables the idle timer.
func parseIdleTimeoutFlag(name string, args []string, def time.Duration) (time.Duration, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	idleTimeout := flags.Duration("idle-timeout", def, "cierra el proceso tras este tiempo sin actividad (0 desactiva)")
	if err := flags.Parse(args); err != nil {
		return 0, err
	}
	if *idleTimeout < 0 {
		return 0, fmt.Errorf("--idle-timeout no puede ser negativo")
	}
	return *idleTimeout, nil
}

// isStdoutTerminal is runLocal's default isTerminal (§7.1): true only when
// stdout is a real terminal, so a script, pipe, or CI job never gets an
// unexpected TUI.
func isStdoutTerminal() bool { return term.IsTerminal(os.Stdout.Fd()) }

// localFlags holds `codex-bridge local`'s own flags (§5.1, §7.1).
type localFlags struct {
	idleTimeout time.Duration
	readyFile   string
	headless    bool
}

func parseLocalFlags(args []string) (localFlags, error) {
	flags := flag.NewFlagSet("codex-bridge local", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	idleTimeout := flags.Duration("idle-timeout", defaultLocalIdleTimeout, "cierra el proceso tras este tiempo sin actividad (0 desactiva)")
	readyFile := flags.String("ready-file", "", "escribe la línea ready también en este archivo (0600, creación exclusiva)")
	headless := flags.Bool("headless", false, "no muestra la TUI observadora aunque stdout sea una terminal")
	if err := flags.Parse(args); err != nil {
		return localFlags{}, err
	}
	if *idleTimeout < 0 {
		return localFlags{}, fmt.Errorf("--idle-timeout no puede ser negativo")
	}
	return localFlags{idleTimeout: *idleTimeout, readyFile: *readyFile, headless: *headless}, nil
}

// hostFlags holds the Mac/tailscale-host command's own flags (§8): headless
// mirrors `local`'s, but --headless is rejected without --ready-file, since
// this is the only place the join command (with its one-use token) can ever
// leave the process (§8: "nunca en stdout").
type hostFlags struct {
	idleTimeout time.Duration
	readyFile   string
	headless    bool
}

func parseHostFlags(args []string) (hostFlags, error) {
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	idleTimeout := flags.Duration("idle-timeout", 0, "cierra el proceso tras este tiempo sin actividad (0 desactiva; por defecto 0 en este modo)")
	readyFile := flags.String("ready-file", "", "escribe la línea ready (con join_command en headless) en este archivo (0600, creación exclusiva)")
	headless := flags.Bool("headless", false, "sin TUI; requiere --ready-file, donde va el comando de unión con su token")
	if err := flags.Parse(args); err != nil {
		return hostFlags{}, err
	}
	if *idleTimeout < 0 {
		return hostFlags{}, fmt.Errorf("--idle-timeout no puede ser negativo")
	}
	if *headless && strings.TrimSpace(*readyFile) == "" {
		return hostFlags{}, fmt.Errorf("--headless requiere --ready-file: el comando de unión con su token nunca sale por stdout")
	}
	return hostFlags{idleTimeout: *idleTimeout, readyFile: *readyFile, headless: *headless}, nil
}

// runOrchestrator hosts the Mac/tailscale-host side (§8). detect is injected
// so tests can exercise both headless and interactive wiring against a
// loopback bridge.Server without a real Tailscale install or network
// (tailscale.DetectWithRunner already supports this; only the production
// caller passes tailscale.Detect itself). In headless mode nothing is ever
// written to stdout or stderr: the join command (with its token) is written
// only into the required --ready-file's join_command field.
func runOrchestrator(ctx context.Context, stdout io.Writer, root string, idleTimeout time.Duration, headless bool, readyFile string, detect func(context.Context) (tailscale.Info, error)) error {
	if headless && strings.TrimSpace(readyFile) == "" {
		return fmt.Errorf("--headless requiere --ready-file")
	}
	info, err := detect(ctx)
	if err != nil {
		return err
	}
	server, err := bridge.NewServer(info.IPv4, bridge.DefaultOptions())
	if err != nil {
		return err
	}
	defer server.Close()

	port := server.Addr().(*net.TCPAddr).Port
	joinCommand := formatPowerShellJoinCommand(fmt.Sprintf("%s join --host %s --port %d --instance %s --token %s", commandName, info.DNSName, port, server.InstanceID(), server.JoinToken()))
	if !headless {
		fmt.Fprintln(stdout, "Instancia codex-bridge creada en RAM.")
		fmt.Fprintln(stdout, "Copia este comando al ejecutor Windows:")
		fmt.Fprintln(stdout, joinCommand)
	}

	client, _, err := bridge.Dial(ctx, server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		return fmt.Errorf("conectar TUI local: %w", err)
	}
	defer client.Close()

	if headless {
		return runOrchestratorHeadless(ctx, client, server, root, idleTimeout, readyFile, joinCommand)
	}

	model := tui.New(tui.Options{
		Client:      client,
		LocalRole:   protocol.RoleOrchestrator,
		JoinCommand: joinCommand,
		CopyCommand: clipboard.Copy,
		OnStop:      server.Close,
		OnPair: func() string {
			token, tokenErr := server.RegeneratePairingToken()
			if tokenErr != nil {
				return "error: " + tokenErr.Error()
			}
			command := formatPowerShellJoinCommand(fmt.Sprintf("%s join --host %s --port %d --instance %s --token %s", commandName, info.DNSName, port, server.InstanceID(), token))
			fmt.Println("Nuevo comando de unión (fallback completo de terminal):")
			fmt.Println(command)
			return command
		},
	})
	// The program is created (but not run) before the control endpoint, so
	// Stop (bound to program.Quit) is safe to call even if POST /v1/stop
	// arrives before Run starts consuming its message queue: Quit blocks
	// until Run does (§4.2).
	program := tea.NewProgram(&model)
	activity := control.NewActivity()
	go touchActivityOnMessages(ctx, client, activity)
	if idleTimeout > 0 {
		go monitorIdle(ctx, activity, idleTimeout, program.Quit)
	}
	if endpoint, ctlErr := control.Start(client, control.Options{
		Role:          protocol.RoleOrchestrator,
		Mode:          control.ModeTailscaleHost,
		CanStop:       true,
		Stop:          program.Quit,
		PeerConnected: server.WorkerConnected,
		Activity:      activity,
	}); ctlErr != nil {
		fmt.Println("ctl no disponible:", ctlErr)
	} else {
		defer endpoint.Close()
	}
	_, err = program.Run()
	return err
}

// runOrchestratorHeadless is runOrchestrator's §8 headless path: no TUI, the
// join command (with its token) goes only into the required --ready-file's
// join_command field, and the process just reconnects and serves ctl until
// ctx ends, the server closes, or POST /v1/stop fires.
func runOrchestratorHeadless(ctx context.Context, client *bridge.Client, server *bridge.Server, root string, idleTimeout time.Duration, readyFile string, joinCommand string) error {
	readyFileHandle, err := os.OpenFile(readyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("--ready-file: %w", err)
	}
	defer readyFileHandle.Close()

	stop := newStopper()
	activity := control.NewActivity()
	endpoint, err := control.Start(client, control.Options{
		Role:          protocol.RoleOrchestrator,
		Mode:          control.ModeTailscaleHost,
		Root:          root,
		CanStop:       true,
		Stop:          stop.stop,
		PeerConnected: server.WorkerConnected,
		Activity:      activity,
	})
	if err != nil {
		return fmt.Errorf("ctl orquestador: %w", err)
	}
	defer endpoint.Close()

	go touchActivityOnMessages(ctx, client, activity)
	if idleTimeout > 0 {
		go monitorIdle(ctx, activity, idleTimeout, stop.stop)
	}

	ready, err := json.Marshal(map[string]any{"v": 1, "type": "ready", "instance_id": server.InstanceID(), "mode": string(control.ModeTailscaleHost), "join_command": joinCommand})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(readyFileHandle, string(ready)); err != nil {
		return fmt.Errorf("--ready-file: %w", err)
	}

	select {
	case <-ctx.Done():
	case <-server.Done():
	case <-stop.done():
	}
	return nil
}

// joinFlags holds `codex-bridge join`'s flags (§8): --headless and
// --ready-file are additive to the original pairing flags, so an existing
// interactive join command keeps working unchanged.
type joinFlags struct {
	host, instanceID, token string
	port                    int
	headless                bool
	readyFile               string
}

func parseJoinFlags(args []string) (joinFlags, error) {
	flags := flag.NewFlagSet("codex-bridge join", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	host := flags.String("host", "", "MagicDNS host or Tailscale IP of the Mac")
	port := flags.Int("port", 0, "ephemeral codex-bridge TCP port")
	instanceID := flags.String("instance", "", "instance_id printed by Mac")
	token := flags.String("token", "", "one-use pairing token printed by Mac")
	headless := flags.Bool("headless", false, "sin TUI: publica el descriptor tailscale-join y solo reconecta")
	readyFile := flags.String("ready-file", "", "escribe la línea ready también en este archivo (0600, creación exclusiva)")
	if err := flags.Parse(args); err != nil {
		return joinFlags{}, err
	}
	if strings.TrimSpace(*host) == "" || *port < 1 || *port > 65535 || strings.TrimSpace(*instanceID) == "" || strings.TrimSpace(*token) == "" {
		return joinFlags{}, fmt.Errorf("join requires --host, --port, --instance and --token")
	}
	return joinFlags{host: *host, port: *port, instanceID: *instanceID, token: *token, headless: *headless, readyFile: *readyFile}, nil
}

// runJoin implements `codex-bridge join` (§8): the interactive path is
// unchanged from before Phase 3; --headless skips the TUI entirely, publishes
// a tailscale-join descriptor, and just keeps the connection alive for a
// local ctl executor until ctx ends or the join process is stopped.
func runJoin(ctx context.Context, args []string, stdout io.Writer, root string) error {
	jf, err := parseJoinFlags(args)
	if err != nil {
		return err
	}

	endpoint := net.JoinHostPort(jf.host, strconv.Itoa(jf.port))
	client, _, err := bridge.Dial(ctx, endpoint, jf.instanceID, protocol.RoleExecutor, jf.token)
	if err != nil {
		return err
	}
	defer client.Close()

	if jf.headless {
		return runJoinHeadless(ctx, client, stdout, root, jf.readyFile)
	}

	model := tui.New(tui.Options{Client: client, LocalRole: protocol.RoleExecutor})
	// Created before the control endpoint so Stop (program.Quit) is safe to
	// call as soon as the endpoint exists, mirroring the host side (§4.2).
	program := tea.NewProgram(&model)
	if ctlEndpoint, ctlErr := control.Start(client, control.Options{
		Role:          protocol.RoleExecutor,
		Mode:          control.ModeTailscaleJoin,
		CanStop:       true,
		Stop:          program.Quit,
		PeerConnected: client.Connected,
	}); ctlErr != nil {
		fmt.Fprintln(os.Stderr, "ctl no disponible:", ctlErr)
	} else {
		defer ctlEndpoint.Close()
	}
	_, err = program.Run()
	return err
}

// runJoinHeadless is runJoin's §8 headless path: no TUI, just a published
// tailscale-join descriptor and a reconnect loop (matching `local`'s
// keepConnected), until ctx ends, the client's connection ends for good, or
// POST /v1/stop fires.
func runJoinHeadless(ctx context.Context, client *bridge.Client, stdout io.Writer, root string, readyFile string) error {
	var readyFileHandle *os.File
	if readyFile != "" {
		var err error
		readyFileHandle, err = os.OpenFile(readyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("--ready-file: %w", err)
		}
		defer readyFileHandle.Close()
	}

	stop := newStopper()
	endpoint, err := control.Start(client, control.Options{
		Role:          protocol.RoleExecutor,
		Mode:          control.ModeTailscaleJoin,
		Root:          root,
		CanStop:       true,
		Stop:          stop.stop,
		PeerConnected: client.Connected,
	})
	if err != nil {
		return fmt.Errorf("ctl ejecutor: %w", err)
	}
	defer endpoint.Close()

	go keepConnected(ctx, client)

	ready, err := json.Marshal(map[string]any{"v": 1, "type": "ready", "instance_id": client.InstanceID(), "mode": string(control.ModeTailscaleJoin)})
	if err != nil {
		return err
	}
	if readyFileHandle != nil {
		if _, err := fmt.Fprintln(readyFileHandle, string(ready)); err != nil {
			return fmt.Errorf("--ready-file: %w", err)
		}
	}
	if _, err := fmt.Fprintln(stdout, string(ready)); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
	case <-client.Done():
	case <-stop.done():
	}
	return nil
}

func printUsage() {
	fmt.Println("codex-bridge [--idle-timeout D]   crea una instancia efímera y TUI de orquestador en Mac")
	fmt.Println("codex-bridge --headless --ready-file FILE")
	fmt.Println("                                   igual, sin TUI; el comando de unión (con su token) va solo en FILE")
	fmt.Println("codex-bridge join ... [--headless] [--ready-file FILE]")
	fmt.Println("                                   une Windows usando el comando impreso por Mac; --headless sin TUI")
	fmt.Println("codex-bridge local [--idle-timeout D] [--headless] [--ready-file FILE]")
	fmt.Println("                                   instancia local sin Tailscale para dos agentes; si stdout es una")
	fmt.Println("                                   terminal muestra su propia TUI observadora (--headless la omite)")
	fmt.Println("                                   (idle-timeout por defecto 30m; 0 desactiva el cierre por inactividad)")
	fmt.Println("codex-bridge ps [--format table|jsonl]")
	fmt.Println("                                   lista los puentes vivos del usuario")
	fmt.Println("codex-bridge stop --instance-id ID cierra un puente (equivale a Ctrl+C en su proceso)")
	fmt.Println("codex-bridge tui --instance-id ID  TUI observadora: ve e interviene sin consumir mensajes")
	fmt.Println("codex-bridge ctl list")
	fmt.Println("codex-bridge ctl read|watch|send|wait --instance-id ID [--role orchestrator|executor] ...")
	fmt.Println("codex-bridge codex open --thread <deeplink|id> --instance-id ID [--prompt-file FILE|-]")
	fmt.Println("                                   abre el chat del ejecutor en la app de Codex con el prompt escrito")
	fmt.Println("codex-bridge --version             muestra la versión")
	fmt.Println("\nLa instancia, tokens, colas e historial solo viven en RAM.")
}

// formatPowerShellJoinCommand keeps every credential-bearing argument visible
// in a narrow terminal while preserving a command that can be pasted directly
// into PowerShell. A backtick immediately followed by a newline continues the
// command; no token characters are lost to horizontal clipping.
func formatPowerShellJoinCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) < 2 || (len(fields)-2)%2 != 0 {
		return command
	}
	lines := []string{fields[0] + " " + fields[1]}
	for i := 2; i < len(fields); i += 2 {
		lines = append(lines, "  "+fields[i]+" "+fields[i+1])
	}
	for i := 0; i < len(lines)-1; i++ {
		lines[i] += " `"
	}
	return strings.Join(lines, "\n")
}
