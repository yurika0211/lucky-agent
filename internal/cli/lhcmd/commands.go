package lhcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/cron"
	"github.com/yurika0211/luckyagent/internal/gateway"
	luckycollector "github.com/yurika0211/luckyagent/internal/gateway/collector"
	"github.com/yurika0211/luckyagent/internal/gateway/napcat"
	"github.com/yurika0211/luckyagent/internal/gateway/openclawweixin"
	"github.com/yurika0211/luckyagent/internal/gateway/qqofficial"
	"github.com/yurika0211/luckyagent/internal/gateway/telegram"
	"github.com/yurika0211/luckyagent/internal/gateway/weixin"
	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/proactive"
	"github.com/yurika0211/luckyagent/internal/server"
	"github.com/yurika0211/luckyagent/internal/soul"
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

	fmt.Println("LuckyAgent 初始化完成")
	fmt.Printf("主目录: %s\n", mgr.HomeDir())
	fmt.Println("下一步:")
	fmt.Println("  la config set api_key sk-xxx")
	fmt.Println("  la config set provider openai")
	fmt.Println("  la chat")
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
	trimmedInput := strings.TrimSpace(userInput)
	if userInput == "" {
		return startREPL(mgr)
	}

	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer a.Close()

	loopCfg := agent.DefaultLoopConfig()
	cfg := mgr.Get()
	agent.ApplyAgentLoopConfig(&loopCfg, cfg.Agent)
	if cmd.Flags().Changed("yolo") {
		loopCfg.AutoApprove = yolo
	}

	if strings.HasPrefix(trimmedInput, "/") {
		sessionMgr := a.Sessions()
		if sessionMgr == nil {
			return fmt.Errorf("session manager is not initialized")
		}
		currentSession := sessionMgr.New()
		cronEngine := a.CronEngine()
		cronStore := a.CronStore()
		watcher := cron.NewWatcher(cronEngine)
		handled, exit := executeSingleCommand(trimmedInput, a, &loopCfg, cronEngine, cronStore, watcher, sessionMgr, &currentSession, mgr)
		if !handled {
			return nil
		}
		if exit {
			return nil
		}
		return nil
	}

	sess := a.Sessions().New()
	plainText, attachments := parseAttachmentsFromInput(userInput)
	var turn agent.UserTurnInput
	if len(attachments) > 0 {
		turn = agent.MultimodalUserTurnInput(plainText, attachments)
	} else {
		turn = agent.TextUserTurnInput(userInput)
	}
	result, err := runChatStreamInput(context.Background(), a, sess, turn, loopCfg)
	if err != nil {
		return fmt.Errorf("chat: %w", err)
	}

	fmt.Println(result.Response)
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
	case "multimodal.provider":
		fmt.Println(cfg.Multimodal.Provider)
	case "multimodal.api_key":
		fmt.Println(maskKey(cfg.Multimodal.APIKey))
	case "multimodal.api_base":
		fmt.Println(cfg.Multimodal.APIBase)
	case "multimodal.image_model":
		fmt.Println(cfg.Multimodal.ImageModel)
	case "multimodal.transcription_model":
		fmt.Println(cfg.Multimodal.TranscriptionModel)
	case "multimodal.image_provider":
		fmt.Println(cfg.Multimodal.ImageProvider)
	case "soul_path":
		fmt.Println(cfg.SoulPath)
	case "max_tokens":
		fmt.Println(cfg.MaxTokens)
	case "temperature":
		fmt.Println(cfg.Temperature)
	case "context.memory_hygiene_before_context":
		fmt.Println(cfg.Context.MemoryHygieneBeforeContext)
	case "context.memory_hygiene_action":
		fmt.Println(cfg.Context.MemoryHygieneAction)
	case "context.memory_hygiene_min_severity":
		fmt.Println(cfg.Context.MemoryHygieneMinSeverity)
	case "context.memory_hygiene_max_findings":
		fmt.Println(cfg.Context.MemoryHygieneMaxFindings)
	case "memory.tidal.enabled":
		fmt.Println(cfg.Memory.Tidal.Enabled)
	case "memory.tidal.beta":
		fmt.Println(cfg.Memory.Tidal.Beta)
	case "memory.tidal.max_boost":
		fmt.Println(cfg.Memory.Tidal.MaxBoost)
	case "memory.tidal.learning_rate":
		fmt.Println(cfg.Memory.Tidal.LearningRate)
	case "memory.tidal.min_samples":
		fmt.Println(cfg.Memory.Tidal.MinSamples)
	case "memory.tidal.store_path":
		fmt.Println(cfg.Memory.Tidal.StorePath)
	case "proactive.enabled":
		fmt.Println(cfg.Proactive.Enabled)
	case "proactive.dry_run":
		fmt.Println(proactiveDryRunEnabled(cfg))
	case "proactive.confidence_threshold":
		fmt.Println(cfg.Proactive.ConfidenceThreshold)
	case "proactive.horizon_seconds":
		fmt.Println(cfg.Proactive.HorizonSeconds)
	case "proactive.store_path":
		fmt.Println(cfg.Proactive.StorePath)
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
	case "msg_gateway.napcat.listen_addr":
		fmt.Println(cfg.MsgGateway.NapCat.ListenAddr)
	case "msg_gateway.napcat.path":
		fmt.Println(cfg.MsgGateway.NapCat.Path)
	case "msg_gateway.napcat.access_token":
		fmt.Println(maskKey(cfg.MsgGateway.NapCat.AccessToken))
	case "msg_gateway.napcat.group_trigger_mode":
		fmt.Println(cfg.MsgGateway.NapCat.GroupTriggerMode)
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
	fmt.Println("LuckyAgent 配置:")
	fmt.Printf("  provider: %s\n", cfg.Provider)
	fmt.Printf("  api_key: %s\n", maskKey(cfg.APIKey))
	fmt.Printf("  api_base: %s\n", cfg.APIBase)
	fmt.Printf("  model: %s\n", cfg.Model)
	fmt.Printf("  soul_path: %s\n", cfg.SoulPath)
	fmt.Printf("  max_tokens: %d\n", cfg.MaxTokens)
	fmt.Printf("  temperature: %.1f\n", cfg.Temperature)
	fmt.Printf("  memory.tidal.enabled: %t\n", cfg.Memory.Tidal.Enabled)
	fmt.Printf("  memory.tidal.beta: %.2f\n", cfg.Memory.Tidal.Beta)
	fmt.Printf("  memory.tidal.max_boost: %.2f\n", cfg.Memory.Tidal.MaxBoost)
	fmt.Printf("  memory.tidal.learning_rate: %.2f\n", cfg.Memory.Tidal.LearningRate)
	fmt.Printf("  memory.tidal.min_samples: %d\n", cfg.Memory.Tidal.MinSamples)
	fmt.Printf("  proactive.enabled: %t\n", cfg.Proactive.Enabled)
	fmt.Printf("  proactive.dry_run: %t\n", proactiveDryRunEnabled(cfg))
	fmt.Printf("  proactive.confidence_threshold: %.2f\n", cfg.Proactive.ConfidenceThreshold)
	fmt.Printf("  proactive.horizon_seconds: %d\n", cfg.Proactive.HorizonSeconds)
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

type msgGatewayStartOptions struct {
	StartAll           bool
	Platform           string
	Token              string
	QQAppID            string
	QQAppSecret        string
	QQSandbox          bool
	NapCatListenAddr   string
	NapCatPath         string
	NapCatAccessToken  string
	WeixinToken        string
	WeixinAcct         string
	OpenClawWeixinAcct string
}

type weixinLoginOptions struct {
	Driver       string
	BaseURL      string
	PollInterval time.Duration
	Timeout      time.Duration
	NoSave       bool
	PrintStatus  bool
}

func resolveMsgGatewayStartOptions(cmd *cobra.Command, cfg *config.Config) msgGatewayStartOptions {
	opts := msgGatewayStartOptions{}
	if cmd == nil {
		return opts
	}

	opts.StartAll, _ = cmd.Flags().GetBool("all")
	if !cmd.Flags().Changed("all") && cfg != nil {
		opts.StartAll = cfg.MsgGateway.StartAll
	}

	opts.Platform, _ = cmd.Flags().GetString("platform")
	if !cmd.Flags().Changed("platform") && cfg != nil && cfg.MsgGateway.Platform != "" {
		opts.Platform = cfg.MsgGateway.Platform
	}

	opts.Token, _ = cmd.Flags().GetString("token")
	if !cmd.Flags().Changed("token") && cfg != nil {
		if cfg.MsgGateway.Telegram.Token != "" {
			opts.Token = cfg.MsgGateway.Telegram.Token
		}
	}

	opts.QQAppID, _ = cmd.Flags().GetString("qq-appid")
	if !cmd.Flags().Changed("qq-appid") && cfg != nil {
		opts.QQAppID = cfg.MsgGateway.QQOfficial.AppID
	}

	opts.QQAppSecret, _ = cmd.Flags().GetString("qq-appsecret")
	if !cmd.Flags().Changed("qq-appsecret") && cfg != nil {
		opts.QQAppSecret = cfg.MsgGateway.QQOfficial.AppSecret
	}

	opts.QQSandbox, _ = cmd.Flags().GetBool("qq-sandbox")
	if !cmd.Flags().Changed("qq-sandbox") && cfg != nil {
		opts.QQSandbox = cfg.MsgGateway.QQOfficial.Sandbox
	}

	opts.NapCatListenAddr, _ = cmd.Flags().GetString("napcat-listen")
	if !cmd.Flags().Changed("napcat-listen") && cfg != nil {
		opts.NapCatListenAddr = cfg.MsgGateway.NapCat.ListenAddr
	}
	opts.NapCatPath, _ = cmd.Flags().GetString("napcat-path")
	if !cmd.Flags().Changed("napcat-path") && cfg != nil {
		opts.NapCatPath = cfg.MsgGateway.NapCat.Path
	}
	opts.NapCatAccessToken, _ = cmd.Flags().GetString("napcat-access-token")
	if !cmd.Flags().Changed("napcat-access-token") && cfg != nil {
		opts.NapCatAccessToken = cfg.MsgGateway.NapCat.AccessToken
	}

	if cfg != nil {
		opts.WeixinToken = strings.TrimSpace(cfg.MsgGateway.Weixin.Token)
		opts.WeixinAcct = strings.TrimSpace(cfg.MsgGateway.Weixin.AccountID)
		opts.OpenClawWeixinAcct = strings.TrimSpace(cfg.MsgGateway.OpenClawWeixin.AccountID)
	}

	return opts
}

func validateMsgGatewayStartOptions(opts msgGatewayStartOptions) error {
	if opts.StartAll {
		return nil
	}

	if opts.Platform == "weixin" {
		if strings.TrimSpace(opts.WeixinToken) == "" || strings.TrimSpace(opts.WeixinAcct) == "" {
			return fmt.Errorf("weixin 需要 msg_gateway.weixin.token 和 msg_gateway.weixin.account_id")
		}
		return nil
	}
	if opts.Platform == "openclawweixin" {
		if strings.TrimSpace(opts.OpenClawWeixinAcct) == "" {
			return fmt.Errorf("openclawweixin 需要 msg_gateway.openclawweixin.account_id")
		}
		return nil
	}

	switch opts.Platform {
	case "telegram":
		if strings.TrimSpace(opts.Token) == "" {
			return fmt.Errorf("telegram 需要 --token 参数（或在 config.json 里设置 msg_gateway.telegram.token）")
		}
	case "qqofficial":
		if strings.TrimSpace(opts.QQAppID) == "" || strings.TrimSpace(opts.QQAppSecret) == "" {
			return fmt.Errorf("qqofficial 需要 --qq-appid 和 --qq-appsecret（或在 config.json 里设置 msg_gateway.qqofficial.app_id / app_secret）")
		}
	case "napcat":
		return nil
	default:
		if opts.Platform == "" {
			return fmt.Errorf("请通过 --platform 指定平台，或在 config.json 设置 msg_gateway.platform")
		}
		return fmt.Errorf("不支持的平台: %s (支持: telegram, qqofficial, napcat, weixin, openclawweixin)", opts.Platform)
	}

	return nil
}

func runMsgGatewayWeixinLogin(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	if err := mgr.InitHome(); err != nil {
		return err
	}
	cfg := mgr.Get()

	opts := weixinLoginOptions{
		Driver:       "ilink",
		BaseURL:      strings.TrimSpace(cfg.MsgGateway.Weixin.BaseURL),
		PollInterval: 2 * time.Second,
		Timeout:      3 * time.Minute,
	}
	if v, _ := cmd.Flags().GetString("driver"); strings.TrimSpace(v) != "" {
		opts.Driver = strings.ToLower(strings.TrimSpace(v))
	}
	if v, _ := cmd.Flags().GetString("base-url"); strings.TrimSpace(v) != "" {
		opts.BaseURL = strings.TrimSpace(v)
	}
	if v, _ := cmd.Flags().GetDuration("poll-interval"); v > 0 {
		opts.PollInterval = v
	}
	if v, _ := cmd.Flags().GetDuration("timeout"); v > 0 {
		opts.Timeout = v
	}
	opts.NoSave, _ = cmd.Flags().GetBool("no-save")
	opts.PrintStatus, _ = cmd.Flags().GetBool("print-status")

	if opts.BaseURL == "" {
		opts.BaseURL = "https://ilinkai.weixin.qq.com"
	}
	if opts.Driver == "" {
		opts.Driver = "ilink"
	}
	if opts.Driver == "openclaw" {
		return runMsgGatewayWeixinLoginWithOpenClaw(opts)
	}
	if opts.Driver != "ilink" {
		return fmt.Errorf("unsupported weixin login driver: %s (supported: ilink, openclaw)", opts.Driver)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	client := weixin.NewLoginClient(opts.BaseURL)
	login, err := client.FetchQRCode(ctx)
	if err != nil {
		return err
	}

	fmt.Println("微信登录二维码已生成。")
	if login.QRCodeURL != "" {
		fmt.Printf("请打开扫码链接：%s\n", login.QRCodeURL)
	}
	if login.QRCode != "" && login.QRCodeURL != login.QRCode {
		fmt.Printf("二维码票据：%s\n", login.QRCode)
	}
	if len(login.RawImage) > 0 {
		qrPath := filepath.Join(mgr.HomeDir(), "runtime", "weixin-login-qrcode.png")
		if err := os.WriteFile(qrPath, login.RawImage, 0o600); err == nil {
			fmt.Printf("二维码图片已保存：%s\n", qrPath)
		}
	}
	fmt.Println("请在手机微信中完成扫码并确认登录。")

	if opts.PrintStatus {
		fmt.Println("开始轮询二维码状态...")
	}

	result, err := pollWeixinLogin(ctx, client, login.QRCode, opts.PollInterval, opts.PrintStatus)
	if err != nil {
		return err
	}

	fmt.Println("微信登录成功。")
	fmt.Printf("account_id: %s\n", result.AccountID)
	fmt.Printf("base_url: %s\n", result.BaseURL)

	if opts.NoSave {
		fmt.Printf("token: %s\n", result.Token)
		return nil
	}

	if err := mgr.Set("msg_gateway.weixin.token", result.Token); err != nil {
		return err
	}
	if err := mgr.Set("msg_gateway.weixin.account_id", result.AccountID); err != nil {
		return err
	}
	if strings.TrimSpace(result.BaseURL) != "" {
		if err := mgr.Set("msg_gateway.weixin.base_url", result.BaseURL); err != nil {
			return err
		}
	}
	if err := mgr.Save(); err != nil {
		return err
	}

	fmt.Printf("已写入配置：%s\n", filepath.Join(mgr.HomeDir(), "config.json"))
	return nil
}

func runMsgGatewayWeixinLoginWithOpenClaw(opts weixinLoginOptions) error {
	_ = opts
	fmt.Println("Using OpenClaw Weixin installer for QR login.")
	fmt.Println("This flow installs the OpenClaw Weixin plugin, performs QR login, and restarts OpenClaw Gateway.")
	fmt.Println("LuckyAgent built-in weixin gateway still uses iLink token/account_id mode.")

	cmd := exec.Command("npx", "-y", "@tencent-weixin/openclaw-weixin-cli@latest", "install")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func pollWeixinLogin(ctx context.Context, client *weixin.LoginClient, qrCode string, interval time.Duration, printStatus bool) (*weixin.QRCodeLogin, error) {
	if client == nil {
		return nil, fmt.Errorf("weixin login: client is nil")
	}
	return pollWeixinLoginWithFetcher(ctx, client.GetQRCodeStatus, qrCode, interval, printStatus)
}

func pollWeixinLoginWithFetcher(ctx context.Context, fetch func(context.Context, string) (*weixin.QRCodeLogin, error), qrCode string, interval time.Duration, printStatus bool) (*weixin.QRCodeLogin, error) {
	if fetch == nil {
		return nil, fmt.Errorf("weixin login: fetcher is nil")
	}
	seen := ""
	for {
		login, err := fetch(ctx, qrCode)
		if err != nil {
			return nil, err
		}

		status := strings.TrimSpace(login.Status)
		if printStatus && status != "" && status != seen {
			seen = status
			if strings.TrimSpace(login.Description) != "" {
				fmt.Printf("状态：%s (%s)\n", status, strings.TrimSpace(login.Description))
			} else {
				fmt.Printf("状态：%s\n", status)
			}
		}

		switch {
		case isWeixinLoginSuccessStatus(status):
			if strings.TrimSpace(login.Token) == "" || strings.TrimSpace(login.AccountID) == "" {
				return nil, fmt.Errorf("weixin login: login succeeded but token/account_id missing")
			}
			return login, nil
		case isWeixinLoginFailureStatus(status):
			msg := strings.TrimSpace(login.Description)
			if msg == "" {
				msg = status
			}
			return nil, fmt.Errorf("weixin login: %s", msg)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func isWeixinLoginSuccessStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "confirmed", "confirm", "success", "ok", "logged_in", "login_success":
		return true
	default:
		return false
	}
}

func isWeixinLoginFailureStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "expired", "cancelled", "canceled", "failed", "denied", "rejected":
		return true
	default:
		return false
	}
}

func runServe(cmd *cobra.Command, args []string) error {
	a, err := getAgent()
	if err != nil {
		return err
	}
	stopWatch, _ := a.StartConfigWatch(5 * time.Second)
	defer stopWatch()

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
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	cfg := mgr.Get()
	opts := resolveMsgGatewayStartOptions(cmd, cfg)
	if err := validateMsgGatewayStartOptions(opts); err != nil {
		return err
	}
	if strings.TrimSpace(opts.Platform) != "" {
		if err := mgr.Set("msg_gateway.platform", strings.TrimSpace(opts.Platform)); err != nil {
			return err
		}
	}

	a, err := agent.New(mgr)
	if err != nil {
		return err
	}
	stopWatch, _ := a.StartConfigWatch(5 * time.Second)
	defer stopWatch()

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

	if opts.StartAll {
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

	switch opts.Platform {
	case "telegram":
		tgAdapter := telegram.NewAdapter(telegram.Config{
			Token: opts.Token,
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
		qqAdapter := qqofficial.NewAdapter(qqofficial.Config{
			AppID:         opts.QQAppID,
			AppSecret:     opts.QQAppSecret,
			Sandbox:       opts.QQSandbox,
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
	case "napcat":
		napcatAdapter := napcat.NewAdapter(napcat.Config{
			ListenAddr:       opts.NapCatListenAddr,
			Path:             opts.NapCatPath,
			AccessToken:      opts.NapCatAccessToken,
			AllowedChats:     append([]string(nil), cfg.MsgGateway.NapCat.AllowedChats...),
			AllowedUsers:     append([]string(nil), cfg.MsgGateway.NapCat.AllowedUsers...),
			RemoveAt:         cfg.MsgGateway.NapCat.RemoveAt,
			GroupTriggerMode: cfg.MsgGateway.NapCat.GroupTriggerMode,
		})
		handler := qqofficial.NewHandlerWithOptions(napcatAdapter, a, qqofficial.HandlerOptions{
			PlatformName:    "napcat",
			DisplayName:     "NapCat QQ 网关",
			LogPrefix:       "napcat",
			FinalAnswerOnly: true,
		})
		handler.SetDataDir(filepath.Join(a.Config().HomeDir(), "data", "napcat"))
		napcatAdapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
			return handler.HandleMessage(ctx, msg)
		})
		if err := gm.Register(napcatAdapter); err != nil {
			return err
		}
		if err := gm.Start(ctx, "napcat"); err != nil {
			return err
		}
		napcatPath := strings.TrimSpace(opts.NapCatPath)
		if napcatPath == "" {
			napcatPath = cfg.MsgGateway.NapCat.Path
		}
		if strings.TrimSpace(napcatPath) == "" {
			napcatPath = "/onebot/v11/ws"
		}
		if !strings.HasPrefix(napcatPath, "/") {
			napcatPath = "/" + napcatPath
		}
		fmt.Printf("NapCat QQ 网关已启动，等待 NapCat 连接 ws://%s%s\n", napcatAdapter.ListenAddr(), napcatPath)
	case "weixin":
		wxAdapter := weixin.NewAdapter(weixin.Config{
			Token:                   cfg.MsgGateway.Weixin.Token,
			AccountID:               cfg.MsgGateway.Weixin.AccountID,
			BaseURL:                 cfg.MsgGateway.Weixin.BaseURL,
			DMPolicy:                cfg.MsgGateway.Weixin.DMPolicy,
			GroupPolicy:             cfg.MsgGateway.Weixin.GroupPolicy,
			AllowedUsers:            append([]string(nil), cfg.MsgGateway.Weixin.AllowedUsers...),
			GroupAllowedUsers:       append([]string(nil), cfg.MsgGateway.Weixin.GroupAllowedUsers...),
			SplitMultilineMessages:  cfg.MsgGateway.Weixin.SplitMultilineMessages,
			PollTimeoutMilliseconds: cfg.MsgGateway.Weixin.PollTimeoutMilliseconds,
			SendChunkDelayMS:        cfg.MsgGateway.Weixin.SendChunkDelayMS,
		})
		wxLucky := luckycollector.NewLucky()
		wxAdapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
			return handlePlainGatewayMessageWithLucky(ctx, "weixin", a, wxAdapter, wxLucky, msg, "处理消息失败")
		})
		if err := gm.Register(wxAdapter); err != nil {
			return err
		}
		if err := gm.Start(ctx, "weixin"); err != nil {
			return err
		}
		fmt.Println("Weixin 鄂大・蟾ｲ蜷ｯ蜉ｨ")
	case "openclawweixin":
		wxAdapter := openclawweixin.NewAdapter(openclawweixin.Config{
			AccountID:               cfg.MsgGateway.OpenClawWeixin.AccountID,
			StateDir:                cfg.MsgGateway.OpenClawWeixin.StateDir,
			DMPolicy:                cfg.MsgGateway.OpenClawWeixin.DMPolicy,
			GroupPolicy:             cfg.MsgGateway.OpenClawWeixin.GroupPolicy,
			AllowedUsers:            append([]string(nil), cfg.MsgGateway.OpenClawWeixin.AllowedUsers...),
			GroupAllowedUsers:       append([]string(nil), cfg.MsgGateway.OpenClawWeixin.GroupAllowedUsers...),
			SplitMultilineMessages:  cfg.MsgGateway.OpenClawWeixin.SplitMultilineMessages,
			PollTimeoutMilliseconds: cfg.MsgGateway.OpenClawWeixin.PollTimeoutMilliseconds,
			SendChunkDelayMS:        cfg.MsgGateway.OpenClawWeixin.SendChunkDelayMS,
		})
		wxLucky := luckycollector.NewLucky()
		wxAdapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
			return handlePlainGatewayMessageWithLucky(ctx, "openclawweixin", a, wxAdapter, wxLucky, msg, "openclawweixin gateway error")
		})
		if err := gm.Register(wxAdapter); err != nil {
			return err
		}
		if err := gm.Start(ctx, "openclawweixin"); err != nil {
			return err
		}
		fmt.Println("OpenClaw Weixin gateway started")
	default:
		return validateMsgGatewayStartOptions(opts)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	_ = gm.StopAll()
	syncTelegramState(false, false)
	return nil
}

func handlePlainGatewayMessageWithLucky(ctx context.Context, platform string, runtime *agent.Agent, gw gateway.Gateway, lucky *luckycollector.Lucky, msg *gateway.Message, errorPrefix string) error {
	if msg == nil || runtime == nil || gw == nil {
		return nil
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		platform = gw.Name()
	}
	if lucky == nil {
		lucky = luckycollector.NewLucky()
	}
	if msg.IsCommand && strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(msg.Command), "/"), "lucky") {
		return handlePlainGatewayLuckyCommand(ctx, platform, runtime, gw, lucky, msg, errorPrefix)
	}

	key := luckycollector.KeyForMessage(platform, msg)
	status := lucky.Status(key)
	if status.Active {
		status, err := lucky.Append(key, msg)
		if err != nil {
			return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, "Lucky collection failed: "+err.Error())
		}
		return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, fmt.Sprintf("已收集第 %d 段（附件 %d 个）。发送 /lucky off 提交，/lucky cancel 放弃。", status.SegmentCount, status.AttachmentCount))
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" && len(msg.Attachments) == 0 {
		return nil
	}
	input := agent.MultimodalUserTurnInput(text, msg.Attachments)
	return sendPlainGatewayAgentInput(ctx, platform, runtime, gw, msg, input, errorPrefix)
}

func handlePlainGatewayLuckyCommand(ctx context.Context, platform string, runtime *agent.Agent, gw gateway.Gateway, lucky *luckycollector.Lucky, msg *gateway.Message, errorPrefix string) error {
	action := luckycollector.ParseLuckyAction(msg.Args)
	key := luckycollector.KeyForMessage(platform, msg)

	switch action {
	case luckycollector.LuckyActionOn:
		status, err := lucky.Start(key)
		if errors.Is(err, luckycollector.ErrAlreadyActive) {
			return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, fmt.Sprintf("Lucky 已经在收集中：当前 %d 段，附件 %d 个。发送 /lucky off 提交，/lucky cancel 放弃。", status.SegmentCount, status.AttachmentCount))
		}
		if err != nil {
			return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, "Lucky 开启失败："+err.Error())
		}
		return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, "Lucky 已开启。接下来发送的多段消息会先被收集；发送 /lucky off 后再统一交给 agent。")
	case luckycollector.LuckyActionOff:
		batch, err := lucky.Finish(key)
		if errors.Is(err, luckycollector.ErrInactive) {
			return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, "当前没有正在进行的 Lucky 收集。发送 /lucky on 开始。")
		}
		if errors.Is(err, luckycollector.ErrEmptyBatch) {
			return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, "没有收集到消息，已退出 Lucky 收集模式。")
		}
		if err != nil {
			return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, "Lucky 提交失败："+err.Error())
		}
		return sendPlainGatewayAgentInput(ctx, platform, runtime, gw, msg, batch.UserTurnInput(), errorPrefix)
	case luckycollector.LuckyActionStatus:
		status := lucky.Status(key)
		if !status.Active {
			return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, "Lucky 未开启。发送 /lucky on 开始收集多段消息。")
		}
		return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, fmt.Sprintf("Lucky 正在收集：%d 段，附件 %d 个。发送 /lucky off 提交，/lucky cancel 放弃。", status.SegmentCount, status.AttachmentCount))
	case luckycollector.LuckyActionCancel:
		status, ok := lucky.Cancel(key)
		if !ok {
			return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, "当前没有正在进行的 Lucky 收集。")
		}
		return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, fmt.Sprintf("已取消 Lucky 收集，丢弃 %d 段消息和 %d 个附件。", status.SegmentCount, status.AttachmentCount))
	default:
		return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, "用法：/lucky on | /lucky off | /lucky status | /lucky cancel")
	}
}

func sendPlainGatewayAgentInput(ctx context.Context, platform string, runtime *agent.Agent, gw gateway.Gateway, msg *gateway.Message, input agent.UserTurnInput, errorPrefix string) error {
	sessionID := platform + ":" + msg.Chat.ID
	if sessions := runtime.Sessions(); sessions != nil {
		sessions.Ensure(sessionID)
	}
	reply, err := runtime.ChatWithSessionInput(ctx, sessionID, input)
	if err != nil {
		prefix := strings.TrimSpace(errorPrefix)
		if prefix == "" {
			prefix = "处理消息失败"
		}
		return gw.Send(ctx, msg.Chat.ID, prefix+": "+err.Error())
	}
	return gw.SendWithReply(ctx, msg.Chat.ID, msg.ID, reply)
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

func runMemoryMigrateGraph(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}

	apply, _ := cmd.Flags().GetBool("apply")
	archiveDirty, _ := cmd.Flags().GetBool("archive-dirty")
	limit, _ := cmd.Flags().GetInt("limit")

	store, err := memory.NewStore(filepath.Join(mgr.HomeDir(), "memory"))
	if err != nil {
		return fmt.Errorf("open memory store: %w", err)
	}
	report, err := store.MigrateGraphMemory(memory.GraphMigrationOptions{
		Apply:            apply,
		ArchiveDirty:     archiveDirty,
		MaxDirtyFindings: limit,
	})
	if err != nil {
		return err
	}

	if apply {
		fmt.Println("Memory graph migration applied.")
	} else {
		fmt.Println("Memory graph migration dry-run. Re-run with --apply to write changes.")
	}
	fmt.Printf("  Scanned: %d\n", report.Scanned)
	fmt.Printf("  Link updates: %d", report.WouldUpdateLinks)
	if apply {
		fmt.Printf(" (applied %d)", report.UpdatedLinks)
	}
	fmt.Println()
	fmt.Printf("  Dirty archives: %d", report.WouldArchive)
	if apply {
		fmt.Printf(" (archived %d)", report.Archived)
	}
	fmt.Println()

	maxShow := 12
	if len(report.Entries) > 0 {
		fmt.Println("  Planned entries:")
		for i, entry := range report.Entries {
			if i >= maxShow {
				fmt.Printf("    ... %d more\n", len(report.Entries)-maxShow)
				break
			}
			action := "link"
			if entry.Archive {
				action = "archive"
			}
			fmt.Printf("    - %s %s [%s/%s]", action, entry.ID, entry.Category, entry.Tier)
			if entry.Path != "" {
				fmt.Printf(" @%s", entry.Path)
			}
			fmt.Println()
			if entry.Archive {
				fmt.Printf("      reason=%s archive=%s\n", entry.Reason, entry.ArchivePath)
				continue
			}
			if len(entry.AddLinks) > 0 {
				fmt.Printf("      links=%s\n", strings.Join(entry.AddLinks, ", "))
			}
			if len(entry.AddTags) > 0 {
				fmt.Printf("      tags=%s\n", strings.Join(entry.AddTags, ", "))
			}
		}
	}
	return nil
}

func runMemoryTidalStats(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	cfg := mgr.Get()
	path := cliTidalMemoryStorePath(mgr.HomeDir(), cfg.Memory.Tidal.StorePath)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Tidal memory store: %s\n", path)
			fmt.Println("Status: no store found")
			return nil
		}
		return fmt.Errorf("stat tidal memory store: %w", err)
	}

	store, err := memory.OpenTidalStore(path)
	if err != nil {
		return fmt.Errorf("open tidal memory store: %w", err)
	}
	defer store.Close()

	stats, err := store.Stats()
	if err != nil {
		return fmt.Errorf("read tidal memory stats: %w", err)
	}
	kernels, err := store.LoadKernels()
	if err != nil {
		return fmt.Errorf("load tidal memory kernels: %w", err)
	}

	fmt.Printf("Tidal memory store: %s\n", path)
	fmt.Printf("Enabled: %t\n", cfg.Memory.Tidal.Enabled)
	fmt.Printf("Query events: %d\n", stats.QueryEvents)
	fmt.Printf("Recall events: %d\n", stats.RecallEvents)
	fmt.Printf("Feedback events: %d\n", stats.FeedbackEvents)
	fmt.Printf("Kernels: %d\n", stats.Kernels)
	limit := 10
	if len(kernels) < limit {
		limit = len(kernels)
	}
	for i := 0; i < limit; i++ {
		kernel := kernels[i]
		fmt.Printf("  %s feature=%s weights=%v counts=%v\n", kernel.Key, kernel.Feature, kernel.Weights, kernel.Counts)
	}
	return nil
}

func cliTidalMemoryStorePath(homeDir, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return filepath.Join(homeDir, "runtime", "tidal_memory.db")
	}
	if filepath.IsAbs(configured) {
		return configured
	}
	return filepath.Join(homeDir, configured)
}

func runProactiveStatus(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	cfg := mgr.Get()
	path := cliProactiveStorePath(mgr.HomeDir(), cfg.Proactive.StorePath)

	fmt.Printf("Proactive enabled: %t\n", cfg.Proactive.Enabled)
	fmt.Printf("Dry-run: %t\n", proactiveDryRunEnabled(cfg))
	fmt.Printf("Confidence threshold: %.2f\n", cfg.Proactive.ConfidenceThreshold)
	fmt.Printf("Horizon seconds: %d\n", cfg.Proactive.HorizonSeconds)
	fmt.Printf("Store: %s\n", path)

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Status: no store found")
			return nil
		}
		return fmt.Errorf("stat proactive store: %w", err)
	}
	store, err := proactive.OpenStore(path)
	if err != nil {
		return fmt.Errorf("open proactive store: %w", err)
	}
	defer store.Close()

	stats, err := store.Stats()
	if err != nil {
		return fmt.Errorf("read proactive stats: %w", err)
	}
	fmt.Printf("Signals: %d\n", stats.Signals)
	fmt.Printf("Estimates: %d\n", stats.Estimates)
	fmt.Printf("Dry-run actions: %d\n", stats.Actions)
	fmt.Printf("Feedback events: %d\n", stats.FeedbackEvents)
	fmt.Printf("Runtime events: %d\n", stats.RuntimeEvents)
	if feedbackStats, err := store.FeedbackStats(100); err != nil {
		return err
	} else if feedbackStats.Events > 0 {
		fmt.Printf("Feedback accuracy: %.2f (%d/%d recent)\n", feedbackStats.Accuracy, feedbackStats.Correct, feedbackStats.Events)
	}
	if runtimeStats, err := store.RuntimeEventStats(); err != nil {
		return err
	} else if runtimeStats.Events > 0 {
		fmt.Printf("Runtime event types: %s\n", formatRuntimeEventTypes(runtimeStats.ByType))
	}
	if estimate, ok, err := store.LatestEstimate(); err != nil {
		return err
	} else if ok {
		fmt.Printf("Latest estimate: %s confidence=%.2f noise=%.2f horizon=%s\n",
			estimate.PredictedState, estimate.Confidence, estimate.NoiseVariance, estimate.Horizon)
	}
	return nil
}

func runProactiveSample(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	cfg := mgr.Get()
	store, err := proactive.OpenStore(cliProactiveStorePath(mgr.HomeDir(), cfg.Proactive.StorePath))
	if err != nil {
		return fmt.Errorf("open proactive store: %w", err)
	}
	defer store.Close()

	sampler := proactive.NewSamplerWithStore("", store)
	signals, err := sampler.Sample(cmd.Context())
	if err != nil {
		return fmt.Errorf("sample proactive signals: %w", err)
	}
	if err := store.RecordSignals(signals); err != nil {
		return fmt.Errorf("persist proactive signals: %w", err)
	}

	fmt.Printf("Sampled signals: %d\n", len(signals))
	for _, signal := range signals {
		fmt.Printf("  %s value=%.3f label=%s\n", signal.Channel, signal.Value, signal.Label)
	}
	return nil
}

func runProactiveDryRun(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	cfg := mgr.Get()
	store, err := proactive.OpenStore(cliProactiveStorePath(mgr.HomeDir(), cfg.Proactive.StorePath))
	if err != nil {
		return fmt.Errorf("open proactive store: %w", err)
	}
	defer store.Close()

	gateCfg := proactive.Config{
		Enabled:             cfg.Proactive.Enabled,
		DryRun:              proactiveDryRunEnabled(cfg),
		ConfidenceThreshold: cfg.Proactive.ConfidenceThreshold,
		Horizon:             time.Duration(cfg.Proactive.HorizonSeconds) * time.Second,
	}
	runner := proactive.NewRunnerWithCalibrator(
		proactive.NewSamplerWithStore("", store),
		proactive.NewEstimator(),
		proactive.NewFeedbackCalibrator(store),
		proactive.NewGate(gateCfg),
		store,
	)
	decision, err := runner.RunDryRun(cmd.Context())
	if err != nil {
		return fmt.Errorf("run proactive dry-run: %w", err)
	}

	fmt.Printf("Predicted state: %s\n", decision.Estimate.PredictedState)
	fmt.Printf("Confidence: %.2f\n", decision.Estimate.Confidence)
	fmt.Printf("Noise variance: %.2f\n", decision.Estimate.NoiseVariance)
	fmt.Printf("Horizon: %s\n", decision.Estimate.Horizon)
	if len(decision.Estimate.Reasons) > 0 {
		fmt.Println("Reasons:")
		for _, reason := range decision.Estimate.Reasons {
			fmt.Printf("  - %s\n", reason)
		}
	}
	fmt.Println("Dry-run actions:")
	for _, action := range decision.Actions {
		fmt.Printf("  - %s allowed=%t confidence=%.2f reason=%s\n", action.Action, action.Allowed, action.Confidence, action.Reason)
	}
	return nil
}

func runProactiveEvents(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	cfg := mgr.Get()
	store, err := proactive.OpenStore(cliProactiveStorePath(mgr.HomeDir(), cfg.Proactive.StorePath))
	if err != nil {
		return fmt.Errorf("open proactive store: %w", err)
	}
	defer store.Close()

	limit, _ := cmd.Flags().GetInt("limit")
	events, err := store.RecentRuntimeEvents(limit)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		fmt.Println("No proactive runtime events found.")
		return nil
	}
	for _, event := range events {
		sessionID := event.SessionID
		if sessionID == "" {
			sessionID = "-"
		}
		fmt.Printf("%s type=%s name=%s source=%s session=%s value=%.2f\n",
			event.CreatedAt.Local().Format(time.RFC3339),
			event.Type,
			event.Name,
			event.Source,
			sessionID,
			event.Value,
		)
		if len(event.Metadata) > 0 {
			fmt.Printf("  metadata=%s\n", formatStringMap(event.Metadata))
		}
	}
	return nil
}

func runProactiveFeedback(cmd *cobra.Command, args []string) error {
	actualState := strings.TrimSpace(args[0])
	if actualState == "" {
		return fmt.Errorf("actual state is required")
	}

	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	cfg := mgr.Get()
	store, err := proactive.OpenStore(cliProactiveStorePath(mgr.HomeDir(), cfg.Proactive.StorePath))
	if err != nil {
		return fmt.Errorf("open proactive store: %w", err)
	}
	defer store.Close()

	stateID, _ := cmd.Flags().GetString("state-id")
	var estimate proactive.StateEstimate
	var ok bool
	if strings.TrimSpace(stateID) != "" {
		estimate, ok, err = store.EstimateByID(stateID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("proactive estimate %q not found", stateID)
		}
	} else {
		estimate, ok, err = store.LatestEstimate()
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no proactive estimate found; run `la proactive dry-run` first")
		}
	}
	value, _ := cmd.Flags().GetFloat64("value")
	source, _ := cmd.Flags().GetString("source")
	note, _ := cmd.Flags().GetString("note")
	event := proactive.FeedbackEvent{
		StateID:        estimate.ID,
		PredictedState: estimate.PredictedState,
		ActualState:    actualState,
		Value:          value,
		Source:         source,
		Note:           note,
		CreatedAt:      time.Now(),
	}
	if err := store.RecordFeedback(event); err != nil {
		return fmt.Errorf("record proactive feedback: %w", err)
	}

	correct := strings.EqualFold(estimate.PredictedState, actualState)
	fmt.Printf("Recorded feedback: predicted=%s actual=%s correct=%t\n", estimate.PredictedState, actualState, correct)
	if feedbackStats, err := store.FeedbackStats(100); err == nil && feedbackStats.Events > 0 {
		fmt.Printf("Recent feedback accuracy: %.2f (%d/%d)\n", feedbackStats.Accuracy, feedbackStats.Correct, feedbackStats.Events)
	}
	return nil
}

func proactiveDryRunEnabled(cfg *config.Config) bool {
	if cfg == nil || cfg.Proactive.DryRun == nil {
		return true
	}
	return *cfg.Proactive.DryRun
}

func cliProactiveStorePath(homeDir, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return filepath.Join(homeDir, "runtime", "proactive_state.db")
	}
	if filepath.IsAbs(configured) {
		return configured
	}
	return filepath.Join(homeDir, configured)
}

func formatRuntimeEventTypes(byType map[string]int) string {
	if len(byType) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(byType))
	for key := range byType {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, byType[key]))
	}
	return strings.Join(parts, ", ")
}

func formatStringMap(values map[string]string) string {
	if len(values) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, values[key]))
	}
	return strings.Join(parts, ", ")
}
