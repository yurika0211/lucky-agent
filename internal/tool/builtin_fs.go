package tool

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func terminalToolWithName(name string, hidden bool) *Tool {
	return &Tool{
		Name:            name,
		Description:     "Run a terminal command when you need to inspect runtime state, execute project commands, check the environment, or perform real system actions that cannot be answered from files alone.",
		Category:        CatBuiltin,
		Source:          "builtin",
		Permission:      PermApprove,
		ShellAware:      true,
		ParallelSafe:    false,
		HiddenFromModel: hidden,
		Parameters: map[string]Param{
			"command": {Type: "string", Description: "Concrete terminal command to run. Prefer precise inspection or execution commands over exploratory one-liners.", Required: true},
			"timeout": {Type: "number", Description: "Timeout in seconds (default 30)", Required: false, Default: 30},
			"workdir": {Type: "string", Description: "Optional working directory. Use when the command must run in a specific project or subdirectory.", Required: false},
		},
		Handler: handleShell,
	}
}

func TerminalTool() *Tool    { return terminalToolWithName("terminal", false) }
func LegacyShellTool() *Tool { return terminalToolWithName("shell", true) }
func ShellTool() *Tool       { return TerminalTool() }

func handleShell(args map[string]any) (string, error) {
	command, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command is required")
	}
	if err := validateShellSandbox(command); err != nil {
		return "", err
	}

	timeout := 30
	if t, ok := args["timeout"]; ok {
		switch v := t.(type) {
		case float64:
			timeout = int(v)
		case int:
			timeout = v
		}
	}
	if timeout <= 0 {
		timeout = 30
	}
	if timeout > 300 {
		timeout = 300
	}

	cwd, _ := args["_cwd"].(string)
	env, _ := args["_env"].(map[string]string)
	workdir := cwd
	if w, ok := args["workdir"]; ok {
		if ws, ok := w.(string); ok && ws != "" {
			workdir = ws
		}
	}

	prefix := ""
	if len(env) > 0 {
		validEnvKey := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
		for k, v := range env {
			if !validEnvKey.MatchString(k) {
				continue
			}
			escaped := strings.ReplaceAll(v, "'", "'\\''")
			prefix += fmt.Sprintf("export %s='%s'; ", k, escaped)
		}
	}
	fullCommand := prefix + command

	ctx := time.Duration(timeout) * time.Second
	cmd := exec.Command("sh", "-c", fullCommand)
	if workdir != "" {
		cmd.Dir = workdir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	select {
	case err := <-done:
		output := stdout.String()
		if stderr.Len() > 0 {
			output += "\n[stderr]\n" + stderr.String()
		}
		if err != nil {
			output += fmt.Sprintf("\n[exit code: %v]", err)
		}
		if len(output) > 10000 {
			output = output[:10000] + "\n... (truncated)"
		}
		return output, nil
	case <-time.After(ctx):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return "", fmt.Errorf("command timed out after %d seconds", timeout)
	}
}

func FileReadTool() *Tool {
	return &Tool{
		Name:        "file_read",
		Description: "Read a local file when repository or document contents are the source of truth. Prefer this before guessing about code, config, notes, or generated artifacts.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":   {Type: "string", Description: "Path to the local file that should be inspected.", Required: true},
			"offset": {Type: "number", Description: "Line number to start reading from (1-indexed)", Required: false, Default: 1},
			"limit":  {Type: "number", Description: "Maximum number of lines to read", Required: false, Default: 2000},
		},
		Handler: handleFileRead,
	}
}

func handleFileRead(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	offset := 1
	if o, ok := args["offset"]; ok {
		switch v := o.(type) {
		case float64:
			offset = int(v)
		case int:
			offset = v
		}
	}
	if offset < 1 {
		offset = 1
	}
	limit := 2000
	if l, ok := args["limit"]; ok {
		switch v := l.(type) {
		case float64:
			limit = int(v)
		case int:
			limit = v
		}
	}

	start := offset - 1
	if start >= len(lines) {
		return "", fmt.Errorf("offset %d exceeds file length %d", offset, len(lines))
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(fmt.Sprintf("%d| %s\n", i+1, lines[i]))
	}
	return b.String(), nil
}

func FileWriteTool() *Tool {
	return &Tool{
		Name:        "file_write",
		Description: "Write or overwrite a local file when the task requires creating, updating, or exporting a real artifact on disk.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"path":    {Type: "string", Description: "Target path of the file to create or overwrite.", Required: true},
			"content": {Type: "string", Description: "Full file content to write. Use complete intended content, not a diff.", Required: true},
		},
		Handler: handleFileWrite,
	}
}

func handleFileWrite(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return fmt.Sprintf("Written %d bytes to %s", len(content), path), nil
}

func FileMkdirTool() *Tool {
	return &Tool{
		Name:        "file_mkdir",
		Description: "Create a directory on disk. Use this before writing files into a new folder hierarchy.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"path":      {Type: "string", Description: "Directory path to create.", Required: true},
			"recursive": {Type: "boolean", Description: "Create parent directories when needed. Default true.", Required: false, Default: true},
		},
		Handler: handleFileMkdir,
	}
}

func handleFileMkdir(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	recursive := true
	if v, ok := args["recursive"].(bool); ok {
		recursive = v
	}
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		return fmt.Sprintf("Directory already exists: %s", path), nil
	case err == nil:
		return "", fmt.Errorf("path exists and is not a directory: %s", path)
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("stat path: %w", err)
	}
	if recursive {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", fmt.Errorf("create directory: %w", err)
		}
	} else if err := os.Mkdir(path, 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}
	return fmt.Sprintf("Created directory %s", path), nil
}

func FileMoveTool() *Tool {
	return &Tool{
		Name:        "file_move",
		Description: "Move or rename a file or directory on disk. Optionally overwrite an existing target.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"src":       {Type: "string", Description: "Existing source path to move.", Required: true},
			"dst":       {Type: "string", Description: "Destination path after the move.", Required: true},
			"overwrite": {Type: "boolean", Description: "Replace an existing destination if present. Default false.", Required: false, Default: false},
		},
		Handler: handleFileMove,
	}
}

func handleFileMove(args map[string]any) (string, error) {
	src, ok := args["src"].(string)
	if !ok {
		return "", fmt.Errorf("src is required")
	}
	dst, ok := args["dst"].(string)
	if !ok {
		return "", fmt.Errorf("dst is required")
	}
	if err := validatePath(src); err != nil {
		return "", err
	}
	if err := validatePath(dst); err != nil {
		return "", err
	}
	overwrite := false
	if v, ok := args["overwrite"].(bool); ok {
		overwrite = v
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("source path does not exist: %s", src)
		}
		return "", fmt.Errorf("stat source: %w", err)
	}
	if sameFilePath(src, dst) {
		return "", fmt.Errorf("source and destination are the same path: %s", src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("create destination parent: %w", err)
	}
	if _, err := os.Stat(dst); err == nil {
		if !overwrite {
			return "", fmt.Errorf("destination already exists: %s", dst)
		}
		if err := removePath(dst, true); err != nil {
			return "", fmt.Errorf("remove destination: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat destination: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("move path: %w", err)
	}
	kind := "file"
	if srcInfo.IsDir() {
		kind = "directory"
	}
	return fmt.Sprintf("Moved %s from %s to %s", kind, src, dst), nil
}

func FileDeleteTool() *Tool {
	return &Tool{
		Name:        "file_delete",
		Description: "Delete a file or directory. Set recursive=true to remove a non-empty directory. Set missing_ok=true to ignore absent paths.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"path":       {Type: "string", Description: "File or directory path to delete.", Required: true},
			"recursive":  {Type: "boolean", Description: "Remove a directory tree instead of only a single file or empty directory. Default false.", Required: false, Default: false},
			"missing_ok": {Type: "boolean", Description: "Succeed when the target path does not exist. Default false.", Required: false, Default: false},
		},
		Handler: handleFileDelete,
	}
}

func handleFileDelete(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	recursive := false
	if v, ok := args["recursive"].(bool); ok {
		recursive = v
	}
	missingOK := false
	if v, ok := args["missing_ok"].(bool); ok {
		missingOK = v
	}
	if err := removePath(path, recursive); err != nil {
		if errors.Is(err, os.ErrNotExist) && missingOK {
			return fmt.Sprintf("Path already absent: %s", path), nil
		}
		return "", err
	}
	return fmt.Sprintf("Deleted %s", path), nil
}

func removePath(path string, recursive bool) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return fmt.Errorf("stat path: %w", err)
	}
	if info.IsDir() {
		if recursive {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("delete directory tree: %w", err)
			}
			return nil
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("delete directory: %w", err)
		}
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

func sameFilePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

func FilePatchTool() *Tool {
	return &Tool{
		Name:        "file_patch",
		Description: "Apply an in-place edit to an existing file. Supports exact text replacement for simple changes and line-oriented diff hunks for more complex edits.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"path":        {Type: "string", Description: "Path to the file that should be patched.", Required: true},
			"match":       {Type: "string", Description: "Exact text to find in the file before applying the patch.", Required: false},
			"replace":     {Type: "string", Description: "Replacement text for the matched block.", Required: false},
			"diff":        {Type: "string", Description: "Optional line-oriented diff hunk. Use unified-diff style lines starting with space, +, -, and optional @@ headers. When provided, diff mode is used instead of match/replace mode.", Required: false},
			"occurrence":  {Type: "number", Description: "1-based occurrence to replace when the same text appears multiple times. Default 1.", Required: false, Default: 1},
			"replace_all": {Type: "boolean", Description: "Replace every exact occurrence instead of a single targeted one.", Required: false, Default: false},
		},
		Handler: handleFilePatch,
	}
}

func handleFilePatch(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	match, _ := args["match"].(string)
	replace, replaceProvided := args["replace"].(string)
	diffText, _ := args["diff"].(string)
	diffText = strings.ReplaceAll(diffText, "\r\n", "\n")

	replaceAll := false
	if v, ok := args["replace_all"].(bool); ok {
		replaceAll = v
	}
	occurrence := 1
	if v, ok := args["occurrence"]; ok {
		switch n := v.(type) {
		case float64:
			occurrence = int(n)
		case int:
			occurrence = n
		}
	}
	if occurrence <= 0 {
		occurrence = 1
	}
	if err := validatePath(path); err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	content := string(data)
	if strings.TrimSpace(diffText) != "" {
		patched, hunkCount, err := applyLinePatch(content, diffText)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
			return "", fmt.Errorf("write file: %w", err)
		}
		return fmt.Sprintf("Patched %s (%d hunk%s)", path, hunkCount, pluralSuffix(hunkCount)), nil
	}
	if !replaceProvided {
		return "", fmt.Errorf("replace is required")
	}
	if strings.TrimSpace(match) == "" {
		return "", fmt.Errorf("match must not be empty")
	}
	matchCount := strings.Count(content, match)
	if matchCount == 0 {
		return "", fmt.Errorf("match text not found in %s", path)
	}
	var patched string
	replacedCount := 0
	if replaceAll {
		patched = strings.ReplaceAll(content, match, replace)
		replacedCount = matchCount
	} else {
		if occurrence > matchCount {
			return "", fmt.Errorf("occurrence %d exceeds %d matches in %s", occurrence, matchCount, path)
		}
		patched, replacedCount = replaceStringOccurrence(content, match, replace, occurrence)
	}
	if replacedCount == 0 {
		return "", fmt.Errorf("no patch applied to %s", path)
	}
	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return fmt.Sprintf("Patched %s (%d replacement%s)", path, replacedCount, pluralSuffix(replacedCount)), nil
}

type linePatchHunk struct {
	before []string
	after  []string
}

func applyLinePatch(content, diffText string) (string, int, error) {
	lines, hadTrailingNewline := splitPatchTargetLines(content)
	hunks, err := parseLinePatchHunks(diffText)
	if err != nil {
		return "", 0, err
	}
	searchFrom := 0
	for i, hunk := range hunks {
		start := findLineSequence(lines, hunk.before, searchFrom)
		if start < 0 && searchFrom > 0 {
			start = findLineSequence(lines, hunk.before, 0)
		}
		if start < 0 {
			return "", 0, fmt.Errorf("diff hunk %d did not match target file", i+1)
		}
		end := start + len(hunk.before)
		updated := make([]string, 0, len(lines)-len(hunk.before)+len(hunk.after))
		updated = append(updated, lines[:start]...)
		updated = append(updated, hunk.after...)
		updated = append(updated, lines[end:]...)
		lines = updated
		searchFrom = start + len(hunk.after)
	}
	return joinPatchTargetLines(lines, hadTrailingNewline), len(hunks), nil
}

func parseLinePatchHunks(diffText string) ([]linePatchHunk, error) {
	rawLines := strings.Split(strings.ReplaceAll(diffText, "\r\n", "\n"), "\n")
	if len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}
	var hunks []linePatchHunk
	current := linePatchHunk{}
	inBody := false
	flushCurrent := func() error {
		if !inBody {
			return nil
		}
		if len(current.before) == 0 && len(current.after) == 0 {
			return fmt.Errorf("diff hunk must contain at least one change or context line")
		}
		hunks = append(hunks, current)
		current = linePatchHunk{}
		inBody = false
		return nil
	}
	for idx, line := range rawLines {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		if strings.HasPrefix(line, "@@") {
			if err := flushCurrent(); err != nil {
				return nil, err
			}
			continue
		}
		if line == `\ No newline at end of file` {
			continue
		}
		if line == "" {
			return nil, fmt.Errorf("diff line %d must start with space, '+', '-', or '@@'", idx+1)
		}
		prefix := line[0]
		payload := line[1:]
		switch prefix {
		case ' ':
			current.before = append(current.before, payload)
			current.after = append(current.after, payload)
			inBody = true
		case '-':
			current.before = append(current.before, payload)
			inBody = true
		case '+':
			current.after = append(current.after, payload)
			inBody = true
		default:
			return nil, fmt.Errorf("diff line %d must start with space, '+', '-', or '@@'", idx+1)
		}
	}
	if err := flushCurrent(); err != nil {
		return nil, err
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("diff is empty")
	}
	return hunks, nil
}

func splitPatchTargetLines(content string) ([]string, bool) {
	hadTrailingNewline := strings.HasSuffix(content, "\n")
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if hadTrailingNewline && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return lines, hadTrailingNewline
}

func joinPatchTargetLines(lines []string, hadTrailingNewline bool) string {
	joined := strings.Join(lines, "\n")
	if hadTrailingNewline {
		return joined + "\n"
	}
	return joined
}

func findLineSequence(lines, target []string, start int) int {
	if len(target) == 0 {
		if start < 0 {
			return 0
		}
		if start > len(lines) {
			return len(lines)
		}
		return start
	}
	if start < 0 {
		start = 0
	}
	for i := start; i+len(target) <= len(lines); i++ {
		match := true
		for j := range target {
			if lines[i+j] != target[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func replaceStringOccurrence(content, match, replace string, occurrence int) (string, int) {
	searchFrom := 0
	found := 0
	for {
		idx := strings.Index(content[searchFrom:], match)
		if idx < 0 {
			return content, 0
		}
		idx += searchFrom
		found++
		if found == occurrence {
			var b strings.Builder
			b.WriteString(content[:idx])
			b.WriteString(replace)
			b.WriteString(content[idx+len(match):])
			return b.String(), 1
		}
		searchFrom = idx + len(match)
	}
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func FileListTool() *Tool {
	return &Tool{
		Name:        "file_list",
		Description: "List files or directories when you need repository structure, candidate files, or navigation context before reading or editing specific paths.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":      {Type: "string", Description: "Directory path to inspect.", Required: true},
			"recursive": {Type: "boolean", Description: "Whether to include nested files and subdirectories.", Required: false, Default: false},
		},
		Handler: handleFileList,
	}
}

func handleFileList(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	recursive := false
	if r, ok := args["recursive"]; ok {
		recursive, _ = r.(bool)
	}
	maxEntries := 200
	if v, ok := args["max_entries"]; ok {
		switch n := v.(type) {
		case float64:
			maxEntries = int(n)
		case int:
			maxEntries = n
		}
	}
	if maxEntries <= 0 {
		maxEntries = 200
	}
	if err := validatePath(path); err != nil {
		return "", err
	}

	var b strings.Builder
	entryCount := 0
	truncated := false
	if recursive {
		stopWalk := errors.New("file list truncated")
		err := filepath.Walk(path, func(walkPath string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if entryCount >= maxEntries {
				truncated = true
				return stopWalk
			}
			rel, _ := filepath.Rel(path, walkPath)
			if info.IsDir() {
				b.WriteString(fmt.Sprintf("  📁 %s/\n", rel))
			} else {
				b.WriteString(fmt.Sprintf("  📄 %s (%d bytes)\n", rel, info.Size()))
			}
			entryCount++
			return nil
		})
		if err != nil && !errors.Is(err, stopWalk) {
			return "", fmt.Errorf("walk directory: %w", err)
		}
	} else {
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", fmt.Errorf("read directory: %w", err)
		}
		for _, entry := range entries {
			if entryCount >= maxEntries {
				truncated = true
				break
			}
			if entry.IsDir() {
				b.WriteString(fmt.Sprintf("  📁 %s/\n", entry.Name()))
			} else {
				info, _ := entry.Info()
				b.WriteString(fmt.Sprintf("  📄 %s (%d bytes)\n", entry.Name(), info.Size()))
			}
			entryCount++
		}
	}
	if truncated {
		b.WriteString(fmt.Sprintf("  ... truncated after %d entries\n", maxEntries))
	}
	return b.String(), nil
}

func validatePath(path string) error {
	clean := filepath.Clean(path)
	if strings.Contains(clean, "..") {
		return fmt.Errorf("path traversal detected: %s", path)
	}
	return validateSandbox(clean)
}

func validateSandbox(cleanPath string) error {
	absPath := cleanPath
	if !filepath.IsAbs(absPath) {
		if wd, err := os.Getwd(); err == nil {
			absPath = filepath.Join(wd, absPath)
		}
	}
	absPath = filepath.Clean(absPath)

	home, err := os.UserHomeDir()
	if err != nil {
		home = "/root"
	}
	allowedPrefixes := []string{filepath.Join(home, ".luckyharness"), "/tmp", "/dev/null"}
	if filepath.Base(home) == ".lh-home" {
		allowedPrefixes = append(allowedPrefixes, home)
	}
	deniedPrefixes := []string{
		filepath.Join(home, ".nanobot"),
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".gnupg"),
		filepath.Join(home, ".aws"),
		filepath.Join(home, ".config/gcloud"),
		"/etc/shadow",
		"/etc/ssh",
	}
	for _, denied := range deniedPrefixes {
		if strings.HasPrefix(absPath, denied) || absPath == denied {
			return fmt.Errorf("access denied: path is outside sandbox (%s)", cleanPath)
		}
	}
	for _, allowed := range allowedPrefixes {
		if strings.HasPrefix(absPath, allowed) || absPath == allowed {
			return nil
		}
	}
	return fmt.Errorf("access denied: path is outside sandbox (allowed: ~/.luckyharness/, /tmp/). Requested: %s", cleanPath)
}

func validateShellSandbox(command string) error {
	deniedPatterns := []string{".nanobot", ".ssh/", ".gnupg/", ".aws/", "/etc/shadow", "/etc/ssh/", "config.json"}
	lowerCmd := strings.ToLower(command)
	for _, pattern := range deniedPatterns {
		if strings.Contains(lowerCmd, strings.ToLower(pattern)) {
			return fmt.Errorf("access denied: command references restricted path (%s)", pattern)
		}
	}
	deniedEnvVars := []string{"FILEBROWSER_", "NANOBOT_", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"}
	for _, envVar := range deniedEnvVars {
		if strings.Contains(lowerCmd, strings.ToLower(envVar)) {
			return fmt.Errorf("access denied: command references restricted environment variable (%s)", envVar)
		}
	}
	return nil
}
