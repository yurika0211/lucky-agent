package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway/napcat"
)

type fakeNapCatGroupReader struct {
	info         napcat.GroupInfo
	groups       []napcat.GroupInfo
	messages     []napcat.GroupMessage
	historyCalls int
	infoCalls    int
	listCalls    int
}

func (f *fakeNapCatGroupReader) GetGroupMessageHistory(_ context.Context, _ string, limit, offset int) ([]napcat.GroupMessage, error) {
	f.historyCalls++
	if offset >= len(f.messages) {
		return []napcat.GroupMessage{}, nil
	}
	end := offset + limit
	if end > len(f.messages) {
		end = len(f.messages)
	}
	return append([]napcat.GroupMessage(nil), f.messages[offset:end]...), nil
}

func (f *fakeNapCatGroupReader) GetGroupInfo(_ context.Context, _ string) (napcat.GroupInfo, error) {
	f.infoCalls++
	return f.info, nil
}

func (f *fakeNapCatGroupReader) ListGroups(_ context.Context) ([]napcat.GroupInfo, error) {
	f.listCalls++
	return append([]napcat.GroupInfo(nil), f.groups...), nil
}

func TestNapCatReadGroupToolRequiresEnablementAndConfirmation(t *testing.T) {
	reader := &fakeNapCatGroupReader{info: napcat.GroupInfo{GroupID: "100", Name: "technical"}}
	args := map[string]any{"group_id": "100", "reason": "user asked about the technical group"}

	disabled := NapCatReadGroupTool(reader, NapCatCrossGroupReadConfig{})
	if _, err := disabled.ContextDetailedHandler(ExecutionContext{}, args); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled policy error, got %v", err)
	}

	enabled := NapCatReadGroupTool(reader, NapCatCrossGroupReadConfig{Enabled: true, RequireConfirmation: true})
	if _, err := enabled.ContextDetailedHandler(ExecutionContext{UserRequest: "请看看 100 群最近的消息"}, args); err == nil || !strings.Contains(err.Error(), "requires user confirmation") {
		t.Fatalf("expected confirmation error, got %v", err)
	}
	if reader.infoCalls != 0 || reader.historyCalls != 0 {
		t.Fatalf("confirmation rejection must not read group data, got info=%d history=%d", reader.infoCalls, reader.historyCalls)
	}
}

func TestNapCatReadGroupToolEnforcesPolicyBeforeReading(t *testing.T) {
	reader := &fakeNapCatGroupReader{info: napcat.GroupInfo{GroupID: "100", Name: "technical"}}
	tool := NapCatReadGroupTool(reader, NapCatCrossGroupReadConfig{Enabled: true, BlockedGroups: []string{"100"}})
	_, err := tool.ContextDetailedHandler(ExecutionContext{UserRequest: "请查看 100 群的消息"}, map[string]any{
		"group_id": "100", "reason": "user asked", "confirmed": true,
	})
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected blocked policy error, got %v", err)
	}
	if reader.infoCalls != 0 || reader.historyCalls != 0 {
		t.Fatalf("blocked group must not be queried, got info=%d history=%d", reader.infoCalls, reader.historyCalls)
	}
}

func TestNapCatReadGroupToolResolvesExactNameFiltersTimeAndMinimizesData(t *testing.T) {
	now := time.Now().UTC()
	reader := &fakeNapCatGroupReader{
		groups: []napcat.GroupInfo{{GroupID: "100", Name: "technical"}},
		messages: []napcat.GroupMessage{
			{MessageID: "old", UserName: "Ada", Content: "old discussion", Time: now.Add(-2 * time.Hour)},
			{MessageID: "new", UserName: "Lin", Content: "recent discussion", Time: now.Add(-30 * time.Minute)},
		},
	}
	readTool := NapCatReadGroupTool(reader, NapCatCrossGroupReadConfig{
		Enabled: true, AllowedGroups: []string{"100"}, LogAccess: true,
	})
	result, err := readTool.ContextDetailedHandler(ExecutionContext{Context: context.Background(), Source: "napcat", SessionID: "session-1", UserID: "user-1", UserRequest: "请看看 technical 群最近在讨论什么"}, map[string]any{
		"group_id": "technical", "reason": "user explicitly asked for recent technical-group discussion", "time_range": "1h", "limit": 20,
	})
	if err != nil {
		t.Fatalf("read group: %v", err)
	}
	if reader.listCalls != 1 || reader.historyCalls != 1 {
		t.Fatalf("unexpected reader calls: list=%d history=%d", reader.listCalls, reader.historyCalls)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("decode result: %v\n%s", err, result.Output)
	}
	if payload["group_id"] != "100" || payload["group_name"] != "technical" || payload["total"] != float64(1) {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	messages, ok := payload["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("unexpected messages: %#v", payload["messages"])
	}
	message := messages[0].(map[string]any)
	if message["content"] != "recent discussion" || message["user_name"] != "Lin" {
		t.Fatalf("unexpected message: %#v", message)
	}
	if _, leaked := message["user_id"]; leaked {
		t.Fatalf("tool result must not contain user_id: %#v", message)
	}
}

func TestNapCatReadGroupToolRejectsImplicitOrMismatchedRequests(t *testing.T) {
	reader := &fakeNapCatGroupReader{info: napcat.GroupInfo{GroupID: "100", Name: "technical"}}
	readTool := NapCatReadGroupTool(reader, NapCatCrossGroupReadConfig{Enabled: true})
	args := map[string]any{"group_id": "100", "reason": "user asked", "confirmed": true}
	for _, request := range []string{"", "请看看 200 群最近的消息", "请告诉我 100 群的群主是谁"} {
		if _, err := readTool.ContextDetailedHandler(ExecutionContext{UserRequest: request}, args); err == nil {
			t.Fatalf("expected explicit-request guard for %q", request)
		}
	}
	if reader.infoCalls != 0 || reader.historyCalls != 0 {
		t.Fatalf("implicit request must not read group data, got info=%d history=%d", reader.infoCalls, reader.historyCalls)
	}
}

func TestNapCatRequestMentionsNumericTargetExactly(t *testing.T) {
	if napCatRequestMentionsTarget("请查看 123 群的消息", "1") {
		t.Fatal("a partial numeric match must not authorize another group")
	}
	if !napCatRequestMentionsTarget("请查看 123 群的消息", "123") {
		t.Fatal("expected the exact numeric group target to match")
	}
}

func TestParseNapCatTimeRangeSupportsDaysAndRejectsInvalidInput(t *testing.T) {
	duration, err := parseNapCatTimeRange("7d")
	if err != nil || duration != 7*24*time.Hour {
		t.Fatalf("parse 7d = %v, %v", duration, err)
	}
	if _, err := parseNapCatTimeRange("not-a-duration"); err == nil {
		t.Fatal("expected invalid duration error")
	}
}
