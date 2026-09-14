// Package clipboard provides the small platform adapter used by the TUI.
package clipboard

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Copy writes text to the native clipboard command when one is available.
// The command is deliberately kept outside the TUI so tests can inject a
// deterministic copier without touching a user's clipboard.
func Copy(text string) error {
	command := ""
	switch runtime.GOOS {
	case "darwin":
		command = "pbcopy"
	case "windows":
		command = "clip.exe"
	default:
		return fmt.Errorf("copia automática no disponible en %s; usa el fallback completo de la terminal", runtime.GOOS)
	}

	cmd := exec.Command(command)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s no pudo copiar el comando: %w; usa el fallback completo de la terminal", command, err)
	}
	return nil
}
