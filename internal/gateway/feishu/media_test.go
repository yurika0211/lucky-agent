package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

func TestMediaDeliveryGuidanceIsIdempotent(t *testing.T) {
	got := MediaDeliveryGuidance("create a report")
	if !strings.Contains(got, mediaDeliveryRuleMarker) || !strings.Contains(got, "media is disabled") {
		t.Fatalf("missing Feishu delivery guidance: %q", got)
	}
	again := MediaDeliveryGuidance(got)
	if strings.Count(again, mediaDeliveryRuleMarker) != 1 {
		t.Fatalf("guidance was duplicated: %q", again)
	}
	enabled := MediaDeliveryGuidance("create a report", true)
	if !strings.Contains(enabled, "can receive image and document attachments") || strings.Contains(enabled, "media is disabled") {
		t.Fatalf("media-enabled guidance is incorrect: %q", enabled)
	}
}

func TestDispatchMessageRepliesWhenMediaIsDisabled(t *testing.T) {
	var replyBody sendMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/im/v1/messages/om_image/reply":
			_ = json.NewDecoder(r.Body).Decode(&replyBody)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_notice"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := apiTestAdapter(server)
	called := false
	a.SetHandler(func(context.Context, *gateway.Message) error {
		called = true
		return nil
	})
	msg, err := a.convertEvent(messageEvent{
		Sender:  eventSender{SenderID: eventUserID{OpenID: "ou_sender"}, SenderType: "user"},
		Message: eventMessage{MessageID: "om_image", ChatID: "oc_chat", ChatType: "p2p", MessageType: "image", Content: `{"image_key":"img_test"}`},
	})
	if err != nil {
		t.Fatalf("convertEvent: %v", err)
	}
	if err := a.dispatchMessage(context.Background(), msg, "image"); err != nil {
		t.Fatalf("dispatchMessage: %v", err)
	}
	if called {
		t.Fatal("handler received an image while media support was disabled")
	}
	var content map[string]string
	if replyBody.MsgType != "text" || json.Unmarshal([]byte(replyBody.Content), &content) != nil || !strings.Contains(content["text"], "目前仅支持文本") {
		t.Fatalf("unexpected media notice request: %#v, content=%#v", replyBody, content)
	}
}

func TestDownloadInboundAttachmentSavesPrivateFileUnderDataDir(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\nfixture-image")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/im/v1/messages/om_image/resources/img_test":
			if r.URL.Query().Get("type") != "image" {
				t.Errorf("resource type = %q", r.URL.Query().Get("type"))
			}
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := apiTestAdapter(server)
	a.cfg.DataDir = t.TempDir()
	path, size, mimeType, err := a.downloadInboundAttachment(context.Background(), "om_image", gateway.Attachment{
		Type: gateway.AttachmentImage, FileID: "img_test", FileName: "../unsafe.png", Metadata: map[string]string{"resource_type": "image"},
	})
	if err != nil {
		t.Fatalf("downloadInboundAttachment: %v", err)
	}
	if !strings.HasPrefix(path, filepath.Join(a.cfg.DataDir, "inbound")+string(os.PathSeparator)) || size != int64(len(data)) || !strings.HasPrefix(mimeType, "image/") {
		t.Fatalf("unexpected attachment result path=%q size=%d mime=%q", path, size, mimeType)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("saved attachment = %q, %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("attachment mode = %o, want 600", info.Mode().Perm())
	}
}

func TestDownloadInboundAttachmentRejectsConfiguredLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/im/v1/messages/om_file/resources/file_test":
			_, _ = io.WriteString(w, strings.Repeat("x", 32))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := apiTestAdapter(server)
	a.cfg.MaxMediaBytes = 8
	a.cfg.DataDir = t.TempDir()
	_, _, _, err := a.downloadInboundAttachment(context.Background(), "om_file", gateway.Attachment{
		Type: gateway.AttachmentDocument, FileID: "file_test", FileName: "notes.txt", Metadata: map[string]string{"resource_type": "file"},
	})
	if err == nil || !strings.Contains(err.Error(), "configured limit") {
		t.Fatalf("expected configured size limit error, got %v", err)
	}
}

func TestSendPhotoUploadsAndSendsImage(t *testing.T) {
	image := []byte("\x89PNG\r\n\x1a\nfixture-image")
	var imageSent bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/im/v1/images":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatalf("MultipartReader: %v", err)
			}
			fields := map[string]string{}
			var upload []byte
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				value, _ := io.ReadAll(part)
				if part.FormName() == "image_type" {
					fields[part.FormName()] = string(value)
				} else if part.FormName() == "image" {
					upload = value
				}
			}
			if fields["image_type"] != "message" || !bytes.Equal(upload, image) {
				t.Errorf("unexpected image upload fields=%v bytes=%q", fields, upload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"image_key": "img_uploaded"}})
		case "/open-apis/im/v1/messages/om_source/reply":
			var request sendMessageRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			var content map[string]string
			if request.MsgType != "image" || json.Unmarshal([]byte(request.Content), &content) != nil || content["image_key"] != "img_uploaded" {
				t.Errorf("unexpected image message %#v content=%#v", request, content)
			}
			imageSent = true
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_image_sent"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	localPath := filepath.Join(t.TempDir(), "generated.png")
	if err := os.WriteFile(localPath, image, 0o600); err != nil {
		t.Fatal(err)
	}
	a := apiTestAdapter(server)
	a.cfg.MediaEnabled = true
	if err := a.SendPhoto(context.Background(), "oc_chat", "om_source", localPath, ""); err != nil {
		t.Fatalf("SendPhoto: %v", err)
	}
	if !imageSent {
		t.Fatal("uploaded image was not sent as a message")
	}
}

func TestSendPhotoReportsUploadFailureToChat(t *testing.T) {
	var failureNotice string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/im/v1/images":
			http.Error(w, `{"code":234006,"msg":"file size exceed max value"}`, http.StatusBadRequest)
		case "/open-apis/im/v1/messages/om_source/reply":
			var request sendMessageRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			var content map[string]string
			_ = json.Unmarshal([]byte(request.Content), &content)
			failureNotice = content["text"]
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_notice"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	localPath := filepath.Join(t.TempDir(), "generated.png")
	if err := os.WriteFile(localPath, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := apiTestAdapter(server)
	a.cfg.MediaEnabled = true
	if err := a.SendPhoto(context.Background(), "oc_chat", "om_source", localPath, ""); err == nil {
		t.Fatal("SendPhoto should return upload error")
	}
	if !strings.Contains(failureNotice, "图片") || !strings.Contains(failureNotice, "附件超过大小限制") {
		t.Fatalf("failure notice = %q", failureNotice)
	}
}

func TestOutboundMediaRequestUsesMultipartContentType(t *testing.T) {
	var contentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
			return
		}
		contentType = r.Header.Get("Content-Type")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		_, _ = io.Copy(io.Discard, r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"image_key": "img"}})
	}))
	defer server.Close()
	a := apiTestAdapter(server)
	var response feishuMediaUploadResponse
	if err := a.uploadMedia(context.Background(), "/open-apis/im/v1/images", map[string]string{"image_type": "message"}, "image", "x.png", []byte("image"), &response); err != nil {
		t.Fatalf("uploadMedia: %v", err)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data; boundary=") {
		t.Fatalf("Content-Type = %q", contentType)
	}
}

func TestReadOutboundMediaAllowsWindowsDrivePaths(t *testing.T) {
	if !isWindowsDrivePath(`C:\LuckyAgent\generated.png`) || !isWindowsDrivePath(`D:/LuckyAgent/generated.png`) {
		t.Fatal("Windows drive paths were not recognized")
	}
	if isWindowsDrivePath(`/tmp/generated.png`) || isWindowsDrivePath(`https://example.test/image.png`) {
		t.Fatal("non-Windows paths were incorrectly recognized")
	}
}
