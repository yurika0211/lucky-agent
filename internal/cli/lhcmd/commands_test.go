package lhcmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/yurika0211/luckyharness/internal/config"
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
	if !strings.Contains(err.Error(), "qqofficial 需要") {
		t.Fatalf("unexpected error: %v", err)
	}
}
