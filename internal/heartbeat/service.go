package heartbeat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyharness/internal/provider"
)

var heartbeatDecisionTool = []map[string]any{
	{
		"type": "function",
		"function": map[string]any{
			"name":        "heartbeat",
			"description": "Report the heartbeat decision after reviewing tasks.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"skip", "run"},
						"description": "skip = nothing to do, run = active tasks exist",
					},
					"tasks": map[string]any{
						"type":        "string",
						"description": "Natural-language summary of active tasks when action=run",
					},
				},
				"required": []string{"action"},
			},
		},
	},
}

type ExecuteFunc func(ctx context.Context, tasks string) (string, error)
type NotifyFunc func(ctx context.Context, response string) error

type Config struct {
	Workspace string
	Provider  provider.Provider
	Model     string
	OnExecute ExecuteFunc
	OnNotify  NotifyFunc
	Interval  time.Duration
	Enabled   bool
}

type Service struct {
	cfg Config

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

func New(cfg Config) *Service {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Minute
	}
	return &Service{cfg: cfg}
}

func (s *Service) HeartbeatFile() string {
	return filepath.Join(s.cfg.Workspace, "HEARTBEAT.md")
}

func (s *Service) EnsureWorkspace() error {
	if strings.TrimSpace(s.cfg.Workspace) == "" {
		return fmt.Errorf("heartbeat workspace is empty")
	}
	if err := os.MkdirAll(s.cfg.Workspace, 0o700); err != nil {
		return fmt.Errorf("create heartbeat workspace: %w", err)
	}
	path := s.HeartbeatFile()
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat heartbeat file: %w", err)
	}
	template := "# HEARTBEAT\n\n在这里写周期性任务。留空则不会触发。\n"
	if err := os.WriteFile(path, []byte(template), 0o600); err != nil {
		return fmt.Errorf("write heartbeat template: %w", err)
	}
	return nil
}

func (s *Service) Start() error {
	if !s.cfg.Enabled {
		return nil
	}
	if err := s.EnsureWorkspace(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("heartbeat already running")
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	go s.loop()
	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.mu.Unlock()

	close(stopCh)
	<-doneCh

	s.mu.Lock()
	s.running = false
	s.stopCh = nil
	s.doneCh = nil
	s.mu.Unlock()
	return nil
}

func (s *Service) TriggerNow(ctx context.Context) (string, error) {
	return s.tick(ctx)
}

func (s *Service) loop() {
	s.mu.Lock()
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.mu.Unlock()

	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	defer close(doneCh)

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			_, _ = s.tick(context.Background())
		}
	}
}

func (s *Service) tick(ctx context.Context) (string, error) {
	content, ok, err := s.readHeartbeatFile()
	if err != nil || !ok {
		return "", err
	}

	action, tasks, err := s.decide(ctx, content)
	if err != nil {
		return "", err
	}
	if action != "run" || strings.TrimSpace(tasks) == "" || s.cfg.OnExecute == nil {
		return "", nil
	}

	response, err := s.cfg.OnExecute(ctx, tasks)
	if err != nil {
		return "", err
	}
	response = strings.TrimSpace(response)
	if response != "" && s.cfg.OnNotify != nil {
		if notifyErr := s.cfg.OnNotify(ctx, response); notifyErr != nil {
			return response, notifyErr
		}
	}
	return response, nil
}

func (s *Service) readHeartbeatFile() (string, bool, error) {
	path := s.HeartbeatFile()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read heartbeat file: %w", err)
	}
	content := strings.TrimSpace(string(data))
	if content == "" || content == "# HEARTBEAT" {
		return "", false, nil
	}
	return content, true, nil
}

func (s *Service) decide(ctx context.Context, content string) (string, string, error) {
	nowText := time.Now().Format("2006-01-02 15:04:05 -0700 MST")
	messages := []provider.Message{
		{Role: "system", Content: "You are a heartbeat agent. Review HEARTBEAT.md and decide whether there are active tasks. Use the heartbeat tool when available."},
		{Role: "user", Content: fmt.Sprintf("Current Time: %s\n\nReview the following HEARTBEAT.md and decide whether there are active tasks.\n\n%s", nowText, content)},
	}

	if fcProvider, ok := s.cfg.Provider.(provider.FunctionCallingProvider); ok {
		resp, err := fcProvider.ChatWithOptions(ctx, messages, provider.CallOptions{
			Tools:        heartbeatDecisionTool,
			ToolChoice:   "auto",
			MaxToolCalls: 1,
		})
		if err != nil {
			return "", "", err
		}
		if len(resp.ToolCalls) == 0 {
			return "skip", "", nil
		}
		var args struct {
			Action string `json:"action"`
			Tasks  string `json:"tasks"`
		}
		if err := json.Unmarshal([]byte(resp.ToolCalls[0].Arguments), &args); err != nil {
			return "", "", fmt.Errorf("parse heartbeat tool args: %w", err)
		}
		if strings.TrimSpace(args.Action) == "" {
			args.Action = "skip"
		}
		return strings.TrimSpace(args.Action), strings.TrimSpace(args.Tasks), nil
	}

	resp, err := s.cfg.Provider.Chat(ctx, append(messages, provider.Message{
		Role:    "user",
		Content: "Reply with compact JSON only, for example {\"action\":\"skip\"} or {\"action\":\"run\",\"tasks\":\"...\"}.",
	}))
	if err != nil {
		return "", "", err
	}
	var args struct {
		Action string `json:"action"`
		Tasks  string `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &args); err != nil {
		return "skip", "", nil
	}
	if strings.TrimSpace(args.Action) == "" {
		args.Action = "skip"
	}
	return strings.TrimSpace(args.Action), strings.TrimSpace(args.Tasks), nil
}
