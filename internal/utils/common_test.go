package utils

import "testing"

func TestDedupStringsLimitKeepsOrder(t *testing.T) {
	items := []string{"a", "b", "a", "c", "b", "d"}
	got := DedupStringsLimit(items, 3)
	want := []string{"a", "b", "c"}
	if !sameStrings(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestDedupNonEmptyStrings(t *testing.T) {
	items := []string{"", "x", "x", "", "y", " "}
	got := DedupNonEmptyStrings(items)
	want := []string{"x", "y", " "}
	if !sameStrings(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestFilterNonEmptyTrimmed(t *testing.T) {
	items := []string{"", "  hello  ", "\t", " world", "a"}
	got := FilterNonEmptyTrimmed(items)
	want := []string{"hello", "world", "a"}
	if !sameStrings(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestMinMaxInt(t *testing.T) {
	if got := MinInt(3, 9); got != 3 {
		t.Fatalf("expected MinInt=3, got %d", got)
	}
	if got := MaxInt(3, 9); got != 9 {
		t.Fatalf("expected MaxInt=9, got %d", got)
	}
}

func TestSplitLines(t *testing.T) {
	got := SplitLines("a\r\nb\nc\r")
	want := []string{"a", "b", "c"}
	if !sameStrings(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestSplitLinesBytes(t *testing.T) {
	got := SplitLinesBytes([]byte("id\r\ntopic\npayload\r"))
	want := []string{"id", "topic", "payload"}
	if !sameStrings(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

/*
sameStrings 判断两个字符串切片是否逐项完全相同。
*/
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
