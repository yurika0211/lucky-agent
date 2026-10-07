package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPatchAndDeleteSession(t *testing.T) {
	agent := createTestAgent(t)
	server := New(agent, DefaultServerConfig())
	sess := agent.Sessions().Ensure("organize-me")
	sess.SetTitle("old")
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	body := bytes.NewBufferString(`{"title":"renamed","pinned":true,"project":"luckyagent"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/organize-me", body)
	writer := httptest.NewRecorder()
	server.handleSessionByID(writer, req)
	if writer.Code != http.StatusOK {
		t.Fatalf("patch status = %d body = %s", writer.Code, writer.Body.String())
	}
	var patched map[string]any
	if err := json.Unmarshal(writer.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched["title"] != "renamed" || patched["pinned"] != true || patched["project"] != "luckyagent" {
		t.Fatalf("patch payload = %#v", patched)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	listWriter := httptest.NewRecorder()
	server.handleSessions(listWriter, listReq)
	if listWriter.Code != http.StatusOK {
		t.Fatalf("list status = %d", listWriter.Code)
	}
	if !bytes.Contains(listWriter.Body.Bytes(), []byte(`"pinned":true`)) {
		t.Fatalf("list missing pin: %s", listWriter.Body.String())
	}

	empty := bytes.NewBufferString(`{"title":"  "}`)
	bad := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/organize-me", empty)
	badWriter := httptest.NewRecorder()
	server.handleSessionByID(badWriter, bad)
	if badWriter.Code != http.StatusBadRequest {
		t.Fatalf("empty title status = %d", badWriter.Code)
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/organize-me", nil)
	delWriter := httptest.NewRecorder()
	server.handleSessionByID(delWriter, del)
	if delWriter.Code != http.StatusOK {
		t.Fatalf("delete status = %d body = %s", delWriter.Code, delWriter.Body.String())
	}
	if _, ok := agent.Sessions().Get("organize-me"); ok {
		t.Fatal("session still present after delete")
	}
}
