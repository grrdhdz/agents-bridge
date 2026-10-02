package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Inspect production dependencies, including transitive imports, so a helper
// cannot accidentally couple the reusable core to one of its clients.
func TestCoreDoesNotDependOnClients(t *testing.T) {
	const module = "github.com/grrdhdz/agents-bridge/engine"
	coreRoots := []string{"protocol", "bridge", "control", "bridges"}
	isWithin := func(path, root string) bool {
		return path == root || strings.HasPrefix(path, root+"/")
	}
	isCore := func(path string) bool {
		for _, root := range coreRoots {
			if isWithin(path, module+"/internal/"+root) {
				return true
			}
		}
		if !strings.HasPrefix(path, module+"/internal/") {
			return false
		}
		// Future hook packages are covered as soon as they are added, without
		// listing a package pattern that currently does not exist.
		for _, part := range strings.Split(strings.TrimPrefix(path, module+"/internal/"), "/") {
			if strings.HasPrefix(part, "hook") {
				return true
			}
		}
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-mod=readonly", "-deps", "-json", "./internal/...")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("inspect dependency graph: %v\n%s", err, stderr.String())
	}

	seen := make(map[string]bool)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg struct {
			ImportPath string
			Deps       []string
		}
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode dependency graph: %v", err)
		}
		if !isCore(pkg.ImportPath) {
			continue
		}
		seen[pkg.ImportPath] = true
		for _, dependency := range pkg.Deps {
			for _, client := range []string{module + "/internal/tui", module + "/internal/api", module + "/api"} {
				if isWithin(dependency, client) {
					t.Errorf("core package %s depends on client %s", pkg.ImportPath, dependency)
				}
			}
		}
	}
	for _, root := range coreRoots {
		if !seen[module+"/internal/"+root] {
			t.Errorf("core package %s was not inspected", root)
		}
	}
}
