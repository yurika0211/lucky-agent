package lhcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/gateway/weixin"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	runErr := fn()
	closeErr := w.Close()
	os.Stdout = old

	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close stdout: %v", closeErr)
	}
	return string(out), runErr
}

func newMsgGatewayStartTestCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("platform", "", "")
	cmd.Flags().String("token", "", "")
	cmd.Flags().String("qq-appid", "", "")
	cmd.Flags().String("qq-appsecret", "", "")
	cmd.Flags().Bool("qq-sandbox", false, "")
	cmd.Flags().String("napcat-listen", "", "")
	cmd.Flags().String("napcat-path", "", "")
	cmd.Flags().String("napcat-access-token", "", "")
	cmd.Flags().Bool("all", false, "")
	return cmd
}

func TestResolveMsgGatewayStartOptionsUsesConfigDefaults(t *testing.T) {
	cmd := newMsgGatewayStartTestCmd()
	cfg := &config.Config{}
	cfg.MsgGateway.Platform = "telegram"
	cfg.MsgGateway.Telegram.Token = "tg-token"
	cfg.MsgGateway.QQOfficial.AppID = "qq-app"
	cfg.MsgGateway.QQOfficial.AppSecret = "qq-secret"
	cfg.MsgGateway.QQOfficial.Sandbox = true
	cfg.MsgGateway.NapCat.ListenAddr = "127.0.0.1:6701"
	cfg.MsgGateway.NapCat.Path = "/onebot/v11/ws"
	cfg.MsgGateway.NapCat.AccessToken = "nap-token"

	opts := resolveMsgGatewayStartOptions(cmd, cfg)
	if opts.Platform != "telegram" {
		t.Fatalf("expected platform telegram, got %q", opts.Platform)
	}
	if opts.Token != "tg-token" {
		t.Fatalf("expected token from config, got %q", opts.Token)
	}
	if opts.QQAppID != "qq-app" || opts.QQAppSecret != "qq-secret" {
		t.Fatalf("expected qq credentials from config, got app=%q secret=%q", opts.QQAppID, opts.QQAppSecret)
	}
	if !opts.QQSandbox {
		t.Fatal("expected qq sandbox from config")
	}
	if opts.NapCatListenAddr != "127.0.0.1:6701" || opts.NapCatPath != "/onebot/v11/ws" || opts.NapCatAccessToken != "nap-token" {
		t.Fatalf("expected napcat config defaults, got listen=%q path=%q token=%q", opts.NapCatListenAddr, opts.NapCatPath, opts.NapCatAccessToken)
	}
}

func TestRunConfigGetSupportsMultimodalKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".luckyagent")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data := []byte(`{
  "multimodal": {
    "provider": "openai",
    "api_key": "sk-1234567890abcdef",
    "api_base": "https://api.example.test/v1",
    "image_model": "gemini-image",
    "transcription_model": "qwen-asr",
    "image_provider": "openai-media"
  }
}`)
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return runConfigGet(&cobra.Command{}, []string{"multimodal.api_base"})
	})
	if err != nil {
		t.Fatalf("runConfigGet multimodal.api_base: %v", err)
	}
	if strings.TrimSpace(out) != "https://api.example.test/v1" {
		t.Fatalf("unexpected api_base output: %q", out)
	}

	out, err = captureStdout(t, func() error {
		return runConfigGet(&cobra.Command{}, []string{"multimodal.api_key"})
	})
	if err != nil {
		t.Fatalf("runConfigGet multimodal.api_key: %v", err)
	}
	if strings.TrimSpace(out) != "sk-12345..." {
		t.Fatalf("unexpected masked api_key output: %q", out)
	}
}

func TestResolveMsgGatewayStartOptionsFlagsOverrideConfig(t *testing.T) {
	cmd := newMsgGatewayStartTestCmd()
	if err := cmd.Flags().Set("platform", "qqofficial"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("qq-appid", "flag-app"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("qq-appsecret", "flag-secret"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("napcat-listen", "0.0.0.0:6701"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.MsgGateway.Platform = "telegram"
	cfg.MsgGateway.QQOfficial.AppID = "config-app"
	cfg.MsgGateway.QQOfficial.AppSecret = "config-secret"
	cfg.MsgGateway.NapCat.ListenAddr = "127.0.0.1:6701"

	opts := resolveMsgGatewayStartOptions(cmd, cfg)
	if opts.Platform != "qqofficial" {
		t.Fatalf("expected platform from flag, got %q", opts.Platform)
	}
	if opts.QQAppID != "flag-app" || opts.QQAppSecret != "flag-secret" {
		t.Fatalf("expected qq credentials from flags, got app=%q secret=%q", opts.QQAppID, opts.QQAppSecret)
	}
	if opts.NapCatListenAddr != "0.0.0.0:6701" {
		t.Fatalf("expected napcat listen from flag, got %q", opts.NapCatListenAddr)
	}
}

func TestValidateMsgGatewayStartOptionsRejectsMissingQQCredentials(t *testing.T) {
	err := validateMsgGatewayStartOptions(msgGatewayStartOptions{Platform: "qqofficial"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "qqofficial") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMsgGatewayStartOptionsRejectsMissingWeixinCredentials(t *testing.T) {
	err := validateMsgGatewayStartOptions(msgGatewayStartOptions{Platform: "weixin"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "msg_gateway.weixin.token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMsgGatewayStartOptionsRejectsMissingOpenClawWeixinAccount(t *testing.T) {
	err := validateMsgGatewayStartOptions(msgGatewayStartOptions{Platform: "openclawweixin"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "msg_gateway.openclawweixin.account_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMsgGatewayStartOptionsAcceptsNapCatWithoutCredentials(t *testing.T) {
	if err := validateMsgGatewayStartOptions(msgGatewayStartOptions{Platform: "napcat"}); err != nil {
		t.Fatalf("expected napcat to validate without credentials, got %v", err)
	}
}

type stubWeixinLoginClient struct {
	statuses []*weixin.QRCodeLogin
	err      error
	index    int
}

func (s *stubWeixinLoginClient) GetQRCodeStatus(ctx context.Context, qrCode string) (*weixin.QRCodeLogin, error) {
	if s.err != nil {
		return nil, s.err
	}
	if len(s.statuses) == 0 {
		return nil, fmt.Errorf("no statuses")
	}
	if s.index >= len(s.statuses) {
		return s.statuses[len(s.statuses)-1], nil
	}
	v := s.statuses[s.index]
	s.index++
	return v, nil
}

func TestPollWeixinLoginSuccess(t *testing.T) {
	client := &stubWeixinLoginClient{
		statuses: []*weixin.QRCodeLogin{
			{Status: "waiting"},
			{Status: "confirmed", AccountID: "wx-123", Token: "bot-token", BaseURL: "https://ilink.example.com"},
		},
	}

	got, err := pollWeixinLoginWithFetcher(context.Background(), client.GetQRCodeStatus, "qr-code", time.Millisecond, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AccountID != "wx-123" || got.Token != "bot-token" {
		t.Fatalf("unexpected login result: %#v", got)
	}
}

func TestPollWeixinLoginFailureStatus(t *testing.T) {
	client := &stubWeixinLoginClient{
		statuses: []*weixin.QRCodeLogin{
			{Status: "failed", Description: "scan denied"},
		},
	}

	_, err := pollWeixinLoginWithFetcher(context.Background(), client.GetQRCodeStatus, "qr-code", time.Millisecond, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "scan denied") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWeixinLoginDriverFlag(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("driver", "ilink", "")
	cmd.Flags().String("base-url", "", "")
	cmd.Flags().Duration("poll-interval", 2*time.Second, "")
	cmd.Flags().Duration("timeout", 3*time.Minute, "")
	cmd.Flags().Bool("no-save", false, "")
	cmd.Flags().Bool("print-status", true, "")

	if err := cmd.Flags().Set("driver", "openclaw"); err != nil {
		t.Fatal(err)
	}
	got, _ := cmd.Flags().GetString("driver")
	if got != "openclaw" {
		t.Fatalf("expected driver openclaw, got %q", got)
	}
}
