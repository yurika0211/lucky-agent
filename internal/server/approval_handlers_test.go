package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleApprovalsEmptyListIsJSONArray(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())
	response := httptest.NewRecorder()
	s.handleApprovals(response, httptest.NewRequest(http.MethodGet, "/api/v1/approvals", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"approvals":null`) {
		t.Fatalf("empty approvals encoded as null: %s", response.Body.String())
	}
	var payload struct {
		Approvals []map[string]any `json:"approvals"`
		Count     int              `json:"count"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Approvals == nil || payload.Count != 0 {
		t.Fatalf("payload = %+v", payload)
	}
}
