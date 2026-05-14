package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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

	"github.com/yurika0211/luckyharness/internal/multimodal"
	searchpkg "github.com/yurika0211/luckyharness/internal/tool/search"
	"github.com/yurika0211/luckyharness/internal/utils"
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
