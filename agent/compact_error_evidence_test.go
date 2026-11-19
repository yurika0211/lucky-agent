package agent

import (
	"slices"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

func TestRequiredCompactIdentifiersDistinguishDiagnosticsFromToolData(t *testing.T) {
	const source = "File: /src/sync.ts\nSize: 900 bytes\nShowing from line 7, max 4 lines\n\n"
	const log = "File: /logs/run.log\nSize: 900 bytes\nShowing from line 7, max 4 lines\n\n"
	for _, tc := range []struct {
		name, tool, header, line string
		required                 bool
	}{
		{"source field", "file_read", source, "7| return {failed: 0}", false},
		{"source status setter", "file_read", source, "8| sync.setStatus('error')", false},
		{"numbered log record", "file_read", log, "7| ERROR: could not resolve status", true},
		{"failed file read", "file_read", "", "error: read attempt failed", true},
		{"unknown file format", "file_read", "", "8| proxy failed to start", true},
		{"metric description", "terminal", "", "# HELP requests_failed Number of requests ending in error", false},
		{"zero failures", "terminal", "", "sync: 7 transferred, 0 failed", false},
		{"positive failures", "terminal", "", "sync: 7 transferred, 1 failed", true},
		{"mixed result", "terminal", "", "0 failed; error: disk full", true},
		{"zero is an identifier", "terminal", "", "worker 0 failed to sync", true},
		{"process exit", "terminal", "", "exit status 2", true},
		{"structured status", "terminal", "", "- status error dur 11 sid 324151", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := []provider.Message{{Role: "tool", Name: tc.tool, Content: tc.header + tc.line}}
			got := requiredCompactIdentifiers(source)
			if slices.Contains(got, tc.line) != tc.required {
				t.Errorf("want required=%v for %q, got %q", tc.required, tc.line, got)
			}
		})
	}

	t.Run("path inside source data", func(t *testing.T) {
		data := []provider.Message{{Role: "tool", Name: "file_read", Content: source + "7| // error examples in /src/testdata/cases"}}
		if got := requiredCompactIdentifiers(data); !slices.Contains(got, "/src/testdata/cases") {
			t.Errorf("lost path in a source listing: %q", got)
		}
	})
}
