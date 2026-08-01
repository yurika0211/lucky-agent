package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/yurika0211/luckyagent/internal/contextx"
)

type promptFingerprint struct {
	Hash            string
	Bytes           int
	EstimatedTokens int
}

type captureRequest struct {
	Messages []map[string]any `json:"messages"`
}

type promptSnapshot struct {
	Hash            string
	EstimatedTokens int
	Messages        [][]byte
	MessageTokens   []int
}

type prefixStability struct {
	Messages int
	Tokens   int
	Ratio    float64
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
		role, _ := msg["role"].(string)
		if strings.TrimSpace(strings.ToLower(role)) != "system" {
			continue
		}
		text := captureContentText(msg["content"])
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func aggregatePromptSnapshot(prefixes []string) promptSnapshot {
	if len(prefixes) == 0 {
		return promptSnapshot{}
	}
	ordered := append([]string(nil), prefixes...)
	sort.Strings(ordered)
	return readPromptSnapshot(ordered[0] + ".request.json")
}

func readPromptSnapshot(path string) promptSnapshot {
	data, err := os.ReadFile(path)
	if err != nil {
		return promptSnapshot{}
	}
	var req captureRequest
	if err := json.Unmarshal(data, &req); err != nil || len(req.Messages) == 0 {
		return promptSnapshot{}
	}

	est := contextx.NewTokenEstimator(0)
	snapshot := promptSnapshot{
		Messages:      make([][]byte, 0, len(req.Messages)),
		MessageTokens: make([]int, 0, len(req.Messages)),
	}
	h := sha256.New()
	for _, msg := range req.Messages {
		canonical, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		snapshot.Messages = append(snapshot.Messages, canonical)
		text := captureContentText(msg["content"])
		tokens := est.Estimate(text) + 4
		snapshot.MessageTokens = append(snapshot.MessageTokens, tokens)
		snapshot.EstimatedTokens += tokens
		_, _ = h.Write(canonical)
		_, _ = h.Write([]byte{'\n'})
	}
	if len(snapshot.Messages) > 0 {
		snapshot.Hash = hex.EncodeToString(h.Sum(nil))[:16]
	}
	return snapshot
}

func comparePromptPrefix(previous, current promptSnapshot) prefixStability {
	if len(previous.Messages) == 0 || len(current.Messages) == 0 {
		return prefixStability{}
	}
	limit := len(previous.Messages)
	if len(current.Messages) < limit {
		limit = len(current.Messages)
	}
	var result prefixStability
	for i := 0; i < limit; i++ {
		if !bytes.Equal(previous.Messages[i], current.Messages[i]) {
			break
		}
		result.Messages++
		if i < len(current.MessageTokens) {
			result.Tokens += current.MessageTokens[i]
		}
	}
	if current.EstimatedTokens > 0 {
		result.Ratio = float64(result.Tokens) / float64(current.EstimatedTokens)
	}
	return result
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
