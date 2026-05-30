package server

import (
	"bufio"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/collab"
	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/logger"
	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/metrics"
	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/server/health"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/telemetry"
	"github.com/yurika0211/luckyharness/internal/tool"
	"github.com/yurika0211/luckyharness/internal/websocket"
	"github.com/yurika0211/luckyharness/internal/workflow"
)

var jsonAPI = jsoniter.ConfigCompatibleWithStandardLibrary

// Server 是 LuckyHarness 的 HTTP API Server
type Server struct {
	mu      sync.RWMutex
	server  *http.Server
	agent   *agent.Agent
	config  ServerConfig
	running bool

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
}

// ServerConfig API Server 配置
type ServerConfig struct {
	Addr        string   `yaml:"addr,omitempty"`         // 监听地址，默认 :9090
	APIKeys     []string `yaml:"api_keys,omitempty"`     // API Key 白名单（空=不鉴权）
	EnableCORS  bool     `yaml:"enable_cors,omitempty"`  // 启用 CORS，默认 true
	CORSOrigins []string `yaml:"cors_origins,omitempty"` // CORS 允许的源
	RateLimit   int      `yaml:"rate_limit,omitempty"`   // 每分钟请求限制，默认 60
	MetricsAddr string   `yaml:"metrics_addr,omitempty"` // Prometheus metrics 独立端口（空=复用主端口）
	LogLevel    string   `yaml:"log_level,omitempty"`    // 日志级别: debug, info, warn, error
	LogFormat   string   `yaml:"log_format,omitempty"`   // 日志格式: json, text
}

// DefaultServerConfig 返回默认配置
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Addr:        "127.0.0.1:9090", // 默认仅本地访问
		EnableCORS:  false,            // 默认关闭 CORS
		CORSOrigins: []string{},       // 空白名单 = 不允许跨域
		RateLimit:   60,
		LogLevel:    "info",
		LogFormat:   "text",
	}
}

// ServerStats 服务器统计
type ServerStats struct {
	mu          sync.RWMutex
	TotalReqs   int64
	ChatReqs    int64
	ErrorReqs   int64
	StartTime   time.Time
	LastReqTime time.Time
}

// ChatRequest 聊天请求
type ChatRequest struct {
	Message     string                  `json:"message"`
	SessionID   string                  `json:"session_id,omitempty"`
	Stream      bool                    `json:"stream,omitempty"`
	MaxIter     int                     `json:"max_iterations,omitempty"`
	AutoApprove bool                    `json:"auto_approve,omitempty"`
	Metadata    map[string]string       `json:"metadata,omitempty"`
	Attachments []gateway.Attachment    `json:"attachments,omitempty"`
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

// ErrorResponse 错误响应
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	Details string `json:"details,omitempty"`
}

type routeEntry struct {
	path    string
	handler http.HandlerFunc
}

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	bytes      int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *responseRecorder) ReadFrom(src io.Reader) (int64, error) {
	rf, ok := r.ResponseWriter.(io.ReaderFrom)
	if !ok {
		return io.Copy(r, src)
	}
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	n, err := rf.ReadFrom(src)
	r.bytes += int(n)
	return n, err
}

func (r *responseRecorder) StatusCode() int {
	if r.statusCode == 0 {
		return http.StatusOK
	}
	return r.statusCode
}

func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return h.Hijack()
}

func (r *responseRecorder) Push(target string, opts *http.PushOptions) error {
	p, ok := r.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return p.Push(target, opts)
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
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
	go wsHub.Run()

	// v0.22.0: 多 Agent 协作
	collabRegistry := collab.NewRegistry()
	delegateManager := collab.NewDelegateManager(collabRegistry, collab.TaskHandlerFunc(func(ctx context.Context, task *collab.SubTask) (string, error) {
		// 默认处理器：调用 Agent Chat
		return a.Chat(ctx, task.Input)
	}))

	// v0.24.0: Workflow Engine
	workflowExecutor := workflow.NewDefaultExecutor()
	workflowEngine := workflow.NewWorkflowEngine(workflowExecutor, 10)

	telemetryOn, telemetryStop := setupTelemetryFromEnv()

	return &Server{
		agent:       a,
		config:      cfg,
		rateLimiter: newRateLimiter(cfg.RateLimit),
		stats: ServerStats{
			StartTime: time.Now(),
		},
		metrics:         m,
		healthCheck:     hc,
		wsHub:           wsHub,
		collabRegistry:  collabRegistry,
		delegateManager: delegateManager,
		workflowEngine:  workflowEngine,
		telemetryOn:     telemetryOn,
		telemetryStop:   telemetryStop,
	}
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

	mux := http.NewServeMux()

	// API v1 路由（声明式注册）
	s.registerRoutes(mux, []routeEntry{
		{path: "/api/v1/health", handler: s.handleHealth},
		{path: "/api/v1/health/live", handler: s.handleHealthLiveness},
		{path: "/api/v1/health/ready", handler: s.handleHealthReadiness},
		{path: "/api/v1/health/detail", handler: s.handleHealthDetail},
		{path: "/api/v1/metrics", handler: s.handleMetrics},
		{path: "/api/v1/chat", handler: s.handleChat},
		{path: "/api/v1/chat/sync", handler: s.handleChatSync},
		{path: "/api/v1/sessions", handler: s.handleSessions},
		{path: "/api/v1/sessions/", handler: s.handleSessionByID},
		{path: "/api/v1/memory", handler: s.handleMemory},
		{path: "/api/v1/memory/recall", handler: s.handleMemoryRecall},
		{path: "/api/v1/memory/stats", handler: s.handleMemoryStats},
		{path: "/api/v1/tools", handler: s.handleTools},
		{path: "/api/v1/stats", handler: s.handleStats},
		{path: "/api/v1/soul", handler: s.handleSoul},
		{path: "/api/v1/context", handler: s.handleContext},
		{path: "/api/v1/context/fit", handler: s.handleContextFit},
		{path: "/api/v1/rag/index", handler: s.handleRAGIndex},
		{path: "/api/v1/rag/search", handler: s.handleRAGSearch},
		{path: "/api/v1/rag/stats", handler: s.handleRAGStats},
		{path: "/api/v1/rag/store", handler: s.handleRAGStore}, // v0.21.0: SQLite 持久化
		{path: "/api/v1/rag/stream/watch", handler: s.handleRAGStreamWatch},
		{path: "/api/v1/rag/stream/scan", handler: s.handleRAGStreamScan},
		{path: "/api/v1/rag/stream/start", handler: s.handleRAGStreamStart},
		{path: "/api/v1/rag/stream/stop", handler: s.handleRAGStreamStop},
		{path: "/api/v1/rag/stream/status", handler: s.handleRAGStreamStatus},
		{path: "/api/v1/rag/stream/index", handler: s.handleRAGStreamIndex},
		{path: "/api/v1/rag/stream/queue", handler: s.handleRAGStreamQueue},
		{path: "/api/v1/rag/stream/process", handler: s.handleRAGStreamProcess},
		{path: "/api/v1/fc", handler: s.handleFunctionCalling},
		{path: "/api/v1/fc/tools", handler: s.handleFCTools},
		{path: "/api/v1/fc/history", handler: s.handleFCHistory},
		{path: "/api/v1/ws", handler: s.handleWebSocket},
		{path: "/api/v1/ws/stats", handler: s.handleWSStats},
		{path: "/api/v1/soul/templates", handler: s.handleSoulTemplates},
		{path: "/api/v1/soul/templates/", handler: s.handleSoulTemplateByID},
		{path: "/api/v1/embedders", handler: s.handleEmbedderList},
		{path: "/api/v1/embedders/register", handler: s.handleEmbedderRegister},
		{path: "/api/v1/embedders/switch", handler: s.handleEmbedderSwitch},
		{path: "/api/v1/embedders/", handler: s.handleEmbedderRoutes},
		{path: "/api/v1/agents", handler: s.handleAgentsList},
		{path: "/api/v1/agents/register", handler: s.handleAgentsRegister},
		{path: "/api/v1/agents/deregister", handler: s.handleAgentsDeregister},
		{path: "/api/v1/agents/delegate", handler: s.handleAgentsDelegate},
		{path: "/api/v1/agents/task", handler: s.handleAgentsTask},
		{path: "/api/v1/agents/tasks", handler: s.handleAgentsTasks},
		{path: "/api/v1/agents/cancel", handler: s.handleAgentsCancel},
		{path: "/api/v1/workflows", handler: s.handleWorkflows},
		{path: "/api/v1/workflows/", handler: s.handleWorkflowByID},
		{path: "/api/v1/workflow-instances", handler: s.handleWorkflowInstances},
		{path: "/api/v1/workflow-instances/", handler: s.handleWorkflowInstanceByID},
		{path: "/api/v1/gateways", handler: s.handleGatewaysList},
		{path: "/api/v1/gateways/telegram/start", handler: s.handleGatewayTelegramStart},
		{path: "/api/v1/gateways/", handler: s.handleGatewayByName},
		{path: "/", handler: s.handleRoot},
	})

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

	s.server = &http.Server{
		Addr:         s.config.Addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // SSE 需要较长超时
		IdleTimeout:  120 * time.Second,
	}

	s.running = true
	s.mu.Unlock()

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("api server crashed", "error", err)
			fmt.Printf("API server error: %v\n", err)
		}
	}()

	logger.Info("api server started",
		"addr", s.config.Addr,
		"cors_enabled", s.config.EnableCORS,
		"api_keys", len(s.config.APIKeys),
		"rate_limit", s.config.RateLimit,
	)
	fmt.Printf("🚀 LuckyHarness API Server running at http://localhost%s\n", s.config.Addr)
	fmt.Printf("   API: /api/v1/chat | /api/v1/health | /api/v1/stats\n")
	return nil
}

// Stop 停止 API Server
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.server == nil {
		return nil
	}

	logger.Info("api server stopping", "addr", s.config.Addr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// v0.18.0: 停止 WebSocket Hub
	if s.wsHub != nil {
		s.wsHub.Stop()
	}

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}
	if s.telemetryStop != nil {
		if err := s.telemetryStop(ctx); err != nil {
			logger.Warn("telemetry shutdown failed", "error", err)
		}
	}

	s.running = false
	logger.Info("api server stopped", "addr", s.config.Addr)
	return nil
}

// IsRunning 返回是否运行中
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Stats 返回服务器统计
func (s *Server) Stats() ServerStats {
	s.stats.mu.RLock()
	defer s.stats.mu.RUnlock()
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

// handleHealth 健康检查（兼容旧版）
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"version":   "v0.21.0",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

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

	s.stats.mu.Lock()
	s.stats.ChatReqs++
	s.stats.TotalReqs++
	s.stats.LastReqTime = time.Now()
	s.stats.mu.Unlock()

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
	loopCfg.AutoApprove = req.AutoApprove

	ctx := r.Context()

	// SSE 流式响应
	flusher, ok := w.(http.Flusher)
	if !ok {
		// 不支持 SSE，降级为同步
		s.doChatSync(w, r, req, loopCfg, ctx, start)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sessionID := req.SessionID
	if sessionID == "" {
		if sess := s.agent.Sessions().NewWithTitle("chat-stream"); sess != nil {
			sessionID = sess.ID
		}
	}
	turn := agent.MultimodalUserTurnInput(req.Message, req.Attachments)
	events, err := s.agent.ChatWithSessionStreamInputWithLoopConfig(ctx, sessionID, turn, loopCfg)
	if err != nil {
		s.sendSSEError(w, flusher, err.Error())
		return
	}

	for event := range events {
		data, _ := jsonAPI.Marshal(map[string]interface{}{
			"type":       chatEventTypeString(event.Type),
			"content":    event.Content,
			"session_id": sessionID,
		})

		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		if event.Type == agent.ChatEventDone || event.Type == agent.ChatEventError {
			break
		}
	}

	duration := time.Since(start)
	summary, _ := jsonAPI.Marshal(map[string]interface{}{
		"type":     "complete",
		"duration": duration.String(),
	})
	fmt.Fprintf(w, "data: %s\n\n", summary)
	flusher.Flush()
}

// handleChatSync 同步聊天
func (s *Server) handleChatSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	s.stats.mu.Lock()
	s.stats.ChatReqs++
	s.stats.TotalReqs++
	s.stats.LastReqTime = time.Now()
	s.stats.mu.Unlock()

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
	if req.MaxIter > 0 {
		loopCfg.MaxIterations = req.MaxIter
	}
	loopCfg.AutoApprove = req.AutoApprove

	ctx := r.Context()
	s.doChatSync(w, r, req, loopCfg, ctx, start)
}

func (s *Server) doChatSync(w http.ResponseWriter, r *http.Request, req ChatRequest, loopCfg agent.LoopConfig, ctx context.Context, start time.Time) {
	turn := agent.MultimodalUserTurnInput(req.Message, req.Attachments)
	// v0.56.0: 检测内置命令
	if strings.HasPrefix(req.Message, "/") {
		parts := strings.SplitN(strings.TrimPrefix(req.Message, "/"), " ", 2)
		cmd := parts[0]
		// args := ""
		// if len(parts) > 1 {
		// 	args = parts[1]
		// }

		// 简单命令处理（不依赖 gateway handler）
		switch cmd {
		case "help":
			s.sendJSON(w, http.StatusOK, ChatResponse{
				Response: `🐾 *LuckyHarness 命令*

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
				Response: fmt.Sprintf("📊 *LuckyHarness Status*\n\n• Version: v0.55.0\n• Model: %s\n• Uptime: %s\n• Total requests: %d",
					cfg.Model, formatDuration(uptime), s.agent.Metrics().TotalRequests.Load()),
			})
			return
		case "stop":
			s.sendJSON(w, http.StatusOK, ChatResponse{
				Response: "ℹ️ Stop command received. Task cancellation not yet implemented.",
			})
			return
		case "restart":
			s.sendJSON(w, http.StatusOK, ChatResponse{
				Response: "🔄 Restarting...\n\n⚠️ Auto-restart not implemented. Please restart manually.",
			})
			return
		default:
			s.sendJSON(w, http.StatusOK, ChatResponse{
				Response: fmt.Sprintf("Unknown command: /%s\nType /help for available commands.", cmd),
			})
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

	// 使用 RunLoopWithSession 确保消息被保存；无法创建/获取会话时降级为无会话 RunLoop
	var (
		result *agent.LoopResult
		err    error
	)
	if sess != nil {
		loopCfgWithSession := agent.LoopConfig{
			MaxIterations: loopCfg.MaxIterations,
			Timeout:       loopCfg.Timeout,
			AutoApprove:   loopCfg.AutoApprove,
		}
		result, err = s.agent.RunLoopWithSessionInput(ctx, sess, turn, loopCfgWithSession)
	} else {
		result, err = s.agent.RunLoopWithSessionInput(ctx, nil, turn, loopCfg)
	}
	if err != nil {
		s.stats.mu.Lock()
		s.stats.ErrorReqs++
		s.stats.mu.Unlock()
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

// handleSessions 会话列表
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Agent 暴露 session manager
		sessions := s.agent.Sessions().List()
		type sessionInfo struct {
			ID           string `json:"id"`
			MessageCount int    `json:"message_count"`
			CreatedAt    string `json:"created_at"`
			UpdatedAt    string `json:"updated_at"`
		}

		var infos []sessionInfo
		for _, sess := range sessions {
			msgs := sess.GetMessages()
			infos = append(infos, sessionInfo{
				ID:           sess.ID,
				MessageCount: len(msgs),
				CreatedAt:    sess.CreatedAt.Format(time.RFC3339),
				UpdatedAt:    sess.UpdatedAt.Format(time.RFC3339),
			})
		}

		s.sendJSON(w, http.StatusOK, map[string]interface{}{
			"sessions": infos,
			"count":    len(infos),
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
		}

		s.sendJSON(w, http.StatusCreated, map[string]interface{}{
			"id":            sess.ID,
			"title":         sess.Title,
			"message_count": sess.MessageCount(),
			"created_at":    sess.CreatedAt.Format(time.RFC3339),
			"updated_at":    sess.UpdatedAt.Format(time.RFC3339),
		})
	default:
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
	}
}

// handleSessionByID returns the full history for a single session.
func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/")
	id = strings.Trim(id, "/")
	if id == "" {
		s.sendError(w, "session id is required", http.StatusBadRequest, "")
		return
	}

	sess, ok := s.agent.Sessions().Get(id)
	if !ok {
		s.sendError(w, "session not found", http.StatusNotFound, id)
		return
	}

	messages := sess.GetMessages()
	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"id":            sess.ID,
		"title":         sess.Title,
		"message_count": len(messages),
		"created_at":    sess.CreatedAt.Format(time.RFC3339),
		"updated_at":    sess.UpdatedAt.Format(time.RFC3339),
		"messages":      messages,
	})
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

			var err error
			if body.LongTerm {
				err = s.agent.RememberLongTerm(body.Content, body.Category)
			} else {
				err = s.agent.Remember(body.Content, body.Category)
			}

			if err != nil {
				s.sendError(w, "save memory failed", http.StatusInternalServerError, err.Error())
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

	results := s.agent.Recall(query)
	var entries []MemoryEntry
	for _, e := range results {
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
	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"short_term":  stats[memory.TierShort],
		"medium_term": stats[memory.TierMedium],
		"long_term":   stats[memory.TierLong],
		"total":       stats[memory.TierShort] + stats[memory.TierMedium] + stats[memory.TierLong],
	})
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
		"name":    "LuckyHarness API",
		"version": "v0.21.0",
		"endpoints": []string{
			"POST /api/v1/chat       — 流式聊天 (SSE)",
			"POST /api/v1/chat/sync  — 同步聊天",
			"GET  /api/v1/ws         — WebSocket 实时通信",
			"GET  /api/v1/ws/stats   — WebSocket 统计",
			"GET  /api/v1/sessions   — 会话列表",
			"GET  /api/v1/memory     — 记忆统计",
			"POST /api/v1/memory     — 保存记忆",
			"GET  /api/v1/memory/recall?q= — 搜索记忆",
			"GET  /api/v1/memory/stats    — 记忆统计",
			"GET  /api/v1/tools      — 工具列表",
			"GET  /api/v1/stats      — 服务器统计",
			"GET  /api/v1/soul       — SOUL 信息",
			"GET  /api/v1/health     — 健康检查",
		},
	})
}

// ===== 中间件 =====

// corsMiddleware CORS 中间件
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.config.EnableCORS {
			next.ServeHTTP(w, r)
			return
		}

		origin := r.Header.Get("Origin")
		allowed := false

		if len(s.config.CORSOrigins) == 0 {
			// 无配置 = 不允许任何跨域（安全默认值）
			allowed = false
		} else {
			for _, o := range s.config.CORSOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}
		}

		if allowed {
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			} else {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		if r.Method == http.MethodOptions {
			if allowed {
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.WriteHeader(http.StatusForbidden)
			}
			return
		}

		next.ServeHTTP(w, r)
	})
}

// authMiddleware API Key 认证中间件
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 健康检查不需要认证
		if r.URL.Path == "/api/v1/health" || r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}

		// 无配置 API Key 则仅允许本地访问
		if len(s.config.APIKeys) == 0 {
			ip := r.RemoteAddr
			if idx := strings.LastIndex(ip, ":"); idx != -1 {
				ip = ip[:idx]
			}
			if ip != "127.0.0.1" && ip != "::1" && ip != "localhost" {
				logger.Warn("auth rejected non-local request without api keys",
					"path", r.URL.Path,
					"method", r.Method,
					"remote_addr", r.RemoteAddr,
				)
				s.sendError(w, "api key required (no keys configured, localhost only)", http.StatusUnauthorized,
					"configure api_keys in server config or access from localhost")
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// 从 Header 获取 API Key（不再支持 query string，防止日志泄露）
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			apiKey = r.Header.Get("Authorization")
			if strings.HasPrefix(apiKey, "Bearer ") {
				apiKey = strings.TrimPrefix(apiKey, "Bearer ")
			}
		}

		if apiKey == "" {
			logger.Warn("auth missing api key",
				"path", r.URL.Path,
				"method", r.Method,
				"remote_addr", r.RemoteAddr,
			)
			s.sendError(w, "api key required", http.StatusUnauthorized, "provide X-API-Key header or Authorization: Bearer <key>")
			return
		}

		// 常量时间比较，防止 timing attack
		valid := false
		for _, k := range s.config.APIKeys {
			if subtle.ConstantTimeCompare([]byte(k), []byte(apiKey)) == 1 {
				valid = true
				break
			}
		}

		if !valid {
			logger.Warn("auth invalid api key",
				"path", r.URL.Path,
				"method", r.Method,
				"remote_addr", r.RemoteAddr,
			)
			s.sendError(w, "invalid api key", http.StatusForbidden, "")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware 限流中间件
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if !s.rateLimiter.Allow(ip) {
			logger.Warn("rate limit exceeded",
				"path", r.URL.Path,
				"method", r.Method,
				"remote_addr", r.RemoteAddr,
				"limit_per_min", s.config.RateLimit,
			)
			s.sendError(w, "rate limit exceeded", http.StatusTooManyRequests,
				fmt.Sprintf("limit: %d req/min", s.config.RateLimit))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware 日志中间件
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		duration := time.Since(start)
		clientIP := r.RemoteAddr
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			parts := strings.Split(xff, ",")
			if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
				clientIP = strings.TrimSpace(parts[0])
			}
		} else if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
			clientIP = xrip
		} else if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			clientIP = host
		}

		fields := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.StatusCode(),
			"duration_ms", duration.Milliseconds(),
			"bytes", rec.bytes,
			"client_ip", clientIP,
		}
		if ua := strings.TrimSpace(r.UserAgent()); ua != "" {
			fields = append(fields, "user_agent", ua)
		}

		switch {
		case rec.StatusCode() >= http.StatusInternalServerError:
			logger.Error("http request completed", fields...)
		case rec.StatusCode() >= http.StatusBadRequest:
			logger.Warn("http request completed", fields...)
		default:
			logger.Info("http request completed", fields...)
		}
	})
}

// recoveryMiddleware 恢复中间件
func (s *Server) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				s.stats.mu.Lock()
				s.stats.ErrorReqs++
				s.stats.mu.Unlock()
				// 内部错误详情只写日志，不返回给客户端
				logger.Error("panic recovered",
					"path", r.URL.Path,
					"method", r.Method,
					"panic", err,
				)
				s.sendError(w, "internal server error", http.StatusInternalServerError, "")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ===== 辅助函数 =====

func (s *Server) sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	jsonAPI.NewEncoder(w).Encode(data)
}

func (s *Server) sendError(w http.ResponseWriter, msg string, code int, details string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	jsonAPI.NewEncoder(w).Encode(ErrorResponse{
		Error:   msg,
		Code:    code,
		Details: details,
	})
}

func (s *Server) registerRoutes(mux *http.ServeMux, routes []routeEntry) {
	for _, route := range routes {
		mux.HandleFunc(route.path, route.handler)
	}
}

func (s *Server) dispatchMethod(w http.ResponseWriter, r *http.Request, handlers map[string]func()) bool {
	handler, ok := handlers[r.Method]
	if !ok {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return false
	}
	handler()
	return true
}

func (s *Server) sendSSEError(w io.Writer, flusher http.Flusher, msg string) {
	data, _ := jsonAPI.Marshal(map[string]interface{}{
		"type":  "error",
		"error": msg,
	})
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
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

type rateLimiter struct {
	mu      sync.RWMutex
	limit   int
	clients map[string]*clientBucket
}

type clientBucket struct {
	count   int
	resetAt time.Time
}

func newRateLimiter(limit int) *rateLimiter {
	rl := &rateLimiter{
		limit:   limit,
		clients: make(map[string]*clientBucket),
	}

	// 后台清理过期桶
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			rl.cleanup()
		}
	}()

	return rl
}

func (rl *rateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	bucket, ok := rl.clients[ip]
	if !ok || now.After(bucket.resetAt) {
		rl.clients[ip] = &clientBucket{
			count:   1,
			resetAt: now.Add(time.Minute),
		}
		return true
	}

	bucket.count++
	return bucket.count <= rl.limit
}

func (rl *rateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for ip, bucket := range rl.clients {
		if now.After(bucket.resetAt) {
			delete(rl.clients, ip)
		}
	}
}

// Ensure Agent exposes Sessions
// We need to add Sessions() method to Agent
var _ provider.Provider = (*provider.OpenAIProvider)(nil)
