package memoryaestus

import (
	sdk "github.com/yurika0211/aestus/memory"
	"github.com/yurika0211/luckyagent-tools/memory"
)

func mapSlice[A, B any](items []A, convert func(A) B) []B {
	if items == nil {
		return nil
	}
	out := make([]B, len(items))
	for i, item := range items {
		out[i] = convert(item)
	}
	return out
}

func mapPointer[A, B any](value *A, convert func(A) B) *B {
	if value == nil {
		return nil
	}
	out := convert(*value)
	return &out
}

func mapMap[KA, KB comparable, A, B any](items map[KA]A, key func(KA) KB, value func(A) B) map[KB]B {
	if items == nil {
		return nil
	}
	out := make(map[KB]B, len(items))
	for k, v := range items {
		out[key(k)] = value(v)
	}
	return out
}

// Tool policy arguments use the usual JSON object/array value representation.
// Copy it at the boundary so callers cannot mutate SDK-owned evidence.
func cloneArgument(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return mapMap(v, func(s string) string { return s }, cloneArgument)
	case []any:
		return mapSlice(v, cloneArgument)
	case []string:
		return append([]string(nil), v...)
	default:
		return value
	}
}

func toSDKEntryFilter(filter func(memory.Entry) bool) func(sdk.Entry) bool {
	if filter == nil {
		return nil
	}
	return func(entry sdk.Entry) bool { return filter(fromSDKEntry(entry)) }
}

func fromSDKEntryFilter(filter func(sdk.Entry) bool) func(memory.Entry) bool {
	if filter == nil {
		return nil
	}
	return func(entry memory.Entry) bool { return filter(toSDKEntry(entry)) }
}
