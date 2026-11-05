package servelock

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const lockName = "serve.lock"

// Path is the flock file under a LuckyAgent home.
func Path(homeDir string) string {
	return filepath.Join(homeDir, "runtime", lockName)
}

// Acquire exclusive-locks runtime/serve.lock. A second serve against the same
// home fails immediately instead of paying bootstrap cost and racing the port.
func Acquire(homeDir string) (*os.File, error) {
	path := Path(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create serve lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open serve lock: %w", err)
	}
	if err := lockExclusive(f); err != nil {
		holder := readHolder(f)
		_ = f.Close()
		if holder != "" {
			return nil, fmt.Errorf("another LuckyAgent serve already holds %s (pid %s)", path, holder)
		}
		return nil, fmt.Errorf("another LuckyAgent serve already holds %s", path)
	}
	if err := f.Truncate(0); err != nil {
		unlock(f)
		_ = f.Close()
		return nil, fmt.Errorf("truncate serve lock: %w", err)
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		unlock(f)
		_ = f.Close()
		return nil, fmt.Errorf("write serve lock: %w", err)
	}
	return f, nil
}

// Release drops the exclusive lock. It is idempotent.
func Release(f *os.File) {
	if f == nil {
		return
	}
	unlock(f)
	_ = f.Close()
}

func readHolder(f *os.File) string {
	if f == nil {
		return ""
	}
	if _, err := f.Seek(0, 0); err != nil {
		return ""
	}
	buf := make([]byte, 64)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return ""
	}
	return strings.TrimSpace(string(buf[:n]))
}
