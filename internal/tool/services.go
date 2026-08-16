package tool

import (
	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/multimodal"
	"github.com/yurika0211/luckyagent/internal/rag"
)

// Services groups tool-layer business services and owns their registration wiring.
type Services struct {
	Builtin   *BuiltinToolService
	SearchCfg *WebSearchConfig
	OpenCLI   *OpenCLIConfig
	Memory    *MemoryToolService
	RAG       *RAGToolService
	Delegate  *DelegateManager
	Cron      *CronToolService
	Autonomy  *AutonomyToolService
	Heartbeat *HeartbeatToolService
	Skills    *SkillToolService
}

// SetComputerUseService attaches the optional desktop automation tools to the
// builtin registration set. It is separate from NewServices so existing
// callers remain source-compatible while computer backends stay optional.
func (s *Services) SetComputerUseService(service *ComputerUseToolService) *Services {
	if s != nil && s.Builtin != nil {
		s.Builtin.SetComputerUseService(service)
	}
	return s
}

// NewServices creates a tool service container.
func NewServices(searchCfg *WebSearchConfig, opencliCfg *OpenCLIConfig, defaultImageProvider string, mediaProcessor *multimodal.Processor, imageGenerator multimodal.ImageGenerator, imageGenDefaults ImageGenerationDefaults, speechSynthesizer multimodal.SpeechSynthesizer, ttsDefaults TTSDefaults, mem *memory.Store, ragMgr *rag.RAGManager, delegate *DelegateManager, policies ...FilesystemPolicy) *Services {
	policy := filesystemPolicyFromOptional(policies)
	return &Services{
		Builtin:   NewBuiltinToolService(searchCfg, opencliCfg, defaultImageProvider, mediaProcessor, imageGenerator, imageGenDefaults, speechSynthesizer, ttsDefaults, policy),
		SearchCfg: searchCfg,
		OpenCLI:   opencliCfg,
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
		r.Register(RecallTool(s.Memory.HandleRecall, s.Memory.HandleRecallDetailed))
		r.Register(MemoryHygieneTool(s.Memory.HandleHygiene))
	}
	if s.RAG != nil {
		r.Register(RAGSearchTool(s.RAG.HandleSearch))
		r.Register(RAGIndexTool(s.RAG.HandleIndex))
	}
	if s.Delegate != nil {
		r.Register(DelegateTaskTool(s.Delegate))
		r.Register(TaskStatusTool(s.Delegate))
		r.Register(WaitForTasksTool(s.Delegate))
		r.Register(ListTasksTool(s.Delegate))
		r.Register(DelegateCancelTool(s.Delegate))
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
