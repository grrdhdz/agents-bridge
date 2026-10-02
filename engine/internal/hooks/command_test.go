package hooks

import "testing"

func TestParseBridgeCommandStaticShellForms(t *testing.T) {
	for _, cmd := range []string{
		`agents-bridge ctl wait --instance-id abc --role executor --timeout 5m`,
		`agents-bridge ctl wait --role=executor --instance-id='abc'`,
		`cd /repo && /opt/bin/agents-bridge ctl wait --instance-id "abc" --role executor`,
		"agents-bridge ctl wait \\\n --instance-id abc --role executor",
		`& 'C:\Program Files\agents-bridge.exe' ctl wait --instance-id abc --role executor`,
		`env FOO=bar agents-bridge ctl wait --instance-id abc --role executor`,
		`bash -lc 'agents-bridge ctl wait --instance-id abc --role executor'`,
	} {
		got, ok := ParseCommand(cmd)
		if !ok || got.Action != "wait" || got.InstanceID != "abc" || got.Role != "executor" {
			t.Fatalf("parse %q: %+v %v", cmd, got, ok)
		}
	}
	for _, cmd := range []string{`echo 'agents-bridge ctl wait --instance-id abc --role executor'`, `cat <<EOF
agents-bridge ctl wait --instance-id abc --role executor
EOF`, `agents-bridge ctl send --instance-id $(cat id) --role executor`, `agents-bridge ctl wait --instance-id abc --instance-id xyz --role executor`, `agents-bridge ctl wait --instance-id abc`, `agents-bridge ctl wait --instance-id abc --role executor; agents-bridge ctl wait --instance-id xyz --role executor`, `agents-bridge ctl wait --instance-id abc --role executor > /tmp/x --force`} {
		if got, ok := ParseCommand(cmd); ok {
			t.Fatalf("accepted ambiguous/dynamic command %q: %+v", cmd, got)
		}
	}
}
func TestParseSendForceAndLiteralUrgentBody(t *testing.T) {
	for _, test := range []struct {
		command       string
		force, exempt bool
	}{
		{`agents-bridge ctl send --instance-id abc --role executor --force`, true, false},
		{`agents-bridge ctl send --instance-id abc --role executor --force=false`, false, false},
		{`printf 'URGENTE\nalto\n' | agents-bridge ctl send --instance-id abc --role executor --body-file -`, false, true},
		{`printf '%s\n' 'FIN' | agents-bridge ctl send --instance-id abc --role executor --body-file=-`, false, true},
		{`printf 'RESULTADO\nhecho' | agents-bridge ctl send --instance-id abc --role executor --body-file -`, false, false},
		{`agents-bridge ctl send --instance-id abc --role executor --body-file URGENTE`, false, false},
	} {
		got, ok := ParseCommand(test.command)
		if !ok || got.Force != test.force || got.Exempt != test.exempt {
			t.Fatalf("parse send %q: %+v %v", test.command, got, ok)
		}
	}
	if got, ok := ParseCommand(`agents-bridge bind --instance-id abc --role executor`); !ok || got.Action != "bind" {
		t.Fatalf("bind: %+v %v", got, ok)
	}
	if got, ok := ParseCommand(`agents-bridge unbind`); !ok || got.Action != "unbind" {
		t.Fatalf("unbind: %+v %v", got, ok)
	}
}

func TestParserHandlesCommentsAndUnquotedWindowsPaths(t *testing.T) {
	for _, command := range []string{
		"# tool step\nagents-bridge ctl wait --instance-id abc --role executor",
		`C:\bin\agents-bridge.exe ctl wait --instance-id abc --role executor`,
		"& C:\\bin\\agents-bridge.exe ctl wait `\n --instance-id abc --role executor",
	} {
		if got, ok := ParseCommand(command); !ok || got.InstanceID != "abc" {
			t.Fatalf("static platform command %q: %+v %v", command, got, ok)
		}
	}
}
