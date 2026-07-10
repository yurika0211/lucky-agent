package feishu

import (
	"strings"
	"testing"
)

func TestMediaDeliveryGuidanceIsIdempotent(t *testing.T) {
	got := MediaDeliveryGuidance("create a report")
	if !strings.Contains(got, mediaDeliveryRuleMarker) || !strings.Contains(got, "text-only") {
		t.Fatalf("missing Feishu delivery guidance: %q", got)
	}
	again := MediaDeliveryGuidance(got)
	if strings.Count(again, mediaDeliveryRuleMarker) != 1 {
		t.Fatalf("guidance was duplicated: %q", again)
	}
}
