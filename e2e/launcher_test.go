//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	root := repoRoot()
	binary := filepath.Join(root, "e2e", "bin", "lh-e2e")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir e2e bin: %v\n", err)
		os.Exit(1)
	}
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/la")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build lh: %v\n%s\n", err, out)
		os.Exit(1)
	}
	if err := os.Setenv("LH_E2E_BIN", binary); err != nil {
		fmt.Fprintf(os.Stderr, "set binary path: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.Remove(binary)
	os.Exit(code)
}

func TestVersion(t *testing.T) {
	cmd := exec.Command(lhBinary(t), "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lh version: %v\n%s", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "LuckyAgent ") || !strings.Contains(text, "commit:") {
		t.Fatalf("version output = %q", text)
	}
}

func TestServeHealthAndShutdown(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, nil)
	srv := startServe(t, home)
	defer srv.stop(t)

	body := getJSON(t, srv.baseURL()+"/api/v1/health/live")
	if body["status"] != "healthy" {
		t.Fatalf("health = %#v", body)
	}
}

func TestSyncChatPersistsAssistantText(t *testing.T) {
	var calls int
	upstream := serveModel(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if sessionRecapRequest(r) {
			writeCompletion(w, "You asked for the fixed answer.")
			return
		}
		calls++
		if completionReviewRequest(r) {
			writeCompletion(w, `{"status":"complete","reason":"the fixed answer was returned","evidence":["assistant returned the fixed answer"]}`)
			return
		}
		writeCompletion(w, "e2e fixed answer")
	})
	home := t.TempDir()
	writeConfig(t, home, chatConfig(upstream.URL+"/v1", "sk-e2e"))
	srv := startServe(t, home)
	defer srv.stop(t)
	defer dumpServer(t, srv)

	resp := postJSON(t, srv.baseURL()+"/api/v1/chat/sync", map[string]any{
		"message":      "say the fixed answer",
		"auto_approve": true,
	})
	if resp["response"] != "e2e fixed answer" {
		t.Fatalf("response = %#v", resp)
	}
	sessionID, _ := resp["session_id"].(string)
	if sessionID == "" {
		t.Fatalf("missing session id: %#v", resp)
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want the answer plus one completion review", calls)
	}
	messages := readSessionMessages(t, home, sessionID)
	var assistant []string
	for _, message := range messages {
		if message["role"] == "assistant" {
			assistant = append(assistant, fmt.Sprint(message["content"]))
		}
	}
	if len(assistant) != 1 || assistant[0] != "e2e fixed answer" {
		t.Fatalf("assistant messages = %#v", messages)
	}
}

func TestToolResultCarriesObservation(t *testing.T) {
	note := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(note, []byte("e2e note"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stage int
	upstream := serveModel(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if completionReviewRequest(r) {
			writeCompletion(w, `{"status":"complete","reason":"the note was read","evidence":["file_read returned the note"]}`)
			return
		}
		stage++
		if stage == 1 {
			writeToolCompletion(w, "call-read", "file_read", fmt.Sprintf(`{"path":%q}`, note))
			return
		}
		writeCompletion(w, "read the note")
	})
	home := t.TempDir()
	writeConfig(t, home, chatConfig(upstream.URL+"/v1", "sk-e2e"))
	srv := startServe(t, home)
	defer srv.stop(t)

	resp := postJSON(t, srv.baseURL()+"/api/v1/chat/sync", map[string]any{
		"message":      "read the note",
		"auto_approve": true,
	})
	if resp["response"] != "read the note" {
		t.Fatalf("response = %#v", resp)
	}
	sessionID := fmt.Sprint(resp["session_id"])
	messages := readSessionMessages(t, home, sessionID)
	var tool map[string]any
	for _, message := range messages {
		if message["role"] == "tool" && message["name"] == "file_read" {
			tool = message
		}
	}
	if tool == nil {
		t.Fatalf("no file_read tool message: %#v", messages)
	}
	if !strings.Contains(fmt.Sprint(tool["content"]), "e2e note") {
		t.Fatalf("tool content = %#v", tool)
	}
	if tool["tool_call_id"] != "call-read" || tool["name"] != "file_read" {
		t.Fatalf("tool identity = %#v", tool)
	}
}

func TestProviderUnauthorizedStartsAndRejectsChat(t *testing.T) {
	upstream := serveModel(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	home := t.TempDir()
	writeConfig(t, home, chatConfig(upstream.URL+"/v1", "sk-rejected"))
	srv := startServe(t, home)
	defer srv.stop(t)

	health := getJSON(t, srv.baseURL()+"/api/v1/health/live")
	if health["status"] != "healthy" {
		t.Fatalf("health = %#v", health)
	}
	status, body := postJSONStatus(t, srv.baseURL()+"/api/v1/chat/sync", map[string]any{
		"message": "hello",
	})
	if status < 400 || !bytes.Contains(body, []byte("401")) && !bytes.Contains(body, []byte("unauthorized")) && !bytes.Contains(body, []byte("chat failed")) {
		t.Fatalf("chat with rejected provider status=%d body=%s", status, body)
	}
	sessions, err := filepath.Glob(filepath.Join(home, ".luckyagent", "sessions", "*", "messages.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range sessions {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(`"role":"assistant"`)) {
			t.Fatalf("assistant message persisted after failed chat: %s", data)
		}
	}
}

type runningServer struct {
	cmd  *exec.Cmd
	addr string
	out  *lockedBuffer
}

func (s *runningServer) baseURL() string {
	return "http://" + s.addr
}

func (s *runningServer) stop(t *testing.T) {
	t.Helper()
	if s.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		<-done
		t.Errorf("server did not exit after interrupt\n%s", s.out.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", s.addr, 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("port %s still accepts connections", s.addr)
}

func startServe(t *testing.T, home string) *runningServer {
	t.Helper()
	port := freePort(t)
	cmd := exec.Command(lhBinary(t), "serve", "--addr", "127.0.0.1:"+port)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"TMPDIR=" + os.TempDir(),
	}
	out := &lockedBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	srv := &runningServer{cmd: cmd, addr: "127.0.0.1:" + port, out: out}
	deadline := time.Now().Add(30 * time.Second)
	healthURL := srv.baseURL() + "/api/v1/health/live"
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			t.Fatalf("server exited early\n%s", out.String())
		}
		resp, err := http.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return srv
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	t.Fatalf("server did not become healthy\n%s", out.String())
	return nil
}

func dumpServer(t *testing.T, srv *runningServer) {
	t.Helper()
	if srv != nil && t.Failed() {
		t.Logf("server output:\n%s", srv.out.String())
	}
}

func chatConfig(baseURL, apiKey string) map[string]any {
	return map[string]any{
		"llm_provider": map[string]any{
			"name":     "openai",
			"api_key":  apiKey,
			"base_url": baseURL,
			"model":    "e2e-model",
			"protocol": "chat_completions",
		},
		"agent":     map[string]any{"auto_approve": true},
		"proactive": map[string]any{"enabled": false},
		"rag":       map[string]any{"top_k": 0},
	}
}

func writeConfig(t *testing.T, home string, values map[string]any) {
	t.Helper()
	home = filepath.Join(home, ".luckyagent")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("{}\n")
	if values != nil {
		var err error
		body, err = json.MarshalIndent(values, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

type modelServer struct {
	URL string
}

func serveModel(t *testing.T, handler http.HandlerFunc) *modelServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return &modelServer{URL: "http://" + ln.Addr().String()}
}

func requestBody(r *http.Request) []byte {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body
}

func completionReviewRequest(r *http.Request) bool {
	return bytes.Contains(requestBody(r), []byte("Review whether the original task"))
}

func sessionRecapRequest(r *http.Request) bool {
	body := requestBody(r)
	// A finished turn queues an asynchronous session recap. It has no tools and
	// must not be counted as the answer or the independent completion review.
	return bytes.Contains(body, []byte("Write exactly one concise sentence to help the user recall")) &&
		!bytes.Contains(body, []byte(`"tools"`))
}

func writeCompletion(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{
			"index":         0,
			"finish_reason": "stop",
			"message":       map[string]any{"role": "assistant", "content": content},
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

func writeToolCompletion(w http.ResponseWriter, id, name, arguments string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{
			"index":         0,
			"finish_reason": "tool_calls",
			"message": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []any{map[string]any{
					"id":   id,
					"type": "function",
					"function": map[string]any{
						"name":      name,
						"arguments": arguments,
					},
				}},
			},
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", url, resp.StatusCode, body)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s: %v body=%s", url, err, body)
	}
	return decoded
}

func postJSON(t *testing.T, url string, payload any) map[string]any {
	t.Helper()
	status, body := postJSONStatus(t, url, payload)
	if status != http.StatusOK {
		t.Fatalf("POST %s status=%d body=%s", url, status, body)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s: %v body=%s", url, err, body)
	}
	return decoded
}

func postJSONStatus(t *testing.T, url string, payload any) (int, []byte) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func readSessionMessages(t *testing.T, home, sessionID string) []map[string]any {
	t.Helper()
	path := filepath.Join(home, ".luckyagent", "sessions", sessionID, "messages.jsonl")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open session %s: %v", path, err)
	}
	defer file.Close()
	var messages []map[string]any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var message map[string]any
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatalf("decode session line: %v", err)
		}
		messages = append(messages, message)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return messages
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func lhBinary(t *testing.T) string {
	t.Helper()
	path := os.Getenv("LH_E2E_BIN")
	if path == "" {
		t.Fatal("LH_E2E_BIN is empty")
	}
	return path
}

func repoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	if filepath.Base(wd) == "e2e" {
		return filepath.Dir(wd)
	}
	return wd
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
