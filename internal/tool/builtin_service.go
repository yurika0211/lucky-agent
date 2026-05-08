package tool

// BuiltinToolService wraps the generic builtin tool registrations.
type BuiltinToolService struct {
	searchCfg *WebSearchConfig
}

// NewBuiltinToolService creates a builtin tool service.
func NewBuiltinToolService(searchCfg *WebSearchConfig) *BuiltinToolService {
	return &BuiltinToolService{searchCfg: searchCfg}
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
	r.Register(FileListTool())
	r.Register(WebSearchTool(s.searchCfg))
	r.Register(WebFetchTool(s.searchCfg))
	r.Register(CurrentTimeTool())
}
