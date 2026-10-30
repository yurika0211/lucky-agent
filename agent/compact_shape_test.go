package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestShapeRewriteDiffKeepsFilesHunksAndCounts(t *testing.T) {
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/agent/compact.go b/agent/compact.go\n")
	fmt.Fprintf(&b, "index 1111111..2222222 100644\n--- a/agent/compact.go\n+++ b/agent/compact.go\n")
	fmt.Fprintf(&b, "@@ -1,120 +1,120 @@\n")
	for i := 0; i < 140; i++ {
		fmt.Fprintf(&b, "+kept line %d /home/dev/workspace/lucky-agent/agent/compact.go\n", i)
		fmt.Fprintf(&b, "-old line %d\n", i)
	}
	fmt.Fprintf(&b, "diff --git a/agent/loop.go b/agent/loop.go\n")
	fmt.Fprintf(&b, "@@ -10,3 +10,4 @@\n+return compact\n")
	raw := b.String()
	if len(raw) < 40*1024 {
		raw += strings.Repeat("+padding /tmp/pad.go\n", 800)
	}

	got := rewriteToolResultShape(raw, false)
	if got.Filter != shapeGitDiff {
		t.Fatalf("filter=%q skip=%q", got.Filter, got.Skip)
	}
	if got.BytesOut >= got.BytesIn {
		t.Fatalf("diff was not shortened: %d -> %d", got.BytesIn, got.BytesOut)
	}
	for _, want := range []string{"agent/compact.go", "agent/loop.go", "@@ -1,120 +1,120 @@", "+140", "lines truncated", "session transcript"} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("missing %q\n%s", want, got.Text[:min(800, len(got.Text))])
		}
	}
	second := applyToolResultShape(detectToolResultShape(got.Text), got.Text)
	if second != got.Text && len(second) < len(got.Text) {
		t.Fatalf("diff rewrite was not idempotent: %d -> %d filter=%q", len(got.Text), len(second), detectToolResultShape(got.Text))
	}
}

func TestShapeRewriteStatusPorcelainAndLongForm(t *testing.T) {
	long := "On branch feat/compact\nChanges not staged for commit:\n  modified:   agent/loop.go\n  modified:   agent/compact_shape.go\nboth modified:   agent/merge.go\n"
	long += strings.Repeat("  modified:   agent/file.go\n", 20)
	got := rewriteToolResultShape(long, false)
	if got.Filter != shapeGitStatus {
		t.Fatalf("long status filter=%q", got.Filter)
	}
	for _, want := range []string{"feat/compact", "Modified:", "agent/loop.go", "conflicts: 1"} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("long status missing %q\n%s", want, got.Text)
		}
	}

	porcelain := "## feat/compact...origin/feat/compact\n"
	for i := 0; i < 30; i++ {
		porcelain += fmt.Sprintf(" M agent/changed-%02d.go\n", i)
		porcelain += fmt.Sprintf("?? agent/new-%02d.go\n", i)
	}
	porcelain += "UU agent/conflict.go\n"
	got = rewriteToolResultShape(porcelain, false)
	if got.Filter != shapePorcelainName && got.Filter != shapeGitStatus {
		t.Fatalf("porcelain filter=%q skip=%q\n%s", got.Filter, got.Skip, porcelain[:200])
	}
	if strings.Contains(got.Text, "clean") {
		t.Fatalf("porcelain reported clean:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "conflicts: 1") || !strings.Contains(got.Text, "Untracked:") {
		t.Fatalf("porcelain counts missing:\n%s", got.Text)
	}
}

func TestShapeRewriteGrepGroupsAndRejectsProse(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&b, "agent/compact_shape.go:%d: rewrite hit %d\n", i, i)
	}
	for i := 1; i <= 4; i++ {
		fmt.Fprintf(&b, "agent/loop.go:%d: other hit\n", i)
	}
	got := rewriteToolResultShape(b.String(), false)
	if got.Filter != shapeGrep {
		t.Fatalf("filter=%q", got.Filter)
	}
	if strings.Count(got.Text, "agent/compact_shape.go") != 1 {
		t.Fatalf("path repeated:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "matches in 2F") || !strings.Contains(got.Text, "+15") {
		t.Fatalf("grep counts missing:\n%s", got.Text)
	}
	prose := b.String() + "\nthis paragraph is not a grep hit and must keep the original\n"
	if got := rewriteToolResultShape(prose, false); got.Filter == shapeGrep {
		t.Fatal("prose mixed into grep was rewritten")
	}
}

func TestShapeRewriteFindAndLs(t *testing.T) {
	var find strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&find, "/home/dev/workspace/lucky-agent/agent/file-%02d.go\n", i)
	}
	got := rewriteToolResultShape(find.String(), false)
	if got.Filter != shapeFind {
		t.Fatalf("find filter=%q skip=%q", got.Filter, got.Skip)
	}
	if !strings.Contains(got.Text, "file-00.go") || strings.Count(got.Text, "/home/dev/workspace/lucky-agent/agent") != 1 {
		t.Fatalf("find grouping failed:\n%s", got.Text)
	}

	var ls strings.Builder
	ls.WriteString("total 88\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&ls, "-rw-r--r-- 1 dev dev %d Oct 29 12:%02d notes-%02d.md\n", 1200+i, i%60, i)
	}
	ls.WriteString("drwxr-xr-x 4 dev dev 4096 Oct 29 12:01 node_modules\n")
	ls.WriteString("-rw------- 1 dev dev 32 Oct 29 12:02 .env\n")
	got = rewriteToolResultShape(ls.String(), false)
	if got.Filter != shapeLs {
		t.Fatalf("ls filter=%q skip=%q", got.Filter, got.Skip)
	}
	if strings.Contains(got.Text, "rw-r--r--") || strings.Contains(got.Text, "Oct 29") {
		t.Fatalf("ls kept permission or date columns:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "notes-00.md") || !strings.Contains(got.Text, ".env") || !strings.Contains(got.Text, "node_modules/") {
		t.Fatalf("ls dropped a name:\n%s", got.Text)
	}
}

func TestShapeRewriteBuildKeepsErrorContinuation(t *testing.T) {
	var b strings.Builder
	b.WriteString("   Compiling lucky-agent v0.0.0 (/home/dev/workspace/lucky-agent)\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "   Compiling crate-%02d v0.1.0\n", i)
	}
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "    Downloading crate-%02d v0.1.0 (registry+https://example.invalid/crate)\n", i)
	}
	b.WriteString("error[E0308]: mismatched types\n")
	b.WriteString("  --> agent/compact_shape.go:10:5\n")
	b.WriteString("   |\n")
	b.WriteString("10 |     return input\n")
	b.WriteString("   |     ^^^^^^ expected i32\n")
	b.WriteString("   = note: expected i32, found String\n")
	b.WriteString("warning: unused import\n")
	b.WriteString("    Finished dev [unoptimized + debuginfo] target(s) in 1.2s\n")
	raw := b.String()
	got := rewriteToolResultShape(raw, false)
	if got.Filter != shapeBuild {
		t.Fatalf("filter=%q skip=%q bytes=%d detect=%q", got.Filter, got.Skip, len(raw), detectToolResultShape(raw))
	}
	if strings.Contains(got.Text, "Compiling crate-00") || !strings.Contains(got.Text, "Compiled 41 packages") {
		t.Fatalf("compiling lines were not counted:\n%s", got.Text)
	}
	for _, want := range []string{"error[E0308]", "--> agent/compact_shape.go:10:5", "= note: expected i32", "Finished dev"} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("build rewrite dropped %q\n%s", want, got.Text)
		}
	}
}

func TestShapeRewriteNumberedReadAndPassThrough(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "%4d|line %d of /tmp/source.go\n", i, i)
	}
	got := rewriteToolResultShape(b.String(), false)
	if got.Filter != shapeReadNumber {
		t.Fatalf("filter=%q", got.Filter)
	}
	if !strings.Contains(got.Text, "   1|") || !strings.Contains(got.Text, " 300|") || !strings.Contains(got.Text, "truncated") {
		t.Fatalf("numbered window missing ends:\n%s", tail(got.Text, 400))
	}

	short := "diff --git a/a.go b/a.go\n@@ -1 +1 @@\n-a\n+b\n"
	if got := rewriteToolResultShape(short, false); got.Skip != shapeSkipSmall || got.Text != short {
		t.Fatalf("short diff changed: %+v", got)
	}
	errText := "[Tool Error: terminal] " + strings.Repeat("panic: boom\n", 40)
	if got := rewriteToolResultShape(errText, true); got.Text != errText || got.Skip != shapeSkipError {
		t.Fatal("error result was rewritten")
	}
}

func TestCompactABRecordsPairedSavings(t *testing.T) {
	compactAB.reset()
	t.Cleanup(compactAB.reset)
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/agent/a.go b/agent/a.go\n@@ -1,80 +1,80 @@\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "+added %d\n-removed %d\n", i, i)
	}
	raw := b.String()
	compactABModeOverride.Store(compactArmTreatment)
	t.Cleanup(func() { compactABModeOverride.Store("") })

	served := compactToolResultAB("terminal", raw, "call-1", 8000, false)
	snap := compactAB.snapshot()
	if snap.Samples != 1 {
		t.Fatalf("samples=%d", snap.Samples)
	}
	if snap.TreatmentBytes >= snap.ControlBytes {
		t.Fatalf("treatment did not beat control: %+v", snap)
	}
	if !strings.Contains(served, "agent/a.go") || !strings.Contains(served, "@@") {
		t.Fatalf("treatment dropped the diff shape:\n%s", served)
	}
	if snap.IdentifierMisses != 0 {
		t.Fatalf("identifier check failed: %+v", snap)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
