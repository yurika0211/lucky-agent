package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyharness/internal/autonomy"
	"github.com/yurika0211/luckyharness/internal/collab"
	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/contextx"
	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/embedder"
	"github.com/yurika0211/luckyharness/internal/function"
	"github.com/yurika0211/luckyharness/internal/gateway"
	appheartbeat "github.com/yurika0211/luckyharness/internal/heartbeat"
	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/metrics"
	"github.com/yurika0211/luckyharness/internal/middleware"
	"github.com/yurika0211/luckyharness/internal/multimodal"
	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/rag"
	"github.com/yurika0211/luckyharness/internal/resilience"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/soul"
	"github.com/yurika0211/luckyharness/internal/tool"
	"github.com/yurika0211/luckyharness/internal/utils"
)

/*
embedderRuntimeConfig 描述运行时解析后的嵌入模型配置。
*/
type embedderRuntimeConfig struct {
	APIKey    string
	Model     string
	BaseURL   string
	Dimension int
}

// Agent 是 LuckyHarness 的核心 Agent
type Agent struct {
	cfg                *config.Manager
	soul               *soul.Soul
	tmplMgr            *soul.TemplateManager  // SOUL 模板管理器
	provider           provider.Provider      // 当前活跃 provider (可能是 FallbackChain)
	registry           *provider.Registry     // provider 注册表
	catalog            *provider.ModelCatalog // 模型目录
	tokenStore         *provider.TokenStore   // token 存储
	memory             *memory.Store
	shortTerm          *memory.ShortTermBuffer // 短期记忆滑动窗口
	midTerm            *memory.MidTermStore    // 中期会话摘要存储
	sessions           *session.Manager
	tools              *tool.Registry
	gateway            *tool.Gateway           // 统一工具网关
	msgGateway         *gateway.GatewayManager // 消息平台网关
	mcpClient          *tool.MCPClient         // MCP 客户端
	delegate           *tool.DelegateManager   // 子代理委派管理器
	contextWin         *contextx.ContextWindow // 上下文窗口管理器
	contextEst         *contextx.TokenEstimator
	ragManager         *rag.RAGManager         // RAG 知识库管理器
	ragPersist         *rag.Persistence        // RAG 持久化
	streamIndexer      *rag.StreamIndexer      // 流式索引器
	embedderReg        *embedder.Registry      // 嵌入模型注册表
	collabReg          *collab.Registry        // Agent 协作注册表
	collabMgr          *collab.DelegateManager // 协作任务管理器
	skills             []*tool.SkillInfo       // 已加载的 skill 列表
	metrics            *metrics.Metrics        // 指标收集器
	cronEngine         *cron.Engine            // 定时任务引擎
	cronStore          *cron.Store
	autonomy           *autonomy.AutonomyKit // 自主工作套件
	heartbeatSvc       *appheartbeat.Service
	heartbeatMu        sync.Mutex
	heartbeatSessionID string
	recentTarget       recentChatTarget
	contextCache       *contextMessageCache
	mediaProcessor     *multimodal.Processor
	chatCount          int // 对话计数，用于触发自动摘要
	activeModel        string
	activeAPIBase      string
}

/*
resolveEmbedderRuntimeConfig 从环境变量和主配置中解析嵌入模型运行时配置。

返回值中的布尔值表示是否解析到了任何有效配置项。
*/
func resolveEmbedderRuntimeConfig(c *config.Config) (embedderRuntimeConfig, bool) {
	cfg := embedderRuntimeConfig{
		APIKey:  os.Getenv("EMBEDDING_MODEL_KEY"),
		Model:   os.Getenv("EMBEDDING_MODEL_NAME"),
		BaseURL: os.Getenv("EMBEDDING_MODEL_URL"),
	}
	if dim := os.Getenv("EMBEDDING_MODEL_DIMENSION"); dim != "" {
		dim = strings.TrimSpace(dim)
		if d, err := strconv.Atoi(dim); err == nil && d > 0 {
			cfg.Dimension = d
		}
	}

	return cfg, cfg.APIKey != "" || cfg.BaseURL != "" || cfg.Model != "" || cfg.Dimension > 0
}

/*
toProviderConfig 将全局配置转换为 provider 层可消费的配置对象。
*/
func toProviderConfig(c *config.Config, modelOverride, apiBaseOverride string) provider.Config {
	model := c.Model
	if strings.TrimSpace(modelOverride) != "" {
		model = strings.TrimSpace(modelOverride)
	}
	apiBase := c.APIBase
	if strings.TrimSpace(apiBaseOverride) != "" {
		apiBase = strings.TrimSpace(apiBaseOverride)
	}
	return provider.Config{
		LlmProvider: provider.LlmProvider{
			Name:        c.Provider,
			APIKey:      c.APIKey,
			BaseURL:     apiBase,
			Model:       model,
			Temperature: c.Temperature,
		},
		ExtraHeaders: c.ExtraHeaders,
		Limits:       c.Limits,
		Retry:        c.Retry,
		CircuitBreaker: provider.CircuitBreakerConfig{
			Enabled:         c.CircuitBreaker.Enabled,
			ErrorThreshold:  c.CircuitBreaker.ErrorThreshold,
			WindowSeconds:   c.CircuitBreaker.WindowSeconds,
			TimeoutSeconds:  c.CircuitBreaker.TimeoutSeconds,
			HalfOpenMaxReqs: c.CircuitBreaker.HalfOpenMaxReqs,
		},
		RateLimit: provider.RateLimitConfig{
			Enabled:           c.RateLimit.Enabled,
			RequestsPerMinute: c.RateLimit.RequestsPerMinute,
			TokensPerMinute:   c.RateLimit.TokensPerMinute,
			BurstSize:         c.RateLimit.BurstSize,
		},
		Context: provider.ContextConfig{
			MaxHistoryTurns:      c.Context.MaxHistoryTurns,
			MaxContextTokens:     c.Context.MaxContextTokens,
			CompressionThreshold: c.Context.CompressionThreshold,
		},
	}
}

/*
wrapProviderWithMiddleware 按当前配置为 provider 叠加中间件链。
*/
func wrapProviderWithMiddleware(p provider.Provider, c *config.Config) provider.Provider {
	if p == nil || c == nil {
		return p
	}
	chain := middleware.NewChain()

	if c.Retry.Enabled {
		retryCfg := resilience.DefaultRetryConfig()
		if c.Retry.MaxAttempts > 0 {
			retryCfg.MaxAttempts = c.Retry.MaxAttempts
		}
		if c.Retry.InitialDelayMs > 0 {
			retryCfg.InitialDelay = time.Duration(c.Retry.InitialDelayMs) * time.Millisecond
		}
		if c.Retry.MaxDelayMs > 0 {
			retryCfg.MaxDelay = time.Duration(c.Retry.MaxDelayMs) * time.Millisecond
		}
		chain.Use(middleware.NewRetryMiddleware(retryCfg))
	}

	if c.CircuitBreaker.Enabled {
		cbCfg := resilience.DefaultCircuitBreakerConfig()
		if c.CircuitBreaker.ErrorThreshold > 0 {
			cbCfg.FailureThreshold = c.CircuitBreaker.ErrorThreshold
		}
		if c.CircuitBreaker.TimeoutSeconds > 0 {
			cbCfg.Timeout = time.Duration(c.CircuitBreaker.TimeoutSeconds) * time.Second
		}
		if c.CircuitBreaker.HalfOpenMaxReqs > 0 {
			cbCfg.HalfOpenMaxReqs = c.CircuitBreaker.HalfOpenMaxReqs
		}
		chain.Use(middleware.NewCircuitBreakerMiddleware(cbCfg))
	}

	if c.RateLimit.Enabled {
		limit := c.RateLimit.RequestsPerMinute
		if limit <= 0 {
			limit = 60
		}
		chain.Use(middleware.NewRateLimitMiddleware(limit, time.Minute))
	}

	if chain.Len() == 0 {
		return p
	}
	return middleware.NewMiddlewareProvider(p, chain)
}

/*
maybeRouteModel 根据输入内容与估算 token 数决定是否切换到更合适的模型。
*/
func (a *Agent) maybeRouteModel(userInput string) {
	if a == nil || a.cfg == nil || a.registry == nil {
		return
	}
	cfg := a.cfg.Get()
	if !cfg.ModelRouter.Enable || len(cfg.Fallbacks) > 0 {
		return
	}
	router := config.NewModelRouter(cfg.ModelRouter)
	tokenCount := len(userInput) / 4
	if a.contextEst != nil {
		tokenCount = a.contextEst.Estimate(userInput)
	}
	model, apiBase := router.SelectModelForTask(userInput, tokenCount)
	if strings.TrimSpace(model) == "" {
		return
	}
	if model == a.activeModel && strings.TrimSpace(apiBase) == strings.TrimSpace(a.activeAPIBase) {
		return
	}

	pCfg := toProviderConfig(cfg, model, apiBase)
	routedProvider, err := a.registry.Resolve(pCfg)
	if err != nil {
		return
	}
	a.provider = wrapProviderWithMiddleware(routedProvider, cfg)
	a.activeModel = model
	if strings.TrimSpace(apiBase) != "" {
		a.activeAPIBase = apiBase
	} else {
		a.activeAPIBase = cfg.APIBase
	}
}

// New 创建 Agent
func New(cfg *config.Manager) (*Agent, error) {
	// v0.37.0: 从环境变量覆盖 web_search 配置
	applyWebSearchEnv(cfg)

	c := cfg.Get()

	// 加载 SOUL
	var s *soul.Soul
	soulPath := c.SoulPath
	if soulPath != "" {
		loaded, err := soul.Load(soulPath)
		if err != nil {
			s = soul.Default()
		} else {
			s = loaded
		}
	} else {
		s = soul.Default()
	}

	// v0.19.0: 创建 SOUL 模板管理器
	tmplMgr := soul.NewTemplateManager()

	// 创建 Provider 注册表
	registry := provider.NewRegistry()

	// 创建模型目录
	catalog := provider.NewModelCatalog()

	// 创建 Token 存储
	tokenStore, err := provider.NewTokenStore(cfg.HomeDir() + "/tokens")
	if err != nil {
		tokenStore = nil // 非关键错误
	}

	// 解析 Provider（支持降级链）
	var p provider.Provider
	if len(c.Fallbacks) > 0 {
		// 使用降级链模式
		fallbackConfigs := make([]provider.FallbackConfig, 0, len(c.Fallbacks)+1)
		// 第一个是主 provider
		fallbackConfigs = append(fallbackConfigs, provider.FallbackConfig{
			Name:    c.Provider,
			APIKey:  c.APIKey,
			APIBase: c.APIBase,
			Model:   c.Model,
		})
		// 后续是降级 provider
		for _, fb := range c.Fallbacks {
			fallbackConfigs = append(fallbackConfigs, provider.FallbackConfig{
				Name:    fb.Provider,
				APIKey:  fb.APIKey,
				APIBase: fb.APIBase,
				Model:   fb.Model,
			})
		}
		chain, err := provider.NewFallbackChain(fallbackConfigs, registry)
		if err != nil {
			return nil, fmt.Errorf("create fallback chain: %w", err)
		}
		p = chain
	} else {
		// 单 provider 模式
		pCfg := toProviderConfig(c, "", "")
		p, err = registry.Resolve(pCfg)
		if err != nil {
			return nil, fmt.Errorf("resolve provider: %w", err)
		}
	}
	p = wrapProviderWithMiddleware(p, c)

	// 创建记忆存储
	mem, err := memory.NewStore(cfg.HomeDir() + "/memory")
	if err != nil {
		return nil, fmt.Errorf("init memory: %w", err)
	}

	// v0.43.0: 创建短期记忆滑动窗口
	shortTermMaxTurns := c.Memory.ShortTermMaxTurns
	if shortTermMaxTurns <= 0 {
		shortTermMaxTurns = 10
	}
	shortTerm := memory.NewShortTermBuffer(shortTermMaxTurns)

	// v0.43.0: 创建中期会话摘要存储
	midTermMaxSummaries := c.Memory.MidTermMaxSummaries
	if midTermMaxSummaries <= 0 {
		midTermMaxSummaries = 100
	}
	midTerm, err := memory.NewMidTermStore(cfg.HomeDir()+"/memory/midterm", midTermMaxSummaries)
	if err != nil {
		return nil, fmt.Errorf("init midterm store: %w", err)
	}

	// 创建会话管理器
	sessions, err := session.NewManager(cfg.HomeDir() + "/sessions")
	if err != nil {
		return nil, fmt.Errorf("init sessions: %w", err)
	}

	// 创建工具注册表并注册内置工具（带搜索配置）
	tools := tool.NewRegistry()
	searchCfg := &tool.WebSearchConfig{
		Provider:   c.WebSearch.Provider,
		APIKey:     c.WebSearch.APIKey,
		BaseURL:    c.WebSearch.BaseURL,
		MaxResults: c.WebSearch.MaxResults,
		Proxy:      c.WebSearch.Proxy,
	}
	tool.RegisterBuiltinToolsWithConfig(tools, searchCfg)

	// 创建子代理委派管理器
	delegateMgr := tool.NewDelegateManager(tool.DefaultDelegateConfig())
	tools.Register(tool.DelegateTaskTool(delegateMgr))
	tools.Register(tool.TaskStatusTool(delegateMgr))
	tools.Register(tool.ListTasksTool(delegateMgr))

	// 创建 MCP 客户端
	mcpClient := tool.NewMCPClient()

	// 创建统一工具网关
	toolGateway := tool.NewGateway(tools)

	// 创建上下文窗口管理器
	contextWin := contextx.NewContextWindow(contextx.WindowConfig{
		MaxTokens:            c.MaxTokens,
		ReservedTokens:       c.MaxTokens / 4, // 为回复预留 1/4
		Strategy:             contextx.TrimLowPriority,
		SlidingWindowSize:    10,
		MaxConversationTurns: 50,
		MemoryBudget:         800,
		SummarizeThreshold:   0.8,
	})
	contextEst := contextx.NewTokenEstimator(c.MaxTokens)

	// 创建 RAG 知识库管理器
	// v0.21.0: 使用 Embedder Registry 管理嵌入模型
	embedderReg := embedder.NewRegistry()
	mockEmb := embedder.NewMockEmbedder(128)
	embedderReg.Register("mock-128", mockEmb)

	if embCfg, ok := resolveEmbedderRuntimeConfig(c); ok {
		openaiEmb := embedder.NewOpenAIEmbedder(embedder.OpenAIEmbedderConfig{
			APIKey:    embCfg.APIKey,
			Model:     embCfg.Model,
			BaseURL:   embCfg.BaseURL,
			Dimension: embCfg.Dimension,
		})
		if embedderReg.Register("openai-default", openaiEmb) {
			embedderReg.Switch("openai-default")
		}
	}

	// 使用 active embedder (带缓存)
	activeEmb := embedder.NewCachedEmbedder(embedderReg.Active(), 512)

	ragConfig := rag.DefaultRAGConfig()

	var ragManager *rag.RAGManager
	var ragPersist *rag.Persistence

	// 尝试使用 SQLite 后端
	ragDBPath := cfg.HomeDir() + "/rag/luckyharness.db"
	ragMgr, err := rag.NewRAGManagerWithSQLite(activeEmb, ragConfig, ragDBPath)
	if err != nil {
		// SQLite 不可用时降级到内存 + JSON 持久化
		ragManager = rag.NewRAGManager(activeEmb, ragConfig)
		ragPersist = rag.NewPersistence(cfg.HomeDir() + "/rag")
		if ragPersist.Exists() {
			if docCount, loadErr := ragPersist.Load(ragManager); loadErr == nil && docCount > 0 {
				// loaded successfully
			}
		}
	} else {
		ragManager = ragMgr
		// SQLite 后端自动持久化，不需要 JSON 持久化
		// 但保留 ragPersist 用于迁移旧数据
		ragPersist = rag.NewPersistence(cfg.HomeDir() + "/rag")
		if ragPersist.Exists() {
			// 迁移旧 JSON 数据到 SQLite
			tempMgr := rag.NewRAGManager(activeEmb, ragConfig)
			if docCount, loadErr := ragPersist.Load(tempMgr); loadErr == nil && docCount > 0 {
				for _, docID := range tempMgr.ListDocuments() {
					if doc, ok := tempMgr.GetDocument(docID); ok {
						ragManager.IndexText(doc.Path, doc.Title, "")
					}
				}
			}
		}
	}

	// v0.22.0: 创建 Agent 协作注册表和管理器
	collabReg := collab.NewRegistry()
	// 注册本地 Agent
	collabReg.Register(&collab.AgentProfile{
		ID:           "local-agent",
		Name:         "Local Agent",
		Description:  "The primary local agent",
		Capabilities: []string{"chat", "code", "analysis", "research"},
		Status:       collab.StatusOnline,
	})
	// 创建协作任务管理器（使用默认 handler，实际执行由 Agent Loop 驱动）
	collabMgr := collab.NewDelegateManager(collabReg, nil)

	// v0.23.0: 创建流式索引器
	streamIndexer := rag.NewStreamIndexer(ragManager, rag.DefaultStreamConfig())

	// v0.36.0: 创建指标收集器
	m := metrics.NewMetrics()

	mediaProcessor := multimodal.NewProcessor()
	_ = mediaProcessor.RegisterProvider(multimodal.NewLocalProvider(
		multimodal.ModalityText,
		multimodal.ModalityImage,
		multimodal.ModalityAudio,
		multimodal.ModalityVideo,
		multimodal.ModalityDocument,
	), true)
	if c.APIKey != "" && (c.Provider == "openai" || strings.Contains(strings.ToLower(c.APIBase), "openai.com")) {
		if openaiMedia, mediaErr := multimodal.NewOpenAIMediaProvider(multimodal.OpenAIMediaConfig{
			APIKey:  c.APIKey,
			APIBase: c.APIBase,
		}); mediaErr == nil {
			_ = mediaProcessor.RegisterProvider(openaiMedia, true)
		}
	}

	// v0.36.0: 创建定时任务引擎
	cronEngine := cron.NewEngine()
	cronEngine.SetEventHandler(func(event cron.Event) {
		switch event.Type {
		case cron.EventJobStarted:
			fmt.Printf("[cron] job %s started\n", event.JobName)
		case cron.EventJobCompleted:
			fmt.Printf("[cron] job %s completed\n", event.JobName)
		case cron.EventJobFailed:
			fmt.Printf("[cron] job %s failed: %v\n", event.JobName, event.Error)
		}
	})

	// v0.38.0: 创建自主工作套件
	autonomyCfg := autonomy.DefaultAutonomyConfig()
	// AgentExecutor will be set after Agent is fully constructed
	autonomyKit := autonomy.NewAutonomyKit(autonomyCfg, nil)

	// 注册自主工作工具
	autonomyTools := autonomy.NewToolDefinitions(autonomyKit)
	tools.Register(&tool.Tool{
		Name:            "autonomy_queue_add",
		Description:     "Add a task to the autonomy task queue. Tasks are picked up by workers automatically.",
		Category:        tool.CatDelegate,
		Source:          "builtin",
		Permission:      tool.PermAuto,
		HiddenFromModel: true,
		Parameters: map[string]tool.Param{
			"title":       {Type: "string", Description: "Task title", Required: true},
			"description": {Type: "string", Description: "Detailed task description", Required: false},
			"priority":    {Type: "string", Description: "Priority: low, normal, high, critical", Required: false, Default: "normal"},
			"tags":        {Type: "array", Description: "Tags for categorization", Required: false},
		},
		Handler: autonomyTools.HandleQueueAdd,
	})
	tools.Register(&tool.Tool{
		Name:            "autonomy_queue_list",
		Description:     "List tasks in the autonomy queue. Optionally filter by state.",
		Category:        tool.CatDelegate,
		Source:          "builtin",
		Permission:      tool.PermAuto,
		HiddenFromModel: true,
		Parameters: map[string]tool.Param{
			"state": {Type: "string", Description: "Filter by state: ready, in_progress, blocked, done", Required: false},
		},
		Handler: autonomyTools.HandleQueueList,
	})
	tools.Register(&tool.Tool{
		Name:            "autonomy_queue_update",
		Description:     "Update a task's state in the autonomy queue.",
		Category:        tool.CatDelegate,
		Source:          "builtin",
		Permission:      tool.PermAuto,
		HiddenFromModel: true,
		Parameters: map[string]tool.Param{
			"task_id": {Type: "string", Description: "Task ID to update", Required: true},
			"action":  {Type: "string", Description: "Action: complete, fail, block, unblock", Required: true},
			"result":  {Type: "string", Description: "Result text (for complete action)", Required: false},
			"error":   {Type: "string", Description: "Error message (for fail action)", Required: false},
			"reason":  {Type: "string", Description: "Block reason (for block action)", Required: false},
			"retry":   {Type: "boolean", Description: "Whether to retry on failure (default true)", Required: false},
		},
		Handler: autonomyTools.HandleQueueUpdate,
	})
	tools.Register(&tool.Tool{
		Name:            "autonomy_worker_spawn",
		Description:     "Spawn a worker to execute a specific task from the queue.",
		Category:        tool.CatDelegate,
		Source:          "builtin",
		Permission:      tool.PermApprove,
		HiddenFromModel: true,
		Parameters: map[string]tool.Param{
			"task_id": {Type: "string", Description: "Task ID to execute", Required: true},
		},
		Handler: autonomyTools.HandleWorkerSpawn,
	})
	tools.Register(&tool.Tool{
		Name:            "autonomy_worker_list",
		Description:     "List active workers and their status.",
		Category:        tool.CatDelegate,
		Source:          "builtin",
		Permission:      tool.PermAuto,
		HiddenFromModel: true,
		Parameters:      map[string]tool.Param{},
		Handler:         autonomyTools.HandleWorkerList,
	})
	tools.Register(&tool.Tool{
		Name:            "autonomy_heartbeat_trigger",
		Description:     "Manually trigger a heartbeat cycle to check for work and dispatch tasks.",
		Category:        tool.CatDelegate,
		Source:          "builtin",
		Permission:      tool.PermAuto,
		HiddenFromModel: true,
		Parameters:      map[string]tool.Param{},
		Handler:         autonomyTools.HandleHeartbeatTrigger,
	})
	tools.Register(&tool.Tool{
		Name:            "autonomy_status",
		Description:     "Get the overall status of the autonomy system (queue, workers, heartbeat).",
		Category:        tool.CatDelegate,
		Source:          "builtin",
		Permission:      tool.PermAuto,
		HiddenFromModel: true,
		Parameters:      map[string]tool.Param{},
		Handler:         autonomyTools.HandleStatus,
	})

	a := &Agent{
		cfg:            cfg,
		soul:           s,
		tmplMgr:        tmplMgr,
		provider:       p,
		registry:       registry,
		catalog:        catalog,
		tokenStore:     tokenStore,
		memory:         mem,
		shortTerm:      shortTerm,
		midTerm:        midTerm,
		sessions:       sessions,
		tools:          tools,
		gateway:        toolGateway,
		msgGateway:     gateway.NewGatewayManager(),
		mcpClient:      mcpClient,
		delegate:       delegateMgr,
		contextWin:     contextWin,
		contextEst:     contextEst,
		ragManager:     ragManager,
		ragPersist:     ragPersist,
		streamIndexer:  streamIndexer,
		embedderReg:    embedderReg,
		collabReg:      collabReg,
		collabMgr:      collabMgr,
		metrics:        m,
		cronEngine:     cronEngine,
		cronStore:      cron.NewStore(filepath.Join(cfg.HomeDir(), "mission.md")),
		autonomy:       autonomyKit,
		contextCache:   newContextMessageCache(64),
		mediaProcessor: mediaProcessor,
		activeModel:    c.Model,
		activeAPIBase:  c.APIBase,
	}

	a.registerCronTools()
	a.registerHeartbeatTools()

	// v0.35.0: 自动加载 skills 目录
	skillsDir := cfg.HomeDir() + "/skills"
	if info, err := os.Stat(skillsDir); err == nil && info.IsDir() {
		if count, err := a.LoadSkills(skillsDir); err == nil && count > 0 {
			fmt.Printf("[agent] loaded %d skills from %s\n", count, skillsDir)
		}
	}

	// v0.36.0: 启动定时任务引擎
	cronEngine.Start()
	if restored, restoreErr := a.restoreCronJobs(); restoreErr != nil {
		fmt.Printf("[cron] restore failed: %v\n", restoreErr)
	} else if restored > 0 {
		fmt.Printf("[cron] restored %d jobs\n", restored)
	}
	if err := a.initHeartbeatService(); err != nil {
		fmt.Printf("[heartbeat] init failed: %v\n", err)
	}

	// v0.38.0: 设置 delegate 的 Agent 执行器，让 delegate_task 真正走 Agent Loop
	delegateMgr.SetAgentExecutor(func(ctx context.Context, description, contextStr string) (string, error) {
		sess := sessions.NewWithTitle("delegate-task")
		prompt := description
		if contextStr != "" {
			prompt = fmt.Sprintf("%s\n\nContext: %s", description, contextStr)
		}
		loopCfg := DefaultLoopConfig()
		loopCfg.AutoApprove = false // 子代理不自动批准危险工具
		loopCfg.MaxIterations = 5   // 子代理限制更严格
		result, err := a.RunLoopWithSession(ctx, sess, prompt, loopCfg)
		if err != nil {
			return "", err
		}
		return result.Response, nil
	})

	// v0.38.0: 将 executor 注入到已注册工具所绑定的 autonomy 实例，避免启动时替换实例。
	a.autonomy.SetExecutor(&agentExecutorAdapter{agent: a})

	return a, nil
}

// Chat 执行一次对话
/*
Chat 在新会话中执行一次完整对话。
*/
func (a *Agent) Chat(ctx context.Context, userInput string) (string, error) {
	sess := a.sessions.New()
	return a.chatWithSession(ctx, sess, userInput)
}

// ChatWithSession 在已有会话中继续对话，实现多轮上下文。
func (a *Agent) ChatWithSession(ctx context.Context, sessionID string, userInput string) (string, error) {
	sess, ok := a.sessions.Get(sessionID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}
	return a.chatWithSession(ctx, sess, userInput)
}

// ProgressFeedback generates a concise model-authored progress update for an unfinished round.
func (a *Agent) ProgressFeedback(ctx context.Context, userInput string, round int, observations []string) (string, error) {
	if a == nil || a.provider == nil {
		return "", fmt.Errorf("provider not initialized")
	}
	if len(observations) == 0 {
		return "", nil
	}

	systemPrompt := "You are generating one concise in-progress update for the user while the main task is still underway. Summarize what has been checked and what remains. Use the user's language. Do not expose hidden chain-of-thought. Do not mention internal event types or implementation details. Limit the update to at most 3 short sentences."

	var userPrompt strings.Builder
	userPrompt.WriteString("Original user request:\n")
	userPrompt.WriteString(strings.TrimSpace(userInput))
	userPrompt.WriteString("\n\nCurrent round:\n")
	userPrompt.WriteString(fmt.Sprintf("%d", round))
	userPrompt.WriteString("\n\nObserved progress so far:\n")
	for _, line := range observations {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		userPrompt.WriteString("- ")
		userPrompt.WriteString(line)
		userPrompt.WriteString("\n")
	}
	userPrompt.WriteString("\nWrite a single progress update for the user.")

	resp, err := a.provider.Chat(ctx, []provider.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt.String()},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}

// chatWithSession 是 Chat/ChatWithSession 的共享实现。
/*
chatWithSession 是 Chat 与 ChatWithSession 共用的内部实现。
*/
func (a *Agent) chatWithSession(ctx context.Context, sess *session.Session, userInput string) (string, error) {
	a.maybeRouteModel(userInput)

	// 优先使用 RunLoop（支持 function calling / 工具调用）
	loopCfg := DefaultLoopConfig()
	if a.cfg != nil {
		cfg := a.cfg.Get()
		ApplyAgentLoopConfig(&loopCfg, cfg.Agent)
	}
	loopCfg.AutoApprove = true // Telegram 场景自动批准工具调用

	result, err := a.RunLoopWithSession(ctx, sess, userInput, loopCfg)
	if err != nil {
		// 如果 RunLoop 失败，回退到简单流式聊天
		response, chatErr := a.chatStreamSimple(ctx, sess, userInput)
		if chatErr != nil {
			return "", fmt.Errorf("runloop: %w; fallback chat: %w", err, chatErr)
		}
		// v0.36.0: 记录指标
		a.metrics.RecordChatRequest()
		return response, nil
	}

	response := result.Response

	// 自动记忆（去重 + 智能分类 + 截断）
	a.chatCount++
	a.saveConversationMemory(userInput, response)

	if a.chatCount%10 == 0 {
		a.memory.Decay(0.05)
		a.memory.Expire()
	}
	if a.chatCount%20 == 0 {
		a.autoSummarize()
	}
	// v0.43.0: 每 50 轮清理过期中期记忆
	if a.chatCount%50 == 0 && a.midTerm != nil {
		expireDays := a.cfg.Get().Memory.MidTermExpireDays
		if expireDays <= 0 {
			expireDays = 90
		}
		a.midTerm.ExpireOldSummaries(time.Duration(expireDays) * 24 * time.Hour)
	}

	// v0.36.0: 记录指标
	a.metrics.RecordChatRequest()
	if len(result.ToolCalls) > 0 {
		for range result.ToolCalls {
			a.metrics.RecordToolCall()
		}
	}

	return response, nil
}

// chatStreamSimple 是不使用工具的简单流式聊天（作为 RunLoop 的回退）。
/*
chatStreamSimple 使用不带工具调用的简单流式聊天作为回退路径。
*/
func (a *Agent) chatStreamSimple(ctx context.Context, sess *session.Session, userInput string) (string, error) {
	a.maybeRouteModel(userInput)
	messages := a.buildContextMessages(ctx, sess, userInput, defaultContextBuildOptions())

	// 调用 Provider
	ch, err := a.provider.ChatStream(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("chat: %w", err)
	}

	var result strings.Builder
	for chunk := range ch {
		result.WriteString(chunk.Content)
		if chunk.Done {
			break
		}
	}

	response := utils.SanitizeToolProtocolOutput(result.String())
	sess.AddMessage("assistant", response)

	// 保存会话
	_ = sess.Save()

	// 自动记忆：将对话存为短期记忆（去重 + 智能分类 + 截断）
	a.chatCount++
	a.saveConversationMemory(userInput, response)

	// 每 10 轮对话触发衰减 + 过期清理
	if a.chatCount%10 == 0 {
		a.memory.Decay(0.05)
		a.memory.Expire()
	}

	// 每 20 轮对话触发自动摘要
	if a.chatCount%20 == 0 {
		a.autoSummarize()
	}

	// v0.43.0: 每 50 轮清理过期中期记忆
	if a.chatCount%50 == 0 && a.midTerm != nil {
		expireDays := a.cfg.Get().Memory.MidTermExpireDays
		if expireDays <= 0 {
			expireDays = 90
		}
		a.midTerm.ExpireOldSummaries(time.Duration(expireDays) * 24 * time.Hour)
	}

	return response, nil
}

// ChatStream 执行流式对话
func (a *Agent) ChatStream(ctx context.Context, userInput string) (<-chan provider.StreamChunk, error) {
	a.maybeRouteModel(userInput)
	sess := a.sessions.New()
	messages := a.buildContextMessages(ctx, sess, userInput, defaultContextBuildOptions())

	return a.provider.ChatStream(ctx, messages)
}

/*
buildContextMessages 为一次请求构造送入模型的完整上下文消息序列。
*/
func (a *Agent) buildContextMessages(ctx context.Context, sess *session.Session, userInput string, opts contextBuildOptions) []provider.Message {
	planner := newContextPlanner(a, opts)
	return planner.Build(ctx, sess, userInput)
}

// ChatEvent 是流式对话事件，包含思考过程和内容
/*
ChatEvent 描述面向上层流式 UI 的聊天事件。
*/
type ChatEvent struct {
	Type    ChatEventType
	Content string
	Name    string // 工具名（Type=EventToolCall 时）
	Args    string // 工具参数
	Result  string // 工具结果
	Err     error
}

// ChatEventType 事件类型
type ChatEventType int

const (
	ChatEventThinking   ChatEventType = iota // 🧠 思考中
	ChatEventToolCall                        // 🔧 工具调用
	ChatEventToolResult                      // 📋 工具结果
	ChatEventContent                         // 📝 内容片段
	ChatEventDone                            // ✅ 完成
	ChatEventError                           // ❌ 错误
)

// StreamMode 流式输出模式
type StreamMode string

const (
	// StreamModeNative 真流式：直接使用 provider 的 ChatStream，逐 chunk 推送
	StreamModeNative StreamMode = "native"
	// StreamModeSimulated 模拟流式：先非流式获取完整响应，再按句子边界逐段推送
	StreamModeSimulated StreamMode = "simulated"
)

// DefaultStreamMode 默认流式模式
const DefaultStreamMode = StreamModeNative

// getStreamMode 获取当前流式模式配置
/*
getStreamMode 返回当前 Agent 使用的流式输出模式。
*/
func (a *Agent) getStreamMode() StreamMode {
	if a.cfg == nil {
		return DefaultStreamMode
	}
	cfg := a.cfg.Get()
	mode := StreamMode(cfg.StreamMode)
	if mode != StreamModeNative && mode != StreamModeSimulated {
		return DefaultStreamMode
	}
	return mode
}

/*
streamConvergenceState 保存流式对话在多轮推理中的收敛与去重状态。
*/
type streamConvergenceState struct {
	emptyResponseRetries     int
	lengthRecoveryCount      int
	continuedResponse        strings.Builder
	toolCallRepeatCount      map[string]int
	toolCallLastResult       map[string]string
	toolURLRepeatCount       map[string]int
	toolURLLastResult        map[string]string
	consecutiveToolOnlyIters int
	successfulSearchEvidence int
	detailedSearchEvidence   int
	forceSearchSynthesis     bool
	repeatToolCallLimit      int
	toolOnlyIterationLimit   int
	duplicateFetchLimit      int
}

/*
hasContinuation 判断当前是否存在待续写的累计回复内容。
*/
func (s *streamConvergenceState) hasContinuation() bool {
	if s == nil {
		return false
	}
	return strings.TrimSpace(s.continuedResponse.String()) != ""
}

/*
toolCallSig 生成工具调用签名，用于重复检测。
*/
func (s *streamConvergenceState) toolCallSig(name, arguments string) string {
	return toolCallSignature(name, arguments)
}

/*
trackToolCallPattern 跟踪工具调用模式，并判断是否进入重复循环。
*/
func (s *streamConvergenceState) trackToolCallPattern(toolCalls []provider.ToolCall, assistantContent string) (bool, []string) {
	if s.toolCallRepeatCount == nil {
		s.toolCallRepeatCount = make(map[string]int)
	}
	if s.repeatToolCallLimit <= 0 {
		s.repeatToolCallLimit = 3
	}
	if s.toolOnlyIterationLimit <= 0 {
		s.toolOnlyIterationLimit = 3
	}
	trimmed := strings.TrimSpace(assistantContent)
	if trimmed == "" {
		s.consecutiveToolOnlyIters++
	} else {
		s.consecutiveToolOnlyIters = 0
	}

	repeatedSigs := make([]string, 0, len(toolCalls))
	allRepeated := true
	for _, tc := range toolCalls {
		sig := s.toolCallSig(tc.Name, tc.Arguments)
		repeatedSigs = append(repeatedSigs, sig)
		s.toolCallRepeatCount[sig]++
		if key := normalizedToolTarget(tc.Name, tc.Arguments); key != "" {
			if s.toolURLRepeatCount == nil {
				s.toolURLRepeatCount = make(map[string]int)
			}
			s.toolURLRepeatCount[key]++
		}
		if s.toolCallRepeatCount[sig] < s.repeatToolCallLimit {
			allRepeated = false
		}
	}

	if (allRepeated && trimmed == "") || s.consecutiveToolOnlyIters >= s.toolOnlyIterationLimit {
		return true, repeatedSigs
	}
	return false, nil
}

/*
rememberToolCallResult 记录一次工具调用的结果，供循环保护和摘要使用。
*/
func (s *streamConvergenceState) rememberToolCallResult(name, arguments, result string) {
	if s.toolCallLastResult == nil {
		s.toolCallLastResult = make(map[string]string)
	}
	s.toolCallLastResult[s.toolCallSig(name, arguments)] = result
	if key := normalizedToolTarget(name, arguments); key != "" {
		if s.toolURLLastResult == nil {
			s.toolURLLastResult = make(map[string]string)
		}
		s.toolURLLastResult[key] = result
	}
}

/*
repeatedToolLoopMessage 构造“重复工具调用已中止”的用户可见提示文本。
*/
func (s *streamConvergenceState) repeatedToolLoopMessage(repeatedSigs []string) string {
	var b strings.Builder
	b.WriteString("Detected repeated tool-call loop and stopped early to avoid timeout.\n")
	b.WriteString("Latest tool outputs:\n")
	seen := make(map[string]struct{}, len(repeatedSigs))
	for _, sig := range repeatedSigs {
		if _, ok := seen[sig]; ok {
			continue
		}
		seen[sig] = struct{}{}
		parts := strings.SplitN(sig, "|", 2)
		name := parts[0]
		out := "(no cached output)"
		if s.toolCallLastResult != nil {
			if v := strings.TrimSpace(s.toolCallLastResult[sig]); v != "" {
				out = v
			}
		}
		if len(out) > 240 {
			out = out[:240] + "...(truncated)"
		}
		b.WriteString(fmt.Sprintf("- %s: %s\n", name, out))
	}
	return strings.TrimSpace(b.String())
}

func (a *Agent) ChatWithSessionStream(ctx context.Context, sessionID string, userInput string) (<-chan ChatEvent, error) {
	sess, ok := a.sessions.Get(sessionID)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	events := make(chan ChatEvent, 64)

	go func() {
		defer close(events)

		messages := a.buildContextMessages(ctx, sess, userInput, defaultContextBuildOptions())

		// 构建 function calling 工具定义
		fcMgr := function.NewManager(a.tools)
		callOpts := provider.CallOptions{
			Tools:      fcMgr.BuildTools(),
			ToolChoice: "auto",
		}

		loopCfg := DefaultLoopConfig()
		cfg := a.cfg.Get()
		ApplyAgentLoopConfig(&loopCfg, cfg.Agent)
		loopCfg.AutoApprove = true
		sanitizeLoopConfig(&loopCfg)
		state := &streamConvergenceState{
			repeatToolCallLimit:    loopCfg.RepeatToolCallLimit,
			toolOnlyIterationLimit: loopCfg.ToolOnlyIterationLimit,
			duplicateFetchLimit:    loopCfg.DuplicateFetchLimit,
		}

		// 🧠 思考阶段（第一轮）
		events <- ChatEvent{Type: ChatEventThinking, Content: "Thinking... (round 1)"}

		mode := a.getStreamMode()
		if mode == StreamModeNative {
			// === 真流式路径 ===
			a.streamNative(ctx, events, messages, callOpts, sess, userInput, 1, loopCfg.MaxIterations, state)
			return
		}

		// === 模拟流式路径 ===
		a.streamSimulated(ctx, events, messages, callOpts, sess, userInput, 1, loopCfg.MaxIterations, state)
	}()

	return events, nil
}

// streamNative 真流式：直接使用 provider 的 ChatStream，逐 chunk 推送
// tool_calls 通过流式增量拼接处理
/*
streamNative 使用 provider 原生流式接口执行一轮或多轮对话。
*/
func (a *Agent) streamNative(ctx context.Context, events chan<- ChatEvent, messages []provider.Message, callOpts provider.CallOptions, sess *session.Session, userInput string, round int, remaining int, state *streamConvergenceState) {
	if state == nil {
		state = &streamConvergenceState{}
	}
	if remaining <= 0 {
		if state.hasContinuation() {
			a.finalizeStream(events, sess, userInput, strings.TrimSpace(state.continuedResponse.String())+lengthTruncatedNotice)
			return
		}
		events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
		return
	}

	// 尝试流式调用
	var ch <-chan provider.StreamChunk
	var err error
	if fcProvider, ok := a.provider.(provider.FunctionCallingProvider); ok && len(callOpts.Tools) > 0 {
		ch, err = fcProvider.ChatStreamWithOptions(ctx, messages, callOpts)
	} else {
		ch, err = a.provider.ChatStream(ctx, messages)
	}
	if err != nil {
		events <- ChatEvent{Type: ChatEventError, Err: err}
		return
	}

	var content strings.Builder
	streamFinishReason := ""
	// 流式 tool_calls 增量拼接
	var toolCallsAcc []streamToolCallAcc // 按 index 累积

	for chunk := range ch {
		if chunk.FinishReason != "" {
			streamFinishReason = chunk.FinishReason
		}
		if chunk.Content != "" {
			content.WriteString(chunk.Content)
			events <- ChatEvent{Type: ChatEventContent, Content: chunk.Content}
		}
		// 处理流式 tool_calls 增量
		if len(chunk.ToolCallDeltas) > 0 {
			for _, dtc := range chunk.ToolCallDeltas {
				// 确保 slice 足够长
				for len(toolCallsAcc) <= dtc.Index {
					toolCallsAcc = append(toolCallsAcc, streamToolCallAcc{})
				}
				acc := &toolCallsAcc[dtc.Index]
				if dtc.ID != "" {
					acc.id = dtc.ID
				}
				if dtc.Name != "" {
					acc.name = dtc.Name
				}
				if dtc.Arguments != "" {
					acc.arguments += dtc.Arguments
				}
			}
		}
		if chunk.Done {
			break
		}
	}

	// 如果有累积的 tool_calls，处理它们
	if len(toolCallsAcc) > 0 {
		state.emptyResponseRetries = 0
		state.lengthRecoveryCount = 0
		toolCalls := make([]provider.ToolCall, 0, len(toolCallsAcc))
		for _, acc := range toolCallsAcc {
			if acc.name != "" {
				// v0.55.1: 如果 ID 为空，生成唯一 call_id
				id := acc.id
				if id == "" {
					id = provider.GenerateCallID()
				}
				toolCalls = append(toolCalls, provider.ToolCall{
					ID:        id,
					Name:      acc.name,
					Arguments: acc.arguments,
				})
			}
		}

		if len(toolCalls) > 0 {
			if shouldStop, repeatedSigs := state.trackToolCallPattern(toolCalls, content.String()); shouldStop {
				a.finalizeStream(events, sess, userInput, state.repeatedToolLoopMessage(repeatedSigs))
				return
			}

			// 将 assistant 消息加入历史
			messages = append(messages, provider.Message{
				Role:      "assistant",
				Content:   content.String(),
				ToolCalls: toolCalls,
			})

			// v0.44.0: 并发执行工具调用（无状态工具并行，有状态工具串行）
			type streamToolResult struct {
				Index       int
				ToolCallID  string
				ToolName    string
				Result      string
				ShortResult string
			}

			resultCh := make(chan streamToolResult, len(toolCalls))

			// 分类：可并发 vs 必须串行
			var parallelIdx []int
			var serialIdx []int
			for i, tc := range toolCalls {
				if a.isToolParallelSafe(tc.Name) {
					parallelIdx = append(parallelIdx, i)
				} else {
					serialIdx = append(serialIdx, i)
				}
			}

			// 先发所有 ToolCall 事件（让用户看到进度）
			for _, tc := range toolCalls {
				shortArgs := tc.Arguments
				if len(shortArgs) > 100 {
					shortArgs = shortArgs[:97] + "..."
				}
				events <- ChatEvent{
					Type:    ChatEventToolCall,
					Name:    tc.Name,
					Args:    shortArgs,
					Content: fmt.Sprintf("🔧 %s", tc.Name),
				}
			}

			// 并发执行无状态工具
			for _, idx := range parallelIdx {
				tc := toolCalls[idx]
				go func(idx int, tc provider.ToolCall) {
					toolResult, err := a.executeToolMaybeDedup(tc.Name, tc.Arguments, true, sess, state.toolURLRepeatCount, state.toolURLLastResult, state.duplicateFetchLimit)
					if err != nil {
						toolResult = fmt.Sprintf("Error: %v", err)
					}
					shortResult := toolResult
					if len(shortResult) > 200 {
						shortResult = shortResult[:197] + "..."
					}
					resultCh <- streamToolResult{
						Index: idx, ToolCallID: tc.ID, ToolName: tc.Name,
						Result: toolResult, ShortResult: shortResult,
					}
				}(idx, tc)
			}

			// 串行执行有状态工具
			for _, idx := range serialIdx {
				tc := toolCalls[idx]
				toolResult, err := a.executeToolMaybeDedup(tc.Name, tc.Arguments, true, sess, state.toolURLRepeatCount, state.toolURLLastResult, state.duplicateFetchLimit)
				if err != nil {
					toolResult = fmt.Sprintf("Error: %v", err)
				}
				shortResult := toolResult
				if len(shortResult) > 200 {
					shortResult = shortResult[:197] + "..."
				}
				resultCh <- streamToolResult{
					Index: idx, ToolCallID: tc.ID, ToolName: tc.Name,
					Result: toolResult, ShortResult: shortResult,
				}
			}

			// 收集结果，按顺序
			allResults := make([]streamToolResult, 0, len(toolCalls))
			for i := 0; i < len(toolCalls); i++ {
				allResults = append(allResults, <-resultCh)
			}
			sort.Slice(allResults, func(i, j int) bool {
				return allResults[i].Index < allResults[j].Index
			})

			for _, r := range allResults {
				events <- ChatEvent{
					Type:    ChatEventToolResult,
					Name:    r.ToolName,
					Result:  r.ShortResult,
					Content: fmt.Sprintf("📋 %s → %s", r.ToolName, r.ShortResult),
				}
				contextResult := compactToolResultForContext(r.ToolName, r.Result)
				if isUsefulSearchEvidence(r.ToolName, r.Result) {
					state.successfulSearchEvidence++
					if r.ToolName == "web_search" {
						if state.detailedSearchEvidence >= 2 {
							contextResult = "[Additional web_search results omitted to save context. Use the earlier search evidence to synthesize the answer.]"
						} else {
							state.detailedSearchEvidence++
						}
					}
				}
				messages = append(messages, provider.Message{
					Role:       "tool",
					Content:    contextResult,
					ToolCallID: r.ToolCallID,
					Name:       r.ToolName,
				})
				if r.Index >= 0 && r.Index < len(toolCalls) {
					state.rememberToolCallResult(r.ToolName, toolCalls[r.Index].Arguments, r.Result)
				}
			}

			// 裁剪上下文，继续下一轮
			messages = a.fitContextWindow(messages)
			if !state.forceSearchSynthesis && shouldForceSearchSynthesis(state.successfulSearchEvidence, state.consecutiveToolOnlyIters) {
				state.forceSearchSynthesis = true
				messages = append(messages, provider.Message{Role: "user", Content: searchSynthesisPrompt})
			}
			if remaining <= 1 {
				if state.hasContinuation() {
					a.finalizeStream(events, sess, userInput, strings.TrimSpace(state.continuedResponse.String())+lengthTruncatedNotice)
					return
				}
				events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
				return
			}
			nextRound := round + 1
			events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}

			// 递归进入下一轮（用非流式，因为 tool_calls 后通常需要完整响应）
			a.streamSimulated(ctx, events, messages, callOpts, sess, userInput, nextRound, remaining-1, state)
			return
		}
	}

	// 没有工具调用，纯文本回复（已在流式中逐 chunk 推送了）
	response := content.String()
	clean := strings.TrimSpace(response)

	// 空回复恢复
	if clean == "" {
		if state.emptyResponseRetries < maxEmptyResponseRetries && remaining > 1 {
			state.emptyResponseRetries++
			messages = append(messages, provider.Message{Role: "assistant", Content: response})
			messages = append(messages, provider.Message{Role: "user", Content: emptyResponseRecoveryPrompt})
			messages = a.fitContextWindow(messages)
			nextRound := round + 1
			events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
			a.streamSimulated(ctx, events, messages, callOpts, sess, userInput, nextRound, remaining-1, state)
			return
		}
		if state.hasContinuation() {
			a.finalizeStream(events, sess, userInput, strings.TrimSpace(state.continuedResponse.String()))
		} else {
			a.finalizeStream(events, sess, userInput, emptyFinalResponseMessage)
		}
		return
	}
	state.emptyResponseRetries = 0

	// 原生流式可携带 finish_reason，遇到 length 时走续写恢复。
	if strings.EqualFold(streamFinishReason, "length") {
		appendContinuation(&state.continuedResponse, response)
		if state.lengthRecoveryCount < maxLengthContinuationRetries && remaining > 1 {
			state.lengthRecoveryCount++
			messages = append(messages, provider.Message{Role: "assistant", Content: response})
			messages = append(messages, provider.Message{Role: "user", Content: lengthRecoveryPrompt})
			messages = a.fitContextWindow(messages)
			nextRound := round + 1
			events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
			a.streamSimulated(ctx, events, messages, callOpts, sess, userInput, nextRound, remaining-1, state)
			return
		}
		partial := strings.TrimSpace(state.continuedResponse.String())
		if partial == "" {
			partial = clean
		}
		a.finalizeStream(events, sess, userInput, partial+lengthTruncatedNotice)
		return
	}
	state.lengthRecoveryCount = 0

	finalResponse := response
	if state.hasContinuation() {
		appendContinuation(&state.continuedResponse, response)
		finalResponse = strings.TrimSpace(state.continuedResponse.String())
	}
	a.finalizeStream(events, sess, userInput, finalResponse)
}

// streamSimulated 模拟流式：先非流式获取完整响应，再按句子边界逐段推送
/*
streamSimulated 先获取完整响应，再按块模拟流式输出。
*/
func (a *Agent) streamSimulated(ctx context.Context, events chan<- ChatEvent, messages []provider.Message, callOpts provider.CallOptions, sess *session.Session, userInput string, round int, remaining int, state *streamConvergenceState) {
	if state == nil {
		state = &streamConvergenceState{}
	}
	if remaining <= 0 {
		if state.hasContinuation() {
			a.finalizeStream(events, sess, userInput, strings.TrimSpace(state.continuedResponse.String())+lengthTruncatedNotice)
			return
		}
		events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
		return
	}

	var resp *provider.Response
	var err error
	iterCallOpts := callOpts
	if state.forceSearchSynthesis {
		iterCallOpts.Tools = nil
		iterCallOpts.ToolChoice = "none"
	}
	if fcProvider, ok := a.provider.(provider.FunctionCallingProvider); ok && len(iterCallOpts.Tools) > 0 {
		resp, err = fcProvider.ChatWithOptions(ctx, messages, iterCallOpts)
	} else {
		resp, err = a.provider.Chat(ctx, messages)
	}
	if err != nil {
		events <- ChatEvent{Type: ChatEventError, Err: err}
		return
	}

	// 有工具调用 → 展示过程 → 执行 → 继续循环
	if len(resp.ToolCalls) > 0 {
		state.emptyResponseRetries = 0
		state.lengthRecoveryCount = 0
		if shouldStop, repeatedSigs := state.trackToolCallPattern(resp.ToolCalls, resp.Content); shouldStop {
			a.finalizeStream(events, sess, userInput, state.repeatedToolLoopMessage(repeatedSigs))
			return
		}
		messages = append(messages, provider.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// v0.44.0: 并发执行工具调用（无状态工具并行，有状态工具串行）
		type simToolResult struct {
			Index       int
			ToolCallID  string
			ToolName    string
			Result      string
			ShortResult string
		}

		simResultCh := make(chan simToolResult, len(resp.ToolCalls))

		// 分类
		var parallelIdx []int
		var serialIdx []int
		for i, tc := range resp.ToolCalls {
			if a.isToolParallelSafe(tc.Name) {
				parallelIdx = append(parallelIdx, i)
			} else {
				serialIdx = append(serialIdx, i)
			}
		}

		// 先发所有 ToolCall 事件
		for _, tc := range resp.ToolCalls {
			shortArgs := tc.Arguments
			if len(shortArgs) > 100 {
				shortArgs = shortArgs[:97] + "..."
			}
			events <- ChatEvent{
				Type:    ChatEventToolCall,
				Name:    tc.Name,
				Args:    shortArgs,
				Content: fmt.Sprintf("🔧 %s", tc.Name),
			}
		}

		// 并发执行无状态工具
		for _, idx := range parallelIdx {
			tc := resp.ToolCalls[idx]
			go func(idx int, tc provider.ToolCall) {
				toolResult, err := a.executeToolMaybeDedup(tc.Name, tc.Arguments, true, sess, state.toolURLRepeatCount, state.toolURLLastResult, state.duplicateFetchLimit)
				if err != nil {
					toolResult = fmt.Sprintf("Error: %v", err)
				}
				shortResult := toolResult
				if len(shortResult) > 200 {
					shortResult = shortResult[:197] + "..."
				}
				simResultCh <- simToolResult{
					Index: idx, ToolCallID: tc.ID, ToolName: tc.Name,
					Result: toolResult, ShortResult: shortResult,
				}
			}(idx, tc)
		}

		// 串行执行有状态工具
		for _, idx := range serialIdx {
			tc := resp.ToolCalls[idx]
			toolResult, err := a.executeToolMaybeDedup(tc.Name, tc.Arguments, true, sess, state.toolURLRepeatCount, state.toolURLLastResult, state.duplicateFetchLimit)
			if err != nil {
				toolResult = fmt.Sprintf("Error: %v", err)
			}
			shortResult := toolResult
			if len(shortResult) > 200 {
				shortResult = shortResult[:197] + "..."
			}
			simResultCh <- simToolResult{
				Index: idx, ToolCallID: tc.ID, ToolName: tc.Name,
				Result: toolResult, ShortResult: shortResult,
			}
		}

		simResults := make([]simToolResult, 0, len(resp.ToolCalls))
		for i := 0; i < len(resp.ToolCalls); i++ {
			simResults = append(simResults, <-simResultCh)
		}
		sort.Slice(simResults, func(i, j int) bool {
			return simResults[i].Index < simResults[j].Index
		})

		for _, r := range simResults {
			events <- ChatEvent{
				Type:    ChatEventToolResult,
				Name:    r.ToolName,
				Result:  r.ShortResult,
				Content: fmt.Sprintf("📋 %s → %s", r.ToolName, r.ShortResult),
			}
			contextResult := compactToolResultForContext(r.ToolName, r.Result)
			if isUsefulSearchEvidence(r.ToolName, r.Result) {
				state.successfulSearchEvidence++
				if r.ToolName == "web_search" {
					if state.detailedSearchEvidence >= 2 {
						contextResult = "[Additional web_search results omitted to save context. Use the earlier search evidence to synthesize the answer.]"
					} else {
						state.detailedSearchEvidence++
					}
				}
			}
			messages = append(messages, provider.Message{
				Role:       "tool",
				Content:    contextResult,
				ToolCallID: r.ToolCallID,
				Name:       r.ToolName,
			})
			if r.Index >= 0 && r.Index < len(resp.ToolCalls) {
				state.rememberToolCallResult(r.ToolName, resp.ToolCalls[r.Index].Arguments, r.Result)
			}
		}

		// 裁剪上下文，递归继续
		messages = a.fitContextWindow(messages)
		if !state.forceSearchSynthesis && shouldForceSearchSynthesis(state.successfulSearchEvidence, state.consecutiveToolOnlyIters) {
			state.forceSearchSynthesis = true
			messages = append(messages, provider.Message{Role: "user", Content: searchSynthesisPrompt})
		}
		if remaining <= 1 {
			if state.hasContinuation() {
				a.finalizeStream(events, sess, userInput, strings.TrimSpace(state.continuedResponse.String())+lengthTruncatedNotice)
				return
			}
			events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
			return
		}
		nextRound := round + 1
		events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
		a.streamSimulated(ctx, events, messages, callOpts, sess, userInput, nextRound, remaining-1, state)
		return
	}

	// 纯文本回复，模拟流式推送
	response := resp.Content
	clean := strings.TrimSpace(response)

	// 空回复恢复
	if clean == "" {
		if state.emptyResponseRetries < maxEmptyResponseRetries && remaining > 1 {
			state.emptyResponseRetries++
			messages = append(messages, provider.Message{Role: "assistant", Content: response})
			messages = append(messages, provider.Message{Role: "user", Content: emptyResponseRecoveryPrompt})
			messages = a.fitContextWindow(messages)
			nextRound := round + 1
			events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
			a.streamSimulated(ctx, events, messages, callOpts, sess, userInput, nextRound, remaining-1, state)
			return
		}
		if state.hasContinuation() {
			a.finalizeStream(events, sess, userInput, strings.TrimSpace(state.continuedResponse.String()))
		} else {
			a.finalizeStream(events, sess, userInput, emptyFinalResponseMessage)
		}
		return
	}
	state.emptyResponseRetries = 0

	// length 续写恢复
	if strings.EqualFold(resp.FinishReason, "length") {
		chunks := splitIntoChunks(response, 60)
		for _, chunk := range chunks {
			events <- ChatEvent{Type: ChatEventContent, Content: chunk}
			time.Sleep(50 * time.Millisecond)
		}
		appendContinuation(&state.continuedResponse, response)
		if state.lengthRecoveryCount < maxLengthContinuationRetries && remaining > 1 {
			state.lengthRecoveryCount++
			messages = append(messages, provider.Message{Role: "assistant", Content: response})
			messages = append(messages, provider.Message{Role: "user", Content: lengthRecoveryPrompt})
			messages = a.fitContextWindow(messages)
			nextRound := round + 1
			events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
			a.streamSimulated(ctx, events, messages, callOpts, sess, userInput, nextRound, remaining-1, state)
			return
		}
		partial := strings.TrimSpace(state.continuedResponse.String())
		if partial == "" {
			partial = clean
		}
		a.finalizeStream(events, sess, userInput, partial+lengthTruncatedNotice)
		return
	}
	state.lengthRecoveryCount = 0

	chunks := splitIntoChunks(response, 60)
	for _, chunk := range chunks {
		events <- ChatEvent{Type: ChatEventContent, Content: chunk}
		time.Sleep(50 * time.Millisecond)
	}

	finalResponse := response
	if state.hasContinuation() {
		appendContinuation(&state.continuedResponse, response)
		finalResponse = strings.TrimSpace(state.continuedResponse.String())
	}
	a.finalizeStream(events, sess, userInput, finalResponse)
}

// finalizeStream 流式对话收尾：保存会话、记忆、RAG 索引
func (a *Agent) finalizeStream(events chan<- ChatEvent, sess *session.Session, userInput, response string) {
	response = utils.SanitizeToolProtocolOutput(response)
	sess.AddMessage("user", userInput)
	sess.AddMessage("assistant", response)
	_ = sess.Save()

	a.chatCount++
	a.saveConversationMemory(userInput, response)
	if a.chatCount%10 == 0 {
		a.memory.Decay(0.05)
		a.memory.Expire()
	}
	if a.chatCount%20 == 0 {
		a.autoSummarize()
	}

	// v0.43.0: 每 50 轮清理过期中期记忆
	if a.chatCount%50 == 0 && a.midTerm != nil {
		expireDays := a.cfg.Get().Memory.MidTermExpireDays
		if expireDays <= 0 {
			expireDays = 90
		}
		a.midTerm.ExpireOldSummaries(time.Duration(expireDays) * 24 * time.Hour)
	}

	if a.ragManager != nil {
		a.indexConversationTurn(userInput, response)
	}

	a.metrics.RecordChatRequest()
	events <- ChatEvent{Type: ChatEventDone, Content: response}
}

// streamToolCallAcc 流式 tool_calls 增量累积器
/*
streamToolCallAcc 用于在原生流式模式下累积单个工具调用的增量字段。
*/
type streamToolCallAcc struct {
	id        string
	name      string
	arguments string
}

// splitIntoChunks 将文本按指定长度分割成块，优先在句子边界分割
func splitIntoChunks(text string, chunkSize int) []string {
	if len(text) <= chunkSize {
		return []string{text}
	}

	var chunks []string
	runes := []rune(text)

	for len(runes) > 0 {
		if len(runes) <= chunkSize {
			chunks = append(chunks, string(runes))
			break
		}

		// 在 chunkSize 附近找句子边界
		splitAt := chunkSize
		for i := chunkSize; i > chunkSize/2 && i < len(runes); i-- {
			r := runes[i]
			if r == '\n' || r == '。' || r == '.' || r == '！' || r == '?' || r == '；' || r == ';' {
				splitAt = i + 1
				break
			}
		}

		chunks = append(chunks, string(runes[:splitAt]))
		runes = runes[splitAt:]
	}

	return chunks
}

// buildMemoryContext 构建分层记忆上下文
func (a *Agent) buildMemoryContext(messages []provider.Message) []provider.Message {
	var memCtx strings.Builder

	// 长期记忆：全部注入（核心身份/偏好）
	longs := a.memory.ByTier(memory.TierLong)
	if len(longs) > 0 {
		memCtx.WriteString("[Core Memory — Long-term]\n")
		for _, e := range longs {
			memCtx.WriteString("- " + e.Content + "\n")
		}
		memCtx.WriteString("\n")
	}

	// v0.43.0: 中期记忆 — 从 MidTermStore 检索相关历史会话摘要
	// 如果当前有用户输入，用它检索；否则取最近 3 条
	if a.midTerm != nil {
		recentSummaries := a.midTerm.ListAll()
		limit := 3
		if len(recentSummaries) < limit {
			limit = len(recentSummaries)
		}
		if limit > 0 {
			memCtx.WriteString("[Session History — Mid-term]\n")
			for i := 0; i < limit; i++ {
				sm := recentSummaries[i]
				memCtx.WriteString("- [" + sm.CreatedAt.Format("2006-01-02") + "] ")
				if len(sm.Topics) > 0 {
					memCtx.WriteString("[" + strings.Join(sm.Topics, ",") + "] ")
				}
				memCtx.WriteString(sm.RawSummary + "\n")
			}
			memCtx.WriteString("\n")
		}
	}

	//  短期记忆 — 使用 ShortTermBuffer 的滑动窗口 + 摘要
	if a.shortTerm != nil {
		shortCtx := a.shortTerm.GetContext()
		if len(shortCtx) > 0 {
			// ShortTermBuffer.GetContext() 已包含摘要 + 最近消息
			// 只注入摘要部分（system role），对话消息由 session 管理
			for _, msg := range shortCtx {
				if msg.Role == "system" {
					memCtx.WriteString("[Recent Conversation Summary — Short-term]\n")
					memCtx.WriteString(msg.Content + "\n\n")
					break
				}
			}
		}
	}

	if memCtx.Len() > 0 {
		messages = append(messages, provider.Message{Role: "system", Content: memCtx.String()})
	}

	return messages
}

// saveConversationMemory 智能保存对话记忆
// - 用户消息：推断分类（preference/project/knowledge/conversation）
// - 助手回复：截断到 150 字，不存完整回复
// - 重要性：根据内容长度和类型动态调整
func (a *Agent) saveConversationMemory(userInput, assistantResponse string) {
	assistantResponse = utils.SanitizeToolProtocolOutput(assistantResponse)
	// v0.43.0: 写入 ShortTermBuffer（滑动窗口 + 摘要压缩）
	if a.shortTerm != nil {
		a.shortTerm.Add("user", userInput)
		a.shortTerm.Add("assistant", utils.Truncate(assistantResponse, 300))
	}

	// 同时写入旧 Store（兼容，用于长期记忆提升）
	userCategory := inferCategory(userInput)
	userImportance := inferImportance(userInput)
	a.memory.SaveWithTier("User: "+utils.Truncate(userInput, 150), userCategory, memory.TierShort, userImportance)

	// 助手回复：只存摘要，不存完整内容
	assistantSummary := utils.Truncate(assistantResponse, 150)
	a.memory.SaveWithTier("Assistant: "+assistantSummary, "conversation", memory.TierShort, 0.2)
}

// inferCategory 从用户输入推断记忆分类
/*
inferCategory 根据用户输入粗略推断记忆分类。
*/
func inferCategory(input string) string {
	lower := strings.ToLower(input)

	// 偏好类
	preferenceKeywords := []string{"喜欢", "偏好", "prefer", "like", "想要", "习惯", "讨厌", "hate", "dislike"}
	for _, kw := range preferenceKeywords {
		if strings.Contains(lower, kw) {
			return "preference"
		}
	}

	// 项目类
	projectKeywords := []string{"项目", "project", "代码", "code", "bug", "部署", "deploy", "仓库", "repo", "pr", "merge"}
	for _, kw := range projectKeywords {
		if strings.Contains(lower, kw) {
			return "project"
		}
	}

	// 知识类
	knowledgeKeywords := []string{"什么是", "怎么", "如何", "为什么", "what is", "how to", "why", "解释", "explain", "调研", "研究"}
	for _, kw := range knowledgeKeywords {
		if strings.Contains(lower, kw) {
			return "knowledge"
		}
	}

	// 身份类
	identityKeywords := []string{"我叫", "我是", "我的名字", "my name", "i am", "住", "学校", "公司"}
	for _, kw := range identityKeywords {
		if strings.Contains(lower, kw) {
			return "identity"
		}
	}

	return "conversation"
}

// inferImportance 根据内容推断重要性
/*
inferImportance 根据输入内容估算记忆的重要性权重。
*/
func inferImportance(input string) float64 {
	lower := strings.ToLower(input)

	// 高重要性关键词
	highKeywords := []string{"重要", "记住", "别忘", "important", "remember", "必须", "密码", "password", "key", "token"}
	for _, kw := range highKeywords {
		if strings.Contains(lower, kw) {
			return 0.7
		}
	}

	// 中等重要性：包含具体信息
	if len(input) > 50 {
		return 0.4
	}

	// 短消息（如"你好"）低重要性
	return 0.2
}

// autoSummarize 自动摘要：将过多的短期记忆压缩为中期
// v0.43.0: 同时生成 SessionSummary 存入 MidTermStore
func (a *Agent) autoSummarize() {
	shorts := a.memory.ByTier(memory.TierShort)
	if len(shorts) <= 5 {
		return // 短期记忆不多，不需要摘要
	}

	// 收集最早的短期记忆（保留最近 5 条）
	var toSummarize []string
	var ids []string
	for i := 0; i < len(shorts)-5; i++ {
		ids = append(ids, shorts[i].ID)
		toSummarize = append(toSummarize, shorts[i].Content)
	}

	if len(ids) == 0 {
		return
	}

	// 简单拼接摘要（v0.4.0: 后续可接入 LLM 生成更智能摘要）
	summary := strings.Join(toSummarize, " | ")
	if len(summary) > 500 {
		summary = summary[:500] + "..."
	}

	a.memory.Summarize(ids, summary, "conversation")

	// v0.43.0: 同时生成 SessionSummary 存入 MidTermStore
	if a.midTerm != nil {
		var turns []memory.ConversationTurn
		for _, s := range shorts {
			turns = append(turns, memory.ConversationTurn{Role: "user", Content: s.Content})
		}
		sessionSummary := memory.GenerateSessionSummary(
			fmt.Sprintf("auto-%d", time.Now().UnixNano()),
			"default",
			turns,
		)
		if err := a.midTerm.SaveSessionSummary(sessionSummary); err != nil {
			fmt.Printf("[agent] warning: failed to save session summary: %v\n", err)
		}
	}
}

// Remember 保存一条中期记忆
func (a *Agent) Remember(content, category string) error {
	return a.memory.Save(content, category)
}

// RememberLongTerm 保存一条长期记忆
func (a *Agent) RememberLongTerm(content, category string) error {
	return a.memory.SaveLongTerm(content, category)
}

// Recall 搜索记忆
func (a *Agent) Recall(query string) []memory.Entry {
	return a.memory.Search(query)
}

// RecallMidTerm 从中期记忆检索相关历史会话摘要
func (a *Agent) RecallMidTerm(query string, topK int) []memory.SessionSummary {
	if a.midTerm == nil {
		return nil
	}
	return a.midTerm.SearchSummaries(query, topK)
}

// MemoryStats 返回记忆统计
func (a *Agent) MemoryStats() map[memory.Tier]int {
	return a.memory.Stats()
}

// DecayMemory 执行记忆衰减
func (a *Agent) DecayMemory(threshold float64) int {
	return a.memory.Decay(threshold)
}

// PromoteMemory 提升记忆层级
func (a *Agent) PromoteMemory(id string) error {
	return a.memory.Promote(id)
}

// ExpireMidTermMemory 过期清理中期记忆
func (a *Agent) ExpireMidTermMemory(olderThan time.Duration) int {
	if a.midTerm == nil {
		return 0
	}
	return a.midTerm.ExpireOldSummaries(olderThan)
}

// handleMemoryTool 处理 LLM 主动调用的记忆工具
func (a *Agent) handleMemoryTool(name, arguments string) (string, error) {
	var args map[string]any
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			args = map[string]any{"raw": arguments}
		}
	}

	switch name {
	case "remember":
		content, _ := args["content"].(string)
		category, _ := args["category"].(string)
		if content == "" {
			return "", fmt.Errorf("content is required")
		}
		if category == "" {
			category = inferCategory(content)
		}
		longTerm, _ := args["long_term"].(bool)
		if longTerm {
			if err := a.memory.SaveLongTerm(content, category); err != nil {
				return "", err
			}
			return fmt.Sprintf("✅ 已保存为长期记忆 [%s]: %s", category, utils.Truncate(content, 80)), nil
		}
		if err := a.memory.Save(content, category); err != nil {
			return "", err
		}
		return fmt.Sprintf("✅ 已保存为中期记忆 [%s]: %s", category, utils.Truncate(content, 80)), nil

	case "recall":
		query, _ := args["query"].(string)
		if query == "" {
			// 无查询词：返回最近记忆
			recent := a.memory.Recent(5)
			if len(recent) == 0 {
				return "没有找到记忆", nil
			}
			var sb strings.Builder
			sb.WriteString("最近的记忆：\n")
			for _, e := range recent {
				sb.WriteString(fmt.Sprintf("- [%s/%s] %s\n", e.Category, e.Tier.String(), utils.Truncate(e.Content, 80)))
			}
			return sb.String(), nil
		}
		results := a.memory.Search(query)
		if len(results) == 0 {
			return fmt.Sprintf("没有找到关于「%s」的记忆", query), nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("找到 %d 条关于「%s」的记忆：\n", len(results), query))
		limit := 10
		if len(results) < limit {
			limit = len(results)
		}
		for i := 0; i < limit; i++ {
			e := results[i]
			sb.WriteString(fmt.Sprintf("- [%s/%s] %s\n", e.Category, e.Tier.String(), utils.Truncate(e.Content, 80)))
		}
		return sb.String(), nil

	default:
		return "", fmt.Errorf("unknown memory tool: %s", name)
	}
}

// handleRAGTool handles LLM-triggered RAG tools.
func (a *Agent) handleRAGTool(name, arguments string) (string, error) {
	if a.ragManager == nil {
		return "", fmt.Errorf("rag manager not initialized")
	}

	var args map[string]any
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			args = map[string]any{"raw": arguments}
		}
	}

	switch name {
	case "rag_search":
		query, _ := args["query"].(string)
		if strings.TrimSpace(query) == "" {
			return "", fmt.Errorf("query is required")
		}

		topK := 5
		if raw, ok := args["top_k"]; ok {
			switch v := raw.(type) {
			case float64:
				if int(v) > 0 {
					topK = int(v)
				}
			case int:
				if v > 0 {
					topK = v
				}
			}
		}

		prev := a.ragManager.RetrieverConfig()
		cfg := prev
		cfg.TopK = topK
		a.ragManager.UpdateRetrieverConfig(cfg)
		defer a.ragManager.UpdateRetrieverConfig(prev)

		results, err := a.ragManager.Search(context.Background(), query)
		if err != nil {
			return "", err
		}
		if len(results) == 0 {
			return fmt.Sprintf("没有找到关于「%s」的 RAG 结果", query), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("找到 %d 条关于「%s」的知识片段：\n", len(results), query))
		for i, r := range results {
			title := strings.TrimSpace(r.DocTitle)
			if title == "" {
				title = strings.TrimSpace(r.DocSource)
			}
			if title == "" {
				title = "(unknown source)"
			}
			content := utils.Truncate(strings.TrimSpace(r.Content), 160)
			sb.WriteString(fmt.Sprintf("%d. [%0.2f] %s — %s\n", i+1, r.Score, title, content))
		}
		return strings.TrimSpace(sb.String()), nil
	}

	return "", fmt.Errorf("unknown rag tool: %s", name)
}

// Soul 返回当前 SOUL
func (a *Agent) Soul() *soul.Soul {
	return a.soul
}

// TemplateManager 返回 SOUL 模板管理器
func (a *Agent) TemplateManager() *soul.TemplateManager {
	return a.tmplMgr
}

// Tools 返回工具注册表
func (a *Agent) Tools() *tool.Registry {
	return a.tools
}

// Catalog 返回模型目录
func (a *Agent) Catalog() *provider.ModelCatalog {
	return a.catalog
}

// Provider 返回当前 provider
func (a *Agent) Provider() provider.Provider {
	return a.provider
}

// Registry 返回 provider 注册表
func (a *Agent) Registry() *provider.Registry {
	return a.registry
}

// SwitchModel 切换模型（通过 catalog 推断 provider）
func (a *Agent) SwitchModel(modelID string) error {
	providerName, err := a.catalog.ResolveProvider(modelID)
	if err != nil {
		return fmt.Errorf("resolve provider for model %s: %w", modelID, err)
	}

	cfg := a.cfg.Get()
	pCfg := provider.Config{
		LlmProvider: provider.LlmProvider{
			Name:    providerName,
			APIKey:  cfg.APIKey,
			BaseURL: cfg.APIBase,
			Model:   modelID,
		},
	}

	p, err := a.registry.Resolve(pCfg)
	if err != nil {
		return fmt.Errorf("create provider %s: %w", providerName, err)
	}

	a.provider = wrapProviderWithMiddleware(p, cfg)
	a.activeModel = modelID
	a.activeAPIBase = cfg.APIBase
	return nil
}

// MCPClient 返回 MCP 客户端
func (a *Agent) MCPClient() *tool.MCPClient {
	return a.mcpClient
}

// Delegate 返回子代理委派管理器
func (a *Agent) Delegate() *tool.DelegateManager {
	return a.delegate
}

// Autonomy 返回自主工作套件 (v0.38.0)
func (a *Agent) Autonomy() *autonomy.AutonomyKit {
	return a.autonomy
}

// StartAutonomy 启动自主工作套件（WorkerPool + HeartbeatEngine）。
// Autonomy 是进程级后台组件，不应绑定到单次请求的取消信号。
func (a *Agent) StartAutonomy(ctx context.Context) error {
	if a.autonomy == nil {
		return fmt.Errorf("autonomy kit not initialized")
	}

	// Create executor adapter that bridges Agent to AgentExecutor interface
	executor := &agentExecutorAdapter{agent: a}
	a.autonomy.SetExecutor(executor)

	if a.autonomy.Status().Started {
		return nil
	}

	if err := a.autonomy.Start(context.Background()); err != nil {
		if strings.Contains(err.Error(), "already started") {
			return nil
		}
		return err
	}

	return nil
}

// agentExecutorAdapter bridges Agent to autonomy.AgentExecutor interface
/*
agentExecutorAdapter 将 Agent 适配为 autonomy 所需的执行器接口。
*/
type agentExecutorAdapter struct {
	agent *Agent
}

/*
RunLoopWithSession 按 autonomy 接口要求在指定会话中执行一轮 Agent Loop。
*/
func (a *agentExecutorAdapter) RunLoopWithSession(ctx context.Context, sessionID string, userInput string, cfg autonomy.LoopConfig) (*autonomy.LoopResult, error) {
	// Look up session by ID
	sess, ok := a.agent.sessions.Get(sessionID)
	if !ok {
		// Fallback: create new session
		sess = a.agent.sessions.NewWithTitle("autonomy-worker")
	}

	loopCfg := LoopConfig{
		MaxIterations: cfg.MaxIterations,
		Timeout:       cfg.Timeout,
		AutoApprove:   cfg.AutoApprove,
	}

	result, err := a.agent.RunLoopWithSession(ctx, sess, userInput, loopCfg)
	if err != nil {
		return nil, err
	}

	return &autonomy.LoopResult{
		Response:   result.Response,
		TokensUsed: result.TokensUsed,
		Iterations: result.Iterations,
	}, nil
}

/*
NewSession 创建 autonomy 使用的新会话并返回其 ID。
*/
func (a *agentExecutorAdapter) NewSession(title string) string {
	sess := a.agent.sessions.NewWithTitle(title)
	return sess.ID
}

// Gateway 返回统一工具网关
func (a *Agent) Gateway() *tool.Gateway {
	return a.gateway
}

// MsgGateway 返回消息平台网关管理器 (v0.6.0)
func (a *Agent) MsgGateway() *gateway.GatewayManager {
	return a.msgGateway
}

// LoadSkills 从目录加载 Skill 插件
func (a *Agent) LoadSkills(skillsDir string) (int, error) {
	loader := tool.NewSkillLoader(skillsDir)
	skills, err := loader.LoadAll()
	if err != nil {
		return 0, fmt.Errorf("load skills: %w", err)
	}

	a.skills = skills
	tool.RegisterSkillTools(a.tools, skills, nil)

	// v0.35.0: 注册 skill_read 工具，让 LLM 能读取 SKILL.md 内容
	a.tools.Register(&tool.Tool{
		Name:        "skill_read",
		Description: "读取指定 skill 的 SKILL.md 内容，了解该 skill 的完整使用方法和步骤。当用户请求涉及某个 skill 的能力时，先调用此工具读取 SKILL.md，再按指引操作。",
		Category:    tool.CatSkill,
		Permission:  tool.PermAuto,
		Enabled:     true,
		Parameters: map[string]tool.Param{
			"name": {
				Type:        "string",
				Description: "Skill 名称（如 web-search, summarize, rewrite 等）",
				Required:    false,
			},
		},
		Handler: a.handleSkillRead(),
	})

	return len(skills), nil
}

// Skills 返回已加载的 skill 列表
func (a *Agent) Skills() []*tool.SkillInfo {
	return a.skills
}

// handleSkillRead 返回 skill_read 工具的 handler
func (a *Agent) handleSkillRead() func(args map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		name, _ := args["name"].(string)
		if name == "" {
			// 没指定名称，返回所有 skill 列表
			var b strings.Builder
			b.WriteString("Available skills:\n")
			for _, s := range a.skills {
				b.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
			}
			return b.String(), nil
		}

		// 查找匹配的 skill
		for _, s := range a.skills {
			if skillMatchesName(s, name) {
				skillFile := filepath.Join(s.Dir, "SKILL.md")
				data, err := os.ReadFile(skillFile)
				if err != nil {
					return "", fmt.Errorf("read SKILL.md for %s: %w", name, err)
				}
				return string(data), nil
			}
		}

		// 模糊匹配
		var candidates []string
		lowerName := strings.ToLower(name)
		for _, s := range a.skills {
			if strings.Contains(strings.ToLower(s.Name), lowerName) {
				candidates = append(candidates, s.Name)
			}
		}
		if len(candidates) > 0 {
			return fmt.Sprintf("Skill '%s' not found. Did you mean: %s?", name, strings.Join(candidates, ", ")), nil
		}

		return fmt.Sprintf("Skill '%s' not found. Use skill_read without name to list all skills.", name), nil
	}
}

/*
skillMatchesName 判断用户提供的名称是否匹配某个技能。
*/
func skillMatchesName(s *tool.SkillInfo, name string) bool {
	if strings.EqualFold(s.Name, name) {
		return true
	}
	target := normalizeSkillLookup(name)
	if target == "" {
		return false
	}
	if normalizeSkillLookup(s.Name) == target {
		return true
	}
	for _, alias := range s.Aliases {
		if strings.EqualFold(alias, name) || normalizeSkillLookup(alias) == target {
			return true
		}
	}
	return false
}

/*
normalizeSkillLookup 将技能名归一化为便于匹配的形式。
*/
func normalizeSkillLookup(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.Join(strings.Fields(name), "-")
	name = strings.Trim(name, "-")
	name = strings.Join(strings.FieldsFunc(name, func(r rune) bool {
		return r == '-'
	}), "-")
	return name
}

// ConnectMCPServer 连接 MCP Server
func (a *Agent) ConnectMCPServer(name, url, apiKey string) {
	a.mcpClient.AddServer(tool.MCPServerConfig{
		Name:   name,
		URL:    url,
		APIKey: apiKey,
	})

	// 注册 MCP 工具
	tool.RegisterMCPTools(a.tools, a.mcpClient)
}

// Sessions 返回会话管理器
func (a *Agent) Sessions() *session.Manager {
	return a.sessions
}

// Config 返回配置管理器
func (a *Agent) Config() *config.Manager {
	return a.cfg
}

// Metrics 返回指标收集器
func (a *Agent) Metrics() *metrics.Metrics {
	return a.metrics
}

// CronEngine 返回定时任务引擎
func (a *Agent) CronEngine() *cron.Engine {
	return a.cronEngine
}

// CronStore 返回 cron 持久化存储
func (a *Agent) CronStore() *cron.Store {
	return a.cronStore
}

// Memory 返回记忆存储
func (a *Agent) Memory() *memory.Store {
	return a.memory
}

// ContextWindow 返回上下文窗口管理器
func (a *Agent) ContextWindow() *contextx.ContextWindow {
	return a.contextWin
}

// FitContext 裁剪消息列表到上下文窗口内
func (a *Agent) FitContext(messages []contextx.Message) ([]contextx.Message, contextx.TrimResult) {
	return a.contextWin.Fit(messages)
}

// ContextStats 返回上下文窗口统计
func (a *Agent) ContextStats(messages []contextx.Message) contextx.ContextStats {
	return a.contextWin.Stats(messages)
}

// RAG 返回 RAG 管理器
func (a *Agent) RAG() *rag.RAGManager {
	return a.ragManager
}

// RAGPersist 返回 RAG 持久化管理器
func (a *Agent) RAGPersist() *rag.Persistence {
	return a.ragPersist
}

// StreamIndexer 返回流式索引器 (v0.23.0)
func (a *Agent) StreamIndexer() *rag.StreamIndexer {
	return a.streamIndexer
}

// EmbedderRegistry 返回嵌入模型注册表
func (a *Agent) EmbedderRegistry() *embedder.Registry {
	return a.embedderReg
}

// AgentRegistry 返回 Agent 协作注册表 (v0.22.0)
func (a *Agent) AgentRegistry() *collab.Registry {
	return a.collabReg
}

// CollabManager 返回协作任务管理器 (v0.22.0)
func (a *Agent) CollabManager() *collab.DelegateManager {
	return a.collabMgr
}

// Close 释放资源，保存持久化数据
func (a *Agent) Close() error {
	var firstErr error

	if a.autonomy != nil && a.autonomy.Status().Started {
		if err := a.autonomy.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("stop autonomy: %w", err)
		}
	}
	if a.cronEngine != nil {
		a.cronEngine.Stop()
	}
	if a.heartbeatSvc != nil {
		if err := a.heartbeatSvc.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("stop heartbeat: %w", err)
		}
	}

	// SQLite 后端自动持久化，只需关闭连接
	if s := a.ragManager.SQLiteStore(); s != nil {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close sqlite store: %w", err)
		}
	} else if a.ragPersist != nil && a.ragManager != nil {
		// 内存后端：关闭时保存到 JSON
		if err := a.ragPersist.Save(a.ragManager); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("save RAG index: %w", err)
		}
	}

	return firstErr
}

// buildRAGContext 构建 RAG 检索上下文
func (a *Agent) buildRAGContext(ctx context.Context, messages []provider.Message, query string) []provider.Message {
	if a.ragManager == nil {
		return messages
	}

	stats := a.ragManager.Stats()
	if stats.DocumentCount == 0 {
		return messages // 没有索引文档，跳过 RAG
	}

	ragCtx, _, err := a.ragManager.SearchWithContext(ctx, query)
	if err != nil || ragCtx == "" {
		return messages
	}

	return append(messages, provider.Message{Role: "system", Content: ragCtx})
}

// unused suppress
var _ = time.Second

// toContextMessages 将 provider.Message 转换为 contextx.Message
/*
toContextMessages 将 provider 消息转换为带优先级的上下文消息。
*/
func (a *Agent) toContextMessages(messages []provider.Message) []contextx.Message {
	result := make([]contextx.Message, len(messages))
	for i, msg := range messages {
		priority := contextx.PriorityNormal
		category := msg.Role

		// system 消息是 critical
		if msg.Role == "system" {
			priority = contextx.PriorityCritical
			category = "system"
		}

		// 记忆上下文按层级分配优先级
		if msg.Role == "system" && len(msg.Content) > 0 {
			switch {
			case strings.HasPrefix(msg.Content, "[Core Memory"):
				priority = contextx.PriorityHigh
				category = "memory_long"
			case strings.HasPrefix(msg.Content, "[Working Memory"):
				priority = contextx.PriorityNormal
				category = "memory_medium"
			case strings.HasPrefix(msg.Content, "[Recent Context"):
				priority = contextx.PriorityLow
				category = "memory_short"
			case strings.HasPrefix(msg.Content, "[Session History"):
				priority = contextx.PriorityNormal
				category = "memory_mid"
			case strings.HasPrefix(msg.Content, "[Conversation Summary"), strings.HasPrefix(msg.Content, "[Conversation Themes"):
				priority = contextx.PriorityLow
				category = "conversation_summary"
			case strings.HasPrefix(msg.Content, "## Retrieved Knowledge"), strings.HasPrefix(msg.Content, "[Retrieved Knowledge"):
				priority = contextx.PriorityHigh
				category = "rag"
			}
		}
		if msg.Role == "tool" {
			priority = contextx.PriorityNormal
			category = "tool_result"
		}

		result[i] = contextx.Message{
			Role:      msg.Role,
			Content:   msg.Content,
			Priority:  priority,
			Category:  category,
			Timestamp: time.Now(),
		}
	}
	return result
}

// fromContextMessages 将 contextx.Message 转换回 provider.Message
/*
fromContextMessages 将上下文消息转换回 provider 消息格式。
*/
func (a *Agent) fromContextMessages(messages []contextx.Message) []provider.Message {
	result := make([]provider.Message, len(messages))
	for i, msg := range messages {
		result[i] = provider.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}
	return result
}

// applyWebSearchEnv 从环境变量覆盖 web_search 配置
/*
applyWebSearchEnv 使用环境变量补全 web_search 相关配置。
*/
func applyWebSearchEnv(cfg *config.Manager) {
	cur := cfg.Get()
	provider := strings.ToLower(strings.TrimSpace(cur.WebSearch.Provider))

	// 配置文件优先：仅在 config.json 对应字段为空时，才用环境变量补全。
	if cur.WebSearch.Provider == "" {
		if v := os.Getenv("LH_WEB_SEARCH_PROVIDER"); v != "" {
			_ = cfg.Set("web_search.provider", v)
			provider = strings.ToLower(strings.TrimSpace(v))
		}
	}
	if cur.WebSearch.APIKey == "" {
		if v := os.Getenv("LH_WEB_SEARCH_API_KEY"); v != "" {
			_ = cfg.Set("web_search.api_key", v)
		} else if provider == "exa" {
			if v := os.Getenv("LH_SEARCH_EXA_KEY"); v != "" {
				_ = cfg.Set("web_search.api_key", v)
			} else if v := os.Getenv("EXA_API_KEY"); v != "" {
				_ = cfg.Set("web_search.api_key", v)
			}
		} else if v := os.Getenv("BRAVE_API_KEY"); v != "" {
			_ = cfg.Set("web_search.api_key", v)
		}
	}
	if cur.WebSearch.BaseURL == "" {
		if v := os.Getenv("LH_WEB_SEARCH_BASE_URL"); v != "" {
			_ = cfg.Set("web_search.base_url", v)
		} else if v := os.Getenv("SEARXNG_BASE_URL"); v != "" {
			_ = cfg.Set("web_search.base_url", v)
		}
	}
	if cur.WebSearch.MaxResults <= 0 {
		if v := os.Getenv("LH_WEB_SEARCH_MAX_RESULTS"); v != "" {
			_ = cfg.Set("web_search.max_results", v)
		}
	}
	if cur.WebSearch.Proxy == "" {
		if v := os.Getenv("LH_WEB_SEARCH_PROXY"); v != "" {
			_ = cfg.Set("web_search.proxy", v)
		}
	}
}
