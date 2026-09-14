package main

import (
	"strings"
	"testing"
)

func TestFormatPowerShellJoinCommandKeepsLongTokenVisible(t *testing.T) {
	command := "codex-bridge join --host macbook-air.example.ts.net --port 61152 --instance " + strings.Repeat("i", 26) + " --token " + strings.Repeat("t", 32)
	formatted := formatPowerShellJoinCommand(command)

	if !strings.Contains(formatted, strings.Repeat("t", 32)) {
		t.Fatalf("formatted command lost token characters: %q", formatted)
	}
	if !strings.Contains(formatted, " `\n") {
		t.Fatalf("formatted command should use PowerShell continuations: %q", formatted)
	}
	flattened := strings.ReplaceAll(formatted, " `\n  ", " ")
	if flattened != command {
		t.Fatalf("formatted command did not preserve exact arguments: %q", flattened)
	}
	for _, line := range strings.Split(formatted, "\n") {
		if len(line) > 80 {
			t.Fatalf("command line may be clipped in a standard terminal: %d columns: %q", len(line), line)
		}
	}
}
