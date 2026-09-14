package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/grrdhdz/codex-agents-bridge/internal/bridge"
	"github.com/grrdhdz/codex-agents-bridge/internal/protocol"
	"github.com/grrdhdz/codex-agents-bridge/internal/tailscale"
	"github.com/grrdhdz/codex-agents-bridge/internal/tui"
)

const commandName = "codex-bridge"

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "join" {
		err = runJoin(os.Args[2:])
	} else if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		printUsage()
		return
	} else {
		err = runOrchestrator()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, commandName+":", err)
		os.Exit(1)
	}
}

func runOrchestrator() error {
	ctx := context.Background()
	info, err := tailscale.Detect(ctx)
	if err != nil {
		return err
	}
	server, err := bridge.NewServer(info.IPv4, bridge.DefaultOptions())
	if err != nil {
		return err
	}
	defer server.Close()

	port := server.Addr().(*net.TCPAddr).Port
	joinCommand := fmt.Sprintf("%s join --host %s --port %d --instance %s --token %s", commandName, info.DNSName, port, server.InstanceID(), server.JoinToken())
	fmt.Println("Instancia codex-bridge creada en RAM.")
	fmt.Println("Copia este comando al ejecutor Windows:")
	fmt.Println(joinCommand)

	client, _, err := bridge.Dial(ctx, server.Addr().String(), server.InstanceID(), protocol.RoleOrchestrator, server.OwnerToken())
	if err != nil {
		return fmt.Errorf("conectar TUI local: %w", err)
	}
	defer client.Close()

	model := tui.New(tui.Options{
		Client:      client,
		LocalRole:   protocol.RoleOrchestrator,
		JoinCommand: joinCommand,
		OnStop:      server.Close,
		OnPair: func() string {
			token, tokenErr := server.RegeneratePairingToken()
			if tokenErr != nil {
				return "error: " + tokenErr.Error()
			}
			return fmt.Sprintf("%s join --host %s --port %d --instance %s --token %s", commandName, info.DNSName, port, server.InstanceID(), token)
		},
	})
	_, err = tea.NewProgram(model).Run()
	return err
}

func runJoin(args []string) error {
	flags := flag.NewFlagSet("codex-bridge join", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	host := flags.String("host", "", "MagicDNS host or Tailscale IP of the Mac")
	port := flags.Int("port", 0, "ephemeral codex-bridge TCP port")
	instanceID := flags.String("instance", "", "instance_id printed by Mac")
	token := flags.String("token", "", "one-use pairing token printed by Mac")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*host) == "" || *port < 1 || *port > 65535 || strings.TrimSpace(*instanceID) == "" || strings.TrimSpace(*token) == "" {
		return fmt.Errorf("join requires --host, --port, --instance and --token")
	}

	endpoint := net.JoinHostPort(*host, strconv.Itoa(*port))
	client, _, err := bridge.Dial(context.Background(), endpoint, *instanceID, protocol.RoleExecutor, *token)
	if err != nil {
		return err
	}
	defer client.Close()
	model := tui.New(tui.Options{Client: client, LocalRole: protocol.RoleExecutor})
	_, err = tea.NewProgram(model).Run()
	return err
}

func printUsage() {
	fmt.Println("codex-bridge              crea una instancia efímera y TUI de orquestador en Mac")
	fmt.Println("codex-bridge join ...      une Windows usando el comando impreso por Mac")
	fmt.Println("\nLa instancia, tokens, colas e historial solo viven en RAM.")
}
