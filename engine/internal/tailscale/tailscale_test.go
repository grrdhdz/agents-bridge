package tailscale

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestDetectWithRunnerUsesIPv4AndMagicDNS(t *testing.T) {
	info, err := DetectWithRunner(context.Background(), func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 2 && args[0] == "ip" {
			return []byte("100.64.0.10\n100.64.0.11\n"), nil
		}
		return []byte(`{"Self":{"DNSName":"mac.example.ts.net."}}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.IPv4 != "100.64.0.10" || info.DNSName != "mac.example.ts.net" {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestDetectWithRunnerFallsBackToIP(t *testing.T) {
	info, err := DetectWithRunner(context.Background(), func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "ip" {
			return []byte("100.64.0.20\n"), nil
		}
		return nil, errors.New("status unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.DNSName != info.IPv4 {
		t.Fatalf("expected IP fallback, got %+v", info)
	}
}

func TestDetectWithRunnerRejectsInvalidIP(t *testing.T) {
	_, err := DetectWithRunner(context.Background(), func(_ context.Context, _ string, args ...string) ([]byte, error) {
		return []byte("not-an-ip\n"), nil
	})
	if err == nil {
		t.Fatal("expected invalid IP error")
	}
}

func TestResolveExecutablePrefersPath(t *testing.T) {
	got, err := resolveExecutableFor(
		"darwin",
		func(name string) (string, error) {
			if name != "tailscale" {
				t.Fatalf("unexpected PATH lookup: %q", name)
			}
			return "/custom/bin/tailscale", nil
		},
		func(path string) bool {
			t.Fatalf("fallback must not be checked when PATH resolves: %s", path)
			return false
		},
		"/Users/tester",
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/custom/bin/tailscale" {
		t.Fatalf("expected PATH executable, got %q", got)
	}
}

func TestResolveExecutableFindsMacOSBundleFallback(t *testing.T) {
	global := "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	got, err := resolveExecutableFor(
		"darwin",
		func(string) (string, error) { return "", errors.New("not on PATH") },
		func(path string) bool { return path == global },
		"/Users/tester",
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != global {
		t.Fatalf("expected macOS bundle fallback, got %q", got)
	}
}

func TestResolveExecutableFindsUserMacOSBundleFallback(t *testing.T) {
	userBundle := "/Users/tester/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	got, err := resolveExecutableFor(
		"darwin",
		func(string) (string, error) { return "", errors.New("not on PATH") },
		func(path string) bool { return path == userBundle },
		"/Users/tester",
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != userBundle {
		t.Fatalf("expected user app fallback, got %q", got)
	}
}

func TestResolveExecutableFindsWindowsProgramFilesFallback(t *testing.T) {
	want := `C:\Program Files\Tailscale\tailscale.exe`
	got, err := resolveExecutableFor(
		"windows",
		func(string) (string, error) { return "", errors.New("not on PATH") },
		func(path string) bool { return path == want },
		"",
		func(name string) string {
			if name == "ProgramFiles" {
				return `C:\Program Files`
			}
			return ""
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected Windows fallback, got %q", got)
	}
}

func TestDetectWithExecutableUsesSupportedSubcommands(t *testing.T) {
	const executable = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	called := make([]string, 0, 2)
	info, err := DetectWithExecutable(context.Background(), executable, func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != executable {
			t.Fatalf("used unexpected executable %q", name)
		}
		called = append(called, fmt.Sprintf("%s %s", name, args))
		if len(args) == 2 && args[0] == "ip" && args[1] == "--4" {
			return []byte("100.64.0.42\n"), nil
		}
		if len(args) == 2 && args[0] == "status" && args[1] == "--json" {
			return []byte(`{"Self":{"DNSName":"mac.example.ts.net."}}`), nil
		}
		return nil, errors.New("unexpected subcommand")
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.IPv4 != "100.64.0.42" || info.DNSName != "mac.example.ts.net" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if len(called) != 2 {
		t.Fatalf("expected ip and status validation calls, got %v", called)
	}
}
