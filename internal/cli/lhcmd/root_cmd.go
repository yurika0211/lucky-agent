package lhcmd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/logger"
)

type commandStartTimeKey struct{}

var commandLoggerInitOnce sync.Once

func initCommandLogger() {
	commandLoggerInitOnce.Do(func() {
		logCfg := logger.DefaultConfig()

		// 尝试从配置读取日志级别/格式；失败时回退默认值。
		if mgr, err := config.NewManager(); err == nil {
			if err := mgr.Load(); err == nil {
				cfg := mgr.Get()
				if cfg.Server.LogLevel != "" {
					logCfg.Level = cfg.Server.LogLevel
				}
				if cfg.Server.LogFormat != "" {
					logCfg.Format = cfg.Server.LogFormat
				}
			}
		}

		logger.InitLogger(logCfg)
	})
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "la",
		Short: "LuckyAgent bot-first CLI",
		Long:  "LuckyAgent 精简版命令行入口，面向聊天与消息网关工作流。",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isInteractiveTerminal() {
				return cmd.Help()
			}
			return runTUI("", "http://127.0.0.1:9090", "dashboard-main", "")
		},
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			initCommandLogger()

			startAt := time.Now()
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			cmd.SetContext(context.WithValue(ctx, commandStartTimeKey{}, startAt))

			logger.Info("command started",
				"command", cmd.CommandPath(),
				"args_count", len(args),
			)
		},
		PersistentPostRun: func(cmd *cobra.Command, _ []string) {
			var duration time.Duration
			if ctx := cmd.Context(); ctx != nil {
				if startAt, ok := ctx.Value(commandStartTimeKey{}).(time.Time); ok && !startAt.IsZero() {
					duration = time.Since(startAt)
				}
			}

			logger.Info("command completed",
				"command", cmd.CommandPath(),
				"duration_ms", duration.Milliseconds(),
			)
		},
	}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "初始化 LuckyAgent 主目录",
		RunE:  runInit,
	}

	chatCmd := &cobra.Command{
		Use:   "chat [message]",
		Short: "本地调试对话",
		RunE:  runChat,
	}
	chatCmd.Flags().StringVarP(&soulFile, "soul", "s", "", "SOUL.md 文件路径")
	chatCmd.Flags().StringVarP(&provider_, "provider", "p", "", "LLM 提供商")
	chatCmd.Flags().StringVarP(&model_, "model", "m", "", "模型名称")
	chatCmd.Flags().BoolVar(&yolo, "yolo", false, "自动批准所有工具调用")
	chatCmd.Flags().SetInterspersed(false)

	configCmd := &cobra.Command{
		Use:   "config",
		Short: "管理配置",
	}
	configGetCmd := &cobra.Command{
		Use:   "get [key]",
		Short: "获取配置项",
		Args:  cobra.ExactArgs(1),
		RunE:  runConfigGet,
	}
	configSetCmd := &cobra.Command{
		Use:   "set [key] [value]",
		Short: "设置配置项",
		Args:  cobra.ExactArgs(2),
		RunE:  runConfigSet,
	}
	configListCmd := &cobra.Command{
		Use:   "list",
		Short: "列出所有配置",
		RunE:  runConfigList,
	}
	configCmd.AddCommand(configGetCmd, configSetCmd, configListCmd)

	soulCmd := &cobra.Command{
		Use:   "soul",
		Short: "管理 SOUL",
	}
	soulShowCmd := &cobra.Command{
		Use:   "show",
		Short: "显示当前 SOUL",
		RunE:  runSoulShow,
	}
	soulListCmd := &cobra.Command{
		Use:   "list",
		Short: "列出所有 SOUL 模板",
		RunE:  runSoulList,
	}
	soulListCmd.Flags().StringP("language", "l", "", "按语言过滤 (zh/en/ja/ko)")
	soulSwitchCmd := &cobra.Command{
		Use:   "switch <template-id>",
		Short: "切换到指定 SOUL 模板",
		Args:  cobra.ExactArgs(1),
		RunE:  runSoulSwitch,
	}
	soulCmd.AddCommand(soulShowCmd, soulListCmd, soulSwitchCmd)

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "显示版本",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("LuckyAgent %s\n", buildVersion)
			fmt.Printf("   commit: %s\n", buildCommit)
			fmt.Printf("   date:   %s\n", buildDate)
		},
	}

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "启动 HTTP API Server",
		RunE:  runServe,
	}
	serveCmd.Flags().String("addr", "", "监听地址，默认使用 config.json 中的 server.addr")

	msgGatewayCmd := &cobra.Command{
		Use:   "msg-gateway",
		Short: "消息平台网关管理",
	}
	msgGatewayStartCmd := &cobra.Command{
		Use:   "start [--platform telegram --token TOKEN]",
		Short: "启动消息网关",
		RunE:  runMsgGatewayStart,
	}
	msgGatewayStartCmd.Flags().String("platform", "", "平台名称 (telegram, qqofficial, napcat, weixin, openclawweixin)")
	msgGatewayStartCmd.Flags().String("token", "", "Bot token (Telegram)")
	msgGatewayStartCmd.Flags().String("qq-appid", "", "QQ 官方机器人 AppID")
	msgGatewayStartCmd.Flags().String("qq-appsecret", "", "QQ 官方机器人 AppSecret")
	msgGatewayStartCmd.Flags().Bool("qq-sandbox", false, "QQ 官方机器人沙箱环境")
	msgGatewayStartCmd.Flags().String("napcat-listen", "", "NapCat OneBot 反向 WebSocket 监听地址")
	msgGatewayStartCmd.Flags().String("napcat-path", "", "NapCat OneBot 反向 WebSocket 路径")
	msgGatewayStartCmd.Flags().String("napcat-access-token", "", "NapCat OneBot 访问令牌")
	msgGatewayStartCmd.Flags().Bool("all", false, "启动所有已配置的网关")
	msgGatewayStopCmd := &cobra.Command{
		Use:   "stop [platform]",
		Short: "停止消息网关",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runMsgGatewayStop,
	}
	msgGatewayStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "查看消息网关状态",
		RunE:  runMsgGatewayStatus,
	}
	msgGatewayWeixinLoginCmd := &cobra.Command{
		Use:   "weixin-login",
		Short: "获取微信登录二维码并写回 iLink 配置",
		RunE:  runMsgGatewayWeixinLogin,
	}
	msgGatewayWeixinLoginCmd.Flags().String("driver", "ilink", "weixin login driver: ilink or openclaw")
	msgGatewayWeixinLoginCmd.Flags().String("base-url", "", "iLink API base URL")
	msgGatewayWeixinLoginCmd.Flags().Duration("poll-interval", 2*time.Second, "二维码状态轮询间隔")
	msgGatewayWeixinLoginCmd.Flags().Duration("timeout", 3*time.Minute, "扫码登录超时时间")
	msgGatewayWeixinLoginCmd.Flags().Bool("no-save", false, "只打印登录结果，不写入 config.json")
	msgGatewayWeixinLoginCmd.Flags().Bool("print-status", true, "打印二维码状态变化")
	msgGatewayCmd.AddCommand(msgGatewayStartCmd, msgGatewayStopCmd, msgGatewayStatusCmd, msgGatewayWeixinLoginCmd)

	ragCmd := &cobra.Command{
		Use:   "rag",
		Short: "RAG 知识库管理",
	}
	ragIndexCmd := &cobra.Command{
		Use:   "index <path>",
		Short: "索引文件或目录到知识库",
		Args:  cobra.ExactArgs(1),
		RunE:  runRAGIndex,
	}
	ragSearchCmd := &cobra.Command{
		Use:   "search <query>",
		Short: "搜索知识库",
		Args:  cobra.ExactArgs(1),
		RunE:  runRAGSearch,
	}
	ragStatsCmd := &cobra.Command{
		Use:   "stats",
		Short: "知识库统计",
		RunE:  runRAGStats,
	}
	ragCmd.AddCommand(ragIndexCmd, ragSearchCmd, ragStatsCmd)

	memoryCmd := &cobra.Command{
		Use:   "memory",
		Short: "管理 LuckyAgent Markdown 记忆库",
	}
	memoryMigrateGraphCmd := &cobra.Command{
		Use:   "migrate-graph",
		Short: "将现有记忆迁移为 Obsidian-first graph memory",
		RunE:  runMemoryMigrateGraph,
	}
	memoryMigrateGraphCmd.Flags().Bool("apply", false, "实际写入迁移结果；默认只 dry-run")
	memoryMigrateGraphCmd.Flags().Bool("archive-dirty", true, "将 high/critical 脏记忆归档到 90_Archive/dirty")
	memoryMigrateGraphCmd.Flags().Int("limit", 200, "最多审计/展示的脏记忆数量")
	memoryTidalStatsCmd := &cobra.Command{
		Use:   "tidal-stats",
		Short: "查看潮汐记忆 reranker 的持久化统计",
		RunE:  runMemoryTidalStats,
	}
	memoryTidalCmd := &cobra.Command{
		Use:   "tidal",
		Short: "管理潮汐记忆 reranker",
	}
	memoryTidalNestedStatsCmd := &cobra.Command{
		Use:   "stats",
		Short: "查看潮汐记忆 reranker 的持久化统计",
		RunE:  runMemoryTidalStats,
	}
	memoryTidalCmd.AddCommand(memoryTidalNestedStatsCmd)
	memoryCmd.AddCommand(memoryMigrateGraphCmd, memoryTidalStatsCmd, memoryTidalCmd)
	addDashboardCmd(rootCmd)
	addTUICmd(rootCmd)
	addLearnCmd(rootCmd)
	rootCmd.AddCommand(initCmd, chatCmd, configCmd, soulCmd, versionCmd, serveCmd, msgGatewayCmd, ragCmd, memoryCmd)

	return rootCmd
}
