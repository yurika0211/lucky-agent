package lhcmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/gateway/qqofficial"
	"github.com/yurika0211/luckyharness/internal/gateway/telegram"
	"github.com/yurika0211/luckyharness/internal/server"
	"github.com/yurika0211/luckyharness/internal/soul"
)

var (
	soulFile  string
	provider_ string
	model_    string
	yolo      bool
)

func runInit(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.InitHome(); err != nil {
		return err
	}
	if err := mgr.Save(); err != nil {
		return err
	}

	fmt.Println("LuckyHarness 初始化完成")
	fmt.Printf("主目录: %s\n", mgr.HomeDir())
	fmt.Println("下一步:")
	fmt.Println("  lh config set api_key sk-xxx")
	fmt.Println("  lh config set provider openai")
	fmt.Println("  lh chat")
	return nil
}

func runChat(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}

	if provider_ != "" {
		_ = mgr.Set("provider", provider_)
	}
	if model_ != "" {
		_ = mgr.Set("model", model_)
	}
	if soulFile != "" {
		_ = mgr.Set("soul_path", soulFile)
	}

	userInput := strings.Join(args, " ")
	if userInput == "" {
		return startREPL(mgr)
	}

	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	loopCfg := agent.DefaultLoopConfig()
	cfg := mgr.Get()
	agent.ApplyAgentLoopConfig(&loopCfg, cfg.Agent)
	if cmd.Flags().Changed("yolo") {
		loopCfg.AutoApprove = yolo
	}

	result, err := a.RunLoop(context.Background(), userInput, loopCfg)
	if err != nil {
		return fmt.Errorf("chat: %w", err)
	}

	fmt.Println(result.Response)
	if len(result.ToolCalls) > 0 {
		fmt.Println()
		for _, tc := range result.ToolCalls {
			fmt.Printf("  %s -> %s\n", tc.Name, truncate(tc.Result, 80))
		}
	}
	return nil
}

func runConfigGet(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}

	cfg := mgr.Get()
	switch args[0] {
	case "provider":
		fmt.Println(cfg.Provider)
	case "api_key":
		fmt.Println(maskKey(cfg.APIKey))
	case "api_base":
		fmt.Println(cfg.APIBase)
	case "model":
		fmt.Println(cfg.Model)
	case "embedding.model":
		fmt.Println(cfg.Embedding.Model)
	case "embedding.api_base":
		fmt.Println(cfg.Embedding.APIBase)
	case "embedding.dimension":
		fmt.Println(cfg.Embedding.Dimension)
	case "embedding.api_key":
		fmt.Println(maskKey(cfg.Embedding.APIKey))
	case "soul_path":
		fmt.Println(cfg.SoulPath)
	case "max_tokens":
		fmt.Println(cfg.MaxTokens)
	case "temperature":
		fmt.Println(cfg.Temperature)
	case "msg_gateway.platform":
		fmt.Println(cfg.MsgGateway.Platform)
	case "msg_gateway.api_addr":
		fmt.Println(cfg.MsgGateway.APIAddr)
	case "msg_gateway.telegram.proxy":
		fmt.Println(cfg.MsgGateway.Telegram.Proxy)
	case "msg_gateway.telegram.progress_summary_with_llm":
		fmt.Println(cfg.MsgGateway.Telegram.ProgressSummaryWithLLM)
	case "msg_gateway.telegram.show_tool_details_in_result", "msg_gateway.telegram.show_tool_chain":
		fmt.Println(cfg.MsgGateway.Telegram.ShowToolDetailsInResult)
	case "msg_gateway.qqofficial.app_id":
		fmt.Println(cfg.MsgGateway.QQOfficial.AppID)
	case "msg_gateway.qqofficial.app_secret":
		fmt.Println(maskKey(cfg.MsgGateway.QQOfficial.AppSecret))
	case "msg_gateway.qqofficial.sandbox":
		fmt.Println(cfg.MsgGateway.QQOfficial.Sandbox)
	case "msg_gateway.qqofficial.api_base_url":
		fmt.Println(cfg.MsgGateway.QQOfficial.APIBaseURL)
	case "msg_gateway.qqofficial.gateway_url":
		fmt.Println(cfg.MsgGateway.QQOfficial.GatewayURL)
	default:
		if v, ok := cfg.Extra[args[0]]; ok {
			fmt.Println(v)
		} else {
			fmt.Println("(未设置)")
		}
	}
	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	if err := mgr.Set(args[0], args[1]); err != nil {
		return err
	}
	if err := mgr.Save(); err != nil {
		return err
	}
	fmt.Printf("%s = %s\n", args[0], args[1])
	return nil
}

func runConfigList(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}

	cfg := mgr.Get()
	fmt.Println("LuckyHarness 配置:")
	fmt.Printf("  provider: %s\n", cfg.Provider)
	fmt.Printf("  api_key: %s\n", maskKey(cfg.APIKey))
	fmt.Printf("  api_base: %s\n", cfg.APIBase)
	fmt.Printf("  model: %s\n", cfg.Model)
	fmt.Printf("  soul_path: %s\n", cfg.SoulPath)
	fmt.Printf("  max_tokens: %d\n", cfg.MaxTokens)
	fmt.Printf("  temperature: %.1f\n", cfg.Temperature)
	return nil
}

func runSoulShow(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	a, err := agent.New(mgr)
	if err != nil {
		return err
	}
	fmt.Println(a.Soul().SystemPrompt())
	return nil
}

func runSoulList(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	a, err := agent.New(mgr)
	if err != nil {
		return err
	}

	tm := a.TemplateManager()
	language, _ := cmd.Flags().GetString("language")

	var templates []*soul.Template
	if language != "" {
		templates = tm.ListByLanguage(language)
	} else {
		templates = tm.ListTemplates()
	}

	if len(templates) == 0 {
		fmt.Println("没有可用的 SOUL 模板")
		return nil
	}

	fmt.Printf("%-20s %-12s %-8s %s\n", "ID", "名称", "语言", "描述")
	fmt.Println(strings.Repeat("-", 70))
	for _, t := range templates {
		desc := t.Description
		if len(desc) > 30 {
			desc = desc[:27] + "..."
		}
		fmt.Printf("%-20s %-12s %-8s %s\n", t.ID, t.Name, t.Language, desc)
	}
	return nil
}

func runSoulSwitch(cmd *cobra.Command, args []string) error {
	templateID := args[0]

	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	a, err := agent.New(mgr)
	if err != nil {
		return err
	}

	tm := a.TemplateManager()
	tmpl, err := tm.GetTemplate(templateID)
	if err != nil {
		return fmt.Errorf("模板 %q 不存在: %w", templateID, err)
	}

	content := tmpl.Render(nil)
	soulPath := mgr.Get().SoulPath
	if soulPath != "" {
		if err := os.WriteFile(soulPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("写入 SOUL 文件失败: %w", err)
		}
	}

	fmt.Printf("已切换到 SOUL 模板: %s (%s)\n", tmpl.Name, soul.LanguageName(tmpl.Language))
	return nil
}

func maskKey(key string) string {
	if key == "" {
		return "(未设置)"
	}
	if len(key) <= 8 {
		return "***"
	}
	return key[:8] + "..."
}

func getAgent() (*agent.Agent, error) {
	mgr, err := config.NewManager()
	if err != nil {
		return nil, err
	}
	if err := mgr.Load(); err != nil {
		return nil, err
	}
	return agent.New(mgr)
}

func runServe(cmd *cobra.Command, args []string) error {
	a, err := getAgent()
	if err != nil {
		return err
	}

	cfg := server.DefaultServerConfig()
	runtimeCfg := a.Config().Get().Server
	if runtimeCfg.Addr != "" {
		cfg.Addr = runtimeCfg.Addr
	}
	if len(runtimeCfg.APIKeys) > 0 {
		cfg.APIKeys = append([]string(nil), runtimeCfg.APIKeys...)
	}
	cfg.EnableCORS = runtimeCfg.EnableCORS
	if len(runtimeCfg.CORSOrigins) > 0 {
		cfg.CORSOrigins = append([]string(nil), runtimeCfg.CORSOrigins...)
	}
	if runtimeCfg.RateLimit > 0 {
		cfg.RateLimit = runtimeCfg.RateLimit
	}
	if runtimeCfg.MetricsAddr != "" {
		cfg.MetricsAddr = runtimeCfg.MetricsAddr
	}
	if runtimeCfg.LogLevel != "" {
		cfg.LogLevel = runtimeCfg.LogLevel
	}
	if runtimeCfg.LogFormat != "" {
		cfg.LogFormat = runtimeCfg.LogFormat
	}

	if cmd.Flags().Changed("addr") {
		addr, _ := cmd.Flags().GetString("addr")
		if addr != "" {
			cfg.Addr = addr
		}
	}

	s := server.New(a, cfg)
	if err := s.Start(); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	return s.Stop()
}

func runMsgGatewayStart(cmd *cobra.Command, args []string) error {
	a, err := getAgent()
	if err != nil {
		return err
	}
	cfg := a.Config().Get()

	gm := a.MsgGateway()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	homeDir := a.Config().HomeDir()
	syncTelegramState := func(registered, connected bool) {
		stats, _ := gm.Stats("telegram")
		_ = gateway.WriteSharedTelegramState(homeDir, gateway.SharedTelegramState{
			PID:              os.Getpid(),
			Registered:       registered,
			Connected:        connected,
			MessagesSent:     stats.MessagesSent,
			MessagesReceived: stats.MessagesReceived,
			Errors:           stats.Errors,
		})
	}

	startAll, _ := cmd.Flags().GetBool("all")
	if !cmd.Flags().Changed("all") {
		startAll = cfg.MsgGateway.StartAll
	}
	if startAll {
		if err := gm.StartAll(ctx); err != nil {
			return err
		}
		fmt.Println("所有消息网关已启动")
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		_ = gm.StopAll()
		return nil
	}

	platform, _ := cmd.Flags().GetString("platform")
	if !cmd.Flags().Changed("platform") && cfg.MsgGateway.Platform != "" {
		platform = cfg.MsgGateway.Platform
	}
	token, _ := cmd.Flags().GetString("token")
	if !cmd.Flags().Changed("token") {
		if cfg.MsgGateway.Telegram.Token != "" {
			token = cfg.MsgGateway.Telegram.Token
		} else if cfg.MsgGateway.Token != "" {
			token = cfg.MsgGateway.Token
		}
	}
	qqAppID, _ := cmd.Flags().GetString("qq-appid")
	if !cmd.Flags().Changed("qq-appid") {
		qqAppID = cfg.MsgGateway.QQOfficial.AppID
	}
	qqAppSecret, _ := cmd.Flags().GetString("qq-appsecret")
	if !cmd.Flags().Changed("qq-appsecret") {
		qqAppSecret = cfg.MsgGateway.QQOfficial.AppSecret
	}
	qqSandbox, _ := cmd.Flags().GetBool("qq-sandbox")
	if !cmd.Flags().Changed("qq-sandbox") {
		qqSandbox = cfg.MsgGateway.QQOfficial.Sandbox
	}

	switch platform {
	case "telegram":
		if token == "" {
			return fmt.Errorf("telegram 需要 --token 参数（或在 config.json 里设置 msg_gateway.telegram.token）")
		}
		tgAdapter := telegram.NewAdapter(telegram.Config{
			Token: token,
			Proxy: cfg.MsgGateway.Telegram.Proxy,
		})
		handler := telegram.NewHandler(tgAdapter, a)
		handler.SetDataDir(filepath.Join(a.Config().HomeDir(), "data", "telegram"))
		tgAdapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
			return handler.HandleMessage(ctx, msg)
		})
		if err := gm.Register(tgAdapter); err != nil {
			return err
		}
		syncTelegramState(true, false)
		if err := gm.Start(ctx, "telegram"); err != nil {
			syncTelegramState(false, false)
			return err
		}
		syncTelegramState(true, true)
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if gw, ok := gm.Get("telegram"); ok {
						syncTelegramState(true, gw.IsRunning())
					} else {
						syncTelegramState(false, false)
					}
				}
			}
		}()
		fmt.Println("Telegram 网关已启动")
	case "qqofficial":
		if strings.TrimSpace(qqAppID) == "" || strings.TrimSpace(qqAppSecret) == "" {
			return fmt.Errorf("qqofficial 需要 --qq-appid 和 --qq-appsecret（或在 config.json 里设置 msg_gateway.qqofficial.app_id / app_secret）")
		}
		qqAdapter := qqofficial.NewAdapter(qqofficial.Config{
			AppID:         qqAppID,
			AppSecret:     qqAppSecret,
			Sandbox:       qqSandbox,
			APIBaseURL:    cfg.MsgGateway.QQOfficial.APIBaseURL,
			GatewayURL:    cfg.MsgGateway.QQOfficial.GatewayURL,
			AllowedChats:  append([]string(nil), cfg.MsgGateway.QQOfficial.AllowedChats...),
			AllowedUsers:  append([]string(nil), cfg.MsgGateway.QQOfficial.AllowedUsers...),
			RemoveAt:      cfg.MsgGateway.QQOfficial.RemoveAt,
			HeartbeatSec:  cfg.MsgGateway.QQOfficial.HeartbeatSec,
			ReconnectWait: cfg.MsgGateway.QQOfficial.ReconnectWait,
			Intents:       append([]string(nil), cfg.MsgGateway.QQOfficial.Intents...),
		})
		handler := qqofficial.NewHandler(qqAdapter, a)
		handler.SetDataDir(filepath.Join(a.Config().HomeDir(), "data", "qqofficial"))
		qqAdapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
			return handler.HandleMessage(ctx, msg)
		})
		if err := gm.Register(qqAdapter); err != nil {
			return err
		}
		if err := gm.Start(ctx, "qqofficial"); err != nil {
			return err
		}
		fmt.Println("QQ 官方机器人网关已启动")
	default:
		if platform == "" {
			return fmt.Errorf("请通过 --platform 指定平台，或在 config.json 设置 msg_gateway.platform")
		}
		return fmt.Errorf("不支持的平台: %s (支持: telegram, qqofficial)", platform)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	_ = gm.StopAll()
	syncTelegramState(false, false)
	return nil
}

func runMsgGatewayStop(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("msg-gateway stop 已禁用：消息网关不再依赖 HTTP API 进行进程外控制。请在运行网关的终端中使用 Ctrl+C 停止")
}

func runMsgGatewayStatus(cmd *cobra.Command, args []string) error {
	fmt.Println("msg-gateway status 已禁用：消息网关不再依赖 HTTP API 暴露状态。请直接查看启动终端日志。")
	return nil
}

func runRAGIndex(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}

	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	ragMgr := a.RAG()
	path := args[0]

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("path not found: %w", err)
	}

	if info.IsDir() {
		docs, err := ragMgr.IndexDirectory(path)
		if err != nil {
			return fmt.Errorf("index directory: %w", err)
		}
		fmt.Printf("Indexed %d documents\n", len(docs))
		for _, d := range docs {
			fmt.Printf("  %s (%d chunks)\n", d.Title, len(d.Chunks))
		}
		return nil
	}

	doc, err := ragMgr.IndexFile(path)
	if err != nil {
		return fmt.Errorf("index file: %w", err)
	}
	fmt.Printf("Indexed: %s (%d chunks)\n", doc.Title, len(doc.Chunks))
	return nil
}

func runRAGSearch(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}

	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	results, err := a.RAG().Search(context.Background(), args[0])
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if len(results) == 0 {
		fmt.Println("No results found")
		return nil
	}

	fmt.Printf("Found %d results:\n", len(results))
	for i, r := range results {
		title := r.DocTitle
		if title == "" {
			if r.DocSource != "" {
				title = r.DocSource
			} else if src := r.Metadata["source"]; src != "" {
				title = src
			} else {
				title = "(unknown source)"
			}
		}
		content := r.Content
		if content == "" {
			content = "(no chunk content cached, chunk_id=" + r.ChunkID + ")"
		}
		if len(content) > 120 {
			content = content[:120] + "..."
		}
		fmt.Printf("  %d. [%.2f] %s - %s\n", i+1, r.Score, title, content)
	}
	return nil
}

func runRAGStats(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}

	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	stats := a.RAG().Stats()
	fmt.Printf("RAG Knowledge Base:\n")
	fmt.Printf("  Documents: %d\n", stats.DocumentCount)
	fmt.Printf("  Chunks: %d\n", stats.ChunkCount)
	if !stats.LastIndexed.IsZero() {
		fmt.Printf("  Last indexed: %s\n", stats.LastIndexed.Format("2006-01-02 15:04:05"))
	}
	if len(stats.Sources) > 0 {
		fmt.Println("  Sources:")
		for src, count := range stats.Sources {
			fmt.Printf("    %s: %d chunks\n", src, count)
		}
	}
	return nil
}
