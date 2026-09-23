package feishu

import "strings"

const mediaDeliveryRuleMarker = "[Feishu delivery rule]"

// MediaDeliveryGuidance adds a capability-aware media delivery rule to agent
// input. It is idempotent so queued or retried messages are not bloated.
func MediaDeliveryGuidance(text string, mediaEnabled ...bool) string {
	text = strings.TrimSpace(text)
	if strings.Contains(text, mediaDeliveryRuleMarker) {
		return text
	}
	guidance := mediaDeliveryRuleMarker + "\nFeishu media is disabled. Do not claim that a local file, image, or other media artifact was delivered. Provide useful content inline as text when practical."
	if len(mediaEnabled) > 0 && mediaEnabled[0] {
		guidance = mediaDeliveryRuleMarker + "\nFeishu can receive image and document attachments and deliver generated images and documents. Only claim a file was delivered when the send operation succeeds; if the format or size is unsupported, explain that clearly."
	}
	if text == "" {
		return guidance
	}
	return text + "\n\n" + guidance
}
