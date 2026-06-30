package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/swebench"
)

type benchConfig struct {
	Dataset       string
	ReposDir      string
	WorkDir       string
	AgentHome     string
	OutPath       string
	Predictions   string
	Variant       string
	ModelName     string
	Provider      string
	Model         string
	APIBase       string
	Limit         int
	MaxIterations int
	Timeout       time.Duration
	AutoApprove   bool
	DryRun        bool
	ResetWorktree bool
	DisabledTools string
	GitBinary     string
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "la-swe-bench: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() benchConfig {
	now := time.Now().Format("20060102-150405")
	defaultBase := filepath.Join(os.TempDir(), "lh-swe-bench", now)
	var cfg benchConfig
	flag.StringVar(&cfg.Dataset, "dataset", "", "SWE-bench JSONL or JSON dataset path")
	flag.StringVar(&cfg.ReposDir, "repos-dir", "", "local repository cache directory, containing owner/repo or owner__repo checkouts")
	flag.StringVar(&cfg.WorkDir, "work-dir", "", "benchmark work directory; default is <agent-home>/bench/swebench")
	flag.StringVar(&cfg.AgentHome, "agent-home", "", "LuckyAgent home directory for model config; default is ~/.luckyagent")
	flag.StringVar(&cfg.OutPath, "out", filepath.Join(defaultBase, "results.jsonl"), "LuckyAgent benchmark trace JSONL output path")
	flag.StringVar(&cfg.Predictions, "predictions", filepath.Join(defaultBase, "predictions.jsonl"), "SWE-bench evaluator prediction JSONL output path")
	flag.StringVar(&cfg.Variant, "variant", "baseline", "benchmark variant label")
	flag.StringVar(&cfg.ModelName, "model-name", "", "model_name_or_path value in SWE-bench predictions")
	flag.StringVar(&cfg.Provider, "provider", "", "temporary LuckyAgent provider override")
	flag.StringVar(&cfg.Model, "model", "", "temporary LuckyAgent model override")
	flag.StringVar(&cfg.APIBase, "api-base", "", "temporary LuckyAgent API base override")
	flag.IntVar(&cfg.Limit, "limit", 0, "maximum number of instances to run; 0 means all")
	flag.IntVar(&cfg.MaxIterations, "max-iterations", 60, "max LuckyAgent loop iterations per instance")
	flag.DurationVar(&cfg.Timeout, "timeout", 5*time.Minute, "per-iteration LuckyAgent timeout")
	flag.BoolVar(&cfg.AutoApprove, "auto-approve", false, "allow the agent to execute write/terminal tools without interactive approval")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "load data and write empty predictions without calling the agent")
	flag.BoolVar(&cfg.ResetWorktree, "reset-worktree", true, "delete and recreate an existing instance worktree before running")
	flag.StringVar(&cfg.DisabledTools, "disabled-tools", strings.Join(defaultDisabledTools(), ","), "comma-separated tool names hidden from the model; use 'none' to expose all")
	flag.StringVar(&cfg.GitBinary, "git", "git", "git executable")
	flag.Parse()
	return cfg
}

func run(cfg benchConfig) error {
	instances, err := swebench.LoadInstances(cfg.Dataset, cfg.Limit)
	if err != nil {
		return err
	}

	mgr, err := newConfigManager(cfg.AgentHome)
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	if err := applyConfigOverrides(mgr, cfg); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkDir) == "" {
		cfg.WorkDir = filepath.Join(mgr.HomeDir(), "bench", "swebench")
	}
	if strings.TrimSpace(cfg.ModelName) == "" {
		model := strings.TrimSpace(mgr.Get().Model)
		if model == "" {
			model = "manual"
		}
		cfg.ModelName = "luckyagent/" + model + "/" + cfg.Variant
	}
	if !cfg.DryRun && strings.TrimSpace(cfg.ReposDir) == "" {
		return fmt.Errorf("repos-dir is required unless -dry-run is set")
	}
	if !cfg.DryRun && !cfg.AutoApprove {
		return fmt.Errorf("non-dry-run SWE-bench execution requires -auto-approve; run only against disposable worktrees or containers")
	}

	var solver swebench.Solver
	var a *agent.Agent
	if !cfg.DryRun {
		a, err = agent.New(mgr)
		if err != nil {
			return fmt.Errorf("create agent: %w", err)
		}
		defer a.Close()

		loopCfg := agent.DefaultLoopConfig()
		loopCfg.MaxIterations = cfg.MaxIterations
		loopCfg.Timeout = cfg.Timeout
		loopCfg.AutoApprove = cfg.AutoApprove
		loopCfg.DisabledTools = parseDisabledTools(cfg.DisabledTools)
		loopCfg.Ephemeral = true
		solver = swebench.LuckyAgentSolver{
			Agent:      a,
			LoopConfig: loopCfg,
			SessionDir: filepath.Join(cfg.WorkDir, "sessions"),
		}
	}

	ctx := context.Background()
	records := make([]swebench.Record, 0, len(instances))
	predictions := make([]swebench.Prediction, 0, len(instances))
	for idx, inst := range instances {
		fmt.Fprintf(os.Stderr, "[%d/%d] %s %s\n", idx+1, len(instances), inst.InstanceID, inst.Repo)
		record, pred := swebench.RunInstance(ctx, swebench.RunOptions{
			Variant:       cfg.Variant,
			ModelName:     cfg.ModelName,
			WorkDir:       cfg.WorkDir,
			ReposDir:      cfg.ReposDir,
			GitBinary:     cfg.GitBinary,
			ResetWorktree: cfg.ResetWorktree,
			DryRun:        cfg.DryRun,
			Solver:        solver,
		}, inst)
		records = append(records, record)
		predictions = append(predictions, pred)
		if record.Error != "" {
			fmt.Fprintf(os.Stderr, "  error: %s\n", record.Error)
		} else {
			fmt.Fprintf(os.Stderr, "  patch bytes: %d\n", record.PatchBytes)
		}
	}

	summary := swebench.SummarizeRecords(cfg.Variant, records)
	if err := swebench.WritePredictions(cfg.Predictions, predictions); err != nil {
		return err
	}
	if err := swebench.WriteRecords(cfg.OutPath, records, summary); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "predictions: %s\n", cfg.Predictions)
	fmt.Fprintf(os.Stderr, "results: %s\n", cfg.OutPath)
	if !summary.Clean {
		return fmt.Errorf("completed with %d errors", summary.Errors)
	}
	return nil
}

func newConfigManager(home string) (*config.Manager, error) {
	if strings.TrimSpace(home) == "" {
		return config.NewManager()
	}
	return config.NewManagerWithDir(filepath.Clean(home))
}

func applyConfigOverrides(mgr *config.Manager, cfg benchConfig) error {
	overrides := []struct {
		key   string
		value string
	}{
		{key: "provider", value: cfg.Provider},
		{key: "model", value: cfg.Model},
		{key: "api_base", value: cfg.APIBase},
	}
	for _, override := range overrides {
		if strings.TrimSpace(override.value) == "" {
			continue
		}
		if err := mgr.Set(override.key, override.value); err != nil {
			return fmt.Errorf("set %s: %w", override.key, err)
		}
	}
	return nil
}

func parseDisabledTools(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "none") {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func defaultDisabledTools() []string {
	return []string{
		"web_search",
		"web_fetch",
		"opencli",
		"remember",
		"recall",
		"memory_hygiene",
		"rag_search",
		"rag_index",
		"cron",
		"cron_add",
		"cron_list",
		"cron_remove",
		"cron_pause",
		"cron_resume",
		"cron_status",
		"autonomy",
		"autonomy_queue_add",
		"autonomy_queue_list",
		"autonomy_queue_update",
		"autonomy_worker_spawn",
		"autonomy_worker_list",
		"autonomy_heartbeat_trigger",
		"autonomy_status",
		"delegate_task",
		"task_status",
		"list_tasks",
		"delegate_parallel",
		"delegate_to_skill",
		"delegate_to_mcp",
	}
}
