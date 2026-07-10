package feishu

import "strings"

const mediaDeliveryRuleMarker = "[Feishu delivery rule]"

// MediaDeliveryGuidance adds the Phase-1 text-only delivery constraint to an
// agent input. It is idempotent so queued or retried messages are not bloated.
func MediaDeliveryGuidance(text string) string {
	text = strings.TrimSpace(text)
	if strings.Contains(text, mediaDeliveryRuleMarker) {
		return text
	}
	const guidance = mediaDeliveryRuleMarker + "\nFeishu delivery is text-only in Phase 1. Do not claim that a local file, image, or other media artifact was delivered. Provide the useful content inline as text when practical; otherwise clearly explain that media delivery is unsupported."
	if text == "" {
		return guidance
	}
	return text + "\n\n" + guidance
}
