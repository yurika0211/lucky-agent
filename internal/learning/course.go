package learning

import (
	"fmt"
	"strings"
)

// Course describes a project-course style learning pack.
type Course struct {
	ID          string   `json:"id" yaml:"id"`
	Title       string   `json:"title" yaml:"title"`
	Description string   `json:"description" yaml:"description"`
	Mode        string   `json:"mode" yaml:"mode"`
	Capstone    string   `json:"capstone" yaml:"capstone"`
	Modules     []Module `json:"modules" yaml:"modules"`
}

// Module is one teach-and-build unit inside a course.
type Module struct {
	ID          string   `json:"id" yaml:"id"`
	Title       string   `json:"title" yaml:"title"`
	Objective   string   `json:"objective" yaml:"objective"`
	Concepts    []string `json:"concepts" yaml:"concepts"`
	Lab         Lab      `json:"lab" yaml:"lab"`
	Rubric      []string `json:"rubric" yaml:"rubric"`
	NextActions []string `json:"next_actions" yaml:"next_actions"`
}

// Lab is the concrete exercise a learner should complete.
type Lab struct {
	ID          string   `json:"id" yaml:"id"`
	Prompt      string   `json:"prompt" yaml:"prompt"`
	Commands    []string `json:"commands" yaml:"commands"`
	Evidence    []string `json:"evidence" yaml:"evidence"`
	AgentRoles  []string `json:"agent_roles" yaml:"agent_roles"`
	Deliverable string   `json:"deliverable" yaml:"deliverable"`
}

// BuiltinCourses returns the bundled LuckyHarness learning packs.
func BuiltinCourses() []Course {
	return []Course{agentSystemsCourse()}
}

// FindCourse returns a bundled course by ID or title prefix.
func FindCourse(idOrPrefix string) (Course, bool) {
	key := strings.ToLower(strings.TrimSpace(idOrPrefix))
	if key == "" {
		return Course{}, false
	}
	for _, c := range BuiltinCourses() {
		id := strings.ToLower(c.ID)
		title := strings.ToLower(c.Title)
		if id == key || strings.HasPrefix(id, key) || strings.HasPrefix(title, key) {
			return c, true
		}
	}
	return Course{}, false
}

// ModuleByID returns a module by ID.
func (c Course) ModuleByID(id string) (Module, bool) {
	for _, m := range c.Modules {
		if m.ID == id {
			return m, true
		}
	}
	return Module{}, false
}

// ModuleAt returns a module by zero-based index.
func (c Course) ModuleAt(index int) (Module, bool) {
	if index < 0 || index >= len(c.Modules) {
		return Module{}, false
	}
	return c.Modules[index], true
}

// IndexOfModule returns a module index by ID.
func (c Course) IndexOfModule(moduleID string) int {
	for i, m := range c.Modules {
		if m.ID == moduleID {
			return i
		}
	}
	return -1
}

func (c Course) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("course id is required")
	}
	if strings.TrimSpace(c.Title) == "" {
		return fmt.Errorf("course title is required")
	}
	if len(c.Modules) == 0 {
		return fmt.Errorf("course %s has no modules", c.ID)
	}
	seen := make(map[string]bool, len(c.Modules))
	for _, m := range c.Modules {
		if strings.TrimSpace(m.ID) == "" {
			return fmt.Errorf("course %s has a module without id", c.ID)
		}
		if seen[m.ID] {
			return fmt.Errorf("course %s has duplicate module id %s", c.ID, m.ID)
		}
		seen[m.ID] = true
		if strings.TrimSpace(m.Lab.ID) == "" {
			return fmt.Errorf("module %s has no lab id", m.ID)
		}
	}
	return nil
}

func agentSystemsCourse() Course {
	return Course{
		ID:          "lh-agent-systems",
		Title:       "LuckyHarness Agent Systems",
		Mode:        "project_course",
		Capstone:    "reproduce-hermes-agent-lite",
		Description: "A project-course learning pack for agent engineering: trace visibility, context packing, multi-agent orchestration, and a Hermes-lite capstone.",
		Modules: []Module{
			{
				ID:        "m1-tool-trace",
				Title:     "Tool Trace and Telegram Delivery",
				Objective: "Understand how visible tool traces make an agent system inspectable, then harden a Telegram delivery edge case.",
				Concepts:  []string{"tool trace", "agent trace", "telegram html", "regression test"},
				Lab: Lab{
					ID:     "lab-tool-trace-formatting",
					Prompt: "Inspect a Telegram rendering bug, explain the cause, patch the formatter or splitter, and add a focused regression test.",
					Commands: []string{
						"go test ./internal/gateway/telegram",
						"git diff --check",
					},
					Evidence: []string{
						"test output for internal/gateway/telegram",
						"diff summary for touched Telegram files",
						"short explanation of the root cause",
					},
					AgentRoles:  []string{"Lab Agent", "Debugger Agent", "Examiner Agent"},
					Deliverable: "A passing Telegram package test plus a concise bug analysis.",
				},
				Rubric: []string{
					"Explains why the formatting bug happened.",
					"Adds or preserves regression coverage.",
					"Keeps unrelated files out of scope.",
				},
				NextActions: []string{"run tests", "submit evidence", "review rubric"},
			},
			{
				ID:        "m2-context-packer",
				Title:     "Context Packer Benchmark",
				Objective: "Learn how long-session context should be packed, measured, and improved without losing active task evidence.",
				Concepts:  []string{"context window", "typed memory", "history relevance", "benchmark gate"},
				Lab: Lab{
					ID:     "lab-context-packer-benchmark",
					Prompt: "Run the context packer benchmark, inspect the summary metrics, and identify one improvement that is not overfitted to synthetic cases.",
					Commands: []string{
						"go run ./cmd/lh-context-packer-bench -variant manual",
					},
					Evidence: []string{
						"summary metrics",
						"one failing or weak scenario",
						"one proposed packing rule",
					},
					AgentRoles:  []string{"Instructor Agent", "Lab Agent", "Critic Agent"},
					Deliverable: "A benchmark note with metrics, weakness, and next packing rule.",
				},
				Rubric: []string{
					"Reports quantitative benchmark evidence.",
					"Separates active-task context from noisy history.",
					"Names a concrete next change.",
				},
				NextActions: []string{"run benchmark", "write summary", "compare with prior report"},
			},
			{
				ID:        "m3-multiagent-orchestration",
				Title:     "Multi-agent Orchestration Math",
				Objective: "Use benchmark evidence to decide when to stay single-agent, split parallel work, build a pipeline, run debate, or queue autonomy.",
				Concepts:  []string{"MDP", "stochastic shortest path", "Lyapunov guard", "verifier gate"},
				Lab: Lab{
					ID:     "lab-multiagent-bench",
					Prompt: "Run the multi-agent benchmark, compare a baseline with math-full-v1, and explain one routing decision using diagnostics.",
					Commands: []string{
						"go run ./cmd/lh-multiagent-bench -variant baseline",
						"go run ./cmd/lh-multiagent-bench -variant math-full-v1",
					},
					Evidence: []string{
						"baseline summary",
						"math-full-v1 summary",
						"one diagnostic trace explanation",
					},
					AgentRoles:  []string{"Planner Agent", "Critic Agent", "Examiner Agent"},
					Deliverable: "A comparison note explaining score, risk, and routing behavior.",
				},
				Rubric: []string{
					"Compares against baseline instead of reporting one run.",
					"Explains a concrete candidate mode decision.",
					"Mentions verifier or Lyapunov behavior when relevant.",
				},
				NextActions: []string{"run baseline", "run math-full", "inspect diagnostics"},
			},
			{
				ID:        "m4-hermes-lite-capstone",
				Title:     "Hermes-lite Agent Capstone",
				Objective: "Design and validate a compact Hermes-like agent workflow across CLI, Telegram, trace visibility, and acceptance review.",
				Concepts:  []string{"planner", "executor", "debugger", "acceptor", "agent trace"},
				Lab: Lab{
					ID:     "lab-hermes-lite",
					Prompt: "Build a Hermes-lite workflow spec for LuckyHarness, including roles, commands, trace output, acceptance checks, and a rollback plan.",
					Commands: []string{
						"go test ./cmd/lh-multiagent-bench",
						"go test ./internal/gateway/telegram",
					},
					Evidence: []string{
						"workflow spec",
						"test output",
						"acceptance checklist",
					},
					AgentRoles:  []string{"Instructor Agent", "Planner Agent", "Debugger Agent", "Examiner Agent"},
					Deliverable: "A Hermes-lite project report with verified traces and acceptance gates.",
				},
				Rubric: []string{
					"Defines roles and handoff rules clearly.",
					"Includes visible Agent Trace evidence.",
					"Has testable acceptance gates and rollback criteria.",
				},
				NextActions: []string{"draft spec", "run tests", "submit capstone report"},
			},
		},
	}
}
