package tool

import (
	"encoding/json"
	"fmt"
)

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
