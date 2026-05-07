package telegram

import (
	"context"
	"github.com/yurika0211/luckyharness/internal/gateway"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type telegramMethodRecorder struct {
	mu      sync.Mutex
	methods []string
}

func (r *telegramMethodRecorder) add(method string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.methods = append(r.methods, method)
}

func (r *telegramMethodRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.methods))
	copy(out, r.methods)
	return out
}

func newCaptureBotAdapter(t *testing.T) (*Adapter, func(), *telegramMethodRecorder) {
	t.Helper()

	recorder := &telegramMethodRecorder{}
	bot, err := newMockBot(func(r *http.Request) map[string]any {
		switch {
		case containsMethod(r.URL.Path, "getMe"):
			return map[string]any{
				"ok": true,
				"result": map[string]any{
					"id":         123456789,
					"is_bot":     true,
					"first_name": "TestBot",
					"username":   "testbot",
				},
			}
		case containsMethod(r.URL.Path, "sendMessage"):
			recorder.add("sendMessage")
			return map[string]any{
				"ok": true,
				"result": map[string]any{
					"message_id": 42,
					"chat": map[string]any{
						"id": 12345,
					},
					"text": "ok",
				},
			}
		case containsMethod(r.URL.Path, "sendPhoto"):
			recorder.add("sendPhoto")
			return map[string]any{
				"ok": true,
				"result": map[string]any{
					"message_id": 43,
					"chat": map[string]any{
						"id": 12345,
					},
				},
			}
		case containsMethod(r.URL.Path, "sendDocument"):
			recorder.add("sendDocument")
			return map[string]any{
				"ok": true,
				"result": map[string]any{
					"message_id": 44,
					"chat": map[string]any{
						"id": 12345,
					},
				},
			}
		default:
			return defaultMockBotResponse(r)
		}
	})
	if err != nil {
		t.Fatalf("create mock bot: %v", err)
	}

	adapter := NewAdapter(Config{Token: bot.Token})
	adapter.bot = bot
	adapter.botUsername = "testbot"
	adapter.running = true
	cleanup := func() {}
	return adapter, cleanup, recorder
}

func TestParseOutboundMediaResponseDirective(t *testing.T) {
	text, media := parseOutboundMediaResponse("Here is the report\n\ntg://document /tmp/report.pdf monthly report")
	if text != "Here is the report" {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(media) != 1 {
		t.Fatalf("expected 1 media item, got %d", len(media))
	}
	if media[0].Kind != outboundMediaDocument {
		t.Fatalf("expected document media, got %q", media[0].Kind)
	}
	if media[0].Source != "/tmp/report.pdf" {
		t.Fatalf("unexpected source: %q", media[0].Source)
	}
	if media[0].Caption != "monthly report" {
		t.Fatalf("unexpected caption: %q", media[0].Caption)
	}
}

func TestParseOutboundMediaResponseMarkdownImage(t *testing.T) {
	text, media := parseOutboundMediaResponse("Take a look:\n\n![trend](https://example.com/trend.png)")
	if text != "Take a look:" {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(media) != 1 || media[0].Kind != outboundMediaPhoto {
		t.Fatalf("expected one photo media item, got %#v", media)
	}
	if media[0].Caption != "trend" {
		t.Fatalf("unexpected caption: %q", media[0].Caption)
	}
}

func TestParseOutboundMediaResponseMarkdownSandboxLink(t *testing.T) {
	text, media := parseOutboundMediaResponse("PNG 在这里：[quadratic.png](sandbox:/tmp/quadratic.png)")
	if text != "PNG 在这里：" {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(media) != 1 || media[0].Kind != outboundMediaPhoto {
		t.Fatalf("expected one photo media item, got %#v", media)
	}
	if media[0].Source != "sandbox:/tmp/quadratic.png" {
		t.Fatalf("unexpected source: %q", media[0].Source)
	}
}

func TestParseOutboundMediaResponseImplicitDocument(t *testing.T) {
	text, media := parseOutboundMediaResponse("/tmp/output.pdf")
	if text != "" {
		t.Fatalf("expected empty text, got %q", text)
	}
	if len(media) != 1 || media[0].Kind != outboundMediaDocument {
		t.Fatalf("expected implicit document media, got %#v", media)
	}
}

func TestResolveOutboundMediaResponseMediaTag(t *testing.T) {
	text, media, err := resolveOutboundMediaResponse("已生成文件\nMEDIA:/tmp/report.pdf")
	if err != nil {
		t.Fatalf("resolveOutboundMediaResponse: %v", err)
	}
	if text != "已生成文件" {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(media) != 1 || media[0].Kind != outboundMediaDocument {
		t.Fatalf("expected MEDIA tag to resolve to document, got %#v", media)
	}
}

func TestExtractLocalFilesIgnoresCodeBlocksAndInlineCode(t *testing.T) {
	tmpDir := t.TempDir()
	realPath := filepath.Join(tmpDir, "real.png")
	if err := os.WriteFile(realPath, []byte("png"), 0600); err != nil {
		t.Fatalf("write real path: %v", err)
	}

	content := "这个先发给你：" + realPath + "\n\n```bash\ncat " + realPath + "\n```\n还有 `" + realPath + "` 不要当附件。"
	text, media, err := extractLocalFiles(content)
	if err != nil {
		t.Fatalf("extractLocalFiles: %v", err)
	}
	if !strings.Contains(text, "这个先发给你：") {
		t.Fatalf("expected explanatory text, got %q", text)
	}
	if !strings.Contains(text, "```bash") {
		t.Fatalf("code block should remain untouched in text, got %q", text)
	}
	if len(media) != 1 || media[0].Source != realPath {
		t.Fatalf("expected exactly one extracted media file, got %#v", media)
	}
}

func TestExtractLocalFilesSandboxPath(t *testing.T) {
	tmpDir := t.TempDir()
	realPath := filepath.Join(tmpDir, "chart.png")
	if err := os.WriteFile(realPath, []byte("png"), 0600); err != nil {
		t.Fatalf("write sandbox file: %v", err)
	}
	content := "文件在这里 sandbox:" + realPath
	text, media, err := extractLocalFiles(content)
	if err != nil {
		t.Fatalf("extractLocalFiles: %v", err)
	}
	if text != "文件在这里" {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(media) != 1 || media[0].Source != "sandbox:"+realPath {
		t.Fatalf("expected sandbox path media, got %#v", media)
	}
}

func TestSendAssistantResponsePhotoDirective(t *testing.T) {
	adapter, cleanup, recorder := newCaptureBotAdapter(t)
	defer cleanup()

	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "chart.png")
	if err := os.WriteFile(imagePath, []byte("fake image data"), 0600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}

	handler := NewHandler(adapter, nil)
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
	}

	response := "Here is the chart\n\ntg://photo " + imagePath + " chart"
	if err := handler.sendAssistantResponse(context.Background(), msg, response); err != nil {
		t.Fatalf("sendAssistantResponse: %v", err)
	}

	methods := strings.Join(recorder.snapshot(), ",")
	if methods != "sendMessage,sendPhoto" {
		t.Fatalf("unexpected send sequence: %s", methods)
	}
}

func TestSendAssistantResponseImplicitDocument(t *testing.T) {
	adapter, cleanup, recorder := newCaptureBotAdapter(t)
	defer cleanup()

	tmpDir := t.TempDir()
	docPath := filepath.Join(tmpDir, "report.pdf")
	if err := os.WriteFile(docPath, []byte("%PDF-1.4"), 0600); err != nil {
		t.Fatalf("write temp doc: %v", err)
	}

	handler := NewHandler(adapter, nil)
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
	}

	if err := handler.sendAssistantResponse(context.Background(), msg, docPath); err != nil {
		t.Fatalf("sendAssistantResponse: %v", err)
	}

	methods := recorder.snapshot()
	if len(methods) != 1 || methods[0] != "sendDocument" {
		t.Fatalf("unexpected methods: %#v", methods)
	}
}

func TestSendAssistantResponseRandomMeme(t *testing.T) {
	adapter, cleanup, recorder := newCaptureBotAdapter(t)
	defer cleanup()

	tmpDir := t.TempDir()
	memePath := filepath.Join(tmpDir, "meme.png")
	if err := os.WriteFile(memePath, []byte("fake image data"), 0600); err != nil {
		t.Fatalf("write meme file: %v", err)
	}

	handler := NewHandler(adapter, nil)
	handler.memeDir = tmpDir
	handler.memeProbability = 1
	handler.memeCooldown = 0
	handler.memeRand = rand.New(rand.NewSource(1))
	handler.memeNow = func() time.Time { return time.Unix(1000, 0) }

	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
	}

	if err := handler.sendAssistantResponse(context.Background(), msg, "搞定了，已经处理完成"); err != nil {
		t.Fatalf("sendAssistantResponse: %v", err)
	}

	methods := strings.Join(recorder.snapshot(), ",")
	if methods != "sendMessage,sendPhoto" {
		t.Fatalf("unexpected send sequence: %s", methods)
	}
}

func TestSendAssistantResponseRandomMemeRespectsCooldown(t *testing.T) {
	adapter, cleanup, recorder := newCaptureBotAdapter(t)
	defer cleanup()

	tmpDir := t.TempDir()
	memePath := filepath.Join(tmpDir, "meme.png")
	if err := os.WriteFile(memePath, []byte("fake image data"), 0600); err != nil {
		t.Fatalf("write meme file: %v", err)
	}

	currentTime := time.Unix(1000, 0)
	handler := NewHandler(adapter, nil)
	handler.memeDir = tmpDir
	handler.memeProbability = 1
	handler.memeCooldown = 10 * time.Minute
	handler.memeRand = rand.New(rand.NewSource(1))
	handler.memeNow = func() time.Time { return currentTime }

	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
	}

	if err := handler.sendAssistantResponse(context.Background(), msg, "搞定了，已经处理完成"); err != nil {
		t.Fatalf("first sendAssistantResponse: %v", err)
	}
	if err := handler.sendAssistantResponse(context.Background(), msg, "搞定了，再次完成"); err != nil {
		t.Fatalf("second sendAssistantResponse: %v", err)
	}

	methods := strings.Join(recorder.snapshot(), ",")
	if methods != "sendMessage,sendPhoto,sendMessage" {
		t.Fatalf("unexpected send sequence with cooldown: %s", methods)
	}
}
