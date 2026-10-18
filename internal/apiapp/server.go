package server

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"
	transport "github.com/yurika0211/luckyagent-api"
	"github.com/yurika0211/luckyagent-orchestrator/collab"
	taskstore "github.com/yurika0211/luckyagent-orchestrator/task"
	"github.com/yurika0211/luckyagent-tools/memory"
	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/logger"
	"github.com/yurika0211/luckyagent/internal/mcpclient"
	"github.com/yurika0211/luckyagent/internal/metrics"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/server/health"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/telemetry"
	"github.com/yurika0211/luckyagent/internal/tool"
	"github.com/yurika0211/luckyagent/internal/websocket"
	"github.com/yurika0211/luckyagent/internal/workflow"
)

var jsonAPI = jsoniter.ConfigCompatibleWithStandardLibrary

const (
	defaultSessionsLimit  = 50
	maxSessionsLimit      = 200
	defaultHistoryLimit   = 60
	maxHistoryLimit       = 500
	defaultToolTraceLimit = 100
	maxToolTraceLimit     = 200
)

// Server 是 LuckyAgent 的 HTTP API Server
type Server struct {
	mu sync.RWMutex
	// configUpdateMu serializes configuration read-modify-write operations so
	// two clients cannot overwrite each other's PATCH/PUT changes.
	configUpdateMu sync.Mutex
	server         *http.Server
	agent          *agent.Agent
	mcpAdmin       mcpclient.Admin
	config         ServerConfig
	running        bool
	// gatewayCtx is owned by the API server, rather than an individual HTTP
	// request.  Gateway adapters keep polling/listening after the start request
	// has returned and are stopped when the server is stopped.
	gatewayCtx    context.Context
	gatewayCancel context.CancelFunc
	// gatewayReleases holds process-wide channel leases acquired for API-
	// started gateways. Telegram uses a lease because Telegram only permits
	// one long-polling consumer for a bot token.
	gatewayReleases map[string]func()
	statsMu         sync.RWMutex
	chatCancelMu    sync.Mutex
	chatCancels     map[string]*chatCancellation

	// 限流
	rateLimiter *rateLimiter

	// 统计
	stats ServerStats

	// v0.17.0: 可观测性
	metrics     *metrics.Metrics
	healthCheck *health.HealthCheck

	// v0.18.0: WebSocket
	wsHub *websocket.Hub

	// v0.22.0: 多 Agent 协作
	collabRegistry  *collab.Registry
	delegateManager *collab.DelegateManager

	// v0.24.0: Workflow Engine
	workflowEngine *workflow.WorkflowEngine
	telemetryOn    bool
	telemetryStop  func(context.Context) error

	// QR pairing keys live only in this process. A restart drops them.
	pairing *pairingStore
}

// ServerStats 服务器统计
type ServerStats struct {
	TotalReqs   int64
	ChatReqs    int64
	ErrorReqs   int64
	StartTime   time.Time
	LastReqTime time.Time
}

type chatCancellation struct {
	cancel context.CancelFunc
}

// ChatRequest 聊天请求
type ChatRequest struct {
	Message        string               `json:"message"`
	SessionID      string               `json:"session_id,omitempty"`
	Stream         bool                 `json:"stream,omitempty"`
	MaxIter        int                  `json:"max_iterations,omitempty"`
	AutoApprove    bool                 `json:"auto_approve,omitempty"`
	Metadata       map[string]string    `json:"metadata,omitempty"`
	Attachments    []gateway.Attachment `json:"attachments,omitempty"`
	autoApproveSet bool
}

// UnmarshalJSON keeps the legacy bool field while remembering whether the
// caller explicitly supplied auto_approve. This lets the agent config remain
// effective when the request omits the optional override.
func (r *ChatRequest) UnmarshalJSON(data []byte) error {
	type alias ChatRequest
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*r = ChatRequest(decoded)
	_, r.autoApproveSet = fields["auto_approve"]
	return nil
}

// ChatResponse 聊天响应
type ChatResponse struct {
	Response   string         `json:"response"`
	SessionID  string         `json:"session_id"`
	Iterations int            `json:"iterations"`
	TokensUsed int            `json:"tokens_used"`
	ToolCalls  []toolCallInfo `json:"tool_calls,omitempty"`
	Duration   string         `json:"duration"`
}

type toolCallInfo struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
	Duration  string `json:"duration"`
}

// MemoryEntry 记忆条目
type MemoryEntry struct {
	ID          string  `json:"id"`
	Content     string  `json:"content"`
	Category    string  `json:"category"`
	Tier        string  `json:"tier"`
	Importance  float64 `json:"importance"`
	AccessCount int     `json:"access_count"`
}

type routeEntry struct {
	path    string
	handler http.HandlerFunc
}

// New 创建 API Server
func New(a *agent.Agent, cfg ServerConfig) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":9090"
	}
	if cfg.RateLimit <= 0 {
		cfg.RateLimit = 60
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.LogFormat == "" {
		cfg.LogFormat = "text"
	}

	m := metrics.NewMetrics()
	hc := health.NewHealthCheck("v0.24.0")

	// v0.18.0: WebSocket Hub
	wsHandler := websocket.NewAgentHandler(a)
	wsHub := websocket.NewHub(wsHandler, websocket.DefaultHubConfig())
	wsHandler.SetEventSink(wsHub.SendToSession)
	if a != nil {
		if store, ok := a.TaskStore().(*taskstore.FileStore); ok {
			store.Observe(func(event taskstore.Event) {
				broadcastTaskEvent(wsHub, event)
			})
		}
		a.SetSessionEventSink(func(sessionID, content string) {
			msg, err := websocket.NewMessage(websocket.TypeStreamEnd, sessionID, websocket.StreamEndData{
				FullResponse: content,
				Iterations:   1,
			})
			if err != nil {
				return
			}
			wsHub.SendToSession(sessionID, msg)
		})
	}
	wsHandler.Start()
	go wsHub.Run()

	// v0.22.0: 多 Agent 协作
	collabRegistry := collab.NewRegistry()
	delegateManager := collab.NewDelegateManager(collabRegistry, collab.TaskHandlerFunc(func(ctx context.Context, task *collab.SubTask) (string, error) {
		// Keep one durable transcript per delegated sub-agent. The execution
		// session IDs are assigned by DelegateManager before the goroutine starts,
		// so the task detail page can open the same conversation while it runs.
		if a == nil {
			return "", fmt.Errorf("agent runtime is not initialized")
		}
		sessionID := strings.TrimSpace(task.ExecutionSessionID)
		if sessionID == "" {
			return a.Chat(ctx, task.Input)
		}
		if _, ok := a.Sessions().Get(sessionID); !ok {
			a.Sessions().Ensure(sessionID)
		}
		if sess, ok := a.Sessions().Get(sessionID); ok {
			role := "subagent"
			if strings.TrimSpace(task.AgentID) != "" {
				role += ":" + strings.TrimSpace(task.AgentID)
			}
			profile := "You are a delegated collaboration sub-agent in LuckyAgent. Follow the assigned role, keep evidence for your result, and report blockers or unfinished work clearly."
			sess.SetAgentProfile(role, profile)
		}
		return a.ChatWithSession(ctx, sessionID, task.Input)
	}))
	if a != nil && a.TaskStore() != nil {
		delegateManager.SetTaskStore(a.TaskStore())
	}
	if a != nil && a.Config() != nil {
		_ = delegateManager.SetMDPSnapshotPath(filepath.Join(a.Config().HomeDir(), "planner", "mdp.json"))
	}

	// v0.24.0: Workflow Engine
	workflowExecutor := workflow.NewDefaultExecutor()
	workflowEngine := workflow.NewWorkflowEngine(workflowExecutor, 10)

	telemetryOn, telemetryStop := setupTelemetryFromEnv()

	return &Server{
		agent:       a,
		mcpAdmin:    a.MCPAdmin(),
		config:      cfg,
		rateLimiter: newRateLimiter(cfg.RateLimit),
		stats: ServerStats{
			StartTime: time.Now(),
		},
		chatCancels:     make(map[string]*chatCancellation),
		gatewayReleases: make(map[string]func()),
		metrics:         m,
		healthCheck:     hc,
		wsHub:           wsHub,
		collabRegistry:  collabRegistry,
		delegateManager: delegateManager,
		workflowEngine:  workflowEngine,
		telemetryOn:     telemetryOn,
		telemetryStop:   telemetryStop,
		pairing:         newPairingStore(),
	}
}

func broadcastTaskEvent(hub *websocket.Hub, event taskstore.Event) {
	if hub == nil || event.TaskID == "" {
		return
	}
	sessionID := ""
	description := ""
	if event.Metadata != nil {
		sessionID = strings.TrimSpace(event.Metadata["session_id"])
		if sessionID == "" {
			sessionID = strings.TrimSpace(event.Metadata["owner_session_id"])
		}
		description = strings.TrimSpace(event.Metadata["description"])
		if description == "" {
			description = strings.TrimSpace(event.Metadata["title"])
		}
	}
	msg, err := websocket.NewMessage(websocket.TypeTaskEvent, sessionID, websocket.TaskEventData{
		Type:               string(event.Type),
		TaskID:             event.TaskID,
		ParentID:           event.ParentID,
		ExecutionSessionID: event.ExecutionSessionID,
		ParentSessionID:    event.ParentSessionID,
		Role:               event.Role,
		Status:             string(event.Status),
		Mode:               string(event.Mode),
		Message:            event.Message,
		Progress:           event.Progress,
		ChildID:            event.ChildID,
		Error:              event.Error,
		SessionID:          sessionID,
		Description:        description,
		Metadata:           event.Metadata,
	})
	if err != nil {
		return
	}
	// Task progress is not bound to one chat socket. Every connected app
	// should see it, then decide whether to notify.
	msg.SessionID = ""
	hub.Broadcast(msg)
}

func setupTelemetryFromEnv() (bool, func(context.Context) error) {
	enabled := strings.EqualFold(strings.TrimSpace(os.Getenv("LH_TELEMETRY_ENABLED")), "true")
	if !enabled {
		return false, nil
	}

	cfg := telemetry.DefaultConfig()
	if v := strings.TrimSpace(os.Getenv("LH_TELEMETRY_EXPORTER")); v != "" {
		cfg.ExporterType = v
	}
	if v := strings.TrimSpace(os.Getenv("LH_TELEMETRY_OTLP_ENDPOINT")); v != "" {
		cfg.OTLPEndpoint = v
	}
	if v := strings.TrimSpace(os.Getenv("LH_TELEMETRY_SAMPLE_RATE")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.SampleRate = f
		}
	}

	stop, err := telemetry.Setup(context.Background(), cfg)
	if err != nil {
		logger.Warn("telemetry setup failed", "error", err)
		return false, nil
	}
	logger.Info("telemetry enabled", "exporter", cfg.ExporterType, "sample_rate", cfg.SampleRate)
	return true, stop
}

// Start 启动 API Server
func (s *Server) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}
	// Gateway adapters are long-lived server resources. Never pass an HTTP
	// request context to them: net/http cancels that context when the handler
	// returns, which would stop a gateway immediately after a successful start.
	s.gatewayCtx, s.gatewayCancel = context.WithCancel(context.Background())

	mux := http.NewServeMux()

	// API v1 路由（声明式注册）
	transport.RegisterRoutes(mux, transport.Handlers{
		HealthLiveness:       s.handleHealthLiveness,
		HealthReadiness:      s.handleHealthReadiness,
		HealthDetail:         s.handleHealthDetail,
		Metrics:              s.handleMetrics,
		ConfigReload:         s.handleConfigReload,
		ConfigSchema:         s.handleConfigSchema,
		Config:               s.handleConfig,
		Models:               s.handleModels,
		ModelEffort:          s.handleModelEffort,
		ModelSwitch:          s.handleModelSwitch,
		ModelProfiles:        s.handleModelProfiles,
		Approvals:            s.handleApprovals,
		Chat:                 s.handleChat,
		ChatSync:             s.handleChatSync,
		Sessions:             s.handleSessions,
		SessionByID:          s.handleSessionByID,
		Uploads:              s.handleUploads,
		Artifact:             s.handleArtifact,
		Commands:             s.handleCommands,
		Cron:                 s.handleCron,
		Tasks:                s.handleTasks,
		TaskByID:             s.handleTaskByID,
		AutonomyDashboard:    s.handleAutonomyDashboard,
		AutonomyTaskByID:     s.handleAutonomyTaskByID,
		Memory:               s.handleMemory,
		MemoryRecall:         s.handleMemoryRecall,
		MemoryRecallTrace:    s.handleMemoryRecallTrace,
		MemoryStats:          s.handleMemoryStats,
		MemoryGraph:          s.handleMemoryGraph,
		ProactiveStatus:      s.handleProactiveStatus,
		Tools:                s.handleTools,
		Skills:               s.handleSkills,
		SkillRoutes:          s.handleSkillRoutes,
		Stats:                s.handleStats,
		Soul:                 s.handleSoul,
		Context:              s.handleContext,
		ContextFit:           s.handleContextFit,
		RAGIndex:             s.handleRAGIndex,
		RAGSearch:            s.handleRAGSearch,
		RAGStats:             s.handleRAGStats,
		RAGStore:             s.handleRAGStore,
		RAGStreamWatch:       s.handleRAGStreamWatch,
		RAGStreamScan:        s.handleRAGStreamScan,
		RAGStreamStart:       s.handleRAGStreamStart,
		RAGStreamStop:        s.handleRAGStreamStop,
		RAGStreamStatus:      s.handleRAGStreamStatus,
		RAGStreamIndex:       s.handleRAGStreamIndex,
		RAGStreamQueue:       s.handleRAGStreamQueue,
		RAGStreamProcess:     s.handleRAGStreamProcess,
		FunctionCalling:      s.handleFunctionCalling,
		FCTools:              s.handleFCTools,
		FCHistory:            s.handleFCHistory,
		WebSocket:            s.handleWebSocket,
		WSStats:              s.handleWSStats,
		SoulTemplates:        s.handleSoulTemplates,
		SoulTemplateByID:     s.handleSoulTemplateByID,
		EmbedderList:         s.handleEmbedderList,
		EmbedderRegister:     s.handleEmbedderRegister,
		EmbedderSwitch:       s.handleEmbedderSwitch,
		EmbedderRoutes:       s.handleEmbedderRoutes,
		AgentsList:           s.handleAgentsList,
		AgentsRegister:       s.handleAgentsRegister,
		AgentsDeregister:     s.handleAgentsDeregister,
		AgentsDelegate:       s.handleAgentsDelegate,
		AgentsTask:           s.handleAgentsTask,
		AgentsTasks:          s.handleAgentsTasks,
		AgentsCancel:         s.handleAgentsCancel,
		AgentsBlackboard:     s.handleAgentsBlackboard,
		AgentsMessages:       s.handleAgentsMessages,
		Workflows:            s.handleWorkflows,
		WorkflowByID:         s.handleWorkflowByID,
		WorkflowInstances:    s.handleWorkflowInstances,
		WorkflowInstanceByID: s.handleWorkflowInstanceByID,
		GatewaysList:         s.handleGatewaysList,
		GatewayTelegramStart: s.handleGatewayTelegramStart,
		GatewayByName:        s.handleGatewayByName,
		Root:                 s.handleRoot,
	})
	mux.HandleFunc("/api/v1/mcp/servers", s.handleMCPServers)

	var handler http.Handler = mux

	// 中间件链
	handler = s.recoveryMiddleware(handler)
	handler = s.rateLimitMiddleware(handler)
	handler = s.authMiddleware(handler)
	if s.config.EnableCORS {
		handler = s.corsMiddleware(handler)
	}
	if s.telemetryOn {
		handler = telemetry.HTTPMiddleware(handler)
	}
	// 放在最外层，覆盖鉴权/限流/CORS 失败等所有请求路径。
	handler = s.loggingMiddleware(handler)

	ln, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		if s.gatewayCancel != nil {
			s.gatewayCancel()
		}
		s.gatewayCtx = nil
		s.gatewayCancel = nil
		s.mu.Unlock()
		return fmt.Errorf("listen %s: %w", s.config.Addr, err)
	}
	boundAddr := ln.Addr().String()

	s.server = transport.NewHTTPServer(boundAddr, handler)
	s.config.Addr = boundAddr
	s.running = true
	s.mu.Unlock()

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("api server crashed", "error", err)
			fmt.Printf("API server error: %v\n", err)
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}
	}()

	displayAddr := publicServerURL(boundAddr)
	logger.Info("api server started",
		"addr", boundAddr,
		"cors_enabled", s.config.EnableCORS,
		"api_keys", len(s.config.APIKeys),
		"rate_limit", s.config.RateLimit,
	)
	fmt.Printf("🚀 LuckyAgent API Server running at %s\n", displayAddr)
	fmt.Printf("   API: /api/v1/chat | /api/v1/health/live | /api/v1/stats\n")
	return nil
}

// Stop 停止 API Server
func (s *Server) Stop() error {
	s.mu.Lock()
	running := s.running
	httpServer := s.server
	gatewayCancel := s.gatewayCancel
	s.gatewayCancel = nil
	s.gatewayCtx = nil
	s.mu.Unlock()

	if gatewayCancel != nil {
		gatewayCancel()
	}
	var gatewayErr error
	if s.agent != nil && s.agent.MsgGateway() != nil {
		gatewayErr = s.agent.MsgGateway().StopAll()
	}
	s.releaseAllGatewayLeases()

	// These components are started by New, so they must also be stopped when
	// Stop is called before Start (or after Serve exits unexpectedly).
	if s.rateLimiter != nil {
		s.rateLimiter.Stop()
	}
	if s.wsHub != nil {
		s.wsHub.Stop()
	}
	if !running || httpServer == nil {
		return gatewayErr
	}

	logger.Info("api server stopping", "addr", s.config.Addr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}
	if s.telemetryStop != nil {
		if err := s.telemetryStop(ctx); err != nil {
			logger.Warn("telemetry shutdown failed", "error", err)
		}
	}

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	logger.Info("api server stopped", "addr", s.config.Addr)
	return gatewayErr
}

// IsRunning 返回是否运行中
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Stats 返回服务器统计
func (s *Server) Stats() ServerStats {
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()
	return s.stats
}

// Metrics 返回指标收集器
func (s *Server) Metrics() *metrics.Metrics {
	return s.metrics
}

// HealthCheck 返回健康检查器
func (s *Server) HealthCheck() *health.HealthCheck {
	return s.healthCheck
}

// ===== 路由处理 =====

// handleHealthLiveness 存活检查
func (s *Server) handleHealthLiveness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	report := s.healthCheck.Liveness()
	data, err := report.ToJSON()
	if err != nil {
		s.sendError(w, "internal error", http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleHealthReadiness 就绪检查
func (s *Server) handleHealthReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	report := s.healthCheck.Readiness()
	statusCode := http.StatusOK
	if report.Status == health.StatusUnhealthy {
		statusCode = http.StatusServiceUnavailable
	} else if report.Status == health.StatusDegraded {
		statusCode = http.StatusOK // degraded 仍然可用
	}
	data, err := report.ToJSON()
	if err != nil {
		s.sendError(w, "internal error", http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	w.Write(data)
}

// handleHealthDetail 详细健康检查
func (s *Server) handleHealthDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	report := s.healthCheck.Detail()
	data, err := report.ToJSON()
	if err != nil {
		s.sendError(w, "internal error", http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleMetrics Prometheus 格式指标
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(s.metrics.ExportPrometheus()))
}

// handleChat 流式聊天 (SSE)
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	s.statsMu.Lock()
	s.stats.ChatReqs++
	s.stats.TotalReqs++
	s.stats.LastReqTime = time.Now()
	s.statsMu.Unlock()

	// v0.17.0: 记录 metrics
	s.metrics.RecordChatRequest()

	var req ChatRequest
	if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
		return
	}

	if req.Message == "" && len(req.Attachments) == 0 {
		s.sendError(w, "message is required", http.StatusBadRequest, "")
		return
	}

	start := time.Now()

	loopCfg := agent.DefaultLoopConfig()
	if s.agent != nil && s.agent.Config() != nil {
		cfg := s.agent.Config().Get()
		agent.ApplyAgentLoopConfig(&loopCfg, cfg.Agent)
	}
	if req.MaxIter > 0 {
		loopCfg.MaxIterations = req.MaxIter
	}
	if req.autoApproveSet {
		loopCfg.AutoApprove = req.AutoApprove
	}
	loopCfg.Source = "http"
	parentCtx := r.Context()

	// SSE 流式响应
	flusher, ok := w.(http.Flusher)
	if !ok {
		// 不支持 SSE，降级为同步
		s.doChatSync(w, r, req, loopCfg, parentCtx, start)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	// SSE has its own rolling write deadline; the server's fixed 120s
	// response deadline must not terminate an otherwise healthy long turn.
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Time{})

	sessionID := req.SessionID
	if sessionID == "" {
		if sess := s.agent.Sessions().NewWithTitle("chat-stream"); sess != nil {
			sessionID = sess.ID
		}
	}
	ctx, cancel := s.registerChatCancellation(parentCtx, sessionID)
	defer cancel()
	turn := agent.MultimodalUserTurnInput(req.Message, req.Attachments)
	events, err := s.agent.ChatWithSessionStreamInputWithLoopConfig(ctx, sessionID, turn, loopCfg)
	if err != nil {
		s.sendSSEError(w, flusher, err.Error())
		return
	}

	// Keep proxies alive and notice a disconnected client even while no model
	// event is arriving. A failed write cancels the turn and drains its channel.
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	writeFrame := func(frame string) error {
		_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := fmt.Fprint(w, frame); err != nil {
			return err
		}
		if err := controller.Flush(); err != nil {
			return err
		}
		_ = controller.SetWriteDeadline(time.Time{})
		return nil
	}
	defer func() {
		cancel()
		go func() {
			for range events {
			}
		}()
	}()
chatEvents:
	for {
		var event agent.ChatEvent
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if err := writeFrame(": keepalive\n\n"); err != nil {
				return
			}
			continue
		case next, ok := <-events:
			if !ok {
				break chatEvents
			}
			event = next
		}
		frame := map[string]interface{}{
			"type":        chatEventTypeString(event.Type),
			"content":     event.Content,
			"session_id":  sessionID,
			"name":        event.Name,
			"args":        event.Args,
			"result":      event.Result,
			"observation": event.Observation,
			"approval":    event.Approval,
			"error":       event.Err,
		}
		if event.Compact != nil {
			frame["compact"] = event.Compact
		}
		data, _ := jsonAPI.Marshal(frame)

		if err := writeFrame(fmt.Sprintf("data: %s\n\n", data)); err != nil {
			return
		}

		if event.Type == agent.ChatEventDone || event.Type == agent.ChatEventError {
			break chatEvents
		}
	}

	duration := time.Since(start)
	summary, _ := jsonAPI.Marshal(map[string]interface{}{
		"type":     "complete",
		"duration": duration.String(),
	})
	_ = writeFrame(fmt.Sprintf("data: %s\n\n", summary))
}

// handleChatSync 同步聊天
func (s *Server) handleChatSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	s.statsMu.Lock()
	s.stats.ChatReqs++
	s.stats.TotalReqs++
	s.stats.LastReqTime = time.Now()
	s.statsMu.Unlock()

	// v0.17.0: 记录 metrics
	s.metrics.RecordChatRequest()

	var req ChatRequest
	if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
		return
	}

	if req.Message == "" && len(req.Attachments) == 0 {
		s.sendError(w, "message is required", http.StatusBadRequest, "")
		return
	}

	start := time.Now()

	loopCfg := agent.DefaultLoopConfig()
	if s.agent != nil && s.agent.Config() != nil {
		cfg := s.agent.Config().Get()
		agent.ApplyAgentLoopConfig(&loopCfg, cfg.Agent)
	}
	if req.MaxIter > 0 {
		loopCfg.MaxIterations = req.MaxIter
	}
	if req.autoApproveSet {
		loopCfg.AutoApprove = req.AutoApprove
	}
	loopCfg.Source = "http"

	ctx := r.Context()
	s.doChatSync(w, r, req, loopCfg, ctx, start)
}

func (s *Server) doChatSync(w http.ResponseWriter, r *http.Request, req ChatRequest, loopCfg agent.LoopConfig, ctx context.Context, start time.Time) {
	turn := agent.MultimodalUserTurnInput(req.Message, req.Attachments)
	// v0.56.0: 检测内置命令
	if strings.HasPrefix(req.Message, "/") {
		parts := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(req.Message, "/")), " ", 2)
		cmd := strings.ToLower(strings.TrimSpace(parts[0]))
		args := ""
		if len(parts) > 1 {
			args = strings.TrimSpace(parts[1])
		}

		// 简单命令处理（不依赖 gateway handler）
		switch cmd {
		case "help":
			s.sendJSON(w, http.StatusOK, ChatResponse{
				Response: `🐾 *LuckyAgent 命令*

/new — 开启新对话
/stop — 停止当前任务
/status — 查看状态
/restart — 重启 bot
/help — 显示帮助

/model — 查看模型
/soul — SOUL 信息
/tools — 工具列表
/skills — 技能列表
/cron — 定时任务
/metrics — 使用指标
/health — 健康检查
/reset — 重置对话
/history — 对话历史
/session — 会话信息

💡 私聊直接发消息即可`,
			})
			return
		case "new":
			// 创建新会话
			sess := s.agent.Sessions().New()
			s.sendJSON(w, http.StatusOK, ChatResponse{
				Response:  fmt.Sprintf("✅ New session started.\n新会话 ID: `%s`", sess.ID),
				SessionID: sess.ID,
			})
			return
		case "status":
			cfg := s.agent.Config().Get()
			uptime := time.Since(s.agent.Metrics().StartTime)
			s.sendJSON(w, http.StatusOK, ChatResponse{
				Response: fmt.Sprintf("📊 *LuckyAgent Status*\n\n• Version: v0.55.0\n• Model: %s\n• Uptime: %s\n• Total requests: %d",
					cfg.Model, formatDuration(uptime), s.agent.Metrics().TotalRequests.Load()),
			})
			return
		case "stop":
			if strings.TrimSpace(req.SessionID) == "" || !s.cancelChat(strings.TrimSpace(req.SessionID)) {
				s.sendError(w, "no active chat for session", http.StatusNotFound, "provide the session_id of a running chat")
				return
			}
			s.sendJSON(w, http.StatusOK, ChatResponse{
				Response:  "✅ Stop requested for the active chat.",
				SessionID: strings.TrimSpace(req.SessionID),
			})
			return
		case "restart":
			s.sendJSON(w, http.StatusOK, ChatResponse{
				Response: "🔄 /restart 在 HTTP Chat API 中不可用。\n\n" +
					"• 消息网关（Telegram/QQ）：请在对应聊天里发送 /restart（重连网关，不重启进程）\n" +
					"• 完整进程重启：请手动重启 lh serve / msg-gateway / systemd 服务\n" +
					"• 若只改了可热更新配置，通常无需重启",
			})
			return
		default:
			// Keep the synchronous chat endpoint and /api/v1/commands on the
			// same command router. This preserves the complete argument tail,
			// including spaces, for commands such as `/models refresh` and
			// `/rag search foo bar`.
			output, err := s.runCommand(cmd, args, strings.TrimSpace(req.SessionID))
			if err != nil {
				s.sendJSON(w, http.StatusOK, ChatResponse{Response: fmt.Sprintf("%s\nType /help for available commands.", err)})
				return
			}
			s.sendJSON(w, http.StatusOK, ChatResponse{Response: output, SessionID: strings.TrimSpace(req.SessionID)})
			return
		}
	}

	// v0.24.1: 如果没有提供 session_id，创建新会话
	sessionID := req.SessionID
	var sess *session.Session
	if sessionID == "" {
		sess = s.agent.Sessions().NewWithTitle("chat")
		if sess != nil {
			sessionID = sess.ID
		}
	} else {
		// 获取现有会话
		s, ok := s.agent.Sessions().Get(sessionID)
		if ok {
			sess = s
		}
	}
	ctx, cancel := s.registerChatCancellation(ctx, sessionID)
	defer cancel()

	// 使用 RunLoopWithSession 确保消息被保存；无法创建/获取会话时降级为无会话 RunLoop
	var (
		result *agent.LoopResult
		err    error
	)
	if sess != nil {
		loopCfgWithSession := loopCfg
		result, err = s.agent.RunLoopWithSessionInput(ctx, sess, turn, loopCfgWithSession)
	} else {
		result, err = s.agent.RunLoopWithSessionInput(ctx, nil, turn, loopCfg)
	}
	if err != nil {
		s.statsMu.Lock()
		s.stats.ErrorReqs++
		s.statsMu.Unlock()
		s.sendError(w, "chat failed", http.StatusInternalServerError, err.Error())
		return
	}

	duration := time.Since(start)

	resp := ChatResponse{
		Response:   result.Response,
		SessionID:  sessionID,
		Iterations: result.Iterations,
		TokensUsed: result.TokensUsed,
		Duration:   duration.String(),
	}

	for _, tc := range result.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, toolCallInfo{
			Name:      tc.Name,
			Arguments: tc.Arguments,
			Result:    tc.Result,
			Duration:  tc.Duration.String(),
		})
	}

	s.sendJSON(w, http.StatusOK, resp)
}

func (s *Server) registerChatCancellation(parent context.Context, sessionID string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if strings.TrimSpace(sessionID) == "" {
		return ctx, cancel
	}
	entry := &chatCancellation{cancel: cancel}
	s.chatCancelMu.Lock()
	if s.chatCancels == nil {
		s.chatCancels = make(map[string]*chatCancellation)
	}
	previous := s.chatCancels[sessionID]
	s.chatCancels[sessionID] = entry
	s.chatCancelMu.Unlock()
	if previous != nil {
		previous.cancel()
	}

	return ctx, func() {
		cancel()
		s.chatCancelMu.Lock()
		if s.chatCancels[sessionID] == entry {
			delete(s.chatCancels, sessionID)
		}
		s.chatCancelMu.Unlock()
	}
}

func (s *Server) cancelChat(sessionID string) bool {
	s.chatCancelMu.Lock()
	entry := s.chatCancels[sessionID]
	s.chatCancelMu.Unlock()
	if entry == nil {
		return false
	}
	entry.cancel()
	return true
}

func boundedQueryInt(r *http.Request, name string, fallback, minValue, maxValue int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// handleSessions 会话列表
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		limit := boundedQueryInt(r, "limit", defaultSessionsLimit, 1, maxSessionsLimit)
		offset := boundedQueryInt(r, "offset", 0, 0, 1_000_000)
		var sessions []session.SessionInfo
		if query != "" {
			sessions = s.agent.Sessions().Search(query)
		} else {
			sessions = s.agent.Sessions().ListInfo()
		}
		type sessionInfo struct {
			ID           string `json:"id"`
			Title        string `json:"title"`
			AgentRole    string `json:"agent_role,omitempty"`
			Pinned       bool   `json:"pinned,omitempty"`
			Project      string `json:"project,omitempty"`
			MessageCount int    `json:"message_count"`
			ByteSize     int64  `json:"byte_size,omitempty"`
			Format       string `json:"format,omitempty"`
			CreatedAt    string `json:"created_at"`
			UpdatedAt    string `json:"updated_at"`
		}

		total := len(sessions)
		start := minInt(offset, total)
		end := minInt(start+limit, total)
		page := sessions[start:end]
		infos := make([]sessionInfo, 0, len(page))
		for _, sess := range page {
			infos = append(infos, sessionInfo{
				ID:           sess.ID,
				Title:        sess.Title,
				AgentRole:    sess.AgentRole,
				Pinned:       sess.Pinned,
				Project:      sess.Project,
				MessageCount: sess.MessageCount,
				ByteSize:     sess.ByteSize,
				Format:       sess.Format,
				CreatedAt:    sess.CreatedAt.Format(time.RFC3339),
				UpdatedAt:    sess.UpdatedAt.Format(time.RFC3339),
			})
		}

		s.sendJSON(w, http.StatusOK, map[string]interface{}{
			"sessions": infos,
			"count":    len(infos),
			"total":    total,
			"limit":    limit,
			"offset":   start,
			"has_more": end < total,
		})
	case http.MethodPost:
		var body struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		if err := jsonAPI.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
			return
		}

		sess := s.agent.Sessions().Ensure(strings.TrimSpace(body.ID))
		if sess == nil {
			s.sendError(w, "create session failed", http.StatusInternalServerError, "")
			return
		}
		if strings.TrimSpace(body.Title) != "" {
			sess.SetTitle(body.Title)
			_ = sess.Save()
		}
		role, _ := sess.AgentProfile()

		s.sendJSON(w, http.StatusCreated, map[string]interface{}{
			"id":            sess.ID,
			"title":         sess.Title,
			"agent_role":    role,
			"message_count": sess.MessageCount(),
			"created_at":    sess.CreatedAt.Format(time.RFC3339),
			"updated_at":    sess.UpdatedAt.Format(time.RFC3339),
		})
	default:
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
	}
}

type compactSessionAPIRequest struct {
	DryRun     bool `json:"dry_run,omitempty"`
	ForceLocal bool `json:"force_local,omitempty"`
}

// handleSessionByID returns the full history for a single session or dispatches
// session-scoped subresources.
func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/"), "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		s.sendError(w, "session id is required", http.StatusBadRequest, "")
		return
	}
	id := strings.TrimSpace(parts[0])

	sess, ok := s.agent.Sessions().Get(id)
	if !ok {
		s.sendError(w, "session not found", http.StatusNotFound, id)
		return
	}

	if len(parts) > 1 {
		s.handleSessionSubresource(w, r, sess, parts[1:])
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.patchSession(w, r, sess)
		return
	case http.MethodDelete:
		if err := s.agent.Sessions().Delete(sess.ID); err != nil {
			s.sendError(w, "delete session failed", http.StatusInternalServerError, err.Error())
			return
		}
		s.agent.ForgetShortTermSession(sess.ID)
		s.sendJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
		return
	case http.MethodGet:
	default:
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	hasLimit := r.URL.Query().Has("limit")
	var rawMessages []provider.Message
	var total int
	var hasMore bool
	var limit, offset int
	if hasLimit {
		limit = boundedQueryInt(r, "limit", defaultHistoryLimit, 1, maxHistoryLimit)
		offset = boundedQueryInt(r, "offset", 0, 0, 1_000_000)
		var err error
		rawMessages, total, hasMore, err = sess.GetMessagesPage(limit, offset)
		if err != nil {
			s.sendError(w, "load session history failed", http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		rawMessages = sess.GetMessages()
		total = len(rawMessages)
	}
	includeFull := false
	if v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("include"))); v == "full" || strings.Contains(v, "full") {
		includeFull = true
	}
	if v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("full"))); v == "1" || v == "true" || v == "yes" {
		includeFull = true
	}
	previewChars := 4000
	if s.agent != nil && s.agent.Config() != nil {
		if n := s.agent.Config().Get().Context.HistoryPreviewChars; n > 0 {
			previewChars = n
		}
	}
	messages := s.historyMessagesWithOptions(rawMessages, includeFull, previewChars)

	pinned, project := sess.Organization()
	role, systemPrompt := sess.AgentProfile()
	payload := map[string]interface{}{
		"id":                sess.ID,
		"title":             sess.Title,
		"agent_role":        role,
		"pinned":            pinned,
		"project":           project,
		"message_count":     total,
		"format":            sess.Format(),
		"byte_size":         sess.ByteSize(),
		"created_at":        sess.CreatedAt.Format(time.RFC3339),
		"updated_at":        sess.UpdatedAt.Format(time.RFC3339),
		"messages":          messages,
		"content_truncated": !includeFull,
	}
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("profile")), "1") || strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("profile")), "true") {
		payload["agent_system_prompt"] = systemPrompt
	}

	// Without a limit the whole history is returned, as before. With one, page
	// backwards from the newest message: offset counts messages skipped from
	// the end, so a client walks into older history by growing the offset.
	// Long sessions run to thousands of messages and megabytes of JSON, which
	// no interactive client wants in a single response.
	if hasLimit {
		payload["limit"] = limit
		payload["offset"] = offset
		payload["returned"] = len(messages)
		payload["has_more"] = hasMore
		payload["messages"] = messages
	}

	s.sendJSON(w, http.StatusOK, payload)
}

func (s *Server) patchSession(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var body struct {
		Title   *string `json:"title"`
		Pinned  *bool   `json:"pinned"`
		Project *string `json:"project"`
	}
	if err := jsonAPI.NewDecoder(r.Body).Decode(&body); err != nil {
		s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
		return
	}
	if body.Title == nil && body.Pinned == nil && body.Project == nil {
		s.sendError(w, "no session fields to update", http.StatusBadRequest, "")
		return
	}
	if body.Title != nil {
		title := strings.TrimSpace(*body.Title)
		if title == "" {
			s.sendError(w, "title is required", http.StatusBadRequest, "")
			return
		}
		sess.SetTitle(title)
	}
	if body.Pinned != nil {
		sess.SetPinned(*body.Pinned)
	}
	if body.Project != nil {
		sess.SetProject(*body.Project)
	}
	if err := sess.SaveMetadata(); err != nil {
		s.sendError(w, "save session failed", http.StatusInternalServerError, err.Error())
		return
	}
	pinned, project := sess.Organization()
	s.sendJSON(w, http.StatusOK, map[string]any{
		"id":            sess.ID,
		"title":         sess.Title,
		"pinned":        pinned,
		"project":       project,
		"message_count": sess.MessageCount(),
		"created_at":    sess.CreatedAt.Format(time.RFC3339),
		"updated_at":    sess.UpdatedAt.Format(time.RFC3339),
	})
}

func (s *Server) handleSessionSubresource(w http.ResponseWriter, r *http.Request, sess *session.Session, parts []string) {
	if len(parts) == 1 && parts[0] == "tools" {
		s.handleSessionToolTrace(w, r, sess)
		return
	}
	if len(parts) == 1 && parts[0] == "backups" {
		if r.Method != http.MethodGet {
			s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
			return
		}
		backups, err := sess.ListBackups()
		if err != nil {
			s.sendError(w, "list session backups failed", http.StatusInternalServerError, err.Error())
			return
		}
		s.sendJSON(w, http.StatusOK, map[string]interface{}{
			"session_id": sess.ID,
			"backups":    backups,
			"count":      len(backups),
		})
		return
	}
	if len(parts) == 3 && parts[0] == "backups" && parts[2] == "restore" {
		if r.Method != http.MethodPost {
			s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
			return
		}
		backup, err := sess.RestoreBackup(parts[1])
		if err != nil {
			s.sendError(w, "restore session backup failed", http.StatusBadRequest, err.Error())
			return
		}
		s.sendJSON(w, http.StatusOK, map[string]interface{}{
			"session_id":    sess.ID,
			"backup":        backup,
			"message_count": backup.MessageCount,
		})
		return
	}

	if len(parts) == 1 && parts[0] == "compact" {
		if r.Method != http.MethodPost {
			s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
			return
		}
		var req compactSessionAPIRequest
		if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
			return
		}
		result, err := s.agent.CompactSessionWithOptions(r.Context(), sess, "manual-api", agent.CompactSessionOptions{
			ForceLocal: req.ForceLocal,
			DryRun:     req.DryRun,
		})
		if err != nil {
			s.sendError(w, "compact session failed", http.StatusBadRequest, err.Error())
			return
		}
		s.sendJSON(w, http.StatusOK, compactSessionAPIResponse(result))
		return
	}

	if len(parts) == 2 && parts[0] == "compact" && parts[1] == "latest" {
		if r.Method != http.MethodGet {
			s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
			return
		}
		trace, ok := sess.LatestCompactTrace()
		if !ok {
			s.sendError(w, "compact trace not found", http.StatusNotFound, sess.ID)
			return
		}
		s.sendJSON(w, http.StatusOK, map[string]interface{}{
			"session_id": sess.ID,
			"trace":      trace,
		})
		return
	}

	s.sendError(w, "not found", http.StatusNotFound, "")
}

func compactSessionAPIResponse(result *agent.CompactSessionResult) map[string]interface{} {
	if result == nil {
		return map[string]interface{}{}
	}
	return map[string]interface{}{
		"boundary_id":          result.BoundaryID,
		"trigger":              result.Trigger,
		"summary":              result.Summary,
		"from_message":         result.FromMessage,
		"to_message":           result.ToMessage,
		"content_hash":         result.ContentHash,
		"policy_version":       result.PolicyVersion,
		"pre_token_estimate":   result.PreTokenEstimate,
		"post_token_estimate":  result.PostTokenEstimate,
		"summary_tokens":       result.SummaryTokens,
		"dropped_messages":     result.DroppedMessages,
		"retained_messages":    result.RetainedMessages,
		"restored_attachments": result.RestoredAttachments,
		"summary_source":       result.SummarySource,
		"dry_run":              result.DryRun,
		"backup":               result.Backup,
		"display":              agent.CompactDisplay(result),
	}
}

// handleMemory 记忆管理
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	s.dispatchMethod(w, r, map[string]func(){
		http.MethodGet: func() {
			// 列出所有记忆
			stats := s.agent.MemoryStats()
			s.sendJSON(w, http.StatusOK, map[string]interface{}{
				"stats": map[string]int{
					"short":  stats[memory.TierShort],
					"medium": stats[memory.TierMedium],
					"long":   stats[memory.TierLong],
				},
			})

		},
		http.MethodPost: func() {
			// 保存记忆
			var body struct {
				Content  string `json:"content"`
				Category string `json:"category"`
				LongTerm bool   `json:"long_term"`
			}
			if err := jsonAPI.NewDecoder(r.Body).Decode(&body); err != nil {
				s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
				return
			}

			if body.Content == "" {
				s.sendError(w, "content is required", http.StatusBadRequest, "")
				return
			}

			if body.Category == "" {
				body.Category = "user"
			}

			tier, importance := memory.TierMedium, .5
			if body.LongTerm {
				tier, importance = memory.TierLong, .9
			}
			_, err := s.agent.WriteMemory(r.Context(), memory.RememberRequest{
				Content: body.Content, Category: body.Category, Tier: tier, Importance: importance,
			})

			if err != nil {
				code := http.StatusInternalServerError
				var rejected *memory.WriteRejectedError
				if errors.As(err, &rejected) {
					code = http.StatusBadRequest
				}
				s.sendError(w, "save memory failed", code, err.Error())
				return
			}

			s.sendJSON(w, http.StatusOK, map[string]interface{}{
				"status":    "saved",
				"long_term": body.LongTerm,
			})
		},
	})
}

// handleMemoryRecall 记忆搜索
func (s *Server) handleMemoryRecall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		s.sendError(w, "query parameter 'q' is required", http.StatusBadRequest, "")
		return
	}

	results, err := s.agent.RecallMemory(r.Context(), memory.RecallRequest{Query: query, Options: memory.SearchOptions{IncludeGraph: true, GraphDepth: 1}})
	if err != nil {
		s.sendMemoryRecallError(w, err)
		return
	}
	var entries []MemoryEntry
	for _, result := range results {
		e := result.Entry
		entries = append(entries, MemoryEntry{
			ID:          e.ID,
			Content:     e.Content,
			Category:    e.Category,
			Tier:        e.Tier.String(),
			Importance:  e.Importance,
			AccessCount: e.AccessCount,
		})
	}

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"results": entries,
		"count":   len(entries),
	})
}

// handleMemoryStats 记忆统计
func (s *Server) handleMemoryStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	stats := s.agent.MemoryStats()
	payload := map[string]interface{}{
		"short_term":  stats[memory.TierShort],
		"medium_term": stats[memory.TierMedium],
		"long_term":   stats[memory.TierLong],
		"total":       stats[memory.TierShort] + stats[memory.TierMedium] + stats[memory.TierLong],
	}
	if tidalStats, enabled, err := s.agent.TidalMemoryStats(); err == nil {
		payload["tidal"] = map[string]interface{}{
			"enabled":         enabled,
			"query_events":    tidalStats.QueryEvents,
			"recall_events":   tidalStats.RecallEvents,
			"feedback_events": tidalStats.FeedbackEvents,
			"kernels":         tidalStats.Kernels,
		}
	} else {
		payload["tidal"] = map[string]interface{}{
			"enabled": false,
			"error":   err.Error(),
		}
	}
	s.sendJSON(w, http.StatusOK, payload)
}

func (s *Server) handleProactiveStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	if s.agent == nil {
		s.sendError(w, "agent unavailable", http.StatusServiceUnavailable, "")
		return
	}
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			s.sendError(w, "invalid limit", http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		if n > 100 {
			n = 100
		}
		limit = n
	}
	status, err := s.agent.ProactiveStatus(limit)
	if err != nil {
		s.sendError(w, "proactive status failed", http.StatusInternalServerError, err.Error())
		return
	}
	s.sendJSON(w, http.StatusOK, status)
}

// handleTools 工具列表
func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	tools := s.agent.Tools()
	allTools := tools.List()

	type toolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Category    string `json:"category"`
		Permission  string `json:"permission"`
		Enabled     bool   `json:"enabled"`
	}

	var infos []toolInfo
	for _, t := range allTools {
		infos = append(infos, toolInfo{
			Name:        t.Name,
			Description: t.Description,
			Category:    string(t.Category),
			Permission:  permString(t.Permission),
			Enabled:     t.Enabled,
		})
	}

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"tools": infos,
		"count": len(infos),
	})
}

// handleStats 服务器统计
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	stats := s.Stats()
	uptime := time.Since(stats.StartTime)
	contextCache := map[string]any{}
	if s.agent != nil {
		contextCache = s.agent.ContextCacheStats()
	}

	providerName := ""
	modelName := ""
	if s.agent != nil && s.agent.Config() != nil {
		cfg := s.agent.Config().Get()
		providerName = cfg.Provider
		modelName = cfg.Model
	}

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"provider":       providerName,
		"model":          modelName,
		"total_requests": stats.TotalReqs,
		"chat_requests":  stats.ChatReqs,
		"error_requests": stats.ErrorReqs,
		"uptime":         uptime.String(),
		"start_time":     stats.StartTime.Format(time.RFC3339),
		"last_request":   stats.LastReqTime.Format(time.RFC3339),
		"context_cache":  contextCache,
		"version":        "v0.21.0",
	})
}

// handleSoul SOUL 信息
func (s *Server) handleSoul(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	soul := s.agent.Soul()
	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"name":          soul.DisplayName(),
		"system_prompt": soul.SystemPrompt(),
	})
}

// handleRoot 根路由
// ===== v0.18.0: WebSocket 端点 =====

// handleWebSocket 处理 WebSocket 连接
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.wsHub == nil {
		http.Error(w, "WebSocket not available", http.StatusServiceUnavailable)
		return
	}
	s.wsHub.ServeHTTP(w, r)
}

// handleWSStats 返回 WebSocket 统计信息
func (s *Server) handleWSStats(w http.ResponseWriter, r *http.Request) {
	if s.wsHub == nil {
		s.sendJSON(w, http.StatusOK, map[string]interface{}{
			"enabled":        false,
			"active_conns":   0,
			"total_conns":    0,
			"total_messages": 0,
			"errors":         0,
		})
		return
	}

	stats := s.wsHub.GetStats()
	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"enabled":        true,
		"active_conns":   stats.ActiveConns,
		"total_conns":    stats.TotalConns,
		"total_messages": stats.TotalMessages,
		"errors":         stats.Errors,
		"sessions":       s.wsHub.SessionCount(),
		"clients":        s.wsHub.ClientCount(),
	})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"name":    "LuckyAgent API",
		"version": "v0.21.0",
		"endpoints": []string{
			"POST /api/v1/chat       — 流式聊天 (SSE)",
			"POST /api/v1/chat/sync  — 同步聊天",
			"GET|PUT|PATCH /api/v1/config — 读取或更新脱敏配置",
			"GET /api/v1/config/schema — 配置字段元数据",
			"POST /api/v1/config/reload — 从磁盘重载配置",
			"GET /api/v1/models — 列出模型。refresh=1 用当前 chat key 查询供应商",
			"GET|PUT /api/v1/models/effort — 读取或设置模型思维强度",
			"POST /api/v1/models/switch — 切换并保存指定类型模型",
			"GET  /api/v1/ws         — WebSocket 实时通信",
			"GET  /api/v1/ws/stats   — WebSocket 统计",
			"GET  /api/v1/sessions   — 会话列表",
			"GET  /api/v1/gateways   — 消息网关状态",
			"POST /api/v1/gateways/{name}/start|stop — 启停消息网关",
			"GET  /api/v1/gateways/{name}/status — 单个网关状态",
			"GET|POST /api/v1/approvals — 外部编码代理审批列表与回执",
			"GET  /api/v1/memory     — 记忆统计",
			"POST /api/v1/memory     — 保存记忆",
			"GET  /api/v1/memory/recall?q= — 搜索记忆",
			"GET  /api/v1/memory/stats    — 记忆统计",
			"GET  /api/v1/proactive/status — proactive 状态",
			"GET  /api/v1/autonomy/dashboard — 后台队列与 worker 状态",
			"GET  /api/v1/autonomy/tasks/{id} — 后台任务详情",
			"GET  /api/v1/cron       — 定时任务列表",
			"GET  /api/v1/tools      — 工具列表",
			"GET  /api/v1/stats      — 服务器统计",
			"GET  /api/v1/soul       — SOUL 信息",
			"GET  /api/v1/health/live — 存活检查",
			"GET  /api/v1/health/ready — 就绪检查",
			"GET  /api/v1/health/detail — 详细健康检查",
		},
	})
}

// ===== 中间件 =====

// ===== 辅助函数 =====

func (s *Server) sendJSON(w http.ResponseWriter, status int, data interface{}) {
	transport.SendJSON(w, status, data)
}

func (s *Server) sendError(w http.ResponseWriter, msg string, code int, details string) {
	transport.SendError(w, msg, code, details)
}

func (s *Server) registerRoutes(mux *http.ServeMux, routes []routeEntry) {
	for _, route := range routes {
		mux.HandleFunc(route.path, route.handler)
	}
}

func (s *Server) dispatchMethod(w http.ResponseWriter, r *http.Request, handlers map[string]func()) bool {
	return transport.DispatchMethod(w, r, handlers)
}

func (s *Server) sendSSEError(w io.Writer, flusher http.Flusher, msg string) {
	transport.SendSSEError(w, flusher, msg)
}

func chatEventTypeString(t agent.ChatEventType) string {
	switch t {
	case agent.ChatEventThinking:
		return "reason"
	case agent.ChatEventToolCall:
		return "act"
	case agent.ChatEventToolResult:
		return "observe"
	case agent.ChatEventContent:
		return "content"
	case agent.ChatEventDone:
		return "done"
	case agent.ChatEventError:
		return "error"
	case agent.ChatEventObservation:
		return "observation"
	case agent.ChatEventApprovalRequired:
		return "approval_required"
	case agent.ChatEventCompact:
		return "compact"
	default:
		return "unknown"
	}
}

func permString(p tool.PermissionLevel) string {
	switch p {
	case tool.PermAuto:
		return "auto"
	case tool.PermApprove:
		return "approve"
	case tool.PermDeny:
		return "deny"
	default:
		return "unknown"
	}
}

// ===== 限流器 =====

// Ensure Agent exposes Sessions
// We need to add Sessions() method to Agent
var _ provider.Provider = (*provider.OpenAIProvider)(nil)
