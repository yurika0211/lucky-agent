package openclawweixin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/gateway/weixin"
)

type Config struct {
	AccountID               string
	StateDir                string
	DMPolicy                string
	GroupPolicy             string
	AllowedUsers            []string
	GroupAllowedUsers       []string
	SplitMultilineMessages  bool
	PollTimeoutMilliseconds int
	SendChunkDelayMS        int
}

type Adapter struct {
	cfg     Config
	inner   *weixin.Adapter
	handler gateway.MessageHandler
}

type accountData struct {
	Token   string `json:"token"`
	BaseURL string `json:"baseUrl"`
}

func NewAdapter(cfg Config) *Adapter {
	return &Adapter{cfg: cfg}
}

func (a *Adapter) Name() string { return "openclawweixin" }

func (a *Adapter) SetHandler(handler gateway.MessageHandler) {
	a.handler = handler
	if a.inner != nil {
		a.inner.SetHandler(handler)
	}
}

func (a *Adapter) Start(ctx context.Context) error {
	accountID := strings.TrimSpace(a.cfg.AccountID)
	if accountID == "" {
		return fmt.Errorf("openclawweixin: account_id is required")
	}

	stateDir := resolveStateDir(strings.TrimSpace(a.cfg.StateDir))
	data, err := loadAccount(stateDir, accountID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(data.Token) == "" {
		return fmt.Errorf("openclawweixin: token missing for account %s", accountID)
	}

	inner := weixin.NewAdapter(weixin.Config{
		Token:                   strings.TrimSpace(data.Token),
		AccountID:               accountID,
		BaseURL:                 strings.TrimSpace(data.BaseURL),
		DMPolicy:                a.cfg.DMPolicy,
		GroupPolicy:             a.cfg.GroupPolicy,
		AllowedUsers:            append([]string(nil), a.cfg.AllowedUsers...),
		GroupAllowedUsers:       append([]string(nil), a.cfg.GroupAllowedUsers...),
		SplitMultilineMessages:  a.cfg.SplitMultilineMessages,
		PollTimeoutMilliseconds: a.cfg.PollTimeoutMilliseconds,
		SendChunkDelayMS:        a.cfg.SendChunkDelayMS,
	})
	if a.handler != nil {
		inner.SetHandler(a.handler)
	}
	a.inner = inner
	return a.inner.Start(ctx)
}

func (a *Adapter) Stop() error {
	if a.inner == nil {
		return nil
	}
	return a.inner.Stop()
}

func (a *Adapter) Send(ctx context.Context, chatID string, message string) error {
	if a.inner == nil {
		return fmt.Errorf("openclawweixin: adapter not started")
	}
	return a.inner.Send(ctx, chatID, message)
}

func (a *Adapter) SendWithReply(ctx context.Context, chatID string, replyToMsgID string, message string) error {
	if a.inner == nil {
		return fmt.Errorf("openclawweixin: adapter not started")
	}
	return a.inner.SendWithReply(ctx, chatID, replyToMsgID, message)
}

func (a *Adapter) IsRunning() bool {
	return a.inner != nil && a.inner.IsRunning()
}

func resolveStateDir(v string) string {
	if v != "" {
		return v
	}
	if env := strings.TrimSpace(os.Getenv("OPENCLAW_STATE_DIR")); env != "" {
		return env
	}
	if env := strings.TrimSpace(os.Getenv("CLAWDBOT_STATE_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".openclaw"
	}
	return filepath.Join(home, ".openclaw")
}

func loadAccount(stateDir, accountID string) (*accountData, error) {
	path := filepath.Join(stateDir, "openclaw-weixin", "accounts", accountID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("openclawweixin: read account file %s: %w", path, err)
	}
	var out accountData
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("openclawweixin: decode account file %s: %w", path, err)
	}
	return &out, nil
}
