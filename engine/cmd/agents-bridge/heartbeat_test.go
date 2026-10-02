package main

import (
	"bytes"
	"testing"
)

func TestPSIncludesHeartbeatTool(t *testing.T) {
	var out bytes.Buffer
	err := writePSTable(&out, []psRow{{InstanceID: "one", RoleStates: map[string]psRoleState{"executor": {State: "trabajando", Tool: "Bash"}}}})
	if err != nil || !bytes.Contains(out.Bytes(), []byte("trabajando (Bash)")) {
		t.Fatalf("tool missing from ps: %s %v", &out, err)
	}
}
