package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Mode controls how a task executes local tools.
type Mode string

const (
	ModeDev Mode = "dev"
	ModeIso Mode = "iso"
)

func (m Mode) Valid() bool { return m == ModeDev || m == ModeIso }

func (m Mode) String() string {
	if !m.Valid() {
		return string(ModeDev)
	}
	return string(m)
}

type persistedMode struct {
	Mode      Mode      `json:"mode"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Manager stores the global mode. The file is read on every Current call so a
// CLI process and a server process see changes made by the other process.
type Manager struct {
	mu   sync.Mutex
	path string
}

func NewManager(home string) (*Manager, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil, errors.New("sandbox home is required")
	}
	path := filepath.Join(home, "runtime", "security_mode.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create sandbox runtime directory: %w", err)
	}
	return &Manager{path: path}, nil
}

func (m *Manager) Path() string {
	if m == nil {
		return ""
	}
	return m.path
}

func (m *Manager) Current() Mode {
	if m == nil {
		return ModeDev
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	mode, err := m.loadLocked()
	if err != nil {
		return ModeDev
	}
	return mode
}

func (m *Manager) Set(mode Mode) error {
	if m == nil {
		return errors.New("sandbox manager is nil")
	}
	if !mode.Valid() {
		return fmt.Errorf("invalid sandbox mode %q; use dev or iso", mode)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return fmt.Errorf("create sandbox runtime directory: %w", err)
	}
	data, err := json.MarshalIndent(persistedMode{Mode: mode, UpdatedAt: time.Now()}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sandbox mode: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".security_mode-*.tmp")
	if err != nil {
		return fmt.Errorf("create sandbox mode temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure sandbox mode temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write sandbox mode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync sandbox mode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close sandbox mode: %w", err)
	}
	if err := os.Rename(tmpName, m.path); err != nil {
		return fmt.Errorf("persist sandbox mode: %w", err)
	}
	return nil
}

func (m *Manager) loadLocked() (Mode, error) {
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return ModeDev, nil
	}
	if err != nil {
		return ModeDev, err
	}
	var state persistedMode
	if err := json.Unmarshal(data, &state); err != nil {
		return ModeDev, err
	}
	if !state.Mode.Valid() {
		return ModeDev, fmt.Errorf("invalid persisted sandbox mode %q", state.Mode)
	}
	return state.Mode, nil
}

// Snapshot is captured when a task starts. Changing the global mode does not
// change permissions of a task that is already running.
type Snapshot struct {
	Mode         Mode
	OriginalRoot string
	Root         string

	cleanupOnce sync.Once
}

type snapshotContextKey struct{}

func WithSnapshot(ctx context.Context, snapshot *Snapshot) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, snapshotContextKey{}, snapshot)
}

func SnapshotFromContext(ctx context.Context) *Snapshot {
	if ctx == nil {
		return nil
	}
	snapshot, _ := ctx.Value(snapshotContextKey{}).(*Snapshot)
	return snapshot
}

func (s *Snapshot) Close() {
	if s == nil || s.Mode != ModeIso || s.Root == "" {
		return
	}
	s.cleanupOnce.Do(func() { _ = os.RemoveAll(s.Root) })
}

func (s *Snapshot) Isolated() bool { return s != nil && s.Mode == ModeIso }

// ValidateToolArgs protects tools that do not execute inside bubblewrap. The
// shell wrapper is not enough: file, network, GUI, and plugin tools need the
// same task boundary enforced at the gateway.
func (s *Snapshot) ValidateToolArgs(toolName string, args map[string]any) error {
	if s == nil || !s.Isolated() {
		return nil
	}
	name := strings.ToLower(strings.TrimSpace(toolName))
	for _, blocked := range []string{
		"web_search", "web_fetch", "http_request", "opencli",
		"computer_observe", "computer_act",
		"cron", "cron_add", "cron_remove", "cron_pause", "cron_resume",
		"autonomy", "autonomy_queue_add", "autonomy_queue_update", "autonomy_worker_spawn",
		"delegate_task", "delegate_cancel",
	} {
		if name == blocked {
			return fmt.Errorf("tool %s is disabled in iso sandbox", toolName)
		}
	}
	if strings.HasPrefix(name, "skill_") && strings.HasSuffix(name, "_run") {
		return fmt.Errorf("skill execution is disabled in iso sandbox")
	}
	if strings.HasPrefix(name, "codex.") {
		if cwd, ok := args["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
			candidate, err := filepath.Abs(strings.TrimSpace(cwd))
			if err != nil || !pathInside(s.Root, candidate) {
				return fmt.Errorf("codex cwd is outside iso workspace: %s", cwd)
			}
		}
	}
	pathKeys := []string{"path", "src", "source", "dst", "dest", "to", "target", "workdir", "download_dir", "output_path", "output_dir"}
	for _, key := range pathKeys {
		value, ok := args[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		candidate := strings.TrimSpace(value)
		if strings.HasPrefix(candidate, "~") {
			return fmt.Errorf("%s path is outside iso workspace: %s", key, value)
		}
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(s.Root, candidate)
		}
		if !pathInside(s.Root, candidate) {
			return fmt.Errorf("%s path is outside iso workspace: %s", key, value)
		}
	}
	if name == "image_generate" || name == "text_to_speech" {
		if strings.TrimSpace(stringArg(args, "output_path")) == "" && strings.TrimSpace(stringArg(args, "output_dir")) == "" {
			return fmt.Errorf("%s requires an output path inside iso workspace", toolName)
		}
	}
	return nil
}

func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, _ := args[key].(string)
	return value
}

// Prepare makes an isolated copy of root when mode is iso. The copy excludes
// Git metadata and obvious local secret files. The original tree is never
// mounted into the isolated process.
func Prepare(mode Mode, root string) (*Snapshot, error) {
	if !mode.Valid() {
		mode = ModeDev
	}
	root = strings.TrimSpace(root)
	if root == "" {
		root, _ = os.Getwd()
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox root: %w", err)
	}
	if mode == ModeDev {
		return &Snapshot{Mode: ModeDev, OriginalRoot: root, Root: root}, nil
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat sandbox source %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("sandbox source is not a directory: %s", root)
	}
	dest, err := os.MkdirTemp("", "luckyagent-iso-")
	if err != nil {
		return nil, fmt.Errorf("create isolated workspace: %w", err)
	}
	if err := copyTree(root, dest); err != nil {
		_ = os.RemoveAll(dest)
		return nil, fmt.Errorf("copy isolated workspace: %w", err)
	}
	return &Snapshot{Mode: ModeIso, OriginalRoot: root, Root: dest}, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".luckyagent") {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed in isolated workspace: %s", rel)
		}
		if strings.HasPrefix(entry.Name(), ".env") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			in.Close()
			return err
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o600
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := ioCopy(out, in)
		closeOutErr := out.Close()
		closeInErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
		return closeInErr
	})
}

// Kept as a small variable so the copy loop is easy to replace on platforms
// that need a different file copier.
var ioCopy = func(dst *os.File, src *os.File) (int64, error) {
	return copyFile(dst, src)
}

func copyFile(dst, src *os.File) (int64, error) {
	buf := make([]byte, 128*1024)
	var total int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

// Command builds the process that runs a shell command under the snapshot.
func (s *Snapshot) Command(command, workdir string, env map[string]string) (*exec.Cmd, error) {
	if s == nil || s.Mode == ModeDev {
		cmd := exec.Command("sh", "-c", command)
		if workdir != "" {
			cmd.Dir = workdir
		}
		return cmd, nil
	}
	if runtime.GOOS == "windows" {
		return nil, errors.New("iso sandbox requires bubblewrap on a Linux host")
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("iso sandbox requires bubblewrap: %w", err)
	}
	if workdir == "" {
		workdir = s.Root
	}
	if !pathInside(s.Root, workdir) {
		return nil, fmt.Errorf("isolated workdir is outside sandbox root: %s", workdir)
	}
	args := []string{
		"--die-with-parent", "--new-session", "--unshare-all", "--clearenv",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind-try", "/bin", "/bin",
		"--ro-bind-try", "/lib", "/lib",
		"--ro-bind-try", "/lib64", "/lib64",
		"--ro-bind-try", "/etc", "/etc",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--dir", "/tmp/home",
		"--bind", s.Root, "/workspace",
		"--chdir", filepath.Join("/workspace", mustRelative(s.Root, workdir)),
	}
	for key, value := range filteredEnvMap(env, true) {
		args = append(args, "--setenv", key, value)
	}
	args = append(args,
		"--setenv", "HOME", "/tmp/home",
		"--setenv", "TMPDIR", "/tmp",
		"--setenv", "PATH", "/usr/local/bin:/usr/bin:/bin",
		"--", "sh", "-c", command,
	)
	return exec.Command(bwrap, args...), nil
}

func filteredEnv(env map[string]string, isolated bool) []string {
	values := filteredEnvMap(env, isolated)
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}

func filteredEnvMap(env map[string]string, isolated bool) map[string]string {
	out := map[string]string{}
	for key, value := range env {
		if !validEnvKey(key) || (isolated && sensitiveEnvKey(key)) {
			continue
		}
		out[key] = value
	}
	return out
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func sensitiveEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, marker := range []string{"TOKEN", "API_KEY", "SECRET", "PASSWORD", "CREDENTIAL", "AUTH", "PRIVATE_KEY", "SSH"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func pathInside(root, path string) bool {
	root, _ = filepath.Abs(root)
	path, _ = filepath.Abs(path)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func mustRelative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}
