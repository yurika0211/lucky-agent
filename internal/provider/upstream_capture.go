package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

var upstreamCaptureSeq atomic.Uint64

type upstreamCapture struct {
	enabled bool
	prefix  string
}

func newUpstreamCapture(kind string, cfg Config, requestBody []byte) *upstreamCapture {
	dir := strings.TrimSpace(os.Getenv("LH_UPSTREAM_CAPTURE_DIR"))
	if dir == "" {
		return &upstreamCapture{}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return &upstreamCapture{}
	}

	seq := upstreamCaptureSeq.Add(1)
	now := time.Now()
	id := fmt.Sprintf("%s_%06d_%s", now.Format("20060102_150405.000"), seq, kind)
	prefix := filepath.Join(dir, id)

	meta := map[string]any{
		"captured_at": now.Format(time.RFC3339Nano),
		"kind":        kind,
		"LlmProvider": LlmProvider{
			Name:        cfg.LlmProvider.Name,
			APIKey:      cfg.LlmProvider.APIKey,
			BaseURL:     cfg.LlmProvider.BaseURL,
			Model:       cfg.LlmProvider.Model,
			Temperature: cfg.LlmProvider.Temperature,
		},
	}
	_ = writeJSON(prefix+".meta.json", meta)
	_ = os.WriteFile(prefix+".request.json", requestBody, 0o600)

	return &upstreamCapture{
		enabled: true,
		prefix:  prefix,
	}
}

func (c *upstreamCapture) writeError(stage string, err error) {
	if c == nil || !c.enabled || err == nil {
		return
	}
	_ = os.WriteFile(
		c.prefix+".error.txt",
		[]byte(fmt.Sprintf("stage=%s\nerror=%v\n", stage, err)),
		0o600,
	)
}

func (c *upstreamCapture) writeResponseMeta(status int, header http.Header) {
	if c == nil || !c.enabled {
		return
	}
	meta := map[string]any{
		"status_code": status,
		"headers":     header,
	}
	_ = writeJSON(c.prefix+".response.meta.json", meta)
}

func (c *upstreamCapture) writeResponseBody(body []byte) {
	if c == nil || !c.enabled {
		return
	}
	_ = os.WriteFile(c.prefix+".response.body.txt", body, 0o600)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
