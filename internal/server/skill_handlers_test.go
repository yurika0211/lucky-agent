package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
)

// writeSkillFixture drops a skill directory into the agent's skills dir and
// reloads, mirroring how a hand-installed skill appears.
func writeSkillFixture(t *testing.T, a *agent.Agent, name, body string) {
	t.Helper()
	dir := filepath.Join(a.Config().HomeDir(), "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if _, err := a.LoadSkills(filepath.Join(a.Config().HomeDir(), "skills")); err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	return out
}

func TestHandleSkillsEmpty(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil)
	w := httptest.NewRecorder()
	s.handleSkills(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeJSON(t, w)
	if count, ok := resp["count"].(float64); !ok || count != 0 {
		t.Errorf("count = %v, want 0", resp["count"])
	}
	if _, ok := resp["skills_dir"]; !ok {
		t.Error("skills_dir missing from the response")
	}
}

func TestHandleSkillsMethodNotAllowed(t *testing.T) {
	s := New(createTestAgent(t), DefaultServerConfig())
	w := httptest.NewRecorder()
	s.handleSkills(w, httptest.NewRequest(http.MethodPut, "/api/v1/skills", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// TestHandleSkillsStateIsAString guards a serialization regression: SkillState is
// an int enum with no MarshalJSON, so a DTO that forwards it raw would emit a
// number the UI cannot render.
func TestHandleSkillsStateIsAString(t *testing.T) {
	a := createTestAgent(t)
	writeSkillFixture(t, a, "alpha", "# alpha\n\nDesc.\n\n## Tools\n\n- `do`: Do\n")
	s := New(a, DefaultServerConfig())

	w := httptest.NewRecorder()
	s.handleSkills(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Skills []struct {
			Name      string `json:"name"`
			State     string `json:"state"`
			Managed   bool   `json:"managed"`
			ToolCount int    `json:"tool_count"`
			Tools     []struct {
				FullName   string `json:"full_name"`
				Registered bool   `json:"registered"`
				Enabled    bool   `json:"enabled"`
			} `json:"tools"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("state is not a string (or shape changed): %v; body: %s", err, w.Body.String())
	}
	if len(resp.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(resp.Skills))
	}
	sk := resp.Skills[0]
	if sk.State != "enabled" {
		t.Errorf("state = %q, want enabled", sk.State)
	}
	// A hand-copied skill has no ledger entry, so the UI must not offer rollback.
	if sk.Managed {
		t.Error("a hand-installed skill was reported as managed")
	}
	if len(sk.Tools) != 1 || sk.Tools[0].FullName != "skill_alpha_do" {
		t.Errorf("unexpected tools: %+v", sk.Tools)
	}
	if !sk.Tools[0].Registered || !sk.Tools[0].Enabled {
		t.Errorf("tool should be registered and enabled: %+v", sk.Tools[0])
	}
}

func TestHandleSkillDetailAndLifecycle(t *testing.T) {
	a := createTestAgent(t)
	writeSkillFixture(t, a, "beta", "# beta\n\nDesc.\n\n## Tools\n\n- `do`: Do\n")
	s := New(a, DefaultServerConfig())

	// Detail.
	w := httptest.NewRecorder()
	s.handleSkillRoutes(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/beta", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("detail: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Disable, then enable.
	for _, step := range []struct {
		action string
		want   string
	}{{"disable", "disabled"}, {"enable", "enabled"}} {
		w := httptest.NewRecorder()
		s.handleSkillRoutes(w, httptest.NewRequest(http.MethodPost, "/api/v1/skills/beta/"+step.action, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", step.action, w.Code, w.Body.String())
		}
		resp := decodeJSON(t, w)
		if resp["state"] != step.want {
			t.Errorf("%s: state = %v, want %v", step.action, resp["state"], step.want)
		}
	}

	// Enabling an already-enabled skill is a state-machine refusal, not a 400.
	w = httptest.NewRecorder()
	s.handleSkillRoutes(w, httptest.NewRequest(http.MethodPost, "/api/v1/skills/beta/enable", nil))
	if w.Code != http.StatusConflict {
		t.Errorf("re-enable: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleSkillReloadPreservesOtherSkills is the HTTP-level check for the
// registry bug: reloading one skill used to reset every other skill's state.
func TestHandleSkillReloadPreservesOtherSkills(t *testing.T) {
	a := createTestAgent(t)
	writeSkillFixture(t, a, "one", "# one\n\nDesc.\n\n## Tools\n\n- `do`: Do\n")
	writeSkillFixture(t, a, "two", "# two\n\nDesc.\n\n## Tools\n\n- `do`: Do\n")
	s := New(a, DefaultServerConfig())

	w := httptest.NewRecorder()
	s.handleSkillRoutes(w, httptest.NewRequest(http.MethodPost, "/api/v1/skills/one/reload", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("reload: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	s.handleSkills(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
	var resp struct {
		Skills []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, sk := range resp.Skills {
		if sk.State != "enabled" {
			t.Errorf("skill %s state = %q after reloading a different skill, want enabled", sk.Name, sk.State)
		}
	}
}

func TestHandleSkillNotFound(t *testing.T) {
	s := New(createTestAgent(t), DefaultServerConfig())
	w := httptest.NewRecorder()
	s.handleSkillRoutes(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/nope", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestHandleSkillRejectsTraversalName guards against building a filesystem path
// from a URL segment.
func TestHandleSkillRejectsTraversalName(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	sentinel := filepath.Join(a.Config().HomeDir(), "config.json")
	for _, path := range []string{
		"/api/v1/skills/..%2F..%2Fetc/uninstall",
		"/api/v1/skills/../../etc/uninstall",
		"/api/v1/skills/..",
	} {
		w := httptest.NewRecorder()
		s.handleSkillRoutes(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Errorf("%s: expected 400 or 404, got %d: %s", path, w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(sentinel); err != nil && !os.IsNotExist(err) {
		t.Errorf("runtime home was disturbed: %v", err)
	}
}

// ---- install ----

func buildSkillTarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// postSkillArchive builds a multipart request the way the GUI does. Modeled on
// postUpload in upload_handlers_test.go.
func postSkillArchive(t *testing.T, filename string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// pollInstall waits for a staged install to reach a terminal state.
func pollInstall(t *testing.T, s *Server, id string) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		w := httptest.NewRecorder()
		s.handleSkillRoutes(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/installs/"+id, nil))
		if w.Code == http.StatusOK {
			resp := decodeJSON(t, w)
			switch resp["status"] {
			case "ready", "rejected", "failed", "aborted", "confirmed":
				return resp
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("install %s never reached a terminal state", id)
	return nil
}

func TestHandleSkillInstallMissingFile(t *testing.T) {
	s := New(createTestAgent(t), DefaultServerConfig())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleSkillRoutes(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSkillInstallAcceptsArchiveAndConfirms(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	archive := buildSkillTarGz(t, map[string]string{
		"my-skill/SKILL.md": "---\nname: my_skill\ndescription: An installed skill.\n---\n\n# My Skill\n\n## Tools\n\n- `do`: Do a thing\n",
	})

	w := httptest.NewRecorder()
	s.handleSkillRoutes(w, postSkillArchive(t, "my-skill.tar.gz", archive))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeJSON(t, w)
	id, _ := resp["install_id"].(string)
	if id == "" {
		t.Fatal("no install_id returned")
	}

	final := pollInstall(t, s, id)
	if final["status"] != "ready" {
		t.Fatalf("status = %v, want ready (error: %v)", final["status"], final["error"])
	}
	if final["can_confirm"] != true {
		t.Errorf("can_confirm = %v, want true", final["can_confirm"])
	}

	w = httptest.NewRecorder()
	s.handleSkillRoutes(w, httptest.NewRequest(http.MethodPost, "/api/v1/skills/installs/"+id+"/confirm", strings.NewReader(`{"accept_warnings":true}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The skill must land on disk registered but NOT enabled.
	if _, err := os.Stat(filepath.Join(a.Config().HomeDir(), "skills", "my_skill", "SKILL.md")); err != nil {
		t.Fatalf("skill not installed: %v", err)
	}
	tl, ok := a.Tools().Get("skill_my_skill_do")
	if !ok {
		t.Fatal("skill tool not registered after install")
	}
	if tl.Enabled {
		t.Error("a freshly installed skill is enabled; it must require an explicit enable")
	}
}

// TestHandleSkillInstallRejectsTraversalArchive is the end-to-end zip-slip case.
func TestHandleSkillInstallRejectsTraversalArchive(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	archive := buildSkillTarGz(t, map[string]string{
		"my-skill/SKILL.md":   "# my skill\n",
		"../../../escaped.sh": "#!/bin/sh\necho pwned\n",
	})

	w := httptest.NewRecorder()
	s.handleSkillRoutes(w, postSkillArchive(t, "evil.tar.gz", archive))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	id := decodeJSON(t, w)["install_id"].(string)

	final := pollInstall(t, s, id)
	if final["status"] != "rejected" {
		t.Fatalf("status = %v, want rejected", final["status"])
	}
	if final["can_confirm"] != false {
		t.Error("a rejected install reports can_confirm=true")
	}

	scan, _ := final["scan"].(map[string]interface{})
	findings, _ := scan["findings"].([]interface{})
	found := false
	for _, f := range findings {
		if m, ok := f.(map[string]interface{}); ok && m["rule"] == "path_traversal" {
			found = true
		}
	}
	if !found {
		t.Errorf("no path_traversal finding reported: %v", findings)
	}

	// Nothing may have been written outside the staging area.
	home := a.Config().HomeDir()
	for _, sentinel := range []string{
		filepath.Join(home, "escaped.sh"),
		filepath.Join(filepath.Dir(home), "escaped.sh"),
	} {
		if _, err := os.Stat(sentinel); err == nil {
			t.Errorf("archive escaped staging: %s exists", sentinel)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(home, "skills"))
	if len(entries) != 0 {
		t.Errorf("skills/ was modified by a rejected install: %v", entries)
	}
}

func TestHandleSkillInstallAbort(t *testing.T) {
	s := New(createTestAgent(t), DefaultServerConfig())
	archive := buildSkillTarGz(t, map[string]string{
		"s/SKILL.md": "---\nname: abortme\ndescription: d.\n---\n\n# A\n",
	})
	w := httptest.NewRecorder()
	s.handleSkillRoutes(w, postSkillArchive(t, "s.tar.gz", archive))
	id := decodeJSON(t, w)["install_id"].(string)
	pollInstall(t, s, id)

	w = httptest.NewRecorder()
	s.handleSkillRoutes(w, httptest.NewRequest(http.MethodPost, "/api/v1/skills/installs/"+id+"/abort", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("abort: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// Abort is idempotent.
	w = httptest.NewRecorder()
	s.handleSkillRoutes(w, httptest.NewRequest(http.MethodPost, "/api/v1/skills/installs/"+id+"/abort", nil))
	if w.Code != http.StatusOK {
		t.Errorf("second abort: expected 200, got %d", w.Code)
	}
}

func TestHandleSkillInstallUnknownID(t *testing.T) {
	s := New(createTestAgent(t), DefaultServerConfig())
	w := httptest.NewRecorder()
	s.handleSkillRoutes(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/installs/inst-1-abcd", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestSkillRoutesAreRegistered checks the two entries actually reach the
// handlers through the mux, since nothing else in the suite exercises routing.
func TestSkillRoutesAreRegistered(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())
	mux := http.NewServeMux()
	s.registerRoutes(mux, []routeEntry{
		{path: "/api/v1/skills", handler: s.handleSkills},
		{path: "/api/v1/skills/", handler: s.handleSkillRoutes},
	})

	for _, tc := range []struct{ path, method string }{
		{"/api/v1/skills", http.MethodGet},
		{"/api/v1/skills/nope", http.MethodGet},
		{"/api/v1/skills/installs", http.MethodGet},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code == http.StatusNotFound && tc.path == "/api/v1/skills" {
			t.Errorf("%s %s did not reach a handler", tc.method, tc.path)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("%s %s: content-type = %q, want JSON", tc.method, tc.path, ct)
		}
	}
	_ = fmt.Sprint()
}
