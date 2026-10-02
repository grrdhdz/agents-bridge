package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	contract "github.com/grrdhdz/agents-bridge/engine/api"
)

func validateAPIRemainder(t *testing.T, decoder *json.Decoder) {
	t.Helper()
	for {
		var packet map[string]any
		if err := decoder.Decode(&packet); err == io.EOF {
			return
		} else if err != nil {
			t.Fatal(err)
		}
		if err := contract.Validate(packet); err != nil {
			t.Fatal(err, packet)
		}
	}
}

func TestRunAPIHelloAndCLIUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runAPI(context.Background(), nil, ctlEnv{stdin: strings.NewReader("{\"v\":1,\"id\":\"hello\",\"op\":\"hello\",\"args\":{}}\n"), stdout: &out, stderr: &errOut, root: filepath.Join(t.TempDir(), "instances")})
	if code != 0 || !bytes.Contains(out.Bytes(), []byte(appVersion)) {
		t.Fatal(code, out.String(), errOut.String())
	}
	validateAPIRemainder(t, json.NewDecoder(bytes.NewReader(out.Bytes())))
	if code = runAPI(context.Background(), []string{"extra"}, ctlEnv{stdout: &out, stderr: &errOut}); code != exitUsage {
		t.Fatal(code)
	}
}
func TestAPIRealProcessCreateAndEOF(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "agents-bridge")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	build := exec.Command("go", "build", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	temporary := t.TempDir()
	env := append(os.Environ(), "TMPDIR="+temporary, "LOCALAPPDATA="+temporary)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "api")
	cmd.Env = env
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	decoder := json.NewDecoder(stdout)
	send := func(id, op string, args any) map[string]any {
		t.Helper()
		json.NewEncoder(stdin).Encode(map[string]any{"v": 1, "id": id, "op": op, "args": args})
		var response map[string]any
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err, stderr.String())
		}
		if err := contract.Validate(response); err != nil {
			t.Fatal(err, response)
		}
		return response
	}
	r := send("create", "create_local", map[string]any{"idle_timeout": "1m"})
	if r["ok"] != true {
		t.Fatal(r)
	}
	id := r["result"].(map[string]any)["instance_id"].(string)
	stop := func() { c := exec.Command(exe, "stop", "--instance-id", id); c.Env = env; c.Run() }
	t.Cleanup(stop)
	if send("health", "health", map[string]any{"instance_id": id})["ok"] != true {
		t.Fatal("new bridge unavailable")
	}
	send("sub", "subscribe", map[string]any{"instance_id": id})
	stdin.Close()
	validateAPIRemainder(t, decoder)
	if err := cmd.Wait(); err != nil {
		t.Fatal(err, stderr.String())
	}
	// EOF must leave the successfully created bridge alive. A fresh API client
	// uses only the public command and never reads its descriptors here.
	probe := exec.CommandContext(ctx, exe, "api")
	probe.Env = env
	probeIn, _ := probe.StdinPipe()
	probeOut, _ := probe.StdoutPipe()
	if err := probe.Start(); err != nil {
		t.Fatal(err)
	}
	json.NewEncoder(probeIn).Encode(map[string]any{"v": 1, "id": "still-live", "op": "health", "args": map[string]any{"instance_id": id}})
	var response map[string]any
	probeDecoder := json.NewDecoder(probeOut)
	err := probeDecoder.Decode(&response)
	probeIn.Close()
	validateAPIRemainder(t, probeDecoder)
	waitErr := probe.Wait()
	if err != nil || waitErr != nil || response["ok"] != true {
		t.Fatal("EOF stopped bridge", response, err, waitErr)
	}
	if err := contract.Validate(response); err != nil {
		t.Fatal(err, response)
	}
	stop()
}
