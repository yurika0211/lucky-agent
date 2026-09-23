package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

const (
	feishuMaxImageUploadBytes = 10 * 1024 * 1024
	feishuMaxFileUploadBytes  = 30 * 1024 * 1024
	feishuMaxResourceBytes    = 100 * 1024 * 1024
)

var safeMediaName = regexp.MustCompile(`[^\pL\pN._-]+`)

type feishuMediaUploadResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		ImageKey string `json:"image_key"`
		FileKey  string `json:"file_key"`
	} `json:"data"`
}

func (a *Adapter) downloadInboundAttachment(ctx context.Context, messageID string, attachment gateway.Attachment) (string, int64, string, error) {
	if strings.TrimSpace(a.cfg.DataDir) == "" {
		return "", 0, "", fmt.Errorf("feishu: media data directory is not configured")
	}
	resourceType := "file"
	if attachment.Type == gateway.AttachmentImage {
		resourceType = "image"
	}
	if attachment.Metadata != nil && strings.TrimSpace(attachment.Metadata["resource_type"]) != "" {
		resourceType = strings.TrimSpace(attachment.Metadata["resource_type"])
	}
	data, err := a.downloadMessageResource(ctx, messageID, attachment.FileID, resourceType, a.cfg.normalizedMaxMediaBytes())
	if err != nil {
		return "", 0, "", err
	}
	if len(data) == 0 {
		return "", 0, "", fmt.Errorf("feishu: downloaded attachment is empty")
	}
	ext := strings.ToLower(filepath.Ext(strings.ReplaceAll(attachment.FileName, "\\", "/")))
	mimeType := http.DetectContentType(data)
	if extMime := mime.TypeByExtension(ext); extMime != "" && (mimeType == "application/octet-stream" || strings.Contains(mimeType, "zip")) {
		mimeType = strings.Split(extMime, ";")[0]
	}
	if attachment.Type == gateway.AttachmentImage {
		if !strings.HasPrefix(mimeType, "image/") {
			return "", 0, "", fmt.Errorf("feishu: downloaded image has unsupported type %q", mimeType)
		}
	} else if !allowedInboundDocument(ext, mimeType) {
		return "", 0, "", fmt.Errorf("feishu: unsupported inbound file type %q", mimeType)
	}
	if ext == "" {
		ext = mediaExtensionForMime(mimeType)
	}
	name := safeMediaName.ReplaceAllString(strings.TrimSpace(attachment.FileName), "_")
	name = strings.Trim(name, "._-")
	if name == "" {
		name = "attachment" + ext
	}
	if filepath.Ext(name) == "" && ext != "" {
		name += ext
	}
	messageID = safeMediaName.ReplaceAllString(strings.TrimSpace(messageID), "_")
	if messageID == "" {
		messageID = "message"
	}
	inboundDir := filepath.Join(a.cfg.DataDir, "inbound")
	if err := os.MkdirAll(inboundDir, 0o700); err != nil {
		return "", 0, "", fmt.Errorf("feishu: create inbound media directory: %w", err)
	}
	file, err := os.CreateTemp(inboundDir, "feishu-"+messageID+"-*-"+name)
	if err != nil {
		return "", 0, "", fmt.Errorf("feishu: create inbound media file: %w", err)
	}
	filePath := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(filePath)
		return "", 0, "", fmt.Errorf("feishu: secure inbound media file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(filePath)
		return "", 0, "", fmt.Errorf("feishu: write inbound media file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(filePath)
		return "", 0, "", fmt.Errorf("feishu: close inbound media file: %w", err)
	}
	return filePath, int64(len(data)), mimeType, nil
}

func (a *Adapter) downloadMessageResource(ctx context.Context, messageID, fileKey, resourceType string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxMediaBytes
	} else if maxBytes > feishuMaxResourceBytes {
		maxBytes = feishuMaxResourceBytes
	}
	messageID = strings.TrimSpace(messageID)
	fileKey = strings.TrimSpace(fileKey)
	if messageID == "" || fileKey == "" {
		return nil, fmt.Errorf("feishu: message id and resource key are required")
	}
	if resourceType != "image" && resourceType != "file" {
		return nil, fmt.Errorf("feishu: unsupported resource type %q", resourceType)
	}
	token, err := a.ensureTenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(a.cfg.normalizedAPIBaseURL() + "/open-apis/im/v1/messages/" + url.PathEscape(messageID) + "/resources/" + url.PathEscape(fileKey))
	if err != nil {
		return nil, fmt.Errorf("feishu: build resource URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("type", resourceType)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("feishu: create resource request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feishu: download message resource: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, apiErrorFromResponse(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("feishu: read message resource: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, &feishuAPIError{HTTPStatus: http.StatusRequestEntityTooLarge, Message: "downloaded resource exceeds configured limit"}
	}
	return data, nil
}

func (a *Adapter) SendPhoto(ctx context.Context, chatID, replyToMsgID, source, caption string) error {
	if !a.cfg.MediaEnabled {
		return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, fmt.Errorf("%w: media_enabled is false", ErrUnsupportedMedia), "飞书媒体发送当前处于关闭状态，请启用 msg_gateway.feishu.media_enabled 后重试。")
	}
	data, name, err := readOutboundMedia(source, min(a.cfg.normalizedMaxMediaBytes(), int64(feishuMaxImageUploadBytes)))
	if err == nil && !isSupportedImageName(name) {
		err = fmt.Errorf("%w: unsupported image format %q", ErrUnsupportedMedia, filepath.Ext(name))
	}
	if err != nil {
		return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, err, "飞书没有成功发送这张图片："+mediaErrorLabel(err)+"。")
	}
	var uploaded feishuMediaUploadResponse
	if err := a.uploadMedia(ctx, "/open-apis/im/v1/images", map[string]string{"image_type": "message"}, "image", name, data, &uploaded); err != nil {
		return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, err, "飞书没有成功发送这张图片："+mediaErrorLabel(err)+"。")
	}
	key := strings.TrimSpace(uploaded.Data.ImageKey)
	if key == "" {
		return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, fmt.Errorf("feishu: image upload returned an empty image_key"), "飞书没有成功发送这张图片：上传接口没有返回图片标识。")
	}
	content, _ := json.Marshal(map[string]string{"image_key": key})
	if err := a.sendMediaMessage(ctx, chatID, replyToMsgID, "image", string(content)); err != nil {
		return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, err, "图片已上传，但飞书没有成功发送消息："+mediaErrorLabel(err)+"。")
	}
	if strings.TrimSpace(caption) != "" {
		if err := a.SendWithReply(ctx, chatID, replyToMsgID, caption); err != nil {
			return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, err, "图片已发送，但说明文字没有成功发送："+mediaErrorLabel(err)+"。")
		}
	}
	return nil
}

func (a *Adapter) SendDocument(ctx context.Context, chatID, replyToMsgID, source, caption string) error {
	if !a.cfg.MediaEnabled {
		return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, fmt.Errorf("%w: media_enabled is false", ErrUnsupportedMedia), "飞书媒体发送当前处于关闭状态，请启用 msg_gateway.feishu.media_enabled 后重试。")
	}
	data, name, err := readOutboundMedia(source, min(a.cfg.normalizedMaxMediaBytes(), int64(feishuMaxFileUploadBytes)))
	if err != nil {
		return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, err, "飞书没有成功发送这个文件："+mediaErrorLabel(err)+"。")
	}
	var uploaded feishuMediaUploadResponse
	fields := map[string]string{"file_type": feishuUploadFileType(filepath.Ext(name)), "file_name": name}
	if err := a.uploadMedia(ctx, "/open-apis/im/v1/files", fields, "file", name, data, &uploaded); err != nil {
		return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, err, "飞书没有成功发送这个文件："+mediaErrorLabel(err)+"。")
	}
	key := strings.TrimSpace(uploaded.Data.FileKey)
	if key == "" {
		return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, fmt.Errorf("feishu: file upload returned an empty file_key"), "飞书没有成功发送这个文件：上传接口没有返回文件标识。")
	}
	content, _ := json.Marshal(map[string]string{"file_key": key})
	if err := a.sendMediaMessage(ctx, chatID, replyToMsgID, "file", string(content)); err != nil {
		return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, err, "文件已上传，但飞书没有成功发送消息："+mediaErrorLabel(err)+"。")
	}
	if strings.TrimSpace(caption) != "" {
		if err := a.SendWithReply(ctx, chatID, replyToMsgID, caption); err != nil {
			return a.reportOutboundMediaFailure(ctx, chatID, replyToMsgID, err, "文件已发送，但说明文字没有成功发送："+mediaErrorLabel(err)+"。")
		}
	}
	return nil
}

func (a *Adapter) uploadMedia(ctx context.Context, endpointPath string, fields map[string]string, fileField, fileName string, data []byte, output *feishuMediaUploadResponse) error {
	token, err := a.ensureTenantAccessToken(ctx)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return fmt.Errorf("feishu: encode media field %s: %w", name, err)
		}
	}
	part, err := writer.CreateFormFile(fileField, filepath.Base(fileName))
	if err != nil {
		return fmt.Errorf("feishu: create media upload part: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("feishu: write media upload: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("feishu: finish media upload: %w", err)
	}
	endpoint, err := url.Parse(a.cfg.normalizedAPIBaseURL() + endpointPath)
	if err != nil {
		return fmt.Errorf("feishu: build media upload URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body.Bytes()))
	if err != nil {
		return fmt.Errorf("feishu: create media upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("feishu: upload media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return apiErrorFromResponse(resp)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("feishu: decode media upload response: %w", err)
	}
	if output.Code != 0 {
		return &feishuAPIError{HTTPStatus: resp.StatusCode, Code: output.Code, Message: output.Msg}
	}
	return nil
}

func (a *Adapter) sendMediaMessage(ctx context.Context, chatID, replyToMsgID, messageType, content string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return fmt.Errorf("feishu: chat id is required")
	}
	payload := sendMessageRequest{MsgType: messageType, Content: content}
	replyToMsgID = strings.TrimSpace(replyToMsgID)
	if replyToMsgID != "" {
		path := "/open-apis/im/v1/messages/" + url.PathEscape(replyToMsgID) + "/reply"
		var response sendMessageResponse
		if err := a.authorizedJSON(ctx, http.MethodPost, path, nil, payload, &response); err == nil {
			_, err = a.recordSentMessage(chatID, response)
			if err == nil {
				return nil
			}
		} else {
			log.Printf("[feishu] reply delivery failed chat_id=%q message_id=%q category=%s; falling back to chat send", chatID, replyToMsgID, apiErrorCategory(err))
		}
	}
	payload.ReceiveID = chatID
	var response sendMessageResponse
	query := url.Values{"receive_id_type": []string{"chat_id"}}
	if err := a.authorizedJSON(ctx, http.MethodPost, "/open-apis/im/v1/messages", query, payload, &response); err != nil {
		return fmt.Errorf("feishu: send %s message: %w", messageType, err)
	}
	_, err := a.recordSentMessage(chatID, response)
	return err
}

func (a *Adapter) reportOutboundMediaFailure(ctx context.Context, chatID, replyToMsgID string, cause error, message string) error {
	if ctx == nil || ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
	}
	if err := a.SendWithReply(ctx, chatID, replyToMsgID, message); err != nil {
		return errors.Join(cause, fmt.Errorf("feishu: report media delivery failure: %w", err))
	}
	return cause
}

func readOutboundMedia(source string, maxBytes int64) ([]byte, string, error) {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(strings.ToLower(source), "sandbox:") {
		source = strings.TrimSpace(strings.TrimPrefix(source, "sandbox:"))
	}
	if strings.HasPrefix(strings.ToLower(source), "file://") {
		parsed, err := url.Parse(source)
		if err != nil {
			return nil, "", fmt.Errorf("feishu: parse local media URL: %w", err)
		}
		source = parsed.Path
	}
	if parsed, err := url.Parse(source); err == nil && parsed.Scheme != "" && !isWindowsDrivePath(source) {
		return nil, "", fmt.Errorf("%w: only local generated files can be sent", ErrUnsupportedMedia)
	}
	file, err := os.Open(source)
	if err != nil {
		return nil, "", fmt.Errorf("feishu: open outbound media: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("feishu: inspect outbound media: %w", err)
	}
	if info.IsDir() {
		return nil, "", fmt.Errorf("%w: source is a directory", ErrUnsupportedMedia)
	}
	if info.Size() <= 0 {
		return nil, "", fmt.Errorf("%w: empty file", ErrUnsupportedMedia)
	}
	if maxBytes <= 0 || maxBytes > feishuMaxFileUploadBytes {
		maxBytes = feishuMaxFileUploadBytes
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("feishu: read outbound media: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, "", &feishuAPIError{HTTPStatus: http.StatusRequestEntityTooLarge, Message: "file exceeds upload limit"}
	}
	return data, filepath.Base(source), nil
}

func isWindowsDrivePath(path string) bool {
	return len(path) >= 3 && ((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

func isSupportedImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".tif", ".tiff", ".bmp", ".ico":
		return true
	default:
		return false
	}
}

func feishuUploadFileType(ext string) string {
	switch strings.ToLower(ext) {
	case ".opus":
		return "opus"
	case ".mp4":
		return "mp4"
	case ".pdf":
		return "pdf"
	case ".doc", ".docx":
		return "doc"
	case ".xls", ".xlsx":
		return "xls"
	case ".ppt", ".pptx":
		return "ppt"
	default:
		return "stream"
	}
}

func allowedInboundDocument(ext, mimeType string) bool {
	switch strings.ToLower(ext) {
	case ".pdf", ".txt", ".md", ".json", ".csv", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".zip", ".7z", ".rar", ".xml", ".yaml", ".yml", ".go", ".py", ".js", ".ts", ".html", ".htm":
		return true
	}
	return strings.HasPrefix(mimeType, "text/") || mimeType == "application/pdf"
}

func mediaExtensionForMime(mimeType string) string {
	exts, _ := mime.ExtensionsByType(mimeType)
	if len(exts) == 0 {
		return ".bin"
	}
	return exts[0]
}

func apiErrorFromResponse(resp *http.Response) *feishuAPIError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(body, &response) == nil && strings.TrimSpace(response.Msg) != "" {
		return &feishuAPIError{HTTPStatus: resp.StatusCode, Code: response.Code, Message: response.Msg}
	}
	return &feishuAPIError{HTTPStatus: resp.StatusCode, Message: strings.TrimSpace(string(body))}
}
