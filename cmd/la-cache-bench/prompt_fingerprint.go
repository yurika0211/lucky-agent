package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"

	"github.com/yurika0211/luckyagent/internal/contextx"
)

type promptFingerprint struct {
	Hash            string
	Bytes           int
	EstimatedTokens int
}

type captureRequest struct {
	Messages []captureMessage `json:"messages"`
}

type captureMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func aggregatePromptFingerprint(prefixes []string) promptFingerprint {
	var blocks []string
	for _, prefix := range prefixes {
		text := readSystemPromptText(prefix + ".request.json")
		if strings.TrimSpace(text) != "" {
			blocks = append(blocks, text)
		}
	}
	if len(blocks) == 0 {
		return promptFingerprint{}
	}
	joined := strings.Join(blocks, "\n\n--- request boundary ---\n\n")
	sum := sha256.Sum256([]byte(joined))
	return promptFingerprint{
		Hash:            hex.EncodeToString(sum[:])[:16],
		Bytes:           len([]byte(joined)),
		EstimatedTokens: contextx.NewTokenEstimator(0).Estimate(joined),
	}
}

func readSystemPromptText(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var req captureRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return ""
	}
	var parts []string
	for _, msg := range req.Messages {
		if strings.TrimSpace(strings.ToLower(msg.Role)) != "system" {
			continue
		}
		text := captureContentText(msg.Content)
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func captureContentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := m["text"].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}
