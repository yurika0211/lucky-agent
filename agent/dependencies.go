package agent

import (
	"fmt"

	"github.com/yurika0211/luckyagent-runtime/bootstrap"
	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/hook"
	"github.com/yurika0211/luckyagent-runtime/metrics"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

// Options controls core execution resources. Configuration schema and backend
// defaults remain in the runtime module; callers supply prepared components.
type Options struct {
	ContextCacheSize int
	TakeOwnership    bool
}

// Dependencies is the prepared component contract shared with application
// builders. The runtime module owns concrete implementations and defaults.
type Dependencies = bootstrap.Components

func validateDependencies(deps *bootstrap.Components) error {
	if deps == nil {
		return fmt.Errorf("agent components are required")
	}
	checks := []struct {
		name    string
		missing bool
	}{
		{"configuration", deps.Config == nil},
		{"provider", deps.Provider.Provider == nil},
		{"sessions", deps.Sessions == nil},
		{"tools", deps.Support.Tools == nil},
	}
	for _, check := range checks {
		if check.missing {
			return fmt.Errorf("agent dependency %s is required", check.name)
		}
	}
	return nil
}

// StartupHooks supplies callbacks to the application's explicit lifecycle.
// Obtaining hooks does not activate any service.
func (a *Agent) StartupHooks() bootstrap.Hooks {
	return bootstrap.Hooks{
		LoadSkills:        a.LoadSkills,
		InstallCronEvents: a.installCronEventHandler,
		RestoreCron:       a.restoreCronJobs,
		SaveCron:          a.saveCronJobs,
		StartHeartbeat:    a.initHeartbeatService,
		ResumeAutonomy:    a.StartAutonomyNow,
	}
}

// NewWithDependencies binds prepared resources to an execution coordinator.
// It performs no backend construction and starts no background services.
// Ownership transfers only on success when TakeOwnership is set.
func NewWithDependencies(opts Options, deps *Dependencies) (*Agent, error) {
	if err := validateDependencies(deps); err != nil {
		return nil, err
	}
	cfg := deps.Config
	c := cfg.Get()
	soulRT, providerRT, memoryRT := deps.Soul, deps.Provider, deps.Memory
	ragRT, supportRT := deps.RAG, deps.Support
	sessionManager := deps.Sessions
	proactiveStore, proactiveRuntime := deps.ProactiveStore, deps.ProactiveRuntime
	taskStore, sandboxMgr := deps.Tasks, deps.Sandbox
	cacheSize := opts.ContextCacheSize
	if cacheSize <= 0 {
		cacheSize = 64
	}
	a := &Agent{
		cfg:                 cfg,
		components:          deps,
		ownComponents:       opts.TakeOwnership,
		mcpClient:           deps.MCP,
		soul:                soulRT.Soul,
		tmplMgr:             soulRT.TmplMgr,
		provider:            providerRT.Provider,
		registry:            providerRT.Registry,
		catalog:             providerRT.Catalog,
		tokenStore:          providerRT.TokenStore,
		memory:              memoryRT.Store,
		shortTerms:          memoryRT.ShortTerm,
		midTerm:             memoryRT.Summaries,
		sessions:            sessionManager,
		tools:               supportRT.Tools,
		toolServices:        supportRT.ToolServices,
		gateway:             supportRT.ToolGateway,
		hitl:                newHITLGate(),
		hooks:               hook.NewRunner(buildHookRuntimeConfig(c)),
		msgGateway:          deps.Gateway,
		delegate:            supportRT.DelegateMgr,
		contextWin:          supportRT.ContextWin,
		contextEst:          supportRT.ContextEst,
		ragManager:          ragRT.Manager,
		ragPersist:          ragRT.Persist,
		streamIndexer:       ragRT.StreamIndexer,
		embedderReg:         ragRT.EmbedderReg,
		collabReg:           deps.CollabRegistry,
		collabMgr:           deps.CollabManager,
		metrics:             supportRT.Metrics,
		proactiveStore:      proactiveStore,
		proactiveRuntime:    proactiveRuntime,
		cronEngine:          supportRT.CronEngine,
		cronStore:           deps.CronStore,
		autonomy:            supportRT.AutonomyKit,
		computerMgr:         supportRT.ComputerMgr,
		sandbox:             sandboxMgr,
		contextCache:        newContextMessageCache(cacheSize),
		mediaProcessor:      supportRT.MediaProcessor,
		taskStore:           taskStore,
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

	if err := a.preparePlugins(); err != nil {
		return nil, fmt.Errorf("prepare plugins: %w", err)
	}
	a.hooks.Reload(a.pluginHookConfig(c))
	if a.mcpClient != nil {
		if err := a.mcpClient.Configure(a.pluginMCPConfig(a.mcpClient.Configured())); err != nil {
			return nil, fmt.Errorf("configure plugin MCP: %w", err)
		}
	}

	a.bindOptionalToolServices()
	a.registerContextHistoryTool()
	if err := a.registerPluginTools(); err != nil {
		return nil, fmt.Errorf("register plugin tools: %w", err)
	}

	a.bindOptionalExecutionServices()
	a.recaps = newRecapService()

	return a, nil
}
