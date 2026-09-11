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
	cmd.Flags().String("feishu-app-id", "", "")
	cmd.Flags().String("feishu-app-secret", "", "")
	cmd.Flags().String("feishu-verification-token", "", "")
	cmd.Flags().String("feishu-listen", "", "")
	cmd.Flags().String("feishu-path", "", "")
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
	cfg.MsgGateway.QQOfficial.Proxy = "http://127.0.0.1:7897"
	cfg.MsgGateway.QQOfficial.Sandbox = true
	cfg.MsgGateway.NapCat.ListenAddr = "127.0.0.1:6701"
	cfg.MsgGateway.NapCat.Path = "/onebot/v11/ws"
	cfg.MsgGateway.NapCat.AccessToken = "nap-token"
	cfg.MsgGateway.Feishu.AppID = "cli-feishu-app"
	cfg.MsgGateway.Feishu.AppSecret = "feishu-secret"
	cfg.MsgGateway.Feishu.VerificationToken = "feishu-verify"
	cfg.MsgGateway.Feishu.ListenAddr = "127.0.0.1:6710"
	cfg.MsgGateway.Feishu.Path = "/feishu/events"

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
	if opts.FeishuAppID != "cli-feishu-app" || opts.FeishuAppSecret != "feishu-secret" || opts.FeishuVerifyToken != "feishu-verify" {
		t.Fatalf("expected feishu credentials from config, got app=%q secret=%q token=%q", opts.FeishuAppID, opts.FeishuAppSecret, opts.FeishuVerifyToken)
	}
	if opts.FeishuListenAddr != "127.0.0.1:6710" || opts.FeishuPath != "/feishu/events" {
		t.Fatalf("expected feishu callback defaults, got listen=%q path=%q", opts.FeishuListenAddr, opts.FeishuPath)
	}
}

func TestRunConfigGetSupportsMultimodalKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
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

func TestRunConfigGetSupportsProtocol(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configDir := filepath.Join(home, ".luckyagent")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{
  "llm_provider": {"protocol": "responses"}
}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for _, key := range []string{"protocol", "llm_provider.protocol"} {
		out, err := captureStdout(t, func() error {
			return runConfigGet(&cobra.Command{}, []string{key})
		})
		if err != nil {
			t.Fatalf("runConfigGet %s: %v", key, err)
		}
		if strings.TrimSpace(out) != "responses" {
			t.Fatalf("%s output = %q, want responses", key, out)
		}
	}
}

func TestRunConfigTimeoutPrintsEffectiveValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	out, err := captureStdout(t, func() error {
		return runConfigTimeout(&cobra.Command{}, nil)
	})
	if err != nil {
		t.Fatalf("runConfigTimeout: %v", err)
	}
	for _, want := range []string{
		"Telegram Gateway Chat:       600s (10m)",
		"Agent Loop:                  60s (1m)",
		"Simple Local Inspection:     25s",
		"OpenCLI:                     20s",
		"Computer Use (total):        300s (5m)",
		"Computer Use (step):         30s",
		"Hooks:                        30s",
		"[msg_gateway.telegram.chat_timeout_seconds]",
		"[agent.timeout_seconds]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected timeout output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestShortCommandAliases(t *testing.T) {
	root := newRootCmd()
	for _, path := range [][]string{
		{"cfg", "g"},
		{"cfg", "s"},
		{"d", "t"},
		{"gw", "s"},
		{"kb", "s"},
		{"mem", "td", "st"},
		{"sess", "c"},
		{"web", "s"},
		{"api"},
		{"v"},
	} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatalf("find short command %v: %v", path, err)
		}
		if cmd == nil {
			t.Fatalf("find short command %v returned nil command", path)
		}
	}

	findChild := func(parent *cobra.Command, name string) *cobra.Command {
		for _, child := range parent.Commands() {
			if child.Name() == name {
				return child
			}
		}
		return nil
	}
	for _, path := range [][]string{
		{"msg-gateway", "stop"},
		{"msg-gateway", "status"},
		{"dashboard", "stop"},
		{"memory", "tidal-stats"},
	} {
		parent := root
		for _, name := range path[:len(path)-1] {
			parent = findChild(parent, name)
			if parent == nil {
				break
			}
		}
		if parent != nil && findChild(parent, path[len(path)-1]) != nil {
			t.Fatalf("expected removed command %v to be unavailable", path)
		}
	}
}

func TestNormalizeMsgGatewayPlatform(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "tg", want: "telegram"},
		{input: " telegram ", want: "telegram"},
		{input: "qq", want: "qqofficial"},
		{input: "nap", want: "napcat"},
		{input: "fs", want: "feishu"},
		{input: "wx", want: "weixin"},
		{input: "ocwx", want: "openclawweixin"},
		{input: "custom", want: "custom"},
	} {
		if got := normalizeMsgGatewayPlatform(test.input); got != test.want {
			t.Errorf("normalizeMsgGatewayPlatform(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestRunDiagTimeoutWithoutRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	out, err := captureStdout(t, func() error {
		return runDiagTimeout(&cobra.Command{}, nil)
	})
	if err != nil {
		t.Fatalf("runDiagTimeout: %v", err)
	}
	if strings.TrimSpace(out) != "暂无超时诊断记录。" {
		t.Fatalf("unexpected diagnostic output: %q", out)
	}
}

func TestRunConfigGetSupportsFeishuKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configDir := filepath.Join(home, ".luckyagent")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data := []byte(`{
  "msg_gateway": {
    "qqofficial": {
      "proxy": "socks5://127.0.0.1:7890"
    },
    "feishu": {
      "app_id": "cli_test",
      "app_secret": "secret-1234567890",
      "verification_token": "verify-1234567890",
      "listen_addr": "127.0.0.1:7710"
    }
  }
}`)
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := captureStdout(t, func() error {
		return runConfigGet(&cobra.Command{}, []string{"msg_gateway.feishu.listen_addr"})
	})
	if err != nil {
		t.Fatalf("runConfigGet feishu listen_addr: %v", err)
	}
	if strings.TrimSpace(out) != "127.0.0.1:7710" {
		t.Fatalf("unexpected listen_addr output: %q", out)
	}

	out, err = captureStdout(t, func() error {
		return runConfigGet(&cobra.Command{}, []string{"msg_gateway.feishu.app_secret"})
	})
	if err != nil {
		t.Fatalf("runConfigGet feishu app_secret: %v", err)
	}
	if strings.TrimSpace(out) != "secret-1..." {
		t.Fatalf("unexpected masked app_secret output: %q", out)
	}

	out, err = captureStdout(t, func() error {
		return runConfigGet(&cobra.Command{}, []string{"msg_gateway.qqofficial.proxy"})
	})
	if err != nil {
		t.Fatalf("runConfigGet QQ Official proxy: %v", err)
	}
	if strings.TrimSpace(out) != "socks5://127.0.0.1:7890" {
		t.Fatalf("unexpected QQ Official proxy output: %q", out)
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
	if err := cmd.Flags().Set("feishu-app-id", "flag-feishu-app"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("feishu-listen", "0.0.0.0:6710"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.MsgGateway.Platform = "telegram"
	cfg.MsgGateway.QQOfficial.AppID = "config-app"
	cfg.MsgGateway.QQOfficial.AppSecret = "config-secret"
	cfg.MsgGateway.NapCat.ListenAddr = "127.0.0.1:6701"
	cfg.MsgGateway.Feishu.AppID = "config-feishu-app"
	cfg.MsgGateway.Feishu.ListenAddr = "127.0.0.1:6710"

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
	if opts.FeishuAppID != "flag-feishu-app" || opts.FeishuListenAddr != "0.0.0.0:6710" {
		t.Fatalf("expected feishu overrides from flags, got app=%q listen=%q", opts.FeishuAppID, opts.FeishuListenAddr)
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

func TestValidateMsgGatewayStartOptionsRejectsMissingFeishuCredentials(t *testing.T) {
	err := validateMsgGatewayStartOptions(msgGatewayStartOptions{Platform: "feishu"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "app_id") || !strings.Contains(err.Error(), "app_secret") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMsgGatewayStartOptionsAcceptsFeishuAppIDAndSecret(t *testing.T) {
	err := validateMsgGatewayStartOptions(msgGatewayStartOptions{
		Platform:        "feishu",
		FeishuAppID:     "cli_xxx",
		FeishuAppSecret: "secret",
	})
	if err != nil {
		t.Fatalf("expected feishu options to validate, got %v", err)
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

func TestPollWeixinLoginRetriesTransientErrors(t *testing.T) {
	attempts := 0
	result, err := pollWeixinLoginWithFetcher(context.Background(), func(context.Context, string) (*weixin.QRCodeLogin, error) {
		attempts++
		if attempts < 3 {
			return nil, fmt.Errorf("temporary network failure")
		}
		return &weixin.QRCodeLogin{Status: "success", Token: "token", AccountID: "account"}, nil
	}, "qr-code", time.Millisecond, false)
	if err != nil {
		t.Fatalf("expected transient errors to be retried, got %v", err)
	}
	if result.AccountID != "account" || attempts != 3 {
		t.Fatalf("unexpected retry result: result=%+v attempts=%d", result, attempts)
	}
}

func TestPollWeixinLoginDoesNotRetryTerminalError(t *testing.T) {
	attempts := 0
	_, err := pollWeixinLoginWithFetcher(context.Background(), func(context.Context, string) (*weixin.QRCodeLogin, error) {
		attempts++
		return nil, fmt.Errorf("qrcode expired")
	}, "qr-code", time.Millisecond, false)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected terminal qrcode error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected terminal error not to retry, attempts=%d", attempts)
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
