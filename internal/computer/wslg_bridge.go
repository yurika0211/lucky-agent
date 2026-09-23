package computer

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed wslg_helper.ps1
var wslgHelper string

// wslgBridge invokes a short-lived Windows PowerShell process for each
// operation. WSL interop does not reliably connect a Linux pipe to the
// Windows console stdin, so requests are passed through a temporary file.
type wslgBridge struct {
	powerShell  string
	scriptPath  string
	scriptLinux string
	gate        chan struct{}
	cmd         *exec.Cmd
	log         boundedBridgeLog
	mu          sync.Mutex
	closed      bool
}

func newWSLGBridge(powerShell string) *wslgBridge {
	return &wslgBridge{
		powerShell: powerShell,
		gate:       make(chan struct{}, 1),
	}
}

func wslgWindowsTempDirs() []string {
	var dirs []string
	if entries, err := os.ReadDir("/mnt/c/Users"); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dirs = append(dirs, filepath.Join("/mnt/c/Users", entry.Name(), "AppData", "Local", "Temp"))
		}
	}
	dirs = append(dirs, "/mnt/c/Windows/Temp")
	dirs = append(dirs, os.TempDir())
	return dirs
}

func wslgTempFile(prefix string, data []byte) (linuxPath, windowsPath string, err error) {
	suffix := ".tmp"
	if prefix == "luckyagent-wslg" {
		suffix = ".ps1"
	}
	for _, dir := range wslgWindowsTempDirs() {
		file, createErr := os.CreateTemp(dir, prefix+"-*"+suffix)
		if createErr != nil {
			continue
		}
		path := file.Name()
		if _, writeErr := file.Write(data); writeErr != nil {
			_ = file.Close()
			_ = os.Remove(path)
			continue
		}
		if closeErr := file.Close(); closeErr != nil {
			_ = os.Remove(path)
			continue
		}
		converted, convertErr := exec.Command("wslpath", "-w", path).Output()
		if convertErr != nil || strings.TrimSpace(string(converted)) == "" {
			_ = os.Remove(path)
			continue
		}
		return path, strings.TrimSpace(string(converted)), nil
	}
	return "", "", errors.New("computer: no Windows-mounted temporary directory is writable")
}

func (b *wslgBridge) ensureScriptPath() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.scriptPath != "" {
		return b.scriptPath, nil
	}
	linuxPath, windowsPath, err := wslgTempFile("luckyagent-wslg", []byte(wslgHelper))
	if err != nil {
		return "", fmt.Errorf("computer: create WSLg helper script: %w", err)
	}
	b.scriptLinux = linuxPath
	b.scriptPath = windowsPath
	return windowsPath, nil
}

func (b *wslgBridge) call(ctx context.Context, request any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	select {
	case b.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-b.gate }()

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errors.New("computer: WSLg helper is closed")
	}
	b.mu.Unlock()

	requestData, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(requestData) > 1<<20 {
		return errors.New("computer: WSLg helper request is too large")
	}
	scriptPath, err := b.ensureScriptPath()
	if err != nil {
		return err
	}
	requestLinux, requestPath, err := wslgTempFile("luckyagent-wslg-request", requestData)
	if err != nil {
		return err
	}
	defer os.Remove(requestLinux)

	cmd := exec.Command(b.powerShell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath, "-RequestPath", requestPath)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &b.log
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errors.New("computer: WSLg helper is closed")
	}
	b.cmd = cmd
	b.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	var runErr error
	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		b.mu.Lock()
		b.cmd = nil
		b.mu.Unlock()
		return ctx.Err()
	case runErr = <-done:
	}
	b.mu.Lock()
	b.cmd = nil
	b.mu.Unlock()
	if runErr != nil {
		return fmt.Errorf("computer: WSLg PowerShell helper: %w (%s)", runErr, b.log.String())
	}

	var response struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &response); err != nil {
		return fmt.Errorf("computer: decode WSLg helper response: %w (stdout=%q stderr=%q)", err, truncateBridgeOutput(stdout.String()), b.log.String())
	}
	if response.Error != "" {
		return fmt.Errorf("computer: WSLg helper: %s", response.Error)
	}
	if result != nil {
		return json.Unmarshal(response.Result, result)
	}
	return nil
}

func truncateBridgeOutput(value string) string {
	if len(value) <= 4096 {
		return value
	}
	return value[:4096] + "..."
}

func (b *wslgBridge) Close() error {
	b.gate <- struct{}{}
	defer func() { <-b.gate }()
	b.mu.Lock()
	b.closed = true
	scriptLinux := b.scriptLinux
	b.scriptLinux, b.scriptPath = "", ""
	b.mu.Unlock()
	if scriptLinux != "" {
		_ = os.Remove(scriptLinux)
	}
	return nil
}
