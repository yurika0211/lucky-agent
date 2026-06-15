package tool

import "github.com/yurika0211/luckyharness/internal/multimodal"

// BuiltinToolService wraps the generic builtin tool registrations.
type BuiltinToolService struct {
	searchCfg            *WebSearchConfig
	opencliCfg           *OpenCLIConfig
	mediaProcessor       *multimodal.Processor
	imageGenerator       multimodal.ImageGenerator
	imageGenDefaults     ImageGenerationDefaults
	speechSynthesizer    multimodal.SpeechSynthesizer
	ttsDefaults          TTSDefaults
	defaultImageProvider string
}

// NewBuiltinToolService creates a builtin tool service.
func NewBuiltinToolService(searchCfg *WebSearchConfig, opencliCfg *OpenCLIConfig, defaultImageProvider string, mediaProcessor *multimodal.Processor, imageGenerator multimodal.ImageGenerator, imageGenDefaults ImageGenerationDefaults, speechSynthesizer multimodal.SpeechSynthesizer, ttsDefaults TTSDefaults) *BuiltinToolService {
	return &BuiltinToolService{
		searchCfg:            searchCfg,
		opencliCfg:           opencliCfg,
		mediaProcessor:       mediaProcessor,
		imageGenerator:       imageGenerator,
		imageGenDefaults:     imageGenDefaults,
		speechSynthesizer:    speechSynthesizer,
		ttsDefaults:          ttsDefaults,
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
	r.Register(FileMkdirTool())
	r.Register(FileMoveTool())
	r.Register(FileDeleteTool())
	r.Register(FilePatchTool())
	r.Register(FileListTool())
	r.Register(WebSearchTool(s.searchCfg))
	r.Register(WebFetchTool(s.searchCfg))
	r.Register(OpenCLITool(s.opencliCfg, s.searchCfg))
	r.Register(CurrentTimeTool())
	r.Register(CalculateTool())
	r.Register(ImageAnalyzeTool(s.mediaProcessor, s.defaultImageProvider))
	r.Register(ImageGenerateTool(s.imageGenerator, s.imageGenDefaults))
	r.Register(TextToSpeechTool(s.speechSynthesizer, s.ttsDefaults))
	r.Register(LogTailTool())
	r.Register(LogGrepTool())
	r.Register(HTTPRequestTool())
	r.Register(JSONQueryTool())
	r.Register(YAMLQueryTool())
	r.Register(CSVQueryTool())
	r.Register(SQLQueryTool())
	r.Register(DBSchemaTool())
}
