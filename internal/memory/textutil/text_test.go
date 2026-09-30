package textutil

import "testing"

func TestTrimToRunes(t *testing.T) {
	if got := TrimToRunes("你好世界", 2); got != "你好" {
		t.Fatalf("TrimToRunes() = %q", got)
	}
	if got := TrimToRunes("hello", 0); got != "" {
		t.Fatalf("TrimToRunes() with zero = %q", got)
	}
}

func TestDedupNonEmptyStrings(t *testing.T) {
	got := DedupNonEmptyStrings([]string{" a ", "", "a", "b", "b"})
	want := []string{"a", "b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("DedupNonEmptyStrings() = %#v, want %#v", got, want)
	}
}
