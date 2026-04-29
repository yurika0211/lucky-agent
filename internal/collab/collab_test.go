package collab

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMessageEncodeDecode(t *testing.T) {
	msg := NewMessage(MsgTaskAssign, "agent-1", "agent-2", map[string]any{
		"task": "analyze data",
	})
	msg.WithPriority(PriorityHigh)

	data, err := msg.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DecodeMessage(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.ID != msg.ID {
		t.Errorf("ID mismatch: got %s, want %s", decoded.ID, msg.ID)
	}
	if decoded.Type != MsgTaskAssign {
		t.Errorf("Type mismatch: got %s, want %s", decoded.Type, MsgTaskAssign)
	}
	if decoded.From != "agent-1" {
		t.Errorf("From mismatch: got %s, want agent-1", decoded.From)
	}
	if decoded.Priority != PriorityHigh {
		t.Errorf("Priority mismatch: got %d, want %d", decoded.Priority, PriorityHigh)
	}
}

func TestMessageValidate(t *testing.T) {
	tests := []struct {
		name    string
		msg     *Message
		wantErr bool
	}{
		{
			name:    "valid message",
			msg:     NewMessage(MsgTaskAssign, "agent-1", "agent-2", nil),
			wantErr: false,
		},
		{
			name: "missing ID",
			msg: &Message{
				Type:      MsgTaskAssign,
				From:      "agent-1",
				Timestamp: time.Now(),
			},
			wantErr: true,
		},
		{
			name: "missing type",
			msg: &Message{
				ID:        "msg-1",
				From:      "agent-1",
				Timestamp: time.Now(),
			},
			wantErr: true,
		},
		{
			name: "missing from",
			msg: &Message{
				ID:        "msg-1",
				Type:      MsgTaskAssign,
				Timestamp: time.Now(),
			},
			wantErr: true,
		},
		{
			name: "missing timestamp",
			msg: &Message{
				ID:   "msg-1",
				Type: MsgTaskAssign,
				From: "agent-1",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegistryRegister(t *testing.T) {
	r := NewRegistry()

	profile := &AgentProfile{
		ID:           "agent-1",
		Name:         "Test Agent",
		Description:  "A test agent",
		Capabilities: []string{"analysis", "coding"},
	}

	err := r.Register(profile)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// 验证注册成功
	p, ok := r.Get("agent-1")
	if !ok {
		t.Fatal("agent not found after registration")
	}
	if p.Name != "Test Agent" {
		t.Errorf("Name mismatch: got %s, want Test Agent", p.Name)
	}
	if p.Status != StatusOnline {
		t.Errorf("Status should be online, got %s", p.Status)
	}
}

func TestRegistryDeregister(t *testing.T) {
	r := NewRegistry()

	profile := &AgentProfile{ID: "agent-1", Name: "Test"}
	_ = r.Register(profile)

	err := r.Deregister("agent-1")
	if err != nil {
		t.Fatalf("deregister: %v", err)
	}

	_, ok := r.Get("agent-1")
	if ok {
		t.Error("agent should not exist after deregistration")
	}
}

func TestRegistryListByCapability(t *testing.T) {
	r := NewRegistry()

	_ = r.Register(&AgentProfile{ID: "agent-1", Capabilities: []string{"analysis", "coding"}})
	_ = r.Register(&AgentProfile{ID: "agent-2", Capabilities: []string{"writing"}})
	_ = r.Register(&AgentProfile{ID: "agent-3", Capabilities: []string{"coding", "testing"}})

	codingAgents := r.ListByCapability("coding")
	if len(codingAgents) != 2 {
		t.Errorf("expected 2 coding agents, got %d", len(codingAgents))
	}
}

func TestRegistryHealthCheck(t *testing.T) {
	r := NewRegistry()

	// 注册一个 agent，手动设置 LastSeen 为很久以前
	profile := &AgentProfile{ID: "agent-1", Status: StatusOnline}
	profile.LastSeen = time.Now().Add(-10 * time.Minute)
	_ = r.Register(profile)

	// 5 分钟超时
	stale := r.HealthCheck(5 * time.Minute)
	if stale != 1 {
		t.Errorf("expected 1 stale agent, got %d", stale)
	}

	p, _ := r.Get("agent-1")
	if p.Status != StatusOffline {
		t.Errorf("agent should be offline after health check, got %s", p.Status)
	}
}

func TestRegistryCount(t *testing.T) {
	r := NewRegistry()

	_ = r.Register(&AgentProfile{ID: "agent-1", Status: StatusOnline})
	_ = r.Register(&AgentProfile{ID: "agent-2", Status: StatusBusy})
	_ = r.Register(&AgentProfile{ID: "agent-3", Status: StatusOffline})

	total, online, busy, offline := r.Count()
	if total != 3 {
		t.Errorf("total: got %d, want 3", total)
	}
	if online != 1 {
		t.Errorf("online: got %d, want 1", online)
	}
	if busy != 1 {
		t.Errorf("busy: got %d, want 1", busy)
	}
	if offline != 1 {
		t.Errorf("offline: got %d, want 1", offline)
	}
}

func TestDelegateManagerPipeline(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&AgentProfile{ID: "agent-1", Name: "Agent 1"})
	_ = r.Register(&AgentProfile{ID: "agent-2", Name: "Agent 2"})

	// Mock handler - 每次调用添加后缀
	handler := TaskHandlerFunc(func(ctx context.Context, task *SubTask) (string, error) {
		return task.Input + "->processed_by_" + task.AgentID, nil
	})

	dm := NewDelegateManager(r, handler)

	task, err := dm.Delegate(context.Background(), ModePipeline, "test pipeline", "input", []string{"agent-1", "agent-2"}, 10*time.Second)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}

	// 等待完成
	time.Sleep(100 * time.Millisecond)

	updated, ok := dm.GetTask(task.ID)
	if !ok {
		t.Fatal("task not found")
	}

	if updated.State != TaskCompleted {
		t.Errorf("task state: got %s, want completed", updated.State)
	}

	// Pipeline: input -> agent-1 -> agent-2
	expected := "input->processed_by_agent-1->processed_by_agent-2"
	if updated.Result != expected {
		t.Errorf("result: got %s, want %s", updated.Result, expected)
	}
}

func TestDelegateManagerParallel(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&AgentProfile{ID: "agent-1", Name: "Agent 1"})
	_ = r.Register(&AgentProfile{ID: "agent-2", Name: "Agent 2"})
	_ = r.Register(&AgentProfile{ID: "agent-3", Name: "Agent 3"})

	var callCount int64 // 使用 int64 以便原子操作
	handler := TaskHandlerFunc(func(ctx context.Context, task *SubTask) (string, error) {
		atomic.AddInt64(&callCount, 1) // 原子递增，避免 data race
		return "result_from_" + task.AgentID, nil
	})

	dm := NewDelegateManager(r, handler)

	task, err := dm.Delegate(context.Background(), ModeParallel, "test parallel", "input", []string{"agent-1", "agent-2", "agent-3"}, 10*time.Second)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}

	// 等待完成
	time.Sleep(100 * time.Millisecond)

	updated, ok := dm.GetTask(task.ID)
	if !ok {
		t.Fatal("task not found")
	}

	if updated.State != TaskCompleted {
		t.Errorf("task state: got %s, want completed", updated.State)
	}

	// 所有 agent 都应该被调用
	if atomic.LoadInt64(&callCount) != 3 {
		t.Errorf("call count: got %d, want 3", callCount)
	}
}

func TestDelegateManagerCancel(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&AgentProfile{ID: "agent-1", Name: "Agent 1"})

	// 慢 handler
	handler := TaskHandlerFunc(func(ctx context.Context, task *SubTask) (string, error) {
		time.Sleep(2 * time.Second)
		return "done", nil
	})

	dm := NewDelegateManager(r, handler)

	task, _ := dm.Delegate(context.Background(), ModePipeline, "test cancel", "input", []string{"agent-1"}, 10*time.Second)

	// 立即取消
	err := dm.CancelTask(task.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	updated, _ := dm.GetTask(task.ID)
	if updated.State != TaskCancelled {
		t.Errorf("task state: got %s, want cancelled", updated.State)
	}
}

func TestDelegateManagerStats(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&AgentProfile{ID: "agent-1", Name: "Agent 1"})

	handler := TaskHandlerFunc(func(ctx context.Context, task *SubTask) (string, error) {
		return "done", nil
	})

	dm := NewDelegateManager(r, handler)

	// 创建多个任务
	_, _ = dm.Delegate(context.Background(), ModeParallel, "task 1", "input", []string{"agent-1"}, 10*time.Second)
	_, _ = dm.Delegate(context.Background(), ModeParallel, "task 2", "input", []string{"agent-1"}, 10*time.Second)

	time.Sleep(100 * time.Millisecond)

	total, running, completed, _ := dm.Stats()
	if total != 2 {
		t.Errorf("total: got %d, want 2", total)
	}
	// running + completed 应该等于 total
	if running+completed != total {
		t.Errorf("running+completed: got %d+%d=%d, want %d", running, completed, running+completed, total)
	}
}

func TestAggregatorConcat(t *testing.T) {
	a := NewAggregator()
	results := []string{"result1", "result2", "result3"}

	agg := a.Aggregate(AggConcat, results)

	if agg.Strategy != AggConcat {
		t.Errorf("strategy: got %s, want concat", agg.Strategy)
	}
	if agg.Count != 3 {
		t.Errorf("count: got %d, want 3", agg.Count)
	}
	if agg.Output != "result1\n\n---\n\nresult2\n\n---\n\nresult3" {
		t.Errorf("output mismatch: %s", agg.Output)
	}
}

func TestAggregatorBest(t *testing.T) {
	a := NewAggregator()
	results := []string{"short", "medium length result here", "very long result that should be the best because it has the most content"}

	agg := a.Aggregate(AggBest, results)

	if agg.Strategy != AggBest {
		t.Errorf("strategy: got %s, want best", agg.Strategy)
	}
	// 最长的应该是最佳
	if agg.BestIndex != 2 {
		t.Errorf("best index: got %d, want 2", agg.BestIndex)
	}
}

func TestAggregatorVote(t *testing.T) {
	a := NewAggregator()
	// 两个相似结果，一个不同
	results := []string{
		"same prefix result one",
		"same prefix result two",
		"different result",
	}

	agg := a.Aggregate(AggVote, results)

	if agg.Strategy != AggVote {
		t.Errorf("strategy: got %s, want vote", agg.Strategy)
	}
	// 应该有投票结果
	if len(agg.Votes) == 0 {
		t.Error("votes should not be empty")
	}
}

func TestAggregatorMerge(t *testing.T) {
	a := NewAggregator()
	results := []string{
		"line1\nline2\nline3",
		"line2\nline4",
		"line1\nline5",
	}

	agg := a.Aggregate(AggMerge, results)

	if agg.Strategy != AggMerge {
		t.Errorf("strategy: got %s, want merge", agg.Strategy)
	}
	// 应该去重
	if agg.Count != 3 {
		t.Errorf("count: got %d, want 3", agg.Count)
	}
}

func TestAggregatorSummary(t *testing.T) {
	a := NewAggregator()
	results := []string{
		"short",
		strings.Repeat("x", 300), // 长结果
	}

	agg := a.Aggregate(AggSummary, results)

	if agg.Strategy != AggSummary {
		t.Errorf("strategy: got %s, want summary", agg.Strategy)
	}
	// 长结果应该被截断
	if !strings.Contains(agg.Output, "...") {
		t.Error("long result should be truncated with ...")
	}
}

func TestAggregatorEmpty(t *testing.T) {
	a := NewAggregator()

	agg := a.Aggregate(AggConcat, []string{})

	if agg.Count != 0 {
		t.Errorf("count: got %d, want 0", agg.Count)
	}
	if agg.Output != "" {
		t.Errorf("output should be empty, got %s", agg.Output)
	}
}

func TestModeConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  ModeConfig
		wantErr bool
	}{
		{
			name:    "valid pipeline",
			config:  DefaultModeConfig(ModePipeline),
			wantErr: false,
		},
		{
			name:    "valid parallel",
			config:  DefaultModeConfig(ModeParallel),
			wantErr: false,
		},
		{
			name:    "valid debate",
			config:  DefaultModeConfig(ModeDebate),
			wantErr: false,
		},
		{
			name:    "invalid mode",
			config:  ModeConfig{Mode: CollabMode("invalid")},
			wantErr: true,
		},
		{
			name:    "invalid debate rounds",
			config:  ModeConfig{Mode: ModeDebate, DebateRounds: 0},
			wantErr: true,
		},
		{
			name:    "invalid max concurrent",
			config:  ModeConfig{Mode: ModeParallel, MaxConcurrent: 0},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		input    string
		expected CollabMode
		wantErr  bool
	}{
		{"pipeline", ModePipeline, false},
		{"Pipeline", ModePipeline, false},
		{"PIPELINE", ModePipeline, false},
		{"parallel", ModeParallel, false},
		{"debate", ModeDebate, false},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			mode, err := ParseMode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMode() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && mode != tt.expected {
				t.Errorf("mode: got %s, want %s", mode, tt.expected)
			}
		})
	}
}

func TestParseAggregationStrategy(t *testing.T) {
	tests := []struct {
		input    string
		expected AggregationStrategy
		wantErr  bool
	}{
		{"concat", AggConcat, false},
		{"best", AggBest, false},
		{"vote", AggVote, false},
		{"merge", AggMerge, false},
		{"summary", AggSummary, false},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			strategy, err := ParseAggregationStrategy(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseAggregationStrategy() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && strategy != tt.expected {
				t.Errorf("strategy: got %s, want %s", strategy, tt.expected)
			}
		})
	}
}

func TestModeDescription(t *testing.T) {
	desc := ModeDescription(ModePipeline)
	if desc == "" {
		t.Error("description should not be empty")
	}
	if !strings.Contains(desc, "Pipeline") {
		t.Errorf("description should contain 'Pipeline': %s", desc)
	}
}

func TestAllModes(t *testing.T) {
	modes := AllModes()
	if len(modes) != 3 {
		t.Errorf("expected 3 modes, got %d", len(modes))
	}
}

func TestPriorityString(t *testing.T) {
	tests := []struct {
		priority Priority
		expected string
	}{
		{PriorityLow, "low"},
		{PriorityNormal, "normal"},
		{PriorityHigh, "high"},
		{PriorityCritical, "critical"},
		{Priority(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.priority.String(); got != tt.expected {
				t.Errorf("String(): got %s, want %s", got, tt.expected)
			}
		})
	}
}
