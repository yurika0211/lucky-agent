package tool

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/yurika0211/luckyharness/internal/multimodal"
	searchpkg "github.com/yurika0211/luckyharness/internal/tool/search"
	"github.com/yurika0211/luckyharness/internal/utils"
	"gopkg.in/yaml.v3"
)

// RegisterBuiltinTools 注册所有内置工具
func RegisterBuiltinTools(r *Registry, mediaProcessor ...*multimodal.Processor) {
	RegisterBuiltinToolsWithConfig(r, nil, mediaProcessor...)
}

// RegisterBuiltinToolsWithConfig 注册所有内置工具（带搜索配置）
func RegisterBuiltinToolsWithConfig(r *Registry, searchCfg *WebSearchConfig, mediaProcessor ...*multimodal.Processor) {
	var processor *multimodal.Processor
	if len(mediaProcessor) > 0 {
		processor = mediaProcessor[0]
	}
	r.Register(TerminalTool())
	r.Register(LegacyShellTool())
	r.Register(FileReadTool())
	r.Register(FileWriteTool())
	r.Register(FilePatchTool())
	r.Register(FileListTool())
	r.Register(WebSearchTool(searchCfg))
	r.Register(WebFetchTool(searchCfg))
	r.Register(CurrentTimeTool())
	r.Register(CalculateTool())
	r.Register(ImageAnalyzeTool(processor, ""))
	r.Register(LogTailTool())
	r.Register(LogGrepTool())
	r.Register(HTTPRequestTool())
	r.Register(JSONQueryTool())
	r.Register(YAMLQueryTool())
	r.Register(CSVQueryTool())
	r.Register(SQLQueryTool())
	r.Register(DBSchemaTool())
	r.Register(RememberTool(nil))
	r.Register(RecallTool(nil))
	r.Register(RAGSearchTool(nil))
	r.Register(RAGIndexTool(nil))
}

func terminalToolWithName(name string, hidden bool) *Tool {
	return &Tool{
		Name:            name,
		Description:     "Run a terminal command when you need to inspect runtime state, execute project commands, check the environment, or perform real system actions that cannot be answered from files alone.",
		Category:        CatBuiltin,
		Source:          "builtin",
		Permission:      PermApprove, // shell 命令需要审批
		ShellAware:      true,
		ParallelSafe:    false,
		HiddenFromModel: hidden,
		Parameters: map[string]Param{
			"command": {
				Type:        "string",
				Description: "Concrete terminal command to run. Prefer precise inspection or execution commands over exploratory one-liners.",
				Required:    true,
			},
			"timeout": {
				Type:        "number",
				Description: "Timeout in seconds (default 30)",
				Required:    false,
				Default:     30,
			},
			"workdir": {
				Type:        "string",
				Description: "Optional working directory. Use when the command must run in a specific project or subdirectory.",
				Required:    false,
			},
		},
		Handler: handleShell,
	}
}

// TerminalTool 执行终端命令，是当前推荐的主工具名。
func TerminalTool() *Tool {
	return terminalToolWithName("terminal", false)
}

// LegacyShellTool 保留 shell 作为兼容入口，但不再默认暴露给模型。
func LegacyShellTool() *Tool {
	return terminalToolWithName("shell", true)
}

// ShellTool 保留旧函数名，避免调用方在迁移期间直接断裂。
func ShellTool() *Tool {
	return TerminalTool()
}

func handleShell(args map[string]any) (string, error) {
	command, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command is required")
	}

	// Shell 沙箱检查：拦截对禁止路径的访问
	if err := validateShellSandbox(command); err != nil {
		return "", err
	}

	timeout := 30
	if t, ok := args["timeout"]; ok {
		switch v := t.(type) {
		case float64:
			timeout = int(v)
		case int:
			timeout = v
		}
	}
	// 硬上限 300 秒
	if timeout <= 0 {
		timeout = 30
	}
	if timeout > 300 {
		timeout = 300
	}

	// 从 shell context 注入的值
	cwd, _ := args["_cwd"].(string)
	env, _ := args["_env"].(map[string]string)

	workdir := cwd
	if w, ok := args["workdir"]; ok {
		if ws, ok := w.(string); ok && ws != "" {
			workdir = ws
		}
	}

	// 构建 shell 前缀：注入环境变量
	prefix := ""
	if len(env) > 0 {
		// 合法环境变量名正则：字母/下划线开头，后跟字母/数字/下划线
		validEnvKey := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
		for k, v := range env {
			// 校验 key 防止 shell 注入
			if !validEnvKey.MatchString(k) {
				continue
			}
			// 转义单引号
			escaped := strings.ReplaceAll(v, "'", "'\\''")
			prefix += fmt.Sprintf("export %s='%s'; ", k, escaped)
		}
	}
	fullCommand := prefix + command

	ctx := time.Duration(timeout) * time.Second
	cmd := exec.Command("sh", "-c", fullCommand)
	if workdir != "" {
		cmd.Dir = workdir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case err := <-done:
		output := stdout.String()
		if stderr.Len() > 0 {
			output += "\n[stderr]\n" + stderr.String()
		}
		if err != nil {
			output += fmt.Sprintf("\n[exit code: %v]", err)
		}
		// 截断过长输出
		if len(output) > 10000 {
			output = output[:10000] + "\n... (truncated)"
		}
		return output, nil
	case <-time.After(ctx):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return "", fmt.Errorf("command timed out after %d seconds", timeout)
	}
}

// FileReadTool 读取文件内容
func FileReadTool() *Tool {
	return &Tool{
		Name:        "file_read",
		Description: "Read a local file when repository or document contents are the source of truth. Prefer this before guessing about code, config, notes, or generated artifacts.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto, // 读文件自动批准
		Parameters: map[string]Param{
			"path": {
				Type:        "string",
				Description: "Path to the local file that should be inspected.",
				Required:    true,
			},
			"offset": {
				Type:        "number",
				Description: "Line number to start reading from (1-indexed)",
				Required:    false,
				Default:     1,
			},
			"limit": {
				Type:        "number",
				Description: "Maximum number of lines to read",
				Required:    false,
				Default:     2000,
			},
		},
		Handler: handleFileRead,
	}
}

func handleFileRead(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}

	// 路径安全检查
	if err := validatePath(path); err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	offset := 1
	if o, ok := args["offset"]; ok {
		switch v := o.(type) {
		case float64:
			offset = int(v)
		case int:
			offset = v
		}
	}
	if offset < 1 {
		offset = 1
	}

	limit := 2000
	if l, ok := args["limit"]; ok {
		switch v := l.(type) {
		case float64:
			limit = int(v)
		case int:
			limit = v
		}
	}

	start := offset - 1
	if start >= len(lines) {
		return "", fmt.Errorf("offset %d exceeds file length %d", offset, len(lines))
	}

	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}

	// 带行号输出
	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(fmt.Sprintf("%d| %s\n", i+1, lines[i]))
	}

	return b.String(), nil
}

// FileWriteTool 写入文件
func FileWriteTool() *Tool {
	return &Tool{
		Name:        "file_write",
		Description: "Write or overwrite a local file when the task requires creating, updating, or exporting a real artifact on disk.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove, // 写文件需要审批
		Parameters: map[string]Param{
			"path": {
				Type:        "string",
				Description: "Target path of the file to create or overwrite.",
				Required:    true,
			},
			"content": {
				Type:        "string",
				Description: "Full file content to write. Use complete intended content, not a diff.",
				Required:    true,
			},
		},
		Handler: handleFileWrite,
	}
}

func handleFileWrite(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}

	// 路径安全检查
	if err := validatePath(path); err != nil {
		return "", err
	}

	// 创建父目录
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("Written %d bytes to %s", len(content), path), nil
}

// FilePatchTool applies a targeted string replacement inside an existing file.
func FilePatchTool() *Tool {
	return &Tool{
		Name:        "file_patch",
		Description: "Apply a targeted in-place edit to an existing file by replacing one matched text block with another. Prefer this over file_write when only a small part of the file should change.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"path": {
				Type:        "string",
				Description: "Path to the file that should be patched.",
				Required:    true,
			},
			"match": {
				Type:        "string",
				Description: "Exact text to find in the file before applying the patch.",
				Required:    true,
			},
			"replace": {
				Type:        "string",
				Description: "Replacement text for the matched block.",
				Required:    true,
			},
			"occurrence": {
				Type:        "number",
				Description: "1-based occurrence to replace when the same text appears multiple times. Default 1.",
				Required:    false,
				Default:     1,
			},
			"replace_all": {
				Type:        "boolean",
				Description: "Replace every exact occurrence instead of a single targeted one.",
				Required:    false,
				Default:     false,
			},
		},
		Handler: handleFilePatch,
	}
}

func handleFilePatch(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	match, ok := args["match"].(string)
	if !ok {
		return "", fmt.Errorf("match is required")
	}
	replace, ok := args["replace"].(string)
	if !ok {
		return "", fmt.Errorf("replace is required")
	}
	if strings.TrimSpace(match) == "" {
		return "", fmt.Errorf("match must not be empty")
	}

	replaceAll := false
	if v, ok := args["replace_all"].(bool); ok {
		replaceAll = v
	}

	occurrence := 1
	if v, ok := args["occurrence"]; ok {
		switch n := v.(type) {
		case float64:
			occurrence = int(n)
		case int:
			occurrence = n
		}
	}
	if occurrence <= 0 {
		occurrence = 1
	}

	if err := validatePath(path); err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	content := string(data)

	matchCount := strings.Count(content, match)
	if matchCount == 0 {
		return "", fmt.Errorf("match text not found in %s", path)
	}

	var patched string
	replacedCount := 0
	if replaceAll {
		patched = strings.ReplaceAll(content, match, replace)
		replacedCount = matchCount
	} else {
		if occurrence > matchCount {
			return "", fmt.Errorf("occurrence %d exceeds %d matches in %s", occurrence, matchCount, path)
		}
		patched, replacedCount = replaceStringOccurrence(content, match, replace, occurrence)
	}

	if replacedCount == 0 {
		return "", fmt.Errorf("no patch applied to %s", path)
	}
	if err := os.WriteFile(path, []byte(patched), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("Patched %s (%d replacement%s)", path, replacedCount, pluralSuffix(replacedCount)), nil
}

func replaceStringOccurrence(content, match, replace string, occurrence int) (string, int) {
	searchFrom := 0
	found := 0
	for {
		idx := strings.Index(content[searchFrom:], match)
		if idx < 0 {
			return content, 0
		}
		idx += searchFrom
		found++
		if found == occurrence {
			var b strings.Builder
			b.WriteString(content[:idx])
			b.WriteString(replace)
			b.WriteString(content[idx+len(match):])
			return b.String(), 1
		}
		searchFrom = idx + len(match)
	}
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

// FileListTool 列出目录内容
func FileListTool() *Tool {
	return &Tool{
		Name:        "file_list",
		Description: "List files or directories when you need repository structure, candidate files, or navigation context before reading or editing specific paths.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto, // 列目录自动批准
		Parameters: map[string]Param{
			"path": {
				Type:        "string",
				Description: "Directory path to inspect.",
				Required:    true,
			},
			"recursive": {
				Type:        "boolean",
				Description: "Whether to include nested files and subdirectories.",
				Required:    false,
				Default:     false,
			},
		},
		Handler: handleFileList,
	}
}

func handleFileList(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}

	recursive := false
	if r, ok := args["recursive"]; ok {
		recursive, _ = r.(bool)
	}

	maxEntries := 200
	if v, ok := args["max_entries"]; ok {
		switch n := v.(type) {
		case float64:
			maxEntries = int(n)
		case int:
			maxEntries = n
		}
	}
	if maxEntries <= 0 {
		maxEntries = 200
	}

	// 路径安全检查
	if err := validatePath(path); err != nil {
		return "", err
	}

	var b strings.Builder
	entryCount := 0
	truncated := false

	if recursive {
		stopWalk := errors.New("file list truncated")
		err := filepath.Walk(path, func(walkPath string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if entryCount >= maxEntries {
				truncated = true
				return stopWalk
			}
			rel, _ := filepath.Rel(path, walkPath)
			if info.IsDir() {
				b.WriteString(fmt.Sprintf("  📁 %s/\n", rel))
			} else {
				b.WriteString(fmt.Sprintf("  📄 %s (%d bytes)\n", rel, info.Size()))
			}
			entryCount++
			return nil
		})
		if err != nil && !errors.Is(err, stopWalk) {
			return "", fmt.Errorf("walk directory: %w", err)
		}
	} else {
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", fmt.Errorf("read directory: %w", err)
		}
		for _, entry := range entries {
			if entryCount >= maxEntries {
				truncated = true
				break
			}
			if entry.IsDir() {
				b.WriteString(fmt.Sprintf("  📁 %s/\n", entry.Name()))
			} else {
				info, _ := entry.Info()
				b.WriteString(fmt.Sprintf("  📄 %s (%d bytes)\n", entry.Name(), info.Size()))
			}
			entryCount++
		}
	}

	if truncated {
		b.WriteString(fmt.Sprintf("  ... truncated after %d entries\n", maxEntries))
	}

	return b.String(), nil
}

// WebSearchConfig 搜索配置（从 config.Manager 传入）
type WebSearchConfig struct {
	Provider   string // brave, ddgs, searxng, exa（默认 brave）
	APIKey     string // Brave / Exa API key
	BaseURL    string // SearXNG 自部署地址
	MaxResults int    // 最大结果数（默认 5）
	Proxy      string // HTTP/SOCKS5 代理
}

// defaultWebSearchConfig 返回默认搜索配置
func defaultWebSearchConfig() *WebSearchConfig {
	return &WebSearchConfig{
		Provider:   "brave",
		MaxResults: 5,
	}
}

// WebSearchTool 网络搜索（多源降级：Brave → ddgs → DDG Lite → SearXNG）
// 照 skills/web-search/SKILL.md 设计：三源降级 + 搜索策略 + 来源标注
func WebSearchTool(cfg *WebSearchConfig) *Tool {
	if cfg == nil {
		cfg = defaultWebSearchConfig()
	}
	return &Tool{
		Name:        "web_search",
		Description: "Search the web when you need external or recent information, candidate sources, or multiple viewpoints before fetching a specific page. Use mode='deep' when cross-source validation matters.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"query": {
				Type:        "string",
				Description: "Search query phrased around the actual fact, identifier, or concept you need to verify.",
				Required:    true,
			},
			"count": {
				Type:        "number",
				Description: "Number of results to return (1-10). Use smaller values when you already know what you are looking for.",
				Required:    false,
				Default:     5,
			},
			"mode": {
				Type:        "string",
				Description: "Search mode: 'quick' for fast single-path lookup, 'deep' for multi-source cross-validation and merged evidence.",
				Required:    false,
				Default:     "quick",
			},
		},
		Handler: func(args map[string]any) (string, error) {
			return handleWebSearch(cfg, args)
		},
		ParallelSafe: true,
	}
}

func handleWebSearch(cfg *WebSearchConfig, args map[string]any) (string, error) {
	query, ok := args["query"].(string)
	if !ok {
		return "", fmt.Errorf("query is required")
	}

	count := cfg.MaxResults
	if count <= 0 {
		count = 5
	}
	if c, ok := args["count"]; ok {
		switch v := c.(type) {
		case float64:
			count = int(v)
		case int:
			count = v
		}
	}
	if count < 1 {
		count = 1
	}
	if count > 10 {
		count = 10
	}

	mode := "quick"
	if m, ok := args["mode"].(string); ok {
		mode = strings.ToLower(m)
	}

	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = "brave"
	}

	if mode == "deep" {
		return handleDeepSearch(cfg, query, count, provider)
	}

	manager := searchpkg.NewManager(buildSearchManagerConfig(cfg, provider))
	searchCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	results, err := manager.QuickSearch(searchCtx, query, count)
	if err != nil || len(results) == 0 {
		return fmt.Sprintf("No results found for '%s' (all search sources failed)", query), nil
	}

	out := formatEntries(query, toSearchEntries(results), count)
	label := ""
	if len(results) > 0 {
		label = sourceDisplayName(results[0].Source)
	}
	if label == "" {
		label = sourceDisplayName(provider)
	}
	if label != "" {
		out = annotateSource(out, label)
	}
	return out, nil
}

func quickSearchOrder(provider string, cfg *WebSearchConfig) []string {
	return []string{"exa", "ddgs", "searxng", "ddg-lite", "brave"}
}

// handleDeepSearch 深度搜索模式：多源交叉验证，合并去重
// 照 SKILL.md「深度调研」策略：多源搜索 → 合并去重 → 标注来源
func handleDeepSearch(cfg *WebSearchConfig, query string, count int, provider string) (string, error) {
	manager := searchpkg.NewManager(buildSearchManagerConfig(cfg, provider))
	searchCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	dr, err := manager.DeepSearch(searchCtx, query, count)
	if err != nil || dr == nil || len(dr.Results) == 0 {
		return fmt.Sprintf("No results found for '%s' (all search sources failed)", query), nil
	}
	return searchpkg.FormatDeepResults(query, dr), nil
}

func buildSearchManagerConfig(cfg *WebSearchConfig, provider string) *searchpkg.SearchConfig {
	if cfg == nil {
		cfg = defaultWebSearchConfig()
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	}
	if provider == "" {
		provider = "brave"
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("SEARXNG_BASE_URL"))
	}

	sc := &searchpkg.SearchConfig{
		DefaultProvider: provider,
		BraveAPIKey:     resolveBraveAPIKey(cfg),
		SearXNGBaseURL:  baseURL,
		ExaAPIKey:       resolveExaAPIKey(cfg),
		JinaAPIKey:      os.Getenv("JINA_API_KEY"),
		MaxResults:      cfg.MaxResults,
		Proxy:           cfg.Proxy,
		CacheTTL:        10 * time.Minute,
		CacheSize:       100,
	}
	if sc.MaxResults <= 0 {
		sc.MaxResults = 5
	}
	return sc
}

func sourceDisplayName(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "searxng":
		return "SearXNG"
	case "exa":
		return "Exa"
	case "ddgs":
		return "DDG (ddgs)"
	case "ddg-lite":
		return "DDG Lite"
	case "brave":
		return "Brave"
	default:
		return ""
	}
}

func deepSearchOrder(provider string, cfg *WebSearchConfig) []string {
	order := []string{"exa", "ddgs", "searxng", "ddg-lite", "brave"}
	if resolveExaAPIKey(cfg) == "" {
		order = []string{"ddgs", "searxng", "ddg-lite", "brave"}
	}
	if cfg == nil || strings.TrimSpace(cfg.BaseURL) == "" {
		filtered := make([]string, 0, len(order))
		for _, name := range order {
			if name == "searxng" {
				continue
			}
			filtered = append(filtered, name)
		}
		return filtered
	}
	return order
}

func buildSearchEngineForSource(source string, cfg *WebSearchConfig) searchpkg.SearchEngine {
	switch source {
	case "searxng":
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = os.Getenv("SEARXNG_BASE_URL")
		}
		return searchpkg.NewSearXNGEngine(baseURL, cfg.Proxy)
	case "exa":
		return searchpkg.NewExaEngine(resolveExaAPIKey(cfg))
	case "ddgs":
		return searchpkg.NewDDGSEngine()
	case "ddg-lite":
		return searchpkg.NewDDGLiteEngine()
	case "brave":
		return searchpkg.NewBraveEngine(resolveBraveAPIKey(cfg), cfg.Proxy)
	default:
		return nil
	}
}

func toSearchEntries(results []searchpkg.SearchResult) []searchEntry {
	entries := make([]searchEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, searchEntry{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Snippet,
		})
	}
	return entries
}

func resolveBraveAPIKey(cfg *WebSearchConfig) string {
	if cfg != nil && strings.TrimSpace(strings.ToLower(cfg.Provider)) == "brave" && strings.TrimSpace(cfg.APIKey) != "" {
		return cfg.APIKey
	}
	if v := os.Getenv("BRAVE_API_KEY"); v != "" {
		return v
	}
	if cfg != nil && strings.TrimSpace(cfg.APIKey) != "" {
		return cfg.APIKey
	}
	return ""
}

// searchEntry 统一的搜索结果条目
type searchEntry struct {
	Title   string
	URL     string
	Snippet string
}

// mergedEntry 合并去重后的条目
type mergedEntry struct {
	title   string
	url     string
	snippet string
	sources []string
}

// annotateSource 给搜索结果标注来源
func annotateSource(result, source string) string {
	// 在 "Results for:" 行后插入来源标注
	return strings.Replace(result, "Results for:", "[Source: "+source+"] Results for:", 1)
}

// ── Brave Search API ─────────────────────────────────────────────────────────

func searchWithBrave(cfg *WebSearchConfig, query string, count int) (string, error) {
	entries, err := searchWithBraveEntries(cfg, query, count)
	if err != nil {
		return "", err
	}
	return formatEntries(query, entries, count), nil
}

func searchWithBraveEntries(cfg *WebSearchConfig, query string, count int) ([]searchEntry, error) {
	engine := searchpkg.NewBraveEngine(resolveBraveAPIKey(cfg), cfg.Proxy)
	results, err := engine.Search(context.Background(), query, count)
	if err != nil {
		return nil, err
	}
	return toSearchEntries(results), nil
}

// ── Exa Search API ───────────────────────────────────────────────────────────

func searchWithExa(cfg *WebSearchConfig, query string, count int) (string, error) {
	entries, err := searchWithExaEntries(cfg, query, count)
	if err != nil {
		return "", err
	}
	return formatEntries(query, entries, count), nil
}

func searchWithExaEntries(cfg *WebSearchConfig, query string, count int) ([]searchEntry, error) {
	engine := searchpkg.NewExaEngine(resolveExaAPIKey(cfg))
	results, err := engine.Search(context.Background(), query, count)
	if err != nil {
		return nil, err
	}
	return toSearchEntries(results), nil
}

func resolveExaAPIKey(cfg *WebSearchConfig) string {
	if cfg != nil && strings.TrimSpace(strings.ToLower(cfg.Provider)) == "exa" && strings.TrimSpace(cfg.APIKey) != "" {
		return cfg.APIKey
	}
	if v := os.Getenv("LH_SEARCH_EXA_KEY"); v != "" {
		return v
	}
	if v := os.Getenv("EXA_API_KEY"); v != "" {
		return v
	}
	if cfg != nil && strings.TrimSpace(cfg.APIKey) != "" {
		return cfg.APIKey
	}
	return ""
}

// ── ddgs Python 包 ───────────────────────────────────────────────────────────

func searchWithDDGS(query string, count int) (string, error) {
	entries, err := searchWithDDGSEntries(query, count)
	if err != nil {
		return "", err
	}
	return formatEntries(query, entries, count), nil
}

func searchWithDDGSEntries(query string, count int) ([]searchEntry, error) {
	engine := searchpkg.NewDDGSEngine()
	results, err := engine.Search(context.Background(), query, count)
	if err != nil {
		return nil, err
	}
	return toSearchEntries(results), nil
}

// ── DDG Lite curl ────────────────────────────────────────────────────────────

func searchWithDDGLite(query string, count int) (string, error) {
	engine := searchpkg.NewDDGLiteEngine()
	results, err := engine.Search(context.Background(), query, count)
	if err != nil {
		return "", err
	}
	return formatEntries(query, toSearchEntries(results), count), nil
}

// ── SearXNG 自部署 ──────────────────────────────────────────────────────────

func searchWithSearXNG(cfg *WebSearchConfig, query string, count int) (string, error) {
	entries, err := searchWithSearXNGEntries(cfg, query, count)
	if err != nil {
		return "", err
	}
	return formatEntries(query, entries, count), nil
}

func searchWithSearXNGEntries(cfg *WebSearchConfig, query string, count int) ([]searchEntry, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("SEARXNG_BASE_URL")
	}
	engine := searchpkg.NewSearXNGEngine(baseURL, cfg.Proxy)
	results, err := engine.Search(context.Background(), query, count)
	if err != nil {
		return nil, err
	}
	return toSearchEntries(results), nil
}

// ── HTML 解析辅助 ────────────────────────────────────────────────────────────

// formatEntries 将 searchEntry 列表格式化为可读文本
func formatEntries(query string, entries []searchEntry, count int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Results for: %s\n\n", query))
	for i, e := range entries {
		if i >= count {
			break
		}
		b.WriteString(fmt.Sprintf("%d. %s\n   %s\n", i+1, e.Title, e.URL))
		if e.Snippet != "" {
			b.WriteString(fmt.Sprintf("   %s\n", e.Snippet))
		}
		b.WriteString("\n")
	}
	result := b.String()
	if len(result) > 8000 {
		result = result[:8000] + "\n... (truncated)"
	}
	return result
}

func parseDDGLiteHTML(html string, count int) string {
	var b strings.Builder
	b.WriteString("Results (DDG Lite):\n\n")

	linkRe := regexp.MustCompile(`<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	snippetRe := regexp.MustCompile(`<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)

	links := linkRe.FindAllStringSubmatch(html, -1)
	snippets := snippetRe.FindAllStringSubmatch(html, -1)

	n := len(links)
	if n > count {
		n = count
	}

	for i := 0; i < n; i++ {
		url := links[i][1]
		title := utils.StripHTMLTags(links[i][2])
		b.WriteString(fmt.Sprintf("%d. %s\n   %s\n", i+1, title, url))
		if i < len(snippets) {
			snippet := utils.StripHTMLTags(snippets[i][1])
			if snippet != "" {
				b.WriteString(fmt.Sprintf("   %s\n", snippet))
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

func urlEncode(s string) string {
	return utils.URLEncode(s)
}

// validateFetchURL 校验 URL 是否安全（SSRF 防护）
// 仅允许 http/https scheme，禁止私有 IP 和云元数据地址
func validateFetchURL(rawURL string) error {
	return searchpkg.ValidateFetchURL(rawURL)
}

// ── WebFetchTool ─────────────────────────────────────────────────────────────

// WebFetchTool 抓取 URL 内容（照 SKILL.md 设计：Defuddle → Jina → curl 降级）
func WebFetchTool(cfg *WebSearchConfig) *Tool {
	if cfg == nil {
		cfg = defaultWebSearchConfig()
	}
	return &Tool{
		Name:        "web_fetch",
		Description: "Fetch and extract the readable content of a specific URL when you already have a target page and need the actual text, not just search snippets.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"url": {
				Type:        "string",
				Description: "Exact URL to fetch and convert into readable text.",
				Required:    true,
			},
			"max_chars": {
				Type:        "number",
				Description: "Maximum readable text to return. Lower this when you only need a focused excerpt.",
				Required:    false,
				Default:     50000,
			},
		},
		Handler: func(args map[string]any) (string, error) {
			return handleWebFetch(cfg, args)
		},
		ParallelSafe: true,
	}
}

func handleWebFetch(cfg *WebSearchConfig, args map[string]any) (string, error) {
	fetchURL, ok := args["url"].(string)
	if !ok {
		return "", fmt.Errorf("url is required")
	}

	// SSRF 防护：校验 URL scheme
	if err := validateFetchURL(fetchURL); err != nil {
		return "", fmt.Errorf("url validation failed: %w", err)
	}

	maxChars := 50000
	if mc, ok := args["max_chars"]; ok {
		switch v := mc.(type) {
		case float64:
			maxChars = int(v)
		case int:
			maxChars = v
		}
	}

	// 策略 1: Defuddle CLI（照 SKILL.md：优先用 Defuddle 提取干净 Markdown）
	if result, err := fetchWithDefuddle(fetchURL, maxChars); err == nil && result != "" {
		return result, nil
	}

	// 策略 2: Jina Reader API（免费额度，提取正文效果好）
	if result, err := fetchWithJina(cfg, fetchURL, maxChars); err == nil && result != "" {
		return result, nil
	}

	// 策略 3: curl + strip HTML（本地降级）
	if result, err := fetchWithCurl(cfg, fetchURL, maxChars); err == nil && result != "" {
		return result, nil
	}

	return fmt.Sprintf("Failed to fetch %s (all methods failed)", fetchURL), nil
}

// fetchWithDefuddle 使用 defuddle CLI 提取网页正文为干净 Markdown
func fetchWithDefuddle(fetchURL string, maxChars int) (string, error) {
	result, err := searchpkg.NewDefuddleEngine().Fetch(context.Background(), fetchURL, maxChars)
	if err != nil {
		return "", err
	}
	return formatFetchResult(result, false), nil
}

func fetchWithJina(cfg *WebSearchConfig, url string, maxChars int) (string, error) {
	apiKey := os.Getenv("JINA_API_KEY")
	engine := searchpkg.NewJinaEngine(apiKey, cfg.Proxy)
	result, err := engine.Fetch(context.Background(), url, maxChars)
	if err != nil {
		return "", err
	}
	return formatFetchResult(result, true), nil
}

func fetchWithCurl(cfg *WebSearchConfig, url string, maxChars int) (string, error) {
	result, err := searchpkg.NewCurlEngine(cfg.Proxy).Fetch(context.Background(), url, maxChars)
	if err != nil {
		return "", err
	}
	return formatFetchResult(result, false), nil
}

func formatFetchResult(result *searchpkg.FetchResult, includeTitle bool) string {
	if result == nil {
		return ""
	}
	content := result.Content
	if !includeTitle || strings.TrimSpace(result.Title) == "" {
		return content
	}
	return fmt.Sprintf("# %s\n\n%s", result.Title, content)
}

// CurrentTimeTool 获取当前时间
func CurrentTimeTool() *Tool {
	return &Tool{
		Name:         "current_time",
		Description:  "Get the current date and time.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermAuto,
		Parameters:   map[string]Param{},
		Handler:      handleCurrentTime,
		ParallelSafe: true,
	}
}

func handleCurrentTime(args map[string]any) (string, error) {
	now := time.Now()
	return fmt.Sprintf("Current time: %s (%s)", now.Format("2006-01-02 15:04:05"), now.Location()), nil
}

// CalculateTool evaluates small arithmetic expressions locally.
func CalculateTool() *Tool {
	return &Tool{
		Name:         "calculate",
		Description:  "Evaluate a small arithmetic expression locally. Useful for quick numeric checks without using a shell or external model call.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermAuto,
		ParallelSafe: true,
		Parameters: map[string]Param{
			"expression": {
				Type:        "string",
				Description: "Arithmetic expression such as (12.5*8)/3, sqrt(144), max(3,7,2), or 2^10.",
				Required:    true,
			},
		},
		Handler: handleCalculate,
	}
}

func handleCalculate(args map[string]any) (string, error) {
	expression, ok := args["expression"].(string)
	if !ok || strings.TrimSpace(expression) == "" {
		return "", fmt.Errorf("expression is required")
	}

	expr, err := parser.ParseExpr(strings.TrimSpace(expression))
	if err != nil {
		return "", fmt.Errorf("parse expression: %w", err)
	}

	value, err := evalNumericExpr(expr)
	if err != nil {
		return "", err
	}

	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("expression produced non-finite result")
	}

	return strconv.FormatFloat(value, 'f', -1, 64), nil
}

func evalNumericExpr(expr ast.Expr) (float64, error) {
	switch n := expr.(type) {
	case *ast.BasicLit:
		if n.Kind != token.INT && n.Kind != token.FLOAT {
			return 0, fmt.Errorf("unsupported literal %q", n.Value)
		}
		v, err := strconv.ParseFloat(n.Value, 64)
		if err != nil {
			return 0, fmt.Errorf("parse number %q: %w", n.Value, err)
		}
		return v, nil

	case *ast.ParenExpr:
		return evalNumericExpr(n.X)

	case *ast.UnaryExpr:
		v, err := evalNumericExpr(n.X)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return v, nil
		case token.SUB:
			return -v, nil
		default:
			return 0, fmt.Errorf("unsupported unary operator %s", n.Op)
		}

	case *ast.BinaryExpr:
		left, err := evalNumericExpr(n.X)
		if err != nil {
			return 0, err
		}
		right, err := evalNumericExpr(n.Y)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left / right, nil
		case token.REM:
			if right == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			return math.Mod(left, right), nil
		case token.XOR:
			return math.Pow(left, right), nil
		default:
			return 0, fmt.Errorf("unsupported binary operator %s", n.Op)
		}

	case *ast.CallExpr:
		ident, ok := n.Fun.(*ast.Ident)
		if !ok {
			return 0, fmt.Errorf("unsupported function call")
		}
		args := make([]float64, 0, len(n.Args))
		for _, arg := range n.Args {
			v, err := evalNumericExpr(arg)
			if err != nil {
				return 0, err
			}
			args = append(args, v)
		}
		return evalNumericFunc(ident.Name, args)

	default:
		return 0, fmt.Errorf("unsupported expression type %T", expr)
	}
}

func evalNumericFunc(name string, args []float64) (float64, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "sqrt":
		if len(args) != 1 {
			return 0, fmt.Errorf("sqrt expects 1 argument")
		}
		if args[0] < 0 {
			return 0, fmt.Errorf("sqrt of negative number")
		}
		return math.Sqrt(args[0]), nil
	case "abs":
		if len(args) != 1 {
			return 0, fmt.Errorf("abs expects 1 argument")
		}
		return math.Abs(args[0]), nil
	case "ceil":
		if len(args) != 1 {
			return 0, fmt.Errorf("ceil expects 1 argument")
		}
		return math.Ceil(args[0]), nil
	case "floor":
		if len(args) != 1 {
			return 0, fmt.Errorf("floor expects 1 argument")
		}
		return math.Floor(args[0]), nil
	case "round":
		if len(args) != 1 {
			return 0, fmt.Errorf("round expects 1 argument")
		}
		return math.Round(args[0]), nil
	case "min":
		if len(args) == 0 {
			return 0, fmt.Errorf("min expects at least 1 argument")
		}
		v := args[0]
		for _, arg := range args[1:] {
			v = math.Min(v, arg)
		}
		return v, nil
	case "max":
		if len(args) == 0 {
			return 0, fmt.Errorf("max expects at least 1 argument")
		}
		v := args[0]
		for _, arg := range args[1:] {
			v = math.Max(v, arg)
		}
		return v, nil
	case "pow":
		if len(args) != 2 {
			return 0, fmt.Errorf("pow expects 2 arguments")
		}
		return math.Pow(args[0], args[1]), nil
	default:
		return 0, fmt.Errorf("unsupported function %q", name)
	}
}

// ImageAnalyzeTool analyzes images, screenshots, and simple documents through the multimodal processor.
func ImageAnalyzeTool(processor *multimodal.Processor, defaultProvider string) *Tool {
	return &Tool{
		Name:         "image_analyze",
		Description:  "Analyze an image, screenshot, chart, or scanned document. Extract visible text, summarize UI or visual content, and surface likely errors or key signals.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermAuto,
		ParallelSafe: true,
		Parameters: map[string]Param{
			"path": {
				Type:        "string",
				Description: "Local file path to the image or document.",
				Required:    false,
			},
			"url": {
				Type:        "string",
				Description: "Remote URL to the image or document.",
				Required:    false,
			},
			"base64_data": {
				Type:        "string",
				Description: "Base64-encoded file contents when the image is already in memory.",
				Required:    false,
			},
			"mime_type": {
				Type:        "string",
				Description: "Optional MIME type such as image/png or application/pdf.",
				Required:    false,
			},
			"provider": {
				Type:        "string",
				Description: "Optional multimodal provider name override.",
				Required:    false,
			},
		},
		Handler: handleImageAnalyze(processor, defaultProvider),
	}
}

func handleImageAnalyze(processor *multimodal.Processor, defaultProvider string) func(args map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		if processor == nil {
			return "", fmt.Errorf("image analysis is not configured")
		}

		input, err := buildImageAnalyzeInput(args)
		if err != nil {
			return "", err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		providerName, _ := args["provider"].(string)
		providerName = strings.TrimSpace(providerName)
		if providerName == "" {
			providerName = strings.TrimSpace(defaultProvider)
		}
		var result *multimodal.AnalysisResult
		if providerName != "" {
			result, err = processor.AnalyzeWithProvider(ctx, providerName, input)
		} else {
			result, err = processor.Analyze(ctx, input)
		}
		if err != nil {
			return "", err
		}
		return formatImageAnalysisResult(result), nil
	}
}

func buildImageAnalyzeInput(args map[string]any) (*multimodal.Input, error) {
	path, _ := args["path"].(string)
	url, _ := args["url"].(string)
	base64Data, _ := args["base64_data"].(string)
	mimeType, _ := args["mime_type"].(string)

	path = strings.TrimSpace(path)
	url = strings.TrimSpace(url)
	base64Data = strings.TrimSpace(base64Data)
	mimeType = strings.TrimSpace(mimeType)

	if path == "" && url == "" && base64Data == "" {
		return nil, fmt.Errorf("one of path, url, or base64_data is required")
	}

	modality := inferImageAnalyzeModality(path, mimeType)
	var input *multimodal.Input
	switch {
	case path != "":
		if err := validatePath(path); err != nil {
			return nil, err
		}
		input = multimodal.NewInputFromPath(modality, path)
		if mimeType == "" {
			mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
		}
	case url != "":
		input = multimodal.NewInputFromURL(modality, url)
	case base64Data != "":
		data, err := base64.StdEncoding.DecodeString(base64Data)
		if err != nil {
			return nil, fmt.Errorf("decode base64_data: %w", err)
		}
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		modality = inferImageAnalyzeModality("", mimeType)
		input = multimodal.NewInput(modality, mimeType, data)
	}

	if input == nil {
		return nil, fmt.Errorf("failed to build multimodal input")
	}
	input.Modality = modality
	input.MimeType = mimeType
	if input.Metadata == nil {
		input.Metadata = make(map[string]string)
	}
	if path != "" {
		input.Metadata["file_path"] = path
		input.Metadata["filename"] = filepath.Base(path)
	}
	if url != "" {
		input.Metadata["url"] = url
	}
	return input, nil
}

func inferImageAnalyzeModality(path, mimeType string) multimodal.Modality {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if strings.EqualFold(mimeType, "application/pdf") || strings.EqualFold(filepath.Ext(path), ".pdf") {
		return multimodal.ModalityDocument
	}
	return multimodal.ModalityImage
}

func formatImageAnalysisResult(result *multimodal.AnalysisResult) string {
	if result == nil {
		return "Image analysis unavailable."
	}

	lines := []string{
		fmt.Sprintf("Modality: %s", result.Modality),
	}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		lines = append(lines, "Summary: "+summary)
	}
	if text := strings.TrimSpace(result.Text); text != "" {
		lines = append(lines, "Visible text / analysis:")
		lines = append(lines, utils.Truncate(text, 4000))
	}
	if len(result.Labels) > 0 {
		lines = append(lines, "Labels: "+strings.Join(result.Labels, ", "))
	}
	if result.Confidence > 0 {
		lines = append(lines, fmt.Sprintf("Confidence: %.2f", result.Confidence))
	}
	if result.Metadata != nil {
		if model := strings.TrimSpace(result.Metadata["model"]); model != "" {
			lines = append(lines, "Model: "+model)
		}
		if source := strings.TrimSpace(result.Metadata["source"]); source != "" {
			lines = append(lines, "Source: "+source)
		}
	}
	return strings.Join(lines, "\n")
}

// LogTailTool returns the last N lines of a log file.
func LogTailTool() *Tool {
	return &Tool{
		Name:        "log_tail",
		Description: "Read the tail of a local log file. Use this when debugging runtime failures, service errors, or recent events near the end of a log.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":  {Type: "string", Description: "Path to the log file.", Required: true},
			"lines": {Type: "number", Description: "Number of trailing lines to return (default 100, max 500).", Required: false, Default: 100},
		},
		Handler:      handleLogTail,
		ParallelSafe: true,
	}
}

func handleLogTail(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	lines := boundedIntArg(args, "lines", 100, 1, 500)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read log file: %w", err)
	}
	parts := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	start := 0
	if len(parts) > lines {
		start = len(parts) - lines
	}
	return strings.Join(parts[start:], "\n"), nil
}

// LogGrepTool searches a log file and returns matching lines with context.
func LogGrepTool() *Tool {
	return &Tool{
		Name:        "log_grep",
		Description: "Search a local log file for a string or regex and return surrounding context. Use this to locate stack traces, error bursts, or request IDs.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":        {Type: "string", Description: "Path to the log file.", Required: true},
			"pattern":     {Type: "string", Description: "Substring or regular expression to search for.", Required: true},
			"regex":       {Type: "boolean", Description: "Treat pattern as a regular expression.", Required: false, Default: false},
			"before":      {Type: "number", Description: "Lines of context before each match (default 2).", Required: false, Default: 2},
			"after":       {Type: "number", Description: "Lines of context after each match (default 2).", Required: false, Default: 2},
			"max_matches": {Type: "number", Description: "Maximum number of matches to return (default 20).", Required: false, Default: 20},
		},
		Handler:      handleLogGrep,
		ParallelSafe: true,
	}
}

func handleLogGrep(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	pattern, ok := args["pattern"].(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	useRegex, _ := args["regex"].(bool)
	before := boundedIntArg(args, "before", 2, 0, 20)
	after := boundedIntArg(args, "after", 2, 0, 20)
	maxMatches := boundedIntArg(args, "max_matches", 20, 1, 100)

	var re *regexp.Regexp
	var err error
	if useRegex {
		re, err = regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("compile regex: %w", err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read log file: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	var b strings.Builder
	matches := 0
	lastIncluded := -1
	for i, line := range lines {
		matched := false
		if useRegex {
			matched = re.MatchString(line)
		} else {
			matched = strings.Contains(line, pattern)
		}
		if !matched {
			continue
		}
		matches++
		if matches > maxMatches {
			break
		}
		start := maxInt(0, i-before)
		end := minInt(len(lines), i+after+1)
		if b.Len() > 0 {
			b.WriteString("\n---\n")
		}
		for j := start; j < end; j++ {
			if j <= lastIncluded {
				continue
			}
			prefix := "  "
			if j == i {
				prefix = "> "
			}
			b.WriteString(fmt.Sprintf("%s%d| %s\n", prefix, j+1, lines[j]))
			lastIncluded = j
		}
	}
	if matches == 0 {
		return "", fmt.Errorf("pattern not found in %s", path)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// HTTPRequestTool performs a controlled HTTP request and returns the response body.
func HTTPRequestTool() *Tool {
	return &Tool{
		Name:        "http_request",
		Description: "Send an HTTP request to a public URL and return the response. Use this for JSON APIs or endpoints that are not best handled by web_fetch.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"url":          {Type: "string", Description: "HTTP or HTTPS URL to request.", Required: true},
			"method":       {Type: "string", Description: "HTTP method such as GET, POST, PUT, PATCH, DELETE. Default GET.", Required: false, Default: "GET"},
			"headers_json": {Type: "string", Description: "Optional JSON object of request headers.", Required: false},
			"body":         {Type: "string", Description: "Optional request body.", Required: false},
			"timeout":      {Type: "number", Description: "Timeout in seconds (default 15, max 60).", Required: false, Default: 15},
		},
		Handler:      handleHTTPRequest,
		ParallelSafe: true,
	}
}

func handleHTTPRequest(args map[string]any) (string, error) {
	rawURL, ok := args["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return "", fmt.Errorf("url is required")
	}
	if err := validateFetchURL(rawURL); err != nil {
		return "", err
	}
	method := "GET"
	if m, ok := args["method"].(string); ok && strings.TrimSpace(m) != "" {
		method = strings.ToUpper(strings.TrimSpace(m))
	}
	timeout := boundedIntArg(args, "timeout", 15, 1, 60)
	body, _ := args["body"].(string)

	req, err := http.NewRequestWithContext(context.Background(), method, rawURL, strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "luckyharness-http-request")

	if rawHeaders, ok := args["headers_json"].(string); ok && strings.TrimSpace(rawHeaders) != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(rawHeaders), &headers); err != nil {
			return "", fmt.Errorf("parse headers_json: %w", err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	bodyText := strings.TrimSpace(string(data))
	if json.Valid(data) {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, data, "", "  "); err == nil {
			bodyText = pretty.String()
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Status: %s\n", resp.Status))
	if ct := strings.TrimSpace(resp.Header.Get("Content-Type")); ct != "" {
		b.WriteString("Content-Type: " + ct + "\n")
	}
	if bodyText != "" {
		b.WriteString("\n")
		b.WriteString(utils.Truncate(bodyText, 12000))
	}
	return strings.TrimSpace(b.String()), nil
}

// JSONQueryTool extracts a nested field from a JSON document using dot-path syntax.
func JSONQueryTool() *Tool {
	return &Tool{
		Name:        "json_query",
		Description: "Read a JSON file and extract a nested value using dot-path syntax like items[0].name.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":  {Type: "string", Description: "Path to the JSON file.", Required: true},
			"query": {Type: "string", Description: "Dot-path query such as user.name or items[0].id. Leave empty to pretty-print the full document.", Required: false},
		},
		Handler:      handleJSONQuery,
		ParallelSafe: true,
	}
}

func handleJSONQuery(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read json file: %w", err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse json: %w", err)
	}
	query, _ := args["query"].(string)
	return queryStructuredValue(doc, query)
}

// YAMLQueryTool extracts a nested field from a YAML document using dot-path syntax.
func YAMLQueryTool() *Tool {
	return &Tool{
		Name:        "yaml_query",
		Description: "Read a YAML file and extract a nested value using dot-path syntax like spec.template.metadata.name.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":  {Type: "string", Description: "Path to the YAML file.", Required: true},
			"query": {Type: "string", Description: "Dot-path query such as metadata.name or items[0].id. Leave empty to pretty-print the full document.", Required: false},
		},
		Handler:      handleYAMLQuery,
		ParallelSafe: true,
	}
}

func handleYAMLQuery(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read yaml file: %w", err)
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse yaml: %w", err)
	}
	return queryStructuredValue(normalizeYAMLValue(doc), args["query"])
}

// CSVQueryTool returns rows from a CSV file, optionally filtered by one column.
func CSVQueryTool() *Tool {
	return &Tool{
		Name:        "csv_query",
		Description: "Read a CSV file and optionally filter rows by one column equality match.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":   {Type: "string", Description: "Path to the CSV file.", Required: true},
			"column": {Type: "string", Description: "Optional column name to filter or project.", Required: false},
			"equals": {Type: "string", Description: "Optional exact string to match in the chosen column.", Required: false},
			"limit":  {Type: "number", Description: "Maximum number of rows to return (default 20, max 100).", Required: false, Default: 20},
		},
		Handler:      handleCSVQuery,
		ParallelSafe: true,
	}
}

func handleCSVQuery(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	limit := boundedIntArg(args, "limit", 20, 1, 100)
	column, _ := args["column"].(string)
	equals, _ := args["equals"].(string)

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open csv file: %w", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	rows, err := reader.ReadAll()
	if err != nil {
		return "", fmt.Errorf("read csv: %w", err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("csv is empty")
	}
	headers := rows[0]
	colIdx := -1
	if strings.TrimSpace(column) != "" {
		for i, h := range headers {
			if h == column {
				colIdx = i
				break
			}
		}
		if colIdx < 0 {
			return "", fmt.Errorf("column %q not found", column)
		}
	}

	var out []map[string]string
	for _, row := range rows[1:] {
		if colIdx >= 0 && strings.TrimSpace(equals) != "" {
			if colIdx >= len(row) || row[colIdx] != equals {
				continue
			}
		}
		entry := make(map[string]string, len(headers))
		for i, h := range headers {
			if i < len(row) {
				entry[h] = row[i]
			} else {
				entry[h] = ""
			}
		}
		out = append(out, entry)
		if len(out) >= limit {
			break
		}
	}
	return prettyStructuredValue(out)
}

// SQLQueryTool executes a read-only SQL query against a local SQLite database.
func SQLQueryTool() *Tool {
	return &Tool{
		Name:        "sql_query",
		Description: "Execute a read-only SQL query against a local SQLite database file.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"path":  {Type: "string", Description: "Path to the SQLite database file.", Required: true},
			"query": {Type: "string", Description: "Read-only SQL query (SELECT, WITH, PRAGMA, EXPLAIN).", Required: true},
			"limit": {Type: "number", Description: "Maximum number of rows to return (default 50, max 200).", Required: false, Default: 50},
		},
		Handler:      handleSQLQuery,
		ParallelSafe: true,
	}
}

func handleSQLQuery(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	if !isReadOnlySQL(query) {
		return "", fmt.Errorf("only read-only queries are allowed")
	}
	limit := boundedIntArg(args, "limit", 50, 1, 200)

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return "", fmt.Errorf("open sqlite database: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(query)
	if err != nil {
		return "", fmt.Errorf("query sqlite database: %w", err)
	}
	defer rows.Close()

	result, err := scanSQLRows(rows, limit)
	if err != nil {
		return "", err
	}
	return prettyStructuredValue(result)
}

// DBSchemaTool inspects the schema of a local SQLite database.
func DBSchemaTool() *Tool {
	return &Tool{
		Name:        "db_schema",
		Description: "Inspect the schema of a local SQLite database, including tables and columns.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":  {Type: "string", Description: "Path to the SQLite database file.", Required: true},
			"table": {Type: "string", Description: "Optional specific table name.", Required: false},
		},
		Handler:      handleDBSchema,
		ParallelSafe: true,
	}
}

func handleDBSchema(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	table, _ := args["table"].(string)

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return "", fmt.Errorf("open sqlite database: %w", err)
	}
	defer db.Close()

	if strings.TrimSpace(table) != "" {
		cols, err := sqliteTableSchema(db, table)
		if err != nil {
			return "", err
		}
		return prettyStructuredValue(map[string]any{
			"table":   table,
			"columns": cols,
		})
	}

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return "", fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	var tables []map[string]any
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", fmt.Errorf("scan table name: %w", err)
		}
		cols, err := sqliteTableSchema(db, name)
		if err != nil {
			return "", err
		}
		tables = append(tables, map[string]any{
			"name":    name,
			"columns": cols,
		})
	}
	return prettyStructuredValue(map[string]any{
		"tables": tables,
	})
}

func boundedIntArg(args map[string]any, key string, def, minValue, maxValue int) int {
	value := def
	if raw, ok := args[key]; ok {
		switch v := raw.(type) {
		case float64:
			value = int(v)
		case int:
			value = v
		}
	}
	if value < minValue {
		value = minValue
	}
	if value > maxValue {
		value = maxValue
	}
	return value
}

func prettyStructuredValue(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(data), nil
}

func queryStructuredValue(doc any, query any) (string, error) {
	queryText, _ := query.(string)
	queryText = strings.TrimSpace(queryText)
	if queryText == "" {
		return prettyStructuredValue(doc)
	}
	value, err := walkStructuredPath(doc, queryText)
	if err != nil {
		return "", err
	}
	return prettyStructuredValue(value)
}

func normalizeYAMLValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = normalizeYAMLValue(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[fmt.Sprint(k)] = normalizeYAMLValue(val)
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, normalizeYAMLValue(item))
		}
		return out
	default:
		return v
	}
}

func walkStructuredPath(doc any, query string) (any, error) {
	current := doc
	for _, token := range parseStructuredPath(query) {
		if token.key != "" {
			obj, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("path %q expected object", token.key)
			}
			next, ok := obj[token.key]
			if !ok {
				return nil, fmt.Errorf("path key %q not found", token.key)
			}
			current = next
		}
		if token.hasIndex {
			arr, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("path index %d expected array", token.index)
			}
			if token.index < 0 || token.index >= len(arr) {
				return nil, fmt.Errorf("path index %d out of range", token.index)
			}
			current = arr[token.index]
		}
	}
	return current, nil
}

type structuredPathToken struct {
	key      string
	index    int
	hasIndex bool
}

func parseStructuredPath(query string) []structuredPathToken {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	parts := strings.Split(query, ".")
	out := make([]structuredPathToken, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		token := structuredPathToken{}
		if idx := strings.Index(part, "["); idx >= 0 && strings.HasSuffix(part, "]") {
			token.key = part[:idx]
			rawIndex := part[idx+1 : len(part)-1]
			if n, err := strconv.Atoi(rawIndex); err == nil {
				token.index = n
				token.hasIndex = true
			}
		} else {
			token.key = part
		}
		out = append(out, token)
	}
	return out
}

func isReadOnlySQL(query string) bool {
	q := strings.TrimSpace(strings.ToLower(query))
	switch {
	case strings.HasPrefix(q, "select "),
		strings.HasPrefix(q, "with "),
		strings.HasPrefix(q, "pragma "),
		strings.HasPrefix(q, "explain "):
		return true
	default:
		return false
	}
}

func scanSQLRows(rows *sql.Rows, limit int) ([]map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("read columns: %w", err)
	}
	result := make([]map[string]any, 0, limit)
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		entry := make(map[string]any, len(columns))
		for i, col := range columns {
			entry[col] = normalizeSQLValue(values[i])
		}
		result = append(result, entry)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func normalizeSQLValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return x
	}
}

func sqliteTableSchema(db *sql.DB, table string) ([]map[string]any, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", sqliteQuoteIdentifier(table)))
	if err != nil {
		return nil, fmt.Errorf("describe table %s: %w", table, err)
	}
	defer rows.Close()

	var cols []map[string]any
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan schema row: %w", err)
		}
		cols = append(cols, map[string]any{
			"cid":      cid,
			"name":     name,
			"type":     colType,
			"not_null": notNull == 1,
			"default":  dflt.String,
			"primary":  pk == 1,
		})
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("table %q not found or has no visible columns", table)
	}
	return cols, nil
}

func sqliteQuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(name), `"`, `""`) + `"`
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// validatePath 路径安全检查（防止路径遍历 + 沙箱限制）
func validatePath(path string) error {
	// 清理路径
	clean := filepath.Clean(path)

	// 检查路径遍历
	if strings.Contains(clean, "..") {
		return fmt.Errorf("path traversal detected: %s", path)
	}

	// 沙箱限制：只允许访问特定目录
	return validateSandbox(clean)
}

// validateSandbox 检查路径是否在允许的沙箱范围内
func validateSandbox(cleanPath string) error {
	// 解析为绝对路径
	absPath := cleanPath
	if !filepath.IsAbs(absPath) {
		// 相对路径基于当前工作目录解析
		if wd, err := os.Getwd(); err == nil {
			absPath = filepath.Join(wd, absPath)
		}
	}
	absPath = filepath.Clean(absPath)

	// 获取用户 home 目录
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/root"
	}

	// 允许的路径前缀
	allowedPrefixes := []string{
		filepath.Join(home, ".luckyharness"), // LuckyHarness 自身目录
		"/tmp",                               // 临时文件
		"/dev/null",                          // 空设备
	}
	if base := filepath.Base(home); base == ".lh-home" {
		allowedPrefixes = append(allowedPrefixes, home)
	}

	// 禁止的路径前缀（即使在上面的允许列表下也拦截）
	deniedPrefixes := []string{
		filepath.Join(home, ".nanobot"),       // nanobot 配置
		filepath.Join(home, ".ssh"),           // SSH 密钥
		filepath.Join(home, ".gnupg"),         // GPG 密钥
		filepath.Join(home, ".aws"),           // AWS 凭证
		filepath.Join(home, ".config/gcloud"), // GCP 凭证
		"/etc/shadow",                         // 系统密码
		"/etc/ssh",                            // SSH 配置
	}

	// 先检查禁止列表
	for _, denied := range deniedPrefixes {
		if strings.HasPrefix(absPath, denied) || absPath == denied {
			return fmt.Errorf("access denied: path is outside sandbox (%s)", cleanPath)
		}
	}

	// 再检查允许列表
	for _, allowed := range allowedPrefixes {
		if strings.HasPrefix(absPath, allowed) || absPath == allowed {
			return nil
		}
	}

	return fmt.Errorf("access denied: path is outside sandbox (allowed: ~/.luckyharness/, /tmp/). Requested: %s", cleanPath)
}

// validateShellSandbox 检查 shell 命令是否试图访问禁止路径
func validateShellSandbox(command string) error {
	// 禁止在 shell 命令中引用的路径模式
	deniedPatterns := []string{
		".nanobot",
		".ssh/",
		".gnupg/",
		".aws/",
		"/etc/shadow",
		"/etc/ssh/",
		"config.json", // nanobot 配置文件
	}

	lowerCmd := strings.ToLower(command)
	for _, pattern := range deniedPatterns {
		if strings.Contains(lowerCmd, strings.ToLower(pattern)) {
			return fmt.Errorf("access denied: command references restricted path (%s)", pattern)
		}
	}

	// 禁止的环境变量读取
	deniedEnvVars := []string{
		"FILEBROWSER_",
		"NANOBOT_",
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
	}
	for _, envVar := range deniedEnvVars {
		if strings.Contains(lowerCmd, strings.ToLower(envVar)) {
			return fmt.Errorf("access denied: command references restricted environment variable (%s)", envVar)
		}
	}

	return nil
}

// RememberTool 保存记忆工具
func RememberTool(handler func(args map[string]any) (string, error)) *Tool {
	if handler == nil {
		handler = func(args map[string]any) (string, error) {
			return "", fmt.Errorf("remember handler not configured")
		}
	}
	return &Tool{
		Name:        "remember",
		Description: "Persist stable user facts, preferences, recurring project context, or other reusable conclusions that should help future conversations.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto, // 记忆操作自动批准
		Parameters: map[string]Param{
			"content": {
				Type:        "string",
				Description: "Stable fact or reusable note to remember. Keep it concise, concrete, and worth recalling later.",
				Required:    true,
			},
			"category": {
				Type:        "string",
				Description: "Optional category such as identity, preference, project, knowledge, or conversation.",
				Required:    false,
				Default:     "conversation",
			},
			"long_term": {
				Type:        "boolean",
				Description: "Set true only for durable core facts like identity, strong preferences, or long-lived project constraints.",
				Required:    false,
				Default:     false,
			},
		},
		Handler:      handler,
		ParallelSafe: false,
	}
}

// RecallTool 搜索记忆工具
func RecallTool(handler func(args map[string]any) (string, error)) *Tool {
	if handler == nil {
		handler = func(args map[string]any) (string, error) {
			return "", fmt.Errorf("recall handler not configured")
		}
	}
	return &Tool{
		Name:        "recall",
		Description: "Search saved memory for durable user preferences, prior project facts, or previously stored conclusions before asking again or guessing.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"query": {
				Type:        "string",
				Description: "Query for the fact or preference you want to recover. Leave empty to inspect recent memories.",
				Required:    false,
			},
		},
		Handler:      handler,
		ParallelSafe: true,
	}
}

// RAGSearchTool searches the local indexed knowledge base.
func RAGSearchTool(handler func(args map[string]any) (string, error)) *Tool {
	if handler == nil {
		handler = func(args map[string]any) (string, error) {
			return "", fmt.Errorf("rag_search handler not configured")
		}
	}
	return &Tool{
		Name:        "rag_search",
		Description: "Search the local indexed knowledge base when the answer is likely in previously indexed documents, notes, or archived final answers.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"query": {
				Type:        "string",
				Description: "Semantic query describing the fact, topic, identifier, or phrase you want to retrieve from indexed knowledge.",
				Required:    true,
			},
			"top_k": {
				Type:        "number",
				Description: "Maximum number of relevant passages to return.",
				Required:    false,
				Default:     5,
			},
		},
		Handler:      handler,
		ParallelSafe: true,
	}
}

// RAGIndexTool indexes a file or directory into the local knowledge base.
func RAGIndexTool(handler func(args map[string]any) (string, error)) *Tool {
	if handler == nil {
		handler = func(args map[string]any) (string, error) {
			return "", fmt.Errorf("rag_index handler not configured")
		}
	}
	return &Tool{
		Name:        "rag_index",
		Description: "Index a local file or directory into the knowledge base so its contents can be retrieved later through semantic search.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"path": {
				Type:        "string",
				Description: "Local file or directory to add to the indexed knowledge base.",
				Required:    true,
			},
		},
		Handler:      handler,
		ParallelSafe: false,
	}
}
