package tool_test

import (
	"encoding/json"
	"github.com/yurika0211/luckyagent-runtime/embedder"
	"github.com/yurika0211/luckyagent-runtime/memoryaestus"
	"github.com/yurika0211/luckyagent-runtime/rag"
	. "github.com/yurika0211/luckyagent-tools"
	"github.com/yurika0211/luckyagent-tools/memory"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryToolServiceRememberAndRecall(t *testing.T) {
	dir := t.TempDir()
	store, err := memoryaestus.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	svc := NewMemoryToolService(store)

	result, err := svc.HandleRemember(map[string]any{
		"content":  "用户喜欢Python",
		"category": "preference",
	})
	if err != nil {
		t.Fatalf("HandleRemember: %v", err)
	}
	if !strings.Contains(result, "已保存") {
		t.Fatalf("unexpected remember result: %q", result)
	}
	if !strings.Contains(result, dir) || !strings.Contains(result, "Markdown 记忆库") {
		t.Fatalf("expected remember result to identify markdown memory vault, got %q", result)
	}

	recall, err := svc.HandleRecall(map[string]any{"query": "Python"})
	if err != nil {
		t.Fatalf("HandleRecall: %v", err)
	}
	if !strings.Contains(recall, "Python") {
		t.Fatalf("unexpected recall result: %q", recall)
	}
	if !strings.Contains(recall, "LuckyAgent Obsidian-compatible Markdown vault") || !strings.Contains(recall, "RAG SQLite") {
		t.Fatalf("expected recall result to identify authoritative memory source, got %q", recall)
	}
}

func TestMemoryToolServiceRecallInjectsLinkedBlock(t *testing.T) {
	store, err := memoryaestus.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	target := "## 失败原因\n凭据过期\n^block-9\n\n## 无关配置\n不要把这一节注入"
	if err := store.SaveWithOptions(target, "fact", memory.TierLong, 0.9, memory.SaveOptions{}); err != nil {
		t.Fatalf("save target: %v", err)
	}
	var targetID string
	for _, entry := range store.Search("凭据过期") {
		targetID = entry.ID
		break
	}
	if err := store.SaveWithOptions("排障指向 [["+targetID+"#^block-9]]", "fact", memory.TierLong, 0.8, memory.SaveOptions{}); err != nil {
		t.Fatalf("save source: %v", err)
	}
	svc := NewMemoryToolService(store)

	recall, err := svc.HandleRecall(map[string]any{"query": "排障指向", "graph_depth": 1})
	if err != nil {
		t.Fatalf("HandleRecall: %v", err)
	}
	if !strings.Contains(recall, "凭据过期") || strings.Contains(recall, "无关配置") {
		t.Fatalf("text recall should show only the linked block, got %q", recall)
	}

	out, err := svc.HandleRecall(map[string]any{"query": "排障指向", "graph_depth": 1, "format": "json"})
	if err != nil {
		t.Fatalf("HandleRecall json: %v", err)
	}
	if !strings.Contains(out, "\"context\"") || !strings.Contains(out, "凭据过期") {
		t.Fatalf("json recall missing located context: %s", out)
	}
	if strings.Contains(out, "无关配置") && !strings.Contains(out, "\"content\"") {
		t.Fatalf("unlocated section leaked without the stored note: %s", out)
	}
	var payload struct {
		Results []struct {
			Content string `json:"content"`
			Context string `json:"context"`
			Block   string `json:"block"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	var found bool
	for _, item := range payload.Results {
		if item.Block != "block-9" {
			continue
		}
		found = true
		if !strings.Contains(item.Content, "无关配置") {
			t.Fatalf("stored content should remain the whole note: %+v", item)
		}
		if !strings.Contains(item.Context, "凭据过期") || strings.Contains(item.Context, "无关配置") {
			t.Fatalf("context should be only the block: %+v", item)
		}
	}
	if !found {
		t.Fatalf("linked block was not recalled: %s", out)
	}
}

func TestMemoryToolServiceRememberLongTerm(t *testing.T) {
	dir := t.TempDir()
	store, err := memoryaestus.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	svc := NewMemoryToolService(store)

	result, err := svc.HandleRemember(map[string]any{
		"content":   "重要密码",
		"category":  "security",
		"long_term": true,
	})
	if err != nil {
		t.Fatalf("HandleRemember: %v", err)
	}
	if !strings.Contains(result, "长期记忆") {
		t.Fatalf("unexpected remember result: %q", result)
	}
}

func TestRAGToolServiceSearchAndIndex(t *testing.T) {
	emb := embedder.NewMockEmbedder(8)
	mgr := rag.NewRAGManager(emb, rag.DefaultRAGConfig())
	svc := NewRAGToolService(mgr)

	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Demo\n\nalpha beta gamma"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	indexResult, err := svc.HandleIndex(map[string]any{"path": path})
	if err != nil {
		t.Fatalf("HandleIndex: %v", err)
	}
	if !strings.Contains(indexResult, "Indexed") {
		t.Fatalf("unexpected index result: %q", indexResult)
	}

	searchResult, err := svc.HandleSearch(map[string]any{"query": "alpha", "top_k": 3})
	if err != nil {
		t.Fatalf("HandleSearch: %v", err)
	}
	if !strings.Contains(searchResult, "alpha") && !strings.Contains(searchResult, "Demo") {
		t.Fatalf("unexpected search result: %q", searchResult)
	}
}

func TestRAGToolServiceSearchOptionsArePerCall(t *testing.T) {
	emb := embedder.NewMockEmbedder(8)
	mgr := rag.NewRAGManager(emb, rag.DefaultRAGConfig())
	mgr.UpdateRetrieverConfig(rag.RetrieverConfig{TopK: 9, MinScore: 0.1})
	svc := NewRAGToolService(mgr)

	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Demo\n\nalpha beta gamma"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := svc.HandleIndex(map[string]any{"path": path}); err != nil {
		t.Fatalf("HandleIndex: %v", err)
	}

	out, err := svc.HandleSearch(map[string]any{
		"query":  "alpha",
		"top_k":  1,
		"format": "json",
	})
	if err != nil {
		t.Fatalf("HandleSearch: %v", err)
	}
	var payload struct {
		Query   string `json:"query"`
		TopK    int    `json:"top_k"`
		Count   int    `json:"count"`
		Results []struct {
			ChunkID string `json:"chunk_id"`
			Source  string `json:"source"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal search json: %v\n%s", err, out)
	}
	if payload.Query != "alpha" || payload.TopK != 1 || payload.Count > 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if len(payload.Results) > 0 && (payload.Results[0].ChunkID == "" || payload.Results[0].Source == "") {
		t.Fatalf("expected chunk/source references: %+v", payload.Results[0])
	}
	if got := mgr.RetrieverConfig().TopK; got != 9 {
		t.Fatalf("retriever top_k mutated to %d, want 9", got)
	}
}

func TestRAGToolServiceSearchRejectsUnsafeBounds(t *testing.T) {
	emb := embedder.NewMockEmbedder(8)
	mgr := rag.NewRAGManager(emb, rag.DefaultRAGConfig())
	svc := NewRAGToolService(mgr)

	if _, err := svc.HandleSearch(map[string]any{"query": "alpha", "top_k": 20 + 1}); err == nil {
		t.Fatal("expected top_k bound error")
	}
	if _, err := svc.HandleSearch(map[string]any{"query": strings.Repeat("x", 2000+1)}); err == nil {
		t.Fatal("expected query length error")
	}
}
