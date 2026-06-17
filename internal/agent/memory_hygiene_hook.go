package agent

import (
	"strings"

	"github.com/yurika0211/luckyharness/internal/logger"
	"github.com/yurika0211/luckyharness/internal/memory"
)

func (a *Agent) runContextMemoryHygieneHook() {
	if a == nil || a.cfg == nil || a.memory == nil {
		return
	}
	cfg := a.cfg.Get().Context
	if !cfg.MemoryHygieneBeforeContext {
		return
	}

	limit := cfg.MemoryHygieneMaxFindings
	if limit <= 0 {
		limit = 25
	}
	opts := memory.HygieneOptions{
		MinSeverity: cfg.MemoryHygieneMinSeverity,
		MaxFindings: limit,
	}

	action := strings.ToLower(strings.TrimSpace(cfg.MemoryHygieneAction))
	if action == "" {
		action = "quarantine"
	}

	var (
		report memory.HygieneReport
		err    error
	)
	switch action {
	case "audit", "scan", "dry_run", "dry-run":
		report = a.memory.AuditHygiene(opts)
	case "quarantine", "archive":
		report, err = a.memory.QuarantineDirty(opts)
	case "delete", "purge":
		report, err = a.memory.DeleteDirty(opts)
	default:
		logger.Warn("memory hygiene hook skipped invalid action", "action", action)
		return
	}
	if err != nil {
		logger.Warn("memory hygiene hook failed", "action", action, "error", err)
		return
	}
	if len(report.Findings) == 0 && report.Quarantined == 0 && report.Deleted == 0 {
		return
	}
	logger.Info("memory hygiene hook completed",
		"action", action,
		"scanned", report.Scanned,
		"findings", len(report.Findings),
		"quarantined", report.Quarantined,
		"deleted", report.Deleted,
	)
}
