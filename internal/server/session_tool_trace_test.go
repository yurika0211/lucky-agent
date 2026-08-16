package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yurika0211/luckyagent/internal/provider"
)

func TestSessionToolTraceEndpointReturnsAnnotations(t *testing.T) {
	agent := createTestAgent(t)
	server := New(agent, DefaultServerConfig())
	session := agent.Sessions().Ensure("tool-trace-session")
	session.AddProviderMessage(provider.Message{
		Role: "assistant",
		ToolCalls: []provider.ToolCall{{
			ID:        "call-read",
			Name:      "file_read",
			Arguments: `{"path":"README.md","limit":12}`,
		}},
	})
	session.AddProviderMessage(provider.Message{
		Role:       "tool",
		ToolCallID: "call-read",
		Name:       "file_read",
		Content:    "# LuckyAgent",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/tool-trace-session/tools", nil)
	writer := httptest.NewRecorder()
	server.handleSessionByID(writer, req)
	if writer.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", writer.Code, writer.Body.String())
	}

	var response struct {
		TotalCalls int `json:"total_calls"`
		Successes  int `json:"successes"`
		Tools      []struct {
			Name       string `json:"name"`
			Success    bool   `json:"success"`
			Annotation string `json:"annotation"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(writer.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.TotalCalls != 1 || response.Successes != 1 || len(response.Tools) != 1 {
		t.Fatalf("unexpected tool trace: %#v", response)
	}
	if response.Tools[0].Name != "file_read" || !response.Tools[0].Success || response.Tools[0].Annotation == "" {
		t.Fatalf("unexpected record: %#v", response.Tools[0])
	}
}

func TestSessionToolTraceRecordsApplyConfiguredTemplates(t *testing.T) {
	records := sessionToolTraceRecords([]provider.Message{{
		Role: "assistant",
		ToolCalls: []provider.ToolCall{{
			ID: "call-1", Name: "file_read", Arguments: `{"path":"README.md"}`,
		}},
	}}, map[string]string{"file_read": "检查了 {path}"})
	if len(records) != 1 || records[0].Annotation != "检查了 README.md" {
		t.Fatalf("unexpected records: %#v", records)
	}
}
