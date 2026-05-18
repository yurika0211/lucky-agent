package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	appheartbeat "github.com/yurika0211/luckyharness/internal/agent/heartbeat"
	"github.com/yurika0211/luckyharness/internal/autonomy"
	"github.com/yurika0211/luckyharness/internal/collab"
	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/contextx"
	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/embedder"
	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/logger"
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

type soulRuntime struct {
	soul    *soul.Soul
	tmplMgr *soul.TemplateManager
}

type providerRuntime struct {
	provider   provider.Provider
	registry   *provider.Registry
	catalog    *provider.ModelCatalog
	tokenStore *provider.TokenStore
}

type memoryRuntime struct {
	store    *memory.Store
	short    *memory.ShortTermBuffer
	mid      *memory.MidTermStore
	sessions *session.Manager
}

type ragRuntime struct {
	manager       *rag.RAGManager
	persist       *rag.Persistence
	streamIndexer *rag.StreamIndexer
	embedderReg   *embedder.Registry
}

type supportRuntime struct {
	tools          *tool.Registry
	toolGateway    *tool.Gateway
	searchCfg      *tool.WebSearchConfig
	toolServices   *tool.Services
	delegateMgr    *tool.DelegateManager
	mcpClient      *tool.MCPClient
	contextWin     *contextx.ContextWindow
	contextEst     *contextx.TokenEstimator
	metrics        *metrics.Metrics
	mediaProcessor *multimodal.Processor
	cronEngine     *cron.Engine
	autonomyKit    *autonomy.AutonomyKit
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
	skillRegistry      *tool.SkillRegistry
	metrics            *metrics.Metrics // 指标收集器
	cronEngine         *cron.Engine     // 定时任务引擎
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
	cfg := embedderRuntimeConfig{}
	if c != nil {
		cfg = embedderRuntimeConfig{
			APIKey:    strings.TrimSpace(c.Embedding.APIKey),
			Model:     strings.TrimSpace(c.Embedding.Model),
			BaseURL:   strings.TrimSpace(c.Embedding.APIBase),
			Dimension: c.Embedding.Dimension,
		}
	}

	if v := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL_KEY")); v != "" {
		cfg.APIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL_NAME")); v != "" {
		cfg.Model = v
	}
	if v := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL_URL")); v != "" {
		cfg.BaseURL = v
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

func initSoulRuntime(c *config.Config) soulRuntime {
	var loadedSoul *soul.Soul
	if c != nil && strings.TrimSpace(c.SoulPath) != "" {
		if loaded, err := soul.Load(c.SoulPath); err == nil {
			loadedSoul = loaded
		}
	}
	if loadedSoul == nil {
		loadedSoul = soul.Default()
	}
	return soulRuntime{
		soul:    loadedSoul,
		tmplMgr: soul.NewTemplateManager(),
	}
}

func initProviderRuntime(cfg *config.Manager, c *config.Config) (providerRuntime, error) {
	registry := provider.NewRegistry()
	catalog := provider.NewModelCatalog()
	tokenStore, err := provider.NewTokenStore(cfg.HomeDir() + "/tokens")
	if err != nil {
		tokenStore = nil
	}

	var p provider.Provider
	if len(c.Fallbacks) > 0 {
		fallbackConfigs := make([]provider.FallbackConfig, 0, len(c.Fallbacks)+1)
		fallbackConfigs = append(fallbackConfigs, provider.FallbackConfig{
			Name:    c.Provider,
			APIKey:  c.APIKey,
			APIBase: c.APIBase,
			Model:   c.Model,
		})
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
			return providerRuntime{}, fmt.Errorf("create fallback chain: %w", err)
		}
		p = chain
	} else {
		pCfg := toProviderConfig(c, "", "")
		p, err = registry.Resolve(pCfg)
		if err != nil {
			return providerRuntime{}, fmt.Errorf("resolve provider: %w", err)
		}
	}
	p = wrapProviderWithMiddleware(p, c)

	return providerRuntime{
		provider:   p,
		registry:   registry,
		catalog:    catalog,
		tokenStore: tokenStore,
	}, nil
}

func initMemoryRuntime(cfg *config.Manager, c *config.Config) (memoryRuntime, error) {
	mem, err := memory.NewStore(cfg.HomeDir() + "/memory")
	if err != nil {
		return memoryRuntime{}, fmt.Errorf("init memory: %w", err)
	}

	shortTermMaxTurns := c.Memory.ShortTermMaxTurns
	if shortTermMaxTurns <= 0 {
		shortTermMaxTurns = 10
	}
	shortTerm := memory.NewShortTermBuffer(shortTermMaxTurns)

	midTermMaxSummaries := c.Memory.MidTermMaxSummaries
	if midTermMaxSummaries <= 0 {
		midTermMaxSummaries = 100
	}
	midTerm, err := memory.NewMidTermStore(filepath.Join(cfg.HomeDir(), "memory", "30_Sessions"), midTermMaxSummaries)
	if err != nil {
		return memoryRuntime{}, fmt.Errorf("init midterm store: %w", err)
	}

	sessions, err := session.NewManager(cfg.HomeDir() + "/sessions")
	if err != nil {
		return memoryRuntime{}, fmt.Errorf("init sessions: %w", err)
	}

	return memoryRuntime{
		store:    mem,
		short:    shortTerm,
		mid:      midTerm,
		sessions: sessions,
	}, nil
}

func initRAGRuntime(cfg *config.Manager, c *config.Config) (ragRuntime, error) {
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

	activeEmb := embedder.NewCachedEmbedder(embedderReg.Active(), 512)
	logger.Info("rag embedder selected",
		"embedder_name", activeEmb.Name(),
		"embedder_model", activeEmb.Model(),
		"embedder_dim", activeEmb.Dimension(),
	)
	ragConfig := rag.DefaultRAGConfig()

	var ragManager *rag.RAGManager
	var ragPersist *rag.Persistence

	ragDBPath := cfg.HomeDir() + "/rag/luckyharness.db"
	ragMgr, err := rag.NewRAGManagerWithSQLite(activeEmb, ragConfig, ragDBPath)
	if err != nil {
		ragManager = rag.NewRAGManager(activeEmb, ragConfig)
		ragPersist = rag.NewPersistence(cfg.HomeDir() + "/rag")
		if ragPersist.Exists() {
			if docCount, loadErr := ragPersist.Load(ragManager); loadErr == nil && docCount > 0 {
				_ = docCount
			}
		}
	} else {
		ragManager = ragMgr
		ragPersist = rag.NewPersistence(cfg.HomeDir() + "/rag")
		if ragPersist.Exists() {
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

	return ragRuntime{
		manager:       ragManager,
		persist:       ragPersist,
		streamIndexer: rag.NewStreamIndexer(ragManager, rag.DefaultStreamConfig()),
		embedderReg:   embedderReg,
	}, nil
}

func initSupportRuntime(c *config.Config, mem *memory.Store, ragMgr *rag.RAGManager) supportRuntime {
	tools := tool.NewRegistry()
	searchCfg := &tool.WebSearchConfig{
		Provider:   c.WebSearch.Provider,
		APIKey:     c.WebSearch.APIKey,
		BaseURL:    c.WebSearch.BaseURL,
		MaxResults: c.WebSearch.MaxResults,
		Proxy:      c.WebSearch.Proxy,
	}
	mediaProcessor := multimodal.NewProcessor()
	var imageGenerator multimodal.ImageGenerator
	var speechSynthesizer multimodal.SpeechSynthesizer
	_ = mediaProcessor.RegisterProvider(multimodal.NewLocalProvider(
		multimodal.ModalityText,
		multimodal.ModalityImage,
		multimodal.ModalityAudio,
		multimodal.ModalityVideo,
		multimodal.ModalityDocument,
	), true)

	mmCfg, mmOK := resolveOpenAIMultimodalConfig(c)
	if mmOK {
		if openaiMedia, mediaErr := multimodal.NewOpenAIMediaProvider(multimodal.OpenAIMediaConfig{
			APIKey:             mmCfg.APIKey,
			APIBase:            mmCfg.APIBase,
			ResponsesModel:     mmCfg.ImageModel,
			TranscriptionModel: mmCfg.TranscriptionModel,
		}); mediaErr == nil {
			_ = mediaProcessor.RegisterProvider(openaiMedia, true)
			imageGenerator = openaiMedia
		}
	}

	if genCfg, ok := resolveImageGenerationConfig(c); ok {
		switch genCfg.Provider {
		case "gemini":
			if geminiGenerator, err := multimodal.NewGeminiImageProvider(multimodal.GeminiImageConfig{
				APIKey:   genCfg.APIKey,
				APIBase:  genCfg.APIBase,
				AuthMode: genCfg.AuthMode,
			}); err == nil {
				imageGenerator = geminiGenerator
			}
		case "openai":
			if openaiGenerator, err := multimodal.NewOpenAIMediaProvider(multimodal.OpenAIMediaConfig{
				APIKey:             genCfg.APIKey,
				APIBase:            genCfg.APIBase,
				ResponsesModel:     mmCfg.ImageModel,
				TranscriptionModel: mmCfg.TranscriptionModel,
			}); err == nil {
				imageGenerator = openaiGenerator
			}
		}
	}

	if ttsCfg, ok := resolveTTSConfig(c); ok {
		switch ttsCfg.Provider {
		case "openai":
			if ttsProvider, err := multimodal.NewOpenAITTSProvider(multimodal.OpenAITTSConfig{
				APIKey:   ttsCfg.APIKey,
				APIBase:  ttsCfg.APIBase,
				AuthMode: ttsCfg.AuthMode,
			}); err == nil {
				speechSynthesizer = ttsProvider
			}
		}
	}

	delegateMgr := tool.NewDelegateManager(tool.DefaultDelegateConfig())
	imageGenDefaults := tool.ImageGenerationDefaults{
		Model:             strings.TrimSpace(c.ImageGeneration.Model),
		Size:              strings.TrimSpace(c.ImageGeneration.Size),
		Quality:           strings.TrimSpace(c.ImageGeneration.Quality),
		Background:        strings.TrimSpace(c.ImageGeneration.Background),
		OutputFormat:      strings.TrimSpace(c.ImageGeneration.OutputFormat),
		OutputCompression: c.ImageGeneration.OutputCompression,
		Count:             c.ImageGeneration.Count,
	}
	ttsDefaults := tool.TTSDefaults{
		Model:  strings.TrimSpace(c.TTS.Model),
		Voice:  strings.TrimSpace(c.TTS.Voice),
		Format: strings.TrimSpace(c.TTS.Format),
		Speed:  c.TTS.Speed,
	}
	toolServices := tool.NewServices(searchCfg, c.Multimodal.ImageProvider, mediaProcessor, imageGenerator, imageGenDefaults, speechSynthesizer, ttsDefaults, mem, ragMgr, delegateMgr)

	contextWin := contextx.NewContextWindow(contextx.WindowConfig{
		MaxTokens:            c.MaxTokens,
		ReservedTokens:       c.MaxTokens / 4,
		Strategy:             contextx.TrimLowPriority,
		SlidingWindowSize:    10,
		MaxConversationTurns: 50,
		MemoryBudget:         800,
		SummarizeThreshold:   0.8,
	})

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

	return supportRuntime{
		tools:          tools,
		toolGateway:    tool.NewGateway(tools),
		searchCfg:      searchCfg,
		toolServices:   toolServices,
		delegateMgr:    delegateMgr,
		mcpClient:      tool.NewMCPClient(),
		contextWin:     contextWin,
		contextEst:     contextx.NewTokenEstimator(c.MaxTokens),
		metrics:        metrics.NewMetrics(),
		mediaProcessor: mediaProcessor,
		cronEngine:     cronEngine,
		autonomyKit:    autonomy.NewAutonomyKit(autonomy.DefaultAutonomyConfig(), nil),
	}
}

type multimodalRuntimeConfig struct {
	APIKey             string
	APIBase            string
	ImageModel         string
	TranscriptionModel string
}

type imageGenerationRuntimeConfig struct {
	Provider          string
	APIKey            string
	APIBase           string
	AuthMode          string
	Model             string
	Size              string
	Quality           string
	Background        string
	OutputFormat      string
	OutputCompression int
	Count             int
}

type ttsRuntimeConfig struct {
	Provider string
	APIKey   string
	APIBase  string
	AuthMode string
	Model    string
	Voice    string
	Format   string
	Speed    float64
}

func resolveOpenAIMultimodalConfig(c *config.Config) (multimodalRuntimeConfig, bool) {
	if c == nil {
		return multimodalRuntimeConfig{}, false
	}

	cfg := multimodalRuntimeConfig{
		APIKey:             strings.TrimSpace(c.Multimodal.APIKey),
		APIBase:            strings.TrimSpace(c.Multimodal.APIBase),
		ImageModel:         strings.TrimSpace(c.Multimodal.ImageModel),
		TranscriptionModel: strings.TrimSpace(c.Multimodal.TranscriptionModel),
	}

	providerName := strings.ToLower(strings.TrimSpace(c.Multimodal.Provider))
	if providerName == "" {
		providerName = strings.ToLower(strings.TrimSpace(c.Provider))
	}

	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(c.APIKey)
	}
	if cfg.APIBase == "" {
		cfg.APIBase = strings.TrimSpace(c.APIBase)
	}

	explicitMultimodalConfig := strings.TrimSpace(c.Multimodal.APIKey) != "" ||
		strings.TrimSpace(c.Multimodal.APIBase) != "" ||
		strings.TrimSpace(c.Multimodal.ImageModel) != "" ||
		strings.TrimSpace(c.Multimodal.TranscriptionModel) != "" ||
		strings.TrimSpace(c.Multimodal.Provider) != ""

	if providerName == "openai" || explicitMultimodalConfig {
		if cfg.APIKey != "" {
			return cfg, true
		}
		return multimodalRuntimeConfig{}, false
	}

	// Backward-compatible fallback for the old implicit OpenAI-only behavior.
	if cfg.APIKey != "" && (strings.EqualFold(c.Provider, "openai") || strings.Contains(strings.ToLower(c.APIBase), "openai.com")) {
		return cfg, true
	}

	return multimodalRuntimeConfig{}, false
}

func resolveImageGenerationConfig(c *config.Config) (imageGenerationRuntimeConfig, bool) {
	if c == nil {
		return imageGenerationRuntimeConfig{}, false
	}

	cfg := imageGenerationRuntimeConfig{
		Provider:          strings.ToLower(strings.TrimSpace(c.ImageGeneration.Provider)),
		APIKey:            strings.TrimSpace(c.ImageGeneration.APIKey),
		APIBase:           strings.TrimSpace(c.ImageGeneration.APIBase),
		AuthMode:          strings.ToLower(strings.TrimSpace(c.ImageGeneration.AuthMode)),
		Model:             strings.TrimSpace(c.ImageGeneration.Model),
		Size:              strings.TrimSpace(c.ImageGeneration.Size),
		Quality:           strings.TrimSpace(c.ImageGeneration.Quality),
		Background:        strings.TrimSpace(c.ImageGeneration.Background),
		OutputFormat:      strings.TrimSpace(c.ImageGeneration.OutputFormat),
		OutputCompression: c.ImageGeneration.OutputCompression,
		Count:             c.ImageGeneration.Count,
	}
	if cfg.Model == "" {
		cfg.Model = strings.TrimSpace(c.Multimodal.GenerationModel)
	}
	if cfg.Size == "" {
		cfg.Size = strings.TrimSpace(c.Multimodal.GenerationSize)
	}
	if cfg.Quality == "" {
		cfg.Quality = strings.TrimSpace(c.Multimodal.GenerationQuality)
	}
	if cfg.Background == "" {
		cfg.Background = strings.TrimSpace(c.Multimodal.GenerationBackground)
	}
	if cfg.OutputFormat == "" {
		cfg.OutputFormat = strings.TrimSpace(c.Multimodal.GenerationOutputFormat)
	}
	if cfg.Provider == "" {
		cfg.Provider = "openai"
	}
	if cfg.Provider == "openai" && looksLikeGeminiImageModel(cfg.Model) {
		cfg.Provider = "gemini"
	}
	if cfg.AuthMode == "" {
		cfg.AuthMode = "bearer"
	}

	if cfg.Provider == "openai" {
		if cfg.APIKey == "" {
			cfg.APIKey = strings.TrimSpace(c.Multimodal.APIKey)
			if cfg.APIKey == "" {
				cfg.APIKey = strings.TrimSpace(c.APIKey)
			}
		}
		if cfg.APIBase == "" {
			cfg.APIBase = strings.TrimSpace(c.Multimodal.APIBase)
			if cfg.APIBase == "" {
				cfg.APIBase = strings.TrimSpace(c.APIBase)
			}
		}
		if cfg.APIBase == "https://api.openai.com/v1" && strings.TrimSpace(c.Multimodal.APIBase) != "" {
			cfg.APIBase = strings.TrimSpace(c.Multimodal.APIBase)
		}
	}

	if cfg.Provider == "gemini" {
		if cfg.APIKey == "" {
			cfg.APIKey = strings.TrimSpace(c.Multimodal.APIKey)
			if cfg.APIKey == "" {
				cfg.APIKey = strings.TrimSpace(c.APIKey)
			}
		}
		if cfg.APIBase == "" || cfg.APIBase == "https://api.openai.com/v1" {
			cfg.APIBase = strings.TrimSpace(c.Multimodal.APIBase)
			if cfg.APIBase == "" {
				cfg.APIBase = "https://generativelanguage.googleapis.com/v1beta"
			}
		}
	}

	if cfg.APIKey == "" || cfg.APIBase == "" {
		return imageGenerationRuntimeConfig{}, false
	}
	return cfg, true
}

func looksLikeGeminiImageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gemini") || strings.HasPrefix(model, "google/gemini")
}

func resolveTTSConfig(c *config.Config) (ttsRuntimeConfig, bool) {
	if c == nil {
		return ttsRuntimeConfig{}, false
	}
	cfg := ttsRuntimeConfig{
		Provider: strings.ToLower(strings.TrimSpace(c.TTS.Provider)),
		APIKey:   strings.TrimSpace(c.TTS.APIKey),
		APIBase:  strings.TrimSpace(c.TTS.APIBase),
		AuthMode: strings.ToLower(strings.TrimSpace(c.TTS.AuthMode)),
		Model:    strings.TrimSpace(c.TTS.Model),
		Voice:    strings.TrimSpace(c.TTS.Voice),
		Format:   strings.TrimSpace(c.TTS.Format),
		Speed:    c.TTS.Speed,
	}
	if cfg.Provider == "" {
		cfg.Provider = "openai"
	}
	if cfg.AuthMode == "" {
		cfg.AuthMode = "bearer"
	}
	if cfg.Provider == "openai" {
		if cfg.APIKey == "" {
			cfg.APIKey = strings.TrimSpace(c.APIKey)
		}
		if cfg.APIBase == "" {
			cfg.APIBase = strings.TrimSpace(c.APIBase)
		}
	}
	if cfg.APIKey == "" || cfg.APIBase == "" {
		return ttsRuntimeConfig{}, false
	}
	return cfg, true
}

// New 创建 Agent
func New(cfg *config.Manager) (*Agent, error) {
	applyWebSearchEnv(cfg)
	c := cfg.Get()
	soulRT := initSoulRuntime(c)
	providerRT, err := initProviderRuntime(cfg, c)
	if err != nil {
		return nil, err
	}
	memoryRT, err := initMemoryRuntime(cfg, c)
	if err != nil {
		return nil, err
	}
	ragRT, err := initRAGRuntime(cfg, c)
	if err != nil {
		return nil, err
	}
	supportRT := initSupportRuntime(c, memoryRT.store, ragRT.manager)

	a := &Agent{
		cfg:            cfg,
		soul:           soulRT.soul,
		tmplMgr:        soulRT.tmplMgr,
		provider:       providerRT.provider,
		registry:       providerRT.registry,
		catalog:        providerRT.catalog,
		tokenStore:     providerRT.tokenStore,
		memory:         memoryRT.store,
		shortTerm:      memoryRT.short,
		midTerm:        memoryRT.mid,
		sessions:       memoryRT.sessions,
		tools:          supportRT.tools,
		gateway:        supportRT.toolGateway,
		msgGateway:     gateway.NewGatewayManager(),
		mcpClient:      supportRT.mcpClient,
		delegate:       supportRT.delegateMgr,
		contextWin:     supportRT.contextWin,
		contextEst:     supportRT.contextEst,
		ragManager:     ragRT.manager,
		ragPersist:     ragRT.persist,
		streamIndexer:  ragRT.streamIndexer,
		embedderReg:    ragRT.embedderReg,
		collabReg:      collab.NewRegistry(),
		collabMgr:      nil,
		metrics:        supportRT.metrics,
		cronEngine:     supportRT.cronEngine,
		cronStore:      cron.NewStore(filepath.Join(cfg.HomeDir(), "mission.md")),
		autonomy:       supportRT.autonomyKit,
		contextCache:   newContextMessageCache(64),
		mediaProcessor: supportRT.mediaProcessor,
		activeModel:    c.Model,
		activeAPIBase:  c.APIBase,
	}

	a.collabReg.Register(&collab.AgentProfile{
		ID:           "local-agent",
		Name:         "Local Agent",
		Description:  "The primary local agent",
		Capabilities: []string{"chat", "code", "analysis", "research"},
		Status:       collab.StatusOnline,
	})
	a.collabMgr = collab.NewDelegateManager(a.collabReg, nil)

	supportRT.toolServices.Cron = tool.NewCronToolService(
		supportRT.cronEngine,
		a.saveCronJobs,
		func(id, mode, command string, metadata map[string]string) func() error {
			return a.buildCronTask(id, cronTaskMode(mode), command, metadata)
		},
	)
	supportRT.toolServices.Autonomy = tool.NewAutonomyToolService(supportRT.autonomyKit)
	supportRT.toolServices.Heartbeat = tool.NewHeartbeatToolService(a.handleHeartbeatTrigger, a.handleHeartbeatStatus)
	supportRT.toolServices.RegisterCoreTools(supportRT.tools)

	// v0.35.0: 自动加载 skills 目录
	skillsDir := cfg.HomeDir() + "/skills"
	if info, err := os.Stat(skillsDir); err == nil && info.IsDir() {
		if count, err := a.LoadSkills(skillsDir); err == nil && count > 0 {
			fmt.Printf("[agent] loaded %d skills from %s\n", count, skillsDir)
		}
	}

	// v0.36.0: 启动定时任务引擎
	supportRT.cronEngine.Start()
	if restored, restoreErr := a.restoreCronJobs(); restoreErr != nil {
		fmt.Printf("[cron] restore failed: %v\n", restoreErr)
	} else if restored > 0 {
		fmt.Printf("[cron] restored %d jobs\n", restored)
	}
	if err := a.initHeartbeatService(); err != nil {
		fmt.Printf("[heartbeat] init failed: %v\n", err)
	}

	// v0.38.0: 设置 delegate 的 Agent 执行器，让 delegate_task 真正走 Agent Loop
	supportRT.delegateMgr.SetAgentExecutor(func(ctx context.Context, description, contextStr string) (string, error) {
		sess := memoryRT.sessions.NewWithTitle("delegate-task")
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
	return a.chatWithSessionInput(ctx, sess, TextUserTurnInput(userInput))
}

// ChatWithSession 在已有会话中继续对话，实现多轮上下文。
func (a *Agent) ChatWithSession(ctx context.Context, sessionID string, userInput string) (string, error) {
	sess, ok := a.sessions.Get(sessionID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}
	return a.chatWithSessionInput(ctx, sess, TextUserTurnInput(userInput))
}

// ChatWithSessionInput 在已有会话中继续对话，支持结构化多模态输入。
func (a *Agent) ChatWithSessionInput(ctx context.Context, sessionID string, input UserTurnInput) (string, error) {
	sess, ok := a.sessions.Get(sessionID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}
	return a.chatWithSessionInput(ctx, sess, input)
}

// ProgressFeedback generates a concise model-authored progress update for an unfinished round.
func (a *Agent) ProgressFeedback(ctx context.Context, userInput string, round int, observations []string) (string, error) {
	if a == nil || a.provider == nil {
		return "", fmt.Errorf("provider not initialized")
	}
	if len(observations) == 0 {
		return "", nil
	}

	systemPrompt := `You are generating one concise reasoning update for the user during an unfinished task.

Report real progress only. Stay close to the observed evidence.

Write in English.

The update should sound like a human investigator thinking aloud in a compact way.
It should read like a short natural reasoning paragraph, not like a checklist, template, or repeated report.

If previous user-facing updates are provided, treat them as messages that the user has already seen. Continue naturally from them instead of restarting the narration from scratch.
Prioritize what changed since the previous update.

What to include when relevant:
- what you have checked,
- what that currently suggests,
- what you are verifying now and why,
- what is still uncertain,
- what likely matters next.

Style requirements:
- use 2 to 4 short connected sentences,
- use natural transitions, but vary them across updates,
- make the first sentence anchor to the newest change or signal, not to a generic restart,
- do not start every update with the same pattern such as "I first checked...",
- do not repeatedly open with first-person patterns like "I've...", "I have...", or "I'm..." unless there is a strong reason,
- prefer continuity cues such as "So far,", "At this point,", "That suggests,", "The latest result shows,", or "This narrows it down because..." when they fit,
- avoid rigid labels like "Verified", "Checking", "Uncertain", "Next",
- avoid repeating the same rhetorical skeleton from one round to the next,
- include brief causal links and small explanations, not just status labels,
- keep it concrete and evidence-driven,
- do not expose hidden chain-of-thought,
- do not mention internal event types, implementation details, or tool protocol syntax,
- do not pretend the task is complete if it is not,
- do not use rigid headings like "Verified:" or "Checking:" unless the user explicitly asked for a checklist.`

	var userPrompt strings.Builder
	var previousUpdates []string
	var newObservations []string
	for _, line := range observations {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Previous user-facing update: ") {
			previousUpdates = append(previousUpdates, strings.TrimPrefix(line, "Previous user-facing update: "))
			continue
		}
		newObservations = append(newObservations, line)
	}

	userPrompt.WriteString("Original user request:\n")
	userPrompt.WriteString(strings.TrimSpace(userInput))
	userPrompt.WriteString("\n\nCurrent round:\n")
	userPrompt.WriteString(fmt.Sprintf("%d", round))
	if len(previousUpdates) > 0 {
		userPrompt.WriteString("\n\nPrevious user-facing updates already shown:\n")
		for _, line := range previousUpdates {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			userPrompt.WriteString("- ")
			userPrompt.WriteString(line)
			userPrompt.WriteString("\n")
		}
	}
	userPrompt.WriteString("\n\nNew observed progress since the last update:\n")
	for _, line := range newObservations {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		userPrompt.WriteString("- ")
		userPrompt.WriteString(line)
		userPrompt.WriteString("\n")
	}
	userPrompt.WriteString("\nWrite a single progress update for the user that clearly continues from the previous updates and focuses on what changed.")

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
	return a.chatWithSessionInput(ctx, sess, TextUserTurnInput(userInput))
}

func (a *Agent) chatWithSessionInput(ctx context.Context, sess *session.Session, input UserTurnInput) (string, error) {
	input = input.Normalize()
	routingText := input.RoutingText
	a.maybeRouteModel(routingText)

	// 优先使用 RunLoop（支持 function calling / 工具调用）
	loopCfg := DefaultLoopConfig()
	agentLoopCfg := config.AgentLoopConfig{}
	if a.cfg != nil {
		cfg := a.cfg.Get()
		agentLoopCfg = cfg.Agent
		ApplyAgentLoopConfig(&loopCfg, cfg.Agent)
	}
	applySimpleTaskLoopTuning(&loopCfg, routingText, agentLoopCfg)
	loopCfg.AutoApprove = true // Telegram 场景自动批准工具调用

	result, err := a.RunLoopWithSessionInput(ctx, sess, input, loopCfg)
	if err != nil {
		// 如果 RunLoop 失败，回退到简单流式聊天
		response, chatErr := a.chatStreamSimpleInput(ctx, sess, input)
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
	a.saveConversationMemory(routingText, response)

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

var simpleLocalInspectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(list|show|check|inspect|read|open|verify|confirm|find)\b.*\b(file|files|dir|directory|folder|path|workspace)\b`),
	regexp.MustCompile(`(?i)\b(can|could|should)\b.*\b(send|attach|upload)\b.*\b(file|files|document|documents)\b`),
	regexp.MustCompile(`(?i)\bwhat\b.*\b(in|inside)\b.*\b(directory|folder|workspace)\b`),
	regexp.MustCompile(`查看.{0,10}(目录|文件|路径|工作区|文件夹)`),
	regexp.MustCompile(`检查.{0,10}(目录|文件|路径|工作区|文件夹)`),
	regexp.MustCompile(`确认.{0,14}(路径|文件|目录|能不能发|是否可发|是否可以发送|是否可发送)`),
	regexp.MustCompile(`(能不能|是否可以|是否可).{0,8}(发送|发出|上传).{0,10}(文件|附件)`),
	regexp.MustCompile(`列出.{0,8}(目录|文件)`),
}

func IsSimpleLocalInspectionTask(input string) bool {
	input = strings.TrimSpace(input)
	if input == "" {
		return false
	}
	for _, re := range simpleLocalInspectionPatterns {
		if re.MatchString(input) {
			return true
		}
	}
	return false
}

func applySimpleTaskLoopTuning(loopCfg *LoopConfig, userInput string, cfg config.AgentLoopConfig) {
	if loopCfg == nil || !IsSimpleLocalInspectionTask(userInput) {
		return
	}
	maxIterations := cfg.SimpleLocalInspection.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 3
	}
	timeout := time.Duration(cfg.SimpleLocalInspection.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	repeatToolCallLimit := cfg.SimpleLocalInspection.RepeatToolCallLimit
	if repeatToolCallLimit <= 0 {
		repeatToolCallLimit = 2
	}
	toolOnlyIterationLimit := cfg.SimpleLocalInspection.ToolOnlyIterationLimit
	if toolOnlyIterationLimit <= 0 {
		toolOnlyIterationLimit = 2
	}

	if loopCfg.MaxIterations > maxIterations {
		loopCfg.MaxIterations = maxIterations
	}
	if loopCfg.Timeout > timeout {
		loopCfg.Timeout = timeout
	}
	if loopCfg.RepeatToolCallLimit > repeatToolCallLimit {
		loopCfg.RepeatToolCallLimit = repeatToolCallLimit
	}
	if loopCfg.ToolOnlyIterationLimit > toolOnlyIterationLimit {
		loopCfg.ToolOnlyIterationLimit = toolOnlyIterationLimit
	}
}

// chatStreamSimple 是不使用工具的简单流式聊天（作为 RunLoop 的回退）。
/*
chatStreamSimple 使用不带工具调用的简单流式聊天作为回退路径。
*/
func (a *Agent) chatStreamSimple(ctx context.Context, sess *session.Session, userInput string) (string, error) {
	return a.chatStreamSimpleInput(ctx, sess, TextUserTurnInput(userInput))
}

func (a *Agent) chatStreamSimpleInput(ctx context.Context, sess *session.Session, input UserTurnInput) (string, error) {
	input = input.Normalize()
	routingText := input.RoutingText
	a.maybeRouteModel(routingText)
	messages := a.buildContextMessagesForInput(ctx, sess, input, defaultContextBuildOptions())

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
	sess.AddProviderMessage(input.Message)
	sess.AddProviderMessage(provider.Message{Role: "assistant", Content: response})

	// 保存会话
	_ = sess.Save()

	// 自动记忆：将对话存为短期记忆（去重 + 智能分类 + 截断）
	a.chatCount++
	a.saveConversationMemory(routingText, response)

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
	return a.buildContextMessagesForInput(ctx, sess, TextUserTurnInput(userInput), opts)
}

func (a *Agent) buildContextMessagesForInput(ctx context.Context, sess *session.Session, input UserTurnInput, opts contextBuildOptions) []provider.Message {
	planner := newContextPlanner(a, opts)
	return planner.BuildInput(ctx, sess, input)
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
	return a.ChatWithSessionStreamInput(ctx, sessionID, TextUserTurnInput(userInput))
}

func (a *Agent) ChatWithSessionStreamInput(ctx context.Context, sessionID string, input UserTurnInput) (<-chan ChatEvent, error) {
	sess, ok := a.sessions.Get(sessionID)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	input = input.Normalize()
	routingText := input.RoutingText

	events := make(chan ChatEvent, 64)

	go func() {
		defer close(events)

		messages := a.buildContextMessagesForInput(ctx, sess, input, defaultContextBuildOptions())

		callOpts := a.buildLoopCallOptions(routingText)

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
			a.streamNative(ctx, events, messages, callOpts, sess, input, 1, loopCfg.MaxIterations, state)
			return
		}

		// === 模拟流式路径 ===
		a.streamSimulated(ctx, events, messages, callOpts, sess, input, 1, loopCfg.MaxIterations, state)
	}()

	return events, nil
}

// streamNative 真流式：直接使用 provider 的 ChatStream，逐 chunk 推送
// tool_calls 通过流式增量拼接处理
/*
streamNative 使用 provider 原生流式接口执行一轮或多轮对话。
*/
func (a *Agent) streamNative(ctx context.Context, events chan<- ChatEvent, messages []provider.Message, callOpts provider.CallOptions, sess *session.Session, turnInput UserTurnInput, round int, remaining int, state *streamConvergenceState) {
	if state == nil {
		state = &streamConvergenceState{}
	}
	if remaining <= 0 {
		if state.hasContinuation() {
			a.finalizeStream(events, sess, turnInput, strings.TrimSpace(state.continuedResponse.String())+lengthTruncatedNotice)
			return
		}
		events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
		return
	}

	ch, err := a.streamLoopIteration(ctx, messages, callOpts, state.forceSearchSynthesis)
	if err != nil {
		events <- ChatEvent{Type: ChatEventError, Err: err}
		return
	}

	var content strings.Builder
	var reasoning strings.Builder
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
		if chunk.ReasoningContent != "" {
			reasoning.WriteString(chunk.ReasoningContent)
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
				a.finalizeStream(events, sess, turnInput, state.repeatedToolLoopMessage(repeatedSigs))
				return
			}

			// 将 assistant 消息加入历史
			messages = append(messages, provider.Message{
				Role:             "assistant",
				Content:          content.String(),
				ReasoningContent: reasoning.String(),
				ToolCalls:        toolCalls,
			})

			emitChatToolCallEvents(events, toolCalls)
			executed := a.executeToolCallsOrdered(
				toolCalls,
				true,
				sess,
				state.toolURLRepeatCount,
				state.toolURLLastResult,
				state.duplicateFetchLimit,
				true,
			)

			for _, execResult := range executed {
				emitChatToolResultEvent(events, execResult.ToolCall.Name, execResult.ShortResult)
				messages = append(messages, provider.Message{
					Role:       "tool",
					Content:    buildContextToolResult(execResult.ToolCall.Name, execResult.Result, &state.successfulSearchEvidence, &state.detailedSearchEvidence),
					ToolCallID: execResult.ToolCall.ID,
					Name:       execResult.ToolCall.Name,
				})
				state.rememberToolCallResult(execResult.ToolCall.Name, execResult.ToolCall.Arguments, execResult.Result)
			}

			// 裁剪上下文，继续下一轮
			messages = a.fitContextWindow(messages)
			messages = maybeAppendSearchSynthesisMessage(messages, &state.forceSearchSynthesis, state.successfulSearchEvidence, state.consecutiveToolOnlyIters)
			if remaining <= 1 {
				if state.hasContinuation() {
					a.finalizeStream(events, sess, turnInput, strings.TrimSpace(state.continuedResponse.String())+lengthTruncatedNotice)
					return
				}
				events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
				return
			}
			nextRound := round + 1
			events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}

			// 递归进入下一轮（用非流式，因为 tool_calls 后通常需要完整响应）
			a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
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
			a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
			return
		}
		if state.hasContinuation() {
			a.finalizeStream(events, sess, turnInput, strings.TrimSpace(state.continuedResponse.String()))
		} else {
			a.finalizeStream(events, sess, turnInput, emptyFinalResponseMessage)
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
			a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
			return
		}
		partial := strings.TrimSpace(state.continuedResponse.String())
		if partial == "" {
			partial = clean
		}
		a.finalizeStream(events, sess, turnInput, partial+lengthTruncatedNotice)
		return
	}
	state.lengthRecoveryCount = 0

	finalResponse := response
	if state.hasContinuation() {
		appendContinuation(&state.continuedResponse, response)
		finalResponse = strings.TrimSpace(state.continuedResponse.String())
	}
	a.finalizeStream(events, sess, turnInput, finalResponse)
}

// streamSimulated 模拟流式：先非流式获取完整响应，再按句子边界逐段推送
/*
streamSimulated 先获取完整响应，再按块模拟流式输出。
*/
func (a *Agent) streamSimulated(ctx context.Context, events chan<- ChatEvent, messages []provider.Message, callOpts provider.CallOptions, sess *session.Session, turnInput UserTurnInput, round int, remaining int, state *streamConvergenceState) {
	if state == nil {
		state = &streamConvergenceState{}
	}
	if remaining <= 0 {
		if state.hasContinuation() {
			a.finalizeStream(events, sess, turnInput, strings.TrimSpace(state.continuedResponse.String())+lengthTruncatedNotice)
			return
		}
		events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
		return
	}

	resp, err := a.chatLoopIteration(ctx, messages, callOpts, state.forceSearchSynthesis)
	if err != nil {
		events <- ChatEvent{Type: ChatEventError, Err: err}
		return
	}

	// 有工具调用 → 展示过程 → 执行 → 继续循环
	if len(resp.ToolCalls) > 0 {
		state.emptyResponseRetries = 0
		state.lengthRecoveryCount = 0
		if shouldStop, repeatedSigs := state.trackToolCallPattern(resp.ToolCalls, resp.Content); shouldStop {
			a.finalizeStream(events, sess, turnInput, state.repeatedToolLoopMessage(repeatedSigs))
			return
		}
		messages = append(messages, provider.Message{
			Role:             "assistant",
			Content:          resp.Content,
			ReasoningContent: resp.ReasoningContent,
			ToolCalls:        resp.ToolCalls,
		})

		emitChatToolCallEvents(events, resp.ToolCalls)
		executed := a.executeToolCallsOrdered(
			resp.ToolCalls,
			true,
			sess,
			state.toolURLRepeatCount,
			state.toolURLLastResult,
			state.duplicateFetchLimit,
			true,
		)

		for _, execResult := range executed {
			emitChatToolResultEvent(events, execResult.ToolCall.Name, execResult.ShortResult)
			messages = append(messages, provider.Message{
				Role:       "tool",
				Content:    buildContextToolResult(execResult.ToolCall.Name, execResult.Result, &state.successfulSearchEvidence, &state.detailedSearchEvidence),
				ToolCallID: execResult.ToolCall.ID,
				Name:       execResult.ToolCall.Name,
			})
			state.rememberToolCallResult(execResult.ToolCall.Name, execResult.ToolCall.Arguments, execResult.Result)
		}

		// 裁剪上下文，递归继续
		messages = a.fitContextWindow(messages)
		messages = maybeAppendSearchSynthesisMessage(messages, &state.forceSearchSynthesis, state.successfulSearchEvidence, state.consecutiveToolOnlyIters)
		if remaining <= 1 {
			if state.hasContinuation() {
				a.finalizeStream(events, sess, turnInput, strings.TrimSpace(state.continuedResponse.String())+lengthTruncatedNotice)
				return
			}
			events <- ChatEvent{Type: ChatEventError, Err: fmt.Errorf("max iterations reached")}
			return
		}
		nextRound := round + 1
		events <- ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("Thinking... (round %d)", nextRound)}
		a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
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
			a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
			return
		}
		if state.hasContinuation() {
			a.finalizeStream(events, sess, turnInput, strings.TrimSpace(state.continuedResponse.String()))
		} else {
			a.finalizeStream(events, sess, turnInput, emptyFinalResponseMessage)
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
			a.streamSimulated(ctx, events, messages, callOpts, sess, turnInput, nextRound, remaining-1, state)
			return
		}
		partial := strings.TrimSpace(state.continuedResponse.String())
		if partial == "" {
			partial = clean
		}
		a.finalizeStream(events, sess, turnInput, partial+lengthTruncatedNotice)
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
	a.finalizeStream(events, sess, turnInput, finalResponse)
}

// finalizeStream 流式对话收尾：保存会话、记忆、RAG 索引
func (a *Agent) finalizeStream(events chan<- ChatEvent, sess *session.Session, turnInput UserTurnInput, response string) {
	turnInput = turnInput.Normalize()
	routingText := turnInput.RoutingText
	response = utils.SanitizeToolProtocolOutput(response)
	sess.AddProviderMessage(turnInput.Message)
	sess.AddProviderMessage(provider.Message{Role: "assistant", Content: response})
	_ = sess.Save()

	a.chatCount++
	a.saveConversationMemory(routingText, response)
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
		a.indexConversationTurn(routingText, response)
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
	if a.cfg != nil {
		cfg := a.cfg.Get()
		enabled := cfg.Autonomy.Enabled
		if !enabled {
			raw := strings.TrimSpace(cfg.Extra["autonomy.enabled"])
			if raw != "" {
				parsed, err := strconv.ParseBool(strings.ToLower(raw))
				if err == nil {
					enabled = parsed
				}
			}
		}
		if !enabled {
			return nil
		}
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
	if a.tools == nil {
		a.tools = tool.NewRegistry()
	}
	loader := tool.NewSkillLoader(skillsDir)
	skillRegistry := tool.NewSkillRegistry(a.tools, loader)

	if _, err := skillRegistry.Discover(); err != nil {
		return 0, fmt.Errorf("discover skills: %w", err)
	}
	if err := skillRegistry.LoadAll(); err != nil {
		return 0, fmt.Errorf("load skills: %w", err)
	}
	if err := skillRegistry.ValidateAll(); err != nil {
		return 0, fmt.Errorf("validate skills: %w", err)
	}
	if err := skillRegistry.RegisterAll(); err != nil {
		return 0, fmt.Errorf("register skills: %w", err)
	}
	if err := skillRegistry.EnableAll(); err != nil {
		return 0, fmt.Errorf("enable skills: %w", err)
	}

	skills := skillRegistry.SkillInfos()
	a.skillRegistry = skillRegistry
	a.skills = skills
	tool.NewSkillToolService(skills).RegisterReadTool(a.tools)

	return len(skills), nil
}

// SkillRegistry returns the lifecycle registry for loaded skills.
func (a *Agent) SkillRegistry() *tool.SkillRegistry {
	return a.skillRegistry
}

// Skills 返回已加载的 skill 列表
func (a *Agent) Skills() []*tool.SkillInfo {
	return a.skills
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

// ContextCacheStats returns local context cache statistics.
func (a *Agent) ContextCacheStats() map[string]any {
	if a == nil || a.contextCache == nil {
		return map[string]any{}
	}
	stats := a.contextCache.Stats()
	return map[string]any{
		"entries":   stats.Entries,
		"hits":      stats.Hits,
		"misses":    stats.Misses,
		"evictions": stats.Evictions,
		"expired":   stats.Expired,
		"ttl":       stats.TTL.String(),
	}
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
