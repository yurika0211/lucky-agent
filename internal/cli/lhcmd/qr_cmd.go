package lhcmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyagent/internal/server"
)

func newQRCmd() *cobra.Command {
	var addr string
	var advertiseURL string
	var ttl time.Duration
	var label string

	cmd := &cobra.Command{
		Use:   "qr",
		Short: "启动 API 并弹出手机扫码配对二维码",
		Long: `启动 LuckyAgent API，签发一把只活在当前进程里的临时 key，并在终端显示二维码。

手机 App 扫码后会写入局域网地址和这把临时 key。默认 24 小时到期。
API 进程退出或重启后，这把 key 立即失效。它不会写入 config.json。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQR(qrOptions{
				Addr:         addr,
				AdvertiseURL: advertiseURL,
				TTL:          ttl,
				Label:        label,
			})
		},
	}
	cmd.Flags().StringVarP(&addr, "addr", "a", "0.0.0.0:9090", "API 监听地址")
	cmd.Flags().StringVar(&advertiseURL, "url", "", "二维码里的 API 地址；默认用本机局域网 IPv4")
	cmd.Flags().DurationVar(&ttl, "ttl", server.DefaultPairingTTL, "临时 key 有效期，API 重启后立即失效")
	cmd.Flags().StringVar(&label, "name", "LuckyAgent", "写入 App 的连接名称")
	return cmd
}

type qrOptions struct {
	Addr         string
	AdvertiseURL string
	TTL          time.Duration
	Label        string
}

func runQR(opts qrOptions) error {
	a, err := getAgent()
	if err != nil {
		return err
	}
	cfg := server.DefaultServerConfig()
	runtimeCfg := a.Config().Get().Server
	cfg.Addr = opts.Addr
	if cfg.Addr == "" {
		cfg.Addr = "0.0.0.0:9090"
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

	s := server.New(a, cfg)
	if err := s.Start(); err != nil {
		return err
	}
	defer s.Stop()

	apiURL := strings.TrimSpace(opts.AdvertiseURL)
	if apiURL == "" {
		apiURL, err = server.LanAdvertiseURL(cfg.Addr)
		if err != nil {
			return err
		}
	}
	issued, err := s.IssuePairingKey(apiURL, opts.TTL, opts.Label)
	if err != nil {
		return err
	}
	text, err := issued.QRText()
	if err != nil {
		return err
	}
	fmt.Println("用 LuckyAgent App 扫描下面的二维码。")
	fmt.Printf("地址：%s\n", issued.URL)
	fmt.Printf("临时 key 到期：%s\n", issued.ExpiresAt.Local().Format(time.RFC3339))
	fmt.Println("API 进程退出或重启后，这把 key 立即失效。")
	fmt.Println(renderQR(text))
	fmt.Println("按 Ctrl-C 停止 API 并作废这把临时 key。")

	return waitForServeStop(s)
}
