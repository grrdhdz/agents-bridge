package hooks

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
)

type Command struct {
	Action, InstanceID string
	Role               string
	Force, Exempt      bool
}
type shellToken struct {
	text     string
	operator bool
}

// lexShell is deliberately static: quotes and line continuations are decoded,
// but substitutions, here-documents and redirections are not evaluated. Unknown
// shell syntax fails open; the control endpoint still enforces its send guard.
func lexShell(input string) ([]shellToken, error) {
	var tokens []shellToken
	var word strings.Builder
	started := false
	quote := rune(0)
	flush := func() {
		if started {
			tokens = append(tokens, shellToken{text: word.String()})
			word.Reset()
			started = false
		}
	}
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		if c == '$' || c == '<' || c == '>' {
			return nil, errors.New("dynamic shell syntax")
		}
		if c == '`' {
			if i+1 < len(runes) && (runes[i+1] == '\n' || runes[i+1] == '\r') {
				i++
				if runes[i] == '\r' && i+1 < len(runes) && runes[i+1] == '\n' {
					i++
				}
				continue
			}
			return nil, errors.New("shell substitution")
		}
		if c == '\\' {
			current := word.String()
			if quote == 0 && ((len(current) >= 2 && current[1] == ':') || current == "." || strings.HasPrefix(current, ".\\")) {
				word.WriteRune(c)
				started = true
				continue
			}
			if i+1 == len(runes) {
				return nil, errors.New("unfinished escape")
			}
			next := runes[i+1]
			if next == '\n' {
				i++
				continue
			}
			if quote == '"' && next != '"' && next != '\\' {
				word.WriteRune(c)
				continue
			}
			i++
			started = true
			word.WriteRune(next)
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		if c == '\'' || c == '"' {
			started = true
			quote = c
			continue
		}
		if c == '#' && !started {
			for i+1 < len(runes) && runes[i+1] != '\n' {
				i++
			}
			continue
		}
		if c == '\n' || c == ';' || c == '|' || c == '&' {
			flush()
			op := string(c)
			if (c == '|' || c == '&') && i+1 < len(runes) && runes[i+1] == c {
				i++
				op += string(c)
			}
			tokens = append(tokens, shellToken{text: op, operator: true})
			continue
		}
		if unicode.IsSpace(c) {
			flush()
			continue
		}
		started = true
		word.WriteRune(c)
	}
	if quote != 0 {
		return nil, errors.New("unfinished quote")
	}
	flush()
	return tokens, nil
}
func executableName(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	_, base, ok := strings.Cut(path, "/")
	if ok {
		parts := strings.Split(base, "/")
		path = parts[len(parts)-1]
	}
	return strings.TrimSuffix(path, ".exe")
}
func ParseCommand(input string) (Command, bool) { return parseCommand(input, 0) }
func parseCommand(input string, depth int) (Command, bool) {
	if depth > 3 || len(input) > 64*1024 {
		return Command{}, false
	}
	tokens, err := lexShell(input)
	if err != nil {
		return Command{}, false
	}
	var found []Command
	start := 0
	for end := 0; end <= len(tokens); end++ {
		if end < len(tokens) && !tokens[end].operator {
			continue
		}
		segment := tokens[start:end]
		if len(segment) > 0 {
			i := 0
			for i < len(segment) && strings.Contains(segment[i].text, "=") && !strings.HasPrefix(segment[i].text, "--") {
				i++
			}
			if i < len(segment) && executableName(segment[i].text) == "env" {
				i++
				for i < len(segment) && strings.Contains(segment[i].text, "=") {
					i++
				}
			}
			if i < len(segment) && (segment[i].text == "command" || segment[i].text == "exec") {
				i++
			}
			if i < len(segment) {
				name := executableName(segment[i].text)
				if name == "bash" || name == "sh" || name == "zsh" || name == "pwsh" || name == "powershell" {
					if i+2 == len(segment)-1 && (segment[i+1].text == "-c" || segment[i+1].text == "-lc" || strings.EqualFold(segment[i+1].text, "-Command")) {
						if cmd, ok := parseCommand(segment[i+2].text, depth+1); ok {
							found = append(found, cmd)
						}
					}
				} else if name == "agents-bridge" {
					args := make([]string, 0, len(segment)-i-1)
					for _, t := range segment[i+1:] {
						args = append(args, t.text)
					}
					cmd, ok := parseBridgeArgs(args)
					if !ok {
						return Command{}, false
					}
					if cmd.Action == "send" && start > 0 && tokens[start-1].text == "|" {
						cmd.Exempt = literalExempt(tokens[:start-1], args)
					}
					found = append(found, cmd)
				}
			}
		}
		start = end + 1
	}
	if len(found) != 1 {
		return Command{}, false
	}
	return found[0], true
}
func parseBridgeArgs(args []string) (Command, bool) {
	var cmd Command
	if len(args) == 1 && args[0] == "unbind" {
		return Command{Action: "unbind"}, true
	}
	if len(args) == 0 {
		return cmd, false
	}
	if args[0] == "ctl" {
		if len(args) < 2 {
			return cmd, false
		}
		cmd.Action = args[1]
		args = args[2:]
	} else if args[0] == "bind" {
		cmd.Action = "bind"
		args = args[1:]
	} else {
		return cmd, false
	}
	switch cmd.Action {
	case "send", "wait", "read", "watch", "peek", "export", "bind":
	default:
		return cmd, false
	}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		flag, value, eq := strings.Cut(args[i], "=")
		if !strings.HasPrefix(flag, "--") || seen[flag] {
			return Command{}, false
		}
		seen[flag] = true
		if flag == "--force" {
			if eq {
				v, err := strconv.ParseBool(value)
				if err != nil {
					return Command{}, false
				}
				cmd.Force = v
			} else {
				cmd.Force = true
			}
			continue
		}
		switch flag {
		case "--instance-id", "--role", "--body-file", "--message-id", "--format", "--timeout", "--after-event-seq", "--limit", "--output":
		default:
			return Command{}, false
		}
		if !eq {
			i++
			if i >= len(args) {
				return Command{}, false
			}
			value = args[i]
		}
		if value == "" || strings.ContainsAny(value, "$\x00") {
			return Command{}, false
		}
		switch flag {
		case "--instance-id":
			cmd.InstanceID = value
		case "--role":
			cmd.Role = value
		}
	}
	return cmd, cmd.InstanceID != "" && (cmd.Role == "executor" || cmd.Role == "orchestrator")
}
func literalExempt(prefix []shellToken, args []string) bool {
	stdin := false
	for i, arg := range args {
		if arg == "--body-file=-" || (arg == "--body-file" && i+1 < len(args) && args[i+1] == "-") {
			stdin = true
		}
	}
	if !stdin || len(prefix) < 2 {
		return false
	}
	for _, t := range prefix {
		if t.operator {
			return false
		}
	}
	name := executableName(prefix[0].text)
	body := ""
	if name == "printf" {
		if prefix[1].text == "%s\\n" && len(prefix) == 3 {
			body = prefix[2].text
		} else if len(prefix) == 2 && !strings.Contains(prefix[1].text, "%") {
			body = prefix[1].text
		}
		body = strings.ReplaceAll(body, "\\n", "\n")
	} else if name == "echo" && len(prefix) == 2 {
		body = prefix[1].text
	}
	first, _, _ := strings.Cut(body, "\n")
	first = strings.TrimSuffix(first, "\r")
	return first == "URGENTE" || first == "FIN"
}
