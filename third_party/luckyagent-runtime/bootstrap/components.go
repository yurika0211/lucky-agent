package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/credentials"
	"github.com/yurika0211/luckyagent-runtime/gateway"
	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/mcpclient"
	"github.com/yurika0211/luckyagent-runtime/proactive"
	"github.com/yurika0211/luckyagent-runtime/sandbox"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/autonomy"
	"github.com/yurika0211/luckyagent-tools/collab"
	"github.com/yurika0211/luckyagent-tools/computer"
	"github.com/yurika0211/luckyagent-tools/cron"
	"github.com/yurika0211/luckyagent-tools/memory"
	taskstore "github.com/yurika0211/luckyagent-tools/task"
)

// Components contains prepared application resources. It has no dependency on
// an Agent implementation. Open creates resources; Start activates services.
type Components struct {
	Config           *config.Manager
	Soul             SoulRuntime
	Provider         ProviderRuntime
	RAG              RagRuntime
	Support          SupportRuntime
	Memory           memory.Runtime
	Sessions         *session.Manager
	Sandbox          *sandbox.Manager
	ProactiveStore   *proactive.Store
	ProactiveRuntime *proactive.RuntimeService
	Tasks            taskstore.Store
	MCP              *mcpclient.Manager
	Gateway          *gateway.GatewayManager
	CollabRegistry   *collab.Registry
	CollabManager    *collab.DelegateManager
	CronStore        *cron.Store
	startOnce        sync.Once
	closeOnce        sync.Once
	closeError       error
	closed           atomic.Bool
}

// Open prepares the default LuckyAgent components without starting MCP,
// heartbeat, proactive workers or queued Agent execution. The caller owns the
// returned resources until it explicitly transfers them to an Agent.
func Open(cfg *config.Manager, injected *memory.Runtime) (_ *Components, err error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration manager is required")
	}
	r := &Components{Config: cfg}
	defer func() {
		if err != nil {
			_ = r.Close()
		}
	}()
	ApplyWebSearchEnv(cfg)
	ApplyOpenCLIEnv(cfg)
	c := cfg.Get()
	r.Sandbox, err = sandbox.NewManager(cfg.HomeDir())
	if err != nil {
		return nil, fmt.Errorf("init sandbox manager: %w", err)
	}
	if err = ResolveConfiguredCredentials(cfg.HomeDir(), c); err != nil {
		return nil, err
	}
	r.Soul = InitSoulRuntime(c)
	if r.Provider, err = InitProviderRuntime(cfg, c); err != nil {
		return nil, err
	}
	if r.Memory, err = ResolveMemoryRuntime(cfg, c, injected); err != nil {
		return nil, err
	}
	if r.Sessions, err = session.NewManager(filepath.Join(cfg.HomeDir(), "sessions")); err != nil {
		return nil, fmt.Errorf("init sessions: %w", err)
	}
	if r.RAG, err = InitRAGRuntime(cfg, c); err != nil {
		return nil, err
	}
	r.Support = InitSupportRuntime(c, r.Memory.Store, r.RAG.Manager)
	if r.ProactiveStore, err = InitProactiveRuntime(cfg.HomeDir(), c.Proactive); err != nil {
		return nil, err
	}
	r.ProactiveRuntime = InitProactiveRuntimeService(cfg.HomeDir(), c.Proactive, r.ProactiveStore)
	if r.Tasks, err = taskstore.NewFileStore(filepath.Join(cfg.HomeDir(), "tasks")); err != nil {
		return nil, fmt.Errorf("init task store: %w", err)
	}
	if err := r.Support.DelegateMgr.SetTaskStore(r.Tasks); err != nil {
		return nil, fmt.Errorf("restore delegate tasks: %w", err)
	}
	if err := r.Support.DelegateMgr.SetMDPSnapshotPath(filepath.Join(cfg.HomeDir(), "planner", "delegate_mdp.json")); err != nil {
		return nil, fmt.Errorf("restore delegate planner: %w", err)
	}
	if c.Tools.ComputerUse.Enabled {
		backend, backendErr := computer.NewBackend(c.Tools.ComputerUse.Backend, computer.BackendOptions{ObserveOnly: c.Tools.ComputerUse.Mode == "observe"})
		if backendErr != nil {
			return nil, fmt.Errorf("init computer backend: %w", backendErr)
		}
		managerCfg := computer.DefaultManagerConfig()
		managerCfg.StorageDir = strings.TrimSpace(c.Tools.ComputerUse.CaptureDir)
		if managerCfg.StorageDir == "" {
			managerCfg.StorageDir = filepath.Join(cfg.HomeDir(), "computer")
		}
		managerCfg.MaxSteps = c.Tools.ComputerUse.MaxSteps
		managerCfg.KeepFrames = c.Tools.ComputerUse.KeepFrames
		if managerCfg.KeepFrames <= 0 {
			managerCfg.KeepFrames = c.Tools.ComputerUse.RetainFrames
		}
		settleMS := c.Tools.ComputerUse.SettleMilliseconds
		if settleMS <= 0 {
			settleMS = c.Tools.ComputerUse.SettleMS
		}
		managerCfg.Settle = time.Duration(settleMS) * time.Millisecond
		if c.Tools.ComputerUse.FrameTTLSeconds > 0 {
			managerCfg.FrameTTL = time.Duration(c.Tools.ComputerUse.FrameTTLSeconds) * time.Second
		}
		managerCfg.MaxObservationBytes = c.Tools.ComputerUse.MaxObservationBytes
		managerCfg.MaxScreenshotWidth = c.Tools.ComputerUse.MaxScreenshotWidth
		managerCfg.MaxBatchActions = c.Tools.ComputerUse.MaxBatchActions
		managerCfg.SettleMode = c.Tools.ComputerUse.SettleMode
		managerCfg.AllowedWindows = append([]string(nil), c.Tools.ComputerUse.AllowedWindows...)
		if managerCfg.Settle < 0 {
			managerCfg.Settle = 0
		}
		r.Support.ComputerMgr, err = computer.NewManagerWithConfig(backend, managerCfg)
		if err != nil {
			return nil, fmt.Errorf("init computer manager: %w", err)
		}
		r.Support.ToolServices.SetComputerUseService(tool.NewComputerUseToolService(r.Support.ComputerMgr, c.Tools.ComputerUse))
	}
	r.MCP = mcpclient.NewManager(c.MCP, func(ctx context.Context, ref string, use func([]byte) error) error {
		store, err := credentials.NewStore(cfg.HomeDir())
		if err != nil {
			return err
		}
		defer store.Close()
		return store.WithCredential(ctx, ref, use)
	})
	adapter := tool.NewMCPAdapter(r.Support.Tools, r.MCP)
	if err = r.MCP.SetPublisher(adapter.Sync); err != nil {
		return nil, err
	}
	r.Support.DelegateMgr.SetMCPExecutor(func(ctx context.Context, server, name string, args map[string]any) (string, error) {
		return r.Support.Tools.CallWithContext(tool.MCPToolName(server, name), args, tool.ExecutionContext{Context: ctx})
	})
	r.Gateway = gateway.NewGatewayManager()
	r.CollabRegistry = collab.NewRegistry()
	r.CollabRegistry.Register(&collab.AgentProfile{
		ID: "local-agent", Name: "Local Agent", Description: "The primary local agent",
		Capabilities: []string{"chat", "code", "analysis", "research"}, Status: collab.StatusOnline,
		Runtime: collab.AgentRuntimeProfile{AgentID: "local-agent",
			MaxIterations: c.Agent.MaxIterations, Timeout: time.Duration(c.Agent.TimeoutSeconds) * time.Second, AllowDelegate: false},
	})
	r.CollabManager = collab.NewDelegateManager(r.CollabRegistry, nil)
	if err := r.CollabManager.SetTaskStore(r.Tasks); err != nil {
		return nil, fmt.Errorf("restore collaboration tasks: %w", err)
	}
	_ = r.CollabManager.SetMDPSnapshotPath(filepath.Join(cfg.HomeDir(), "planner", "mdp.json"))
	r.CronStore = cron.NewStore(filepath.Join(cfg.HomeDir(), "memory", "prompts", "mission.md"))
	if restored, restoreErr := r.Support.AutonomyKit.EnablePersistence(filepath.Join(cfg.HomeDir(), "runtime", "autonomy_queue.json")); restoreErr != nil {
		fmt.Printf("[autonomy] restore failed: %v\n", restoreErr)
	} else if restored > 0 {
		fmt.Printf("[autonomy] restored %d queued tasks\n", restored)
	}
	if err := r.Support.AutonomyKit.Queue().SetSessionInitializer(func(task *autonomy.QueueTask) (string, error) {
		sess := r.Sessions.Ensure("autonomy-" + task.ID)
		sess.SetTitle(task.Title)
		sess.SetAgentProfile("worker-task", "You are a LuckyAgent worker executing a queued task. Work on the assigned task, preserve evidence, and report blockers or unfinished work clearly.")
		if err := sess.Save(); err != nil {
			return "", err
		}
		return sess.ID, nil
	}); err != nil {
		fmt.Printf("[autonomy] initialize queued task sessions failed: %v\n", err)
	}
	if r.Support.ToolServices.Ephemeral == nil {
		r.Support.ToolServices.Ephemeral = tool.NewEphemeralCredentialToolService(cfg.HomeDir())
	}
	if r.Support.ToolServices.EphemeralTerminal == nil {
		r.Support.ToolServices.EphemeralTerminal = tool.NewEphemeralCredentialTerminalToolService(cfg.HomeDir())
	}
	return r, nil
}

// Hooks bind application lifecycle actions to a prepared execution runtime.
// Keeping callbacks here avoids a dependency from this module to Agent.
type Hooks struct {
	LoadSkills        func(string) (int, error)
	InstallCronEvents func()
	RestoreCron       func() (int, error)
	SaveCron          func() error
	StartHeartbeat    func() error
	ResumeAutonomy    func(context.Context) error
	ResumeForeground  func(context.Context) error
}

func (r *Components) Start(ctx context.Context, hooks Hooks) error {
	if r == nil || r.Config == nil {
		return fmt.Errorf("runtime components are unavailable")
	}
	if r.closed.Load() {
		return fmt.Errorf("runtime components are closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.startOnce.Do(func() {
		if r.MCP != nil {
			if err := r.MCP.Start(ctx); err != nil {
				logger.Warn("some MCP servers could not start", "error", err)
			}
		}
		if r.ProactiveRuntime != nil {
			if err := r.ProactiveRuntime.Start(ctx); err != nil {
				fmt.Printf("[proactive] start failed: %v\n", err)
			}
		}
		skillsDir := ResolveSkillsDir(r.Config)
		if hooks.LoadSkills != nil {
			if info, err := os.Stat(skillsDir); err == nil && info.IsDir() {
				if count, err := hooks.LoadSkills(skillsDir); err == nil && count > 0 {
					fmt.Printf("[agent] loaded %d skills from %s\n", count, skillsDir)
				}
			}
		}
		if hooks.InstallCronEvents != nil {
			hooks.InstallCronEvents()
		}
		if hooks.RestoreCron != nil {
			if restored, err := hooks.RestoreCron(); err != nil {
				fmt.Printf("[cron] restore failed: %v\n", err)
			} else if restored > 0 {
				fmt.Printf("[cron] restored %d jobs\n", restored)
				if hooks.SaveCron != nil {
					if err := hooks.SaveCron(); err != nil {
						fmt.Printf("[cron] save restored jobs failed: %v\n", err)
					}
				}
			}
		}
		if hooks.StartHeartbeat != nil {
			if err := hooks.StartHeartbeat(); err != nil {
				fmt.Printf("[heartbeat] init failed: %v\n", err)
			}
		}
		c := r.Config.Get()
		if hooks.ResumeAutonomy != nil && r.Support.AutonomyKit != nil && (c.Autonomy.Recovery.ResumeOnStart == nil || *c.Autonomy.Recovery.ResumeOnStart) {
			if ready, _, _, _ := r.Support.AutonomyKit.Queue().Stats(); ready > 0 {
				if err := hooks.ResumeAutonomy(ctx); err != nil {
					logger.Warn("resume queued autonomy tasks failed", "error", err)
				}
			}
		}
		if hooks.ResumeForeground != nil {
			if err := hooks.ResumeForeground(ctx); err != nil {
				logger.Warn("resume foreground tasks failed", "error", err)
			}
		}
	})
	return nil
}

// Close is idempotent. It also handles a partially prepared component set.
func (r *Components) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.closed.Store(true)
		keep := func(err error) {
			if err != nil && r.closeError == nil {
				r.closeError = err
			}
		}
		if r.Support.ToolServices != nil {
			keep(r.Support.ToolServices.Close())
		}
		if r.Support.AutonomyKit != nil && r.Support.AutonomyKit.Status().Started {
			keep(r.Support.AutonomyKit.Stop())
		}
		if r.Support.CronEngine != nil {
			r.Support.CronEngine.Stop()
		}
		if r.ProactiveRuntime != nil {
			keep(r.ProactiveRuntime.Stop())
		}
		if r.Memory.Store != nil {
			keep(r.Memory.Store.Close())
		}
		if r.ProactiveStore != nil {
			keep(r.ProactiveStore.Close())
		}
		if r.Support.ComputerMgr != nil {
			keep(r.Support.ComputerMgr.Close())
		}
		if r.MCP != nil {
			keep(r.MCP.Close())
		}
		if r.RAG.Manager != nil && r.RAG.Manager.IsSQLite() {
			keep(r.RAG.Manager.CloseStore())
		} else if r.RAG.Persist != nil && r.RAG.Manager != nil {
			keep(r.RAG.Persist.Save(r.RAG.Manager))
		}
	})
	return r.closeError
}

func ResolveSkillsDir(cfg *config.Manager) string {
	if cfg == nil {
		return ""
	}
	if c := cfg.Get(); c != nil {
		if directory := strings.TrimSpace(c.Skills.Dir); directory != "" {
			return directory
		}
	}
	return filepath.Join(cfg.HomeDir(), "skills")
}
