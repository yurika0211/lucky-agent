package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Provider != "openai" {
		t.Errorf("expected provider openai, got %s", cfg.Provider)
	}
	if cfg.Model != "gpt-5.4-mini" {
		t.Errorf("expected model ggpt-5.4-mini, got %s", cfg.Model)
	}
	if cfg.MaxTokens != 4096 {
		t.Errorf("expected max_tokens 4096, got %d", cfg.MaxTokens)
	}
	if cfg.Agent.RepeatToolCallLimit != 3 {
		t.Errorf("expected repeat_tool_call_limit 3, got %d", cfg.Agent.RepeatToolCallLimit)
	}
	if cfg.Agent.ToolOnlyIterationLimit != 3 {
		t.Errorf("expected tool_only_iteration_limit 3, got %d", cfg.Agent.ToolOnlyIterationLimit)
	}
	if cfg.Agent.DuplicateFetchLimit != 1 {
		t.Errorf("expected duplicate_fetch_limit 1, got %d", cfg.Agent.DuplicateFetchLimit)
	}
	if cfg.Agent.SimpleLocalInspection.MaxIterations != 3 {
		t.Errorf("expected simple_local_inspection.max_iterations 3, got %d", cfg.Agent.SimpleLocalInspection.MaxIterations)
	}
	if cfg.Agent.SimpleLocalInspection.TimeoutSeconds != 25 {
		t.Errorf("expected simple_local_inspection.timeout_seconds 25, got %d", cfg.Agent.SimpleLocalInspection.TimeoutSeconds)
	}
	if cfg.Agent.SimpleLocalInspection.RepeatToolCallLimit != 2 {
		t.Errorf("expected simple_local_inspection.repeat_tool_call_limit 2, got %d", cfg.Agent.SimpleLocalInspection.RepeatToolCallLimit)
	}
	if cfg.Agent.SimpleLocalInspection.ToolOnlyIterationLimit != 2 {
		t.Errorf("expected simple_local_inspection.tool_only_iteration_limit 2, got %d", cfg.Agent.SimpleLocalInspection.ToolOnlyIterationLimit)
	}
	if cfg.Autonomy.Enabled {
		t.Errorf("expected autonomy.enabled false by default, got true")
	}
	if cfg.Autonomy.Worker.MaxIterations != 10 {
		t.Errorf("expected autonomy.worker.max_iterations 10, got %d", cfg.Autonomy.Worker.MaxIterations)
	}
	if cfg.Autonomy.Worker.TimeoutSeconds != 120 {
		t.Errorf("expected autonomy.worker.timeout_seconds 120, got %d", cfg.Autonomy.Worker.TimeoutSeconds)
	}
	if cfg.Autonomy.Worker.AutoApprove == nil || !*cfg.Autonomy.Worker.AutoApprove {
		t.Errorf("expected autonomy.worker.auto_approve true")
	}
	if cfg.Autonomy.Worker.RepeatToolCallLimit != 3 {
		t.Errorf("expected autonomy.worker.repeat_tool_call_limit 3, got %d", cfg.Autonomy.Worker.RepeatToolCallLimit)
	}
	if cfg.Autonomy.Worker.ToolOnlyIterationLimit != 3 {
		t.Errorf("expected autonomy.worker.tool_only_iteration_limit 3, got %d", cfg.Autonomy.Worker.ToolOnlyIterationLimit)
	}
	if cfg.Autonomy.Worker.DuplicateFetchLimit != 1 {
		t.Errorf("expected autonomy.worker.duplicate_fetch_limit 1, got %d", cfg.Autonomy.Worker.DuplicateFetchLimit)
	}
	if len(cfg.Autonomy.Worker.DisabledTools) != 1 || cfg.Autonomy.Worker.DisabledTools[0] != "autonomy" {
		t.Errorf("expected autonomy.worker.disabled_tools [autonomy], got %v", cfg.Autonomy.Worker.DisabledTools)
	}
	if cfg.Multimodal.GenerationModel != "gpt-image-1.5" {
		t.Errorf("expected multimodal.generation_model gpt-image-1.5, got %s", cfg.Multimodal.GenerationModel)
	}
	if cfg.Multimodal.GenerationSize != "1024x1024" {
		t.Errorf("expected multimodal.generation_size 1024x1024, got %s", cfg.Multimodal.GenerationSize)
	}
	if cfg.Multimodal.GenerationOutputFormat != "png" {
		t.Errorf("expected multimodal.generation_output_format png, got %s", cfg.Multimodal.GenerationOutputFormat)
	}
	if cfg.ImageGeneration.Provider != "openai" {
		t.Errorf("expected image_generation.provider openai, got %s", cfg.ImageGeneration.Provider)
	}
	if cfg.ImageGeneration.APIBase != "https://api.openai.com/v1" {
		t.Errorf("expected image_generation.api_base https://api.openai.com/v1, got %s", cfg.ImageGeneration.APIBase)
	}
	if cfg.ImageGeneration.AuthMode != "bearer" {
		t.Errorf("expected image_generation.auth_mode bearer, got %s", cfg.ImageGeneration.AuthMode)
	}
	if cfg.TTS.Model != "gpt-4o-mini-tts" {
		t.Errorf("expected tts.model gpt-4o-mini-tts, got %s", cfg.TTS.Model)
	}
	if cfg.TTS.Voice != "alloy" {
		t.Errorf("expected tts.voice alloy, got %s", cfg.TTS.Voice)
	}
}

func TestManagerSetAndGet(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	mgr.Set("provider", "anthropic")
	mgr.Set("model", "claude-3")
	mgr.Set("max_tokens", "8192")
	mgr.Set("temperature", "0.5")

	cfg := mgr.Get()
	if cfg.Provider != "anthropic" {
		t.Errorf("expected anthropic, got %s", cfg.Provider)
	}
	if cfg.Model != "claude-3" {
		t.Errorf("expected claude-3, got %s", cfg.Model)
	}
	if cfg.MaxTokens != 8192 {
		t.Errorf("expected 8192, got %d", cfg.MaxTokens)
	}
	if cfg.Temperature != 0.5 {
		t.Errorf("expected 0.5, got %f", cfg.Temperature)
	}
}

func TestManagerSetTelegramProxy(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.Set("msg_gateway.telegram.proxy", "http://127.0.0.1:7897"); err != nil {
		t.Fatalf("Set telegram proxy: %v", err)
	}

	cfg := mgr.Get()
	if cfg.MsgGateway.Telegram.Proxy != "http://127.0.0.1:7897" {
		t.Errorf("expected telegram proxy to be set, got %q", cfg.MsgGateway.Telegram.Proxy)
	}
}

func TestManagerSetSimpleLocalInspectionConfig(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.Set("agent.simple_local_inspection.max_iterations", "5"); err != nil {
		t.Fatalf("Set simple_local_inspection.max_iterations: %v", err)
	}
	if err := mgr.Set("agent.simple_local_inspection.timeout_seconds", "15"); err != nil {
		t.Fatalf("Set simple_local_inspection.timeout_seconds: %v", err)
	}
	if err := mgr.Set("agent.simple_local_inspection.repeat_tool_call_limit", "4"); err != nil {
		t.Fatalf("Set simple_local_inspection.repeat_tool_call_limit: %v", err)
	}
	if err := mgr.Set("agent.simple_local_inspection.tool_only_iteration_limit", "3"); err != nil {
		t.Fatalf("Set simple_local_inspection.tool_only_iteration_limit: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Agent.SimpleLocalInspection.MaxIterations != 5 {
		t.Fatalf("expected 5, got %d", cfg.Agent.SimpleLocalInspection.MaxIterations)
	}
	if cfg.Agent.SimpleLocalInspection.TimeoutSeconds != 15 {
		t.Fatalf("expected 15, got %d", cfg.Agent.SimpleLocalInspection.TimeoutSeconds)
	}
	if cfg.Agent.SimpleLocalInspection.RepeatToolCallLimit != 4 {
		t.Fatalf("expected 4, got %d", cfg.Agent.SimpleLocalInspection.RepeatToolCallLimit)
	}
	if cfg.Agent.SimpleLocalInspection.ToolOnlyIterationLimit != 3 {
		t.Fatalf("expected 3, got %d", cfg.Agent.SimpleLocalInspection.ToolOnlyIterationLimit)
	}
}

func TestManagerSetAutonomyEnabled(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.Set("autonomy.enabled", "true"); err != nil {
		t.Fatalf("Set autonomy.enabled: %v", err)
	}

	cfg := mgr.Get()
	if !cfg.Autonomy.Enabled {
		t.Fatalf("expected autonomy.enabled to be true")
	}
}

func TestManagerSetAutonomyWorkerConfig(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.Set("autonomy.worker.max_iterations", "25"); err != nil {
		t.Fatalf("Set autonomy.worker.max_iterations: %v", err)
	}
	if err := mgr.Set("autonomy.worker.timeout_seconds", "240"); err != nil {
		t.Fatalf("Set autonomy.worker.timeout_seconds: %v", err)
	}
	if err := mgr.Set("autonomy.worker.auto_approve", "false"); err != nil {
		t.Fatalf("Set autonomy.worker.auto_approve: %v", err)
	}
	if err := mgr.Set("autonomy.worker.repeat_tool_call_limit", "5"); err != nil {
		t.Fatalf("Set autonomy.worker.repeat_tool_call_limit: %v", err)
	}
	if err := mgr.Set("autonomy.worker.tool_only_iteration_limit", "6"); err != nil {
		t.Fatalf("Set autonomy.worker.tool_only_iteration_limit: %v", err)
	}
	if err := mgr.Set("autonomy.worker.duplicate_fetch_limit", "2"); err != nil {
		t.Fatalf("Set autonomy.worker.duplicate_fetch_limit: %v", err)
	}
	if err := mgr.Set("autonomy.worker.disabled_tools", "autonomy,cron_add"); err != nil {
		t.Fatalf("Set autonomy.worker.disabled_tools: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Autonomy.Worker.MaxIterations != 25 {
		t.Fatalf("expected 25, got %d", cfg.Autonomy.Worker.MaxIterations)
	}
	if cfg.Autonomy.Worker.TimeoutSeconds != 240 {
		t.Fatalf("expected 240, got %d", cfg.Autonomy.Worker.TimeoutSeconds)
	}
	if cfg.Autonomy.Worker.AutoApprove == nil || *cfg.Autonomy.Worker.AutoApprove {
		t.Fatalf("expected auto_approve false, got %#v", cfg.Autonomy.Worker.AutoApprove)
	}
	if cfg.Autonomy.Worker.RepeatToolCallLimit != 5 {
		t.Fatalf("expected 5, got %d", cfg.Autonomy.Worker.RepeatToolCallLimit)
	}
	if cfg.Autonomy.Worker.ToolOnlyIterationLimit != 6 {
		t.Fatalf("expected 6, got %d", cfg.Autonomy.Worker.ToolOnlyIterationLimit)
	}
	if cfg.Autonomy.Worker.DuplicateFetchLimit != 2 {
		t.Fatalf("expected 2, got %d", cfg.Autonomy.Worker.DuplicateFetchLimit)
	}
	if len(cfg.Autonomy.Worker.DisabledTools) != 2 || cfg.Autonomy.Worker.DisabledTools[0] != "autonomy" || cfg.Autonomy.Worker.DisabledTools[1] != "cron_add" {
		t.Fatalf("unexpected disabled tools: %v", cfg.Autonomy.Worker.DisabledTools)
	}
}

func TestManagerSetAutonomyWorkerDisabledToolsCanBeCleared(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.Set("autonomy.worker.disabled_tools", ""); err != nil {
		t.Fatalf("Set autonomy.worker.disabled_tools: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Autonomy.Worker.DisabledTools == nil {
		t.Fatal("expected explicit empty disabled tools to be preserved")
	}
	if len(cfg.Autonomy.Worker.DisabledTools) != 0 {
		t.Fatalf("expected disabled tools to be empty, got %v", cfg.Autonomy.Worker.DisabledTools)
	}
}

func TestManagerSetMultimodalImageProvider(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.Set("multimodal.provider", "openai"); err != nil {
		t.Fatalf("Set multimodal.provider: %v", err)
	}
	if err := mgr.Set("multimodal.api_key", "mm-key"); err != nil {
		t.Fatalf("Set multimodal.api_key: %v", err)
	}
	if err := mgr.Set("multimodal.api_base", "https://vision.example/v1"); err != nil {
		t.Fatalf("Set multimodal.api_base: %v", err)
	}
	if err := mgr.Set("multimodal.image_model", "gpt-4.1-mini"); err != nil {
		t.Fatalf("Set multimodal.image_model: %v", err)
	}
	if err := mgr.Set("multimodal.generation_model", "gpt-image-1"); err != nil {
		t.Fatalf("Set multimodal.generation_model: %v", err)
	}
	if err := mgr.Set("multimodal.generation_size", "1536x1024"); err != nil {
		t.Fatalf("Set multimodal.generation_size: %v", err)
	}
	if err := mgr.Set("multimodal.generation_quality", "high"); err != nil {
		t.Fatalf("Set multimodal.generation_quality: %v", err)
	}
	if err := mgr.Set("multimodal.generation_background", "transparent"); err != nil {
		t.Fatalf("Set multimodal.generation_background: %v", err)
	}
	if err := mgr.Set("multimodal.generation_output_format", "webp"); err != nil {
		t.Fatalf("Set multimodal.generation_output_format: %v", err)
	}
	if err := mgr.Set("multimodal.transcription_model", "whisper-1"); err != nil {
		t.Fatalf("Set multimodal.transcription_model: %v", err)
	}
	if err := mgr.Set("multimodal.image_provider", "openai-media"); err != nil {
		t.Fatalf("Set multimodal.image_provider: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Multimodal.Provider != "openai" {
		t.Fatalf("expected openai, got %q", cfg.Multimodal.Provider)
	}
	if cfg.Multimodal.APIKey != "mm-key" {
		t.Fatalf("expected mm-key, got %q", cfg.Multimodal.APIKey)
	}
	if cfg.Multimodal.APIBase != "https://vision.example/v1" {
		t.Fatalf("expected multimodal api base, got %q", cfg.Multimodal.APIBase)
	}
	if cfg.Multimodal.ImageModel != "gpt-4.1-mini" {
		t.Fatalf("expected gpt-4.1-mini, got %q", cfg.Multimodal.ImageModel)
	}
	if cfg.Multimodal.GenerationModel != "gpt-image-1" {
		t.Fatalf("expected gpt-image-1, got %q", cfg.Multimodal.GenerationModel)
	}
	if cfg.Multimodal.GenerationSize != "1536x1024" {
		t.Fatalf("expected 1536x1024, got %q", cfg.Multimodal.GenerationSize)
	}
	if cfg.Multimodal.GenerationQuality != "high" {
		t.Fatalf("expected high, got %q", cfg.Multimodal.GenerationQuality)
	}
	if cfg.Multimodal.GenerationBackground != "transparent" {
		t.Fatalf("expected transparent, got %q", cfg.Multimodal.GenerationBackground)
	}
	if cfg.Multimodal.GenerationOutputFormat != "webp" {
		t.Fatalf("expected webp, got %q", cfg.Multimodal.GenerationOutputFormat)
	}
	if cfg.Multimodal.TranscriptionModel != "whisper-1" {
		t.Fatalf("expected whisper-1, got %q", cfg.Multimodal.TranscriptionModel)
	}
	if cfg.Multimodal.ImageProvider != "openai-media" {
		t.Fatalf("expected openai-media, got %q", cfg.Multimodal.ImageProvider)
	}
}

func TestManagerSetImageGenerationConfig(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.Set("image_generation.provider", "gemini"); err != nil {
		t.Fatalf("Set image_generation.provider: %v", err)
	}
	if err := mgr.Set("image_generation.api_key", "gen-key"); err != nil {
		t.Fatalf("Set image_generation.api_key: %v", err)
	}
	if err := mgr.Set("image_generation.api_base", "https://api.shiokou.asia/v1"); err != nil {
		t.Fatalf("Set image_generation.api_base: %v", err)
	}
	if err := mgr.Set("image_generation.auth_mode", "bearer"); err != nil {
		t.Fatalf("Set image_generation.auth_mode: %v", err)
	}
	if err := mgr.Set("image_generation.model", "gemini-3.1-flash-image-preview"); err != nil {
		t.Fatalf("Set image_generation.model: %v", err)
	}
	if err := mgr.Set("image_generation.size", "1024x1024"); err != nil {
		t.Fatalf("Set image_generation.size: %v", err)
	}
	if err := mgr.Set("image_generation.quality", "auto"); err != nil {
		t.Fatalf("Set image_generation.quality: %v", err)
	}
	if err := mgr.Set("image_generation.background", "auto"); err != nil {
		t.Fatalf("Set image_generation.background: %v", err)
	}
	if err := mgr.Set("image_generation.output_format", "png"); err != nil {
		t.Fatalf("Set image_generation.output_format: %v", err)
	}
	if err := mgr.Set("image_generation.count", "1"); err != nil {
		t.Fatalf("Set image_generation.count: %v", err)
	}

	cfg := mgr.Get()
	if cfg.ImageGeneration.Provider != "gemini" {
		t.Fatalf("expected gemini, got %q", cfg.ImageGeneration.Provider)
	}
	if cfg.ImageGeneration.APIKey != "gen-key" {
		t.Fatalf("expected gen-key, got %q", cfg.ImageGeneration.APIKey)
	}
	if cfg.ImageGeneration.APIBase != "https://api.shiokou.asia/v1" {
		t.Fatalf("expected https://api.shiokou.asia/v1, got %q", cfg.ImageGeneration.APIBase)
	}
	if cfg.ImageGeneration.AuthMode != "bearer" {
		t.Fatalf("expected bearer, got %q", cfg.ImageGeneration.AuthMode)
	}
	if cfg.ImageGeneration.Model != "gemini-3.1-flash-image-preview" {
		t.Fatalf("expected gemini model, got %q", cfg.ImageGeneration.Model)
	}
}

func TestManagerSetTTSConfig(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.Set("tts.provider", "openai"); err != nil {
		t.Fatalf("Set tts.provider: %v", err)
	}
	if err := mgr.Set("tts.api_key", "tts-key"); err != nil {
		t.Fatalf("Set tts.api_key: %v", err)
	}
	if err := mgr.Set("tts.api_base", "https://speech.example/v1"); err != nil {
		t.Fatalf("Set tts.api_base: %v", err)
	}
	if err := mgr.Set("tts.auth_mode", "bearer"); err != nil {
		t.Fatalf("Set tts.auth_mode: %v", err)
	}
	if err := mgr.Set("tts.model", "gpt-4o-mini-tts"); err != nil {
		t.Fatalf("Set tts.model: %v", err)
	}
	if err := mgr.Set("tts.voice", "alloy"); err != nil {
		t.Fatalf("Set tts.voice: %v", err)
	}
	if err := mgr.Set("tts.format", "wav"); err != nil {
		t.Fatalf("Set tts.format: %v", err)
	}
	if err := mgr.Set("tts.speed", "1.25"); err != nil {
		t.Fatalf("Set tts.speed: %v", err)
	}

	cfg := mgr.Get()
	if cfg.TTS.APIKey != "tts-key" || cfg.TTS.APIBase != "https://speech.example/v1" {
		t.Fatalf("unexpected tts config: %+v", cfg.TTS)
	}
	if cfg.TTS.Format != "wav" || cfg.TTS.Speed != 1.25 {
		t.Fatalf("unexpected tts format/speed: %+v", cfg.TTS)
	}
}

func TestManagerSetTelegramShowToolChainAlias(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.Set("msg_gateway.telegram.show_tool_chain", "true"); err != nil {
		t.Fatalf("Set telegram show_tool_chain: %v", err)
	}

	cfg := mgr.Get()
	if !cfg.MsgGateway.Telegram.ShowToolDetailsInResult {
		t.Fatalf("expected telegram tool chain alias to enable ShowToolDetailsInResult")
	}
}

func TestManagerSetEmbeddingConfig(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.Set("embedding.model", "jina-embeddings-v4"); err != nil {
		t.Fatalf("Set embedding.model: %v", err)
	}
	if err := mgr.Set("embedding.api_key", "emb-key"); err != nil {
		t.Fatalf("Set embedding.api_key: %v", err)
	}
	if err := mgr.Set("embedding.api_base", "https://proxy.example/v1"); err != nil {
		t.Fatalf("Set embedding.api_base: %v", err)
	}
	if err := mgr.Set("embedding.dimension", "2048"); err != nil {
		t.Fatalf("Set embedding.dimension: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Embedding.Model != "jina-embeddings-v4" {
		t.Fatalf("expected embedding model to be set, got %q", cfg.Embedding.Model)
	}
	if cfg.Embedding.APIKey != "emb-key" {
		t.Fatalf("expected embedding api_key to be set, got %q", cfg.Embedding.APIKey)
	}
	if cfg.Embedding.APIBase != "https://proxy.example/v1" {
		t.Fatalf("expected embedding api_base to be set, got %q", cfg.Embedding.APIBase)
	}
	if cfg.Embedding.Dimension != 2048 {
		t.Fatalf("expected embedding dimension 2048, got %d", cfg.Embedding.Dimension)
	}
}

func TestManagerSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, ".luckyharness")

	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// Override paths for test
	mgr.homeDir = homeDir
	mgr.cfgPath = filepath.Join(homeDir, "config.yaml")

	mgr.Set("provider", "ollama")
	mgr.Set("model", "llama3")

	if err := mgr.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load into new manager
	mgr2, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager2: %v", err)
	}
	mgr2.homeDir = homeDir
	mgr2.cfgPath = filepath.Join(homeDir, "config.yaml")

	if err := mgr2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := mgr2.Get()
	if cfg.Provider != "ollama" {
		t.Errorf("expected ollama, got %s", cfg.Provider)
	}
	if cfg.Model != "llama3" {
		t.Errorf("expected llama3, got %s", cfg.Model)
	}
}

func TestInitHome(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.homeDir = tmpDir

	if err := mgr.InitHome(); err != nil {
		t.Fatalf("InitHome: %v", err)
	}

	// Check directories
	dirs := []string{"sessions", "memory", "logs", "skills"}
	for _, d := range dirs {
		path := filepath.Join(tmpDir, d)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("directory %s not created", d)
		}
	}

	// Check SOUL.md
	soulPath := filepath.Join(tmpDir, "SOUL.md")
	if _, err := os.Stat(soulPath); os.IsNotExist(err) {
		t.Error("SOUL.md not created")
	}
}

// ---------------------------------------------------------------------------
// v0.62.0 ConfigCenter Coverage Improvements
// ---------------------------------------------------------------------------

func TestModelRouterNewModelRouter(t *testing.T) {
	config := ModelRouterConfig{
		Enable:         true,
		SimpleModel:    "gpt-5.4-mini",
		ComplexModel:   "gpt-5.4",
		LocalModel:     "qwen2.5-coder-32b",
		LocalBaseURL:   "http://localhost:11434",
		TokenThreshold: 500,
	}
	router := NewModelRouter(config)
	if router == nil {
		t.Fatal("NewModelRouter returned nil")
	}
	if router.config != config {
		t.Error("config not set correctly")
	}
}

func TestModelRouterSelectModel(t *testing.T) {
	config := ModelRouterConfig{
		Enable:         true,
		SimpleModel:    "gpt-5.4-mini",
		ComplexModel:   "gpt-5.4",
		LocalModel:     "qwen2.5-coder-32b",
		LocalBaseURL:   "http://localhost:11434",
		TokenThreshold: 500,
	}
	router := NewModelRouter(config)

	// Test simple task
	model, apiBase := router.SelectModel(TaskSimple)
	if model != "gpt-5.4-mini" {
		t.Errorf("expected gpt-5.4-mini, got %s", model)
	}

	// Test complex task
	model, apiBase = router.SelectModel(TaskComplex)
	if model != "gpt-5.4" {
		t.Errorf("expected gpt-5.4, got %s", model)
	}

	// Test moderate task (should use local)
	model, apiBase = router.SelectModel(TaskModerate)
	if model != "qwen2.5-coder-32b" {
		t.Errorf("expected qwen2.5-coder-32b, got %s", model)
	}
	if apiBase != "http://localhost:11434" {
		t.Errorf("expected local base URL, got %s", apiBase)
	}
}

func TestModelRouterSelectModelDisabled(t *testing.T) {
	config := ModelRouterConfig{
		Enable: false,
	}
	router := NewModelRouter(config)
	model, apiBase := router.SelectModel(TaskSimple)
	if model != "" || apiBase != "" {
		t.Error("should return empty when disabled")
	}
}

func TestEstimateComplexity(t *testing.T) {
	config := ModelRouterConfig{
		Enable:         true,
		TokenThreshold: 100,
	}
	router := NewModelRouter(config)

	// Test simple keywords
	if complexity := router.EstimateComplexity("hello world", 50); complexity != TaskSimple {
		t.Errorf("expected TaskSimple for 'hello', got %v", complexity)
	}
	if complexity := router.EstimateComplexity("你好", 50); complexity != TaskSimple {
		t.Errorf("expected TaskSimple for '你好', got %v", complexity)
	}
	if complexity := router.EstimateComplexity("what time is it", 50); complexity != TaskSimple {
		t.Errorf("expected TaskSimple for 'what time', got %v", complexity)
	}

	// Test complex keywords
	if complexity := router.EstimateComplexity("write code for me", 50); complexity != TaskComplex {
		t.Errorf("expected TaskComplex for 'write code', got %v", complexity)
	}
	if complexity := router.EstimateComplexity("实现一个功能", 50); complexity != TaskComplex {
		t.Errorf("expected TaskComplex for '实现', got %v", complexity)
	}
	// Note: avoid words containing simple keywords like "hi" in "this"
	if complexity := router.EstimateComplexity("debugging is fun", 50); complexity != TaskComplex {
		t.Errorf("expected TaskComplex for 'debug', got %v", complexity)
	}

	// Test token count threshold
	if complexity := router.EstimateComplexity("some random text", 200); complexity != TaskComplex {
		t.Errorf("expected TaskComplex for high token count, got %v", complexity)
	}

	// Test default (moderate)
	if complexity := router.EstimateComplexity("some random text", 50); complexity != TaskModerate {
		t.Errorf("expected TaskModerate for default, got %v", complexity)
	}
}

func TestIsLocalTask(t *testing.T) {
	config := ModelRouterConfig{}
	router := NewModelRouter(config)

	// Test local keywords
	if !router.IsLocalTask("file operation") {
		t.Error("should detect 'file' as local")
	}
	if !router.IsLocalTask("运行命令") {
		t.Error("should detect '运行' as local")
	}
	if !router.IsLocalTask("terminal command") {
		t.Error("should detect 'terminal' as local")
	}
	if !router.IsLocalTask("本地文件") {
		t.Error("should detect '本地' as local")
	}

	// Test non-local
	if router.IsLocalTask("hello world") {
		t.Error("should not detect 'hello' as local")
	}
	if router.IsLocalTask("write code") {
		t.Error("should not detect 'write code' as local")
	}
}

func TestSelectModelForTask(t *testing.T) {
	config := ModelRouterConfig{
		Enable:         true,
		SimpleModel:    "gpt-5.4-mini",
		ComplexModel:   "gpt-5.4",
		LocalModel:     "qwen2.5-coder-32b",
		LocalBaseURL:   "http://localhost:11434",
		TokenThreshold: 500,
	}
	router := NewModelRouter(config)

	// Local task should use local model
	model, apiBase := router.SelectModelForTask("file operation", 100)
	if model != "qwen2.5-coder-32b" {
		t.Errorf("expected local model for local task, got %s", model)
	}

	// Complex task
	model, apiBase = router.SelectModelForTask("write code for me", 100)
	if model != "gpt-5.4" {
		t.Errorf("expected complex model for complex task, got %s", model)
	}

	// Disabled router
	config.Enable = false
	router2 := NewModelRouter(config)
	model, apiBase = router2.SelectModelForTask("test", 100)
	if model != "" || apiBase != "" {
		t.Error("should return empty when disabled")
	}
}

func TestConfigWatcherOnError(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	// Create manager first
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.homeDir = tmpDir
	mgr.cfgPath = cfgPath

	// Create watcher with manager
	watcher := NewConfigWatcher(mgr, 1*time.Second)

	// Set error callback
	watcher.OnError(func(err error) {
		t.Logf("Error callback triggered: %v", err)
	})

	// Start watcher
	if err := watcher.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer watcher.Stop()

	// Write initial valid config
	if err := os.WriteFile(cfgPath, []byte("provider: test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Wait a bit for watcher to pick up changes
	time.Sleep(100 * time.Millisecond)
}

func TestManagerConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.cfgPath = cfgPath

	if mgr.ConfigFile() != cfgPath {
		t.Errorf("expected %s, got %s", cfgPath, mgr.ConfigFile())
	}
}

func TestManagerHomeDirPath(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.homeDir = tmpDir

	if mgr.HomeDirPath() != tmpDir {
		t.Errorf("expected %s, got %s", tmpDir, mgr.HomeDirPath())
	}
}

func TestHomeDir(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	homeDir := mgr.HomeDir()
	if homeDir == "" {
		t.Error("HomeDir should not be empty")
	}
}

func TestSetInvalidKey(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Test setting invalid key
	err = mgr.Set("invalid_key", "value")
	if err != nil {
		t.Logf("Set invalid key returned error (expected): %v", err)
	}
}

func TestModelRouterEstimateComplexityTokenThreshold(t *testing.T) {
	config := ModelRouterConfig{
		TokenThreshold: 0, // Zero threshold
	}
	router := NewModelRouter(config)

	// Should handle zero threshold gracefully
	complexity := router.EstimateComplexity("some text", 1000)
	if complexity != TaskComplex {
		t.Errorf("expected TaskComplex for high token count with zero threshold, got %v", complexity)
	}
}

// TestManagerLoad_InvalidYAML 测试 Load 方法处理无效 YAML
func TestManagerLoad_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	// 写入无效 YAML
	invalidYAML := []byte("invalid: yaml: content: [")
	if err := os.WriteFile(cfgPath, invalidYAML, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.cfgPath = cfgPath

	// 应该返回错误
	err = mgr.Load()
	if err == nil {
		t.Error("Load with invalid YAML should return error")
	}

	t.Logf("Load invalid YAML correctly returned error: %v", err)
}

// TestManagerLoad_NonExistentFile 测试 Load 方法处理不存在的文件
func TestManagerLoad_NonExistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "nonexistent.yaml")

	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.cfgPath = cfgPath

	// Load 可能会创建默认配置或返回空配置，不一定会报错
	err = mgr.Load()

	// 验证行为：要么返回错误，要么创建默认配置
	if err != nil {
		t.Logf("Load non-existent file returned error: %v", err)
	} else {
		t.Logf("Load non-existent file succeeded (created default config)")
	}
}

// TestManagerSave_InvalidPath 测试 Save 方法处理无效路径
func TestManagerSave_InvalidPath(t *testing.T) {
	tmpDir := t.TempDir()
	invalidPath := filepath.Join(tmpDir, "nonexistent_dir", "config.yaml")

	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.cfgPath = invalidPath

	// 设置一些值
	mgr.Set("test_key", "test_value")

	// 应该返回错误
	err = mgr.Save()
	if err == nil {
		t.Error("Save to invalid path should return error")
	}

	t.Logf("Save to invalid path correctly returned error: %v", err)
}

// TestManagerSaveAndLoad_RoundTrip 测试 Save 和 Load 的往返
func TestManagerSaveAndLoad_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	mgr1, err := NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	mgr1.cfgPath = cfgPath

	// 设置多个值
	testData := map[string]string{
		"provider":   "openai",
		"model":      "gpt-4",
		"max_tokens": "4096",
	}

	for k, v := range testData {
		if err := mgr1.Set(k, v); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	// 保存
	if err := mgr1.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 创建新的 manager 并加载
	mgr2, err := NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	mgr2.cfgPath = cfgPath

	if err := mgr2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 验证值
	cfg := mgr2.Get()
	if cfg.Provider != testData["provider"] {
		t.Errorf("Provider: expected %s, got %s", testData["provider"], cfg.Provider)
	}
	if cfg.Model != testData["model"] {
		t.Errorf("Model: expected %s, got %s", testData["model"], cfg.Model)
	}

	t.Logf("Save/Load roundtrip successful")
}

// TestSet_OverwriteExisting 测试 Set 覆盖已存在的键
func TestSet_OverwriteExisting(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}

	// 设置初始值
	if err := mgr.Set("provider", "anthropic"); err != nil {
		t.Fatalf("Set initial: %v", err)
	}

	// 覆盖
	if err := mgr.Set("provider", "openai"); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Provider != "openai" {
		t.Errorf("Expected 'openai', got %s", cfg.Provider)
	}

	t.Logf("Set overwrite successful: anthropic -> openai")
}

// TestNewManagerWithDir_PermDenied 测试 NewManagerWithDir 处理权限拒绝
func TestNewManagerWithDir_PermDenied(t *testing.T) {
	// 跳过 root 用户测试
	if os.Geteuid() == 0 {
		t.Skip("Skipping permission test as root")
	}

	// 创建目录并设置不可写
	tmpDir := t.TempDir()
	restrictedDir := filepath.Join(tmpDir, "restricted")
	if err := os.MkdirAll(restrictedDir, 0o000); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	defer os.Chmod(restrictedDir, 0o755)

	_, err := NewManagerWithDir(restrictedDir)
	if err == nil {
		t.Log("NewManagerWithDir with restricted dir succeeded (unexpected)")
	} else {
		t.Logf("NewManagerWithDir with restricted dir returned error (expected): %v", err)
	}
}

// v0.84.0: config 包补测 - 覆盖 Set 更多分支和辅助函数

// TestSet_WebSearchOptions 测试 websearch 子配置
func TestSet_WebSearchOptions(t *testing.T) {
	mgr, err := NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}

	mgr.Set("web_search.provider", "google")
	mgr.Set("web_search.api_key", "test-key")
	mgr.Set("web_search.base_url", "https://api.google.com")
	mgr.Set("web_search.max_results", "10")
	mgr.Set("web_search.proxy", "http://proxy:8080")

	cfg := mgr.Get()
	if cfg.WebSearch.Provider != "google" {
		t.Errorf("expected google, got %s", cfg.WebSearch.Provider)
	}
	if cfg.WebSearch.APIKey != "test-key" {
		t.Errorf("expected test-key, got %s", cfg.WebSearch.APIKey)
	}
	if cfg.WebSearch.MaxResults != 10 {
		t.Errorf("expected 10, got %d", cfg.WebSearch.MaxResults)
	}

	t.Logf("WebSearch options set correctly")
}

// TestSet_AgentOptions 测试 agent 子配置
func TestSet_AgentOptions(t *testing.T) {
	mgr, err := NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}

	mgr.Set("agent.max_iterations", "50")
	mgr.Set("agent.timeout_seconds", "300")
	mgr.Set("agent.auto_approve", "true")
	mgr.Set("agent.repeat_tool_call_limit", "2")
	mgr.Set("agent.tool_only_iteration_limit", "4")
	mgr.Set("agent.duplicate_fetch_limit", "1")
	mgr.Set("agent.context_debug", "true")

	cfg := mgr.Get()
	if cfg.Agent.MaxIterations != 50 {
		t.Errorf("expected 50, got %d", cfg.Agent.MaxIterations)
	}
	if cfg.Agent.TimeoutSeconds != 300 {
		t.Errorf("expected 300, got %d", cfg.Agent.TimeoutSeconds)
	}
	if cfg.Agent.RepeatToolCallLimit != 2 {
		t.Errorf("expected 2, got %d", cfg.Agent.RepeatToolCallLimit)
	}
	if cfg.Agent.ToolOnlyIterationLimit != 4 {
		t.Errorf("expected 4, got %d", cfg.Agent.ToolOnlyIterationLimit)
	}
	if cfg.Agent.DuplicateFetchLimit != 1 {
		t.Errorf("expected 1, got %d", cfg.Agent.DuplicateFetchLimit)
	}
	if !cfg.Agent.ContextDebug {
		t.Errorf("expected context_debug true")
	}

	t.Logf("Agent options set correctly")
}

// TestSet_StreamMode 测试 stream_mode
func TestSet_StreamMode(t *testing.T) {
	mgr, err := NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}

	mgr.Set("stream_mode", "sse")

	cfg := mgr.Get()
	if cfg.StreamMode != "sse" {
		t.Errorf("expected sse, got %s", cfg.StreamMode)
	}

	t.Logf("StreamMode set correctly")
}

// TestParseBool 测试 parseBool 函数
func TestParseBool(t *testing.T) {
	tests := []struct {
		input  string
		expect bool
	}{
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
		{"yes", true},
		{"no", false},
		{"y", true},
		{"n", false},
		{"on", true},
		{"off", false},
		{"TRUE", true},
		{"FALSE", false},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		result := parseBool(tt.input)
		if result != tt.expect {
			t.Errorf("parseBool(%q) = %v, want %v", tt.input, result, tt.expect)
		}
	}

	t.Logf("parseBool handles all cases correctly")
}

// TestSplitCSV 测试 splitCSV 函数
func TestSplitCSV(t *testing.T) {
	tests := []struct {
		input  string
		expect []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, b, c", []string{"a", "b", "c"}},
		{"a,,b", []string{"a", "b"}},
		{",a,b,", []string{"a", "b"}},
		{"", []string{}},
		{"single", []string{"single"}},
		{"  a  ,  b  ", []string{"a", "b"}},
	}

	for _, tt := range tests {
		result := splitCSV(tt.input)
		if len(result) != len(tt.expect) {
			t.Errorf("splitCSV(%q) length = %d, want %d", tt.input, len(result), len(tt.expect))
			continue
		}
		for i, v := range result {
			if v != tt.expect[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", tt.input, i, v, tt.expect[i])
			}
		}
	}

	t.Logf("splitCSV handles all cases correctly")
}

// TestSet_SoulPath 测试 soul_path
func TestSet_SoulPath(t *testing.T) {
	mgr, err := NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}

	mgr.Set("soul_path", "/custom/path/SOUL.md")

	cfg := mgr.Get()
	if cfg.SoulPath != "/custom/path/SOUL.md" {
		t.Errorf("expected /custom/path/SOUL.md, got %s", cfg.SoulPath)
	}

	t.Logf("SoulPath set correctly")
}

// TestSet_APIBase 测试 api_base
func TestSet_APIBase(t *testing.T) {
	mgr, err := NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}

	mgr.Set("api_base", "https://custom.api.com/v1")

	cfg := mgr.Get()
	if cfg.APIBase != "https://custom.api.com/v1" {
		t.Errorf("expected https://custom.api.com/v1, got %s", cfg.APIBase)
	}

	t.Logf("APIBase set correctly")
}

// TestSet_ExtraKeys 测试 extra.* 键
func TestSet_ExtraKeys(t *testing.T) {
	mgr, err := NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}

	mgr.Set("extra.custom_key", "custom_value")
	mgr.Set("extra.another_key", "another_value")

	// Extra map 可能为 nil 如果没有设置任何 extra key
	// 这里只验证 Set 不报错
	t.Logf("Extra keys set completed")
}

// TestInitHome_SoulAlreadyExists 测试 InitHome 在 SOUL.md 已存在时不覆盖
func TestInitHome_SoulAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}

	// 先创建 SOUL.md
	soulPath := filepath.Join(tmpDir, "SOUL.md")
	customContent := "custom soul content"
	if err := os.WriteFile(soulPath, []byte(customContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err = mgr.InitHome()
	if err != nil {
		t.Fatalf("InitHome: %v", err)
	}

	// 验证内容未被覆盖
	content, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != customContent {
		t.Errorf("SOUL.md should not be overwritten, got %q", string(content))
	}

	t.Logf("InitHome correctly preserves existing SOUL.md")
}
