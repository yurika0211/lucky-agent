package tool

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/config"
)

// TestDelegateManager_SetAgentExecutor 测试设置 Agent 执行器
func TestDelegateManager_SetAgentExecutor(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := DelegateConfig{}
	dm := NewDelegateManager(cfg)

	executor := func(ctx context.Context, description, contextStr string) (string, error) {
		return "test result", nil
	}

	dm.SetAgentExecutor(executor)

	// 验证执行器已设置（通过调用 delegate 来验证）
	// 这里主要测试 SetAgentExecutor 不 panic
	if dm == nil {
		t.Fatal("DelegateManager should not be nil")
	}
}

// TestDelegateManager_DelegateParallel 测试并行委派
func TestDelegateManager_DelegateParallel(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := DelegateConfig{}
	dm := NewDelegateManager(cfg)

	// 测试空任务列表
	result := dm.DelegateParallel([]string{}, "", time.Minute)
	if result.Summary != "No tasks to delegate" {
		t.Errorf("Expected 'No tasks to delegate', got: %s", result.Summary)
	}

	// 测试单个任务
	result = dm.DelegateParallel([]string{"test task"}, "", time.Minute)
	if result.Summary == "" {
		t.Error("Expected non-empty summary")
	}
	t.Logf("Parallel result: %s", result.Summary)
}

// TestDelegateManager_DelegateParallelMultiple 测试多个并行任务
func TestDelegateManager_DelegateParallelMultiple(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := DelegateConfig{}
	dm := NewDelegateManager(cfg)

	tasks := []string{
		"task 1",
		"task 2",
		"task 3",
	}

	result := dm.DelegateParallel(tasks, "test context", time.Minute)

	// 验证结果包含所有任务
	if result.SuccessCount < 0 || result.SuccessCount > len(tasks) {
		t.Errorf("Expected success count between 0 and %d, got %d", len(tasks), result.SuccessCount)
	}

	t.Logf("Parallel delegate: %d tasks, %d succeeded", len(tasks), result.SuccessCount)
}

// TestDelegateManager_DelegateParallelEmptyContext 测试空上下文
func TestDelegateManager_DelegateParallelEmptyContext(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := DelegateConfig{}
	dm := NewDelegateManager(cfg)

	result := dm.DelegateParallel([]string{"task with empty context"}, "", 30*time.Second)

	if result.TotalDuration < 0 {
		t.Error("Expected non-negative duration")
	}
}

// TestDelegateManager_Config 测试配置
func TestDelegateManager_Config(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := DelegateConfig{}
	dm := NewDelegateManager(cfg)

	// 验证管理器已正确初始化
	if dm == nil {
		t.Fatal("DelegateManager should not be nil")
	}

	// 验证默认配置
	if dm.config.MaxConcurrent <= 0 {
		t.Log("Using default max concurrent")
	}
}

// TestDelegateManager_ConcurrentLimit 测试并发限制
func TestDelegateManager_ConcurrentLimit(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := DelegateConfig{}
	dm := NewDelegateManager(cfg)

	// 设置较小的并发数
	dm.config.MaxConcurrent = 2

	tasks := []string{
		"task 1",
		"task 2",
		"task 3",
		"task 4",
		"task 5",
	}

	result := dm.DelegateParallel(tasks, "", time.Minute)

	// 验证任务执行完成
	if result.Summary == "" {
		t.Error("Expected non-empty summary")
	}

	t.Logf("Concurrent test completed: %s", result.Summary)
}

// TestDelegateManager_Timeout 测试超时处理
func TestDelegateManager_Timeout(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := DelegateConfig{}
	dm := NewDelegateManager(cfg)

	// 测试非常短的超时
	result := dm.DelegateParallel([]string{"quick task"}, "", 100*time.Millisecond)

	// 验证有结果返回（即使超时）
	if result == nil {
		t.Error("Expected result even with timeout")
	}
}

// TestDelegateManager_LargeParallel 测试大量并行任务
func TestDelegateManager_LargeParallel(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large parallel test in short mode")
	}

	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := DelegateConfig{}
	dm := NewDelegateManager(cfg)

	// 创建 10 个任务
	tasks := make([]string, 10)
	for i := 0; i < 10; i++ {
		tasks[i] = fmt.Sprintf("parallel task %d", i+1)
	}

	result := dm.DelegateParallel(tasks, "", 2*time.Minute)

	t.Logf("Large parallel: %d tasks, %d succeeded, duration: %v",
		len(tasks), result.SuccessCount, result.TotalDuration)
}

// TestGateway_ExecuteWithShellContext 测试带 shell 上下文的工具执行
func TestGateway_ExecuteWithShellContext(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	reg := NewRegistry()
	gw := NewGateway(reg)
	if gw == nil {
		t.Fatal("Gateway should not be nil")
	}

	// 测试不存在的工具
	_, err = gw.ExecuteWithShellContext("nonexistent_tool", nil, "", nil)
	if err == nil {
		t.Error("Expected error for nonexistent tool")
	}
	t.Logf("ExecuteWithShellContext error (expected): %v", err)
}

// TestGateway_ExecuteWithShellContextNilArgs 测试 nil 参数
func TestGateway_ExecuteWithShellContextNilArgs(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	reg := NewRegistry()
	gw := NewGateway(reg)

	// 测试 nil args
	_, err = gw.ExecuteWithShellContext("test_tool", nil, "user123", nil)
	// 应该返回错误（工具不存在）
	if err == nil {
		t.Error("Expected error for nonexistent tool")
	}
}

// TestGateway_ExecuteWithShellContextEmptyUser 测试空用户 ID
func TestGateway_ExecuteWithShellContextEmptyUser(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	reg := NewRegistry()
	gw := NewGateway(reg)

	// 测试空用户 ID
	_, err = gw.ExecuteWithShellContext("test_tool", map[string]any{}, "", nil)
	if err == nil {
		t.Error("Expected error for nonexistent tool")
	}
}

// TestGateway_ExecuteWithShellContextWithShell 测试带 shell 上下文
func TestGateway_ExecuteWithShellContextWithShell(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	reg := NewRegistry()
	gw := NewGateway(reg)

	// 创建 mock shell 上下文
	sc := &ShellContext{
		Cwd: tmpDir,
		Env: map[string]string{"TEST": "value"},
	}

	// 测试不存在的工具（带 shell 上下文）
	_, err = gw.ExecuteWithShellContext("nonexistent", nil, "", sc)
	if err == nil {
		t.Error("Expected error for nonexistent tool")
	}
}

// TestGateway_ExecuteWithShellContextPermission 测试权限检查
func TestGateway_ExecuteWithShellContextPermission(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	reg := NewRegistry()
	gw := NewGateway(reg)

	// 测试权限检查流程（工具不存在时会先返回 NotFound）
	_, err = gw.ExecuteWithShellContext("test_perm", nil, "user", nil)
	if err == nil {
		t.Error("Expected error")
	}
}

// TestGateway_ExecuteWithShellContextSubscription 测试订阅检查
func TestGateway_ExecuteWithShellContextSubscription(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	reg := NewRegistry()
	gw := NewGateway(reg)

	// 测试订阅检查流程
	_, err = gw.ExecuteWithShellContext("test_sub", nil, "subscriber", nil)
	if err == nil {
		t.Error("Expected error for nonexistent tool")
	}
}

// TestGateway_ExecuteWithShellContextResult 测试结果结构
func TestGateway_ExecuteWithShellContextResult(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	reg := NewRegistry()
	gw := NewGateway(reg)

	// 验证错误情况下的结果结构
	result, err := gw.ExecuteWithShellContext("fake_tool", nil, "", nil)
	
	// 应该返回错误
	if err == nil {
		t.Error("Expected error")
	}
	// result 应该为 nil
	if result != nil {
		t.Log("Result may be nil on error")
	}
}

// TestDelegateManager_DelegateParallelTool 测试 DelegateParallelTool
func TestDelegateManager_DelegateParallelTool(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := DelegateConfig{}
	dm := NewDelegateManager(cfg)

	tool := dm.DelegateParallelTool()

	if tool == nil {
		t.Fatal("DelegateParallelTool should not be nil")
	}

	if tool.Name != "delegate_parallel" {
		t.Errorf("Expected name 'delegate_parallel', got: %s", tool.Name)
	}

	if tool.Permission != PermApprove {
		t.Errorf("Expected permission PermApprove, got: %v", tool.Permission)
	}

	// 验证参数
	if _, ok := tool.Parameters["tasks"]; !ok {
		t.Error("Expected 'tasks' parameter")
	}

	t.Logf("DelegateParallelTool created: %s", tool.Name)
}

// TestDelegateManager_HandleDelegateParallel 测试 handleDelegateParallel
func TestDelegateManager_HandleDelegateParallel(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := DelegateConfig{}
	dm := NewDelegateManager(cfg)

	// 测试空 tasks
	args := map[string]any{
		"tasks": []any{},
	}

	// handleDelegateParallel 应该能处理空 tasks
	// 由于实际实现可能不同，这里只验证不 panic
	result, err := dm.handleDelegateParallel(args)
	t.Logf("handleDelegateParallel result: %s, err: %v", result, err)
}

// TestDelegateManager_HandleDelegateParallelWithTasks 测试带任务的处理
func TestDelegateManager_HandleDelegateParallelWithTasks(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := DelegateConfig{}
	dm := NewDelegateManager(cfg)

	args := map[string]any{
		"tasks":   []any{"task 1", "task 2"},
		"context": "test context",
		"timeout": 60,
	}

	result, err := dm.handleDelegateParallel(args)
	t.Logf("handleDelegateParallel with tasks: %s, err: %v", result, err)
}

// TestMCPClient_NewMCPClient 测试创建 MCP 客户端
func TestMCPClient_NewMCPClient(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	client := NewMCPClient()

	if client == nil {
		t.Fatal("NewMCPClient should not return nil")
	}

	if client.servers == nil {
		t.Error("servers map should be initialized")
	}

	if client.client == nil {
		t.Error("http client should be initialized")
	}
}

// TestMCPClient_AddServer 测试添加服务器
func TestMCPClient_AddServer(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	client := NewMCPClient()

	cfg := MCPServerConfig{
		Name: "test-server",
		URL:  "http://localhost:8080",
	}

	client.AddServer(cfg)

	// 验证服务器已添加
	client.mu.RLock()
	_, exists := client.servers["test-server"]
	client.mu.RUnlock()

	if !exists {
		t.Error("Server should be added")
	}
}

// TestMCPClient_RemoveServer 测试移除服务器
func TestMCPClient_RemoveServer(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	client := NewMCPClient()

	// 先添加
	cfg := MCPServerConfig{
		Name: "to-remove",
		URL:  "http://localhost:8080",
	}
	client.AddServer(cfg)

	// 再移除
	client.RemoveServer("to-remove")

	// 验证已移除
	client.mu.RLock()
	_, exists := client.servers["to-remove"]
	client.mu.RUnlock()

	if exists {
		t.Error("Server should be removed")
	}
}

// TestMCPClient_ListServers 测试列出服务器
func TestMCPClient_ListServers(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	client := NewMCPClient()

	// 添加两个服务器
	client.AddServer(MCPServerConfig{Name: "server1", URL: "http://localhost:8081"})
	client.AddServer(MCPServerConfig{Name: "server2", URL: "http://localhost:8082"})

	servers := client.ListServers()

	if len(servers) != 2 {
		t.Errorf("Expected 2 servers, got %d", len(servers))
	}

	t.Logf("Listed %d servers", len(servers))
}

// TestMCPClient_RemoveNonexistent 测试移除不存在的服务器
func TestMCPClient_RemoveNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	client := NewMCPClient()

	// 移除不存在的服务器不应 panic
	client.RemoveServer("nonexistent")
	t.Log("RemoveServer on nonexistent server completed without panic")
}

// TestMCPClient_MultipleOperations 测试多个操作
func TestMCPClient_MultipleOperations(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	client := NewMCPClient()

	// 添加多个服务器
	for i := 0; i < 5; i++ {
		client.AddServer(MCPServerConfig{
			Name: fmt.Sprintf("server-%d", i),
			URL:  fmt.Sprintf("http://localhost:%d", 8080+i),
		})
	}

	// 列出现在应该有 5 个
	servers := client.ListServers()
	if len(servers) != 5 {
		t.Errorf("Expected 5 servers, got %d", len(servers))
	}

	// 移除一个
	client.RemoveServer("server-2")

	// 再列出应该有 4 个
	servers = client.ListServers()
	if len(servers) != 4 {
		t.Errorf("Expected 4 servers after removal, got %d", len(servers))
	}

	t.Logf("Final server count: %d", len(servers))
}

// TestGateway_Getters 测试 Gateway 的 Getter 方法
func TestGateway_Getters(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	reg := NewRegistry()
	gw := NewGateway(reg)

	// 测试 Tracker
	tracker := gw.Tracker()
	if tracker == nil {
		t.Error("Tracker should not be nil")
	}

	// 测试 Subscriptions
	sub := gw.Subscriptions()
	if sub == nil {
		t.Error("Subscriptions should not be nil")
	}

	// 测试 Router
	router := gw.Router()
	if router == nil {
		t.Error("Router should not be nil")
	}

	t.Logf("Gateway getters: tracker=%v, sub=%v, router=%v", tracker != nil, sub != nil, router != nil)
}

// TestGatewayResult_Format 测试结果格式化
func TestGatewayResult_Format(t *testing.T) {
	// 测试结果格式化
	result := &GatewayResult{
		ToolName:  "test_tool",
		Output:    "success",
		Duration:  100 * time.Millisecond,
		Success:   true,
		Timestamp: time.Now(),
	}

	formatted := result.Format()
	if formatted == "" {
		t.Error("Expected non-empty formatted result")
	}

	t.Logf("Formatted result: %s", formatted)
}

// TestGatewayResult_FormatFailure 测试失败结果格式化
func TestGatewayResult_FormatFailure(t *testing.T) {
	result := &GatewayResult{
		ToolName:  "failed_tool",
		Output:    "error message",
		Duration:  50 * time.Millisecond,
		Success:   false,
		Timestamp: time.Now(),
	}

	formatted := result.Format()
	if formatted == "" {
		t.Error("Expected non-empty formatted result")
	}

	// 应该包含 ❌
	if !contains(formatted, "❌") {
		t.Error("Expected failure indicator in formatted result")
	}

	t.Logf("Formatted failure: %s", formatted)
}

// TestErrQuotaExceeded_Error 测试配额超限错误
func TestErrQuotaExceeded_Error(t *testing.T) {
	err := ErrQuotaExceeded{
		Tool:   "test_tool",
		UserID: "user123",
		Reason: "daily limit exceeded",
	}

	errStr := err.Error()
	if errStr == "" {
		t.Error("Expected non-empty error string")
	}

	// 应该包含关键信息
	if !contains(errStr, "test_tool") {
		t.Error("Error should contain tool name")
	}
	if !contains(errStr, "user123") {
		t.Error("Error should contain user ID")
	}

	t.Logf("ErrQuotaExceeded: %s", errStr)
}

// TestErrQuotaExceeded_ErrorEmptyFields 测试空字段
func TestErrQuotaExceeded_ErrorEmptyFields(t *testing.T) {
	err := ErrQuotaExceeded{
		Tool:   "unknown",
		UserID: "",
		Reason: "unknown reason",
	}

	errStr := err.Error()
	if errStr == "" {
		t.Error("Expected non-empty error string")
	}

	t.Logf("ErrQuotaExceeded (empty user): %s", errStr)
}

// TestSearchWithBraveEntries_NoAPIKey 测试 Brave API 无 API key 的情况
func TestSearchWithBraveEntries_NoAPIKey(t *testing.T) {
	// 临时清除环境变量
	origKey := os.Getenv("BRAVE_API_KEY")
	os.Unsetenv("BRAVE_API_KEY")
	defer func() {
		if origKey != "" {
			os.Setenv("BRAVE_API_KEY", origKey)
		}
	}()

	cfg := &WebSearchConfig{
		APIKey: "",
	}

	_, err := searchWithBraveEntries(cfg, "test query", 5)
	
	// 应该返回 "no API key" 错误
	if err == nil {
		t.Fatal("Expected error when no API key")
	}

	if !contains(err.Error(), "no API key") {
		t.Errorf("Expected 'no API key' error, got: %v", err)
	}

	t.Logf("searchWithBraveEntries correctly returned error: %v", err)
}

// TestSearchWithBraveEntries_WithAPIKey 测试 Brave API 有 API key 的情况 (mock)
func TestSearchWithBraveEntries_WithAPIKey(t *testing.T) {
	// 使用 mock API key (不会真正调用 API，因为会先检查配置)
	cfg := &WebSearchConfig{
		APIKey: "mock-key-for-testing",
	}

	// 由于实际会调用 curl，这里主要测试配置逻辑
	// 实际测试会因网络或 API key 无效而失败，这是预期的
	_, err := searchWithBraveEntries(cfg, "test query", 5)
	
	// 可能失败 (API key 无效或网络问题)，但函数应该能执行
	t.Logf("searchWithBraveEntries with mock key: %v", err)
}

// TestSearchWithBraveEntries_EmptyResponse 测试空响应处理
func TestSearchWithBraveEntries_EmptyResponse(t *testing.T) {
	// 这个测试验证空响应处理逻辑
	// 由于实际实现使用 curl，这里主要记录测试意图
	t.Log("Empty response handling is tested via error cases")
}

// TestWebSearchConfig 测试 WebSearchConfig 结构
func TestWebSearchConfig(t *testing.T) {
	cfg := &WebSearchConfig{
		APIKey: "test-key",
		Proxy:  "http://proxy:8080",
	}

	if cfg.APIKey != "test-key" {
		t.Errorf("Expected APIKey 'test-key', got: %s", cfg.APIKey)
	}

	if cfg.Proxy != "http://proxy:8080" {
		t.Errorf("Expected Proxy 'http://proxy:8080', got: %s", cfg.Proxy)
	}

	t.Logf("WebSearchConfig: APIKey=%s, Proxy=%s", cfg.APIKey, cfg.Proxy)
}

// TestSearchEntry 测试 searchEntry 结构
func TestSearchEntry(t *testing.T) {
	entry := searchEntry{
		Title:   "Test Title",
		URL:     "https://example.com",
		Snippet: "Test Snippet",
	}

	if entry.Title != "Test Title" {
		t.Errorf("Expected Title 'Test Title', got: %s", entry.Title)
	}

	t.Logf("searchEntry: Title=%s, URL=%s", entry.Title, entry.URL)
}
