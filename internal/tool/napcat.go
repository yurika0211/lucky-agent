package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway/napcat"
	"github.com/yurika0211/luckyagent/internal/logger"
)

const (
	defaultNapCatGroupReadLimit = 20
	maxNapCatGroupReadLimit     = 100
)

// NapCatGroupReader is the narrow live-gateway capability required by the
// cross-group history tool. Keeping it as an interface makes the policy layer
// testable and prevents arbitrary OneBot actions from reaching the model.
type NapCatGroupReader interface {
	GetGroupMessageHistory(context.Context, string, int, int) ([]napcat.GroupMessage, error)
	GetGroupInfo(context.Context, string) (napcat.GroupInfo, error)
	ListGroups(context.Context) ([]napcat.GroupInfo, error)
}

// NapCatCrossGroupReadConfig is intentionally opt-in. A blank configuration
// is denied even if the tool is registered manually.
type NapCatCrossGroupReadConfig struct {
	Enabled             bool
	RequireConfirmation bool
	AllowedGroups       []string
	BlockedGroups       []string
	LogAccess           bool
}

type napCatGroupReadArgs struct {
	GroupID   string
	Limit     int
	Offset    int
	TimeRange string
	Confirmed bool
	Reason    string
}

type napCatGroupReadResult struct {
	GroupID   string              `json:"group_id"`
	GroupName string              `json:"group_name"`
	Messages  []napCatReadMessage `json:"messages"`
	Total     int                 `json:"total"`
	ReadAt    time.Time           `json:"read_at"`
	TimeRange string              `json:"time_range,omitempty"`
}

// napCatReadMessage intentionally omits QQ user IDs. A group nickname and
// timestamp are enough for the model to summarize the discussion.
type napCatReadMessage struct {
	MessageID string    `json:"message_id"`
	UserName  string    `json:"user_name"`
	Content   string    `json:"content"`
	Time      time.Time `json:"time"`
}

// NapCatReadGroupTool creates the model-visible, policy-enforced cross-group
// history tool. It must only be registered by an active NapCat gateway when
// cross_group_read.enabled is true.
func NapCatReadGroupTool(reader NapCatGroupReader, cfg NapCatCrossGroupReadConfig) *Tool {
	return &Tool{
		Name:        "napcat_read_group",
		Description: "Read recent messages from another NapCat QQ group only after the user explicitly requests that exact group. Never use proactively. When confirmation is required, ask the user to confirm the target and set confirmed=true only after that confirmation.",
		Parameters: map[string]Param{
			"group_id": {
				Type:        "string",
				Description: "Target QQ group ID, or an exact group name explicitly supplied by the user. Ambiguous names are rejected.",
				Required:    true,
			},
			"limit": {
				Type:        "number",
				Description: "Number of recent messages to read (default 20, maximum 100).",
				Default:     defaultNapCatGroupReadLimit,
			},
			"offset": {
				Type:        "number",
				Description: "Offset into the history returned by NapCat (default 0).",
				Default:     0,
			},
			"time_range": {
				Type:        "string",
				Description: "Optional time range such as 1h, 24h, or 7d. Older records are excluded.",
			},
			"confirmed": {
				Type:        "boolean",
				Description: "Set true only after the user has confirmed this cross-group read when confirmation is required by policy.",
				Default:     false,
			},
			"reason": {
				Type:        "string",
				Description: "Short summary of the user's explicit request for this read. Do not invent a reason.",
				Required:    true,
			},
		},
		Permission: PermApprove,
		Category:   CatBuiltin,
		Source:     "napcat",
		ContextDetailedHandler: func(exec ExecutionContext, args map[string]any) (ToolCallResult, error) {
			return handleNapCatReadGroup(exec, args, reader, cfg)
		},
	}
}

func handleNapCatReadGroup(exec ExecutionContext, values map[string]any, reader NapCatGroupReader, cfg NapCatCrossGroupReadConfig) (ToolCallResult, error) {
	if !cfg.Enabled {
		return ToolCallResult{}, fmt.Errorf("napcat cross-group reading is disabled by policy")
	}
	if reader == nil {
		return ToolCallResult{}, fmt.Errorf("napcat gateway is unavailable")
	}
	args, err := parseNapCatGroupReadArgs(values)
	if err != nil {
		return ToolCallResult{}, err
	}
	if err := validateNapCatExplicitRequest(exec.UserRequest, args.GroupID); err != nil {
		return ToolCallResult{}, err
	}
	if cfg.RequireConfirmation && !args.Confirmed {
		return ToolCallResult{}, fmt.Errorf("cross-group read requires user confirmation for group %q; ask for confirmation before retrying", args.GroupID)
	}
	if targetGroupID, ok := napCatNumericGroupID(args.GroupID); ok {
		if err := checkNapCatGroupReadPolicy(targetGroupID, cfg); err != nil {
			return ToolCallResult{}, err
		}
	}

	ctx := exec.Context
	if ctx == nil {
		ctx = context.Background()
	}
	group, err := resolveNapCatGroup(ctx, reader, args.GroupID)
	if err != nil {
		return ToolCallResult{}, err
	}
	if err := checkNapCatGroupReadPolicy(group.GroupID, cfg); err != nil {
		return ToolCallResult{}, err
	}

	messages, err := reader.GetGroupMessageHistory(ctx, group.GroupID, args.Limit, args.Offset)
	if err != nil {
		return ToolCallResult{}, fmt.Errorf("read NapCat group history: %w", err)
	}
	if group.Name == "" {
		if info, infoErr := reader.GetGroupInfo(ctx, group.GroupID); infoErr == nil {
			group = info
		}
	}

	if args.TimeRange != "" {
		duration, err := parseNapCatTimeRange(args.TimeRange)
		if err != nil {
			return ToolCallResult{}, err
		}
		cutoff := time.Now().Add(-duration)
		filtered := messages[:0]
		for _, message := range messages {
			if message.Time.After(cutoff) || message.Time.Equal(cutoff) {
				filtered = append(filtered, message)
			}
		}
		messages = filtered
	}

	result := napCatGroupReadResult{
		GroupID:   group.GroupID,
		GroupName: group.Name,
		Messages:  make([]napCatReadMessage, 0, len(messages)),
		Total:     len(messages),
		ReadAt:    time.Now().UTC(),
		TimeRange: args.TimeRange,
	}
	for _, message := range messages {
		result.Messages = append(result.Messages, napCatReadMessage{
			MessageID: message.MessageID,
			UserName:  message.UserName,
			Content:   message.Content,
			Time:      message.Time,
		})
	}
	if cfg.LogAccess {
		logger.Info("napcat cross-group messages read",
			"source", exec.Source,
			"session_id", exec.SessionID,
			"requester_user_id", exec.UserID,
			"target_group_id", group.GroupID,
			"message_count", result.Total,
			"time_range", args.TimeRange,
			"confirmed", args.Confirmed,
		)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return ToolCallResult{}, fmt.Errorf("encode NapCat group history: %w", err)
	}
	return ToolCallResult{Output: string(encoded)}, nil
}

// validateNapCatExplicitRequest is a fail-closed guard against a model
// inventing a cross-group read from stale context. It checks the current user
// turn, rather than the tool's self-reported reason, for both a read request
// and the exact requested group target.
func validateNapCatExplicitRequest(userRequest, target string) error {
	request := strings.TrimSpace(strings.ToLower(userRequest))
	if request == "" {
		return fmt.Errorf("napcat_read_group: current user request is unavailable; cross-group reading requires an explicit user request")
	}
	target = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(target), "group:"))
	if target == "" || !napCatRequestMentionsTarget(request, target) {
		return fmt.Errorf("napcat_read_group: the current user request does not name target group %q", target)
	}
	readIntent := []string{
		"读取", "查看", "看看", "看下", "看一下", "总结", "汇总", "最近在讨论", "讨论什么", "消息历史", "群消息", "群里说",
		"read", "check", "look at", "look into", "history", "messages", "discussion", "summarize", "what are they discussing", "cross-group", "another group",
	}
	for _, phrase := range readIntent {
		if strings.Contains(request, phrase) {
			return nil
		}
	}
	return fmt.Errorf("napcat_read_group: current user request does not explicitly ask to read group messages")
}

func napCatRequestMentionsTarget(request, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return false
	}
	if _, numeric := napCatNumericGroupID(target); !numeric {
		return strings.Contains(request, target)
	}
	for start := 0; start < len(request); {
		index := strings.Index(request[start:], target)
		if index < 0 {
			return false
		}
		index += start
		beforeDigit := index > 0 && request[index-1] >= '0' && request[index-1] <= '9'
		end := index + len(target)
		afterDigit := end < len(request) && request[end] >= '0' && request[end] <= '9'
		if !beforeDigit && !afterDigit {
			return true
		}
		start = end
	}
	return false
}

func parseNapCatGroupReadArgs(values map[string]any) (napCatGroupReadArgs, error) {
	args := napCatGroupReadArgs{
		GroupID:   strings.TrimSpace(napCatStringArg(values, "group_id")),
		Limit:     napCatIntArg(values, "limit"),
		Offset:    napCatIntArg(values, "offset"),
		TimeRange: strings.TrimSpace(napCatStringArg(values, "time_range")),
		Confirmed: napCatBoolArg(values, "confirmed"),
		Reason:    strings.TrimSpace(napCatStringArg(values, "reason")),
	}
	if args.GroupID == "" {
		return napCatGroupReadArgs{}, fmt.Errorf("napcat_read_group: group_id is required")
	}
	if args.Reason == "" {
		return napCatGroupReadArgs{}, fmt.Errorf("napcat_read_group: reason must summarize the user's explicit request")
	}
	if args.Limit == 0 {
		args.Limit = defaultNapCatGroupReadLimit
	}
	if args.Limit < 1 || args.Limit > maxNapCatGroupReadLimit {
		return napCatGroupReadArgs{}, fmt.Errorf("napcat_read_group: limit must be between 1 and %d", maxNapCatGroupReadLimit)
	}
	if args.Offset < 0 {
		return napCatGroupReadArgs{}, fmt.Errorf("napcat_read_group: offset must not be negative")
	}
	return args, nil
}

func resolveNapCatGroup(ctx context.Context, reader NapCatGroupReader, target string) (napcat.GroupInfo, error) {
	target = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(target), "group:"))
	if target == "" {
		return napcat.GroupInfo{}, fmt.Errorf("napcat_read_group: group_id is required")
	}
	if _, ok := napCatNumericGroupID(target); ok {
		info, err := reader.GetGroupInfo(ctx, target)
		if err != nil {
			return napcat.GroupInfo{}, fmt.Errorf("get NapCat group info: %w", err)
		}
		if info.GroupID == "" {
			info.GroupID = target
		}
		return info, nil
	}

	groups, err := reader.ListGroups(ctx)
	if err != nil {
		return napcat.GroupInfo{}, fmt.Errorf("list NapCat groups to resolve %q: %w", target, err)
	}
	var matches []napcat.GroupInfo
	for _, group := range groups {
		if strings.TrimSpace(group.Name) == target {
			matches = append(matches, group)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return napcat.GroupInfo{}, fmt.Errorf("NapCat group %q was not found; provide its QQ group ID", target)
	default:
		return napcat.GroupInfo{}, fmt.Errorf("NapCat group name %q is ambiguous; provide its QQ group ID", target)
	}
}

func napCatNumericGroupID(target string) (string, bool) {
	target = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(target), "group:"))
	if _, err := strconv.ParseInt(target, 10, 64); err != nil {
		return "", false
	}
	return target, true
}

func checkNapCatGroupReadPolicy(groupID string, cfg NapCatCrossGroupReadConfig) error {
	if stringInList(groupID, cfg.BlockedGroups) {
		return fmt.Errorf("access to group %q is blocked by policy", groupID)
	}
	if len(cfg.AllowedGroups) > 0 && !stringInList(groupID, cfg.AllowedGroups) {
		return fmt.Errorf("access to group %q is not in the allowed_groups policy", groupID)
	}
	return nil
}

func parseNapCatTimeRange(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, nil
	}
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.ParseInt(strings.TrimSuffix(raw, "d"), 10, 64)
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("napcat_read_group: invalid time_range %q", raw)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("napcat_read_group: invalid time_range %q", raw)
	}
	return duration, nil
}

func stringInList(value string, items []string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) == value {
			return true
		}
	}
	return false
}

func napCatStringArg(values map[string]any, key string) string {
	value, _ := values[key]
	text, _ := value.(string)
	return text
}

func napCatIntArg(values map[string]any, key string) int {
	switch value := values[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	default:
		return 0
	}
}

func napCatBoolArg(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}
