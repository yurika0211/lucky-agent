package servelock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireRejectsSecondHolder(t *testing.T) {
	home := t.TempDir()
	first, err := Acquire(home)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer Release(first)

	if _, err := Acquire(home); err == nil {
		t.Fatal("second acquire should fail while first holds the lock")
	}

	Release(first)
	second, err := Acquire(home)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	Release(second)
}

func TestAcquireCreatesRuntimeDir(t *testing.T) {
	home := t.TempDir()
	lock, err := Acquire(home)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer Release(lock)
	if _, err := os.Stat(filepath.Join(home, "runtime", "serve.lock")); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
}
