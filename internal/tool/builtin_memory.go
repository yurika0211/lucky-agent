package tool

import "fmt"

// RememberTool 保存记忆工具
func RememberTool(handler func(args map[string]any) (string, error)) *Tool {
	if handler == nil {
		handler = func(args map[string]any) (string, error) {
			return "", fmt.Errorf("remember handler not configured")
		}
	}
	return &Tool{
		Name:        "remember",
		Description: "Persist stable user facts, preferences, recurring project context, or reusable conclusions into the LuckyAgent Obsidian-compatible Markdown memory vault.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"content": {
				Type:        "string",
				Description: "Stable fact or reusable note to remember. Keep it concise, concrete, and worth recalling later.",
				Required:    true,
			},
			"category": {
				Type:        "string",
				Description: "Optional category such as identity, preference, project, health, rule, location, plan, knowledge, or conversation.",
				Required:    false,
				Default:     "conversation",
			},
			"tier": {
				Type:        "string",
				Description: "Optional memory tier: short, medium, or long. Overrides long_term when provided.",
				Required:    false,
				Default:     "medium",
			},
			"importance": {
				Type:        "number",
				Description: "Optional importance from 0.0 to 1.0. Use high values for durable constraints.",
				Required:    false,
				Default:     0.5,
			},
			"tags": {
				Type:        "array",
				Description: "Optional short tags for filtering, for example health, family, tool-routing.",
				Required:    false,
			},
			"links": {
				Type:        "array",
				Description: "Optional Obsidian wikilink targets such as Daughter, Pollen Allergy, Outdoor Plan, Weather Forecast, or Air Quality.",
				Required:    false,
			},
			"aliases": {
				Type:        "array",
				Description: "Optional aliases that should retrieve this memory, including Chinese or English synonyms.",
				Required:    false,
			},
			"status": {
				Type:        "string",
				Description: "Optional temporal status: active, superseded, archived, or conflict.",
				Required:    false,
				Default:     "active",
			},
			"state_key": {
				Type:        "string",
				Description: "Optional stable key for facts that can change over time, for example family.daughter.pollen_allergy.",
				Required:    false,
			},
			"state_value": {
				Type:        "string",
				Description: "Optional current value for state_key, for example active, resolved, unknown, or mild.",
				Required:    false,
			},
			"confidence": {
				Type:        "number",
				Description: "Optional confidence from 0.0 to 1.0 for temporal state resolution.",
				Required:    false,
			},
			"supersedes": {
				Type:        "array",
				Description: "Optional memory IDs this note replaces.",
				Required:    false,
			},
			"valid_from": {
				Type:        "string",
				Description: "Optional RFC3339 or YYYY-MM-DD start date for when this memory becomes valid.",
				Required:    false,
			},
			"valid_until": {
				Type:        "string",
				Description: "Optional RFC3339 or YYYY-MM-DD end date after which this memory is no longer valid.",
				Required:    false,
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
		Description: "Search the LuckyAgent Obsidian-compatible Markdown memory vault for durable user preferences, prior project facts, or stored conclusions before asking again or guessing.",
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

// MemoryHygieneTool audits or cleans dirty memories from the durable vault.
func MemoryHygieneTool(handler func(args map[string]any) (string, error)) *Tool {
	if handler == nil {
		handler = func(args map[string]any) (string, error) {
			return "", fmt.Errorf("memory_hygiene handler not configured")
		}
	}
	return &Tool{
		Name:        "memory_hygiene",
		Description: "Audit or clean dirty memories in the LuckyAgent memory vault. Default action=audit is read-only; quarantine archives suspicious memories so they stop being recalled; delete physically removes matching entries.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"action": {
				Type:        "string",
				Description: "Action: audit, quarantine, or delete. Use audit first unless the user explicitly asks to clean.",
				Required:    false,
				Default:     "audit",
			},
			"min_severity": {
				Type:        "string",
				Description: "Minimum severity to include: low, medium, high, or critical.",
				Required:    false,
				Default:     "medium",
			},
			"include_inactive": {
				Type:        "boolean",
				Description: "Whether to include archived, superseded, expired, or future-dated memories in the scan.",
				Required:    false,
				Default:     false,
			},
			"limit": {
				Type:        "number",
				Description: "Maximum number of findings to return or apply.",
				Required:    false,
				Default:     50,
			},
		},
		Handler:      handler,
		ParallelSafe: false,
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
