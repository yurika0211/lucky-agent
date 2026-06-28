package cron

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReportGenerator 定时报告生成器
type ReportGenerator struct {
	outputDir string
	engine    *Engine
}

// NewReportGenerator 创建报告生成器
func NewReportGenerator(outputDir string, engine *Engine) *ReportGenerator {
	return &ReportGenerator{
		outputDir: outputDir,
		engine:    engine,
	}
}

// DailyReport 每日报告任务
func (r *ReportGenerator) DailyReport() func() error {
	return func() error {
		now := time.Now()
		filename := fmt.Sprintf("daily-report-%s.md", now.Format("2006-01-02"))
		path := filepath.Join(r.outputDir, filename)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# LuckyAgent 每日报告\n\n"))
		sb.WriteString(fmt.Sprintf("**日期**: %s\n\n", now.Format("2006年01月02日 15:04:05")))

		// 任务统计
		jobs := r.engine.ListJobs()
		sb.WriteString("## 定时任务状态\n\n")
		if len(jobs) == 0 {
			sb.WriteString("暂无定时任务。\n")
		} else {
			sb.WriteString("| ID | 调度 | 状态 | 执行次数 | 错误次数 | 上次运行 |\n")
			sb.WriteString("|---|---|---|---|---|---|\n")
			for _, j := range jobs {
				statusStr := j.Status.String()
				lastRun := "N/A"
				if !j.LastRun.IsZero() {
					lastRun = j.LastRun.Format("15:04:05")
				}
				sb.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %d | %s |\n",
					j.ID, j.Schedule, statusStr, j.RunCount, j.ErrorCount, lastRun))
			}
		}

		sb.WriteString("\n---\n")
		sb.WriteString(fmt.Sprintf("*报告生成时间: %s*\n", now.Format(time.RFC3339)))

		if err := os.MkdirAll(r.outputDir, 0755); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}

		if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}

		fmt.Printf("📊 每日报告已生成: %s\n", path)
		return nil
	}
}

// HealthCheck 健康检查任务
func (r *ReportGenerator) HealthCheck() func() error {
	return func() error {
		jobs := r.engine.ListJobs()
		var failed []string
		for _, j := range jobs {
			if j.Status == StatusFailed {
				failed = append(failed, fmt.Sprintf("%s (errors: %d, last: %s)", j.ID, j.ErrorCount, j.LastError))
			}
		}

		if len(failed) > 0 {
			fmt.Printf("⚠️ 健康检查: %d 个任务失败\n", len(failed))
			for _, f := range failed {
				fmt.Printf("  - %s\n", f)
			}
		} else {
			fmt.Printf("✅ 健康检查: 所有 %d 个任务正常\n", len(jobs))
		}
		return nil
	}
}
