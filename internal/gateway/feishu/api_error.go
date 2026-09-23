package feishu

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type feishuAPIError struct {
	HTTPStatus int
	Code       int
	Message    string
}

func (e *feishuAPIError) Error() string {
	if e == nil {
		return "feishu: API error"
	}
	if e.Code != 0 {
		return fmt.Sprintf("feishu: HTTP %d API code %d: %s", e.HTTPStatus, e.Code, strings.TrimSpace(e.Message))
	}
	return fmt.Sprintf("feishu: HTTP %d: %s", e.HTTPStatus, strings.TrimSpace(e.Message))
}

func (e *feishuAPIError) Category() string {
	if e == nil {
		return "delivery failure"
	}
	message := strings.ToLower(e.Message)
	switch {
	case e.HTTPStatus == http.StatusUnauthorized || strings.Contains(message, "token") && strings.Contains(message, "invalid"):
		return "authentication failure"
	case e.HTTPStatus == http.StatusForbidden || strings.Contains(message, "permission") || strings.Contains(message, "scope"):
		return "permission failure"
	case e.HTTPStatus == http.StatusTooManyRequests || e.Code == 99991400 || strings.Contains(message, "rate limit"):
		return "rate limited"
	case e.Code == 234006 || e.HTTPStatus == http.StatusRequestEntityTooLarge:
		return "media too large"
	case strings.Contains(message, "too long") || strings.Contains(message, "exceed") || strings.Contains(message, "length"):
		return "message too long"
	case strings.Contains(message, "reply") && (strings.Contains(message, "not exist") || strings.Contains(message, "invalid")):
		return "reply target unavailable"
	default:
		return "delivery failure"
	}
}

func apiErrorCategory(err error) string {
	var apiErr *feishuAPIError
	if errors.As(err, &apiErr) {
		return apiErr.Category()
	}
	return "network or delivery failure"
}

func mediaErrorLabel(err error) string {
	switch apiErrorCategory(err) {
	case "authentication failure":
		return "应用认证失败"
	case "permission failure":
		return "应用权限不足"
	case "rate limited":
		return "飞书接口触发限流"
	case "message too long":
		return "飞书拒绝了过长内容"
	case "reply target unavailable":
		return "原消息已无法回复"
	case "media too large":
		return "附件超过大小限制"
	default:
		return "飞书接口暂时不可用"
	}
}
