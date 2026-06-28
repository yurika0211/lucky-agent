package lhcmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/server/dashboard"
)

func addDashboardCmd(root *cobra.Command) {
	dashboardCmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Start or inspect the web dashboard",
	}

	startCmd := &cobra.Command{
		Use:   "start [addr]",
		Short: "Start the dashboard server",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runDashboardStart,
	}
	startCmd.Flags().String("addr", "", "Dashboard listen address")
	startCmd.Flags().Bool("open", false, "Print the dashboard URL for quick access")

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Print the dashboard address and runtime status",
		RunE:  runDashboardStatus,
	}

	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop is not available for this CLI process",
		RunE:  runDashboardStop,
	}

	dashboardCmd.AddCommand(startCmd, statusCmd, stopCmd)
	root.AddCommand(dashboardCmd)
}

func runDashboardStart(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.InitHome(); err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}

	cfg := mgr.Get()
	addr := strings.TrimSpace(cfg.Dashboard.Addr)
	if cmd != nil {
		if v, _ := cmd.Flags().GetString("addr"); strings.TrimSpace(v) != "" {
			addr = strings.TrimSpace(v)
		}
	}
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		addr = strings.TrimSpace(args[0])
	}
	if addr == "" {
		addr = dashboard.DefaultConfig().Addr
	}

	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	d := dashboard.New(dashboard.Config{Addr: addr})
	d.AddProvider(&cliDashboardProvider{agent: a})

	if err := d.Start(); err != nil {
		return err
	}

	url := dashboardURL(addr)
	fmt.Printf("Dashboard running at %s\n", url)
	fmt.Println("Press Ctrl+C to stop.")
	return waitForInterrupt(func() error { return d.Stop() })
}

func runDashboardStatus(cmd *cobra.Command, args []string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}

	addr := strings.TrimSpace(mgr.Get().Dashboard.Addr)
	if addr == "" {
		addr = dashboard.DefaultConfig().Addr
	}

	fmt.Printf("Dashboard addr: %s\n", addr)
	fmt.Printf("Dashboard url: %s\n", dashboardURL(addr))
	fmt.Println("Status: use `lh dashboard start` to run it in this process")
	return nil
}

func runDashboardStop(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("dashboard stop is only supported inside the REPL or the same process")
}

type cliDashboardProvider struct {
	agent *agent.Agent
}

func (p *cliDashboardProvider) DashboardData() map[string]interface{} {
	if p == nil || p.agent == nil {
		return map[string]interface{}{
			"active_profile": "cli",
		}
	}
	cfg := p.agent.Config().Get()
	return map[string]interface{}{
		"active_profile": "cli",
		"provider":       cfg.Provider,
		"model":          cfg.Model,
		"api_addr":       cfg.MsgGateway.APIAddr,
		"dashboard_addr": cfg.Dashboard.Addr,
	}
}

func dashboardURL(addr string) string {
	host := strings.TrimSpace(addr)
	if host == "" {
		host = ":8765"
	}
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	return "http://" + host
}

func waitForInterrupt(stop func() error) error {
	if stop == nil {
		return nil
	}

	ctx, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	<-ctx.Done()
	return stop()
}
