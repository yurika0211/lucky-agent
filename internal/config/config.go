package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Config 代表 LuckyHarness 的运行时配置
type Config struct {
	LlmProvider  LlmProviderConfig `json:"llm_provider,omitempty"`
	Provider     string            `json:"-"`
	APIKey       string            `json:"-"`
	APIBase      string            `json:"-"`
	Model        string            `json:"-"`
	SoulPath     string            `json:"soul_path,omitempty"`
	MaxTokens    int               `json:"max_tokens"`
	Temperature  float64           `json:"temperature"`
	Extra        map[string]string `json:"extra,omitempty"`
	ExtraHeaders map[string]string `json:"extra_headers,omitempty"`

	// 降级链配置
	Fallbacks []FallbackEntry `json:"fallbacks,omitempty"`

	// Web 搜索配置
	WebSearch WebSearchConfig `json:"web_search,omitempty"`

	// OpenCLI 内容抽取配置
	OpenCLI OpenCLIConfig `json:"opencli,omitempty"`

	// Embedding 配置（供 RAG / 记忆向量化使用）
	Embedding EmbeddingConfig `json:"embedding,omitempty"`

	// 多模态配置
	Multimodal MultimodalConfig `json:"multimodal,omitempty"`

	// 独立图片生成配置
	ImageGeneration ImageGenerationConfig `json:"image_generation,omitempty"`

	// 独立 TTS 配置
	TTS TTSConfig `json:"tts,omitempty"`

	// 流式输出模式 (native=真流式，simulated=非流式获取 + 模拟推送)
	StreamMode string `json:"stream_mode,omitempty"`

	// 记忆系统配置
	Memory MemoryConfig `json:"memory,omitempty"`

	// 模型路由配置
	ModelRouter ModelRouterConfig `json:"model_router,omitempty"`

	// 限制配置
	Limits LimitsConfig `json:"limits,omitempty"`

	// 重试配置
	Retry RetryConfig `json:"retry,omitempty"`

	// 熔断器配置
	CircuitBreaker CircuitBreakerConfig `json:"circuit_breaker,omitempty"`

	// 限流配置
	RateLimit RateLimitConfig `json:"rate_limit,omitempty"`

	// 上下文配置
	Context ContextConfig `json:"context,omitempty"`

	// Agent Loop 配置
	Agent AgentLoopConfig `json:"agent,omitempty"`

	// API Server 配置
	Server ServerConfig `json:"server,omitempty"`

	// Dashboard 配置
	Dashboard DashboardConfig `json:"dashboard,omitempty"`

	// Autonomy 配置
	Autonomy AutonomyConfig `json:"autonomy,omitempty"`

	// Messaging Gateway 配置
	MsgGateway MsgGatewayConfig `json:"msg_gateway,omitempty"`

	// Hooks 配置工具执行前后的可插拔 hook（PreToolUse / PostToolUse）
	Hooks HooksConfig `json:"hooks,omitempty"`
}

// HooksConfig 配置工具执行边界上的 hook。Enabled 为 false 时所有 hook 不生效，
// 保持未配置 hook 的运行时行为不变。
type HooksConfig struct {
	Enabled        bool       `json:"enabled,omitempty"`
	TimeoutSeconds int        `json:"timeout_seconds,omitempty"` // 单个 hook 的执行超时，默认 30s
	FailClosed     bool       `json:"fail_closed,omitempty"`     // hook 出错/超时时是否拦截（默认放行）
	PreToolUse     []HookSpec `json:"pre_tool_use,omitempty"`
	PostToolUse    []HookSpec `json:"post_tool_use,omitempty"`
}

// HookSpec 声明单个外部命令 hook。Match/Sources 为空表示匹配全部工具/来源。
// Command 经平台 shell 执行；或用 Script 指定脚本路径（按扩展名选择解释器）。
type HookSpec struct {
	Match   []string `json:"match,omitempty"`   // 工具名，空=全部
	Sources []string `json:"sources,omitempty"` // 来源 cli/telegram/qq/...，空=全部
	Command string   `json:"command,omitempty"` // 外部命令
	Script  string   `json:"script,omitempty"`  // 或脚本路径
}

type LlmProviderConfig struct {
	Name    string `json:"name,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	Model   string `json:"model,omitempty"`
}

type EmbeddingConfig struct {
	Model     string `json:"model,omitempty"`
	APIKey    string `json:"api_key,omitempty"`
	APIBase   string `json:"api_base,omitempty"`
	Dimension int    `json:"dimension,omitempty"`
}

type MultimodalConfig struct {
	Provider           string `json:"provider,omitempty"`
	APIKey             string `json:"api_key,omitempty"`
	APIBase            string `json:"api_base,omitempty"`
	ImageModel         string `json:"image_model,omitempty"`
	TranscriptionModel string `json:"transcription_model,omitempty"`
	ImageProvider      string `json:"image_provider,omitempty"`
}

type ImageGenerationConfig struct {
	Provider          string `json:"provider,omitempty"`
	APIKey            string `json:"api_key,omitempty"`
	APIBase           string `json:"api_base,omitempty"`
	AuthMode          string `json:"auth_mode,omitempty"`
	Model             string `json:"model,omitempty"`
	Size              string `json:"size,omitempty"`
	Quality           string `json:"quality,omitempty"`
	Background        string `json:"background,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	OutputCompression int    `json:"output_compression,omitempty"`
	Count             int    `json:"count,omitempty"`
}

type TTSConfig struct {
	Provider string  `json:"provider,omitempty"`
	APIKey   string  `json:"api_key,omitempty"`
	APIBase  string  `json:"api_base,omitempty"`
	AuthMode string  `json:"auth_mode,omitempty"`
	Model    string  `json:"model,omitempty"`
	Voice    string  `json:"voice,omitempty"`
	Format   string  `json:"format,omitempty"`
	Speed    float64 `json:"speed,omitempty"`
}

// LimitsConfig 限制配置
type LimitsConfig struct {
	MaxTokens              int     `json:"max_tokens"`
	Temperature            float64 `json:"temperature"`
	TimeoutSeconds         int     `json:"timeout_seconds"`
	MaxTimeoutSeconds      int     `json:"max_timeout_seconds"`
	MaxToolCalls           int     `json:"max_tool_calls"`
	MaxConcurrentToolCalls int     `json:"max_concurrent_tool_calls"`
}

// RetryConfig 重试配置
type RetryConfig struct {
	Enabled            bool `json:"enabled"`
	MaxAttempts        int  `json:"max_attempts"`
	InitialDelayMs     int  `json:"initial_delay_ms"`
	MaxDelayMs         int  `json:"max_delay_ms"`
	RetryOnRateLimit   bool `json:"retry_on_rate_limit"`
	RetryOnTimeout     bool `json:"retry_on_timeout"`
	RetryOnServerError bool `json:"retry_on_server_error"`
}

// CircuitBreakerConfig 熔断器配置
type CircuitBreakerConfig struct {
	Enabled         bool `json:"enabled"`
	ErrorThreshold  int  `json:"error_threshold"`
	WindowSeconds   int  `json:"window_seconds"`
	TimeoutSeconds  int  `json:"timeout_seconds"`
	HalfOpenMaxReqs int  `json:"half_open_max_requests"`
}

// RateLimitConfig 限流配置
type RateLimitConfig struct {
	Enabled           bool `json:"enabled"`
	RequestsPerMinute int  `json:"requests_per_minute"`
	TokensPerMinute   int  `json:"tokens_per_minute"`
	BurstSize         int  `json:"burst_size"`
}

// ContextConfig 上下文配置
type ContextConfig struct {
	MaxHistoryTurns      int     `json:"max_history_turns"`
	MaxContextTokens     int     `json:"max_context_tokens"`
	CompressionThreshold float64 `json:"compression_threshold"`
}

// AgentLoopConfig Agent Loop 配置
type SimpleLocalInspectionConfig struct {
	MaxIterations          int `json:"max_iterations,omitempty"`
	TimeoutSeconds         int `json:"timeout_seconds,omitempty"`
	RepeatToolCallLimit    int `json:"repeat_tool_call_limit,omitempty"`
	ToolOnlyIterationLimit int `json:"tool_only_iteration_limit,omitempty"`
}

// AgentLoopConfig Agent Loop 配置
type AgentLoopConfig struct {
	MaxIterations          int                         `json:"max_iterations,omitempty"`
	TimeoutSeconds         int                         `json:"timeout_seconds,omitempty"`
	AutoApprove            bool                        `json:"auto_approve,omitempty"`
	RepeatToolCallLimit    int                         `json:"repeat_tool_call_limit,omitempty"`
	ToolOnlyIterationLimit int                         `json:"tool_only_iteration_limit,omitempty"`
	DuplicateFetchLimit    int                         `json:"duplicate_fetch_limit,omitempty"`
	ContextDebug           bool                        `json:"context_debug,omitempty"`
	SimpleLocalInspection  SimpleLocalInspectionConfig `json:"simple_local_inspection,omitempty"`
}

// ServerConfig API Server 配置
type ServerConfig struct {
	Addr        string   `json:"addr,omitempty"`
	APIKeys     []string `json:"api_keys,omitempty"`
	EnableCORS  bool     `json:"enable_cors,omitempty"`
	CORSOrigins []string `json:"cors_origins,omitempty"`
	RateLimit   int      `json:"rate_limit,omitempty"`
	MetricsAddr string   `json:"metrics_addr,omitempty"`
	LogLevel    string   `json:"log_level,omitempty"`
	LogFormat   string   `json:"log_format,omitempty"`
}

// DashboardConfig Dashboard 配置
type DashboardConfig struct {
	Addr string `json:"addr,omitempty"`
}

// AutonomyWorkerConfig 自主 worker 配置
type AutonomyWorkerConfig struct {
	MaxIterations          int      `json:"max_iterations,omitempty"`
	TimeoutSeconds         int      `json:"timeout_seconds,omitempty"`
	AutoApprove            *bool    `json:"auto_approve,omitempty"`
	RepeatToolCallLimit    int      `json:"repeat_tool_call_limit,omitempty"`
	ToolOnlyIterationLimit int      `json:"tool_only_iteration_limit,omitempty"`
	DuplicateFetchLimit    int      `json:"duplicate_fetch_limit,omitempty"`
	DisabledTools          []string `json:"disabled_tools,omitempty"`
}

// AutonomyConfig 自主工作套件配置
type AutonomyConfig struct {
	Enabled bool                 `json:"enabled,omitempty"`
	Worker  AutonomyWorkerConfig `json:"worker,omitempty"`
}

// MsgGatewayConfig 消息网关配置
type MsgGatewayConfig struct {
	Platform       string                   `json:"platform,omitempty"`
	StartAll       bool                     `json:"start_all,omitempty"`
	APIAddr        string                   `json:"api_addr,omitempty"`
	Telegram       MsgGatewayTelegram       `json:"telegram,omitempty"`
	QQOfficial     MsgGatewayQQOfficial     `json:"qqofficial,omitempty"`
	NapCat         MsgGatewayNapCat         `json:"napcat,omitempty"`
	Weixin         MsgGatewayWeixin         `json:"weixin,omitempty"`
	OpenClawWeixin MsgGatewayOpenClawWeixin `json:"openclawweixin,omitempty"`
}

// MsgGatewayTelegram Telegram 网关配置
type MsgGatewayTelegram struct {
	Token                     string `json:"token,omitempty"`
	Proxy                     string `json:"proxy,omitempty"`                        // Telegram API proxy URL (http/https/socks5)
	ChatTimeoutSeconds        int    `json:"chat_timeout_seconds,omitempty"`         // Telegram 对话总超时（秒）
	ProgressAsMessages        bool   `json:"progress_as_messages,omitempty"`         // 中间思考/工具步骤是否单独发消息
	ProgressAsNaturalLanguage bool   `json:"progress_as_natural_language,omitempty"` // 中间步骤是否转成自然语言进度播报（结论最后输出）
	ProgressSummaryWithLLM    bool   `json:"progress_summary_with_llm,omitempty"`    // 每轮未完成时是否由 LLM 生成一条总结性进度反馈
	ShowToolDetailsInResult   bool   `json:"show_tool_details_in_result,omitempty"`  // 最终回答前是否附上自然语言工具步骤摘要
}

// MsgGatewayQQOfficial QQ 官方机器人配置
type MsgGatewayQQOfficial struct {
	AppID         string   `json:"app_id,omitempty"`
	AppSecret     string   `json:"app_secret,omitempty"`
	Sandbox       bool     `json:"sandbox,omitempty"`
	APIBaseURL    string   `json:"api_base_url,omitempty"`
	GatewayURL    string   `json:"gateway_url,omitempty"`
	AllowedChats  []string `json:"allowed_chats,omitempty"`
	AllowedUsers  []string `json:"allowed_users,omitempty"`
	RemoveAt      bool     `json:"remove_at,omitempty"`
	HeartbeatSec  int      `json:"heartbeat_sec,omitempty"`
	ReconnectWait int      `json:"reconnect_wait_seconds,omitempty"`
	Intents       []string `json:"intents,omitempty"`
}

// MsgGatewayNapCat NapCat / OneBot v11 反向 WebSocket 配置
type MsgGatewayNapCat struct {
	ListenAddr       string   `json:"listen_addr,omitempty"`
	Path             string   `json:"path,omitempty"`
	AccessToken      string   `json:"access_token,omitempty"`
	AllowedChats     []string `json:"allowed_chats,omitempty"`
	AllowedUsers     []string `json:"allowed_users,omitempty"`
	RemoveAt         bool     `json:"remove_at,omitempty"`
	GroupTriggerMode string   `json:"group_trigger_mode,omitempty"`
}

// MemoryConfig 记忆系统配置
type MemoryConfig struct {
	ShortTermMaxTurns   int `json:"short_term_max_turns,omitempty"`  // 短期记忆最大轮数（默认 10）
	MidTermExpireDays   int `json:"midterm_expire_days,omitempty"`   // 中期记忆过期天数（默认 90）
	MidTermMaxSummaries int `json:"midterm_max_summaries,omitempty"` // 中期记忆最大摘要数（默认 100）
}

// ModelRouterConfig 模型路由配置
type ModelRouterConfig struct {
	Enable       bool   `json:"enable,omitempty"`         // 是否启用模型路由
	SimpleModel  string `json:"simple_model,omitempty"`   // 简单任务模型（便宜/快速）
	ComplexModel string `json:"complex_model,omitempty"`  // 复杂任务模型（强/慢）
	LocalModel   string `json:"local_model,omitempty"`    // 本地模型（ollama）
	LocalBaseURL string `json:"local_base_url,omitempty"` // 本地模型 API 地址

	// 自动路由阈值
	TokenThreshold int `json:"token_threshold,omitempty"` // 超过此 token 数视为复杂任务（默认 500）
}

// TaskComplexity 任务复杂度
type TaskComplexity int

const (
	TaskSimple   TaskComplexity = iota // 简单任务：问候、简单问答
	TaskModerate                       // 中等任务：一般查询、简单分析
	TaskComplex                        // 复杂任务：代码生成、复杂分析、多步骤推理
)

// ModelRouter 模型路由器
type ModelRouter struct {
	config ModelRouterConfig
}

// NewModelRouter 创建模型路由器
func NewModelRouter(config ModelRouterConfig) *ModelRouter {
	return &ModelRouter{config: config}
}

// SelectModel 根据任务复杂度选择模型
func (r *ModelRouter) SelectModel(complexity TaskComplexity) (model string, apiBase string) {
	if !r.config.Enable {
		return "", "" // 未启用路由，使用默认配置
	}

	switch complexity {
	case TaskSimple:
		// 简单任务使用便宜模型
		if r.config.SimpleModel != "" {
			return r.config.SimpleModel, ""
		}
	case TaskComplex:
		// 复杂任务使用强模型
		if r.config.ComplexModel != "" {
			return r.config.ComplexModel, ""
		}
	default:
		// 中等任务：如果有本地模型优先使用本地
		if r.config.LocalModel != "" {
			return r.config.LocalModel, r.config.LocalBaseURL
		}
	}

	return "", ""
}

// EstimateComplexity 根据输入估算任务复杂度
func (r *ModelRouter) EstimateComplexity(input string, tokenCount int) TaskComplexity {
	inputLower := strings.ToLower(input)

	// 简单任务关键词
	simpleKeywords := []string{
		"hello", "hi", "hey", "good morning", "good night",
		"谢谢", "你好", "再见", "早上好", "晚安",
		"what time", "current time", "date",
	}

	for _, kw := range simpleKeywords {
		if strings.Contains(inputLower, kw) {
			return TaskSimple
		}
	}

	// 复杂任务关键词
	complexKeywords := []string{
		"write code", "implement", "create a program", "build",
		"analyze", "compare", "explain in detail", "step by step",
		"optimize", "refactor", "debug", "design",
		"编写代码", "实现", "创建程序", "构建",
		"分析", "比较", "详细解释", "逐步",
		"优化", "重构", "调试", "设计",
	}

	for _, kw := range complexKeywords {
		if strings.Contains(inputLower, kw) {
			return TaskComplex
		}
	}

	// 根据 token 数判断
	if tokenCount > r.config.TokenThreshold {
		if r.config.TokenThreshold <= 0 {
			r.config.TokenThreshold = 500
		}
		return TaskComplex
	}

	// 默认为中等任务
	return TaskModerate
}

// IsLocalTask 判断是否为本地任务（涉及本地文件/命令）
func (r *ModelRouter) IsLocalTask(input string) bool {
	localKeywords := []string{
		"file", "directory", "folder", "path",
		"run", "execute", "command", "terminal", "shell",
		"local", "localhost",
		"文件", "目录", "文件夹", "路径",
		"运行", "执行", "命令", "终端",
	}

	inputLower := strings.ToLower(input)
	for _, kw := range localKeywords {
		if strings.Contains(inputLower, kw) {
			return true
		}
	}

	return false
}

// SelectModelForTask 根据任务描述自动选择模型
func (r *ModelRouter) SelectModelForTask(taskDescription string, tokenCount int) (model string, apiBase string) {
	if !r.config.Enable {
		return "", ""
	}

	// 如果是本地任务，优先使用本地模型
	if r.IsLocalTask(taskDescription) && r.config.LocalModel != "" {
		return r.config.LocalModel, r.config.LocalBaseURL
	}

	// 估算复杂度
	complexity := r.EstimateComplexity(taskDescription, tokenCount)
	return r.SelectModel(complexity)
}

// WebSearchConfig 网络搜索配置（照 nanobot WebSearchConfig 设计）
type WebSearchConfig struct {
	Provider   string `json:"provider,omitempty"`    // brave, ddgs, searxng（默认 brave）
	APIKey     string `json:"api_key,omitempty"`     // Brave / Tavily / Jina API key
	BaseURL    string `json:"base_url,omitempty"`    // SearXNG 自部署地址
	MaxResults int    `json:"max_results,omitempty"` // 最大结果数（默认 5）
	Proxy      string `json:"proxy,omitempty"`       // HTTP/SOCKS5 代理
}

// OpenCLIConfig OpenCLI 内容抽取配置
type OpenCLIConfig struct {
	Enabled            bool     `json:"enabled,omitempty"`               // 是否启用 OpenCLI 抽取器
	Command            string   `json:"command,omitempty"`               // 可执行文件名，默认 opencli
	Args               []string `json:"args,omitempty"`                  // 参数模板，支持 {url} / {max_chars} 占位符
	TimeoutSeconds     int      `json:"timeout_seconds,omitempty"`       // 超时时间（秒）
	MaxChars           int      `json:"max_chars,omitempty"`             // 默认最大返回字符数
	FallbackToWebFetch bool     `json:"fallback_to_web_fetch,omitempty"` // OpenCLI 失败后是否回退到 web_fetch
}

// FallbackEntry 是降级链中的一个节点配置
type FallbackEntry struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key,omitempty"`
	APIBase  string `json:"api_base,omitempty"`
	Model    string `json:"model,omitempty"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		LlmProvider: LlmProviderConfig{
			Name:  "openai",
			Model: "gpt-5.4-mini",
		},
		Provider:     "openai",
		Model:        "gpt-5.4-mini",
		SoulPath:     filepath.Join(home, ".luckyharness", "SOUL.md"),
		MaxTokens:    4096,
		Temperature:  0.7,
		Extra:        make(map[string]string),
		ExtraHeaders: make(map[string]string),
		WebSearch: WebSearchConfig{
			Provider:   "brave",
			MaxResults: 5,
		},
		OpenCLI: OpenCLIConfig{
			Enabled:            false,
			Command:            "opencli",
			Args:               []string{"web", "read", "--url", "{url}", "--stdout", "true", "--download-images", "false", "-f", "md"},
			TimeoutSeconds:     20,
			MaxChars:           50000,
			FallbackToWebFetch: true,
		},
		Multimodal: MultimodalConfig{
			Provider:           "openai",
			ImageModel:         "gpt-5.4-mini",
			TranscriptionModel: "whisper-1",
		},
		ImageGeneration: ImageGenerationConfig{
			Provider:     "openai",
			APIBase:      "https://api.openai.com/v1",
			AuthMode:     "bearer",
			Model:        "gpt-image-1.5",
			Size:         "1024x1024",
			Quality:      "auto",
			Background:   "auto",
			OutputFormat: "png",
			Count:        1,
		},
		TTS: TTSConfig{
			Provider: "openai",
			APIBase:  "https://api.openai.com/v1",
			AuthMode: "bearer",
			Model:    "gpt-4o-mini-tts",
			Voice:    "alloy",
			Format:   "mp3",
			Speed:    1.0,
		},
		StreamMode: "native",
		Memory: MemoryConfig{
			ShortTermMaxTurns:   10,
			MidTermExpireDays:   365,
			MidTermMaxSummaries: 100,
		},
		Limits: LimitsConfig{
			MaxTokens:              4096,
			Temperature:            0.7,
			TimeoutSeconds:         60,
			MaxTimeoutSeconds:      600,
			MaxToolCalls:           5,
			MaxConcurrentToolCalls: 3,
		},
		Retry: RetryConfig{
			Enabled:            true,
			MaxAttempts:        3,
			InitialDelayMs:     1000,
			MaxDelayMs:         10000,
			RetryOnRateLimit:   true,
			RetryOnTimeout:     true,
			RetryOnServerError: true,
		},
		CircuitBreaker: CircuitBreakerConfig{
			Enabled:         false,
			ErrorThreshold:  5,
			WindowSeconds:   60,
			TimeoutSeconds:  30,
			HalfOpenMaxReqs: 1,
		},
		RateLimit: RateLimitConfig{
			Enabled:           true,
			RequestsPerMinute: 60,
			TokensPerMinute:   100000,
			BurstSize:         10,
		},
		Context: ContextConfig{
			MaxHistoryTurns:      50,
			MaxContextTokens:     8000,
			CompressionThreshold: 0.8,
		},
		Agent: AgentLoopConfig{
			MaxIterations:          10,
			TimeoutSeconds:         60,
			AutoApprove:            false,
			RepeatToolCallLimit:    3,
			ToolOnlyIterationLimit: 3,
			DuplicateFetchLimit:    1,
			ContextDebug:           false,
			SimpleLocalInspection: SimpleLocalInspectionConfig{
				MaxIterations:          3,
				TimeoutSeconds:         25,
				RepeatToolCallLimit:    2,
				ToolOnlyIterationLimit: 2,
			},
		},
		Server: ServerConfig{
			Addr:        "127.0.0.1:9090",
			EnableCORS:  true,
			CORSOrigins: []string{"*"},
			RateLimit:   60,
			LogLevel:    "info",
			LogFormat:   "text",
		},
		Dashboard: DashboardConfig{
			Addr: ":8765",
		},
		Autonomy: AutonomyConfig{
			Enabled: false,
			Worker: AutonomyWorkerConfig{
				MaxIterations:          300,
				TimeoutSeconds:         300,
				AutoApprove:            boolPtr(true),
				RepeatToolCallLimit:    300,
				ToolOnlyIterationLimit: 300,
				DuplicateFetchLimit:    300,
				DisabledTools:          []string{"autonomy"},
			},
		},
		MsgGateway: MsgGatewayConfig{
			APIAddr: "127.0.0.1:9090",
			Telegram: MsgGatewayTelegram{
				ChatTimeoutSeconds:        600,  // 10 分钟
				ProgressAsMessages:        true, // 默认启用独立步骤消息
				ProgressAsNaturalLanguage: false,
				ShowToolDetailsInResult:   false,
			},
			QQOfficial: MsgGatewayQQOfficial{
				RemoveAt:      true,
				HeartbeatSec:  25,
				ReconnectWait: 5,
				Intents: []string{
					"public_guild_messages",
					"group_and_c2c_messages",
				},
			},
			NapCat: MsgGatewayNapCat{
				ListenAddr:       "127.0.0.1:6701",
				Path:             "/onebot/v11/ws",
				RemoveAt:         true,
				GroupTriggerMode: "mention",
			},
			Weixin: MsgGatewayWeixin{
				BaseURL:                 "https://ilinkai.weixin.qq.com",
				DMPolicy:                "open",
				GroupPolicy:             "disabled",
				PollTimeoutMilliseconds: 35000,
				SendChunkDelayMS:        350,
			},
			OpenClawWeixin: MsgGatewayOpenClawWeixin{
				StateDir:                "",
				DMPolicy:                "open",
				GroupPolicy:             "disabled",
				PollTimeoutMilliseconds: 35000,
				SendChunkDelayMS:        350,
			},
		},
	}
}

func parseConfigData(data []byte) (*Config, error) {
	cfg := DefaultConfig()
	if len(strings.TrimSpace(string(data))) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	normalizeConfig(cfg)
	return cfg, nil
}

func normalizeConfig(cfg *Config) {
	def := DefaultConfig()

	if strings.TrimSpace(cfg.LlmProvider.Name) == "" {
		cfg.LlmProvider.Name = def.LlmProvider.Name
	}
	if strings.TrimSpace(cfg.LlmProvider.Model) == "" {
		cfg.LlmProvider.Model = def.LlmProvider.Model
	}

	cfg.Provider = strings.TrimSpace(cfg.LlmProvider.Name)
	cfg.APIKey = strings.TrimSpace(cfg.LlmProvider.APIKey)
	cfg.APIBase = strings.TrimSpace(cfg.LlmProvider.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.LlmProvider.Model)

	if cfg.SoulPath == "" {
		cfg.SoulPath = def.SoulPath
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = def.MaxTokens
	}
	if cfg.Extra == nil {
		cfg.Extra = make(map[string]string)
	}
	if cfg.ExtraHeaders == nil {
		cfg.ExtraHeaders = make(map[string]string)
	}
	if cfg.WebSearch.Provider == "" {
		cfg.WebSearch.Provider = def.WebSearch.Provider
	}
	if cfg.WebSearch.MaxResults <= 0 {
		cfg.WebSearch.MaxResults = def.WebSearch.MaxResults
	}
	if cfg.Multimodal.Provider == "" {
		cfg.Multimodal.Provider = def.Multimodal.Provider
	}
	if cfg.Multimodal.ImageModel == "" {
		cfg.Multimodal.ImageModel = def.Multimodal.ImageModel
	}
	if cfg.Multimodal.TranscriptionModel == "" {
		cfg.Multimodal.TranscriptionModel = def.Multimodal.TranscriptionModel
	}
	if cfg.ImageGeneration.Provider == "" {
		cfg.ImageGeneration.Provider = def.ImageGeneration.Provider
	}
	if cfg.ImageGeneration.APIBase == "" {
		cfg.ImageGeneration.APIBase = def.ImageGeneration.APIBase
	}
	if cfg.ImageGeneration.AuthMode == "" {
		cfg.ImageGeneration.AuthMode = def.ImageGeneration.AuthMode
	}
	if cfg.ImageGeneration.Model == "" {
		cfg.ImageGeneration.Model = def.ImageGeneration.Model
	}
	if cfg.ImageGeneration.Size == "" {
		cfg.ImageGeneration.Size = def.ImageGeneration.Size
	}
	if cfg.ImageGeneration.Quality == "" {
		cfg.ImageGeneration.Quality = def.ImageGeneration.Quality
	}
	if cfg.ImageGeneration.Background == "" {
		cfg.ImageGeneration.Background = def.ImageGeneration.Background
	}
	if cfg.ImageGeneration.OutputFormat == "" {
		cfg.ImageGeneration.OutputFormat = def.ImageGeneration.OutputFormat
	}
	if cfg.ImageGeneration.APIKey == "" && cfg.ImageGeneration.Provider == "openai" && cfg.Multimodal.APIKey != "" {
		cfg.ImageGeneration.APIKey = cfg.Multimodal.APIKey
	}
	if cfg.ImageGeneration.APIBase == def.ImageGeneration.APIBase && cfg.ImageGeneration.Provider == "openai" && cfg.Multimodal.APIBase != "" {
		cfg.ImageGeneration.APIBase = cfg.Multimodal.APIBase
	}
	if cfg.ImageGeneration.Count <= 0 {
		cfg.ImageGeneration.Count = def.ImageGeneration.Count
	}
	if cfg.TTS.Provider == "" {
		cfg.TTS.Provider = def.TTS.Provider
	}
	if cfg.TTS.APIBase == "" {
		cfg.TTS.APIBase = def.TTS.APIBase
	}
	if cfg.TTS.AuthMode == "" {
		cfg.TTS.AuthMode = def.TTS.AuthMode
	}
	if cfg.TTS.Model == "" {
		cfg.TTS.Model = def.TTS.Model
	}
	if cfg.TTS.Voice == "" {
		cfg.TTS.Voice = def.TTS.Voice
	}
	if cfg.TTS.Format == "" {
		cfg.TTS.Format = def.TTS.Format
	}
	if cfg.TTS.Speed <= 0 {
		cfg.TTS.Speed = def.TTS.Speed
	}
	if cfg.StreamMode == "" {
		cfg.StreamMode = def.StreamMode
	}
	if cfg.Memory.ShortTermMaxTurns <= 0 {
		cfg.Memory.ShortTermMaxTurns = def.Memory.ShortTermMaxTurns
	}
	if cfg.Memory.MidTermExpireDays <= 0 {
		cfg.Memory.MidTermExpireDays = def.Memory.MidTermExpireDays
	}
	if cfg.Memory.MidTermMaxSummaries <= 0 {
		cfg.Memory.MidTermMaxSummaries = def.Memory.MidTermMaxSummaries
	}
	if cfg.ModelRouter.TokenThreshold <= 0 {
		cfg.ModelRouter.TokenThreshold = 500
	}

	if cfg.Limits.MaxTokens <= 0 {
		cfg.Limits.MaxTokens = def.Limits.MaxTokens
	}
	if cfg.Limits.TimeoutSeconds <= 0 {
		cfg.Limits.TimeoutSeconds = def.Limits.TimeoutSeconds
	}
	if cfg.Limits.MaxTimeoutSeconds <= 0 {
		cfg.Limits.MaxTimeoutSeconds = def.Limits.MaxTimeoutSeconds
	}
	if cfg.Limits.MaxToolCalls <= 0 {
		cfg.Limits.MaxToolCalls = def.Limits.MaxToolCalls
	}
	if cfg.Limits.MaxConcurrentToolCalls <= 0 {
		cfg.Limits.MaxConcurrentToolCalls = def.Limits.MaxConcurrentToolCalls
	}

	if cfg.Retry.MaxAttempts <= 0 {
		cfg.Retry.MaxAttempts = def.Retry.MaxAttempts
	}
	if cfg.Retry.InitialDelayMs <= 0 {
		cfg.Retry.InitialDelayMs = def.Retry.InitialDelayMs
	}
	if cfg.Retry.MaxDelayMs <= 0 {
		cfg.Retry.MaxDelayMs = def.Retry.MaxDelayMs
	}

	if cfg.CircuitBreaker.ErrorThreshold <= 0 {
		cfg.CircuitBreaker.ErrorThreshold = def.CircuitBreaker.ErrorThreshold
	}
	if cfg.CircuitBreaker.WindowSeconds <= 0 {
		cfg.CircuitBreaker.WindowSeconds = def.CircuitBreaker.WindowSeconds
	}
	if cfg.CircuitBreaker.TimeoutSeconds <= 0 {
		cfg.CircuitBreaker.TimeoutSeconds = def.CircuitBreaker.TimeoutSeconds
	}
	if cfg.CircuitBreaker.HalfOpenMaxReqs <= 0 {
		cfg.CircuitBreaker.HalfOpenMaxReqs = def.CircuitBreaker.HalfOpenMaxReqs
	}

	if cfg.RateLimit.RequestsPerMinute <= 0 {
		cfg.RateLimit.RequestsPerMinute = def.RateLimit.RequestsPerMinute
	}
	if cfg.RateLimit.TokensPerMinute <= 0 {
		cfg.RateLimit.TokensPerMinute = def.RateLimit.TokensPerMinute
	}
	if cfg.RateLimit.BurstSize <= 0 {
		cfg.RateLimit.BurstSize = def.RateLimit.BurstSize
	}

	if cfg.Context.MaxHistoryTurns <= 0 {
		cfg.Context.MaxHistoryTurns = def.Context.MaxHistoryTurns
	}
	if cfg.Context.MaxContextTokens <= 0 {
		cfg.Context.MaxContextTokens = def.Context.MaxContextTokens
	}
	if cfg.Context.CompressionThreshold <= 0 {
		cfg.Context.CompressionThreshold = def.Context.CompressionThreshold
	}

	if cfg.Agent.MaxIterations <= 0 {
		cfg.Agent.MaxIterations = def.Agent.MaxIterations
	}
	if cfg.Agent.TimeoutSeconds <= 0 {
		cfg.Agent.TimeoutSeconds = def.Agent.TimeoutSeconds
	}
	if cfg.Agent.RepeatToolCallLimit <= 0 {
		cfg.Agent.RepeatToolCallLimit = def.Agent.RepeatToolCallLimit
	}
	if cfg.Agent.ToolOnlyIterationLimit <= 0 {
		cfg.Agent.ToolOnlyIterationLimit = def.Agent.ToolOnlyIterationLimit
	}
	if cfg.Agent.DuplicateFetchLimit <= 0 {
		cfg.Agent.DuplicateFetchLimit = def.Agent.DuplicateFetchLimit
	}
	if cfg.Agent.SimpleLocalInspection.MaxIterations <= 0 {
		cfg.Agent.SimpleLocalInspection.MaxIterations = def.Agent.SimpleLocalInspection.MaxIterations
	}
	if cfg.Agent.SimpleLocalInspection.TimeoutSeconds <= 0 {
		cfg.Agent.SimpleLocalInspection.TimeoutSeconds = def.Agent.SimpleLocalInspection.TimeoutSeconds
	}
	if cfg.Agent.SimpleLocalInspection.RepeatToolCallLimit <= 0 {
		cfg.Agent.SimpleLocalInspection.RepeatToolCallLimit = def.Agent.SimpleLocalInspection.RepeatToolCallLimit
	}
	if cfg.Agent.SimpleLocalInspection.ToolOnlyIterationLimit <= 0 {
		cfg.Agent.SimpleLocalInspection.ToolOnlyIterationLimit = def.Agent.SimpleLocalInspection.ToolOnlyIterationLimit
	}
	if cfg.Autonomy.Worker.MaxIterations <= 0 {
		cfg.Autonomy.Worker.MaxIterations = def.Autonomy.Worker.MaxIterations
	}
	if cfg.Autonomy.Worker.TimeoutSeconds <= 0 {
		cfg.Autonomy.Worker.TimeoutSeconds = def.Autonomy.Worker.TimeoutSeconds
	}
	if cfg.Autonomy.Worker.RepeatToolCallLimit <= 0 {
		cfg.Autonomy.Worker.RepeatToolCallLimit = def.Autonomy.Worker.RepeatToolCallLimit
	}
	if cfg.Autonomy.Worker.ToolOnlyIterationLimit <= 0 {
		cfg.Autonomy.Worker.ToolOnlyIterationLimit = def.Autonomy.Worker.ToolOnlyIterationLimit
	}
	if cfg.Autonomy.Worker.DuplicateFetchLimit <= 0 {
		cfg.Autonomy.Worker.DuplicateFetchLimit = def.Autonomy.Worker.DuplicateFetchLimit
	}
	if cfg.Autonomy.Worker.AutoApprove == nil {
		cfg.Autonomy.Worker.AutoApprove = def.Autonomy.Worker.AutoApprove
	}
	if cfg.Autonomy.Worker.DisabledTools == nil {
		cfg.Autonomy.Worker.DisabledTools = append([]string(nil), def.Autonomy.Worker.DisabledTools...)
	}

	if cfg.Server.Addr == "" {
		cfg.Server.Addr = def.Server.Addr
	}
	if cfg.Server.RateLimit <= 0 {
		cfg.Server.RateLimit = def.Server.RateLimit
	}
	if cfg.Server.LogLevel == "" {
		cfg.Server.LogLevel = def.Server.LogLevel
	}
	if cfg.Server.LogFormat == "" {
		cfg.Server.LogFormat = def.Server.LogFormat
	}
	if len(cfg.Server.CORSOrigins) == 0 {
		cfg.Server.CORSOrigins = append([]string(nil), def.Server.CORSOrigins...)
	}

	if cfg.Dashboard.Addr == "" {
		cfg.Dashboard.Addr = def.Dashboard.Addr
	}

	if cfg.MsgGateway.APIAddr == "" {
		cfg.MsgGateway.APIAddr = def.MsgGateway.APIAddr
	}
	if cfg.MsgGateway.Telegram.ChatTimeoutSeconds <= 0 {
		cfg.MsgGateway.Telegram.ChatTimeoutSeconds = def.MsgGateway.Telegram.ChatTimeoutSeconds
	}
	if !cfg.MsgGateway.Telegram.ShowToolDetailsInResult {
		cfg.MsgGateway.Telegram.ShowToolDetailsInResult = def.MsgGateway.Telegram.ShowToolDetailsInResult
	}
	if cfg.MsgGateway.QQOfficial.HeartbeatSec <= 0 {
		cfg.MsgGateway.QQOfficial.HeartbeatSec = def.MsgGateway.QQOfficial.HeartbeatSec
	}
	if cfg.MsgGateway.QQOfficial.ReconnectWait <= 0 {
		cfg.MsgGateway.QQOfficial.ReconnectWait = def.MsgGateway.QQOfficial.ReconnectWait
	}
	if len(cfg.MsgGateway.QQOfficial.Intents) == 0 {
		cfg.MsgGateway.QQOfficial.Intents = append([]string(nil), def.MsgGateway.QQOfficial.Intents...)
	}
	if strings.TrimSpace(cfg.MsgGateway.NapCat.ListenAddr) == "" {
		cfg.MsgGateway.NapCat.ListenAddr = def.MsgGateway.NapCat.ListenAddr
	}
	if strings.TrimSpace(cfg.MsgGateway.NapCat.Path) == "" {
		cfg.MsgGateway.NapCat.Path = def.MsgGateway.NapCat.Path
	}
	if strings.TrimSpace(cfg.MsgGateway.NapCat.GroupTriggerMode) == "" {
		cfg.MsgGateway.NapCat.GroupTriggerMode = def.MsgGateway.NapCat.GroupTriggerMode
	}
	if cfg.MsgGateway.OpenClawWeixin.PollTimeoutMilliseconds <= 0 {
		cfg.MsgGateway.OpenClawWeixin.PollTimeoutMilliseconds = def.MsgGateway.OpenClawWeixin.PollTimeoutMilliseconds
	}
	if cfg.MsgGateway.OpenClawWeixin.SendChunkDelayMS <= 0 {
		cfg.MsgGateway.OpenClawWeixin.SendChunkDelayMS = def.MsgGateway.OpenClawWeixin.SendChunkDelayMS
	}
	if strings.TrimSpace(cfg.MsgGateway.OpenClawWeixin.DMPolicy) == "" {
		cfg.MsgGateway.OpenClawWeixin.DMPolicy = def.MsgGateway.OpenClawWeixin.DMPolicy
	}
	if strings.TrimSpace(cfg.MsgGateway.OpenClawWeixin.GroupPolicy) == "" {
		cfg.MsgGateway.OpenClawWeixin.GroupPolicy = def.MsgGateway.OpenClawWeixin.GroupPolicy
	}
}

func cloneConfig(in *Config) *Config {
	if in == nil {
		return nil
	}
	cp := *in
	if in.Extra != nil {
		cp.Extra = make(map[string]string, len(in.Extra))
		for k, v := range in.Extra {
			cp.Extra[k] = v
		}
	}
	if in.ExtraHeaders != nil {
		cp.ExtraHeaders = make(map[string]string, len(in.ExtraHeaders))
		for k, v := range in.ExtraHeaders {
			cp.ExtraHeaders[k] = v
		}
	}
	cp.Fallbacks = append([]FallbackEntry(nil), in.Fallbacks...)
	cp.Server.APIKeys = append([]string(nil), in.Server.APIKeys...)
	cp.Server.CORSOrigins = append([]string(nil), in.Server.CORSOrigins...)
	cp.MsgGateway.QQOfficial.AllowedChats = append([]string(nil), in.MsgGateway.QQOfficial.AllowedChats...)
	cp.MsgGateway.QQOfficial.AllowedUsers = append([]string(nil), in.MsgGateway.QQOfficial.AllowedUsers...)
	cp.MsgGateway.QQOfficial.Intents = append([]string(nil), in.MsgGateway.QQOfficial.Intents...)
	cp.MsgGateway.NapCat.AllowedChats = append([]string(nil), in.MsgGateway.NapCat.AllowedChats...)
	cp.MsgGateway.NapCat.AllowedUsers = append([]string(nil), in.MsgGateway.NapCat.AllowedUsers...)
	cp.MsgGateway.Weixin.AllowedUsers = append([]string(nil), in.MsgGateway.Weixin.AllowedUsers...)
	cp.MsgGateway.Weixin.GroupAllowedUsers = append([]string(nil), in.MsgGateway.Weixin.GroupAllowedUsers...)
	cp.MsgGateway.OpenClawWeixin.AllowedUsers = append([]string(nil), in.MsgGateway.OpenClawWeixin.AllowedUsers...)
	cp.MsgGateway.OpenClawWeixin.GroupAllowedUsers = append([]string(nil), in.MsgGateway.OpenClawWeixin.GroupAllowedUsers...)
	if in.Autonomy.Worker.AutoApprove != nil {
		v := *in.Autonomy.Worker.AutoApprove
		cp.Autonomy.Worker.AutoApprove = &v
	}
	if in.Autonomy.Worker.DisabledTools != nil {
		cp.Autonomy.Worker.DisabledTools = append([]string{}, in.Autonomy.Worker.DisabledTools...)
	}
	return &cp
}

// Manager 管理配置的加载和保存
type Manager struct {
	mu      sync.RWMutex
	config  *Config
	homeDir string
	cfgPath string
}

// NewManager 创建配置管理器
func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	return NewManagerWithDir(filepath.Join(home, ".luckyharness"))
}

// NewManagerWithDir 创建指定目录的配置管理器（用于测试隔离）
func NewManagerWithDir(homeDir string) (*Manager, error) {
	// v0.55.1: 统一使用 config.json
	cfgPath := filepath.Join(homeDir, "config.json")

	m := &Manager{
		config:  DefaultConfig(),
		homeDir: homeDir,
		cfgPath: cfgPath,
	}

	return m, nil
}

// Load 从磁盘加载配置
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 使用默认配置
		}
		return fmt.Errorf("read config: %w", err)
	}

	cfg, err := parseConfigData(data)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	m.config = cfg
	return nil
}

// Save 保存配置到磁盘
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.homeDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	normalizeConfig(m.config)
	out := cloneConfig(m.config)

	// v0.55.1: 使用 JSON 格式保存
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(m.cfgPath, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// Get 获取当前配置的只读副本
func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneConfig(m.config)
}

// Set 修改配置项
func (m *Manager) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch key {
	case "provider":
		m.config.LlmProvider.Name = value
		m.config.Provider = value
	case "api_key":
		m.config.LlmProvider.APIKey = value
		m.config.APIKey = value
	case "api_base":
		m.config.LlmProvider.BaseURL = value
		m.config.APIBase = value
	case "model":
		m.config.LlmProvider.Model = value
		m.config.Model = value
	case "embedding.model":
		m.config.Embedding.Model = value
	case "embedding.api_key":
		m.config.Embedding.APIKey = value
	case "embedding.api_base":
		m.config.Embedding.APIBase = value
	case "embedding.dimension":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Embedding.Dimension = n
	case "multimodal.provider":
		m.config.Multimodal.Provider = value
	case "multimodal.api_key":
		m.config.Multimodal.APIKey = value
	case "multimodal.api_base":
		m.config.Multimodal.APIBase = value
	case "multimodal.image_model":
		m.config.Multimodal.ImageModel = value
	case "multimodal.transcription_model":
		m.config.Multimodal.TranscriptionModel = value
	case "multimodal.image_provider":
		m.config.Multimodal.ImageProvider = value
	case "image_generation.provider":
		m.config.ImageGeneration.Provider = value
	case "image_generation.api_key":
		m.config.ImageGeneration.APIKey = value
	case "image_generation.api_base":
		m.config.ImageGeneration.APIBase = value
	case "image_generation.auth_mode":
		m.config.ImageGeneration.AuthMode = value
	case "image_generation.model":
		m.config.ImageGeneration.Model = value
	case "image_generation.size":
		m.config.ImageGeneration.Size = value
	case "image_generation.quality":
		m.config.ImageGeneration.Quality = value
	case "image_generation.background":
		m.config.ImageGeneration.Background = value
	case "image_generation.output_format":
		m.config.ImageGeneration.OutputFormat = value
	case "image_generation.output_compression":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.ImageGeneration.OutputCompression = n
	case "image_generation.count":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.ImageGeneration.Count = n
	case "tts.provider":
		m.config.TTS.Provider = value
	case "tts.api_key":
		m.config.TTS.APIKey = value
	case "tts.api_base":
		m.config.TTS.APIBase = value
	case "tts.auth_mode":
		m.config.TTS.AuthMode = value
	case "tts.model":
		m.config.TTS.Model = value
	case "tts.voice":
		m.config.TTS.Voice = value
	case "tts.format":
		m.config.TTS.Format = value
	case "tts.speed":
		var f float64
		fmt.Sscanf(value, "%f", &f)
		m.config.TTS.Speed = f
	case "soul_path":
		m.config.SoulPath = value
	case "max_tokens":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.MaxTokens = n
	case "temperature":
		var f float64
		fmt.Sscanf(value, "%f", &f)
		m.config.Temperature = f
	// v0.37.0: web_search 子配置
	case "web_search.provider":
		m.config.WebSearch.Provider = value
	case "web_search.api_key":
		m.config.WebSearch.APIKey = value
	case "web_search.base_url":
		m.config.WebSearch.BaseURL = value
	case "web_search.max_results":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.WebSearch.MaxResults = n
	case "web_search.proxy":
		m.config.WebSearch.Proxy = value
	case "opencli.enabled":
		m.config.OpenCLI.Enabled = parseBool(value)
	case "opencli.command":
		m.config.OpenCLI.Command = value
	case "opencli.args":
		m.config.OpenCLI.Args = splitCSV(value)
	case "opencli.timeout_seconds":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.OpenCLI.TimeoutSeconds = n
	case "opencli.max_chars":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.OpenCLI.MaxChars = n
	case "opencli.fallback_to_web_fetch":
		m.config.OpenCLI.FallbackToWebFetch = parseBool(value)
	case "stream_mode":
		m.config.StreamMode = value
	case "agent.max_iterations":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Agent.MaxIterations = n
	case "agent.timeout_seconds":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Agent.TimeoutSeconds = n
	case "agent.auto_approve":
		m.config.Agent.AutoApprove = parseBool(value)
	case "agent.repeat_tool_call_limit":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Agent.RepeatToolCallLimit = n
	case "agent.tool_only_iteration_limit":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Agent.ToolOnlyIterationLimit = n
	case "agent.duplicate_fetch_limit":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Agent.DuplicateFetchLimit = n
	case "agent.context_debug":
		m.config.Agent.ContextDebug = parseBool(value)
	case "agent.simple_local_inspection.max_iterations":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Agent.SimpleLocalInspection.MaxIterations = n
	case "agent.simple_local_inspection.timeout_seconds":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Agent.SimpleLocalInspection.TimeoutSeconds = n
	case "agent.simple_local_inspection.repeat_tool_call_limit":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Agent.SimpleLocalInspection.RepeatToolCallLimit = n
	case "agent.simple_local_inspection.tool_only_iteration_limit":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Agent.SimpleLocalInspection.ToolOnlyIterationLimit = n
	case "server.addr":
		m.config.Server.Addr = value
	case "server.api_keys":
		m.config.Server.APIKeys = splitCSV(value)
	case "server.enable_cors":
		m.config.Server.EnableCORS = parseBool(value)
	case "server.cors_origins":
		m.config.Server.CORSOrigins = splitCSV(value)
	case "server.rate_limit":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Server.RateLimit = n
	case "server.metrics_addr":
		m.config.Server.MetricsAddr = value
	case "server.log_level":
		m.config.Server.LogLevel = value
	case "server.log_format":
		m.config.Server.LogFormat = value
	case "dashboard.addr":
		m.config.Dashboard.Addr = value
	case "autonomy.enabled":
		m.config.Autonomy.Enabled = parseBool(value)
	case "autonomy.worker.max_iterations":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Autonomy.Worker.MaxIterations = n
	case "autonomy.worker.timeout_seconds":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Autonomy.Worker.TimeoutSeconds = n
	case "autonomy.worker.auto_approve":
		v := parseBool(value)
		m.config.Autonomy.Worker.AutoApprove = &v
	case "autonomy.worker.repeat_tool_call_limit":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Autonomy.Worker.RepeatToolCallLimit = n
	case "autonomy.worker.tool_only_iteration_limit":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Autonomy.Worker.ToolOnlyIterationLimit = n
	case "autonomy.worker.duplicate_fetch_limit":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Autonomy.Worker.DuplicateFetchLimit = n
	case "autonomy.worker.disabled_tools":
		m.config.Autonomy.Worker.DisabledTools = splitCSV(value)
	case "msg_gateway.platform":
		m.config.MsgGateway.Platform = value
	case "msg_gateway.start_all":
		m.config.MsgGateway.StartAll = parseBool(value)
	case "msg_gateway.api_addr":
		m.config.MsgGateway.APIAddr = value
	case "msg_gateway.telegram.token":
		m.config.MsgGateway.Telegram.Token = value
	case "msg_gateway.telegram.proxy":
		m.config.MsgGateway.Telegram.Proxy = value
	case "msg_gateway.telegram.chat_timeout_seconds":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.MsgGateway.Telegram.ChatTimeoutSeconds = n
	case "msg_gateway.telegram.progress_as_messages":
		m.config.MsgGateway.Telegram.ProgressAsMessages = parseBool(value)
	case "msg_gateway.telegram.progress_as_natural_language":
		m.config.MsgGateway.Telegram.ProgressAsNaturalLanguage = parseBool(value)
	case "msg_gateway.telegram.progress_summary_with_llm":
		m.config.MsgGateway.Telegram.ProgressSummaryWithLLM = parseBool(value)
	case "msg_gateway.telegram.show_tool_details_in_result":
		m.config.MsgGateway.Telegram.ShowToolDetailsInResult = parseBool(value)
	case "msg_gateway.telegram.show_tool_chain":
		m.config.MsgGateway.Telegram.ShowToolDetailsInResult = parseBool(value)
	case "msg_gateway.qqofficial.app_id":
		m.config.MsgGateway.QQOfficial.AppID = value
	case "msg_gateway.qqofficial.app_secret":
		m.config.MsgGateway.QQOfficial.AppSecret = value
	case "msg_gateway.qqofficial.sandbox":
		m.config.MsgGateway.QQOfficial.Sandbox = parseBool(value)
	case "msg_gateway.qqofficial.api_base_url":
		m.config.MsgGateway.QQOfficial.APIBaseURL = value
	case "msg_gateway.qqofficial.gateway_url":
		m.config.MsgGateway.QQOfficial.GatewayURL = value
	case "msg_gateway.qqofficial.allowed_chats":
		m.config.MsgGateway.QQOfficial.AllowedChats = splitCSV(value)
	case "msg_gateway.qqofficial.allowed_users":
		m.config.MsgGateway.QQOfficial.AllowedUsers = splitCSV(value)
	case "msg_gateway.qqofficial.remove_at":
		m.config.MsgGateway.QQOfficial.RemoveAt = parseBool(value)
	case "msg_gateway.qqofficial.heartbeat_sec":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.MsgGateway.QQOfficial.HeartbeatSec = n
	case "msg_gateway.qqofficial.reconnect_wait_seconds":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.MsgGateway.QQOfficial.ReconnectWait = n
	case "msg_gateway.napcat.listen_addr":
		m.config.MsgGateway.NapCat.ListenAddr = value
	case "msg_gateway.napcat.path":
		m.config.MsgGateway.NapCat.Path = value
	case "msg_gateway.napcat.access_token":
		m.config.MsgGateway.NapCat.AccessToken = value
	case "msg_gateway.napcat.allowed_chats":
		m.config.MsgGateway.NapCat.AllowedChats = splitCSV(value)
	case "msg_gateway.napcat.allowed_users":
		m.config.MsgGateway.NapCat.AllowedUsers = splitCSV(value)
	case "msg_gateway.napcat.remove_at":
		m.config.MsgGateway.NapCat.RemoveAt = parseBool(value)
	case "msg_gateway.napcat.group_trigger_mode":
		m.config.MsgGateway.NapCat.GroupTriggerMode = value
	case "msg_gateway.weixin.token":
		m.config.MsgGateway.Weixin.Token = value
	case "msg_gateway.weixin.account_id":
		m.config.MsgGateway.Weixin.AccountID = value
	case "msg_gateway.weixin.base_url":
		m.config.MsgGateway.Weixin.BaseURL = value
	case "msg_gateway.weixin.dm_policy":
		m.config.MsgGateway.Weixin.DMPolicy = value
	case "msg_gateway.weixin.group_policy":
		m.config.MsgGateway.Weixin.GroupPolicy = value
	case "msg_gateway.weixin.allowed_users":
		m.config.MsgGateway.Weixin.AllowedUsers = splitCSV(value)
	case "msg_gateway.weixin.group_allowed_users":
		m.config.MsgGateway.Weixin.GroupAllowedUsers = splitCSV(value)
	case "msg_gateway.weixin.split_multiline_messages":
		m.config.MsgGateway.Weixin.SplitMultilineMessages = parseBool(value)
	case "msg_gateway.weixin.poll_timeout_ms":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.MsgGateway.Weixin.PollTimeoutMilliseconds = n
	case "msg_gateway.weixin.send_chunk_delay_ms":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.MsgGateway.Weixin.SendChunkDelayMS = n
	case "msg_gateway.openclawweixin.account_id":
		m.config.MsgGateway.OpenClawWeixin.AccountID = value
	case "msg_gateway.openclawweixin.state_dir":
		m.config.MsgGateway.OpenClawWeixin.StateDir = value
	case "msg_gateway.openclawweixin.dm_policy":
		m.config.MsgGateway.OpenClawWeixin.DMPolicy = value
	case "msg_gateway.openclawweixin.group_policy":
		m.config.MsgGateway.OpenClawWeixin.GroupPolicy = value
	case "msg_gateway.openclawweixin.allowed_users":
		m.config.MsgGateway.OpenClawWeixin.AllowedUsers = splitCSV(value)
	case "msg_gateway.openclawweixin.group_allowed_users":
		m.config.MsgGateway.OpenClawWeixin.GroupAllowedUsers = splitCSV(value)
	case "msg_gateway.openclawweixin.split_multiline_messages":
		m.config.MsgGateway.OpenClawWeixin.SplitMultilineMessages = parseBool(value)
	case "msg_gateway.openclawweixin.poll_timeout_ms":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.MsgGateway.OpenClawWeixin.PollTimeoutMilliseconds = n
	case "msg_gateway.openclawweixin.send_chunk_delay_ms":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.MsgGateway.OpenClawWeixin.SendChunkDelayMS = n
	case "msg_gateway.qqofficial.intents":
		m.config.MsgGateway.QQOfficial.Intents = splitCSV(value)
	case "limits.max_tokens":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Limits.MaxTokens = n
	case "limits.temperature":
		var f float64
		fmt.Sscanf(value, "%f", &f)
		m.config.Limits.Temperature = f
	case "limits.timeout_seconds":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Limits.TimeoutSeconds = n
	case "limits.max_timeout_seconds":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Limits.MaxTimeoutSeconds = n
	case "limits.max_tool_calls":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Limits.MaxToolCalls = n
	case "limits.max_concurrent_tool_calls":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Limits.MaxConcurrentToolCalls = n
	case "retry.enabled":
		m.config.Retry.Enabled = parseBool(value)
	case "retry.max_attempts":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Retry.MaxAttempts = n
	case "retry.initial_delay_ms":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Retry.InitialDelayMs = n
	case "retry.max_delay_ms":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Retry.MaxDelayMs = n
	case "retry.retry_on_rate_limit":
		m.config.Retry.RetryOnRateLimit = parseBool(value)
	case "retry.retry_on_timeout":
		m.config.Retry.RetryOnTimeout = parseBool(value)
	case "retry.retry_on_server_error":
		m.config.Retry.RetryOnServerError = parseBool(value)
	case "circuit_breaker.enabled":
		m.config.CircuitBreaker.Enabled = parseBool(value)
	case "circuit_breaker.error_threshold":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.CircuitBreaker.ErrorThreshold = n
	case "circuit_breaker.window_seconds":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.CircuitBreaker.WindowSeconds = n
	case "circuit_breaker.timeout_seconds":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.CircuitBreaker.TimeoutSeconds = n
	case "circuit_breaker.half_open_max_requests":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.CircuitBreaker.HalfOpenMaxReqs = n
	case "rate_limit.enabled":
		m.config.RateLimit.Enabled = parseBool(value)
	case "rate_limit.requests_per_minute":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.RateLimit.RequestsPerMinute = n
	case "rate_limit.tokens_per_minute":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.RateLimit.TokensPerMinute = n
	case "rate_limit.burst_size":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.RateLimit.BurstSize = n
	case "context.max_history_turns":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Context.MaxHistoryTurns = n
	case "context.max_context_tokens":
		var n int
		fmt.Sscanf(value, "%d", &n)
		m.config.Context.MaxContextTokens = n
	case "context.compression_threshold":
		var f float64
		fmt.Sscanf(value, "%f", &f)
		m.config.Context.CompressionThreshold = f
	default:
		if strings.HasPrefix(key, "extra_headers.") {
			headerKey := strings.TrimPrefix(key, "extra_headers.")
			if m.config.ExtraHeaders == nil {
				m.config.ExtraHeaders = make(map[string]string)
			}
			if headerKey != "" {
				m.config.ExtraHeaders[headerKey] = value
				break
			}
		}
		if m.config.Extra == nil {
			m.config.Extra = make(map[string]string)
		}
		m.config.Extra[key] = value
	}
	return nil
}

func parseBool(s string) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(strings.ToLower(s)))
	if err == nil {
		return v
	}
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "1", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		item := strings.TrimSpace(p)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// HomeDir 返回 LuckyHarness 主目录
func (m *Manager) HomeDir() string {
	return m.homeDir
}

// InitHome 初始化主目录结构
func (m *Manager) InitHome() error {
	dirs := []string{
		m.homeDir,
		filepath.Join(m.homeDir, "sessions"),
		filepath.Join(m.homeDir, "memory"),
		filepath.Join(m.homeDir, "memory", "midterm"),
		filepath.Join(m.homeDir, "logs"),
		filepath.Join(m.homeDir, "skills"),
		filepath.Join(m.homeDir, "tokens"),
		filepath.Join(m.homeDir, "rag"),
		filepath.Join(m.homeDir, "workspace"),
		filepath.Join(m.homeDir, "knowledge"),
		filepath.Join(m.homeDir, "knowledge", "final_answers"),
		filepath.Join(m.homeDir, "runtime"),
		filepath.Join(m.homeDir, "data"),
		filepath.Join(m.homeDir, "data", "telegram"),
		filepath.Join(m.homeDir, "description"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	// 写入默认 SOUL.md
	soulPath := filepath.Join(m.homeDir, "SOUL.md")
	if _, err := os.Stat(soulPath); os.IsNotExist(err) {
		defaultSoul := DefaultSoul()
		if err := os.WriteFile(soulPath, []byte(defaultSoul), 0o644); err != nil {
			return fmt.Errorf("write SOUL.md: %w", err)
		}
	}

	manualPath := filepath.Join(m.homeDir, "description", "LUCKYHARNESS_AGENT_MANUAL.md")
	if _, err := os.Stat(manualPath); os.IsNotExist(err) {
		if err := os.WriteFile(manualPath, []byte(DefaultAgentManual()), 0o644); err != nil {
			return fmt.Errorf("write LUCKYHARNESS_AGENT_MANUAL.md: %w", err)
		}
	}

	missionPath := filepath.Join(m.homeDir, "mission.md")
	if _, err := os.Stat(missionPath); os.IsNotExist(err) {
		if err := os.WriteFile(missionPath, []byte(DefaultMission()), 0o644); err != nil {
			return fmt.Errorf("write mission.md: %w", err)
		}
	}

	heartbeatPath := filepath.Join(m.homeDir, "workspace", "HEARTBEAT.md")
	if _, err := os.Stat(heartbeatPath); os.IsNotExist(err) {
		if err := os.WriteFile(heartbeatPath, []byte(DefaultHeartbeat()), 0o600); err != nil {
			return fmt.Errorf("write HEARTBEAT.md: %w", err)
		}
	}

	return nil
}

// MsgGatewayWeixin 个人微信 iLink Bot API 配置
type MsgGatewayWeixin struct {
	Token                   string   `json:"token,omitempty"`
	AccountID               string   `json:"account_id,omitempty"`
	BaseURL                 string   `json:"base_url,omitempty"`
	DMPolicy                string   `json:"dm_policy,omitempty"`
	GroupPolicy             string   `json:"group_policy,omitempty"`
	AllowedUsers            []string `json:"allowed_users,omitempty"`
	GroupAllowedUsers       []string `json:"group_allowed_users,omitempty"`
	SplitMultilineMessages  bool     `json:"split_multiline_messages,omitempty"`
	PollTimeoutMilliseconds int      `json:"poll_timeout_ms,omitempty"`
	SendChunkDelayMS        int      `json:"send_chunk_delay_ms,omitempty"`
}

// MsgGatewayOpenClawWeixin OpenClaw 登录的微信渠道配置
type MsgGatewayOpenClawWeixin struct {
	AccountID               string   `json:"account_id,omitempty"`
	StateDir                string   `json:"state_dir,omitempty"`
	DMPolicy                string   `json:"dm_policy,omitempty"`
	GroupPolicy             string   `json:"group_policy,omitempty"`
	AllowedUsers            []string `json:"allowed_users,omitempty"`
	GroupAllowedUsers       []string `json:"group_allowed_users,omitempty"`
	SplitMultilineMessages  bool     `json:"split_multiline_messages,omitempty"`
	PollTimeoutMilliseconds int      `json:"poll_timeout_ms,omitempty"`
	SendChunkDelayMS        int      `json:"send_chunk_delay_ms,omitempty"`
}
