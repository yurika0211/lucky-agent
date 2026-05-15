package tool

import (
	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/multimodal"
	"github.com/yurika0211/luckyharness/internal/rag"
)

// Services groups tool-layer business services and owns their registration wiring.
type Services struct {
	Builtin   *BuiltinToolService
	SearchCfg *WebSearchConfig
	Memory    *MemoryToolService
	RAG       *RAGToolService
	Delegate  *DelegateManager
	Cron      *CronToolService
	Autonomy  *AutonomyToolService
	Heartbeat *HeartbeatToolService
	Skills    *SkillToolService
}

// NewServices creates a tool service container.
func NewServices(searchCfg *WebSearchConfig, defaultImageProvider string, mediaProcessor *multimodal.Processor, imageGenerator multimodal.ImageGenerator, imageGenDefaults ImageGenerationDefaults, mem *memory.Store, ragMgr *rag.RAGManager, delegate *DelegateManager) *Services {
	return &Services{
		Builtin:   NewBuiltinToolService(searchCfg, defaultImageProvider, mediaProcessor, imageGenerator, imageGenDefaults),
		SearchCfg: searchCfg,
		Memory:    NewMemoryToolService(mem),
		RAG:       NewRAGToolService(ragMgr),
		Delegate:  delegate,
	}
}

// RegisterCoreTools registers builtins and delegate tools through the service container.
func (s *Services) RegisterCoreTools(r *Registry) {
	if r == nil {
		return
	}

	if s.Builtin != nil {
		s.Builtin.RegisterTools(r)
	}

	if s.Memory != nil {
		r.Register(RememberTool(s.Memory.HandleRemember))
		r.Register(RecallTool(s.Memory.HandleRecall))
	}
	if s.RAG != nil {
		r.Register(RAGSearchTool(s.RAG.HandleSearch))
		r.Register(RAGIndexTool(s.RAG.HandleIndex))
	}
	if s.Delegate != nil {
		r.Register(DelegateTaskTool(s.Delegate))
		r.Register(TaskStatusTool(s.Delegate))
		r.Register(ListTasksTool(s.Delegate))
	}
	if s.Cron != nil {
		s.Cron.RegisterTools(r)
	}
	if s.Autonomy != nil {
		s.Autonomy.RegisterTools(r)
	}
	if s.Heartbeat != nil {
		s.Heartbeat.RegisterTools(r)
	}
	if s.Skills != nil {
		s.Skills.RegisterSkillTools(r)
	}
}
