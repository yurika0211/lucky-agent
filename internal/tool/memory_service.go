package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/utils"
)

const (
	maxMemoryToolMetadataItems = 20
	maxMemoryToolMetadataRunes = 80
	defaultRecallSearchLimit   = 10
	defaultRecallRecentLimit   = 5
	maxRecallLimit             = 50
	maxRecallQueryRunes        = 1000
	defaultRecallGraphDepth    = 1
	maxRecallGraphDepth        = 3
	defaultHygieneLimit        = 50
	maxHygieneLimit            = 200
)

// MemoryToolService implements remember/recall handlers in the tool layer.
type MemoryToolService struct {
	store *memory.Store
}

// NewMemoryToolService creates a tool-layer memory service.
func NewMemoryToolService(store *memory.Store) *MemoryToolService {
	return &MemoryToolService{store: store}
}

func (s *MemoryToolService) HandleRemember(args map[string]any) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("memory store not initialized")
	}
	content, _ := args["content"].(string)
	category, _ := args["category"].(string)
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("content is required")
	}
	if err := rejectDirtyMemoryContent(content); err != nil {
		return "", err
	}
	categoryInferred := false
	if category == "" {
		category = inferMemoryCategory(content)
		categoryInferred = true
	}
	tier := memory.TierMedium
	importance := 0.5
	longTerm, _ := args["long_term"].(bool)
	if longTerm {
		tier = memory.TierLong
		importance = 0.9
	}
	if rawTier, _ := args["tier"].(string); rawTier != "" {
		tier = parseMemoryToolTier(rawTier)
		if _, ok := args["importance"]; !ok {
			importance = defaultImportanceForTier(tier)
		}
	}
	if rawImportance, ok := numberArg(args["importance"]); ok {
		importance = clamp01(rawImportance)
	}
	tags, err := normalizeMemoryMetadataArg("tags", stringSliceArg(args["tags"]))
	if err != nil {
		return "", err
	}
	links, err := normalizeMemoryMetadataArg("links", stringSliceArg(args["links"]))
	if err != nil {
		return "", err
	}
	aliases, err := normalizeMemoryMetadataArg("aliases", stringSliceArg(args["aliases"]))
	if err != nil {
		return "", err
	}
	routePolicies, err := routePoliciesArg(args["route_policies"])
	if err != nil {
		return "", err
	}
	opts := memory.SaveOptions{
		Tags:          tags,
		Links:         links,
		Aliases:       aliases,
		Status:        stringArg(args["status"]),
		StateKey:      stringArg(args["state_key"]),
		StateValue:    stringArg(args["state_value"]),
		Supersedes:    stringSliceArg(args["supersedes"]),
		RoutePolicies: routePolicies,
	}
	if confidence, ok := numberArg(args["confidence"]); ok {
		opts.Confidence = clamp01(confidence)
	}
	if tier == memory.TierLong && opts.Confidence > 0 && opts.Confidence < 0.35 {
		return "", fmt.Errorf("long-term memory with confidence below 0.35 is rejected; save a medium-tier note or confirm stronger evidence")
	}
	if validFrom, ok, err := timeArg(args["valid_from"]); err != nil {
		return "", err
	} else if ok {
		opts.ValidFrom = validFrom
	}
	if validUntil, ok, err := timeArg(args["valid_until"]); err != nil {
		return "", err
	} else if ok {
		opts.ValidUntil = &validUntil
	}

	mode := strings.ToLower(strings.TrimSpace(stringArg(args["mode"])))
	if mode == "" {
		mode = "append"
	}
	if mode != "append" && mode != "upsert_state" && mode != "supersede" {
		return "", fmt.Errorf("mode must be append, upsert_state, or supersede")
	}
	if mode == "supersede" && len(opts.Supersedes) == 0 {
		return "", fmt.Errorf("mode=supersede requires supersedes")
	}
	var superseded []string
	if mode == "upsert_state" {
		if opts.StateKey == "" {
			return "", fmt.Errorf("mode=upsert_state requires state_key")
		}
		superseded = s.store.ActiveStateIDs(opts.StateKey)
		opts.Supersedes = append(opts.Supersedes, superseded...)
	}

	result, err := s.store.SaveWithOptionsResult(content, category, tier, importance, opts)
	if err != nil {
		return "", err
	}
	if mode == "upsert_state" && len(superseded) > 0 {
		filtered := make([]string, 0, len(superseded))
		for _, id := range superseded {
			if id != result.ID {
				filtered = append(filtered, id)
			}
		}
		superseded, err = s.store.SupersedeEntries(filtered)
		if err != nil {
			return "", err
		}
	}
	format := strings.ToLower(strings.TrimSpace(stringArg(args["format"])))
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return "", fmt.Errorf("format must be text or json")
	}
	if format == "json" {
		return formatRememberJSON(memoryRememberResponse{
			Saved:            true,
			ID:               result.ID,
			Path:             result.Path,
			Created:          result.Created,
			UpdatedExisting:  result.UpdatedExisting,
			DuplicateOf:      result.DuplicateOf,
			Category:         category,
			CategoryInferred: categoryInferred,
			Tier:             tier.String(),
			Importance:       importance,
			Links:            links,
			Aliases:          aliases,
			Tags:             tags,
			Superseded:       superseded,
		})
	}
	action := "已保存"
	if result.UpdatedExisting {
		action = "已更新已有"
	}
	return fmt.Sprintf("✅ %s为%s记忆 [%s] 到 LuckyAgent Markdown 记忆库 %s: %s", action, memoryTierLabel(tier), category, memoryVaultPathForTool(s.store), utils.Truncate(content, 80)), nil
}

type memoryRememberResponse struct {
	Saved            bool     `json:"saved"`
	ID               string   `json:"id,omitempty"`
	Path             string   `json:"path,omitempty"`
	Created          bool     `json:"created"`
	UpdatedExisting  bool     `json:"updated_existing"`
	DuplicateOf      string   `json:"duplicate_of,omitempty"`
	Category         string   `json:"category"`
	CategoryInferred bool     `json:"category_inferred"`
	Tier             string   `json:"tier"`
	Importance       float64  `json:"importance"`
	Links            []string `json:"links,omitempty"`
	Aliases          []string `json:"aliases,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	Superseded       []string `json:"superseded,omitempty"`
}

func formatRememberJSON(payload memoryRememberResponse) (string, error) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func rejectDirtyMemoryContent(content string) error {
	for _, issue := range memory.AnalyzeMemoryContent(content) {
		switch issue.Reason {
		case "empty":
			return fmt.Errorf("content is required")
		case "raw_conversation":
			return fmt.Errorf("raw conversation content is rejected; save a concise stable summary instead")
		case "secret_like":
			return fmt.Errorf("secret-like content is rejected and must not be stored in durable memory")
		case "prompt_injection":
			return fmt.Errorf("prompt-injection-like content is rejected and must not be stored in durable memory")
		case "oversized":
			return fmt.Errorf("content exceeds %d rune limit; summarize it or index the source with rag_index", memory.MaxDurableMemoryContentRunes)
		}
	}
	return nil
}

func normalizeMemoryMetadataArg(name string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len([]rune(value)) > maxMemoryToolMetadataRunes {
			return nil, fmt.Errorf("%s item exceeds %d rune limit", name, maxMemoryToolMetadataRunes)
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return nil, fmt.Errorf("%s item contains control characters", name)
			}
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) > maxMemoryToolMetadataItems {
			return nil, fmt.Errorf("%s exceeds maximum %d items", name, maxMemoryToolMetadataItems)
		}
	}
	return out, nil
}

func (s *MemoryToolService) HandleRecall(args map[string]any) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("memory store not initialized")
	}
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if len([]rune(query)) > maxRecallQueryRunes {
		return "", fmt.Errorf("query exceeds %d rune limit", maxRecallQueryRunes)
	}
	mode := strings.ToLower(strings.TrimSpace(stringArg(args["mode"])))
	if mode == "" {
		if query == "" {
			mode = "recent"
		} else {
			mode = "search"
		}
	}
	if mode != "recent" && mode != "search" {
		return "", fmt.Errorf("mode must be recent or search")
	}
	if mode == "search" && query == "" {
		return "", fmt.Errorf("query is required when mode=search")
	}
	limitDefault := defaultRecallSearchLimit
	if mode == "recent" {
		limitDefault = defaultRecallRecentLimit
	}
	limit, err := boundedMemoryIntArg(args["limit"], limitDefault, 1, maxRecallLimit, "limit")
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
	var tierPtr *memory.Tier
	if rawTier := stringArg(args["tier"]); rawTier != "" {
		tier, err := parseMemoryToolTierStrict(rawTier)
		if err != nil {
			return "", err
		}
		tierPtr = &tier
	}
	var asOf time.Time
	if parsed, ok, err := timeArg(args["as_of"]); err != nil {
		return "", err
	} else if ok {
		asOf = parsed
	}
	graphDepth, err := boundedMemoryIntArg(args["graph_depth"], defaultRecallGraphDepth, 0, maxRecallGraphDepth, "graph_depth")
	if err != nil {
		return "", err
	}
	if query == "" {
		recent := s.store.Recent(limit)
		recent = filterRecallEntries(recent, stringArg(args["category"]), tierPtr)
		if len(recent) > limit {
			recent = recent[:limit]
		}
		if format == "json" {
			return formatRecallRecentJSON(memoryVaultPathForTool(s.store), recent)
		}
		if len(recent) == 0 {
			return "没有找到记忆", nil
		}
		var sb strings.Builder
		sb.WriteString(memorySourceNotice(s.store))
		sb.WriteString("最近的记忆：\n")
		for _, e := range recent {
			sb.WriteString(fmt.Sprintf("- [%s/%s] %s\n", e.Category, e.Tier.String(), utils.Truncate(e.Content, 80)))
		}
		return sb.String(), nil
	}
	results := s.store.SearchWithOptions(query, memory.SearchOptions{
		Limit:           limit,
		Category:        stringArg(args["category"]),
		Tier:            tierPtr,
		IncludeInactive: boolArg(args["include_inactive"]),
		IncludeExpired:  boolArg(args["include_expired"]),
		AsOf:            asOf,
		IncludeGraph:    graphDepth > 0,
		GraphDepth:      graphDepth,
		Explain:         boolArg(args["explain_graph"]),
	})
	if format == "json" {
		return formatRecallSearchJSON(memoryVaultPathForTool(s.store), query, graphDepth, results)
	}
	if len(results) == 0 {
		return fmt.Sprintf("没有找到关于「%s」的记忆", query), nil
	}
	var sb strings.Builder
	sb.WriteString(memorySourceNotice(s.store))
	sb.WriteString(fmt.Sprintf("找到 %d 条关于「%s」的记忆：\n", len(results), query))
	for i := 0; i < len(results); i++ {
		result := results[i]
		e := result.Entry
		ref := ""
		if e.Path != "" {
			ref = " @" + e.Path
			if e.BlockID != "" {
				ref += "#" + e.BlockID
			}
		}
		graph := ""
		if len(e.Links) > 0 {
			graph = " links=" + strings.Join(limitStrings(e.Links, 4), ",")
		}
		score := ""
		if result.Score > 0 {
			score = fmt.Sprintf(" score=%0.2f", result.Score)
		}
		sb.WriteString(fmt.Sprintf("- [%s/%s%s%s%s] %s\n", e.Category, e.Tier.String(), score, graph, ref, utils.Truncate(e.Content, 100)))
	}
	return sb.String(), nil
}

func (s *MemoryToolService) HandleRecallDetailed(args map[string]any) (ToolCallResult, error) {
	out, err := s.HandleRecall(args)
	if err != nil {
		return ToolCallResult{}, err
	}
	trace, ok, err := s.buildRecallTrace(args)
	if err != nil {
		return ToolCallResult{Output: out}, nil
	}
	if !ok {
		return ToolCallResult{Output: out}, nil
	}
	return ToolCallResult{
		Output:   out,
		Metadata: map[string]any{"memory_trace": trace},
	}, nil
}

func (s *MemoryToolService) buildRecallTrace(args map[string]any) (memory.SearchTrace, bool, error) {
	if s == nil || s.store == nil {
		return memory.SearchTrace{}, false, nil
	}
	query := strings.TrimSpace(stringArg(args["query"]))
	if query == "" {
		return memory.SearchTrace{}, false, nil
	}
	mode := strings.ToLower(strings.TrimSpace(stringArg(args["mode"])))
	if mode == "" {
		mode = "search"
	}
	if mode != "search" {
		return memory.SearchTrace{}, false, nil
	}
	limit, err := boundedMemoryIntArg(args["limit"], defaultRecallSearchLimit, 1, maxRecallLimit, "limit")
	if err != nil {
		return memory.SearchTrace{}, false, err
	}
	var tierPtr *memory.Tier
	if rawTier := stringArg(args["tier"]); rawTier != "" {
		tier, err := parseMemoryToolTierStrict(rawTier)
		if err != nil {
			return memory.SearchTrace{}, false, err
		}
		tierPtr = &tier
	}
	var asOf time.Time
	if parsed, ok, err := timeArg(args["as_of"]); err != nil {
		return memory.SearchTrace{}, false, err
	} else if ok {
		asOf = parsed
	}
	graphDepth, err := boundedMemoryIntArg(args["graph_depth"], defaultRecallGraphDepth, 0, maxRecallGraphDepth, "graph_depth")
	if err != nil {
		return memory.SearchTrace{}, false, err
	}
	opts := memory.SearchOptions{
		Limit:           limit,
		Category:        stringArg(args["category"]),
		Tier:            tierPtr,
		IncludeInactive: boolArg(args["include_inactive"]),
		IncludeExpired:  boolArg(args["include_expired"]),
		AsOf:            asOf,
		IncludeGraph:    graphDepth > 0,
		GraphDepth:      graphDepth,
		Explain:         true,
		SkipAccessStats: true,
	}
	start := time.Now()
	results := s.store.SearchWithOptions(query, opts)
	trace := s.store.BuildSearchTrace(query, mode, memoryVaultPathForTool(s.store), opts, results, time.Since(start))
	return trace, true, nil
}

type recallEntryJSON struct {
	ID          string                  `json:"id"`
	Category    string                  `json:"category"`
	Tier        string                  `json:"tier"`
	Content     string                  `json:"content"`
	Path        string                  `json:"path,omitempty"`
	BlockID     string                  `json:"block_id,omitempty"`
	Links       []string                `json:"links,omitempty"`
	Status      string                  `json:"status,omitempty"`
	Confidence  float64                 `json:"confidence,omitempty"`
	Score       float64                 `json:"score,omitempty"`
	DirectScore float64                 `json:"direct_score,omitempty"`
	GraphScore  float64                 `json:"graph_score,omitempty"`
	Paths       []memory.ActivationPath `json:"paths,omitempty"`
}

func formatRecallSearchJSON(source, query string, graphDepth int, results []memory.SearchResult) (string, error) {
	payload := struct {
		Source     string            `json:"source"`
		Query      string            `json:"query"`
		GraphDepth int               `json:"graph_depth"`
		Count      int               `json:"count"`
		Results    []recallEntryJSON `json:"results"`
	}{
		Source:     source,
		Query:      query,
		GraphDepth: graphDepth,
		Count:      len(results),
		Results:    make([]recallEntryJSON, 0, len(results)),
	}
	for _, result := range results {
		payload.Results = append(payload.Results, recallEntryToJSON(result.Entry, result))
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func formatRecallRecentJSON(source string, entries []memory.Entry) (string, error) {
	payload := struct {
		Source  string            `json:"source"`
		Mode    string            `json:"mode"`
		Count   int               `json:"count"`
		Results []recallEntryJSON `json:"results"`
	}{
		Source:  source,
		Mode:    "recent",
		Count:   len(entries),
		Results: make([]recallEntryJSON, 0, len(entries)),
	}
	for _, entry := range entries {
		payload.Results = append(payload.Results, recallEntryToJSON(entry, memory.SearchResult{}))
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func recallEntryToJSON(entry memory.Entry, result memory.SearchResult) recallEntryJSON {
	return recallEntryJSON{
		ID:          entry.ID,
		Category:    entry.Category,
		Tier:        entry.Tier.String(),
		Content:     entry.Content,
		Path:        entry.Path,
		BlockID:     entry.BlockID,
		Links:       entry.Links,
		Status:      entry.Status,
		Confidence:  entry.Confidence,
		Score:       result.Score,
		DirectScore: result.DirectScore,
		GraphScore:  result.GraphScore,
		Paths:       result.Paths,
	}
}

func filterRecallEntries(entries []memory.Entry, category string, tier *memory.Tier) []memory.Entry {
	category = strings.TrimSpace(category)
	if category == "" && tier == nil {
		return entries
	}
	out := make([]memory.Entry, 0, len(entries))
	for _, entry := range entries {
		if category != "" && !strings.EqualFold(entry.Category, category) {
			continue
		}
		if tier != nil && entry.Tier != *tier {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (s *MemoryToolService) HandleHygiene(args map[string]any) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("memory store not initialized")
	}
	opts, action, dryRun, err := parseHygieneToolOptions(args)
	if err != nil {
		return "", err
	}
	var (
		report memory.HygieneReport
	)
	switch action {
	case "audit", "scan", "dry_run", "dry-run":
		report = s.store.AuditHygiene(opts)
	case "quarantine", "archive":
		if dryRun {
			report = s.store.AuditHygiene(opts)
			report.Action = "quarantine"
			report.DryRun = true
		} else {
			report, err = s.store.QuarantineDirty(opts)
		}
	case "delete", "purge":
		if !boolArg(args["confirm_delete"]) {
			return "", fmt.Errorf("delete requires confirm_delete=true; run audit first and prefer quarantine")
		}
		if dryRun {
			report = s.store.AuditHygiene(opts)
			report.Action = "delete"
			report.DryRun = true
		} else {
			report, err = s.store.DeleteDirty(opts)
		}
	case "restore":
		if dryRun {
			report = memory.HygieneReport{Action: "restore", DryRun: true}
		} else {
			report, err = s.store.RestoreHygiene(opts)
		}
	default:
		return "", fmt.Errorf("invalid memory_hygiene action %q (use audit, quarantine, delete, or restore)", action)
	}
	if err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func parseHygieneToolOptions(args map[string]any) (memory.HygieneOptions, string, bool, error) {
	action := strings.ToLower(strings.TrimSpace(stringArg(args["action"])))
	if action == "" {
		action = "audit"
	}
	minSeverity := strings.ToLower(strings.TrimSpace(stringArg(args["min_severity"])))
	if minSeverity == "" {
		minSeverity = "medium"
	}
	switch minSeverity {
	case "low", "medium", "high", "critical":
	default:
		return memory.HygieneOptions{}, "", false, fmt.Errorf("min_severity must be low, medium, high, or critical")
	}
	limit := defaultHygieneLimit
	if rawLimit, ok := numberArg(args["limit"]); ok {
		limit = int(rawLimit)
	}
	if limit < 0 {
		return memory.HygieneOptions{}, "", false, fmt.Errorf("limit must be >= 0")
	}
	if limit == 0 && !boolArg(args["allow_unlimited"]) {
		return memory.HygieneOptions{}, "", false, fmt.Errorf("limit=0 requires allow_unlimited=true")
	}
	if limit > maxHygieneLimit {
		return memory.HygieneOptions{}, "", false, fmt.Errorf("limit exceeds maximum %d", maxHygieneLimit)
	}
	return memory.HygieneOptions{
		MinSeverity:     minSeverity,
		IncludeInactive: boolArg(args["include_inactive"]),
		MaxFindings:     limit,
		IDs:             stringSliceArg(args["ids"]),
	}, action, boolArg(args["dry_run"]), nil
}

func memorySourceNotice(store *memory.Store) string {
	return fmt.Sprintf("记忆源：LuckyAgent Obsidian-compatible Markdown vault at %s。RAG SQLite 不是当前 durable memory 事实源。\n", memoryVaultPathForTool(store))
}

func memoryVaultPathForTool(store *memory.Store) string {
	if store == nil || strings.TrimSpace(store.Dir()) == "" {
		return "~/.luckyagent/memory"
	}
	return store.Dir()
}

func parseMemoryToolTier(raw string) memory.Tier {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "short", "短期":
		return memory.TierShort
	case "long", "长期":
		return memory.TierLong
	default:
		return memory.TierMedium
	}
}

func parseMemoryToolTierStrict(raw string) (memory.Tier, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "short", "短期":
		return memory.TierShort, nil
	case "medium", "中期":
		return memory.TierMedium, nil
	case "long", "长期":
		return memory.TierLong, nil
	default:
		return memory.TierMedium, fmt.Errorf("tier must be short, medium, or long")
	}
}

func defaultImportanceForTier(t memory.Tier) float64 {
	switch t {
	case memory.TierShort:
		return 0.25
	case memory.TierLong:
		return 0.9
	default:
		return 0.5
	}
}

func memoryTierLabel(t memory.Tier) string {
	switch t {
	case memory.TierShort:
		return "短期"
	case memory.TierLong:
		return "长期"
	default:
		return "中期"
	}
}

func numberArg(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		parsed, err := n.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func boundedMemoryIntArg(v any, defaultValue, minValue, maxValue int, name string) (int, error) {
	value := defaultValue
	if raw, ok := numberArg(v); ok {
		value = int(raw)
	}
	if value < minValue || value > maxValue {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minValue, maxValue)
	}
	return value, nil
}

func stringArg(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func routePoliciesArg(v any) ([]memory.RoutePolicy, error) {
	if v == nil {
		return nil, nil
	}
	var data []byte
	var err error
	switch typed := v.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, nil
		}
		data = []byte(typed)
	default:
		data, err = json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("route_policies must be valid JSON: %w", err)
		}
	}

	var policies []memory.RoutePolicy
	if err := json.Unmarshal(data, &policies); err == nil {
		return policies, nil
	}
	var policy memory.RoutePolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("route_policies must be a JSON policy object or array: %w", err)
	}
	return []memory.RoutePolicy{policy}, nil
}

func boolArg(v any, rest ...any) bool {
	if len(rest) >= 2 {
		args, _ := v.(map[string]any)
		key, _ := rest[0].(string)
		def, _ := rest[1].(bool)
		if args == nil || key == "" {
			return def
		}
		b, ok := args[key].(bool)
		if !ok {
			return def
		}
		return b
	}
	b, _ := v.(bool)
	return b
}

func timeArg(v any) (time.Time, bool, error) {
	raw := stringArg(v)
	if raw == "" {
		return time.Time{}, false, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed, true, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("invalid time %q: use RFC3339 or YYYY-MM-DD", raw)
}

func stringSliceArg(v any) []string {
	switch raw := v.(type) {
	case []string:
		return raw
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		fields := strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == '，' || r == '\n' || r == ';' || r == '；'
		})
		out := make([]string, 0, len(fields))
		for _, field := range fields {
			if strings.TrimSpace(field) != "" {
				out = append(out, strings.TrimSpace(field))
			}
		}
		return out
	default:
		return nil
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func inferMemoryCategory(input string) string {
	lower := strings.ToLower(input)

	for _, kw := range []string{"喜欢", "偏好", "prefer", "like", "想要", "习惯", "讨厌", "hate", "dislike"} {
		if strings.Contains(lower, kw) {
			return "preference"
		}
	}
	for _, kw := range []string{"项目", "project", "代码", "code", "bug", "部署", "deploy", "仓库", "repo", "pr", "merge"} {
		if strings.Contains(lower, kw) {
			return "project"
		}
	}
	for _, kw := range []string{"过敏", "花粉", "诊断", "健康", "生病", "allergy", "pollen", "health", "diagnosed"} {
		if strings.Contains(lower, kw) {
			return "health"
		}
	}
	for _, kw := range []string{"必须", "应该", "需要查询", "工具", "tool", "rule", "workflow", "路由"} {
		if strings.Contains(lower, kw) {
			return "rule"
		}
	}
	for _, kw := range []string{"城市", "地点", "位置", "住在", "location", "city"} {
		if strings.Contains(lower, kw) {
			return "location"
		}
	}
	for _, kw := range []string{"什么是", "怎么", "如何", "为什么", "what is", "how to", "why", "解释", "explain", "调研", "研究"} {
		if strings.Contains(lower, kw) {
			return "knowledge"
		}
	}
	for _, kw := range []string{"我叫", "我是", "我的名字", "my name", "i am", "住", "学校", "公司"} {
		if strings.Contains(lower, kw) {
			return "identity"
		}
	}
	return "conversation"
}
