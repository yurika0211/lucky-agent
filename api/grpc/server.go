// Package luckyagent provides gRPC server implementation for LuckyAgent.
package luckyagent

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/rag"
	"github.com/yurika0211/luckyagent/internal/workflow"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements LuckyAgentService gRPC server.
type Server struct {
	UnimplementedLuckyAgentServiceServer

	agent          *agent.Agent
	memoryStore    *memory.Store
	ragManager     *rag.RAGManager
	workflowEngine *workflow.WorkflowEngine

	mu      sync.RWMutex
	startAt time.Time
}

// NewServer creates a new gRPC server.
func NewServer(
	a *agent.Agent,
	ms *memory.Store,
	rm *rag.RAGManager,
	we *workflow.WorkflowEngine,
) *Server {
	return &Server{
		agent:          a,
		memoryStore:    ms,
		ragManager:     rm,
		workflowEngine: we,
		startAt:        time.Now(),
	}
}

// Chat handles single chat request.
func (s *Server) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	resp, err := s.agent.Chat(ctx, req.Message)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "chat failed: %v", err)
	}

	return &ChatResponse{
		Response:   resp,
		SessionId:  req.SessionId,
		Iterations: 1,
		TokensUsed: 0,
		Duration:   "0s",
	}, nil
}

// ChatStream handles streaming chat request.
func (s *Server) ChatStream(req *ChatRequest, stream LuckyAgentService_ChatStreamServer) error {
	resp, err := s.agent.Chat(stream.Context(), req.Message)
	if err != nil {
		return status.Errorf(codes.Internal, "chat failed: %v", err)
	}

	// Send response in chunks
	chunkSize := 100
	for i := 0; i < len(resp); i += chunkSize {
		end := i + chunkSize
		if end > len(resp) {
			end = len(resp)
		}

		chunk := &ChatChunk{
			Content:   resp[i:end],
			SessionId: req.SessionId,
			Done:      end >= len(resp),
		}

		if err := stream.Send(chunk); err != nil {
			return status.Errorf(codes.Internal, "stream send failed: %v", err)
		}
	}

	return nil
}

// MemoryStore stores a memory entry.
func (s *Server) MemoryStore(ctx context.Context, req *MemoryStoreRequest) (*MemoryEntry, error) {
	tier := memory.TierMedium
	switch req.Tier {
	case "short":
		tier = memory.TierShort
	case "long":
		tier = memory.TierLong
	}

	if err := s.memoryStore.SaveWithTier(req.Content, req.Category, tier, req.Importance); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to store memory: %v", err)
	}

	// Get the most recent entry
	entries := s.memoryStore.Recent(1)
	if len(entries) == 0 {
		return nil, status.Errorf(codes.Internal, "failed to retrieve stored memory")
	}

	entry := entries[0]
	return &MemoryEntry{
		Id:          entry.ID,
		Content:     entry.Content,
		Category:    entry.Category,
		Tier:        entry.Tier.String(),
		Importance:  entry.Importance,
		AccessCount: int32(entry.AccessCount),
		CreatedAt:   timestamppb.New(entry.CreatedAt),
		UpdatedAt:   timestamppb.New(entry.CreatedAt),
	}, nil
}

// MemoryRecall recalls memories matching query.
func (s *Server) MemoryRecall(ctx context.Context, req *MemoryRecallRequest) (*MemoryRecallResponse, error) {
	entries := s.memoryStore.Search(req.Query)

	limit := int(req.Limit)
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	pbEntries := make([]*MemoryEntry, len(entries))
	for i, e := range entries {
		pbEntries[i] = &MemoryEntry{
			Id:          e.ID,
			Content:     e.Content,
			Category:    e.Category,
			Tier:        e.Tier.String(),
			Importance:  e.Importance,
			AccessCount: int32(e.AccessCount),
			CreatedAt:   timestamppb.New(e.CreatedAt),
			UpdatedAt:   timestamppb.New(e.CreatedAt),
		}
	}

	return &MemoryRecallResponse{
		Entries: pbEntries,
		Total:   int32(len(pbEntries)),
	}, nil
}

// MemoryList lists all memory entries.
func (s *Server) MemoryList(ctx context.Context, _ *emptypb.Empty) (*MemoryListResponse, error) {
	entries := s.memoryStore.Recent(s.memoryStore.Count())

	pbEntries := make([]*MemoryEntry, len(entries))
	for i, e := range entries {
		pbEntries[i] = &MemoryEntry{
			Id:          e.ID,
			Content:     e.Content,
			Category:    e.Category,
			Tier:        e.Tier.String(),
			Importance:  e.Importance,
			AccessCount: int32(e.AccessCount),
			CreatedAt:   timestamppb.New(e.CreatedAt),
			UpdatedAt:   timestamppb.New(e.CreatedAt),
		}
	}

	return &MemoryListResponse{
		Entries: pbEntries,
		Count:   int32(len(pbEntries)),
	}, nil
}

// MemoryDelete deletes a memory entry.
func (s *Server) MemoryDelete(ctx context.Context, req *MemoryDeleteRequest) (*emptypb.Empty, error) {
	if err := s.memoryStore.Delete(req.Id); err != nil {
		return nil, status.Errorf(codes.NotFound, "memory entry not found: %s", req.Id)
	}
	return &emptypb.Empty{}, nil
}

// RAGIndex indexes content for RAG.
func (s *Server) RAGIndex(ctx context.Context, req *RAGIndexRequest) (*RAGIndexResponse, error) {
	doc, err := s.ragManager.IndexText(req.Source, "", req.Content)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to index content: %v", err)
	}

	stats := s.ragManager.Stats()
	return &RAGIndexResponse{
		Id:           doc.ID,
		Dimension:    1536, // OpenAI embedding dimension
		TotalEntries: int32(stats.ChunkCount),
	}, nil
}

// RAGSearch searches indexed content.
func (s *Server) RAGSearch(ctx context.Context, req *RAGSearchRequest) (*RAGSearchResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 10
	}

	threshold := req.Threshold
	if threshold <= 0 {
		threshold = 0.7
	}

	results, err := s.ragManager.Search(ctx, req.Query)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "search failed: %v", err)
	}

	pbResults := make([]*RAGResult, 0, len(results))
	for _, r := range results {
		if r.Score < threshold {
			continue
		}

		pbResults = append(pbResults, &RAGResult{
			Id:       r.ChunkID,
			Content:  r.Content,
			Source:   r.DocSource,
			Score:    r.Score,
			Metadata: r.Metadata,
		})

		if len(pbResults) >= limit {
			break
		}
	}

	return &RAGSearchResponse{
		Results: pbResults,
		Total:   int32(len(pbResults)),
	}, nil
}

// WorkflowCreate creates a new workflow.
func (s *Server) WorkflowCreate(ctx context.Context, req *WorkflowCreateRequest) (*Workflow, error) {
	tasks := make([]*workflow.Task, len(req.Tasks))
	for i, t := range req.Tasks {
		params := make(map[string]interface{})
		for k, v := range t.Params {
			params[k] = v
		}

		tasks[i] = &workflow.Task{
			ID:          t.Id,
			Name:        t.Name,
			Description: t.Description,
			Action:      t.Action,
			Params:      params,
			DependsOn:   t.DependsOn,
			Timeout:     time.Duration(t.TimeoutMs) * time.Millisecond,
			RetryCount:  int(t.RetryCount),
			RetryDelay:  time.Duration(t.RetryDelayMs) * time.Millisecond,
		}
	}

	wf := workflow.NewWorkflow(req.Name, tasks)
	wf.Description = req.Description
	wf.Version = req.Version

	if err := s.workflowEngine.RegisterWorkflow(wf); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid workflow: %v", err)
	}

	return s.workflowToProto(wf), nil
}

// WorkflowGet gets a workflow by ID.
func (s *Server) WorkflowGet(ctx context.Context, req *WorkflowGetRequest) (*Workflow, error) {
	wf, ok := s.workflowEngine.GetWorkflow(req.Id)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "workflow not found: %s", req.Id)
	}
	return s.workflowToProto(wf), nil
}

// WorkflowList lists all workflows.
func (s *Server) WorkflowList(ctx context.Context, _ *emptypb.Empty) (*WorkflowListResponse, error) {
	workflows := s.workflowEngine.ListWorkflows()

	pbWorkflows := make([]*Workflow, len(workflows))
	for i, wf := range workflows {
		pbWorkflows[i] = s.workflowToProto(wf)
	}

	return &WorkflowListResponse{
		Workflows: pbWorkflows,
		Count:     int32(len(pbWorkflows)),
	}, nil
}

// WorkflowDelete deletes a workflow.
func (s *Server) WorkflowDelete(ctx context.Context, req *WorkflowDeleteRequest) (*emptypb.Empty, error) {
	if err := s.workflowEngine.DeleteWorkflow(req.Id); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete workflow: %v", err)
	}
	return &emptypb.Empty{}, nil
}

// WorkflowStart starts a workflow instance.
func (s *Server) WorkflowStart(ctx context.Context, req *WorkflowStartRequest) (*WorkflowInstance, error) {
	instance, err := s.workflowEngine.StartWorkflow(req.WorkflowId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to start workflow: %v", err)
	}

	return s.instanceToProto(instance), nil
}

// WorkflowInstanceGet gets a workflow instance.
func (s *Server) WorkflowInstanceGet(ctx context.Context, req *WorkflowInstanceGetRequest) (*WorkflowInstance, error) {
	instance, ok := s.workflowEngine.GetInstance(req.Id)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "instance not found: %s", req.Id)
	}
	return s.instanceToProto(instance), nil
}

// HealthCheck returns server health status.
func (s *Server) HealthCheck(ctx context.Context, _ *emptypb.Empty) (*HealthCheckResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	components := make(map[string]*ComponentHealth)

	// Check agent
	if s.agent != nil {
		components["agent"] = &ComponentHealth{
			Status:  "healthy",
			Message: "running",
		}
	}

	// Check memory
	if s.memoryStore != nil {
		components["memory"] = &ComponentHealth{
			Status:  "healthy",
			Message: fmt.Sprintf("%d entries", s.memoryStore.Count()),
		}
	}

	// Check RAG
	if s.ragManager != nil {
		stats := s.ragManager.Stats()
		components["rag"] = &ComponentHealth{
			Status:  "healthy",
			Message: fmt.Sprintf("%d chunks, %d documents", stats.ChunkCount, stats.DocumentCount),
		}
	}

	// Check workflow
	if s.workflowEngine != nil {
		workflows := s.workflowEngine.ListWorkflows()
		components["workflow"] = &ComponentHealth{
			Status:  "healthy",
			Message: fmt.Sprintf("%d workflows", len(workflows)),
		}
	}

	return &HealthCheckResponse{
		Status:     "healthy",
		Version:    "v0.25.0",
		Uptime:     timestamppb.New(s.startAt),
		Components: components,
	}, nil
}

// Helper functions

func (s *Server) workflowToProto(wf *workflow.Workflow) *Workflow {
	tasks := make([]*Task, len(wf.Tasks))
	for i, t := range wf.Tasks {
		params := make(map[string]string)
		for k, v := range t.Params {
			params[k] = fmt.Sprintf("%v", v)
		}

		tasks[i] = &Task{
			Id:           t.ID,
			Name:         t.Name,
			Description:  t.Description,
			Action:       t.Action,
			Params:       params,
			DependsOn:    t.DependsOn,
			TimeoutMs:    int64(t.Timeout.Milliseconds()),
			RetryCount:   int32(t.RetryCount),
			RetryDelayMs: int64(t.RetryDelay.Milliseconds()),
		}
	}

	return &Workflow{
		Id:          wf.ID,
		Name:        wf.Name,
		Description: wf.Description,
		Tasks:       tasks,
		Version:     wf.Version,
		CreatedAt:   timestamppb.New(wf.CreatedAt),
		UpdatedAt:   timestamppb.New(wf.UpdatedAt),
	}
}

func (s *Server) instanceToProto(instance *workflow.WorkflowInstance) *WorkflowInstance {
	// Use Snapshot() for thread-safe access
	snap := instance.Snapshot()

	results := make(map[string]*TaskResult)
	for k, r := range snap.Results {
		results[k] = &TaskResult{
			TaskId:     r.TaskID,
			Status:     string(r.Status),
			Output:     fmt.Sprintf("%v", r.Output),
			Error:      r.Error,
			StartTime:  timestamppb.New(r.StartTime),
			EndTime:    timestamppb.New(r.EndTime),
			DurationMs: r.Duration.Milliseconds(),
		}
	}

	return &WorkflowInstance{
		Id:         snap.ID,
		WorkflowId: snap.WorkflowID,
		Status:     string(snap.Status),
		Results:    results,
		StartTime:  timestamppb.New(snap.StartTime),
		EndTime:    timestamppb.New(snap.EndTime),
	}
}

// GRPCServer wraps the gRPC server with additional functionality.
type GRPCServer struct {
	server   *grpc.Server
	health   *health.Server
	listener net.Listener
	addr     string
}

// NewGRPCServer creates a new gRPC server wrapper.
func NewGRPCServer(addr string, serviceServer *Server) *GRPCServer {
	server := grpc.NewServer()

	// Register service
	RegisterLuckyAgentServiceServer(server, serviceServer)

	// Register health service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("luckyagent", grpc_health_v1.HealthCheckResponse_SERVING)

	// Register reflection
	reflection.Register(server)

	return &GRPCServer{
		server: server,
		health: healthServer,
		addr:   addr,
	}
}

// Start starts the gRPC server.
func (s *GRPCServer) Start() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.addr, err)
	}
	s.listener = listener

	go func() {
		s.server.Serve(listener)
	}()

	return nil
}

// Stop stops the gRPC server gracefully.
func (s *GRPCServer) Stop() {
	s.health.SetServingStatus("luckyagent", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	s.server.GracefulStop()
}

// Addr returns the server address.
func (s *GRPCServer) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.addr
}
