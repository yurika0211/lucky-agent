package lhcmd

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/yurika0211/luckyagent/internal/config"
)

// ensureServeRunning checks whether the LuckyAgent API service at apiBase is
// already up; if not, it spawns a detached `la serve` process bound to
// 0.0.0.0:<port> and waits for it to become healthy.
func ensureServeRunning(apiBase string) error {
	if isServeHealthy(apiBase) {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not resolve current executable to auto-start la serve: %w", err)
	}

	addr, err := addrFromAPIBase(apiBase)
	if err != nil {
		return err
	}

	mgr, err := config.NewManager()
	if err != nil {
		return fmt.Errorf("could not resolve LuckyAgent home directory to auto-start la serve: %w", err)
	}
	logDir := filepath.Join(mgr.HomeDir(), "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("could not create log directory %s: %w", logDir, err)
	}
	logPath := filepath.Join(logDir, "serve-autostart.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("could not open log file %s: %w", logPath, err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "serve", "--addr", addr)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detachProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start la serve: %w", err)
	}

	fmt.Printf("检测到 API 服务未运行，已自动启动 la serve (pid=%d)，等待就绪...\n", cmd.Process.Pid)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if isServeHealthy(apiBase) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("la serve did not become healthy within 20s; check %s", logPath)
}

func isServeHealthy(apiBase string) bool {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(apiBase + "/api/v1/health/live")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func addrFromAPIBase(apiBase string) (string, error) {
	u, err := url.Parse(apiBase)
	if err != nil {
		return "", fmt.Errorf("invalid --api-base %q: %w", apiBase, err)
	}
	port := u.Port()
	if port == "" {
		port = "9090"
	}
	return "0.0.0.0:" + port, nil
}
