package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	openCLIActionRaw             = "raw"
	openCLIActionWebRead         = "web_read"
	openCLIActionSite            = "site"
	openCLIActionBrowser         = "browser"
	openCLIActionTwitterTimeline = "twitter_timeline"
)

// OpenCLITool exposes OpenCLI as one entrypoint for website adapters, browser
// automation, URL-to-Markdown extraction, and raw passthrough commands.
func OpenCLITool(cfg *OpenCLIConfig, fallbackCfg *WebSearchConfig) *Tool {
	if cfg == nil {
		cfg = &OpenCLIConfig{}
	}
	normalized := normalizeOpenCLIConfig(cfg)
	return &Tool{
		Name:        "opencli",
		Description: "Run OpenCLI for website adapters, authenticated browser sessions, URL-to-Markdown extraction, and raw OpenCLI commands. Use action=web_read for any URL, action=site for adapters like twitter/youtube/zhihu/xiaohongshu, action=browser for browser primitives, and action=raw for doctor/list/external/plugin commands. Do not pass bash/sh commands here; use terminal for shell execution.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"action":          {Type: "string", Description: "Operation to run: web_read, site, twitter_timeline, browser, or raw. If omitted, it is inferred from url/site/args.", Required: false},
			"url":             {Type: "string", Description: "URL for action=web_read. The tool returns Markdown from OpenCLI stdout.", Required: false},
			"site":            {Type: "string", Description: "OpenCLI adapter/site name for action=site, for example twitter, youtube, zhihu, xiaohongshu, bilibili, web.", Required: false},
			"command":         {Type: "string", Description: "Adapter command for action=site, or browser subcommand for action=browser.", Required: false},
			"args":            {Type: "array", Description: "Additional OpenCLI arguments. For action=raw this is the complete argument list after the opencli binary; do not include bash/sh unless it wraps an opencli command.", Required: false},
			"format":          {Type: "string", Description: "OpenCLI output format for site/browser-aware commands when no -f/--format is already present. Common values: md, json, yaml, table, csv.", Required: false, Default: "md"},
			"limit":           {Type: "number", Description: "Common item limit for adapter commands such as twitter timeline.", Required: false},
			"feed_type":       {Type: "string", Description: "Twitter timeline feed type. Defaults to following for authenticated following feed; use for-you only when explicitly requested.", Required: false, Default: "following"},
			"browser_session": {Type: "string", Description: "Browser session name for action=browser. Reuse the same value to keep tab state.", Required: false, Default: "luckyharness"},
			"max_chars":       {Type: "number", Description: "Maximum characters returned to the model.", Required: false, Default: normalized.MaxChars},
			"timeout_seconds": {Type: "number", Description: "Per-command timeout in seconds.", Required: false, Default: normalized.TimeoutSeconds},
		},
		Handler: func(args map[string]any) (string, error) { return handleOpenCLI(normalized, fallbackCfg, args) },
	}
}

type openCLIInvocation struct {
	Action         string
	Command        string
	Args           []string
	URL            string
	MaxChars       int
	TimeoutSeconds int
}

func normalizeOpenCLIConfig(cfg *OpenCLIConfig) *OpenCLIConfig {
	out := &OpenCLIConfig{
		Enabled:            cfg.Enabled,
		Command:            strings.TrimSpace(cfg.Command),
		Args:               append([]string(nil), cfg.Args...),
		TimeoutSeconds:     cfg.TimeoutSeconds,
		MaxChars:           cfg.MaxChars,
		FallbackToWebFetch: cfg.FallbackToWebFetch,
	}
	if out.Command == "" {
		out.Command = "opencli"
	}
	if len(out.Args) == 0 {
		out.Args = defaultOpenCLIWebReadArgs()
	}
	if out.TimeoutSeconds <= 0 {
		out.TimeoutSeconds = 20
	}
	if out.MaxChars <= 0 {
		out.MaxChars = 50000
	}
	return out
}

func defaultOpenCLIWebReadArgs() []string {
	return []string{
		"web", "read",
		"--url", "{url}",
		"--stdout", "true",
		"--download-images", "false",
		"-f", "md",
	}
}

func handleOpenCLI(cfg *OpenCLIConfig, fallbackCfg *WebSearchConfig, args map[string]any) (string, error) {
	inv, err := buildOpenCLIInvocation(cfg, args)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(inv.TimeoutSeconds)*time.Second)
	defer cancel()

	output, err := runOpenCLI(ctx, inv.Command, inv.Args, inv.MaxChars)
	if inv.Action == openCLIActionWebRead {
		if saved := readOpenCLISavedMarkdown(output, inv.MaxChars); saved != "" {
			return formatOpenCLIResult(saved, inv.MaxChars), nil
		}
	}
	if err == nil && strings.TrimSpace(output) != "" {
		return formatOpenCLIResult(output, inv.MaxChars), nil
	}
	if inv.Action == openCLIActionWebRead && cfg.FallbackToWebFetch && fallbackCfg != nil && strings.TrimSpace(inv.URL) != "" {
		if result, fallbackErr := handleWebFetch(fallbackCfg, map[string]any{"url": inv.URL, "max_chars": inv.MaxChars}); fallbackErr == nil && strings.TrimSpace(result) != "" {
			return result, nil
		}
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("opencli returned empty output")
}

func buildOpenCLIInvocation(cfg *OpenCLIConfig, raw map[string]any) (openCLIInvocation, error) {
	if cfg == nil {
		cfg = normalizeOpenCLIConfig(&OpenCLIConfig{})
	}
	inv := openCLIInvocation{
		Command:        cfg.Command,
		MaxChars:       openCLINumberArg(raw, "max_chars", cfg.MaxChars),
		TimeoutSeconds: openCLINumberArg(raw, "timeout_seconds", cfg.TimeoutSeconds),
	}
	if inv.MaxChars <= 0 {
		inv.MaxChars = cfg.MaxChars
	}
	if inv.TimeoutSeconds <= 0 {
		inv.TimeoutSeconds = cfg.TimeoutSeconds
	}

	action := normalizeOpenCLIAction(openCLIStringArg(raw, "action"))
	url := strings.TrimSpace(openCLIStringArg(raw, "url"))
	site := strings.TrimSpace(openCLIStringArg(raw, "site"))
	commandName := strings.TrimSpace(openCLIStringArg(raw, "command"))
	extraArgs, err := optionalStringSlice(raw["args"])
	if err != nil {
		return inv, fmt.Errorf("args: %w", err)
	}

	if action == "" {
		switch {
		case url != "":
			action = openCLIActionWebRead
		case site != "" || commandName != "":
			action = openCLIActionSite
		default:
			action = openCLIActionRaw
		}
	}
	inv.Action = action

	switch action {
	case openCLIActionWebRead:
		if url == "" {
			return inv, fmt.Errorf("url is required for opencli action=web_read")
		}
		if err := validateFetchURL(url); err != nil {
			return inv, fmt.Errorf("url validation failed: %w", err)
		}
		args, err := expandOpenCLIArgs(cfg.Args, url, inv.MaxChars)
		if err != nil {
			return inv, err
		}
		args = ensureOpenCLIOption(args, "--stdout", "true")
		args = ensureOpenCLIOption(args, "--download-images", "false")
		args = appendFormatArg(args, openCLIStringArgDefault(raw, "format", "md"))
		inv.URL = url
		inv.Args = append(args, extraArgs...)
		return inv, nil

	case openCLIActionTwitterTimeline:
		limit := openCLINumberArg(raw, "limit", 10)
		if limit <= 0 {
			limit = 10
		}
		feedType := strings.TrimSpace(openCLIStringArgDefault(raw, "feed_type", "following"))
		if feedType == "" {
			feedType = "following"
		}
		args := []string{"twitter", "timeline", "--type", feedType, "--limit", strconv.Itoa(limit)}
		args = append(args, extraArgs...)
		inv.Args = appendFormatArg(args, openCLIStringArgDefault(raw, "format", "md"))
		return inv, nil

	case openCLIActionSite:
		if site == "" {
			return inv, fmt.Errorf("site is required for opencli action=site")
		}
		args := []string{site}
		if commandName != "" {
			args = append(args, commandName)
		}
		args = append(args, extraArgs...)
		if strings.EqualFold(site, "twitter") && strings.EqualFold(commandName, "timeline") {
			args = ensureOpenCLIOption(args, "--type", openCLIStringArgDefault(raw, "feed_type", "following"))
			if limit := openCLINumberArg(raw, "limit", 0); limit > 0 {
				args = ensureOpenCLIOption(args, "--limit", strconv.Itoa(limit))
			}
		} else if limit := openCLINumberArg(raw, "limit", 0); limit > 0 {
			args = ensureOpenCLIOption(args, "--limit", strconv.Itoa(limit))
		}
		inv.Args = appendFormatArg(args, openCLIStringArgDefault(raw, "format", "md"))
		return inv, nil

	case openCLIActionBrowser:
		session := strings.TrimSpace(openCLIStringArgDefault(raw, "browser_session", "luckyharness"))
		if session == "" {
			session = "luckyharness"
		}
		browserCommand := strings.TrimSpace(openCLIStringArg(raw, "browser_command"))
		if browserCommand == "" {
			browserCommand = commandName
		}
		if browserCommand == "" {
			if len(extraArgs) == 0 {
				return inv, fmt.Errorf("command or args is required for opencli action=browser")
			}
			inv.Args = append([]string{"browser", session}, extraArgs...)
			return inv, nil
		}
		inv.Args = append([]string{"browser", session, browserCommand}, extraArgs...)
		return inv, nil

	case openCLIActionRaw:
		if len(extraArgs) == 0 {
			return inv, fmt.Errorf("args is required for opencli action=raw")
		}
		args, err := normalizeRawOpenCLIArgs(extraArgs)
		if err != nil {
			return inv, err
		}
		inv.Args = args
		return inv, nil

	default:
		return inv, fmt.Errorf("unsupported opencli action %q", action)
	}
}

func normalizeRawOpenCLIArgs(args []string) ([]string, error) {
	args = trimOpenCLIArgs(args)
	if len(args) == 0 {
		return nil, fmt.Errorf("args is required for opencli action=raw")
	}
	if isOpenCLIBinaryName(args[0]) {
		stripped := trimOpenCLIArgs(args[1:])
		if len(stripped) == 0 {
			return nil, fmt.Errorf("args is required after the opencli binary for action=raw")
		}
		return stripped, nil
	}
	if isShellBinaryName(args[0]) {
		unwrapped, ok, err := unwrapOpenCLIFromShellArgs(args)
		if err != nil {
			return nil, err
		}
		if ok {
			return unwrapped, nil
		}
		return nil, fmt.Errorf("opencli action=raw only accepts OpenCLI arguments, not shell command %q; use the terminal tool for bash or sh commands", args[0])
	}
	return args, nil
}

func trimOpenCLIArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			out = append(out, arg)
		}
	}
	return out
}

func isOpenCLIBinaryName(name string) bool {
	base := filepath.Base(strings.TrimSpace(name))
	return base == "opencli" || base == "opencli.cmd" || base == "opencli.exe"
}

func isShellBinaryName(name string) bool {
	base := filepath.Base(strings.TrimSpace(name))
	switch base {
	case "bash", "sh", "zsh", "fish", "dash":
		return true
	default:
		return false
	}
}

func unwrapOpenCLIFromShellArgs(args []string) ([]string, bool, error) {
	for i := 1; i < len(args); i++ {
		if args[i] != "-c" && args[i] != "-lc" {
			continue
		}
		if i+1 >= len(args) {
			return nil, false, fmt.Errorf("opencli action=raw received %s without a command string", args[i])
		}
		parts, err := splitOpenCLIShellCommand(args[i+1])
		if err != nil {
			return nil, false, err
		}
		parts = trimOpenCLIArgs(parts)
		if len(parts) == 0 || !isOpenCLIBinaryName(parts[0]) {
			return nil, false, nil
		}
		stripped := trimOpenCLIArgs(parts[1:])
		if len(stripped) == 0 {
			return nil, false, fmt.Errorf("args is required after the opencli binary for action=raw")
		}
		return stripped, true, nil
	}
	return nil, false, nil
}

func splitOpenCLIShellCommand(command string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, nil
	}
	var args []string
	var b strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		args = append(args, b.String())
		b.Reset()
	}
	for _, r := range command {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in wrapped opencli command")
	}
	flush()
	return args, nil
}

func normalizeOpenCLIAction(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "", openCLIActionRaw, "command", "passthrough":
		return action
	case openCLIActionWebRead, "web", "read", "read_url", "fetch", "url":
		return openCLIActionWebRead
	case openCLIActionSite, "adapter", "site_command":
		return openCLIActionSite
	case openCLIActionBrowser, "browser_command":
		return openCLIActionBrowser
	case openCLIActionTwitterTimeline, "following_timeline", "twitter_following", "twitter":
		return openCLIActionTwitterTimeline
	default:
		return action
	}
}

func runOpenCLI(ctx context.Context, command string, args []string, maxChars int) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "opencli"
	}
	cmd := exec.CommandContext(ctx, command, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout

	err := cmd.Run()
	output := normalizeOpenCLIOutput(stdout.String())
	if err != nil {
		if output != "" {
			return output, fmt.Errorf("opencli command failed: %w: %s", err, truncateForError(output))
		}
		return "", fmt.Errorf("opencli command failed: %w", err)
	}
	return output, nil
}

func expandOpenCLIArgs(template []string, rawURL string, maxChars int) ([]string, error) {
	args := make([]string, 0, len(template))
	for _, item := range template {
		if strings.TrimSpace(item) == "" {
			continue
		}
		replaced := strings.ReplaceAll(item, "{url}", rawURL)
		replaced = strings.ReplaceAll(replaced, "{max_chars}", strconv.Itoa(maxChars))
		args = append(args, replaced)
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("opencli args template is empty")
	}
	return args, nil
}

func appendFormatArg(args []string, format string) []string {
	format = strings.TrimSpace(format)
	if format == "" || hasOpenCLIOption(args, "-f", "--format") {
		return args
	}
	return append(args, "-f", format)
}

func ensureOpenCLIOption(args []string, name string, value string) []string {
	if strings.TrimSpace(name) == "" || hasOpenCLIOption(args, name) {
		return args
	}
	if strings.TrimSpace(value) == "" {
		return append(args, name)
	}
	return append(args, name, value)
}

func hasOpenCLIOption(args []string, names ...string) bool {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		for _, name := range names {
			name = strings.TrimSpace(name)
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}

func readOpenCLISavedMarkdown(output string, maxChars int) string {
	path := extractOpenCLISavedMarkdownPath(output)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Clean(path)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return ""
	}
	if info.Size() > int64(maxChars*4) && maxChars > 0 {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func extractOpenCLISavedMarkdownPath(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, ".md") || !strings.Contains(line, "|") {
			continue
		}
		cells := splitMarkdownTableLine(line)
		for _, cell := range cells {
			cell = strings.TrimSpace(cell)
			if strings.HasSuffix(cell, ".md") {
				return cell
			}
		}
	}
	return ""
}

func splitMarkdownTableLine(line string) []string {
	parts := strings.Split(strings.Trim(line, " |"), "|")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func coerceStringSlice(v any) ([]string, error) {
	switch items := v.(type) {
	case nil:
		return nil, nil
	case []string:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if strings.TrimSpace(item) == "" {
				continue
			}
			out = append(out, item)
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("non-string arg %T", item)
			}
			if strings.TrimSpace(s) == "" {
				continue
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected array of strings, got %T", v)
	}
}

func optionalStringSlice(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	return coerceStringSlice(v)
}

func openCLIStringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func openCLIStringArgDefault(args map[string]any, key string, fallback string) string {
	if v := openCLIStringArg(args, key); v != "" {
		return v
	}
	return fallback
}

func openCLINumberArg(args map[string]any, key string, fallback int) int {
	if args == nil {
		return fallback
	}
	raw, ok := args[key]
	if !ok {
		return fallback
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case json.Number:
		n, err := strconv.Atoi(v.String())
		if err == nil {
			return n
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return fallback
}

func formatOpenCLIResult(output string, maxChars int) string {
	cleaned := normalizeOpenCLIOutput(output)
	if cleaned == "" {
		return ""
	}
	if maxChars > 0 && len(cleaned) > maxChars {
		cleaned = cleaned[:maxChars] + "\n... (truncated)"
	}
	if strings.HasPrefix(cleaned, "# ") {
		return cleaned
	}
	if title := firstMarkdownTitle(cleaned); title != "" {
		return fmt.Sprintf("# %s\n\n%s", title, cleaned)
	}
	return cleaned
}

func normalizeOpenCLIOutput(output string) string {
	out := strings.TrimSpace(output)
	if out == "" {
		return ""
	}
	out = strings.ReplaceAll(out, "\r\n", "\n")
	return stripOpenCLIUpdateNotice(out)
}

func stripOpenCLIUpdateNotice(output string) string {
	lines := strings.Split(output, "\n")
	end := len(lines)
	for end > 0 {
		line := strings.TrimSpace(lines[end-1])
		if line == "" ||
			strings.HasPrefix(line, "Update available:") ||
			strings.HasPrefix(line, "Run: npm install -g @jackwener/opencli") {
			end--
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines[:end], "\n"))
}

func firstMarkdownTitle(result string) string {
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func truncateForError(output string) string {
	const limit = 1600
	output = strings.TrimSpace(output)
	if len(output) <= limit {
		return output
	}
	return output[:limit] + "... (truncated)"
}
