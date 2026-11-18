package agent

import (
	"fmt"

	"github.com/yurika0211/luckyagent-runtime/bootstrap"
	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/embedder"
	"github.com/yurika0211/luckyagent-runtime/gateway"
	"github.com/yurika0211/luckyagent-runtime/hook"
	"github.com/yurika0211/luckyagent-runtime/mcpclient"
	"github.com/yurika0211/luckyagent-runtime/metrics"
	"github.com/yurika0211/luckyagent-runtime/proactive"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/rag"
	"github.com/yurika0211/luckyagent-runtime/sandbox"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/soul"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/autonomy"
	"github.com/yurika0211/luckyagent-tools/collab"
	"github.com/yurika0211/luckyagent-tools/computer"
	"github.com/yurika0211/luckyagent-tools/cron"
	"github.com/yurika0211/luckyagent-tools/memory"
	"github.com/yurika0211/luckyagent-tools/multimodal"
	taskstore "github.com/yurika0211/luckyagent-tools/task"
)

// Options controls core execution resources. Configuration schema and backend
// defaults remain in the runtime module; callers supply prepared components.
type Options struct {
	ContextCacheSize int
	TakeOwnership    bool
}

// Inputs supplies prepared capabilities to an Agent. Config, Provider, Sessions
// and Tools are required. All other capabilities are optional.
type Inputs struct {
	Config   *config.Manager
	Provider provider.Provider
	Sessions *session.Manager
	Tools    *tool.Registry

	ProviderRegistry *provider.Registry
	ModelCatalog     *provider.ModelCatalog
	TokenStore       *provider.TokenStore

	Soul           *soul.Soul
	Templates      *soul.TemplateManager
	Memory         memory.Runtime
	ContextWindow  *contextx.ContextWindow
	TokenEstimator *contextx.TokenEstimator

	RAG            *rag.RAGManager
	RAGPersistence *rag.Persistence
	StreamIndexer  *rag.StreamIndexer
	Embedders      *embedder.Registry

	ToolServices   *tool.Services
	ToolGateway    *tool.Gateway
	Delegate       *tool.DelegateManager
	Metrics        *metrics.Metrics
	MediaProcessor *multimodal.Processor
	MCP            *mcpclient.Manager
	Gateway        *gateway.GatewayManager
	Sandbox        *sandbox.Manager

	Tasks            taskstore.Store
	CollabRegistry   *collab.Registry
	CollabManager    *collab.DelegateManager
	ProactiveStore   *proactive.Store
	ProactiveRuntime *proactive.RuntimeService
	Cron             *cron.Engine
	CronStore        *cron.Store
	Autonomy         *autonomy.AutonomyKit
	Computer         *computer.Manager

	// CloseResources releases the supplied resources when ownership is transferred.
	// Required when Options.TakeOwnership is true. The caller keeps ownership
	// on construction failure and whenever TakeOwnership is false.
	CloseResources func() error
}

func validateInputs(in *Inputs) error {
	if in == nil {
		return fmt.Errorf("agent components are required")
	}
	checks := []struct {
		name    string
		missing bool
	}{
		{"configuration", in.Config == nil},
		{"provider", in.Provider == nil},
		{"sessions", in.Sessions == nil},
		{"tools", in.Tools == nil},
	}
	for _, check := range checks {
		if check.missing {
			return fmt.Errorf("agent dependency %s is required", check.name)
		}
	}
	return nil
}

// NewWithInputs binds prepared capabilities without opening backends or
// starting background services. Ownership transfers only on success.
func NewWithInputs(opts Options, in *Inputs) (*Agent, error) {
	if err := validateInputs(in); err != nil {
		return nil, err
	}
	if opts.TakeOwnership && in.CloseResources == nil {
		return nil, fmt.Errorf("agent dependency CloseResources is required when taking ownership")
	}
	c := in.Config.Get()
	cacheSize := opts.ContextCacheSize
	if cacheSize <= 0 {
		cacheSize = 64
	}
	a := &Agent{
		cfg:                 in.Config,
		injectedInputs:      true,
		mcpClient:           in.MCP,
		soul:                in.Soul,
		tmplMgr:             in.Templates,
		provider:            in.Provider,
		registry:            in.ProviderRegistry,
		catalog:             in.ModelCatalog,
		tokenStore:          in.TokenStore,
		memory:              in.Memory.Store,
		shortTerms:          in.Memory.ShortTerm,
		midTerm:             in.Memory.Summaries,
		sessions:            in.Sessions,
		tools:               in.Tools,
		toolServices:        in.ToolServices,
		gateway:             in.ToolGateway,
		hitl:                newHITLGate(),
		hooks:               hook.NewRunner(buildHookRuntimeConfig(c)),
		msgGateway:          in.Gateway,
		delegate:            in.Delegate,
		contextWin:          in.ContextWindow,
		contextEst:          in.TokenEstimator,
		ragManager:          in.RAG,
		ragPersist:          in.RAGPersistence,
		streamIndexer:       in.StreamIndexer,
		embedderReg:         in.Embedders,
		collabReg:           in.CollabRegistry,
		collabMgr:           in.CollabManager,
		metrics:             in.Metrics,
		toolMetrics:         newToolDurationMetrics(),
		proactiveStore:      in.ProactiveStore,
		proactiveRuntime:    in.ProactiveRuntime,
		cronEngine:          in.Cron,
		cronStore:           in.CronStore,
		autonomy:            in.Autonomy,
		computerMgr:         in.Computer,
		sandbox:             in.Sandbox,
		contextCache:        newContextMessageCache(cacheSize),
		mediaProcessor:      in.MediaProcessor,
		taskStore:           in.Tasks,
		autoCompactFailures: make(map[string]int),
		activeModel:         c.Model,
		activeAPIBase:       c.APIBase,
	}
	if a.gateway == nil {
		a.gateway = tool.NewGateway(a.tools)
	}
	if a.contextWin == nil {
		a.contextWin = contextx.NewContextWindow(contextx.WindowConfig{
			MaxTokens: bootstrap.ContextTokensFor(c), ReservedTokens: bootstrap.ReservedTokensFor(c), Strategy: contextx.TrimLowPriority,
		})
	}
	if a.contextEst == nil {
		a.contextEst = contextx.NewTokenEstimator(a.contextWin.Config().MaxTokens)
	}
	if a.metrics == nil {
		a.metrics = metrics.NewMetrics()
	}
	if a.toolMetrics == nil {
		a.toolMetrics = newToolDurationMetrics()
	}

	if err := a.preparePlugins(); err != nil {
		return nil, fmt.Errorf("prepare plugins: %w", err)
	}
	a.hooks.Reload(a.pluginHookConfig(c))
	if a.mcpClient != nil {
		if err := a.mcpClient.Configure(a.pluginMCPConfig(a.mcpClient.Configured())); err != nil {
			return nil, fmt.Errorf("configure plugin MCP: %w", err)
		}
	}

	a.memory = wrapRecallTimeout(capMemoryStore(in.Memory.Store, DefaultMaxMemoryNoteBytes), DefaultMemoryRecallTimeout)
	if a.toolServices != nil {
		if backend, ok := a.memory.(memory.ToolBackend); ok {
			a.toolServices.Memory = tool.NewMemoryToolService(backend)
		}
	}
	a.bindOptionalToolServices()
	a.registerContextHistoryTool()
	a.registerPresentUITool()
	a.registerStoryModeTool()
	if err := a.registerPluginTools(); err != nil {
		return nil, fmt.Errorf("register plugin tools: %w", err)
	}

	a.bindOptionalExecutionServices()
	a.recaps = newRecapService()
	if opts.TakeOwnership {
		a.closeResources = in.CloseResources
	}

	return a, nil
}
