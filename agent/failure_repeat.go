package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const failureRepeatLimit = 3

func failureRepeatKey(name, result string) string {
	name = strings.TrimSpace(name)
	kind, target := failureRepeatParts(result)
	if name == "" || kind == "" || target == "" {
		return ""
	}
	return name + "|" + kind + "|" + target
}

func failureRepeatParts(result string) (kind, target string) {
	text := strings.TrimSpace(result)
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "restricted path") || strings.Contains(lower, "outside sandbox") || strings.Contains(lower, "access denied"):
		kind = "sandbox"
	case strings.Contains(lower, "command timed out") || strings.Contains(lower, "timed out after"):
		kind = "timeout"
	case strings.Contains(lower, "no such file") || strings.Contains(lower, "not found") || strings.Contains(lower, "cannot find"):
		kind = "missing"
	default:
		return "", ""
	}
	target = failureRepeatTarget(text)
	if target == "" {
		target = failureRepeatTarget(firstLine(text))
	}
	return kind, target
}

func failureRepeatTarget(text string) string {
	for _, marker := range []string{"Requested: ", "restricted path (", "restricted path "} {
		if idx := strings.Index(text, marker); idx >= 0 {
			rest := strings.TrimSpace(text[idx+len(marker):])
			rest = strings.Trim(rest, " )]")
			if path := cleanFailurePath(rest); path != "" {
				return path
			}
		}
	}
	fields := strings.Fields(text)
	for i := len(fields) - 1; i >= 0; i-- {
		if path := pathFromToken(fields[i]); path != "" {
			return path
		}
	}
	if idx := strings.LastIndex(text, ": "); idx >= 0 {
		return cleanFailurePath(text[:idx])
	}
	return ""
}

func looksLikeFailurePath(path string) bool {
	return strings.Contains(path, "/") || strings.Contains(path, `\`) || strings.HasPrefix(path, "~") || strings.Contains(path, ".")
}

func cleanFailurePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`,;:()[]'\"")
	if path == "" || strings.ContainsAny(path, " \t\n") {
		return ""
	}
	path = strings.TrimPrefix(path, "file://")
	slash := filepath.ToSlash(path)
	switch {
	case strings.HasPrefix(slash, "~/"):
		slash = slash[2:]
	case strings.HasPrefix(slash, "/home/") || strings.HasPrefix(slash, "/Users/"):
		parts := strings.SplitN(strings.TrimPrefix(slash, "/"), "/", 3)
		if len(parts) == 3 {
			slash = parts[2]
		}
	}
	slash = filepath.Clean(slash)
	slash = filepath.ToSlash(slash)
	slash = strings.TrimPrefix(slash, "./")
	return strings.ToLower(slash)
}

func firstLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
}

func formatRepeatedToolStop(sigs []string, last map[string]string) string {
	var b strings.Builder
	b.WriteString("Detected repeated tool-call loop and stopped early to avoid timeout.\n")
	b.WriteString("Latest tool outputs:\n")
	for _, sig := range sigs {
		label := formatRepeatedToolSignature(sig)
		out := "(no cached output)"
		if last != nil {
			if v := strings.TrimSpace(last[sig]); v != "" {
				out = v
			}
		}
		if len(out) > 240 {
			out = out[:240] + "...(truncated)"
		}
		b.WriteString(fmt.Sprintf("- %s: %s\n", label, out))
	}
	return strings.TrimSpace(b.String())
}

func formatRepeatedToolSignature(sig string) string {
	parts := strings.SplitN(sig, "|", 2)
	name := parts[0]
	if len(parts) == 1 || strings.TrimSpace(parts[1]) == "" {
		return name
	}
	args := strings.TrimSpace(parts[1])
	if len(args) > 240 {
		args = args[:240] + "...(truncated)"
	}
	return fmt.Sprintf("%s %s", name, args)
}

func (s *streamConvergenceState) failureRepeated(name, arguments string) bool {
	if s == nil {
		return false
	}
	return failureCountReached(s.failureRepeatCount, name, arguments)
}

func (s *streamConvergenceState) noteFailureRepeat(name, result string) {
	if s == nil {
		return
	}
	key := failureRepeatKey(name, result)
	if key == "" {
		return
	}
	if s.failureRepeatCount == nil {
		s.failureRepeatCount = make(map[string]int)
	}
	s.failureRepeatCount[key]++
}

func (s *loopRuntimeState) failureRepeated(name, arguments string) bool {
	if s == nil {
		return false
	}
	return failureCountReached(s.failureRepeatCount, name, arguments)
}

func (s *loopRuntimeState) noteFailureRepeat(name, result string) {
	if s == nil {
		return
	}
	key := failureRepeatKey(name, result)
	if key == "" {
		return
	}
	if s.failureRepeatCount == nil {
		s.failureRepeatCount = make(map[string]int)
	}
	s.failureRepeatCount[key]++
}

func failureCountReached(counts map[string]int, name, arguments string) bool {
	path := toolCallPathArg(arguments)
	if path == "" || counts == nil {
		return false
	}
	for _, kind := range []string{"sandbox", "timeout", "missing"} {
		if counts[name+"|"+kind+"|"+path] >= failureRepeatLimit {
			return true
		}
	}
	return false
}

func toolCallPathArg(arguments string) string {
	var args map[string]any
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return ""
	}
	for _, key := range []string{"path", "file", "workdir"} {
		value, _ := args[key].(string)
		if path := cleanFailurePath(value); path != "" && looksLikeFailurePath(path) {
			return path
		}
	}
	command, _ := args["command"].(string)
	command = strings.ReplaceAll(command, `\'`, `'`)
	command = strings.ReplaceAll(command, `\"`, `"`)
	direct := failureRepeatTarget(command)
	nested := failurePathInText(command)
	if len(nested) > len(direct) {
		return nested
	}
	return direct
}

func failurePathInText(text string) string {
	var best string
	consider := func(candidate string) {
		path := pathFromToken(candidate)
		if len(path) > len(best) {
			best = path
		}
	}
	for i := 0; i < len(text); i++ {
		if text[i] != '\'' && text[i] != '"' && text[i] != '`' {
			continue
		}
		quote := text[i]
		end := i + 1
		for end < len(text) && text[end] != quote {
			end++
		}
		if end >= len(text) {
			continue
		}
		consider(text[i+1 : end])
		i = end
	}
	if best != "" {
		return best
	}
	return pathFromToken(text)
}

func pathFromToken(token string) string {
	token = strings.Trim(token, "`,;:()[]'\"")
	start := strings.Index(token, "~/")
	slash := strings.Index(token, "/")
	if start < 0 || (slash >= 0 && slash < start) {
		start = slash
	}
	if start < 0 {
		if looksLikeFailurePath(token) && !strings.ContainsAny(token, "()") {
			return cleanFailurePath(token)
		}
		return ""
	}
	rest := token[start:]
	end := 0
	for end < len(rest) && !strings.ContainsRune(" \t'\"`),;:()[]", rune(rest[end])) {
		end++
	}
	path := cleanFailurePath(rest[:end])
	if path == "" || !strings.Contains(path, "/") {
		return ""
	}
	return path
}
