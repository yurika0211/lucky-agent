package tool

import "github.com/yurika0211/luckyagent/internal/multimodal"

// RegisterBuiltinTools 注册所有内置工具
func RegisterBuiltinTools(r *Registry, mediaProcessor ...*multimodal.Processor) {
	RegisterBuiltinToolsWithConfig(r, nil, nil, mediaProcessor...)
}

// RegisterBuiltinToolsWithConfig 注册所有内置工具（带搜索配置）
func RegisterBuiltinToolsWithConfig(r *Registry, searchCfg *WebSearchConfig, opencliCfg *OpenCLIConfig, mediaProcessor ...*multimodal.Processor) {
	policy := DefaultFilesystemPolicy()
	var processor *multimodal.Processor
	if len(mediaProcessor) > 0 {
		processor = mediaProcessor[0]
	}

	r.Register(TerminalTool())
	r.Register(FileReadTool(policy))
	r.Register(DocumentReadTool(policy))
	r.Register(FileWriteTool())
	r.Register(FileMkdirTool())
	r.Register(FileMoveTool())
	r.Register(FileDeleteTool())
	r.Register(FilePatchTool())
	r.Register(FileListTool(policy))
	r.Register(WebSearchTool(searchCfg))
	r.Register(WebFetchTool(searchCfg))
	r.Register(OpenCLITool(opencliCfg, searchCfg))
	r.Register(CurrentTimeTool())
	r.Register(CalculateTool())
	r.Register(ImageAnalyzeTool(processor, ""))
	r.Register(ImageGenerateTool(nil, ImageGenerationDefaults{}))
	r.Register(TextToSpeechTool(nil, TTSDefaults{}))
	r.Register(LogTailTool())
	r.Register(LogGrepTool())
	r.Register(HTTPRequestTool())
	r.Register(JSONQueryTool())
	r.Register(YAMLQueryTool())
	r.Register(CSVQueryTool())
	r.Register(SQLQueryTool())
	r.Register(DBSchemaTool())
	r.Register(RememberTool(nil))
	r.Register(RecallTool(nil))
	r.Register(RAGSearchTool(nil))
	r.Register(RAGIndexTool(nil))
}

// RegisterBuiltinComputerTools adds the optional desktop observation/control
// tools to an existing registry. Keeping this separate avoids constructing a
// platform backend when computer use is disabled.
func RegisterBuiltinComputerTools(r *Registry, manager ComputerManager, cfg ComputerUseConfig) {
	if r == nil || manager == nil {
		return
	}
	NewComputerUseToolService(manager, cfg).RegisterTools(r)
}
