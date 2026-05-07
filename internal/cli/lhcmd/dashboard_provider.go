package lhcmd

import (
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/dashboard"
	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/tool"
)

var (
	replDashboardMu sync.Mutex
	replDashboard   *dashboard.Dashboard
)

type replDashboardProvider struct {
	agent *agent.Agent
}

func ensureREPLDashboard(a *agent.Agent, addr string) (*dashboard.Dashboard, bool) {
	replDashboardMu.Lock()
	defer replDashboardMu.Unlock()

	if replDashboard != nil {
		return replDashboard, false
	}

	cfg := dashboard.Config{Addr: addr}
	replDashboard = dashboard.New(cfg)
	if a != nil {
		replDashboard.AddProvider(&replDashboardProvider{agent: a})
	}
	return replDashboard, true
}

func getREPLDashboard() *dashboard.Dashboard {
	replDashboardMu.Lock()
	defer replDashboardMu.Unlock()
	return replDashboard
}

func (p *replDashboardProvider) DashboardData() map[string]interface{} {
	data := map[string]interface{}{
		"active_profile": "repl",
	}
	if p == nil || p.agent == nil {
		return data
	}

	cfg := p.agent.Config().Get()
	data["provider"] = cfg.Provider
	data["model"] = cfg.Model
	data["stream_mode"] = cfg.StreamMode
	data["soul_path"] = cfg.SoulPath
	data["telegram_platform"] = cfg.MsgGateway.Platform
	data["telegram_proxy"] = cfg.MsgGateway.Telegram.Proxy
	data["telegram_timeout_seconds"] = cfg.MsgGateway.Telegram.ChatTimeoutSeconds

	if sessions := p.agent.Sessions(); sessions != nil {
		infos := sessions.ListInfo()
		recent := make([]map[string]interface{}, 0, minInt(len(infos), 5))
		for i, info := range infos {
			if i >= 5 {
				break
			}
			recent = append(recent, map[string]interface{}{
				"id":            info.ID,
				"title":         info.Title,
				"message_count": info.MessageCount,
				"updated_at":    info.UpdatedAt.Format(time.RFC3339),
			})
		}
		data["sessions_total"] = sessions.Count()
		data["sessions_recent"] = recent
	}

	if mem := p.agent.Memory(); mem != nil {
		stats := mem.Stats()
		data["memory_short"] = stats[memory.TierShort]
		data["memory_medium"] = stats[memory.TierMedium]
		data["memory_long"] = stats[memory.TierLong]
		data["memory_total"] = mem.Count()
	}

	if cronEngine := p.agent.CronEngine(); cronEngine != nil {
		jobs := cronEngine.ListJobs()
		recentJobs := make([]map[string]interface{}, 0, minInt(len(jobs), 5))
		for i, job := range jobs {
			if i >= 5 {
				break
			}
			recentJobs = append(recentJobs, map[string]interface{}{
				"id":        job.ID,
				"status":    job.Status.String(),
				"next_run":  formatTime(job.NextRun),
				"last_run":  formatTime(job.LastRun),
				"schedule":  describeCronSchedule(job),
				"run_count": job.RunCount,
			})
		}
		data["cron_running"] = cronEngine.IsRunning()
		data["cron_jobs_total"] = cronEngine.JobCount()
		data["cron_jobs"] = recentJobs
	}

	if gm := p.agent.MsgGateway(); gm != nil {
		gatewayNames := gm.List()
		data["gateway_manager_running"] = gm.IsRunning()
		data["gateways_registered"] = gatewayNames
		data["gateway_stats"] = gm.AllStats()
		if gw, ok := gm.Get("telegram"); ok {
			data["telegram_registered"] = true
			data["telegram_connected"] = gw.IsRunning()
		} else {
			data["telegram_registered"] = false
			data["telegram_connected"] = false
		}
		if stats, ok := gm.Stats("telegram"); ok {
			data["telegram_messages_sent"] = stats.MessagesSent
			data["telegram_messages_received"] = stats.MessagesReceived
			data["telegram_errors"] = stats.Errors
		} else {
			data["telegram_messages_sent"] = 0
			data["telegram_messages_received"] = 0
			data["telegram_errors"] = 0
		}
	}

	if tools := p.agent.Tools(); tools != nil {
		allTools := tools.List()
		data["tools_total"] = tools.Count()
		data["tools_enabled"] = len(tools.ListEnabled())
		data["tools_builtin_total"] = len(tools.ListByCategory(tool.CatBuiltin))
		data["tools_skill_total"] = len(tools.ListByCategory(tool.CatSkill))
		data["tools_mcp_total"] = len(tools.ListByCategory(tool.CatMCP))
		data["tools_delegate_total"] = len(tools.ListByCategory(tool.CatDelegate))
		data["tools_model_visible_total"] = len(tools.ListModelVisible())
		data["tools_sample"] = sampleToolNames(allTools, 10)
	}
	skills := p.agent.Skills()
	data["skills_loaded"] = len(skills)
	data["skills_names"] = sampleSkillNames(skills, 10)

	if m := p.agent.Metrics(); m != nil {
		snap := m.Snapshot()
		data["metrics"] = snap
		data["total_requests"] = snap.TotalRequests
		data["chat_requests"] = snap.ChatRequests
		data["tool_calls"] = snap.ToolCalls
		data["function_calls"] = snap.FunctionCalls
		data["error_requests"] = snap.ErrorRequests
		data["metrics_uptime"] = snap.Uptime
	}

	return data
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func describeCronSchedule(job *cron.Job) string {
	if job == nil {
		return ""
	}
	if text := strings.TrimSpace(job.Metadata["schedule_text"]); text != "" {
		return text
	}
	if job.Schedule == nil {
		return ""
	}
	return cron.DescribeSchedule(job.Schedule)
}

func sampleToolNames(tools []*tool.Tool, limit int) []string {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, minInt(len(tools), limit))
	for i, t := range tools {
		if i >= limit || t == nil {
			break
		}
		names = append(names, t.Name)
	}
	return names
}

func sampleSkillNames(skills []*tool.SkillInfo, limit int) []string {
	if len(skills) == 0 {
		return nil
	}
	names := make([]string, 0, minInt(len(skills), limit))
	for i, s := range skills {
		if i >= limit || s == nil {
			break
		}
		names = append(names, s.Name)
	}
	return names
}
