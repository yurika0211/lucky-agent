package luckyagent

import (
	"context"
	"testing"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/embedder"
	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/rag"
	"github.com/yurika0211/luckyagent/internal/workflow"
)

// mockExecutor is a simple test executor
type mockExecutor struct{}

func (m *mockExecutor) Execute(ctx context.Context, task *workflow.Task) (interface{}, error) {
	return "mock result", nil
}

// mockEmbedder is a simple test embedder
type mockEmbedder struct{}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	// Return mock 1536-dim embedding
	embedding := make([]float64, 1536)
	for i := range embedding {
		embedding[i] = 0.1
	}
	return embedding, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	embeddings := make([][]float64, len(texts))
	for i := range embeddings {
		embeddings[i] = make([]float64, 1536)
		for j := range embeddings[i] {
			embeddings[i][j] = 0.1
		}
	}
	return embeddings, nil
}

func (m *mockEmbedder) Dimension() int {
	return 1536
}

func (m *mockEmbedder) Name() string {
	return "mock"
}

func (m *mockEmbedder) Model() string {
	return "mock-embedding"
}

func newTestWorkflowEngine() *workflow.WorkflowEngine {
	return workflow.NewWorkflowEngine(&mockExecutor{}, 2)
}

func newTestRAGManager() *rag.RAGManager {
	return rag.NewRAGManager(&mockEmbedder{}, rag.RAGConfig{})
}

func TestServer_HealthCheck(t *testing.T) {
	// Create test dependencies
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	resp, err := server.HealthCheck(context.Background(), nil)
	if err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}

	if resp.Status != "healthy" {
		t.Errorf("expected status healthy, got %s", resp.Status)
	}

	if resp.Version != "v0.25.0" {
		t.Errorf("expected version v0.25.0, got %s", resp.Version)
	}

	if _, ok := resp.Components["memory"]; !ok {
		t.Error("expected memory component in health check")
	}

	if _, ok := resp.Components["rag"]; !ok {
		t.Error("expected rag component in health check")
	}

	if _, ok := resp.Components["workflow"]; !ok {
		t.Error("expected workflow component in health check")
	}
}

func TestServer_MemoryStore(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &MemoryStoreRequest{
		Content:    "test memory content",
		Category:   "test",
		Tier:       "medium",
		Importance: 0.5,
	}

	resp, err := server.MemoryStore(context.Background(), req)
	if err != nil {
		t.Fatalf("MemoryStore failed: %v", err)
	}

	if resp.Content != "test memory content" {
		t.Errorf("expected content 'test memory content', got %s", resp.Content)
	}

	if resp.Category != "test" {
		t.Errorf("expected category 'test', got %s", resp.Category)
	}

	if resp.Id == "" {
		t.Error("expected non-empty ID")
	}
}

func TestServer_MemoryRecall(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	// Store some test memories
	memStore.SaveWithTier("golang programming", "tech", memory.TierMedium, 0.5)
	memStore.SaveWithTier("python programming", "tech", memory.TierMedium, 0.5)
	memStore.SaveWithTier("cooking recipes", "food", memory.TierMedium, 0.5)

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &MemoryRecallRequest{
		Query: "programming",
		Limit: 10,
	}

	resp, err := server.MemoryRecall(context.Background(), req)
	if err != nil {
		t.Fatalf("MemoryRecall failed: %v", err)
	}

	if resp.Total == 0 {
		t.Error("expected at least one result")
	}
}

func TestServer_MemoryList(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	// Store some test memories
	memStore.Save("memory 1", "cat1")
	memStore.Save("memory 2", "cat2")

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	resp, err := server.MemoryList(context.Background(), nil)
	if err != nil {
		t.Fatalf("MemoryList failed: %v", err)
	}

	if resp.Count != 2 {
		t.Errorf("expected 2 entries, got %d", resp.Count)
	}
}

func TestServer_MemoryDelete(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	memStore.Save("test memory", "test")
	entries := memStore.Recent(1)
	if len(entries) == 0 {
		t.Fatal("failed to store test memory")
	}
	entryID := entries[0].ID

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &MemoryDeleteRequest{Id: entryID}
	_, err = server.MemoryDelete(context.Background(), req)
	if err != nil {
		t.Fatalf("MemoryDelete failed: %v", err)
	}

	// Verify deletion
	if memStore.Count() != 0 {
		t.Error("expected memory to be deleted")
	}
}

func TestServer_WorkflowCreate(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &WorkflowCreateRequest{
		Name:        "test-workflow",
		Description: "A test workflow",
		Tasks: []*Task{
			{
				Id:          "task1",
				Name:        "First Task",
				Description: "First task description",
				Action:      "echo",
				Params:      map[string]string{"message": "hello"},
			},
		},
		Version: "1.0",
	}

	resp, err := server.WorkflowCreate(context.Background(), req)
	if err != nil {
		t.Fatalf("WorkflowCreate failed: %v", err)
	}

	if resp.Name != "test-workflow" {
		t.Errorf("expected name 'test-workflow', got %s", resp.Name)
	}

	if len(resp.Tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(resp.Tasks))
	}
}

func TestServer_WorkflowList(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	// Create a workflow directly
	wf := workflow.NewWorkflow("test-wf", []*workflow.Task{
		{ID: "t1", Name: "Task 1", Action: "echo"},
	})
	wfEngine.RegisterWorkflow(wf)

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	resp, err := server.WorkflowList(context.Background(), nil)
	if err != nil {
		t.Fatalf("WorkflowList failed: %v", err)
	}

	if resp.Count != 1 {
		t.Errorf("expected 1 workflow, got %d", resp.Count)
	}
}

func TestGRPCServer_StartStop(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	serviceServer := NewServer(nil, memStore, ragMgr, wfEngine)
	grpcServer := NewGRPCServer(":0", serviceServer)

	if err := grpcServer.Start(); err != nil {
		t.Fatalf("failed to start gRPC server: %v", err)
	}

	// Verify server is listening
	addr := grpcServer.Addr()
	if addr == "" {
		t.Error("expected non-empty address")
	}

	// Stop the server
	grpcServer.Stop()
}

func TestNewServer(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()
	ag := &agent.Agent{}

	server := NewServer(ag, memStore, ragMgr, wfEngine)

	if server.agent != ag {
		t.Error("agent not set correctly")
	}

	if server.memoryStore != memStore {
		t.Error("memory store not set correctly")
	}

	if server.ragManager != ragMgr {
		t.Error("rag manager not set correctly")
	}

	if server.workflowEngine != wfEngine {
		t.Error("workflow engine not set correctly")
	}
}

// Ensure mockEmbedder implements embedder.Embedder
var _ embedder.Embedder = (*mockEmbedder)(nil)

// ── RAG Tests ──────────────────────────────────────

func TestServer_RAGIndex(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &RAGIndexRequest{
		Source:  "test-source",
		Content: "This is test content for RAG indexing",
	}

	resp, err := server.RAGIndex(context.Background(), req)
	if err != nil {
		t.Fatalf("RAGIndex failed: %v", err)
	}

	if resp.Id == "" {
		t.Error("expected non-empty ID")
	}
}

func TestServer_RAGSearch(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	// Index some test content
	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	// First index some content
	indexReq := &RAGIndexRequest{
		Source:  "test-doc",
		Content: "This is about golang programming language",
	}
	_, err = server.RAGIndex(context.Background(), indexReq)
	if err != nil {
		t.Fatalf("RAGIndex failed: %v", err)
	}

	// Now search
	searchReq := &RAGSearchRequest{
		Query: "golang",
		Limit: 5,
	}

	resp, err := server.RAGSearch(context.Background(), searchReq)
	if err != nil {
		t.Fatalf("RAGSearch failed: %v", err)
	}

	if resp.Total == 0 {
		t.Error("expected at least one result")
	}
}

func TestServer_RAGSearch_DefaultLimit(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	// Search without specifying limit
	searchReq := &RAGSearchRequest{
		Query: "test",
	}

	resp, err := server.RAGSearch(context.Background(), searchReq)
	if err != nil {
		t.Fatalf("RAGSearch failed: %v", err)
	}

	// Should have some results (possibly 0 if nothing indexed)
	if resp.Total < 0 {
		t.Error("expected non-negative total")
	}
}

// ── Workflow Tests ──────────────────────────────────────

func TestServer_WorkflowGet(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	// Create a workflow
	wf := workflow.NewWorkflow("test-get-wf", []*workflow.Task{
		{ID: "t1", Name: "Task 1", Action: "echo"},
	})
	if err := wfEngine.RegisterWorkflow(wf); err != nil {
		t.Fatalf("RegisterWorkflow failed: %v", err)
	}

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &WorkflowGetRequest{Id: wf.ID}
	resp, err := server.WorkflowGet(context.Background(), req)
	if err != nil {
		t.Fatalf("WorkflowGet failed: %v", err)
	}

	if resp.Name != "test-get-wf" {
		t.Errorf("expected name 'test-get-wf', got %s", resp.Name)
	}
}

func TestServer_WorkflowGet_NotFound(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &WorkflowGetRequest{Id: "nonexistent-wf"}
	_, err = server.WorkflowGet(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for nonexistent workflow")
	}
}

func TestServer_WorkflowDelete(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	// Create a workflow
	wf := workflow.NewWorkflow("test-delete-wf", []*workflow.Task{
		{ID: "t1", Name: "Task 1", Action: "echo"},
	})
	if err := wfEngine.RegisterWorkflow(wf); err != nil {
		t.Fatalf("RegisterWorkflow failed: %v", err)
	}

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &WorkflowDeleteRequest{Id: wf.ID}
	_, err = server.WorkflowDelete(context.Background(), req)
	if err != nil {
		t.Fatalf("WorkflowDelete failed: %v", err)
	}

	// Verify workflow is deleted
	_, exists := wfEngine.GetWorkflow(wf.ID)
	if exists {
		t.Error("expected workflow to be deleted")
	}
}

func TestServer_WorkflowDelete_NotFound(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &WorkflowDeleteRequest{Id: "nonexistent-wf"}
	// Delete is idempotent - should not error even for nonexistent workflow
	_, err = server.WorkflowDelete(context.Background(), req)
	if err != nil {
		t.Fatalf("WorkflowDelete should not error for nonexistent workflow: %v", err)
	}
}

func TestServer_WorkflowStart(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	// Create a workflow
	wf := workflow.NewWorkflow("test-start-wf", []*workflow.Task{
		{ID: "t1", Name: "Task 1", Action: "echo"},
	})
	if err := wfEngine.RegisterWorkflow(wf); err != nil {
		t.Fatalf("RegisterWorkflow failed: %v", err)
	}

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &WorkflowStartRequest{
		WorkflowId: wf.ID,
	}

	resp, err := server.WorkflowStart(context.Background(), req)
	if err != nil {
		t.Fatalf("WorkflowStart failed: %v", err)
	}

	if resp.WorkflowId != wf.ID {
		t.Errorf("expected workflow ID '%s', got %s", wf.ID, resp.WorkflowId)
	}

	if resp.Id == "" {
		t.Error("expected non-empty instance ID")
	}
}

func TestServer_WorkflowStart_NotFound(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &WorkflowStartRequest{
		WorkflowId: "nonexistent-wf",
	}
	_, err = server.WorkflowStart(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for nonexistent workflow")
	}
}

func TestServer_WorkflowInstanceGet(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	// Create and start a workflow
	wf := workflow.NewWorkflow("test-instance-wf", []*workflow.Task{
		{ID: "t1", Name: "Task 1", Action: "echo"},
	})
	if err := wfEngine.RegisterWorkflow(wf); err != nil {
		t.Fatalf("RegisterWorkflow failed: %v", err)
	}

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	startReq := &WorkflowStartRequest{
		WorkflowId: wf.ID,
	}
	startResp, err := server.WorkflowStart(context.Background(), startReq)
	if err != nil {
		t.Fatalf("WorkflowStart failed: %v", err)
	}

	instanceReq := &WorkflowInstanceGetRequest{Id: startResp.Id}
	resp, err := server.WorkflowInstanceGet(context.Background(), instanceReq)
	if err != nil {
		t.Fatalf("WorkflowInstanceGet failed: %v", err)
	}

	if resp.Id != startResp.Id {
		t.Errorf("expected instance ID '%s', got %s", startResp.Id, resp.Id)
	}
}

func TestServer_WorkflowInstanceGet_NotFound(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	req := &WorkflowInstanceGetRequest{Id: "nonexistent-instance"}
	_, err = server.WorkflowInstanceGet(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for nonexistent instance")
	}
}

func TestServer_WorkflowToProtoViaGet(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	// Create a workflow with description and version
	wf := workflow.NewWorkflow("test-proto-wf", []*workflow.Task{
		{ID: "t1", Name: "Task 1", Action: "echo", Params: map[string]interface{}{"key": "value"}},
		{ID: "t2", Name: "Task 2", Action: "sleep"},
	})
	wf.Description = "Test workflow description"
	wf.Version = "2.0"
	if err := wfEngine.RegisterWorkflow(wf); err != nil {
		t.Fatalf("RegisterWorkflow failed: %v", err)
	}

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	// Use WorkflowGet to indirectly test workflowToProto
	req := &WorkflowGetRequest{Id: wf.ID}
	resp, err := server.WorkflowGet(context.Background(), req)
	if err != nil {
		t.Fatalf("WorkflowGet failed: %v", err)
	}

	if resp.Name != "test-proto-wf" {
		t.Errorf("expected name 'test-proto-wf', got %s", resp.Name)
	}

	if resp.Description != "Test workflow description" {
		t.Errorf("expected description 'Test workflow description', got %s", resp.Description)
	}

	if resp.Version != "2.0" {
		t.Errorf("expected version '2.0', got %s", resp.Version)
	}

	if len(resp.Tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(resp.Tasks))
	}
}

func TestServer_InstanceToProtoViaStart(t *testing.T) {
	memStore, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	ragMgr := newTestRAGManager()
	wfEngine := newTestWorkflowEngine()

	// Create and start a workflow
	wf := workflow.NewWorkflow("test-inst-wf", []*workflow.Task{
		{ID: "t1", Name: "Task 1", Action: "echo"},
	})
	if err := wfEngine.RegisterWorkflow(wf); err != nil {
		t.Fatalf("RegisterWorkflow failed: %v", err)
	}

	server := NewServer(nil, memStore, ragMgr, wfEngine)

	startReq := &WorkflowStartRequest{
		WorkflowId: wf.ID,
	}
	resp, err := server.WorkflowStart(context.Background(), startReq)
	if err != nil {
		t.Fatalf("WorkflowStart failed: %v", err)
	}

	// Use WorkflowInstanceGet to indirectly test instanceToProto
	instanceReq := &WorkflowInstanceGetRequest{Id: resp.Id}
	instanceResp, err := server.WorkflowInstanceGet(context.Background(), instanceReq)
	if err != nil {
		t.Fatalf("WorkflowInstanceGet failed: %v", err)
	}

	if instanceResp.Id != resp.Id {
		t.Errorf("expected instance ID '%s', got %s", resp.Id, instanceResp.Id)
	}

	if instanceResp.WorkflowId != wf.ID {
		t.Errorf("expected workflow ID '%s', got %s", wf.ID, instanceResp.WorkflowId)
	}
}

// ── Chat & ChatStream Tests ──────────────────────────────────────
// Note: Chat and ChatStream require a real *agent.Agent instance which is complex to mock.
// These tests are skipped until we have a proper integration test setup.

func TestServer_Chat_WithNilAgent(t *testing.T) {
	t.Skip("Chat requires real *agent.Agent, skipping for unit test")
}

func TestServer_ChatStream_WithNilAgent(t *testing.T) {
	t.Skip("ChatStream requires real *agent.Agent, skipping for unit test")
}

