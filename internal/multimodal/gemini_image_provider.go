package multimodal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var geminiDataURLRe = regexp.MustCompile(`data:(image/[-+.\w]+);base64,([A-Za-z0-9+/=]+)`)

type GeminiImageConfig struct {
	APIKey   string
	APIBase  string
	AuthMode string
}

type GeminiImageProvider struct {
	apiKey   string
	apiBase  string
	authMode string
	client   *http.Client
}

func NewGeminiImageProvider(cfg GeminiImageConfig) (*GeminiImageProvider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("gemini image provider requires api key")
	}
	if strings.TrimSpace(cfg.APIBase) == "" {
		return nil, fmt.Errorf("gemini image provider requires api base")
	}
	authMode := strings.ToLower(strings.TrimSpace(cfg.AuthMode))
	if authMode == "" {
		authMode = "bearer"
	}
	return &GeminiImageProvider{
		apiKey:   strings.TrimSpace(cfg.APIKey),
		apiBase:  strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/"),
		authMode: authMode,
		client: &http.Client{
			Timeout: 90 * time.Second,
		},
	}, nil
}

func (g *GeminiImageProvider) Name() string {
	return "gemini-image"
}

func (g *GeminiImageProvider) GenerateImage(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("image generation prompt is required")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "gemini-3.1-flash-image-preview"
	}

	parts := []map[string]any{
		{"text": buildGeminiPrompt(req)},
	}
	for _, image := range req.InputImages {
		if len(image.Data) == 0 {
			continue
		}
		mimeType := strings.TrimSpace(image.MimeType)
		if mimeType == "" {
			mimeType = "image/png"
		}
		parts = append(parts, map[string]any{
			"inline_data": map[string]any{
				"mime_type": mimeType,
				"data":      base64.StdEncoding.EncodeToString(image.Data),
			},
		})
	}

	reqBody := map[string]any{
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": parts,
			},
		},
		"generationConfig": map[string]any{
			"responseModalities": []string{"TEXT", "IMAGE"},
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini image request: %w", err)
	}

	endpoint := g.apiBase + "/models/" + url.PathEscape(model) + ":generateContent"
	if g.authMode == "query" {
		if strings.Contains(endpoint, "?") {
			endpoint += "&key=" + url.QueryEscape(g.apiKey)
		} else {
			endpoint += "?key=" + url.QueryEscape(g.apiKey)
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create gemini image request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", defaultOpenAIUserAgent)
	switch g.authMode {
	case "x-goog-api-key":
		httpReq.Header.Set("x-goog-api-key", g.apiKey)
	case "bearer":
		httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)
	}

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send gemini image request: %w", err)
	}
	defer resp.Body.Close()

	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		return nil, fmt.Errorf("read gemini image response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gemini image api error %d: %s", resp.StatusCode, strings.TrimSpace(body.String()))
	}

	return parseGeminiGeneratedImages(body.Bytes(), model)
}

func buildGeminiPrompt(req ImageGenerationRequest) string {
	prompt := strings.TrimSpace(req.Prompt)
	var hints []string
	if size := strings.TrimSpace(req.Size); size != "" {
		hints = append(hints, "Target size "+size+".")
	}
	if quality := strings.TrimSpace(req.Quality); quality != "" && quality != "auto" {
		hints = append(hints, "Quality "+quality+".")
	}
	if background := strings.TrimSpace(req.Background); background != "" && background != "auto" {
		hints = append(hints, "Background "+background+".")
	}
	if format := strings.TrimSpace(req.OutputFormat); format != "" {
		hints = append(hints, "Preferred output format "+format+".")
	}
	if len(hints) == 0 {
		return prompt
	}
	return prompt + "\n\n" + strings.Join(hints, " ")
}

func parseGeminiGeneratedImages(body []byte, model string) (*ImageGenerationResult, error) {
	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text       string `json:"text"`
					InlineData *struct {
						MimeType string `json:"mime_type"`
						Data     string `json:"data"`
					} `json:"inlineData"`
					InlineDataAlt *struct {
						MimeType string `json:"mime_type"`
						Data     string `json:"data"`
					} `json:"inline_data"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode gemini image response: %w", err)
	}

	result := &ImageGenerationResult{
		Provider: "gemini-image",
		Model:    model,
		Metadata: map[string]string{
			"source": "gemini-generate-content",
		},
		CreatedAt: time.Now().UTC(),
	}

	for _, candidate := range payload.Candidates {
		for _, part := range candidate.Content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" && result.RevisedPrompt == "" {
				result.RevisedPrompt = text
			}
			if part.InlineData != nil {
				image, err := decodeGeminiInlineImage(part.InlineData.MimeType, part.InlineData.Data)
				if err != nil {
					return nil, err
				}
				result.Images = append(result.Images, image)
			}
			if part.InlineDataAlt != nil {
				image, err := decodeGeminiInlineImage(part.InlineDataAlt.MimeType, part.InlineDataAlt.Data)
				if err != nil {
					return nil, err
				}
				result.Images = append(result.Images, image)
			}
			if strings.TrimSpace(part.Text) != "" {
				matches := geminiDataURLRe.FindAllStringSubmatch(part.Text, -1)
				for _, match := range matches {
					image, err := decodeGeminiInlineImage(match[1], match[2])
					if err != nil {
						return nil, err
					}
					result.Images = append(result.Images, image)
				}
			}
		}
	}

	if len(result.Images) == 0 {
		return nil, fmt.Errorf("gemini image api returned no images")
	}
	return result, nil
}

func decodeGeminiInlineImage(mimeType, data string) (GeneratedImage, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
	if err != nil {
		return GeneratedImage{}, fmt.Errorf("decode gemini generated image: %w", err)
	}
	return GeneratedImage{
		Data:     raw,
		MimeType: strings.TrimSpace(mimeType),
	}, nil
}
