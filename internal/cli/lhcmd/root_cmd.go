package lhcmd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/logger"
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
		Use:   "lh",
		Short: "LuckyHarness bot-first CLI",
		Long:  "LuckyHarness 精简版命令行入口，面向聊天与消息网关工作流。",
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
		Short: "初始化 LuckyHarness 主目录",
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
			fmt.Printf("LuckyHarness %s\n", buildVersion)
			fmt.Printf("   commit: %s\n", buildCommit)
			fmt.Printf("   date:   %s\n", buildDate)
		},
	}

	msgGatewayCmd := &cobra.Command{
		Use:   "msg-gateway",
		Short: "消息平台网关管理",
	}
	msgGatewayStartCmd := &cobra.Command{
		Use:   "start [--platform telegram --token TOKEN]",
		Short: "启动消息网关",
		RunE:  runMsgGatewayStart,
	}
	msgGatewayStartCmd.Flags().String("platform", "", "平台名称 (telegram, onebot)")
	msgGatewayStartCmd.Flags().String("token", "", "Bot token (Telegram)")
	msgGatewayStartCmd.Flags().String("onebot-api", "", "OneBot HTTP API 地址 (如 http://127.0.0.1:3000)")
	msgGatewayStartCmd.Flags().String("onebot-ws", "", "OneBot WebSocket 事件地址 (如 ws://127.0.0.1:3001)")
	msgGatewayStartCmd.Flags().String("onebot-token", "", "OneBot Access Token")
	msgGatewayStartCmd.Flags().String("onebot-bot-id", "", "OneBot Bot QQ ID")
	msgGatewayStartCmd.Flags().Bool("onebot-typing", true, "OneBot 显示正在输入")
	msgGatewayStartCmd.Flags().Bool("onebot-like", true, "OneBot 收到消息自动点赞")
	msgGatewayStartCmd.Flags().Int("onebot-like-times", 1, "OneBot 点赞次数 (1-10)")
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
	msgGatewayCmd.AddCommand(msgGatewayStartCmd, msgGatewayStopCmd, msgGatewayStatusCmd)

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
	rootCmd.AddCommand(initCmd, chatCmd, configCmd, soulCmd, versionCmd, msgGatewayCmd, ragCmd)

	return rootCmd
}
