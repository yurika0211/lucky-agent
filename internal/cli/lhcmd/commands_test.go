package lhcmd

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/gateway/weixin"
)

func newMsgGatewayStartTestCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("platform", "", "")
	cmd.Flags().String("token", "", "")
	cmd.Flags().String("qq-appid", "", "")
	cmd.Flags().String("qq-appsecret", "", "")
	cmd.Flags().Bool("qq-sandbox", false, "")
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
	cfg := &config.Config{}
	cfg.MsgGateway.Platform = "telegram"
	cfg.MsgGateway.QQOfficial.AppID = "config-app"
	cfg.MsgGateway.QQOfficial.AppSecret = "config-secret"

	opts := resolveMsgGatewayStartOptions(cmd, cfg)
	if opts.Platform != "qqofficial" {
		t.Fatalf("expected platform from flag, got %q", opts.Platform)
	}
	if opts.QQAppID != "flag-app" || opts.QQAppSecret != "flag-secret" {
		t.Fatalf("expected qq credentials from flags, got app=%q secret=%q", opts.QQAppID, opts.QQAppSecret)
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
