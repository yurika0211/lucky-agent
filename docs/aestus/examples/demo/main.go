// Command demo serves a small memory vault and the same graph-and-recall
// surface the LuckyAgent desktop client shows.
//
//	go run ./examples/demo
//
// Open http://127.0.0.1:8787
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/memory"
)

//go:embed static
var staticFiles embed.FS

func main() {
	addr := env("AESTUS_DEMO_ADDR", "127.0.0.1:8787")
	dir := env("AESTUS_DEMO_DIR", "")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal(err)
		}
		dir = filepath.Join(home, ".aestus", "demo")
	}

	store, err := memory.NewStore(dir)
	if err != nil {
		log.Fatal(err)
	}
	if store.Count() == 0 {
		if err := seed(store); err != nil {
			log.Fatal(err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		isolated := r.URL.Query().Get("isolated") == "1"
		writeJSON(w, store.GraphTopology(memory.GraphTopologyOptions{
			Limit:           limit,
			IncludeIsolated: isolated,
		}))
	})
	mux.HandleFunc("/api/recall", func(w http.ResponseWriter, r *http.Request) {
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		depth, _ := strconv.Atoi(r.URL.Query().Get("depth"))
		if depth <= 0 {
			depth = 1
		}
		if depth > 3 {
			depth = 3
		}
		started := time.Now()
		results := store.SearchWithOptions(query, memory.SearchOptions{
			Limit:           8,
			IncludeGraph:    depth > 0,
			GraphDepth:      depth,
			Explain:         true,
			SkipAccessStats: true,
		})
		writeJSON(w, map[string]any{
			"query":   query,
			"depth":   depth,
			"elapsed": time.Since(started).String(),
			"results": recallResults(results),
		})
	})
	mux.HandleFunc("/api/remember", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Content    string   `json:"content"`
			Category   string   `json:"category"`
			Tier       string   `json:"tier"`
			Importance float64  `json:"importance"`
			Tags       []string `json:"tags"`
			Links      []string `json:"links"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body.Content = strings.TrimSpace(body.Content)
		if body.Content == "" {
			http.Error(w, "content is required", http.StatusBadRequest)
			return
		}
		if body.Category == "" {
			body.Category = "fact"
		}
		if body.Importance <= 0 || body.Importance > 1 {
			body.Importance = 0.7
		}
		if err := store.SaveWithMetadata(body.Content, body.Category, parseTier(body.Tier), body.Importance, body.Tags, body.Links, nil); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "count": store.Count()})
	})
	mux.HandleFunc("/api/recent", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, store.Recent(12))
	})

	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(static)))

	log.Printf("aestus demo vault: %s", store.Dir())
	log.Printf("open http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func seed(store *memory.Store) error {
	type note struct {
		content, category string
		tier              memory.Tier
		importance        float64
		tags, links       []string
	}
	notes := []note{
		{"Aestus stores durable memories as Obsidian-compatible Markdown.", "project", memory.TierLong, 0.9, []string{"aestus"}, []string{"Obsidian", "Markdown"}},
		{"Obsidian notes stay the source of truth. The graph is rebuilt from wikilinks.", "concept", memory.TierLong, 0.85, []string{"graph"}, []string{"Wikilink", "Aestus"}},
		{"A wikilink such as [[Obsidian]] connects two notes without a database.", "concept", memory.TierLong, 0.8, []string{"graph"}, []string{"Obsidian"}},
		{"Recall walks one graph hop by default, then an optional tidal rerank.", "rule", memory.TierLong, 0.86, []string{"recall"}, []string{"Aestus", "Tidal reranker"}},
		{"The tidal reranker only adjusts order after recall. Markdown remains authoritative.", "rule", memory.TierMedium, 0.7, []string{"tidal"}, []string{"Recall"}},
		{"Short-term memory is an in-process sliding window with a structured overflow summary.", "fact", memory.TierMedium, 0.74, []string{"layers"}, []string{"Aestus"}},
		{"Mid-term memory writes a session summary into 30_Sessions.", "fact", memory.TierMedium, 0.74, []string{"layers"}, []string{"Aestus", "Session summary"}},
		{"Prefer concise Go examples with tests.", "preference", memory.TierLong, 0.92, []string{"style"}, []string{"Go"}},
		{"Go modules keep Aestus independent of any provider SDK.", "project", memory.TierLong, 0.8, []string{"go"}, []string{"Aestus"}},
		{"Vault folders follow the LuckyAgent layout: 10_Profile, 20_Projects, 50_Facts, 60_Rules.", "fact", memory.TierLong, 0.78, []string{"vault"}, []string{"LuckyAgent", "Aestus"}},
		{"召回默认走一跳 wikilink，再按需要做 tidal 重排。", "rule", memory.TierLong, 0.88, []string{"召回"}, []string{"Aestus", "Recall"}},
		{"知识图谱由笔记之间的双向链接组成，没有单独的图数据库。", "concept", memory.TierLong, 0.84, []string{"图谱"}, []string{"Wikilink", "Aestus"}},
		{"短期记忆只留在进程里；中期记忆写成会话摘要；长期记忆是 Markdown 笔记。", "fact", memory.TierMedium, 0.8, []string{"分层"}, []string{"Aestus"}},
	}
	for _, item := range notes {
		if err := store.SaveWithMetadata(item.content, item.category, item.tier, item.importance, item.tags, item.links, nil); err != nil {
			return err
		}
	}
	return nil
}

func recallResults(results []memory.SearchResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, result := range results {
		paths := make([]map[string]string, 0, len(result.Paths))
		for _, path := range result.Paths {
			paths = append(paths, map[string]string{"from_id": path.FromID, "to_id": path.ToID})
		}
		out = append(out, map[string]any{
			"entry": map[string]any{
				"id":       result.Entry.ID,
				"content":  result.Entry.Content,
				"category": result.Entry.Category,
				"tier":     result.Entry.Tier.String(),
			},
			"score":       result.Score,
			"graph_score": result.GraphScore,
			"paths":       paths,
		})
	}
	return out
}

func parseTier(raw string) memory.Tier {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "short":
		return memory.TierShort
	case "long":
		return memory.TierLong
	default:
		return memory.TierMedium
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
