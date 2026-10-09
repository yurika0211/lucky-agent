package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/tool"
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

func TestSessionToolTraceEndpointPaginatesAndBoundsPayloads(t *testing.T) {
	agent := createTestAgent(t)
	server := New(agent, DefaultServerConfig())
	session := agent.Sessions().Ensure("tool-trace-paging-session")
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("call-%d", i)
		session.AddProviderMessage(provider.Message{
			Role: "assistant",
			ToolCalls: []provider.ToolCall{{
				ID: id, Name: "file_read", Arguments: fmt.Sprintf(`{"path":"%s"}`, strings.Repeat("x", 6000)),
			}},
		})
		session.AddProviderMessage(provider.Message{
			Role: "tool", ToolCallID: id, Name: "file_read", Content: strings.Repeat("result ", 1500),
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/tool-trace-paging-session/tools?limit=2", nil)
	writer := httptest.NewRecorder()
	server.handleSessionByID(writer, req)
	if writer.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", writer.Code, writer.Body.String())
	}
	var response struct {
		TotalCalls int                `json:"total_calls"`
		Returned   int                `json:"returned"`
		HasMore    bool               `json:"has_more"`
		Tools      []tool.TraceRecord `json:"tools"`
	}
	if err := json.NewDecoder(writer.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.TotalCalls != 4 || response.Returned != 2 || len(response.Tools) != 2 || !response.HasMore {
		t.Fatalf("unexpected page: total=%d returned=%d tools=%d has_more=%v", response.TotalCalls, response.Returned, len(response.Tools), response.HasMore)
	}
	if len([]rune(response.Tools[0].Arguments)) > traceArgumentMaxChars || len([]rune(response.Tools[0].Result)) > traceResultMaxChars {
		t.Fatalf("trace fields were not bounded: args=%d result=%d", len([]rune(response.Tools[0].Arguments)), len([]rune(response.Tools[0].Result)))
	}
}
