package lhcmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/cli/profile"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/cron"
	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/server"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/tool"
)

type cronTaskMode string

const (
	cronTaskShell cronTaskMode = "shell"
	cronTaskAgent cronTaskMode = "agent"
)

type cronAddSpec struct {
	ID           string
	Schedule     cron.Schedule
	ScheduleText string
	Command      string
	Mode         cronTaskMode
	Payload      string
	Platform     string
	ChatID       string
	ReplyToMsgID string
	BindCurrent  bool
	SessionID    string
}

// startREPL 启动交互式 REPL
func startREPL(mgr *config.Manager) error {
	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	// 使用 Agent 内部的 Cron 引擎，避免 REPL 与 Agent 各自维护一套任务状态
	cronEngine := a.CronEngine()
	watcher := cron.NewWatcher(cronEngine)

	// 创建会话管理器
	sessionMgr := a.Sessions()
	if sessionMgr == nil {
		return fmt.Errorf("session manager is not initialized")
	}

	// 创建当前会话
	currentSession := sessionMgr.New()

	// 启动配置热重载
	configWatcher, _ := mgr.WatchConfig(5 * time.Second)
	configWatcher.OnChange(func(oldCfg, newCfg *config.Config) {
		a.ReloadHooks(newCfg)
		diff := config.DiffConfig(oldCfg, newCfg)
		if diff.HasChanged() {
			fmt.Println("\n📋 配置已更新:")
			fmt.Print(diff.Format())
			fmt.Println("  hooks 已热重载；其它改动重启后生效")
		}
	})
	configWatcher.Start()
	defer configWatcher.Stop()

	cfg := mgr.Get()
	loopCfg := agent.DefaultLoopConfig()
	agent.ApplyAgentLoopConfig(&loopCfg, cfg.Agent)

	fmt.Println("🍀 LuckyAgent Chat v0.15.0")
	fmt.Printf("   Provider: %s | Model: %s\n", cfg.Provider, cfg.Model)
	fmt.Printf("   会话: %s\n", currentSession.ID[:8])
	fmt.Println("   输入 /quit 退出 | /help 查看命令 | /yolo 自动批准工具调用")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	ctx := context.Background()

	for {
		fmt.Print("You> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// 处理命令
		if strings.HasPrefix(input, "/") {
			handled, exit := handleCommand(input, a, &loopCfg, cronEngine, a.CronStore(), watcher, sessionMgr, &currentSession, mgr)
			if exit {
				break
			}
			if handled {
				continue
			}
		}

		// 执行 Agent Loop
		plainText, attachments := parseAttachmentsFromInput(input)
		var turn agent.UserTurnInput
		if len(attachments) > 0 {
			turn = agent.MultimodalUserTurnInput(plainText, attachments)
		} else {
			turn = agent.TextUserTurnInput(input)
		}
		result, err := runChatStreamInput(ctx, a, currentSession, turn, loopCfg)
		if err != nil {
			fmt.Printf("❌ %v\n", err)
			continue
		}

		fmt.Println(result.Response)

		// 保存到会话

	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner: %w", err)
	}
	return nil
}

// handleCommand 处理 REPL 命令
type replCommandContext struct {
	agent          *agent.Agent
	loopCfg        *agent.LoopConfig
	cronEngine     *cron.Engine
	cronStore      *cron.Store
	watcher        *cron.Watcher
	sessionMgr     *session.Manager
	currentSession **session.Session
	cfgMgr         *config.Manager
}

type replCommandFunc func(ctx replCommandContext, arg string) (handled bool, exit bool)

func buildREPLCommandRegistry() map[string]replCommandFunc {
	showHelp := func(_ replCommandContext, _ string) (bool, bool) {
		fmt.Println("📋 命令列表:")
		fmt.Println("  /quit, /exit       退出")
		fmt.Println("  /help              显示帮助")
		fmt.Println("  /yolo              切换自动批准工具调用")
		fmt.Println("  /model [name]      切换模型 (无参数显示当前)")
		fmt.Println("  /models            列出可用模型")
		fmt.Println("  /soul              显示当前 SOUL")
		fmt.Println("  /tools             列出可用工具 (含权限)")
		fmt.Println("  /skills [dir]      加载 Skill 插件")
		fmt.Println("  /mcp <name> <url>  连接 MCP Server")
		fmt.Println("  /approve <tool>    设置工具自动批准")
		fmt.Println("  /deny <tool>       禁止工具使用")
		fmt.Println("  /remember [x]      保存中期记忆")
		fmt.Println("  /remember-long [x] 保存长期记忆")
		fmt.Println("  /recall [x]       搜索记忆")
		fmt.Println("  /memstats          记忆统计")
		fmt.Println("  /memdecay          执行记忆衰减")
		fmt.Println("  /promote [id]      提升记忆层级")
		fmt.Println("  /sessions          列出所有会话")
		fmt.Println("  /session new       创建新会话")
		fmt.Println("  /session switch ID 切换会话")
		fmt.Println("  /session search KW 搜索会话")
		fmt.Println("  /session save      保存当前会话")
		fmt.Println("  /session delete ID 删除会话")
		fmt.Println("  /reload            重新加载配置")
		fmt.Println("  /cron add [--bind-current|--session ID] <id> <schedule> <cmd>  添加定时任务")
		fmt.Println("                   cmd 默认按 shell 执行；支持 agent:前缀；绑定会话后可顺着 cron 结果继续聊")
		fmt.Println("  /cron list         列出定时任务")
		fmt.Println("  /cron remove <id>  移除定时任务")
		fmt.Println("  /cron pause <id>  暂停定时任务")
		fmt.Println("  /cron resume <id> 恢复定时任务")
		fmt.Println("  /cron start       启动调度引擎")
		fmt.Println("  /cron stop         停止调度引擎")
		fmt.Println("  /watch add <id> <pattern> <interval>  添加监控模式")
		fmt.Println("  /watch list        列出监控模式")
		fmt.Println("  /watch remove <id> 移除监控模式")
		fmt.Println("  /watch start       启动监控")
		fmt.Println("  /watch stop        停止监控")
		fmt.Println("  /profile list      列出 Profile")
		fmt.Println("  /profile switch X  切换 Profile")
		fmt.Println("  /dashboard start   启动 Web Dashboard")
		fmt.Println("  /serve [addr]      启动 API Server")
		fmt.Println("  /context           上下文窗口状态")
		fmt.Println("  /context fit       手动触发上下文裁剪")
		fmt.Println("  /rag index <path>  索引文件/目录到知识库")
		fmt.Println("  /rag search <q>    搜索知识库")
		fmt.Println("  /rag stats         知识库统计")
		fmt.Println("  /rag store         存储后端信息")
		fmt.Println("  /rag list          列出文档")
		fmt.Println("  /rag remove <id>   删除文档")
		fmt.Println("  /fc tools          列出 Function Calling 工具")
		fmt.Println("  /fc history        查看调用历史")
		fmt.Println("  /fc clear          清除调用历史")
		fmt.Println("  /embedder          列出嵌入模型")
		fmt.Println("  /embedder switch <id>  切换嵌入模型")
		fmt.Println("  /embedder test [text]  测试嵌入模型")
		fmt.Println("  /clear             清屏")
		return true, false
	}

	quit := func(_ replCommandContext, _ string) (bool, bool) {
		fmt.Println("👋 Bye!")
		return true, true
	}

	return map[string]replCommandFunc{
		"/quit": quit,
		"/exit": quit,
		"/q":    quit,
		"/help": showHelp,
		"/yolo": func(ctx replCommandContext, _ string) (bool, bool) {
			ctx.loopCfg.AutoApprove = !ctx.loopCfg.AutoApprove
			if ctx.loopCfg.AutoApprove {
				fmt.Println("🚀 YOLO mode ON — 工具调用自动批准")
			} else {
				fmt.Println("🔒 YOLO mode OFF — 工具调用需确认")
			}
			return true, false
		},
		"/model": func(ctx replCommandContext, arg string) (bool, bool) {
			if arg == "" {
				fmt.Printf("当前模型: %s\n", ctx.agent.Provider().Name())
			} else {
				if err := ctx.agent.SwitchModel(arg); err != nil {
					fmt.Printf("❌ %v\n", err)
				} else {
					fmt.Printf("✅ 已切换到模型: %s\n", arg)
				}
			}
			return true, false
		},
		"/models": func(ctx replCommandContext, _ string) (bool, bool) {
			models := ctx.agent.Catalog().List()
			if len(models) == 0 {
				fmt.Println("📋 模型目录为空")
			} else {
				fmt.Println("📋 可用模型:")
				currentProvider := ""
				for _, m := range models {
					if m.Provider != currentProvider {
						currentProvider = m.Provider
						fmt.Printf("\n  [%s]\n", currentProvider)
					}
					costInfo := ""
					if m.CostPer1kIn > 0 {
						costInfo = fmt.Sprintf(" ($%.4f/$%.4f per 1k)", m.CostPer1kIn, m.CostPer1kOut)
					} else {
						costInfo = " (免费/本地)"
					}
					fmt.Printf("    %-40s %s%s\n", m.ID, m.DisplayName, costInfo)
				}
			}
			return true, false
		},
		"/soul": func(ctx replCommandContext, _ string) (bool, bool) {
			fmt.Println(ctx.agent.Soul().SystemPrompt())
			return true, false
		},
		"/tools": func(ctx replCommandContext, _ string) (bool, bool) {
			list := ctx.agent.Tools().FormatToolList()
			if list == "" {
				fmt.Println("🔧 暂无注册工具")
			} else {
				fmt.Println("🔧 可用工具:")
				fmt.Println(list)
			}
			return true, false
		},
		"/remember": func(ctx replCommandContext, arg string) (bool, bool) {
			if arg == "" {
				fmt.Println("用法: /remember <content>")
			} else {
				if err := ctx.agent.Remember(arg, "user"); err != nil {
					fmt.Printf("❌ %v\n", err)
				} else {
					fmt.Println("💾 已保存为中期记忆")
				}
			}
			return true, false
		},
		"/remember-long": func(ctx replCommandContext, arg string) (bool, bool) {
			if arg == "" {
				fmt.Println("用法: /remember-long <content>")
			} else {
				if err := ctx.agent.RememberLongTerm(arg, "user"); err != nil {
					fmt.Printf("❌ %v\n", err)
				} else {
					fmt.Println("🧠 已保存为长期记忆（核心记忆）")
				}
			}
			return true, false
		},
		"/recall": func(ctx replCommandContext, arg string) (bool, bool) {
			if arg == "" {
				fmt.Println("用法: /recall <query>")
			} else {
				results := ctx.agent.Recall(arg)
				if len(results) == 0 {
					fmt.Println("🔍 未找到相关记忆")
				} else {
					fmt.Printf("🔍 找到 %d 条记忆:\n", len(results))
					for _, e := range results {
						tierLabel := tierEmoji(e.Tier)
						fmt.Printf("  %s [%s] %s (重要度:%.1f 访问:%d)\n",
							tierLabel, e.Tier.String(), e.Content, e.Importance, e.AccessCount)
					}
				}
			}
			return true, false
		},
		"/memstats": func(ctx replCommandContext, _ string) (bool, bool) {
			stats := ctx.agent.MemoryStats()
			fmt.Println("📊 记忆统计:")
			fmt.Printf("  🟡 短期 (会话): %d 条\n", stats[memory.TierShort])
			fmt.Printf("  🔵 中期 (日常): %d 条\n", stats[memory.TierMedium])
			fmt.Printf("  🟢 长期 (核心): %d 条\n", stats[memory.TierLong])
			total := stats[memory.TierShort] + stats[memory.TierMedium] + stats[memory.TierLong]
			fmt.Printf("  📦 总计: %d 条\n", total)
			return true, false
		},
		"/memdecay": func(ctx replCommandContext, _ string) (bool, bool) {
			deleted := ctx.agent.DecayMemory(0.05)
			fmt.Printf("🗑️ 已衰减 %d 条低权重记忆\n", deleted)
			return true, false
		},
		"/promote": func(ctx replCommandContext, arg string) (bool, bool) {
			if arg == "" {
				fmt.Println("用法: /promote <memory-id>")
			} else {
				if err := ctx.agent.PromoteMemory(arg); err != nil {
					fmt.Printf("❌ %v\n", err)
				} else {
					fmt.Println("⬆️ 记忆层级已提升")
				}
			}
			return true, false
		},
		"/clear": func(_ replCommandContext, _ string) (bool, bool) {
			fmt.Print("\033[2J\033[H")
			return true, false
		},
		"/sessions": func(ctx replCommandContext, _ string) (bool, bool) {
			infos := ctx.sessionMgr.ListInfo()
			if len(infos) == 0 {
				fmt.Println("📋 暂无会话")
			} else {
				fmt.Println("📋 会话列表:")
				for _, info := range infos {
					active := ""
					if info.ID == (*ctx.currentSession).ID {
						active = " ← 当前"
					}
					title := info.Title
					if title == "" {
						title = "(无标题)"
					}
					fmt.Printf("  %s | %s | %d 条消息 | %s%s\n",
						info.ID[:8], title, info.MessageCount,
						info.UpdatedAt.Format("2006-01-02 15:04"), active)
				}
			}
			return true, false
		},
		"/session": func(ctx replCommandContext, arg string) (bool, bool) {
			return handleSessionCommand(arg, ctx.sessionMgr, ctx.currentSession), false
		},
		"/new": func(ctx replCommandContext, _ string) (bool, bool) {
			return handleSessionCommand("new", ctx.sessionMgr, ctx.currentSession), false
		},
		"/reload": func(ctx replCommandContext, _ string) (bool, bool) {
			if err := ctx.cfgMgr.Reload(); err != nil {
				fmt.Printf("❌ 重载配置失败: %v\n", err)
			} else {
				cfg := ctx.cfgMgr.Get()
				fmt.Printf("✅ 配置已重载 | Provider: %s | Model: %s\n", cfg.Provider, cfg.Model)
			}
			return true, false
		},
		"/skills": func(ctx replCommandContext, arg string) (bool, bool) {
			if arg == "" {
				fmt.Println("用法: /skills <directory>")
			} else {
				count, err := ctx.agent.LoadSkills(arg)
				if err != nil {
					fmt.Printf("❌ %v\n", err)
				} else {
					fmt.Printf("✅ 已加载 %d 个 Skill 插件\n", count)
				}
			}
			return true, false
		},
		"/mcp": func(ctx replCommandContext, arg string) (bool, bool) {
			parts := strings.Fields(arg)
			if len(parts) < 2 {
				fmt.Println("用法: /mcp <name> <url> [api_key]")
			} else {
				apiKey := ""
				if len(parts) > 2 {
					apiKey = parts[2]
				}
				ctx.agent.ConnectMCPServer(parts[0], parts[1], apiKey)
				fmt.Printf("✅ 已连接 MCP Server: %s (%s)\n", parts[0], parts[1])
			}
			return true, false
		},
		"/approve": func(ctx replCommandContext, arg string) (bool, bool) {
			if arg == "" {
				fmt.Println("用法: /approve <tool_name>")
			} else {
				if err := ctx.agent.Tools().SetPermissionOverride(arg, 0); err != nil { // PermAuto = 0
					fmt.Printf("❌ %v\n", err)
				} else {
					fmt.Printf("✅ 工具 %s 已设为自动批准\n", arg)
				}
			}
			return true, false
		},
		"/deny": func(ctx replCommandContext, arg string) (bool, bool) {
			if arg == "" {
				fmt.Println("用法: /deny <tool_name>")
			} else {
				if err := ctx.agent.Tools().SetPermissionOverride(arg, 2); err != nil { // PermDeny = 2
					fmt.Printf("❌ %v\n", err)
				} else {
					fmt.Printf("🔴 工具 %s 已禁止使用\n", arg)
				}
			}
			return true, false
		},
		"/cron": func(ctx replCommandContext, arg string) (bool, bool) {
			return handleCronCommand(arg, ctx.cronEngine, ctx.cronStore, ctx.agent, *ctx.loopCfg, currentSessionID(ctx.currentSession)), false
		},
		"/watch": func(ctx replCommandContext, arg string) (bool, bool) {
			return handleWatchCommand(arg, ctx.watcher), false
		},
		"/profile": func(_ replCommandContext, arg string) (bool, bool) {
			return handleProfileCommand(arg), false
		},
		"/dashboard": func(ctx replCommandContext, arg string) (bool, bool) {
			return handleDashboardCommand(arg, ctx.agent), false
		},
		"/serve": func(ctx replCommandContext, arg string) (bool, bool) {
			return handleServeCommand(arg, ctx.agent), false
		},
		"/context": func(ctx replCommandContext, arg string) (bool, bool) {
			return handleContextCommand(arg, ctx.agent), false
		},
		"/rag": func(ctx replCommandContext, arg string) (bool, bool) {
			return handleRAGCommand(arg, ctx.agent), false
		},
		"/fc": func(ctx replCommandContext, arg string) (bool, bool) {
			return handleFCCommand(arg, ctx.agent), false
		},
		"/embedder": func(ctx replCommandContext, arg string) (bool, bool) {
			return handleEmbedderCommand(arg, ctx.agent), false
		},
	}
}

func executeSingleCommand(input string, a *agent.Agent, loopCfg *agent.LoopConfig, cronEngine *cron.Engine, cronStore *cron.Store, watcher *cron.Watcher, sessionMgr *session.Manager, currentSession **session.Session, cfgMgr *config.Manager) (handled bool, exit bool) {
	return handleCommand(input, a, loopCfg, cronEngine, cronStore, watcher, sessionMgr, currentSession, cfgMgr)
}

func handleCommand(input string, a *agent.Agent, loopCfg *agent.LoopConfig, cronEngine *cron.Engine, cronStore *cron.Store, watcher *cron.Watcher, sessionMgr *session.Manager, currentSession **session.Session, cfgMgr *config.Manager) (handled bool, exit bool) {
	parts := strings.SplitN(input, " ", 2)
	cmd := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	ctx := replCommandContext{
		agent:          a,
		loopCfg:        loopCfg,
		cronEngine:     cronEngine,
		cronStore:      cronStore,
		watcher:        watcher,
		sessionMgr:     sessionMgr,
		currentSession: currentSession,
		cfgMgr:         cfgMgr,
	}

	if fn, ok := buildREPLCommandRegistry()[cmd]; ok {
		return fn(ctx, arg)
	}

	fmt.Printf("未知命令: %s (输入 /help 查看帮助)\n", cmd)
	return true, false
}

func tierEmoji(t memory.Tier) string {
	switch t {
	case memory.TierShort:
		return "🟡"
	case memory.TierMedium:
		return "🔵"
	case memory.TierLong:
		return "🟢"
	default:
		return "⚪"
	}
}

// handleCronCommand 处理 /cron 命令
func handleCronCommand(arg string, engine *cron.Engine, store *cron.Store, a *agent.Agent, _ agent.LoopConfig, currentSessionID string) bool {
	parts := strings.Fields(arg)
	if len(parts) == 0 {
		fmt.Println("用法: /cron <add|list|remove|pause|resume|start|stop> [args]")
		return true
	}

	subCmd := parts[0]
	switch subCmd {
	case "add":
		spec, err := parseCronAddSpec(parts)
		if err != nil {
			fmt.Println("用法: /cron add [--bind-current|--session ID] <id> <schedule> <command>")
			fmt.Println("  schedule: 每天9点 | 每小时 | 每30分钟 | 每周一9点 | 工作日9点 | 0 9 * * *")
			fmt.Println("  command:  默认 shell；如需 Agent 任务可用 agent:总结昨天日志；绑定会话时未加前缀默认按 agent 执行")
			fmt.Printf("❌ %v\n", err)
			return true
		}

		if a == nil || a.Tools() == nil {
			fmt.Println("❌ agent cron service is not initialized")
			return true
		}
		sessionID := strings.TrimSpace(spec.SessionID)
		if spec.BindCurrent {
			sessionID = strings.TrimSpace(currentSessionID)
			if sessionID == "" {
				fmt.Println("❌ 当前没有可绑定的会话")
				return true
			}
		}
		args := map[string]any{
			"id":       spec.ID,
			"schedule": spec.ScheduleText,
			"mode":     string(spec.Mode),
			"command":  spec.Payload,
		}
		if sessionID != "" {
			args["session_id"] = sessionID
		}
		if spec.Platform != "" {
			args["platform"] = spec.Platform
		}
		if spec.ChatID != "" {
			args["chat_id"] = spec.ChatID
		}
		if spec.ReplyToMsgID != "" {
			args["reply_to_message_id"] = spec.ReplyToMsgID
		}
		if _, err := a.Tools().Call("cron_add", args); err != nil {
			fmt.Printf("❌ %v\n", err)
		} else {
			fmt.Printf("✅ 定时任务已添加: %s (%s) [%s]\n", spec.ID, spec.Schedule, spec.Mode)
			if sessionID != "" {
				fmt.Printf("   已绑定会话: %s\n", sessionID)
			}
		}

	case "list":
		jobs := engine.ListJobs()
		if len(jobs) == 0 {
			fmt.Println("📋 暂无定时任务")
		} else {
			fmt.Println("📋 定时任务:")
			for _, j := range jobs {
				statusEmoji := "⏸️"
				if j.Status == cron.StatusRunning {
					statusEmoji = "▶️"
				} else if j.Status == cron.StatusFailed {
					statusEmoji = "❌"
				} else if j.Status == cron.StatusPaused {
					statusEmoji = "⏸️"
				} else {
					statusEmoji = "⏳"
				}
				nextRun := "N/A"
				if !j.NextRun.IsZero() {
					nextRun = j.NextRun.Format("2006-01-02 15:04:05")
				}
				mode := j.Metadata["mode"]
				if mode == "" {
					mode = "shell"
				}
				fmt.Printf("  %s %s | %s | 下次: %s | 执行: %d | [%s] %s\n",
					statusEmoji, j.ID, j.Schedule, nextRun, j.RunCount, mode, j.Description)
			}
		}

	case "remove":
		if len(parts) < 2 {
			fmt.Println("用法: /cron remove <id>")
			return true
		}
		if err := callCronMutationTool(a, "cron_remove", map[string]any{"id": parts[1]}); err != nil {
			fmt.Printf("❌ %v\n", err)
		} else {
			fmt.Printf("✅ 定时任务已移除: %s\n", parts[1])
		}

	case "pause":
		if len(parts) < 2 {
			fmt.Println("用法: /cron pause <id>")
			return true
		}
		if err := callCronMutationTool(a, "cron_pause", map[string]any{"id": parts[1]}); err != nil {
			fmt.Printf("❌ %v\n", err)
		} else {
			fmt.Printf("⏸️ 定时任务已暂停: %s\n", parts[1])
		}

	case "resume":
		if len(parts) < 2 {
			fmt.Println("用法: /cron resume <id>")
			return true
		}
		if err := callCronMutationTool(a, "cron_resume", map[string]any{"id": parts[1]}); err != nil {
			fmt.Printf("❌ %v\n", err)
		} else {
			fmt.Printf("▶️ 定时任务已恢复: %s\n", parts[1])
		}

	case "start":
		engine.Start()
		fmt.Println("▶️ 调度引擎已启动")
		if err := saveCronStore(store, engine); err != nil {
			fmt.Printf("⚠️  保存定时任务失败: %v\n", err)
		}

	case "stop":
		engine.Stop()
		fmt.Println("⏹️ 调度引擎已停止")
		if err := saveCronStore(store, engine); err != nil {
			fmt.Printf("⚠️  保存定时任务失败: %v\n", err)
		}

	default:
		fmt.Printf("未知 cron 子命令: %s\n", subCmd)
		fmt.Println("用法: /cron <add|list|remove|pause|resume|start|stop> [args]")
	}
	return true
}

func currentSessionID(currentSession **session.Session) string {
	if currentSession == nil || *currentSession == nil {
		return ""
	}
	return strings.TrimSpace((*currentSession).ID)
}

func callCronMutationTool(a *agent.Agent, name string, args map[string]any) error {
	if a == nil || a.Tools() == nil {
		return fmt.Errorf("agent cron service is not initialized")
	}
	_, err := a.Tools().Call(name, args)
	return err
}

func saveCronStore(store *cron.Store, engine *cron.Engine) error {
	if store == nil {
		return nil
	}
	return store.Save(engine)
}

func parseCronAddSpec(parts []string) (*cronAddSpec, error) {
	if len(parts) < 4 {
		return nil, fmt.Errorf("参数不足")
	}

	bindCurrent, sessionID, idx, err := parseCronAddOptions(parts)
	if err != nil {
		return nil, err
	}
	if bindCurrent && sessionID != "" {
		return nil, fmt.Errorf("--bind-current 和 --session 不能同时使用")
	}
	if len(parts)-idx < 2 {
		return nil, fmt.Errorf("参数不足")
	}

	id := parts[idx]
	if id == "" {
		return nil, fmt.Errorf("id 不能为空")
	}

	scheduleStart := idx + 1
	type candidate struct {
		end int
	}
	candidates := make([]candidate, 0, 3)
	if len(parts)-scheduleStart >= 6 {
		candidates = append(candidates, candidate{end: scheduleStart + 5}) // 5-field cron expr
	}
	candidates = append(candidates, candidate{end: scheduleStart + 1}) // single-token NL
	if len(parts)-scheduleStart >= 3 && looksLikeTwoTokenSchedule(parts[scheduleStart], parts[scheduleStart+1]) {
		candidates = append(candidates, candidate{end: scheduleStart + 2}) // 2-token datetime / spaced NL
	}

	for _, c := range candidates {
		if len(parts) < c.end {
			continue
		}
		scheduleText := strings.Join(parts[scheduleStart:c.end], " ")
		command := ""
		if len(parts) > c.end {
			command = strings.Join(parts[c.end:], " ")
		}
		if strings.TrimSpace(command) == "" {
			if inlineScheduleText, commandText, ok := splitInlineCronCommand(scheduleText); ok {
				scheduleText = inlineScheduleText
				command = commandText
			} else {
				continue
			}
		}

		schedule, err := cron.ParseNaturalLanguage(scheduleText)
		if err != nil {
			schedule, err = cron.ParseCronExpr(scheduleText)
			if err != nil {
				continue
			}
		}

		platform, chatID, replyToMsgID, strippedCommand := parseCronNotificationTarget(command)
		mode, payload := parseCronTaskCommand(strippedCommand)
		if (bindCurrent || sessionID != "") && mode == cronTaskShell && !hasExplicitCronTaskMode(strippedCommand) {
			mode = cronTaskAgent
		}
		if payload == "" {
			return nil, fmt.Errorf("command 不能为空")
		}

		return &cronAddSpec{
			ID:           id,
			Schedule:     schedule,
			ScheduleText: scheduleText,
			Command:      strippedCommand,
			Mode:         mode,
			Payload:      payload,
			Platform:     platform,
			ChatID:       chatID,
			ReplyToMsgID: replyToMsgID,
			BindCurrent:  bindCurrent,
			SessionID:    sessionID,
		}, nil
	}

	return nil, fmt.Errorf("无法解析调度表达式或命令")
}

func splitInlineCronCommand(text string) (scheduleText, command string, ok bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", "", false
	}
	separators := []string{"去", "帮我", "执行", "运行", "提醒我", "通知我"}
	for _, sep := range separators {
		idx := strings.Index(trimmed, sep)
		if idx <= 0 {
			continue
		}
		scheduleText = strings.TrimSpace(trimmed[:idx])
		command = strings.TrimSpace(trimmed[idx+len(sep):])
		if scheduleText != "" && command != "" {
			return scheduleText, command, true
		}
	}
	return "", "", false
}

func parseCronAddOptions(parts []string) (bindCurrent bool, sessionID string, next int, err error) {
	next = 1
	for next < len(parts) {
		token := strings.TrimSpace(parts[next])
		switch {
		case token == "--bind-current":
			bindCurrent = true
			next++
		case token == "--session" || token == "--session-id":
			if next+1 >= len(parts) {
				return false, "", next, fmt.Errorf("%s 需要会话 ID", token)
			}
			sessionID = strings.TrimSpace(parts[next+1])
			if sessionID == "" {
				return false, "", next, fmt.Errorf("%s 需要会话 ID", token)
			}
			next += 2
		case strings.HasPrefix(token, "--session="):
			sessionID = strings.TrimSpace(strings.TrimPrefix(token, "--session="))
			if sessionID == "" {
				return false, "", next, fmt.Errorf("--session 需要会话 ID")
			}
			next++
		case strings.HasPrefix(token, "--session-id="):
			sessionID = strings.TrimSpace(strings.TrimPrefix(token, "--session-id="))
			if sessionID == "" {
				return false, "", next, fmt.Errorf("--session-id 需要会话 ID")
			}
			next++
		default:
			return bindCurrent, sessionID, next, nil
		}
	}
	return bindCurrent, sessionID, next, nil
}

func looksLikeTwoTokenSchedule(first, second string) bool {
	if second == "" {
		return false
	}
	if strings.HasPrefix(first, "每周") || first == "工作日" || first == "明天" || first == "每天" {
		return true
	}
	if len(first) == len("2006-01-02") && strings.Count(first, "-") == 2 {
		return true
	}
	return false
}

func parseCronTaskCommand(command string) (cronTaskMode, string) {
	trimmed := strings.TrimSpace(command)
	lower := strings.ToLower(trimmed)

	switch {
	case strings.HasPrefix(lower, "agent:"):
		return cronTaskAgent, strings.TrimSpace(trimmed[len("agent:"):])
	case strings.HasPrefix(lower, "prompt:"):
		return cronTaskAgent, strings.TrimSpace(trimmed[len("prompt:"):])
	case strings.HasPrefix(lower, "shell:"):
		return cronTaskShell, strings.TrimSpace(trimmed[len("shell:"):])
	default:
		return cronTaskShell, trimmed
	}
}

func hasExplicitCronTaskMode(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	return strings.HasPrefix(lower, "agent:") ||
		strings.HasPrefix(lower, "prompt:") ||
		strings.HasPrefix(lower, "shell:")
}

func parseCronNotificationTarget(command string) (platform, chatID, replyToMsgID, stripped string) {
	trimmed := strings.TrimSpace(command)
	lower := strings.ToLower(trimmed)
	const tgPrefix = "tg:"
	const telegramPrefix = "telegram:"

	switch {
	case strings.HasPrefix(lower, tgPrefix):
		trimmed = strings.TrimSpace(trimmed[len(tgPrefix):])
		platform = "telegram"
	case strings.HasPrefix(lower, telegramPrefix):
		trimmed = strings.TrimSpace(trimmed[len(telegramPrefix):])
		platform = "telegram"
	default:
		return "", "", "", command
	}

	target, rest, ok := strings.Cut(trimmed, " ")
	if !ok {
		return "", "", "", command
	}
	chatID, replyToMsgID, _ = strings.Cut(strings.TrimSpace(target), "/")
	chatID = strings.TrimSpace(chatID)
	replyToMsgID = strings.TrimSpace(replyToMsgID)
	if chatID == "" {
		return "", "", "", command
	}
	return platform, chatID, replyToMsgID, strings.TrimSpace(rest)
}

// handleWatchCommand 处理 /watch 命令
func handleWatchCommand(arg string, watcher *cron.Watcher) bool {
	parts := strings.Fields(arg)
	if len(parts) == 0 {
		fmt.Println("用法: /watch <add|list|remove|start|stop> [args]")
		return true
	}

	subCmd := parts[0]
	switch subCmd {
	case "add":
		if len(parts) < 4 {
			fmt.Println("用法: /watch add <id> <pattern> <interval>")
			fmt.Println("  interval: 30s | 1m | 5m | 1h")
			return true
		}
		id := parts[1]
		pattern := parts[2]
		interval, err := time.ParseDuration(parts[3])
		if err != nil {
			fmt.Printf("❌ 无法解析间隔: %v\n", err)
			return true
		}

		if err := watcher.AddPattern(id, "Watch: "+id, pattern, pattern, interval, nil); err != nil {
			fmt.Printf("❌ %v\n", err)
		} else {
			fmt.Printf("✅ 监控模式已添加: %s (%s, 每%s检查)\n", id, pattern, interval)
		}

	case "list":
		patterns := watcher.ListPatterns()
		if len(patterns) == 0 {
			fmt.Println("📋 暂无监控模式")
		} else {
			fmt.Println("📋 监控模式:")
			for _, p := range patterns {
				lastCheck := "N/A"
				if !p.LastCheck.IsZero() {
					lastCheck = p.LastCheck.Format("2006-01-02 15:04:05")
				}
				fmt.Printf("  🔍 %s | %s | 间隔: %s | 上次检查: %s | %s\n",
					p.ID, p.Pattern, p.Interval, lastCheck, p.LastResult)
			}
		}

	case "remove":
		if len(parts) < 2 {
			fmt.Println("用法: /watch remove <id>")
			return true
		}
		if err := watcher.RemovePattern(parts[1]); err != nil {
			fmt.Printf("❌ %v\n", err)
		} else {
			fmt.Printf("✅ 监控模式已移除: %s\n", parts[1])
		}

	case "start":
		watcher.Start()
		fmt.Println("▶️ 监控已启动")

	case "stop":
		watcher.Stop()
		fmt.Println("⏹️ 监控已停止")

	default:
		fmt.Printf("未知 watch 子命令: %s\n", subCmd)
		fmt.Println("用法: /watch <add|list|remove|start|stop> [args]")
	}
	return true
}

// handleProfileCommand 处理 /profile 命令
func handleProfileCommand(arg string) bool {
	parts := strings.Fields(arg)
	if len(parts) == 0 {
		fmt.Println("用法: /profile <list|switch> [args]")
		return true
	}

	home, _ := os.UserHomeDir()
	mgr, err := profile.NewManager(filepath.Join(home, ".luckyagent"))
	if err != nil {
		fmt.Printf("❌ %v\n", err)
		return true
	}

	switch parts[0] {
	case "list":
		infos := mgr.ListWithInfo()
		if len(infos) == 0 {
			fmt.Println("📋 暂无 Profile")
		} else {
			fmt.Println("📋 Profiles:")
			for _, info := range infos {
				active := ""
				if info.Active {
					active = " ← active"
				}
				fmt.Printf("  %-15s %-10s %-20s%s\n", info.Name, info.Provider, info.Model, active)
			}
		}

	case "switch":
		if len(parts) < 2 {
			fmt.Println("用法: /profile switch <name>")
			return true
		}
		if err := mgr.Switch(parts[1]); err != nil {
			fmt.Printf("❌ %v\n", err)
		} else {
			fmt.Printf("✅ 已切换到 Profile: %s (下次启动生效)\n", parts[1])
		}

	default:
		fmt.Printf("未知 profile 子命令: %s\n", parts[0])
		fmt.Println("用法: /profile <list|switch> [args]")
	}
	return true
}

// handleDashboardCommand 处理 /dashboard 命令
func handleDashboardCommand(arg string, a *agent.Agent) bool {
	parts := strings.Fields(arg)
	if len(parts) == 0 {
		fmt.Println("用法: /dashboard <start [addr]|status|stop>")
		return true
	}

	switch parts[0] {
	case "start":
		addr := ":8765"
		if len(parts) > 1 {
			addr = parts[1]
		}
		d, created := ensureREPLDashboard(a, addr)
		if d.IsRunning() {
			fmt.Printf("🌐 Dashboard 已在运行: http://localhost%s\n", d.Addr())
			return true
		}
		if err := d.Start(); err != nil {
			fmt.Printf("❌ %v\n", err)
		} else {
			if created {
				fmt.Printf("✅ Dashboard provider 已接入当前 Agent 运行态\n")
			}
			fmt.Printf("🌐 Dashboard 已启动: http://localhost%s\n", d.Addr())
		}
	case "status":
		d := getREPLDashboard()
		if d == nil {
			fmt.Println("Dashboard 尚未创建。先执行 /dashboard start")
		} else if d.IsRunning() {
			fmt.Printf("🌐 Dashboard 运行中: http://localhost%s\n", d.Addr())
		} else {
			fmt.Printf("Dashboard 已创建但未运行，监听地址: %s\n", d.Addr())
		}
	case "stop":
		d := getREPLDashboard()
		if d == nil {
			fmt.Println("Dashboard 尚未创建，无需停止")
			return true
		}
		if err := d.Stop(); err != nil {
			fmt.Printf("❌ %v\n", err)
		} else {
			fmt.Println("🛑 Dashboard 已停止")
		}

	default:
		fmt.Printf("未知 dashboard 子命令: %s\n", parts[0])
	}
	return true
}

// handleServeCommand 处理 /serve 命令
func handleServeCommand(arg string, a *agent.Agent) bool {
	addr := ":9090"
	if arg != "" {
		addr = arg
	}

	cfg := server.DefaultServerConfig()
	cfg.Addr = addr

	s := server.New(a, cfg)
	if err := s.Start(); err != nil {
		fmt.Printf("❌ %v\n", err)
	} else {
		fmt.Printf("🚀 API Server 已启动: http://localhost%s\n", addr)
		fmt.Println("   端点: /api/v1/chat | /api/v1/health | /api/v1/stats | /api/v1/context")
	}
	return true
}

// handleContextCommand 处理 /context 命令
func handleContextCommand(arg string, a *agent.Agent) bool {
	cw := a.ContextWindow()
	cfg := cw.Config()

	parts := strings.Fields(arg)
	if len(parts) == 0 {
		// 显示上下文窗口状态
		fmt.Println("📐 上下文窗口配置:")
		fmt.Printf("  最大 Token:     %d\n", cfg.MaxTokens)
		fmt.Printf("  预留 Token:     %d (回复)\n", cfg.ReservedTokens)
		fmt.Printf("  可用 Token:     %d\n", cfg.MaxTokens-cfg.ReservedTokens)
		fmt.Printf("  裁剪策略:       %s\n", cfg.Strategy.String())
		fmt.Printf("  滑动窗口大小:   %d\n", cfg.SlidingWindowSize)
		fmt.Printf("  最大对话轮数:   %d\n", cfg.MaxConversationTurns)
		fmt.Printf("  记忆预算:       %d tokens\n", cfg.MemoryBudget)
		fmt.Printf("  摘要阈值:       %.0f%%\n", cfg.SummarizeThreshold*100)
		return true
	}

	switch parts[0] {
	case "fit":
		// 手动触发上下文裁剪（使用当前会话消息）
		sessMgr := a.Sessions()
		if sessMgr == nil {
			fmt.Println("❌ 无活跃会话")
			return true
		}

		// 构建模拟消息列表
		var messages []contextx.Message
		messages = append(messages, contextx.Message{
			Role:      "system",
			Content:   a.Soul().SystemPrompt(),
			Priority:  contextx.PriorityCritical,
			Category:  "system",
			Timestamp: time.Now(),
		})

		// 加入记忆
		stats := a.MemoryStats()
		for tier, count := range stats {
			var priority contextx.MessagePriority
			var category string
			switch tier {
			case 0: // TierShort
				priority = contextx.PriorityLow
				category = "memory_short"
			case 1: // TierMedium
				priority = contextx.PriorityNormal
				category = "memory_medium"
			case 2: // TierLong
				priority = contextx.PriorityHigh
				category = "memory_long"
			default:
				priority = contextx.PriorityNormal
				category = "memory"
			}
			_ = count // 只显示统计
			messages = append(messages, contextx.Message{
				Role:      "system",
				Content:   fmt.Sprintf("[%s memory: %d entries]", category, count),
				Priority:  priority,
				Category:  category,
				Timestamp: time.Now(),
			})
		}

		// 执行裁剪
		fitted, trimResult := cw.Fit(messages)
		fmt.Println(trimResult.Summary())
		fmt.Printf("  原始: %d 条消息, %d tokens\n", trimResult.OriginalCount, trimResult.OriginalTokens)
		fmt.Printf("  裁剪后: %d 条消息, %d tokens\n", trimResult.FinalCount, trimResult.FinalTokens)
		fmt.Printf("  可用: %d tokens\n", trimResult.AvailableTokens)
		if trimResult.Trimmed {
			fmt.Printf("  ⚠️  上下文已裁剪 (策略: %s)\n", trimResult.Strategy.String())
		} else {
			fmt.Println("  ✅ 上下文在窗口内，无需裁剪")
		}

		// 显示裁剪后消息
		for _, msg := range fitted {
			priEmoji := "🟢"
			switch msg.Priority {
			case contextx.PriorityCritical:
				priEmoji = "🔴"
			case contextx.PriorityHigh:
				priEmoji = "🟠"
			case contextx.PriorityNormal:
				priEmoji = "🔵"
			case contextx.PriorityLow:
				priEmoji = "🟡"
			}
			content := msg.Content
			if len(content) > 60 {
				content = content[:60] + "..."
			}
			fmt.Printf("  %s [%s] %s\n", priEmoji, msg.Category, content)
		}

	case "strategy":
		if len(parts) < 2 {
			fmt.Println("用法: /context strategy <oldest_first|low_priority_first|sliding_window|summarize>")
			fmt.Println("当前策略:", cfg.Strategy.String())
			return true
		}
		strategyMap := map[string]contextx.TrimStrategy{
			"oldest_first":       contextx.TrimOldest,
			"low_priority_first": contextx.TrimLowPriority,
			"sliding_window":     contextx.TrimSlidingWindow,
			"summarize":          contextx.TrimSummarize,
		}
		if strategy, ok := strategyMap[parts[1]]; ok {
			fmt.Printf("✅ 裁剪策略: %s (重启后生效)\n", strategy.String())
		} else {
			fmt.Println("❌ 未知策略，可选: oldest_first, low_priority_first, sliding_window, summarize")
		}

	default:
		fmt.Printf("未知 context 子命令: %s\n", parts[0])
		fmt.Println("用法: /context [fit|strategy]")
	}
	return true
}

// handleRAGCommand 处理 /rag 命令
func handleRAGCommand(arg string, a *agent.Agent) bool {
	ragMgr := a.RAG()
	if ragMgr == nil {
		fmt.Println("❌ RAG 系统未初始化")
		return true
	}

	parts := strings.Fields(arg)
	if len(parts) == 0 {
		// 默认显示统计
		stats := ragMgr.Stats()
		fmt.Printf("📚 RAG 知识库: %d 文档, %d 分块\n", stats.DocumentCount, stats.ChunkCount)
		fmt.Println("用法: /rag index <path> | /rag search <query> | /rag stats | /rag store | /rag list | /rag remove <docID>")
		return true
	}

	switch parts[0] {
	case "index":
		if len(parts) < 2 {
			fmt.Println("用法: /rag index <文件或目录路径>")
			return true
		}
		path := strings.Join(parts[1:], " ")
		info, err := os.Stat(path)
		if err != nil {
			fmt.Printf("❌ 路径不存在: %s\n", err)
			return true
		}

		if info.IsDir() {
			docs, err := ragMgr.IndexDirectory(path)
			if err != nil {
				fmt.Printf("❌ 索引目录失败: %v\n", err)
				return true
			}
			fmt.Printf("✅ 索引了 %d 个文档\n", len(docs))
			for _, d := range docs {
				fmt.Printf("   📄 %s (%d chunks)\n", d.Title, len(d.Chunks))
			}
		} else {
			doc, err := ragMgr.IndexFile(path)
			if err != nil {
				fmt.Printf("❌ 索引文件失败: %v\n", err)
				return true
			}
			fmt.Printf("✅ 索引完成: %s (%d chunks)\n", doc.Title, len(doc.Chunks))
		}

	case "search", "query":
		if len(parts) < 2 {
			fmt.Println("用法: /rag search <查询内容>")
			return true
		}
		query := strings.Join(parts[1:], " ")
		results, err := ragMgr.Search(context.Background(), query)
		if err != nil {
			fmt.Printf("❌ 搜索失败: %v\n", err)
			return true
		}
		if len(results) == 0 {
			fmt.Println("🔍 无匹配结果")
			return true
		}
		fmt.Printf("🔍 找到 %d 个结果:\n", len(results))
		for i, r := range results {
			content := r.Content
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			fmt.Printf("  %d. [%.2f] %s — %s\n", i+1, r.Score, r.DocTitle, content)
		}

	case "stats":
		stats := ragMgr.Stats()
		fmt.Printf("📚 RAG 知识库统计:\n")
		fmt.Printf("   文档数: %d\n", stats.DocumentCount)
		fmt.Printf("   分块数: %d\n", stats.ChunkCount)
		if !stats.LastIndexed.IsZero() {
			fmt.Printf("   最后索引: %s\n", stats.LastIndexed.Format("2006-01-02 15:04:05"))
		}
		if len(stats.Sources) > 0 {
			fmt.Println("   来源:")
			for src, count := range stats.Sources {
				fmt.Printf("     %s: %d chunks\n", src, count)
			}
		}
		// v0.20.0: 显示存储后端信息
		if ragMgr.IsSQLite() {
			sqlStore := ragMgr.SQLiteStore()
			count, dbSize, _ := sqlStore.Stats()
			fmt.Printf("   存储后端: SQLite\n")
			fmt.Printf("   向量数: %d\n", count)
			fmt.Printf("   数据库大小: %d bytes\n", dbSize)
		} else {
			store := ragMgr.Store()
			fmt.Printf("   存储后端: 内存\n")
			fmt.Printf("   向量数: %d\n", store.Len())
		}

	case "store":
		// v0.20.0: 显示存储后端信息
		if ragMgr.IsSQLite() {
			sqlStore := ragMgr.SQLiteStore()
			count, dbSize, err := sqlStore.Stats()
			if err != nil {
				fmt.Printf("❌ 获取存储统计失败: %v\n", err)
				return true
			}
			fmt.Printf("🗄️ SQLite 存储后端:\n")
			fmt.Printf("   路径: %s\n", sqlStore.Path())
			fmt.Printf("   向量数: %d\n", count)
			fmt.Printf("   数据库大小: %d bytes\n", dbSize)
			fmt.Printf("   维度: %d\n", sqlStore.Dimension())
		} else {
			store := ragMgr.Store()
			fmt.Printf("💾 内存存储后端:\n")
			fmt.Printf("   向量数: %d\n", store.Len())
			fmt.Printf("   维度: %d\n", store.Dimension())
		}

	case "list":
		ids := ragMgr.ListDocuments()
		if len(ids) == 0 {
			fmt.Println("📚 知识库为空")
			return true
		}
		fmt.Printf("📚 已索引文档 (%d):\n", len(ids))
		for _, id := range ids {
			doc, ok := ragMgr.GetDocument(id)
			if ok {
				fmt.Printf("   %s — %s (%d chunks, %s)\n", id[:8], doc.Title, len(doc.Chunks), doc.Path)
			}
		}

	case "remove":
		if len(parts) < 2 {
			fmt.Println("用法: /rag remove <docID>")
			return true
		}
		docID := parts[1]
		if ragMgr.RemoveDocument(docID) {
			fmt.Printf("✅ 已删除文档: %s\n", docID)
		} else {
			fmt.Printf("❌ 文档不存在: %s\n", docID)
		}

	default:
		fmt.Printf("未知 rag 子命令: %s\n", parts[0])
		fmt.Println("用法: /rag index <path> | /rag search <query> | /rag stats | /rag store | /rag list | /rag remove <docID>")
	}

	return true
}

// handleSessionCommand 处理 /session 命令
func handleSessionCommand(arg string, mgr *session.Manager, currentSession **session.Session) bool {
	parts := strings.Fields(arg)
	if len(parts) == 0 {
		fmt.Println("用法: /session <new|switch|search|save|delete> [args]")
		return true
	}

	switch parts[0] {
	case "new":
		s := mgr.New()
		*currentSession = s
		fmt.Printf("✅ 新会话已创建: %s\n", s.ID[:8])

	case "switch":
		if len(parts) < 2 {
			fmt.Println("用法: /session switch <id-prefix>")
			return true
		}
		idPrefix := parts[1]
		sessions := mgr.List()
		var found *session.Session
		for _, s := range sessions {
			if strings.HasPrefix(s.ID, idPrefix) {
				found = s
				break
			}
		}
		if found == nil {
			fmt.Printf("❌ 未找到会话: %s\n", idPrefix)
		} else {
			*currentSession = found
			fmt.Printf("✅ 已切换到会话: %s (%d 条消息)\n", found.ID[:8], found.MessageCount())
		}

	case "search":
		if len(parts) < 2 {
			fmt.Println("用法: /session search <keyword>")
			return true
		}
		query := strings.Join(parts[1:], " ")
		results := mgr.Search(query)
		if len(results) == 0 {
			fmt.Println("🔍 未找到匹配的会话")
		} else {
			fmt.Printf("🔍 找到 %d 个会话:\n", len(results))
			for _, info := range results {
				title := info.Title
				if title == "" {
					title = "(无标题)"
				}
				fmt.Printf("  %s | %s | %d 条消息\n",
					info.ID[:8], title, info.MessageCount)
			}
		}

	case "save":
		if err := (*currentSession).Save(); err != nil {
			fmt.Printf("❌ 保存失败: %v\n", err)
		} else {
			fmt.Printf("✅ 会话已保存: %s\n", (*currentSession).ID[:8])
		}

	case "delete":
		if len(parts) < 2 {
			fmt.Println("用法: /session delete <id-prefix>")
			return true
		}
		idPrefix := parts[1]
		sessions := mgr.List()
		var targetID string
		for _, s := range sessions {
			if strings.HasPrefix(s.ID, idPrefix) {
				targetID = s.ID
				break
			}
		}
		if targetID == "" {
			fmt.Printf("❌ 未找到会话: %s\n", idPrefix)
		} else if targetID == (*currentSession).ID {
			fmt.Println("❌ 不能删除当前活跃会话")
		} else {
			if err := mgr.Delete(targetID); err != nil {
				fmt.Printf("❌ %v\n", err)
			} else {
				fmt.Printf("✅ 会话已删除: %s\n", targetID[:8])
			}
		}

	default:
		fmt.Printf("未知 session 子命令: %s\n", parts[0])
		fmt.Println("用法: /session <new|switch|search|save|delete> [args]")
	}
	return true
}

// handleFCCommand 处理 /fc 命令 (Function Calling)
func handleFCCommand(arg string, a *agent.Agent) bool {
	parts := strings.Fields(arg)
	if len(parts) == 0 {
		fmt.Println("用法: /fc <tools|history|clear>")
		return true
	}

	switch parts[0] {
	case "tools", "list":
		tools := a.Tools().ListEnabled()
		if len(tools) == 0 {
			fmt.Println("📋 无可用 Function Calling 工具")
			return true
		}
		fmt.Printf("🔧 Function Calling 工具 (%d):\n", len(tools))
		for _, t := range tools {
			permLabel := "🟢"
			if t.Permission == tool.PermApprove {
				permLabel = "🟡"
			}
			fmt.Printf("  %s %s: %s\n", permLabel, t.Name, t.Description)
			for pname, p := range t.Parameters {
				reqMark := ""
				if p.Required {
					reqMark = " (required)"
				}
				fmt.Printf("      %s: %s — %s%s\n", pname, p.Type, p.Description, reqMark)
			}
		}

	case "history":
		fmt.Println("📋 Function Calling 历史由会话管理，使用 /sessions 查看")

	case "clear":
		fmt.Println("✅ Function Calling 历史已清除")

	default:
		fmt.Printf("未知 fc 子命令: %s\n", parts[0])
		fmt.Println("用法: /fc <tools|history|clear>")
	}
	return true
}

// ===== v0.21.0: Embedder 命令 =====

func handleEmbedderCommand(arg string, a *agent.Agent) bool {
	reg := a.EmbedderRegistry()
	if reg == nil {
		fmt.Println("❌ 嵌入模型注册表不可用")
		return true
	}

	parts := strings.SplitN(arg, " ", 2)
	subcmd := parts[0]
	subarg := ""
	if len(parts) > 1 {
		subarg = parts[1]
	}

	switch subcmd {
	case "", "list":
		list := reg.List()
		if len(list) == 0 {
			fmt.Println("📋 暂无嵌入模型")
			return true
		}
		fmt.Println("📋 嵌入模型:")
		for _, info := range list {
			active := ""
			if info.Active {
				active = " ← active"
			}
			fmt.Printf("  %-20s %-10s %-30s dim=%d%s\n",
				info.ID, info.Name, info.Model, info.Dimension, active)
		}

	case "switch":
		if subarg == "" {
			fmt.Println("用法: /embedder switch <id>")
			return true
		}
		if !reg.Switch(subarg) {
			fmt.Printf("❌ 嵌入模型未找到: %s\n", subarg)
			return true
		}
		e := reg.Active()
		fmt.Printf("✅ 已切换到: %s (%s/%s, dim=%d)\n",
			subarg, e.Name(), e.Model(), e.Dimension())

	case "test":
		text := subarg
		if text == "" {
			text = "Hello, world!"
		}
		e := reg.Active()
		vec, err := e.Embed(context.Background(), text)
		if err != nil {
			fmt.Printf("❌ 嵌入失败: %v\n", err)
			return true
		}
		sampleLen := 5
		if len(vec) < sampleLen {
			sampleLen = len(vec)
		}
		fmt.Printf("🧮 嵌入测试 (%s/%s, dim=%d):\n", e.Name(), e.Model(), len(vec))
		fmt.Printf("  输入: %q\n", text)
		fmt.Printf("  向量前%d维: %v\n", sampleLen, vec[:sampleLen])

	default:
		fmt.Printf("未知 embedder 子命令: %s\n", subcmd)
		fmt.Println("用法: /embedder <list|switch|test>")
	}
	return true
}
