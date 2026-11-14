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

var lhE2EBin string

func TestMain(m *testing.M) {
	root := repoRoot()
	dir, err := os.MkdirTemp("", "lh-e2e-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "temp dir: %v\n", err)
		os.Exit(1)
	}
	binary := filepath.Join(dir, "lh-e2e")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/la")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build lh: %v\n%s\n", err, out)
		os.Exit(1)
	}
	lhE2EBin = binary
	code := m.Run()
	_ = os.RemoveAll(dir)
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
	if status != http.StatusInternalServerError || !bytes.Contains(body, []byte("chat failed")) {
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

func TestMalformedChatJSONIsBadRequest(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, nil)
	srv := startServe(t, home)
	defer srv.stop(t)

	status, body := postRaw(t, srv.baseURL()+"/api/v1/chat/sync", "application/json", "{not json")
	if status != http.StatusBadRequest || !bytes.Contains(body, []byte("invalid JSON request body")) {
		t.Fatalf("malformed JSON status=%d body=%s", status, body)
	}
}

func TestEmptyChatMessageIsBadRequest(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, nil)
	srv := startServe(t, home)
	defer srv.stop(t)

	status, body := postJSONStatus(t, srv.baseURL()+"/api/v1/chat/sync", map[string]any{"message": ""})
	if status != http.StatusBadRequest || !bytes.Contains(body, []byte("message is required")) {
		t.Fatalf("empty message status=%d body=%s", status, body)
	}
}

func TestSSEChatStreamsComplete(t *testing.T) {
	upstream := serveModel(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if bytes.Contains(requestBody(r), []byte("Classify the user's request")) {
			writeMaybeStream(w, r, `{"profile":"chat"}`)
			return
		}
		if sessionRecapRequest(r) {
			writeMaybeStream(w, r, "You asked for the stream answer.")
			return
		}
		if completionReviewRequest(r) {
			writeMaybeStream(w, r, `{"status":"complete","reason":"the stream answer was returned","evidence":["assistant returned the stream answer"]}`)
			return
		}
		writeMaybeStream(w, r, "e2e stream answer")
	})
	home := t.TempDir()
	writeConfig(t, home, chatConfig(upstream.URL+"/v1", "sk-e2e"))
	srv := startServe(t, home)
	defer srv.stop(t)
	defer dumpServer(t, srv)

	payload, err := json.Marshal(map[string]any{"message": "say the stream answer", "auto_approve": true})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.baseURL()+"/api/v1/chat", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("SSE chat: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("SSE status=%d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type = %q", resp.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"type":"content"`)) && !bytes.Contains(body, []byte(`"type":"done"`)) {
		t.Fatalf("missing content/done frame: %s", body)
	}
	if !bytes.Contains(body, []byte(`"type":"complete"`)) {
		t.Fatalf("missing complete frame: %s", body)
	}
}

func TestShutdownEndsInFlightChat(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var startedOnce, releaseOnce sync.Once
	startEntered := func() { startedOnce.Do(func() { close(entered) }) }
	unblockUpstream := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblockUpstream)
	upstream := serveModel(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		startEntered()
		select {
		case <-release:
		case <-r.Context().Done():
		}
		writeMaybeStream(w, r, "late")
	})
	home := t.TempDir()
	writeConfig(t, home, chatConfig(upstream.URL+"/v1", "sk-e2e"))
	srv := startServe(t, home)
	t.Cleanup(func() {
		if srv.cmd.Process == nil {
			return
		}
		_ = syscall.Kill(-srv.cmd.Process.Pid, syscall.SIGKILL)
		_ = srv.cmd.Wait()
	})

	payload, err := json.Marshal(map[string]any{"message": "hang please", "auto_approve": true})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.baseURL()+"/api/v1/chat", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	done := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 20 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			done <- err
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		done <- nil
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		unblockUpstream()
		t.Fatal("in-flight chat did not reach upstream")
	}
	if srv.cmd.Process == nil {
		t.Fatal("server process missing")
	}
	if err := syscall.Kill(-srv.cmd.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatalf("interrupt server: %v", err)
	}
	// Graceful shutdown waits for the in-flight handler. Unblock the
	// fixture so that handler can finish; the client and process must then return.
	unblockUpstream()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("in-flight chat hung after shutdown")
	}
	waited := make(chan error, 1)
	go func() { waited <- srv.cmd.Wait() }()
	select {
	case <-waited:
	case <-time.After(8 * time.Second):
		_ = syscall.Kill(-srv.cmd.Process.Pid, syscall.SIGKILL)
		<-waited
		t.Fatal("server did not exit after in-flight chat drained")
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

func streamRequest(r *http.Request) bool {
	return bytes.Contains(requestBody(r), []byte(`"stream":true`))
}

func writeMaybeStream(w http.ResponseWriter, r *http.Request, content string) {
	if streamRequest(r) {
		writeStreamCompletion(w, content)
		return
	}
	writeCompletion(w, content)
}

func writeStreamCompletion(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	payload, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{"content": content},
		}},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", payload)
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func postRaw(t *testing.T, url, contentType, body string) (int, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(url, contentType, strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
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
	if lhE2EBin == "" {
		t.Fatal("lh e2e binary is empty")
	}
	return lhE2EBin
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
