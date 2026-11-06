package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestSyntaxFoldRepeatedJSON(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 40; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"row-%d","path":"/tmp/data/%d.json","code":"E42","note":"%s"}`, i, i, strings.Repeat("x", 40))
	}
	b.WriteString(`]}`)
	raw := b.String()
	got := compactToolResultSyntax(raw, false)
	if got.Filter != syntaxJSON || len(got.Text) >= len(raw) {
		t.Fatalf("filter=%q in=%d out=%d skip=%q", got.Filter, got.BytesIn, len(got.Text), got.Skip)
	}
	for _, want := range []string{"id", "path", "code", "E42", "/tmp/data/0.json", "+"} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("folded json dropped %q\n%s", want, got.Text)
		}
	}
	if !strings.Contains(got.Text, "more") {
		t.Fatalf("array fold has no count:\n%s", got.Text)
	}
}

func TestSyntaxFoldGoSignatures(t *testing.T) {
	var b strings.Builder
	b.WriteString("package sample\n\n")
	fmt.Fprintf(&b, "func Load(path string) error {\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "\tfmt.Printf(\"step %%d at 10| /tmp/sample.go\", %d)\n", i)
	}
	b.WriteString("\treturn nil\n}\n")
	raw := b.String()
	if len(raw) < shapeMinBytes {
		raw += "\n" + strings.Repeat("// pad\n", 40)
	}
	got := compactToolResultSyntax(raw, false)
	if got.Filter != syntaxSource || len(got.Text) >= len(raw) {
		t.Fatalf("filter=%q in=%d out=%d skip=%q\n%s", got.Filter, len(raw), len(got.Text), got.Skip, got.Text)
	}
	if !strings.Contains(got.Text, "func Load(path string) error {") {
		t.Fatalf("signature dropped:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "10|") || !strings.Contains(got.Text, "/tmp/sample.go") {
		t.Fatalf("line number or path dropped:\n%s", got.Text)
	}
	if strings.Count(got.Text, "fmt.Printf") > 4 {
		t.Fatalf("function body left whole:\n%s", got.Text)
	}
}

func TestSyntaxPassThroughFailureOpen(t *testing.T) {
	broken := "{\n" + strings.Repeat(`"k": "unterminated`+"\n", 40)
	short := `{"a":1}`
	empty := ""
	errText := "[Tool Error: terminal] " + strings.Repeat("panic: boom\n", 40)
	for _, raw := range []string{broken, short, empty, errText} {
		got := compactToolResultSyntax(raw, strings.HasPrefix(raw, "[Tool Error:"))
		if got.Text != raw || got.Filter != "" {
			t.Fatalf("changed %q -> filter=%q skip=%q out=%q", raw[:min(20, len(raw))], got.Filter, got.Skip, got.Text[:min(40, len(got.Text))])
		}
	}
}

func TestSyntaxLeavesDiffGrepLsToShape(t *testing.T) {
	var diff strings.Builder
	fmt.Fprintf(&diff, "diff --git a/agent/a.go b/agent/a.go\n@@ -1,80 +1,80 @@\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&diff, "+added %d\n-removed %d\n", i, i)
	}
	var grep strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&grep, "agent/compact_shape.go:%d:hit %d\n", i+1, i)
	}
	var ls strings.Builder
	ls.WriteString("total 80\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&ls, "-rw-r--r-- 1 user user %d Jan 2 03:04 file-%d.txt\n", 100+i, i)
	}
	for _, raw := range []string{diff.String(), grep.String(), ls.String()} {
		got := compactToolResultSyntax(raw, false)
		if got.Text != raw || got.Filter != "" {
			t.Fatalf("plaintext shape was syntax-folded filter=%q\n%s", got.Filter, got.Text[:min(120, len(got.Text))])
		}
	}
}

func TestCompactABReportsSyntaxBytes(t *testing.T) {
	compactAB.reset()
	t.Cleanup(compactAB.reset)
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 30; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"row-%d","path":"/tmp/data/%d.json","code":"E42","note":"%s"}`, i, i, strings.Repeat("y", 30))
	}
	b.WriteString(`]}`)
	raw := b.String()
	compactABModeOverride.Store(compactArmTreatment)
	t.Cleanup(func() { compactABModeOverride.Store("") })

	served := compactToolResultAB("file_read", raw, "call-syntax", 8000, false)
	snap := compactAB.snapshot()
	if snap.SyntaxSamples != 1 {
		t.Fatalf("syntax samples=%d snap=%+v", snap.SyntaxSamples, snap)
	}
	if snap.SyntaxOutBytes >= snap.SyntaxInBytes || snap.SyntaxInBytes != len(raw) {
		t.Fatalf("syntax bytes in=%d out=%d raw=%d", snap.SyntaxInBytes, snap.SyntaxOutBytes, len(raw))
	}
	if snap.BySyntax[syntaxJSON] != 1 {
		t.Fatalf("by syntax=%v", snap.BySyntax)
	}
	if !strings.Contains(served, "E42") || !strings.Contains(served, "/tmp/data/0.json") {
		t.Fatalf("served dropped identifiers:\n%s", served)
	}
}
