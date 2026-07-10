package tool

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	defaultFileReadLimit = 2000
	maxFileReadLimit     = 5000
	binarySniffBytes     = 8192
	maxFileWriteBytes    = 25 * 1024 * 1024
	defaultDeleteMax     = 1000
)

type filesystemOperation string

const (
	filesystemRead filesystemOperation = "read"
)

// FilesystemPolicy extends the built-in filesystem sandbox with explicit,
// read-only roots. Write, patch, move, mkdir, and delete operations continue to
// use the built-in sandbox only.
type FilesystemPolicy struct {
	AllowedReadRoots []string
}

func DefaultFilesystemPolicy() FilesystemPolicy {
	return FilesystemPolicy{}
}

func filesystemPolicyFromOptional(policies []FilesystemPolicy) FilesystemPolicy {
	if len(policies) == 0 {
		return DefaultFilesystemPolicy()
	}
	return policies[0]
}

func TerminalTool() *Tool {
	return &Tool{
		Name:         "terminal",
		Description:  "Run a terminal command when you need to inspect runtime state, execute project commands, check the environment, or perform real system actions that cannot be answered from files alone.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermApprove,
		ShellAware:   true,
		ParallelSafe: false,
		Parameters: map[string]Param{
			"command": {Type: "string", Description: "Concrete terminal command to run. Prefer precise inspection or execution commands over exploratory one-liners.", Required: true},
			"timeout": {Type: "number", Description: "Timeout in seconds (default 30)", Required: false, Default: 30},
			"workdir": {Type: "string", Description: "Optional working directory. Use when the command must run in a specific project or subdirectory.", Required: false},
		},
		Handler: handleShell,
	}
}

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
		prefix = shellEnvPrefix(env, runtime.GOOS)
	}
	fullCommand := prefix + command

	ctx := time.Duration(timeout) * time.Second
	cmd, err := buildShellCommand(fullCommand)
	if err != nil {
		return "", err
	}
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

func shellEnvPrefix(env map[string]string, goos string) string {
	validEnvKey := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	var b strings.Builder
	for k, v := range env {
		if !validEnvKey.MatchString(k) {
			continue
		}
		if goos == "windows" {
			escaped := strings.ReplaceAll(v, "'", "''")
			fmt.Fprintf(&b, "$env:%s = '%s'; ", k, escaped)
			continue
		}
		escaped := strings.ReplaceAll(v, "'", "'\\''")
		fmt.Fprintf(&b, "export %s='%s'; ", k, escaped)
	}
	return b.String()
}

func FileReadTool(policies ...FilesystemPolicy) *Tool {
	policy := filesystemPolicyFromOptional(policies)
	return &Tool{
		Name:        "file_read",
		Description: "Read a local file when repository or document contents are the source of truth. Prefer this before guessing about code, config, notes, or generated artifacts.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		ShellAware:  true,
		Parameters: map[string]Param{
			"path":   {Type: "string", Description: "Path to the local file that should be inspected.", Required: true},
			"offset": {Type: "number", Description: "Line number to start reading from (1-indexed)", Required: false, Default: 1},
			"limit":  {Type: "number", Description: "Maximum number of lines to read", Required: false, Default: 2000},
		},
		Handler: func(args map[string]any) (string, error) {
			return handleFileReadWithPolicy(args, policy)
		},
	}
}

func handleFileRead(args map[string]any) (string, error) {
	return handleFileReadWithPolicy(args, DefaultFilesystemPolicy())
}

func handleFileReadWithPolicy(args map[string]any, policy FilesystemPolicy) (string, error) {
	path, err := resolveReadPathArg(args, "path", policy)
	if err != nil {
		return "", err
	}
	offset, limit := parseFileReadRange(args)
	return readTextFileRange(path, offset, limit)
}

func parseFileReadRange(args map[string]any) (offset, limit int) {
	offset = boundedIntArg(args, "offset", 1, 1, int(^uint(0)>>1))
	limit = boundedIntArg(args, "limit", defaultFileReadLimit, 1, maxFileReadLimit)
	return offset, limit
}

func readTextFileRange(path string, offset, limit int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("file_read expects a file, got directory: %s", path)
	}
	if binary, err := fileLooksBinary(file); err != nil {
		return "", err
	} else if binary {
		return "", fmt.Errorf("file appears to be binary; use document_read or a media-specific tool instead: %s", path)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek file: %w", err)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("File: %s\n", path))
	b.WriteString(fmt.Sprintf("Size: %d bytes\n", info.Size()))
	b.WriteString(fmt.Sprintf("Showing from line %d, max %d lines\n\n", offset, limit))

	reader := bufio.NewReader(file)
	lineNo := 0
	shown := 0
	truncated := false
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return "", fmt.Errorf("read file: %w", readErr)
		}
		if line == "" && errors.Is(readErr, io.EOF) {
			break
		}
		lineNo++
		if lineNo >= offset && shown < limit {
			b.WriteString(fmt.Sprintf("%d| %s\n", lineNo, trimLineEnding(line)))
			shown++
		} else if lineNo >= offset && shown >= limit {
			truncated = true
			break
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if shown == 0 {
		return "", fmt.Errorf("offset %d exceeds file length %d", offset, lineNo)
	}
	if truncated {
		b.WriteString(fmt.Sprintf("... truncated; use offset=%d to continue\n", offset+shown))
	}
	return b.String(), nil
}

func fileLooksBinary(file *os.File) (bool, error) {
	buf := make([]byte, binarySniffBytes)
	n, err := file.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read file header: %w", err)
	}
	buf = buf[:n]
	if len(buf) == 0 {
		return false, nil
	}
	if bytes.IndexByte(buf, 0) >= 0 {
		return true, nil
	}
	return !utf8.Valid(buf), nil
}

func trimLineEnding(line string) string {
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimSuffix(line, "\r")
}

func FileWriteTool() *Tool {
	return &Tool{
		Name:        "file_write",
		Description: "Write a complete local file. Use dry_run=true to preview, mode=create|overwrite|upsert to control existing files, and expected_sha256 to protect against concurrent changes.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		ShellAware:  true,
		Parameters: map[string]Param{
			"path":            {Type: "string", Description: "Target path of the file to create or overwrite.", Required: true},
			"content":         {Type: "string", Description: "Full file content to write. Use complete intended content, not a diff.", Required: true},
			"mode":            {Type: "string", Description: "Write mode: upsert creates or overwrites, create fails if the file exists, overwrite fails if it does not exist. Default upsert.", Required: false, Default: "upsert"},
			"dry_run":         {Type: "boolean", Description: "Preview the write plan without changing the filesystem. Default false.", Required: false, Default: false},
			"expected_sha256": {Type: "string", Description: "Optional SHA-256 hash the existing file must match before overwriting.", Required: false},
		},
		Handler: handleFileWrite,
	}
}

type fileWritePlan struct {
	Path           string `json:"path"`
	Mode           string `json:"mode"`
	Exists         bool   `json:"exists"`
	Action         string `json:"action"`
	OldSize        int64  `json:"old_size,omitempty"`
	NewSize        int    `json:"new_size"`
	OldSHA256      string `json:"old_sha256,omitempty"`
	NewSHA256      string `json:"new_sha256"`
	SameContent    bool   `json:"same_content"`
	AtomicReplace  bool   `json:"atomic_replace"`
	MaxAllowedSize int    `json:"max_allowed_size"`
}

func handleFileWrite(args map[string]any) (string, error) {
	path, pathErr := resolvePathArg(args, "path")
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}
	if pathErr != nil {
		return "", pathErr
	}
	contentBytes := []byte(content)
	if len(contentBytes) > maxFileWriteBytes {
		return "", fmt.Errorf("content is too large (%d bytes, max %d)", len(contentBytes), maxFileWriteBytes)
	}
	mode := strings.ToLower(strings.TrimSpace(stringArg(args["mode"])))
	if mode == "" {
		mode = "upsert"
	}
	dryRun := false
	if v, ok := args["dry_run"].(bool); ok {
		dryRun = v
	}
	expectedSHA := strings.ToLower(strings.TrimSpace(stringArg(args["expected_sha256"])))

	plan, existingPerm, err := buildFileWritePlan(path, contentBytes, mode, expectedSHA)
	if err != nil {
		return "", err
	}
	if dryRun {
		out, err := prettyStructuredValue(plan)
		if err != nil {
			return "", err
		}
		return "Dry run: file write plan\n" + out, nil
	}
	if plan.SameContent {
		return fmt.Sprintf("Skipped write to %s; content already matches sha256 %s", path, plan.NewSHA256), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}
	if err := writeFileAtomic(path, contentBytes, existingPerm); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return fmt.Sprintf("Written %d bytes to %s (sha256 %s)", len(contentBytes), path, plan.NewSHA256), nil
}

func buildFileWritePlan(path string, content []byte, mode, expectedSHA string) (fileWritePlan, os.FileMode, error) {
	plan := fileWritePlan{
		Path:           path,
		Mode:           mode,
		NewSize:        len(content),
		NewSHA256:      sha256Hex(content),
		AtomicReplace:  true,
		MaxAllowedSize: maxFileWriteBytes,
	}
	if mode != "upsert" && mode != "create" && mode != "overwrite" {
		return plan, 0, fmt.Errorf("mode must be one of create, overwrite, upsert")
	}

	info, err := os.Stat(path)
	existingPerm := os.FileMode(0o644)
	switch {
	case err == nil:
		if info.IsDir() {
			return plan, 0, fmt.Errorf("cannot write file because path is a directory: %s", path)
		}
		existingPerm = info.Mode().Perm()
		plan.Exists = true
		plan.OldSize = info.Size()
		data, err := os.ReadFile(path)
		if err != nil {
			return plan, 0, fmt.Errorf("read existing file: %w", err)
		}
		plan.OldSHA256 = sha256Hex(data)
		plan.SameContent = bytes.Equal(data, content)
	case errors.Is(err, os.ErrNotExist):
	default:
		return plan, 0, fmt.Errorf("stat file: %w", err)
	}

	if mode == "create" && plan.Exists {
		return plan, 0, fmt.Errorf("file already exists: %s", path)
	}
	if mode == "overwrite" && !plan.Exists {
		return plan, 0, fmt.Errorf("file does not exist for overwrite mode: %s", path)
	}
	if expectedSHA != "" {
		if !plan.Exists {
			return plan, 0, fmt.Errorf("expected_sha256 requires an existing file: %s", path)
		}
		if plan.OldSHA256 != expectedSHA {
			return plan, 0, fmt.Errorf("existing file sha256 mismatch: got %s, expected %s", plan.OldSHA256, expectedSHA)
		}
	}
	switch {
	case plan.SameContent:
		plan.Action = "skip"
	case plan.Exists:
		plan.Action = "overwrite"
	default:
		plan.Action = "create"
	}
	return plan, existingPerm, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func FileMkdirTool() *Tool {
	return &Tool{
		Name:        "file_mkdir",
		Description: "Create a directory on disk. Use dry_run=true to preview the directory creation plan before changing the filesystem.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		ShellAware:  true,
		Parameters: map[string]Param{
			"path":      {Type: "string", Description: "Directory path to create.", Required: true},
			"recursive": {Type: "boolean", Description: "Create parent directories when needed. Default true.", Required: false, Default: true},
			"dry_run":   {Type: "boolean", Description: "Preview what would be created without changing the filesystem. Default false.", Required: false, Default: false},
		},
		Handler: handleFileMkdir,
	}
}

type mkdirPlan struct {
	Path       string   `json:"path"`
	Recursive  bool     `json:"recursive"`
	Exists     bool     `json:"exists"`
	CreateDirs []string `json:"create_dirs"`
}

func handleFileMkdir(args map[string]any) (string, error) {
	path, err := resolvePathArg(args, "path")
	if err != nil {
		return "", err
	}
	recursive := true
	if v, ok := args["recursive"].(bool); ok {
		recursive = v
	}
	dryRun := false
	if v, ok := args["dry_run"].(bool); ok {
		dryRun = v
	}
	plan, err := buildMkdirPlan(path, recursive)
	if err != nil {
		return "", err
	}
	if dryRun {
		out, err := prettyStructuredValue(plan)
		if err != nil {
			return "", err
		}
		return "Dry run: directory creation plan\n" + out, nil
	}
	if plan.Exists {
		return fmt.Sprintf("Directory already exists: %s", path), nil
	}
	if recursive {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", fmt.Errorf("create directory: %w", err)
		}
	} else if err := os.Mkdir(path, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
				return fmt.Sprintf("Directory already exists: %s", path), nil
			}
		}
		return "", fmt.Errorf("create directory: %w", err)
	}
	return fmt.Sprintf("Created directory %s (%d new %s)", path, len(plan.CreateDirs), directoryWord(len(plan.CreateDirs))), nil
}

func buildMkdirPlan(path string, recursive bool) (mkdirPlan, error) {
	plan := mkdirPlan{Path: path, Recursive: recursive}
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		plan.Exists = true
		return plan, nil
	case err == nil:
		return plan, fmt.Errorf("path exists and is not a directory: %s", path)
	case !errors.Is(err, os.ErrNotExist):
		return plan, fmt.Errorf("stat path: %w", err)
	}

	if !recursive {
		parent := filepath.Dir(path)
		parentInfo, err := os.Stat(parent)
		switch {
		case err == nil && parentInfo.IsDir():
			plan.CreateDirs = []string{path}
			return plan, nil
		case err == nil:
			return plan, fmt.Errorf("parent path exists and is not a directory: %s", parent)
		case errors.Is(err, os.ErrNotExist):
			return plan, fmt.Errorf("parent directory does not exist: %s; set recursive=true to create parents", parent)
		default:
			return plan, fmt.Errorf("stat parent directory: %w", err)
		}
	}

	for cur := filepath.Clean(path); ; cur = filepath.Dir(cur) {
		info, err := os.Stat(cur)
		switch {
		case err == nil && info.IsDir():
			reverseStrings(plan.CreateDirs)
			return plan, nil
		case err == nil:
			return plan, fmt.Errorf("path component exists and is not a directory: %s", cur)
		case errors.Is(err, os.ErrNotExist):
			plan.CreateDirs = append(plan.CreateDirs, cur)
		default:
			return plan, fmt.Errorf("stat path component: %w", err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			reverseStrings(plan.CreateDirs)
			return plan, nil
		}
	}
}

func reverseStrings(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func FileMoveTool() *Tool {
	return &Tool{
		Name:        "file_move",
		Description: "Move or rename a file or directory on disk. Use dry_run=true to preview the move plan before changing files.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		ShellAware:  true,
		Parameters: map[string]Param{
			"src":       {Type: "string", Description: "Existing source path to move.", Required: true},
			"dst":       {Type: "string", Description: "Destination path after the move.", Required: true},
			"overwrite": {Type: "boolean", Description: "Replace an existing destination if present. Default false.", Required: false, Default: false},
			"dry_run":   {Type: "boolean", Description: "Preview the move plan without changing the filesystem. Default false.", Required: false, Default: false},
		},
		Handler: handleFileMove,
	}
}

type movePlan struct {
	Source              string `json:"source"`
	Destination         string `json:"destination"`
	Kind                string `json:"kind"`
	Overwrite           bool   `json:"overwrite"`
	DestinationExists   bool   `json:"destination_exists"`
	DestinationKind     string `json:"destination_kind,omitempty"`
	Action              string `json:"action"`
	CrossDeviceFallback string `json:"cross_device_fallback"`
}

func handleFileMove(args map[string]any) (string, error) {
	src, err := resolvePathArg(args, "src")
	if err != nil {
		return "", err
	}
	dst, err := resolvePathArg(args, "dst")
	if err != nil {
		return "", err
	}
	overwrite := false
	if v, ok := args["overwrite"].(bool); ok {
		overwrite = v
	}
	dryRun := false
	if v, ok := args["dry_run"].(bool); ok {
		dryRun = v
	}
	plan, err := buildMovePlan(src, dst, overwrite)
	if err != nil {
		return "", err
	}
	if dryRun {
		out, err := prettyStructuredValue(plan)
		if err != nil {
			return "", err
		}
		return "Dry run: file move plan\n" + out, nil
	}
	if err := executeMovePlan(plan); err != nil {
		return "", err
	}
	return fmt.Sprintf("Moved %s from %s to %s", plan.Kind, src, dst), nil
}

func buildMovePlan(src, dst string, overwrite bool) (movePlan, error) {
	plan := movePlan{Source: src, Destination: dst, Overwrite: overwrite, Action: "move", CrossDeviceFallback: "file-only"}
	srcInfo, err := os.Lstat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return plan, fmt.Errorf("source path does not exist: %s", src)
		}
		return plan, fmt.Errorf("stat source: %w", err)
	}
	plan.Kind = fileKind(srcInfo)
	if same, err := sameResolvedPath(src, dst); err != nil {
		return plan, err
	} else if same {
		return plan, fmt.Errorf("source and destination are the same path: %s", src)
	}
	if srcInfo.IsDir() {
		inside, err := destinationInsideSource(src, dst)
		if err != nil {
			return plan, err
		}
		if inside {
			return plan, fmt.Errorf("cannot move directory into itself: %s -> %s", src, dst)
		}
	}
	if dstInfo, err := os.Lstat(dst); err == nil {
		plan.DestinationExists = true
		plan.DestinationKind = fileKind(dstInfo)
		if !overwrite {
			return plan, fmt.Errorf("destination already exists: %s", dst)
		}
		if srcInfo.IsDir() != dstInfo.IsDir() {
			return plan, fmt.Errorf("cannot overwrite %s destination with %s source", plan.DestinationKind, plan.Kind)
		}
		plan.Action = "overwrite"
	} else if !errors.Is(err, os.ErrNotExist) {
		return plan, fmt.Errorf("stat destination: %w", err)
	}
	return plan, nil
}

func executeMovePlan(plan movePlan) error {
	if err := os.MkdirAll(filepath.Dir(plan.Destination), 0o755); err != nil {
		return fmt.Errorf("create destination parent: %w", err)
	}
	if plan.DestinationExists {
		if err := removePath(plan.Destination, true); err != nil {
			return fmt.Errorf("remove destination: %w", err)
		}
	}
	if err := os.Rename(plan.Source, plan.Destination); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			if plan.Kind == "directory" {
				return fmt.Errorf("move path across devices: directory fallback is not supported")
			}
			if copyErr := copyFileThenRemove(plan.Source, plan.Destination); copyErr != nil {
				return fmt.Errorf("move path across devices: %w", copyErr)
			}
			return nil
		}
		return fmt.Errorf("move path: %w", err)
	}
	return nil
}

func sameResolvedPath(a, b string) (bool, error) {
	ar, err := resolvedComparablePath(a)
	if err != nil {
		return false, err
	}
	br, err := resolvedComparablePath(b)
	if err != nil {
		return false, err
	}
	return ar == br, nil
}

func resolvedComparablePath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Abs(resolved)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve path %s: %w", path, err)
	}
	parent := filepath.Dir(path)
	resolvedParent, parentErr := filepath.EvalSymlinks(parent)
	if parentErr == nil {
		return filepath.Abs(filepath.Join(resolvedParent, filepath.Base(path)))
	}
	if !errors.Is(parentErr, os.ErrNotExist) {
		return "", fmt.Errorf("resolve parent path %s: %w", parent, parentErr)
	}
	return filepath.Abs(filepath.Clean(path))
}

func destinationInsideSource(src, dst string) (bool, error) {
	srcPath, err := resolvedComparablePath(src)
	if err != nil {
		return false, err
	}
	dstPath, err := resolvedComparablePath(dst)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(srcPath, dstPath)
	if err != nil {
		return false, err
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..", nil
}

func copyFileThenRemove(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	if err := writeFileAtomic(dst, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write destination: %w", err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove source: %w", err)
	}
	return nil
}

func FileDeleteTool() *Tool {
	return &Tool{
		Name:        "file_delete",
		Description: "Delete a file or directory. Use dry_run=true to preview recursive deletion, trash=true to quarantine instead of permanent deletion, and missing_ok=true to ignore absent paths.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		ShellAware:  true,
		Parameters: map[string]Param{
			"path":        {Type: "string", Description: "File or directory path to delete.", Required: true},
			"recursive":   {Type: "boolean", Description: "Remove a directory tree instead of only a single file or empty directory. Default false.", Required: false, Default: false},
			"missing_ok":  {Type: "boolean", Description: "Succeed when the target path does not exist. Default false.", Required: false, Default: false},
			"dry_run":     {Type: "boolean", Description: "Preview the delete plan without changing the filesystem. Default false.", Required: false, Default: false},
			"trash":       {Type: "boolean", Description: "Move the target to ~/.luckyagent/trash instead of deleting permanently. Default false.", Required: false, Default: false},
			"max_entries": {Type: "number", Description: "Maximum entries allowed for recursive permanent deletion. Default 1000.", Required: false, Default: defaultDeleteMax},
		},
		Handler: handleFileDelete,
	}
}

type deletePlan struct {
	Path       string `json:"path"`
	Exists     bool   `json:"exists"`
	Kind       string `json:"kind,omitempty"`
	Recursive  bool   `json:"recursive"`
	EntryCount int    `json:"entry_count,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
	Action     string `json:"action"`
	TrashPath  string `json:"trash_path,omitempty"`
}

func handleFileDelete(args map[string]any) (string, error) {
	path, err := resolvePathArg(args, "path")
	if err != nil {
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
	dryRun := false
	if v, ok := args["dry_run"].(bool); ok {
		dryRun = v
	}
	trash := false
	if v, ok := args["trash"].(bool); ok {
		trash = v
	}
	maxEntries := boundedIntArg(args, "max_entries", defaultDeleteMax, 1, int(^uint(0)>>1))

	plan, err := buildDeletePlan(path, recursive, trash, maxEntries)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && missingOK {
			return fmt.Sprintf("Path already absent: %s", path), nil
		}
		return "", err
	}
	if dryRun {
		out, err := prettyStructuredValue(plan)
		if err != nil {
			return "", err
		}
		return "Dry run: file delete plan\n" + out, nil
	}
	if err := executeDeletePlan(plan); err != nil {
		return "", err
	}
	if trash {
		return fmt.Sprintf("Moved %s to trash: %s", path, plan.TrashPath), nil
	}
	return fmt.Sprintf("Deleted %s (%s, %d entr%s)", path, plan.Kind, plan.EntryCount, entrySuffix(plan.EntryCount)), nil
}

func removePath(path string, recursive bool) error {
	plan, err := buildDeletePlan(path, recursive, false, int(^uint(0)>>1))
	if err != nil {
		return err
	}
	return executeDeletePlan(plan)
}

func buildDeletePlan(path string, recursive, trash bool, maxEntries int) (deletePlan, error) {
	plan := deletePlan{Path: path, Recursive: recursive, Exists: true}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			plan.Exists = false
			plan.Action = "absent"
			return plan, os.ErrNotExist
		}
		return plan, fmt.Errorf("stat path: %w", err)
	}
	plan.Kind = fileKind(info)
	plan.Action = "delete"
	if trash {
		trashPath, err := allocateTrashPath(path)
		if err != nil {
			return plan, err
		}
		plan.Action = "trash"
		plan.TrashPath = trashPath
	}
	if !info.IsDir() {
		plan.EntryCount = 1
		plan.Bytes = info.Size()
		return plan, nil
	}
	if !recursive {
		entries, err := os.ReadDir(path)
		if err != nil {
			return plan, fmt.Errorf("read directory: %w", err)
		}
		plan.EntryCount = len(entries) + 1
		if len(entries) > 0 {
			return plan, fmt.Errorf("directory is not empty: %s; set recursive=true to delete the tree", path)
		}
		return plan, nil
	}
	err = filepath.WalkDir(path, func(_ string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		plan.EntryCount++
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				plan.Bytes += info.Size()
			}
		}
		if !trash && plan.EntryCount > maxEntries {
			return fmt.Errorf("recursive delete exceeds max_entries=%d", maxEntries)
		}
		return nil
	})
	if err != nil {
		return plan, fmt.Errorf("scan directory tree: %w", err)
	}
	return plan, nil
}

func executeDeletePlan(plan deletePlan) error {
	if plan.Action == "trash" {
		if err := os.MkdirAll(filepath.Dir(plan.TrashPath), 0o755); err != nil {
			return fmt.Errorf("create trash directory: %w", err)
		}
		if err := os.Rename(plan.Path, plan.TrashPath); err != nil {
			return fmt.Errorf("move to trash: %w", err)
		}
		return nil
	}
	if plan.Kind == "directory" {
		if plan.Recursive {
			if err := os.RemoveAll(plan.Path); err != nil {
				return fmt.Errorf("delete directory tree: %w", err)
			}
			return nil
		}
		if err := os.Remove(plan.Path); err != nil {
			return fmt.Errorf("delete directory: %w", err)
		}
		return nil
	}
	if err := os.Remove(plan.Path); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

func fileKind(info os.FileInfo) string {
	if info.Mode()&os.ModeSymlink != 0 {
		return "symlink"
	}
	if info.IsDir() {
		return "directory"
	}
	return "file"
}

func allocateTrashPath(path string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
		name = "deleted"
	}
	return filepath.Join(home, ".luckyagent", "trash", fmt.Sprintf("%s-%d", name, time.Now().UnixNano())), nil
}

func entrySuffix(count int) string {
	if count == 1 {
		return "y"
	}
	return "ies"
}

func FilePatchTool() *Tool {
	return &Tool{
		Name:        "file_patch",
		Description: "Apply an in-place edit to an existing text file. Supports dry_run preview, expected_sha256 protection, exact replacement, and line-oriented diff hunks.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		ShellAware:  true,
		Parameters: map[string]Param{
			"path":            {Type: "string", Description: "Path to the file that should be patched.", Required: true},
			"match":           {Type: "string", Description: "Exact text to find in the file before applying the patch.", Required: false},
			"replace":         {Type: "string", Description: "Replacement text for the matched block.", Required: false},
			"diff":            {Type: "string", Description: "Optional line-oriented diff hunk. Use unified-diff style lines starting with space, +, -, and optional @@ headers. When provided, diff mode is used instead of match/replace mode.", Required: false},
			"occurrence":      {Type: "number", Description: "1-based occurrence to replace when the same text appears multiple times. Default 1.", Required: false, Default: 1},
			"replace_all":     {Type: "boolean", Description: "Replace every exact occurrence instead of a single targeted one.", Required: false, Default: false},
			"dry_run":         {Type: "boolean", Description: "Preview the patch plan without changing the filesystem. Default false.", Required: false, Default: false},
			"expected_sha256": {Type: "string", Description: "Optional SHA-256 hash the existing file must match before patching.", Required: false},
		},
		Handler: handleFilePatch,
	}
}

type filePatchPlan struct {
	Path          string `json:"path"`
	Kind          string `json:"kind"`
	ChangeCount   int    `json:"change_count"`
	OldSize       int    `json:"old_size"`
	NewSize       int    `json:"new_size"`
	OldSHA256     string `json:"old_sha256"`
	NewSHA256     string `json:"new_sha256"`
	AtomicReplace bool   `json:"atomic_replace"`
}

func handleFilePatch(args map[string]any) (string, error) {
	path, pathErr := resolvePathArg(args, "path")
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
	if pathErr != nil {
		return "", pathErr
	}
	dryRun := false
	if v, ok := args["dry_run"].(bool); ok {
		dryRun = v
	}
	expectedSHA := strings.ToLower(strings.TrimSpace(stringArg(args["expected_sha256"])))

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("file_patch expects a file, got directory: %s", path)
	}
	if info.Size() > maxFileWriteBytes {
		return "", fmt.Errorf("file is too large to patch (%d bytes, max %d)", info.Size(), maxFileWriteBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	if textLooksBinary(data) {
		return "", fmt.Errorf("file appears to be binary; refusing to patch: %s", path)
	}
	oldSHA := sha256Hex(data)
	if expectedSHA != "" && oldSHA != expectedSHA {
		return "", fmt.Errorf("existing file sha256 mismatch: got %s, expected %s", oldSHA, expectedSHA)
	}
	content := string(data)
	if strings.TrimSpace(diffText) != "" {
		patched, hunkCount, err := applyLinePatch(content, diffText)
		if err != nil {
			return "", err
		}
		return finishFilePatch(path, data, []byte(patched), info.Mode().Perm(), dryRun, "diff", hunkCount)
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
	return finishFilePatch(path, data, []byte(patched), info.Mode().Perm(), dryRun, "replacement", replacedCount)
}

func finishFilePatch(path string, before, after []byte, perm os.FileMode, dryRun bool, kind string, count int) (string, error) {
	if bytes.Equal(before, after) {
		return "", fmt.Errorf("patch produced no changes for %s", path)
	}
	plan := filePatchPlan{
		Path:          path,
		Kind:          kind,
		ChangeCount:   count,
		OldSize:       len(before),
		NewSize:       len(after),
		OldSHA256:     sha256Hex(before),
		NewSHA256:     sha256Hex(after),
		AtomicReplace: true,
	}
	if dryRun {
		out, err := prettyStructuredValue(plan)
		if err != nil {
			return "", err
		}
		return "Dry run: file patch plan\n" + out, nil
	}
	if err := writeFileAtomic(path, after, perm); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	if kind == "diff" {
		return fmt.Sprintf("Patched %s (%d hunk%s, sha256 %s)", path, count, pluralSuffix(count), plan.NewSHA256), nil
	}
	return fmt.Sprintf("Patched %s (%d replacement%s, sha256 %s)", path, count, pluralSuffix(count), plan.NewSHA256), nil
}

func textLooksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
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

func directoryWord(count int) string {
	if count == 1 {
		return "directory"
	}
	return "directories"
}

func FileListTool(policies ...FilesystemPolicy) *Tool {
	policy := filesystemPolicyFromOptional(policies)
	return &Tool{
		Name:        "file_list",
		Description: "List files or directories when you need repository structure, candidate files, or navigation context before reading or editing specific paths.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		ShellAware:  true,
		Parameters: map[string]Param{
			"path":      {Type: "string", Description: "Directory path to inspect.", Required: true},
			"recursive": {Type: "boolean", Description: "Whether to include nested files and subdirectories.", Required: false, Default: false},
		},
		Handler: func(args map[string]any) (string, error) {
			return handleFileListWithPolicy(args, policy)
		},
	}
}

func handleFileList(args map[string]any) (string, error) {
	return handleFileListWithPolicy(args, DefaultFilesystemPolicy())
}

func handleFileListWithPolicy(args map[string]any, policy FilesystemPolicy) (string, error) {
	path, pathErr := resolveReadPathArg(args, "path", policy)
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
	if pathErr != nil {
		return "", pathErr
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
	if containsInvalidPathChars(path) {
		return fmt.Errorf("invalid path: %s", path)
	}
	_, err := resolvePath(path, "")
	return err
}

func containsInvalidPathChars(path string) bool {
	for _, r := range path {
		if r < 32 || strings.ContainsRune("!@#$", r) {
			return true
		}
	}
	return false
}

func resolvePathArg(args map[string]any, key string) (string, error) {
	path, ok := args[key].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	cwd, _ := args["_cwd"].(string)
	return resolvePath(path, cwd)
}

func resolveReadPathArg(args map[string]any, key string, policy FilesystemPolicy) (string, error) {
	path, ok := args[key].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	cwd, _ := args["_cwd"].(string)
	return resolvePathForOperation(path, cwd, filesystemRead, policy)
}

func resolvePath(path, baseCwd string) (string, error) {
	return resolvePathForOperation(path, baseCwd, "", DefaultFilesystemPolicy())
}

func resolvePathForOperation(path, baseCwd string, op filesystemOperation, policy FilesystemPolicy) (string, error) {
	path = expandSandboxPath(strings.TrimSpace(path))
	clean := filepath.Clean(path)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("path traversal detected: %s", path)
	}
	if !filepath.IsAbs(clean) {
		baseCwd = expandSandboxPath(strings.TrimSpace(baseCwd))
		if baseCwd != "" {
			baseCwd = filepath.Clean(baseCwd)
			if err := validateSandboxForOperation(baseCwd, op, policy); err == nil {
				clean = filepath.Join(baseCwd, clean)
			}
		}
	}
	if !filepath.IsAbs(clean) {
		if wd, err := os.Getwd(); err == nil {
			clean = filepath.Join(wd, clean)
		}
	}
	clean = filepath.Clean(clean)
	if err := validateSandboxForOperation(clean, op, policy); err != nil {
		return "", err
	}
	return clean, nil
}

func expandSandboxPath(path string) string {
	if path == "~" {
		return sandboxHomeDir()
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(sandboxHomeDir(), path[2:])
	}
	if runtime.GOOS == "windows" {
		slashPath := filepath.ToSlash(path)
		if slashPath == "/tmp" {
			return os.TempDir()
		}
		if strings.HasPrefix(slashPath, "/tmp/") {
			return filepath.Join(os.TempDir(), strings.TrimPrefix(slashPath, "/tmp/"))
		}
		if slashPath == "/dev/null" {
			return os.DevNull
		}
	}
	return path
}

func validateSandbox(cleanPath string) error {
	return validateSandboxForOperation(cleanPath, "", DefaultFilesystemPolicy())
}

func validateSandboxForOperation(cleanPath string, op filesystemOperation, policy FilesystemPolicy) error {
	normalizedRequested := strings.ReplaceAll(cleanPath, `\`, `/`)
	if normalizedRequested == "/dev/null" || strings.HasPrefix(normalizedRequested, "/tmp/") {
		return nil
	}

	absPath := normalizeFilesystemCandidate(cleanPath)

	home := sandboxHomeDir()
	tempDir := normalizeSandboxPath(os.TempDir())
	allowedPrefixes := []string{
		normalizeSandboxPath(filepath.Join(home, ".luckyagent")),
		tempDir,
		normalizeSandboxPath(os.DevNull),
	}
	if filepath.Base(home) == ".lh-home" {
		allowedPrefixes = append(allowedPrefixes, normalizeSandboxPath(home))
	}
	deniedPrefixes := []string{
		normalizeSandboxPath(filepath.Join(home, ".nanobot")),
		normalizeSandboxPath(filepath.Join(home, ".ssh")),
		normalizeSandboxPath(filepath.Join(home, ".gnupg")),
		normalizeSandboxPath(filepath.Join(home, ".aws")),
		normalizeSandboxPath(filepath.Join(home, ".config", "gcloud")),
		normalizeSandboxPath(filepath.Join(home, "AppData", "Roaming", "gcloud")),
		normalizeSandboxPath("/etc/shadow"),
		normalizeSandboxPath("/etc/ssh"),
	}
	for _, denied := range deniedPrefixes {
		if pathMatchesPrefix(absPath, denied) {
			return fmt.Errorf("access denied: path is outside sandbox (%s)", cleanPath)
		}
	}
	for _, allowed := range allowedPrefixes {
		if pathMatchesPrefix(absPath, allowed) {
			return nil
		}
	}
	if op == filesystemRead {
		for _, root := range policy.AllowedReadRoots {
			allowed := normalizeFilesystemRoot(root)
			if allowed != "" && pathMatchesPrefix(absPath, allowed) {
				return nil
			}
		}
	}
	return fmt.Errorf("access denied: path is outside sandbox (allowed: ~/.luckyagent/, /tmp/). Requested: %s", cleanPath)
}

func normalizeFilesystemCandidate(path string) string {
	absPath := expandSandboxPath(strings.TrimSpace(path))
	if !filepath.IsAbs(absPath) {
		if wd, err := os.Getwd(); err == nil {
			absPath = filepath.Join(wd, absPath)
		}
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolved
	}
	return normalizeSandboxPath(absPath)
}

func normalizeFilesystemRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	return normalizeFilesystemCandidate(root)
}

func sandboxHomeDir() string {
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return home
	}
	if home := strings.TrimSpace(os.Getenv("USERPROFILE")); home != "" {
		return home
	}
	if drive := strings.TrimSpace(os.Getenv("HOMEDRIVE")); drive != "" {
		if path := strings.TrimSpace(os.Getenv("HOMEPATH")); path != "" {
			return filepath.Clean(drive + path)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "/root"
	}
	return home
}

func sandboxWorkspaceDir() string {
	return filepath.Join(sandboxHomeDir(), ".luckyagent", "workspace")
}

func resolveWorkspacePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("workspace path is empty")
	}

	workspace := filepath.Clean(sandboxWorkspaceDir())
	clean := filepath.Clean(expandSandboxPath(path))
	resolved := clean
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workspace, clean)
	}
	resolved = filepath.Clean(resolved)

	if err := validatePath(resolved); err != nil {
		return "", err
	}
	if !pathMatchesPrefix(normalizeSandboxPath(resolved), normalizeSandboxPath(workspace)) {
		return "", fmt.Errorf("access denied: path is outside workspace (allowed: ~/.luckyagent/workspace/). Requested: %s", path)
	}
	return resolved, nil
}

func normalizeSandboxPath(path string) string {
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	clean = filepath.ToSlash(clean)
	return strings.ToLower(clean)
}

func pathMatchesPrefix(path, prefix string) bool {
	if prefix == "" {
		return false
	}
	if path == prefix {
		return true
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return strings.HasPrefix(path, prefix)
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
