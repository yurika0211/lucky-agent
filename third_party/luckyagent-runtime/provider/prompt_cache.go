package provider

import (
	"sort"
	"strings"
)

// canonicalizeOpenAITools keeps equivalent schemas byte-stable without changing
// the caller's objects. Only JSON Schema required sets and the tool list are
// sorted; literal defaults, enums and ordered schema alternatives stay ordered.
func canonicalizeOpenAITools(tools []map[string]any) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	result := make([]map[string]any, len(tools))
	for i, tool := range tools {
		copyTool := cloneSchemaJSON(tool).(map[string]any)
		if function, ok := copyTool["function"].(map[string]any); ok {
			if parameters, ok := function["parameters"].(map[string]any); ok {
				canonicalizeSchemaRequired(parameters)
			}
		}
		result[i] = copyTool
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := openAIToolName(result[i]), openAIToolName(result[j])
		if left != right {
			return left < right
		}
		leftJSON, _ := jsonAPI.Marshal(result[i])
		rightJSON, _ := jsonAPI.Marshal(result[j])
		return string(leftJSON) < string(rightJSON)
	})
	return result
}

func openAIToolName(tool map[string]any) string {
	function, _ := tool["function"].(map[string]any)
	name, _ := function["name"].(string)
	return strings.TrimSpace(name)
}

func buildOpenAITools(tools []map[string]any) []openaiTool {
	canonical := canonicalizeOpenAITools(tools)
	result := make([]openaiTool, 0, len(canonical))
	for _, tool := range canonical {
		function, _ := tool["function"].(map[string]any)
		result = append(result, openaiTool{Type: "function", Function: newToolFunction(function)})
	}
	return result
}

func cloneSchemaJSON(value any) any {
	switch value := value.(type) {
	case map[string]any:
		if value == nil {
			return value
		}
		copy := make(map[string]any, len(value))
		for key, child := range value {
			copy[key] = cloneSchemaJSON(child)
		}
		return copy
	case []any:
		if value == nil {
			return value
		}
		copy := make([]any, len(value))
		for i, child := range value {
			copy[i] = cloneSchemaJSON(child)
		}
		return copy
	case []map[string]any:
		if value == nil {
			return value
		}
		copy := make([]map[string]any, len(value))
		for i, child := range value {
			copy[i] = cloneSchemaJSON(child).(map[string]any)
		}
		return copy
	case []string:
		if value == nil {
			return value
		}
		cloned := make([]string, len(value))
		copy(cloned, value)
		return cloned
	default:
		return value
	}
}

// Walk schema-bearing keywords only. A const/default/example may itself contain
// a field named required whose array order must not be changed.
func canonicalizeSchemaRequired(schema map[string]any) {
	switch values := schema["required"].(type) {
	case []string:
		sort.Strings(values)
	case []any:
		if values == nil {
			break
		}
		names := make([]string, len(values))
		for i, value := range values {
			name, ok := value.(string)
			if !ok {
				return
			}
			names[i] = name
		}
		sort.Strings(names)
		schema["required"] = names
	}
	for key, value := range schema {
		switch key {
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
			if children, ok := value.(map[string]any); ok {
				for _, child := range children {
					canonicalizeSchemaChild(child)
				}
			}
		case "items", "prefixItems", "allOf", "anyOf", "oneOf", "not", "if", "then", "else", "contains", "additionalItems", "additionalProperties", "unevaluatedItems", "unevaluatedProperties", "propertyNames", "contentSchema":
			canonicalizeSchemaChild(value)
		}
	}
}

func canonicalizeSchemaChild(value any) {
	switch value := value.(type) {
	case map[string]any:
		canonicalizeSchemaRequired(value)
	case []any:
		for _, child := range value {
			canonicalizeSchemaChild(child)
		}
	case []map[string]any:
		for _, child := range value {
			canonicalizeSchemaRequired(child)
		}
	}
}
