package tool

import "github.com/yurika0211/luckyharness/internal/multimodal"

// BuiltinToolService wraps the generic builtin tool registrations.
type BuiltinToolService struct {
	searchCfg            *WebSearchConfig
	mediaProcessor       *multimodal.Processor
	defaultImageProvider string
}

// NewBuiltinToolService creates a builtin tool service.
func NewBuiltinToolService(searchCfg *WebSearchConfig, defaultImageProvider string, mediaProcessor ...*multimodal.Processor) *BuiltinToolService {
	var processor *multimodal.Processor
	if len(mediaProcessor) > 0 {
		processor = mediaProcessor[0]
	}
	return &BuiltinToolService{
		searchCfg:            searchCfg,
		mediaProcessor:       processor,
		defaultImageProvider: defaultImageProvider,
	}
}

// RegisterTools registers builtin terminal/file/web/time tools.
func (s *BuiltinToolService) RegisterTools(r *Registry) {
	if s == nil || r == nil {
		return
	}
	r.Register(TerminalTool())
	r.Register(LegacyShellTool())
	r.Register(FileReadTool())
	r.Register(FileWriteTool())
	r.Register(FilePatchTool())
	r.Register(FileListTool())
	r.Register(WebSearchTool(s.searchCfg))
	r.Register(WebFetchTool(s.searchCfg))
	r.Register(CurrentTimeTool())
	r.Register(CalculateTool())
	r.Register(ImageAnalyzeTool(s.mediaProcessor, s.defaultImageProvider))
}
