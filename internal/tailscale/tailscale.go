// Package tailscale discovers only the local address to which the ephemeral
// bridge should bind. It does not use Tailscale as an identity or data store.
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
)

var ErrUnavailable = errors.New("tailscale CLI is not installed or is not available")

type Info struct {
	IPv4    string
	DNSName string
}

type Runner func(context.Context, string, ...string) ([]byte, error)

func Detect(ctx context.Context) (Info, error) {
	executable, err := resolveExecutable()
	if err != nil {
		return Info{}, err
	}
	return DetectWithExecutable(ctx, executable, func(runCtx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(runCtx, name, args...).Output()
	})
}

func DetectWithRunner(ctx context.Context, run Runner) (Info, error) {
	return DetectWithExecutable(ctx, "tailscale", run)
}

func DetectWithExecutable(ctx context.Context, executable string, run Runner) (Info, error) {
	ipOutput, err := run(ctx, executable, "ip", "--4")
	if err != nil {
		return Info{}, fmt.Errorf("tailscale IPv4 discovery failed: %w", err)
	}
	ip := strings.TrimSpace(strings.SplitN(string(ipOutput), "\n", 2)[0])
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		return Info{}, fmt.Errorf("tailscale returned no valid IPv4 address: %q", ip)
	}

	dnsName := ""
	statusOutput, err := run(ctx, executable, "status", "--json")
	if err == nil {
		var status struct {
			Self struct {
				DNSName string `json:"DNSName"`
			} `json:"Self"`
		}
		if json.Unmarshal(statusOutput, &status) == nil {
			dnsName = strings.TrimSuffix(strings.TrimSpace(status.Self.DNSName), ".")
		}
	}
	if dnsName == "" {
		dnsName = ip
	}
	return Info{IPv4: ip, DNSName: dnsName}, nil
}

func resolveExecutable() (string, error) {
	homeDir, _ := os.UserHomeDir()
	return resolveExecutableFor(runtime.GOOS, exec.LookPath, executableFile, homeDir, os.Getenv)
}

// resolveExecutableFor keeps platform lookup policy injectable so resolution
// can be tested without modifying PATH or touching an installed Tailscale.
func resolveExecutableFor(goos string, lookPath func(string) (string, error), exists func(string) bool, homeDir string, getenv func(string) string) (string, error) {
	if executable, err := lookPath("tailscale"); err == nil && executable != "" {
		return executable, nil
	}
	for _, candidate := range fallbackCandidates(goos, homeDir, getenv) {
		if exists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: install Tailscale and ensure `tailscale` is on PATH or installed in its standard application location", ErrUnavailable)
}

func fallbackCandidates(goos, homeDir string, getenv func(string) string) []string {
	switch goos {
	case "darwin":
		candidates := []string{"/Applications/Tailscale.app/Contents/MacOS/Tailscale"}
		if homeDir != "" {
			candidates = append(candidates, joinPlatformPath(goos, homeDir, "Applications", "Tailscale.app", "Contents", "MacOS", "Tailscale"))
		}
		return candidates
	case "windows":
		candidates := make([]string, 0, 2)
		if programFiles := getenv("ProgramFiles"); programFiles != "" {
			candidates = append(candidates, joinPlatformPath(goos, programFiles, "Tailscale", "tailscale.exe"))
		}
		if programFiles32 := getenv("ProgramFiles(x86)"); programFiles32 != "" {
			candidates = append(candidates, joinPlatformPath(goos, programFiles32, "Tailscale", "tailscale.exe"))
		}
		return candidates
	default:
		return nil
	}
}

// joinPlatformPath joins with goos's separator, not the host's: filepath.Join
// would build a macOS path with backslashes when tests run on Windows.
func joinPlatformPath(goos string, first string, rest ...string) string {
	if goos != "windows" {
		parts := append([]string{first}, rest...)
		return path.Join(parts...)
	}
	joined := strings.TrimRight(first, `\/`)
	for _, part := range rest {
		joined += `\` + strings.Trim(part, `\/`)
	}
	return joined
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0111 != 0
}
