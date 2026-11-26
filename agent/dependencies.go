package agent

import "github.com/yurika0211/luckyagent-runtime/bootstrap"

// Dependencies is the existing runtime bootstrap input for NewWithDependencies.
// Embedders constructing their own components can use Inputs directly.
type Dependencies = bootstrap.Components

// InputsFromComponents maps a prepared runtime to the capabilities consumed by
// Agent. A nil runtime maps to nil. Mapping does not start or take ownership
// of resources.
func InputsFromComponents(c *Dependencies) *Inputs {
	if c == nil {
		return nil
	}
	return &Inputs{
		Config:           c.Config,
		Provider:         c.Provider.Provider,
		Sessions:         c.Sessions,
		Tools:            c.Support.Tools,
		ProviderRegistry: c.Provider.Registry,
		ModelCatalog:     c.Provider.Catalog,
		TokenStore:       c.Provider.TokenStore,
		Soul:             c.Soul.Soul,
		Templates:        c.Soul.TmplMgr,
		Memory:           c.Memory,
		ContextWindow:    c.Support.ContextWin,
		TokenEstimator:   c.Support.ContextEst,
		RAG:              c.RAG.Manager,
		RAGPersistence:   c.RAG.Persist,
		StreamIndexer:    c.RAG.StreamIndexer,
		Embedders:        c.RAG.EmbedderReg,
		ToolServices:     c.Support.ToolServices,
		ToolGateway:      c.Support.ToolGateway,
		Delegate:         c.Support.DelegateMgr,
		Metrics:          c.Support.Metrics,
		MediaProcessor:   c.Support.MediaProcessor,
		MCP:              c.MCP,
		Gateway:          c.Gateway,
		Sandbox:          c.Sandbox,
		Tasks:            c.Tasks,
		CollabRegistry:   c.CollabRegistry,
		CollabManager:    c.CollabManager,
		ProactiveStore:   c.ProactiveStore,
		ProactiveRuntime: c.ProactiveRuntime,
		Cron:             c.Support.CronEngine,
		CronStore:        c.CronStore,
		CronResults:      c.CronResults,
		Autonomy:         c.Support.AutonomyKit,
		Computer:         c.Support.ComputerMgr,
		CloseResources:   c.Close,
	}
}

// NewWithDependencies constructs an Agent from the runtime bootstrap bundle.
func NewWithDependencies(opts Options, deps *Dependencies) (*Agent, error) {
	return NewWithInputs(opts, InputsFromComponents(deps))
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
		ResumeForeground:  a.ResumeForeground,
	}
}
