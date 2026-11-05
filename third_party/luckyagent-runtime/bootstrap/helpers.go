package bootstrap

import (
	"context"
	"fmt"
	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/credentials"
	"github.com/yurika0211/luckyagent-runtime/embedder"
	"github.com/yurika0211/luckyagent-runtime/hook"
	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/memoryaestus"
	"github.com/yurika0211/luckyagent-runtime/metrics"
	"github.com/yurika0211/luckyagent-runtime/middleware"
	"github.com/yurika0211/luckyagent-runtime/proactive"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/rag"
	"github.com/yurika0211/luckyagent-runtime/resilience"
	"github.com/yurika0211/luckyagent-runtime/soul"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/autonomy"
	codexruntime "github.com/yurika0211/luckyagent-tools/codex"
	"github.com/yurika0211/luckyagent-tools/computer"
	"github.com/yurika0211/luckyagent-tools/cron"
	grokruntime "github.com/yurika0211/luckyagent-tools/grok"
	"github.com/yurika0211/luckyagent-tools/memory"
	"github.com/yurika0211/luckyagent-tools/multimodal"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

/*
EmbedderRuntimeConfig 描述运行时解析后的嵌入模型配置。
*/
type EmbedderRuntimeConfig struct {
	APIKey    string
	Model     string
	BaseURL   string
	Dimension int
}

/**
 * SoulRuntime 管理 soul.Soul 实例及其相关的模板管理器。
 */
type SoulRuntime struct {
	Soul    *soul.Soul
	TmplMgr *soul.TemplateManager
}

/**
 * ProviderRuntime 管理 provider.Provider 实例及其相关的注册、模型目录和令牌存储。
 */
type ProviderRuntime struct {
	Provider   provider.Provider
	Registry   *provider.Registry
	Catalog    *provider.ModelCatalog
	TokenStore *provider.TokenStore
}

/**
 * RagRuntime 管理 RAG 相关的管理器和持久化存储。
 */
type RagRuntime struct {
	Manager       *rag.RAGManager
	Persist       *rag.Persistence
	StreamIndexer *rag.StreamIndexer
	EmbedderReg   *embedder.Registry
	SQLiteError   error
}

/**
 * SupportRuntime 管理支持工具和服务的运行时环境。
 */
type SupportRuntime struct {
	Tools          *tool.Registry
	ToolGateway    *tool.Gateway
	SearchCfg      *tool.WebSearchConfig
	ToolServices   *tool.Services
	DelegateMgr    *tool.DelegateManager
	ContextWin     *contextx.ContextWindow
	ContextEst     *contextx.TokenEstimator
	Metrics        *metrics.Metrics
	MediaProcessor *multimodal.Processor
	CronEngine     *cron.Engine
	AutonomyKit    *autonomy.AutonomyKit
	ComputerMgr    *computer.Manager
}

/**
 * ResolveEmbedderRuntimeConfig 从环境变量和主配置中解析嵌入模型运行时配置。
 * 返回值中的布尔值表示是否解析到了任何有效配置项。
 */
func ResolveEmbedderRuntimeConfig(c *config.Config) (EmbedderRuntimeConfig, bool) {
	cfg := EmbedderRuntimeConfig{}
	if c != nil {
		cfg = EmbedderRuntimeConfig{
			APIKey:    strings.TrimSpace(c.Embedding.APIKey),
			Model:     strings.TrimSpace(c.Embedding.Model),
			BaseURL:   strings.TrimSpace(c.Embedding.APIBase),
			Dimension: c.Embedding.Dimension,
		}
	}

	parsed := cfg.HasAny()
	if v := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL_KEY")); v != "" {
		cfg.APIKey = v
		parsed = true
	}
	if v := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL_NAME")); v != "" {
		cfg.Model = v
		parsed = true
	}
	if v := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL_URL")); v != "" {
		cfg.BaseURL = v
		parsed = true
	}
	if dim := os.Getenv("EMBEDDING_MODEL_DIMENSION"); dim != "" {
		dim = strings.TrimSpace(dim)
		if d, err := strconv.Atoi(dim); err == nil && d > 0 {
			cfg.Dimension = d
			parsed = true
		}
	}

	return cfg, parsed
}

func (cfg EmbedderRuntimeConfig) HasAny() bool {
	return cfg.APIKey != "" || cfg.BaseURL != "" || cfg.Model != "" || cfg.Dimension > 0
}

func (cfg EmbedderRuntimeConfig) Ready() bool {
	return cfg.APIKey != "" && cfg.BaseURL != "" && cfg.Model != "" && cfg.Dimension > 0
}

/**
 * ToProviderConfig 将全局配置转换为 provider 层可消费的配置对象。
 */
func ToProviderConfig(c *config.Config, modelOverride, apiBaseOverride string) provider.Config {
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
			Name:             c.Provider,
			APIKey:           c.APIKey,
			BaseURL:          apiBase,
			Model:            model,
			Protocol:         c.LlmProvider.Protocol,
			ReasoningSummary: c.LlmProvider.ReasoningSummary,
			ReasoningEffort:  c.LlmProvider.ReasoningEffort,
			PromptCacheKey:   c.LlmProvider.PromptCacheKey,
			Temperature:      c.Temperature,
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

/**
 * WrapProviderWithMiddleware 按当前配置为 provider 叠加中间件链。
 */
func WrapProviderWithMiddleware(p provider.Provider, c *config.Config) provider.Provider {
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
		chain.Use(middleware.NewRetryMiddlewareWithPredicate(retryCfg, RetryPredicateFromConfig(c.Retry)))
	}

	if c.CircuitBreaker.Enabled && !providerIsFallback(p) {
		chain.Use(middleware.NewCircuitBreakerMiddleware(circuitBreakerConfig(c)))
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

func providerIsFallback(p provider.Provider) bool {
	_, ok := p.(*provider.FallbackChain)
	return ok
}

func circuitBreakerConfig(c *config.Config) resilience.CircuitBreakerConfig {
	cbCfg := resilience.DefaultCircuitBreakerConfig()
	if c == nil {
		return cbCfg
	}
	if c.CircuitBreaker.ErrorThreshold > 0 {
		cbCfg.FailureThreshold = c.CircuitBreaker.ErrorThreshold
	}
	if c.CircuitBreaker.TimeoutSeconds > 0 {
		cbCfg.Timeout = time.Duration(c.CircuitBreaker.TimeoutSeconds) * time.Second
	}
	if c.CircuitBreaker.HalfOpenMaxReqs > 0 {
		cbCfg.HalfOpenMaxReqs = c.CircuitBreaker.HalfOpenMaxReqs
	}
	return cbCfg
}

func RetryPredicateFromConfig(cfg config.RetryConfig) resilience.IsRetryableFunc {
	return func(err error) bool {
		if err == nil {
			return false
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "context canceled") {
			return false
		}
		if strings.Contains(msg, "context deadline exceeded") ||
			strings.Contains(msg, "timeout") {
			return cfg.RetryOnTimeout
		}
		if strings.Contains(msg, "429") ||
			strings.Contains(msg, "rate limit") ||
			strings.Contains(msg, "too many requests") {
			return cfg.RetryOnRateLimit
		}
		if strings.Contains(msg, "500") ||
			strings.Contains(msg, "502") ||
			strings.Contains(msg, "503") ||
			strings.Contains(msg, "504") {
			return cfg.RetryOnServerError
		}
		if strings.Contains(msg, "connection refused") ||
			strings.Contains(msg, "connection reset") ||
			strings.Contains(msg, "broken pipe") ||
			strings.Contains(msg, "unexpected eof") ||
			strings.Contains(msg, "http2: client connection lost") ||
			strings.Contains(msg, "use of closed network connection") {
			return cfg.RetryOnTimeout
		}
		return false
	}
}

/**
 * InitSoulRuntime 初始化 soul 运行时，加载或使用默认的 soul 实例。
 */
func InitSoulRuntime(c *config.Config) SoulRuntime {
	var loadedSoul *soul.Soul
	if c != nil && strings.TrimSpace(c.SoulPath) != "" {
		if loaded, err := soul.Load(c.SoulPath); err == nil {
			loadedSoul = loaded
		}
	}
	if loadedSoul == nil {
		loadedSoul = soul.Default()
	}
	return SoulRuntime{
		Soul:    loadedSoul,
		TmplMgr: soul.NewTemplateManager(),
	}
}

/**
 * InitProviderRuntime 初始化 provider 运行时，包括注册 provider 工厂、创建模型目录和令牌存储等。
 */
func InitProviderRuntime(cfg *config.Manager, c *config.Config) (ProviderRuntime, error) {
	registry := provider.NewRegistry()
	if c.CircuitBreaker.Enabled {
		cbCfg := circuitBreakerConfig(c)
		registry.SetNodeResilience(func(p provider.Provider) provider.Provider {
			chain := middleware.NewChain()
			chain.Use(middleware.NewCircuitBreakerMiddleware(cbCfg))
			return middleware.NewMiddlewareProvider(p, chain)
		})
	}
	catalog := provider.NewModelCatalog()

	// 加载自定义模型
	for _, custom := range c.CustomModels {
		if custom.ID == "" {
			continue // 跳过没有ID的模型
		}
		catalog.Register(provider.ModelInfo{
			ID:            custom.ID,
			Provider:      custom.Provider,
			DisplayName:   custom.DisplayName,
			Capabilities:  custom.Capabilities,
			ContextWindow: custom.ContextWindow,
			CostPer1kIn:   custom.CostPer1kIn,
			CostPer1kOut:  custom.CostPer1kOut,
		})
	}

	tokenStore, err := provider.NewTokenStore(cfg.HomeDir() + "/tokens")
	if err != nil {
		tokenStore = nil
	}

	p, err := BuildConfiguredProvider(c, registry)
	if err != nil {
		return ProviderRuntime{}, err
	}
	p = WrapProviderWithMiddleware(p, c)

	return ProviderRuntime{
		Provider:   p,
		Registry:   registry,
		Catalog:    catalog,
		TokenStore: tokenStore,
	}, nil
}

func BuildConfiguredProvider(c *config.Config, registry *provider.Registry) (provider.Provider, error) {
	if c == nil || registry == nil {
		return nil, fmt.Errorf("provider configuration is unavailable")
	}
	var p provider.Provider
	if len(c.Fallbacks) > 0 {
		fallbackConfigs := make([]provider.FallbackConfig, 0, len(c.Fallbacks)+1)
		fallbackConfigs = append(fallbackConfigs, provider.FallbackConfig{
			Name:             c.Provider,
			APIKey:           c.APIKey,
			APIBase:          c.APIBase,
			Model:            c.Model,
			Protocol:         c.LlmProvider.Protocol,
			ReasoningSummary: c.LlmProvider.ReasoningSummary,
			ReasoningEffort:  c.LlmProvider.ReasoningEffort,
			PromptCacheKey:   c.LlmProvider.PromptCacheKey,
		})
		for _, fb := range c.Fallbacks {
			fallbackConfigs = append(fallbackConfigs, provider.FallbackConfig{
				Name:             fb.Provider,
				APIKey:           fb.APIKey,
				APIBase:          fb.APIBase,
				Model:            fb.Model,
				Protocol:         fb.Protocol,
				ReasoningSummary: fb.ReasoningSummary,
				ReasoningEffort:  fb.ReasoningEffort,
				PromptCacheKey:   fb.PromptCacheKey,
			})
		}
		chain, err := provider.NewFallbackChain(fallbackConfigs, registry)
		if err != nil {
			return nil, fmt.Errorf("create fallback chain: %w", err)
		}
		p = chain
	} else {
		pCfg := ToProviderConfig(c, "", "")
		var err error
		p, err = registry.Resolve(pCfg)
		if err != nil {
			return nil, fmt.Errorf("resolve Provider: %w", err)
		}
	}
	return p, nil
}

func InitProactiveRuntime(homeDir string, cfg config.ProactiveConfig) (*proactive.Store, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	store, err := proactive.OpenStore(ProactiveStorePath(homeDir, cfg.StorePath))
	if err != nil {
		return nil, fmt.Errorf("init proactive store: %w", err)
	}
	return store, nil
}

func ProactiveStorePath(homeDir, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return filepath.Join(homeDir, "runtime", "proactive_state.db")
	}
	if filepath.IsAbs(configured) {
		return configured
	}
	return filepath.Join(homeDir, configured)
}

func InitProactiveRuntimeService(homeDir string, cfg config.ProactiveConfig, store *proactive.Store) *proactive.RuntimeService {
	if store == nil || !cfg.Enabled || cfg.ActionIntervalSecs <= 0 {
		return nil
	}
	workspaceDir, _ := os.Getwd()
	dryRun := true
	if cfg.DryRun != nil {
		dryRun = *cfg.DryRun
	}
	gateCfg := proactive.Config{
		Enabled:             cfg.Enabled,
		DryRun:              dryRun,
		ConfidenceThreshold: cfg.ConfidenceThreshold,
		Horizon:             time.Duration(cfg.HorizonSeconds) * time.Second,
	}
	calibrator := proactive.NewFeedbackCalibrator(store)
	calibrator.KernelMinSamples = cfg.KernelMinSamples
	runner := proactive.NewRunnerWithCalibrator(
		proactive.NewSamplerWithStore(workspaceDir, store),
		proactive.NewEstimator(),
		calibrator,
		proactive.NewGate(gateCfg),
		store,
	)
	executor := proactive.NewActionExecutor(proactive.ActionPolicy{
		Enabled:        cfg.Enabled,
		DryRun:         dryRun,
		MaxActions:     cfg.MaxActions,
		Cooldown:       time.Duration(cfg.ActionCooldownSecs) * time.Second,
		WorkspaceDir:   workspaceDir,
		HomeDir:        homeDir,
		Allowed:        ProactiveAllowedActionMap(cfg.AllowedActions),
		ExecutionStore: store,
	})
	return proactive.NewRuntimeService(proactive.RuntimeServiceOptions{
		Runner:   runner,
		Executor: executor,
		Store:    store,
		Interval: time.Duration(cfg.ActionIntervalSecs) * time.Second,
	})
}

func ProactiveAllowedActionMap(actions []string) map[string]bool {
	if actions == nil {
		return nil
	}
	allowed := make(map[string]bool, len(actions))
	for _, action := range actions {
		action = strings.TrimSpace(action)
		if action != "" {
			allowed[action] = true
		}
	}
	return allowed
}

/**
 * InitRAGRuntime 初始化 RAG 运行时，包括注册嵌入器、创建缓存嵌入器和设置活动嵌入器。
 */
func InitRAGRuntime(cfg *config.Manager, c *config.Config) (RagRuntime, error) {
	embedderReg := embedder.NewRegistry()
	mockEmb := embedder.NewMockEmbedder(128)
	embedderReg.Register("mock-128", mockEmb)

	if embCfg, ok := ResolveEmbedderRuntimeConfig(c); ok && embCfg.Ready() {
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
	ragConfig.RetrieverConfig.TopK = c.RAG.TopK
	ragConfig.RetrieverConfig.MinScore = c.RAG.MinScore
	ragConfig.RetrieverConfig.UseHybrid = c.RAG.UseHybrid
	ragConfig.RetrieverConfig.DenseWeight = c.RAG.DenseWeight
	ragConfig.RetrieverConfig.UseMMR = c.RAG.UseMMR
	ragConfig.RetrieverConfig.MMRLambda = c.RAG.MMRLambda

	var ragManager *rag.RAGManager
	var ragPersist *rag.Persistence

	ragDBPath := cfg.HomeDir() + "/rag/luckyagent.db"
	ragMgr, err := rag.NewRAGManagerWithSQLite(activeEmb, ragConfig, ragDBPath)
	if err != nil {
		logger.Warn("sqlite RAG unavailable; serving without a loaded index", "path", ragDBPath, "error", err)
		ragManager = rag.NewRAGManager(activeEmb, ragConfig)
		// Keep a Persistence handle so Close can still snapshot an empty
		// in-memory index. Do not Load() an existing JSON dump: that path
		// materializes the whole corpus on the heap for a long-lived serve.
		ragPersist = rag.NewPersistence(cfg.HomeDir() + "/rag")
	} else {
		ragManager = ragMgr
	}

	return RagRuntime{
		Manager:       ragManager,
		Persist:       ragPersist,
		StreamIndexer: rag.NewStreamIndexer(ragManager, rag.DefaultStreamConfig()),
		EmbedderReg:   embedderReg,
		SQLiteError:   err,
	}, nil
}

/**
 * InitSupportRuntime 初始化支持运行时，包括注册工具、设置搜索配置和初始化多媒体处理器。
 */
func InitSupportRuntime(c *config.Config, mem memory.ToolBackend, ragMgr *rag.RAGManager) SupportRuntime {
	tools := tool.NewRegistry()
	searchCfg := &tool.WebSearchConfig{
		Provider:   c.WebSearch.Provider,
		APIKey:     c.WebSearch.APIKey,
		BaseURL:    c.WebSearch.BaseURL,
		MaxResults: c.WebSearch.MaxResults,
		Proxy:      c.WebSearch.Proxy,
	}
	media := BuildMediaRuntime(c)
	mediaProcessor, imageGenerator, speechSynthesizer := media.Processor, media.ImageGenerator, media.SpeechSynthesizer

	delegateMgr := tool.NewDelegateManager(BuildDelegateRuntimeConfig(c))
	imageGenDefaults, ttsDefaults := media.ImageDefaults, media.TtsDefaults
	opencliCfg := &tool.OpenCLIConfig{
		Enabled:            c.OpenCLI.Enabled,
		Command:            c.OpenCLI.Command,
		Args:               append([]string(nil), c.OpenCLI.Args...),
		TimeoutSeconds:     c.OpenCLI.TimeoutSeconds,
		MaxChars:           c.OpenCLI.MaxChars,
		FallbackToWebFetch: c.OpenCLI.FallbackToWebFetch,
	}
	filesystemPolicy := tool.FilesystemPolicy{
		AllowedReadRoots: append([]string(nil), c.Tools.Filesystem.AllowedReadRoots...),
	}
	toolServices := tool.NewServices(searchCfg, opencliCfg, "", mediaProcessor, imageGenerator, imageGenDefaults, speechSynthesizer, ttsDefaults, mem, ragMgr, delegateMgr, filesystemPolicy)
	if c.Codex.Enabled {
		command := append([]string{c.Codex.Command}, c.Codex.Args...)
		if strings.TrimSpace(command[0]) == "" {
			command = []string{"codex", "app-server"}
		}
		toolServices.Codex = tool.NewCodexToolService(codexruntime.NewManager(codexruntime.Config{
			Command:        command,
			ApprovalMode:   c.Codex.ApprovalMode,
			DefaultSandbox: c.Codex.DefaultSandbox,
			CWDAllowlist:   append([]string(nil), c.Codex.CWDAllowlist...),
			MaxEvents:      c.Codex.MaxEvents,
		}))
	}
	if c.Grok.Enabled {
		command := append([]string{c.Grok.Command}, c.Grok.Args...)
		if strings.TrimSpace(command[0]) == "" {
			command = []string{"grok", "agent", "stdio"}
		}
		toolServices.Grok = tool.NewGrokToolService(grokruntime.NewManager(grokruntime.Config{
			Command:      command,
			ApprovalMode: c.Grok.ApprovalMode,
			CWDAllowlist: append([]string(nil), c.Grok.CWDAllowlist...),
			MaxEvents:    c.Grok.MaxEvents,
			Model:        c.Grok.Model,
		}))
	}

	contextWin := contextx.NewContextWindow(contextx.WindowConfig{
		MaxTokens:            ContextTokensFor(c),
		ReservedTokens:       ReservedTokensFor(c),
		Strategy:             contextx.TrimLowPriority,
		SlidingWindowSize:    10,
		MaxConversationTurns: 50,
		MemoryBudget:         800,
		SummarizeThreshold:   0.8,
	})

	cronEngine := cron.NewEngine()

	return SupportRuntime{
		Tools:          tools,
		ToolGateway:    tool.NewGateway(tools),
		SearchCfg:      searchCfg,
		ToolServices:   toolServices,
		DelegateMgr:    delegateMgr,
		ContextWin:     contextWin,
		ContextEst:     contextx.NewTokenEstimator(ContextTokensFor(c)),
		Metrics:        metrics.NewMetrics(),
		MediaProcessor: mediaProcessor,
		CronEngine:     cronEngine,
		AutonomyKit:    autonomy.NewAutonomyKit(BuildAutonomyRuntimeConfig(c), nil),
	}
}

// ContextTokensFor is the shared context limit. max_tokens is a compatibility
// fallback for configurations constructed without Context.
func ContextTokensFor(c *config.Config) int {
	if c != nil {
		if c.Context.MaxContextTokens > 0 {
			return c.Context.MaxContextTokens
		}
		if c.MaxTokens > 0 {
			return c.MaxTokens
		}
	}
	return 8000
}

// ReservedTokensFor reserves output space from the same limit as ContextWin.
func ReservedTokensFor(c *config.Config) int {
	limit := ContextTokensFor(c)
	ratio := 0.25
	if c != nil {
		ratio = c.ContextBudget.ReservedRatio
	}
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.25
	}
	reserved := int(float64(limit) * ratio)
	if reserved < 1 {
		reserved = 1
	}
	if reserved >= limit {
		reserved = limit / 4
		if reserved < 1 {
			reserved = 1
		}
	}
	return reserved
}

func BuildAutonomyRuntimeConfig(c *config.Config) autonomy.AutonomyConfig {
	cfg := autonomy.DefaultAutonomyConfig()
	if c == nil {
		return cfg
	}
	worker := c.Autonomy.Worker
	recovery := c.Autonomy.Recovery
	if recovery.MaxRetries != nil {
		cfg.Pool.RunPolicy.MaxRetries = *recovery.MaxRetries
	}
	if recovery.RetryInitialSeconds > 0 {
		cfg.Pool.RunPolicy.RetryInitial = time.Duration(recovery.RetryInitialSeconds) * time.Second
	}
	if recovery.RetryMaxSeconds > 0 {
		cfg.Pool.RunPolicy.RetryMax = time.Duration(recovery.RetryMaxSeconds) * time.Second
	}
	if recovery.MaxSlices > 0 {
		cfg.Pool.RunPolicy.MaxSlices = recovery.MaxSlices
	}
	if recovery.MaxTotalSeconds > 0 {
		cfg.Pool.RunPolicy.MaxTotalTime = time.Duration(recovery.MaxTotalSeconds) * time.Second
	}
	loop := autonomy.DefaultWorkerLoopConfig()
	if worker.MaxIterations > 0 {
		loop.MaxIterations = worker.MaxIterations
	}
	if worker.TimeoutSeconds > 0 {
		loop.Timeout = time.Duration(worker.TimeoutSeconds) * time.Second
	}
	if worker.AutoApprove != nil {
		loop.AutoApprove = *worker.AutoApprove
		loop.AutoApproveSet = true
	}
	if worker.RepeatToolCallLimit > 0 {
		loop.RepeatToolCallLimit = worker.RepeatToolCallLimit
	}
	if worker.ToolOnlyIterationLimit > 0 {
		loop.ToolOnlyIterationLimit = worker.ToolOnlyIterationLimit
	}
	if worker.DuplicateFetchLimit > 0 {
		loop.DuplicateFetchLimit = worker.DuplicateFetchLimit
	}
	if worker.DisabledTools != nil {
		loop.DisabledTools = append([]string{}, worker.DisabledTools...)
	}
	cfg.Pool.WorkerLoop = loop
	if worker.TimeoutSeconds > 0 {
		cfg.Pool.TaskTimeout = time.Duration(worker.TimeoutSeconds) * time.Second
	}
	if c.Autonomy.QueueBuffer > 0 {
		cfg.QueueBuf = c.Autonomy.QueueBuffer
	}
	if c.Autonomy.Pool.MaxWorkers > 0 {
		cfg.Pool.MaxWorkers = c.Autonomy.Pool.MaxWorkers
	}
	if c.Autonomy.Pool.QueueBuffer > 0 {
		cfg.Pool.QueueBuffer = c.Autonomy.Pool.QueueBuffer
	}
	cfg.Pool.AutoScale = c.Autonomy.Pool.AutoScale
	if c.Autonomy.Pool.MinWorkers > 0 {
		cfg.Pool.MinWorkers = c.Autonomy.Pool.MinWorkers
	}
	if cfg.Pool.MinWorkers > cfg.Pool.MaxWorkers {
		cfg.Pool.MinWorkers = cfg.Pool.MaxWorkers
	}
	if mode := strings.ToLower(strings.TrimSpace(c.Autonomy.Heartbeat.Mode)); mode == "passive" || mode == "proactive" {
		cfg.Heartbeat.Mode = autonomy.HeartbeatMode(mode)
	}
	if c.Autonomy.Heartbeat.IntervalSeconds > 0 {
		cfg.Heartbeat.Interval = time.Duration(c.Autonomy.Heartbeat.IntervalSeconds) * time.Second
	}
	if c.Autonomy.Heartbeat.ActiveStart >= 0 && c.Autonomy.Heartbeat.ActiveStart <= 23 {
		cfg.Heartbeat.ActiveStart = c.Autonomy.Heartbeat.ActiveStart
	}
	if c.Autonomy.Heartbeat.ActiveEnd >= 0 && c.Autonomy.Heartbeat.ActiveEnd <= 23 {
		cfg.Heartbeat.ActiveEnd = c.Autonomy.Heartbeat.ActiveEnd
	}
	if c.Autonomy.Heartbeat.MaxTasksPerBeat > 0 {
		cfg.Heartbeat.MaxTasksPerBeat = c.Autonomy.Heartbeat.MaxTasksPerBeat
	}
	return cfg
}

func BuildDelegateRuntimeConfig(c *config.Config) tool.DelegateConfig {
	cfg := tool.DefaultDelegateConfig()
	if c == nil {
		return cfg
	}
	d := c.Delegate
	if d.MaxConcurrent > 0 {
		cfg.MaxConcurrent = d.MaxConcurrent
	}
	if d.TimeoutSeconds > 0 {
		cfg.Timeout = time.Duration(d.TimeoutSeconds) * time.Second
	}
	if d.MinTimeoutSeconds > 0 {
		cfg.MinTimeout = time.Duration(d.MinTimeoutSeconds) * time.Second
	}
	if d.MaxTimeoutSeconds > 0 {
		cfg.MaxTimeout = time.Duration(d.MaxTimeoutSeconds) * time.Second
	}
	if d.MaxResultBytesInline > 0 {
		cfg.MaxResultBytesInline = d.MaxResultBytesInline
	}
	if d.MaxChildren > 0 {
		cfg.MaxChildren = d.MaxChildren
	}
	if d.Child.MaxIterations > 0 {
		cfg.ChildMaxIterations = d.Child.MaxIterations
	}
	if d.Child.TimeoutSeconds > 0 {
		cfg.ChildTimeout = time.Duration(d.Child.TimeoutSeconds) * time.Second
	}
	if d.Child.AutoApprove != nil {
		cfg.ChildAutoApprove = *d.Child.AutoApprove
	}
	if d.Child.RepeatToolCallLimit > 0 {
		cfg.ChildRepeatToolLimit = d.Child.RepeatToolCallLimit
	}
	if d.Child.ToolOnlyIterationLimit > 0 {
		cfg.ChildToolOnlyLimit = d.Child.ToolOnlyIterationLimit
	}
	if d.Child.DuplicateFetchLimit > 0 {
		cfg.ChildDuplicateLimit = d.Child.DuplicateFetchLimit
	}
	if d.Child.DisabledTools != nil {
		cfg.ChildDisabledTools = append([]string(nil), d.Child.DisabledTools...)
	}
	cfg.ChildAllowRecursive = d.Child.AllowRecursiveDelegate
	return cfg
}

/**
 * BuildHookRuntimeConfig 将主配置中的 Hooks 段转换为 hook 运行时配置，
 * 写法对齐 BuildAutonomyRuntimeConfig：读取配置段并补默认值。
 */
func BuildHookRuntimeConfig(c *config.Config) hook.Config {
	cfg := hook.Config{
		Timeout:   30 * time.Second,
		MaxOutput: 1 << 20, // 1MB
	}
	if c == nil {
		return cfg
	}
	h := c.Hooks
	cfg.Enabled = h.Enabled
	cfg.FailClosed = h.FailClosed
	if h.TimeoutSeconds > 0 {
		cfg.Timeout = time.Duration(h.TimeoutSeconds) * time.Second
	}
	cfg.PreToolUse = ToHookSpecs(h.PreToolUse)
	cfg.PostToolUse = ToHookSpecs(h.PostToolUse)
	return cfg
}

// ToHookSpecs 将配置层的 HookSpec 列表转换为 hook 包的 Spec 列表。
func ToHookSpecs(specs []config.HookSpec) []hook.Spec {
	if len(specs) == 0 {
		return nil
	}
	out := make([]hook.Spec, 0, len(specs))
	for _, s := range specs {
		out = append(out, hook.Spec{
			Match:   s.Match,
			Sources: s.Sources,
			Command: s.Command,
			Script:  s.Script,
		})
	}
	return out
}

type ImageGenerationRuntimeConfig struct {
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

type TtsRuntimeConfig struct {
	Provider string
	APIKey   string
	APIBase  string
	AuthMode string
	Model    string
	Voice    string
	Format   string
	Speed    float64
}

func ResolveImageGenerationConfig(c *config.Config) (ImageGenerationRuntimeConfig, bool) {
	if c == nil {
		return ImageGenerationRuntimeConfig{}, false
	}

	selection, ok := c.ModelSelection(config.ModelKindImage)
	if !ok {
		return ImageGenerationRuntimeConfig{}, false
	}
	ep := c.ModelEndpoint(config.ModelKindImage)
	cfg := ImageGenerationRuntimeConfig{
		Provider:          strings.ToLower(strings.TrimSpace(ep.Provider)),
		APIKey:            strings.TrimSpace(ep.APIKey),
		APIBase:           strings.TrimSpace(ep.APIBase),
		AuthMode:          strings.ToLower(strings.TrimSpace(c.ImageGeneration.AuthMode)),
		Model:             selection.ID,
		Size:              strings.TrimSpace(c.ImageGeneration.Size),
		Quality:           strings.TrimSpace(c.ImageGeneration.Quality),
		Background:        strings.TrimSpace(c.ImageGeneration.Background),
		OutputFormat:      strings.TrimSpace(c.ImageGeneration.OutputFormat),
		OutputCompression: c.ImageGeneration.OutputCompression,
		Count:             c.ImageGeneration.Count,
	}
	if cfg.Provider == "" {
		cfg.Provider = "openai"
	}
	if cfg.AuthMode == "" {
		cfg.AuthMode = "bearer"
	}

	if cfg.APIBase == "" {
		if cfg.Provider == "gemini" {
			cfg.APIBase = "https://generativelanguage.googleapis.com/v1beta"
		} else {
			cfg.APIBase = "https://api.openai.com/v1"
		}
	}

	if cfg.APIKey == "" || cfg.APIBase == "" {
		return ImageGenerationRuntimeConfig{}, false
	}
	return cfg, true
}

func ResolveTTSConfig(c *config.Config) (TtsRuntimeConfig, bool) {
	if c == nil {
		return TtsRuntimeConfig{}, false
	}
	selection, ok := c.ModelSelection(config.ModelKindTTS)
	if !ok {
		return TtsRuntimeConfig{}, false
	}
	ep := c.ModelEndpoint(config.ModelKindTTS)
	cfg := TtsRuntimeConfig{
		Provider: strings.ToLower(strings.TrimSpace(ep.Provider)),
		APIKey:   strings.TrimSpace(ep.APIKey),
		APIBase:  strings.TrimSpace(ep.APIBase),
		AuthMode: strings.ToLower(strings.TrimSpace(c.TTS.AuthMode)),
		Model:    selection.ID,
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
		if cfg.APIBase == "" {
			cfg.APIBase = "https://api.openai.com/v1"
		}
	}
	if cfg.APIKey == "" || cfg.APIBase == "" {
		return TtsRuntimeConfig{}, false
	}
	return cfg, true
}

// ApplyWebSearchEnv 从环境变量覆盖 web_search 配置
/*
ApplyWebSearchEnv 使用环境变量补全 web_search 相关配置。
*/
func ApplyWebSearchEnv(cfg *config.Manager) {
	cur := cfg.Get()
	provider := strings.ToLower(strings.TrimSpace(cur.WebSearch.Provider))

	// 配置文件优先：仅在 config.json 对应字段为空时，才用环境变量补全。
	if cur.WebSearch.Provider == "" {
		if v := os.Getenv("LH_WEB_SEARCH_PROVIDER"); v != "" {
			_ = cfg.Set("web_search.Provider", v)
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

// ApplyOpenCLIEnv 从环境变量覆盖 opencli 配置。
/*
ApplyOpenCLIEnv 使用环境变量补全 opencli 相关配置。
*/
func ApplyOpenCLIEnv(cfg *config.Manager) {
	if v := os.Getenv("LH_OPENCLI_ENABLED"); v != "" {
		_ = cfg.Set("opencli.enabled", v)
	}
	if v := os.Getenv("LH_OPENCLI_COMMAND"); v != "" {
		_ = cfg.Set("opencli.command", v)
	}
	if v := os.Getenv("LH_OPENCLI_ARGS"); v != "" {
		_ = cfg.Set("opencli.args", v)
	}
	if v := os.Getenv("LH_OPENCLI_TIMEOUT_SECONDS"); v != "" {
		_ = cfg.Set("opencli.timeout_seconds", v)
	}
	if v := os.Getenv("LH_OPENCLI_MAX_CHARS"); v != "" {
		_ = cfg.Set("opencli.max_chars", v)
	}
	if v := os.Getenv("LH_OPENCLI_FALLBACK_TO_WEB_FETCH"); v != "" {
		_ = cfg.Set("opencli.fallback_to_web_fetch", v)
	}
}

type MediaRuntime struct {
	Processor         *multimodal.Processor
	ImageGenerator    multimodal.ImageGenerator
	ImageDefaults     tool.ImageGenerationDefaults
	SpeechSynthesizer multimodal.SpeechSynthesizer
	TtsDefaults       tool.TTSDefaults
}

func BuildMediaRuntime(c *config.Config) MediaRuntime {
	runtime := MediaRuntime{Processor: multimodal.NewProcessor()}
	_ = runtime.Processor.RegisterProvider(multimodal.NewLocalProvider(
		multimodal.ModalityText,
		multimodal.ModalityVideo,
	), true)

	for _, kind := range []config.ModelKind{config.ModelKindVision, config.ModelKindTranscription} {
		selection, ok := c.ModelSelection(kind)
		if !ok {
			continue
		}
		ep := c.ModelEndpoint(kind)
		modalities := []multimodal.Modality{multimodal.ModalityImage, multimodal.ModalityDocument}
		if kind == config.ModelKindTranscription {
			modalities = []multimodal.Modality{multimodal.ModalityAudio}
		}
		if ep.Provider == "local" {
			_ = runtime.Processor.RegisterProvider(multimodal.NewLocalProvider(modalities...), true, modalities...)
			continue
		}
		if media, err := multimodal.NewOpenAIMediaProvider(multimodal.OpenAIMediaConfig{
			APIKey: ep.APIKey, APIBase: ep.APIBase, ResponsesModel: selection.ID, TranscriptionModel: selection.ID,
		}); err == nil {
			_ = runtime.Processor.RegisterProvider(media, true, modalities...)
		}
	}

	if imageCfg, ok := ResolveImageGenerationConfig(c); ok {
		switch imageCfg.Provider {
		case "gemini":
			if generator, err := multimodal.NewGeminiImageProvider(multimodal.GeminiImageConfig{
				APIKey: imageCfg.APIKey, APIBase: imageCfg.APIBase, AuthMode: imageCfg.AuthMode,
			}); err == nil {
				runtime.ImageGenerator = generator
			}
		case "openai":
			if generator, err := multimodal.NewOpenAIMediaProvider(multimodal.OpenAIMediaConfig{
				APIKey:  imageCfg.APIKey,
				APIBase: imageCfg.APIBase,
			}); err == nil {
				runtime.ImageGenerator = generator
			}
		}
	}

	if ttsCfg, ok := ResolveTTSConfig(c); ok && ttsCfg.Provider == "openai" {
		if synthesizer, err := multimodal.NewOpenAITTSProvider(multimodal.OpenAITTSConfig{
			APIKey: ttsCfg.APIKey, APIBase: ttsCfg.APIBase, AuthMode: ttsCfg.AuthMode,
		}); err == nil {
			runtime.SpeechSynthesizer = synthesizer
		}
	}

	runtime.ImageDefaults = tool.ImageGenerationDefaults{
		Model:             strings.TrimSpace(c.ImageGeneration.Model),
		Size:              strings.TrimSpace(c.ImageGeneration.Size),
		Quality:           strings.TrimSpace(c.ImageGeneration.Quality),
		Background:        strings.TrimSpace(c.ImageGeneration.Background),
		OutputFormat:      strings.TrimSpace(c.ImageGeneration.OutputFormat),
		OutputCompression: c.ImageGeneration.OutputCompression,
		Count:             c.ImageGeneration.Count,
	}
	runtime.TtsDefaults = tool.TTSDefaults{
		Model: strings.TrimSpace(c.TTS.Model), Voice: strings.TrimSpace(c.TTS.Voice),
		Format: strings.TrimSpace(c.TTS.Format), Speed: c.TTS.Speed,
	}
	return runtime
}

// Default backend selection is confined to this assembly point. The Agent
// loop and context planner operate only on the supplied memory ports.
func ResolveMemoryRuntime(cfg *config.Manager, c *config.Config, injected *memory.Runtime) (memory.Runtime, error) {
	if injected != nil {
		if injected.Store == nil || injected.ShortTerm == nil || injected.Summaries == nil {
			return memory.Runtime{}, fmt.Errorf("incomplete memory runtime")
		}
		return *injected, nil
	}
	t := c.Memory.Tidal
	return memoryaestus.Open(memoryaestus.Options{HomeDir: cfg.HomeDir(), ShortTermMaxTurns: c.Memory.ShortTermMaxTurns,
		MidTermMaxSummaries: c.Memory.MidTermMaxSummaries, Tidal: memoryaestus.TidalOptions{
			Enabled: t.Enabled, StorePath: t.StorePath, Beta: t.Beta, MaxBoost: t.MaxBoost, LearningRate: t.LearningRate, MinSamples: t.MinSamples,
		}})
}

// ResolveConfiguredCredentials resolves credential_ref values into the local
// runtime snapshot used to construct providers. The config manager itself is
// never mutated, so encrypted references remain the only persisted form.
func ResolveConfiguredCredentials(homeDir string, c *config.Config) error {
	if c == nil {
		return nil
	}
	refs := false
	for _, endpoint := range c.Models.Endpoints {
		if strings.TrimSpace(endpoint.CredentialRef) != "" {
			refs = true
			break
		}
	}
	if !refs {
		for _, fallback := range c.Fallbacks {
			if strings.TrimSpace(fallback.CredentialRef) != "" {
				refs = true
				break
			}
		}
	}
	if !refs {
		return nil
	}

	store, err := credentials.NewStore(homeDir)
	if err != nil {
		return fmt.Errorf("open credential store: %w", err)
	}
	defer store.Close()

	for kind, endpoint := range c.Models.Endpoints {
		ref := strings.TrimSpace(endpoint.CredentialRef)
		if ref == "" {
			continue
		}
		kind, endpoint, ref := kind, endpoint, ref
		err := store.WithCredential(context.Background(), ref, func(secret []byte) error {
			endpoint.APIKey = string(secret)
			c.Models.Endpoints[kind] = endpoint
			ApplyResolvedModelCredential(c, kind, endpoint.APIKey)
			return nil
		})
		if err != nil {
			return fmt.Errorf("resolve credential %q for model kind %q: %w", ref, kind, err)
		}
	}
	for index := range c.Fallbacks {
		ref := strings.TrimSpace(c.Fallbacks[index].CredentialRef)
		if ref == "" {
			continue
		}
		index, ref := index, ref
		kind := strings.TrimSpace(c.Fallbacks[index].Provider)
		if kind == "" {
			kind = "fallback"
		}
		if err := store.WithCredential(context.Background(), ref, func(secret []byte) error {
			c.Fallbacks[index].APIKey = string(secret)
			return nil
		}); err != nil {
			return fmt.Errorf("resolve credential %q for fallback %q: %w", ref, kind, err)
		}
	}
	return nil
}

func ApplyResolvedModelCredential(c *config.Config, kind config.ModelKind, value string) {
	switch kind {
	case config.ModelKindChat:
		c.LlmProvider.APIKey = value
		c.APIKey = value
	case config.ModelKindEmbedding:
		c.Embedding.APIKey = value
	case config.ModelKindVision, config.ModelKindTranscription:
		c.Multimodal.APIKey = value
	case config.ModelKindImage:
		c.ImageGeneration.APIKey = value
	case config.ModelKindTTS:
		c.TTS.APIKey = value
	}
}
