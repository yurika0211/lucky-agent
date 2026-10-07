package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/credentials"
	"github.com/yurika0211/luckyagent/internal/tool"
)

func testConfigManager(t *testing.T, home string) *config.Manager {
	t.Helper()
	mgr, err := config.NewManagerWithDir(home)
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	return mgr
}

func TestCredentialFormRejectsSecretArguments(t *testing.T) {
	args := scrubCredentialArgs(map[string]any{
		"id":       "openai-main",
		"kind":     "llm_api_key",
		"prompt":   "填写 OpenAI key",
		"api_key":  "sk-secret",
		"password": "hidden",
		"value":    "also-hidden",
	})
	if _, ok := args["api_key"]; ok {
		t.Fatal("api_key leaked into the form request")
	}
	if _, ok := args["password"]; ok || args["value"] != nil {
		t.Fatal("secret field leaked into the form request")
	}
	if args["id"] != "openai-main" {
		t.Fatalf("id=%v", args["id"])
	}
}

func TestCredentialFormFieldsRejectBadID(t *testing.T) {
	_, err := credentialFormFields(map[string]any{
		"id":     "../config",
		"prompt": "填写",
	})
	if err == nil {
		t.Fatal("path-like credential id must be rejected")
	}
}

func TestRequestCredentialStoresSecretOutsideModelResult(t *testing.T) {
	home := t.TempDir()
	mgr := testConfigManager(t, home)
	agent := &Agent{cfg: mgr, hitl: newHITLGate()}
	done := make(chan detailedToolExecutionResult, 1)
	go func() {
		result, err := agent.executeRequestCredentialHITL(context.Background(), "s1", map[string]any{
			"id":      "openai-main",
			"kind":    "llm_api_key",
			"prompt":  "填写 OpenAI key",
			"bind":    "chat",
			"api_key": "sk-must-not-be-used",
		})
		if err != nil {
			t.Errorf("execute: %v", err)
			return
		}
		done <- result
	}()

	var pending []tool.PendingApproval
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pending = agent.hitl.list()
		if len(pending) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pending) != 1 {
		t.Fatalf("pending=%d", len(pending))
	}
	if pending[0].Method != hitlKindCredential {
		t.Fatalf("method=%s", pending[0].Method)
	}
	if _, ok := pending[0].Params["args"].(map[string]any)["api_key"]; ok {
		t.Fatal("pending form exposed api_key")
	}
	if _, err := agent.hitl.Resolve(pending[0].ID, "submit", "sk-live-secret"); err != nil {
		t.Fatal(err)
	}

	var result detailedToolExecutionResult
	select {
	case result = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
	if result.Output == "" || strings.Contains(result.Output, "sk-live-secret") || strings.Contains(result.Output, "sk-must-not-be-used") {
		t.Fatalf("model-visible output leaked or was empty: %q", result.Output)
	}
	store, err := credentials.NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	found := false
	err = store.WithCredential(context.Background(), "openai-main", func(value []byte) error {
		found = string(value) == "sk-live-secret"
		return nil
	})
	if err != nil || !found {
		t.Fatalf("stored credential missing: found=%v err=%v", found, err)
	}
	if mgr.Get().ModelEndpoint("chat").CredentialRef != "openai-main" {
		t.Fatalf("bind=%q", mgr.Get().ModelEndpoint("chat").CredentialRef)
	}
}
