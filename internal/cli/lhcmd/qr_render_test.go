package lhcmd

import "testing"

func TestRenderQRContainsFinderPattern(t *testing.T) {
	got := renderQR(`{"v":1,"url":"http://192.168.1.8:9090","token":"lp_test"}`)
	if len(got) < 40 || !containsQRBlock(got) {
		t.Fatalf("expected a terminal QR drawing, got %q", got)
	}
}

func containsQRBlock(text string) bool {
	blocks := 0
	for _, r := range text {
		if r == '█' || r == '▀' || r == '▄' {
			blocks++
		}
	}
	return blocks > 20
}
