package agent

import (
	"context"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

func TestRequestProfileGateEndToEndSelectsLightweightChat(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	cfg.Foreground = false
	if err := a.cfg.Set("agent.foreground.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		return &provider.Response{Content: "你好"}, nil
	}}
	result, err := a.RunLoopWithSession(context.Background(), sess, "你好", cfg)
	if err != nil {
		t.Fatalf("RunLoopWithSession() error = %v", err)
	}
	if result.Profile != RequestProfileChat || result.ProfileSource != "fallback_low_risk" {
		t.Fatalf("unexpected profile result: %+v", result)
	}
	if result.ProfileConfidence <= 0 {
		t.Fatalf("unexpected profile confidence or budget: %+v", result)
	}
}

func TestRequestProfileGateEndToEndSelectsCodeTask(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	cfg.Foreground = false
	if err := a.cfg.Set("agent.foreground.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		return &provider.Response{Content: "已完成"}, nil
	}}
	result, err := a.RunLoopWithSession(context.Background(), sess, "运行测试并修复失败", cfg)
	if err != nil {
		t.Fatalf("RunLoopWithSession() error = %v", err)
	}
	if result.Profile != RequestProfileCodeTask || result.ProfileSource != "deterministic_code_intent" {
		t.Fatalf("unexpected profile result: %+v", result)
	}
}
