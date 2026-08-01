package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/rag"
	"github.com/yurika0211/luckyagent/internal/utils"
)

const (
	defaultRAGSearchTopK          = 5
	maxRAGSearchTopK              = 20
	maxRAGSearchQueryRunes        = 2000
	defaultRAGSearchTimeout       = 30 * time.Second
	maxRAGSearchTimeoutSeconds    = 120
	defaultRAGIndexMaxFiles       = 200
	defaultRAGIndexMaxFileBytes   = 5 * 1024 * 1024
	defaultRAGIndexMaxTotalBytes  = 100 * 1024 * 1024
	maxRAGIndexPlanPreviewEntries = 50
)

// RAGToolService implements rag_search/rag_index handlers in the tool layer.
type RAGToolService struct {
	manager *rag.RAGManager
}

// NewRAGToolService creates a tool-layer RAG service.
func NewRAGToolService(manager *rag.RAGManager) *RAGToolService {
	return &RAGToolService{manager: manager}
}

func (s *RAGToolService) HandleSearch(args map[string]any) (string, error) {
	if s == nil || s.manager == nil {
		return "", fmt.Errorf("rag manager not initialized")
	}
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if len([]rune(query)) > maxRAGSearchQueryRunes {
		return "", fmt.Errorf("query exceeds %d rune limit", maxRAGSearchQueryRunes)
	}

	topK := defaultRAGSearchTopK
	if raw, ok := args["top_k"]; ok {
		switch v := raw.(type) {
		case float64:
			if int(v) > 0 {
				topK = int(v)
			}
		case int:
			if v > 0 {
				topK = v
			}
		}
	}
	if topK > maxRAGSearchTopK {
		return "", fmt.Errorf("top_k exceeds maximum %d", maxRAGSearchTopK)
	}

	timeout := defaultRAGSearchTimeout
	if raw, ok := numberArg(args["timeout_seconds"]); ok {
		if raw <= 0 || raw > maxRAGSearchTimeoutSeconds {
			return "", fmt.Errorf("timeout_seconds must be between 1 and %d", maxRAGSearchTimeoutSeconds)
		}
		timeout = time.Duration(raw * float64(time.Second))
	}
	var minScore float64
	if raw, ok := numberArg(args["min_score"]); ok {
		if raw < 0 || raw > 1 {
			return "", fmt.Errorf("min_score must be between 0 and 1")
		}
		minScore = raw
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	results, err := s.manager.SearchWithOptions(ctx, query, rag.SearchOptions{
		TopK:     topK,
		MinScore: minScore,
	})
	if err != nil {
		return "", err
	}
	format := strings.ToLower(strings.TrimSpace(stringArg(args["format"])))
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return "", fmt.Errorf("format must be text or json")
	}
	if format == "json" {
		return formatRAGSearchJSON(query, topK, minScore, results)
	}
	if len(results) == 0 {
		return fmt.Sprintf("没有找到关于「%s」的 RAG 结果；请确认已运行 rag_index，当前 top_k=%d min_score=%0.2f", query, topK, minScore), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条关于「%s」的知识片段：\n", len(results), query))
	for i, r := range results {
		title := strings.TrimSpace(r.DocTitle)
		if title == "" {
			title = strings.TrimSpace(r.DocSource)
		}
		if title == "" {
			title = "(unknown source)"
		}
		content := utils.Truncate(strings.TrimSpace(r.Content), 160)
		ref := ragResultRef(r)
		if ref != "" {
			title = title + " (" + ref + ")"
		}
		sb.WriteString(fmt.Sprintf("%d. [%0.2f] %s — %s\n", i+1, r.Score, title, content))
	}
	return strings.TrimSpace(sb.String()), nil
}

type ragSearchJSONResult struct {
	Score      float64           `json:"score"`
	DocID      string            `json:"doc_id,omitempty"`
	ChunkID    string            `json:"chunk_id"`
	ChunkIndex string            `json:"chunk_index,omitempty"`
	Title      string            `json:"title,omitempty"`
	Source     string            `json:"source,omitempty"`
	Content    string            `json:"content"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type ragSearchJSONResponse struct {
	Query    string                `json:"query"`
	TopK     int                   `json:"top_k"`
	MinScore float64               `json:"min_score,omitempty"`
	Count    int                   `json:"count"`
	Results  []ragSearchJSONResult `json:"results"`
}

func formatRAGSearchJSON(query string, topK int, minScore float64, results []rag.RetrievalResult) (string, error) {
	payload := ragSearchJSONResponse{
		Query:    query,
		TopK:     topK,
		MinScore: minScore,
		Count:    len(results),
		Results:  make([]ragSearchJSONResult, 0, len(results)),
	}
	for _, r := range results {
		payload.Results = append(payload.Results, ragSearchJSONResult{
			Score:      r.Score,
			DocID:      r.Metadata["doc_id"],
			ChunkID:    r.ChunkID,
			ChunkIndex: r.Metadata["chunk_i"],
			Title:      r.DocTitle,
			Source:     r.DocSource,
			Content:    r.Content,
			Metadata:   r.Metadata,
		})
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ragResultRef(r rag.RetrievalResult) string {
	source := strings.TrimSpace(r.DocSource)
	chunk := strings.TrimSpace(r.ChunkID)
	if source == "" {
		return chunk
	}
	if chunk == "" {
		return source
	}
	return source + "#" + chunk
}

func (s *RAGToolService) HandleIndex(args map[string]any) (string, error) {
	if s == nil || s.manager == nil {
		return "", fmt.Errorf("rag manager not initialized")
	}
	path, _ := args["path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	opts, err := parseRAGIndexOptions(args)
	if err != nil {
		return "", err
	}
	plan, err := buildRAGIndexPlan(path, opts)
	if err != nil {
		return "", err
	}
	if opts.DryRun {
		return formatRAGIndexPlan(plan, opts.Format)
	}
	var docs []*rag.Document
	for _, file := range plan.Files {
		doc, err := s.manager.IndexFile(file.Path)
		if err != nil {
			return "", fmt.Errorf("index %s: %w", file.Path, err)
		}
		docs = append(docs, doc)
	}
	result := ragIndexResult{
		Indexed:    true,
		Root:       plan.Root,
		Documents:  len(docs),
		Chunks:     countRAGChunks(docs),
		Skipped:    len(plan.Skipped),
		TotalBytes: plan.TotalBytes,
		Files:      plan.TotalFiles,
	}
	if opts.Format == "json" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if len(docs) == 1 {
		return fmt.Sprintf("✅ Indexed %s (%d chunks)", docs[0].Title, len(docs[0].Chunks)), nil
	}
	return fmt.Sprintf("✅ Indexed %d documents from %s (%d skipped)", len(docs), plan.Root, len(plan.Skipped)), nil
}

type ragIndexOptions struct {
	Recursive     bool
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
	Include       []string
	Exclude       []string
	DryRun        bool
	Format        string
	AllowExternal bool
}

type ragIndexFilePlan struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

type ragIndexSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type ragIndexPlan struct {
	DryRun     bool               `json:"dry_run"`
	Root       string             `json:"root"`
	Recursive  bool               `json:"recursive"`
	Files      []ragIndexFilePlan `json:"files,omitempty"`
	Skipped    []ragIndexSkip     `json:"skipped,omitempty"`
	TotalFiles int                `json:"total_files"`
	TotalBytes int64              `json:"total_bytes"`
	Warnings   []string           `json:"warnings,omitempty"`
}

type ragIndexResult struct {
	Indexed    bool   `json:"indexed"`
	Root       string `json:"root"`
	Documents  int    `json:"documents"`
	Chunks     int    `json:"chunks"`
	Files      int    `json:"files"`
	Skipped    int    `json:"skipped"`
	TotalBytes int64  `json:"total_bytes"`
}

func parseRAGIndexOptions(args map[string]any) (ragIndexOptions, error) {
	maxFiles, err := boundedMemoryIntArg(args["max_files"], defaultRAGIndexMaxFiles, 1, defaultRAGIndexMaxFiles, "max_files")
	if err != nil {
		return ragIndexOptions{}, err
	}
	maxFileBytes, err := boundedMemoryIntArg(args["max_file_bytes"], defaultRAGIndexMaxFileBytes, 1, defaultRAGIndexMaxFileBytes, "max_file_bytes")
	if err != nil {
		return ragIndexOptions{}, err
	}
	maxTotalBytes, err := boundedMemoryIntArg(args["max_total_bytes"], defaultRAGIndexMaxTotalBytes, 1, defaultRAGIndexMaxTotalBytes, "max_total_bytes")
	if err != nil {
		return ragIndexOptions{}, err
	}
	format := strings.ToLower(strings.TrimSpace(stringArg(args["format"])))
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return ragIndexOptions{}, fmt.Errorf("format must be text or json")
	}
	return ragIndexOptions{
		Recursive:     boolArg(args["recursive"]),
		MaxFiles:      maxFiles,
		MaxFileBytes:  int64(maxFileBytes),
		MaxTotalBytes: int64(maxTotalBytes),
		Include:       stringSliceArg(args["include"]),
		Exclude:       append(defaultRAGIndexExcludeGlobs(), stringSliceArg(args["exclude"])...),
		DryRun:        boolArg(args["dry_run"]),
		Format:        format,
		AllowExternal: boolArg(args["allow_external"]),
	}, nil
}

func buildRAGIndexPlan(path string, opts ragIndexOptions) (ragIndexPlan, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ragIndexPlan{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return ragIndexPlan{}, fmt.Errorf("path not found: %w", err)
	}
	if err := validateRAGIndexPath(abs, opts.AllowExternal); err != nil {
		return ragIndexPlan{}, err
	}
	plan := ragIndexPlan{
		DryRun:    opts.DryRun,
		Root:      abs,
		Recursive: opts.Recursive,
		Files:     make([]ragIndexFilePlan, 0),
		Skipped:   make([]ragIndexSkip, 0),
	}
	visit := func(candidate string, info os.FileInfo) {
		if len(plan.Files) >= opts.MaxFiles {
			plan.Skipped = append(plan.Skipped, ragIndexSkip{Path: candidate, Reason: "max_files"})
			return
		}
		if skipReason := ragIndexSkipReason(candidate, info, opts); skipReason != "" {
			plan.Skipped = append(plan.Skipped, ragIndexSkip{Path: candidate, Reason: skipReason})
			return
		}
		if plan.TotalBytes+info.Size() > opts.MaxTotalBytes {
			plan.Skipped = append(plan.Skipped, ragIndexSkip{Path: candidate, Reason: "max_total_bytes"})
			return
		}
		plan.Files = append(plan.Files, ragIndexFilePlan{Path: candidate, Bytes: info.Size()})
		plan.TotalBytes += info.Size()
		plan.TotalFiles++
	}
	if !info.IsDir() {
		visit(abs, info)
		return plan, nil
	}
	if opts.Recursive {
		err = filepath.Walk(abs, func(candidate string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if info.IsDir() {
				if candidate != abs && shouldSkipRAGIndexDir(candidate, opts) {
					plan.Skipped = append(plan.Skipped, ragIndexSkip{Path: candidate, Reason: "excluded_dir"})
					return filepath.SkipDir
				}
				return nil
			}
			visit(candidate, info)
			return nil
		})
		if err != nil {
			return ragIndexPlan{}, err
		}
	} else {
		entries, err := os.ReadDir(abs)
		if err != nil {
			return ragIndexPlan{}, fmt.Errorf("read dir %s: %w", abs, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			visit(filepath.Join(abs, entry.Name()), info)
		}
	}
	sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].Path < plan.Files[j].Path })
	sort.Slice(plan.Skipped, func(i, j int) bool { return plan.Skipped[i].Path < plan.Skipped[j].Path })
	return plan, nil
}

func validateRAGIndexPath(abs string, allowExternal bool) error {
	clean := filepath.Clean(abs)
	for _, forbidden := range []string{"/etc", "/proc", "/sys", "/dev", "/run"} {
		if clean == forbidden || strings.HasPrefix(clean, forbidden+string(os.PathSeparator)) {
			return fmt.Errorf("refusing to index sensitive system path %s", clean)
		}
	}
	if allowExternal {
		return nil
	}
	return nil
}

func ragIndexSkipReason(path string, info os.FileInfo, opts ragIndexOptions) string {
	if info == nil {
		return "stat_failed"
	}
	if info.Size() > opts.MaxFileBytes {
		return "max_file_bytes"
	}
	if matchAnyRAGIndexGlob(path, opts.Exclude) || sensitiveRAGIndexName(path) {
		return "excluded"
	}
	if len(opts.Include) > 0 {
		if !matchAnyRAGIndexGlob(path, opts.Include) {
			return "not_included"
		}
	} else if !defaultRAGIndexIncluded(path) {
		return "unsupported_type"
	}
	if isBinaryRAGIndexFile(path) {
		return "binary"
	}
	return ""
}

func shouldSkipRAGIndexDir(path string, opts ragIndexOptions) bool {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasPrefix(base, ".") {
		return true
	}
	return matchAnyRAGIndexGlob(path, opts.Exclude)
}

func defaultRAGIndexIncluded(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".txt", ".rst", ".adoc", ".json", ".yaml", ".yml", ".csv":
		return true
	default:
		return false
	}
}

func defaultRAGIndexExcludeGlobs() []string {
	return []string{
		".git/**", "node_modules/**", "vendor/**", "dist/**", "build/**",
		".env*", "*.pem", "*.key", "id_rsa*", "*.sqlite", "*.db",
	}
}

func sensitiveRAGIndexName(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return strings.HasPrefix(name, ".env") || strings.HasPrefix(name, "id_rsa")
}

func matchAnyRAGIndexGlob(path string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	base := filepath.Base(clean)
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}
		if ok, _ := filepath.Match(pattern, clean); ok {
			return true
		}
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			if strings.Contains(clean, "/"+prefix+"/") || strings.HasSuffix(clean, "/"+prefix) {
				return true
			}
		}
	}
	return false
}

func isBinaryRAGIndexFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if len(data) > 8192 {
		data = data[:8192]
	}
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func formatRAGIndexPlan(plan ragIndexPlan, format string) (string, error) {
	if format == "json" {
		if len(plan.Files) > maxRAGIndexPlanPreviewEntries {
			plan.Files = plan.Files[:maxRAGIndexPlanPreviewEntries]
			plan.Warnings = append(plan.Warnings, "file preview truncated")
		}
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return fmt.Sprintf("RAG index plan for %s: %d files, %d bytes, %d skipped", plan.Root, plan.TotalFiles, plan.TotalBytes, len(plan.Skipped)), nil
}

func countRAGChunks(docs []*rag.Document) int {
	total := 0
	for _, doc := range docs {
		if doc != nil {
			total += len(doc.Chunks)
		}
	}
	return total
}
