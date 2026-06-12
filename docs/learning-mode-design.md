# LuckyHarness Learning Mode Design

## Goal

Learning Mode turns LuckyHarness from a task-completion assistant into a project-course runner.

The target pattern is closer to a MIT-style project subject than a normal chatbot tutorial: a learner receives a concrete lab, attempts the work, submits evidence, gets reviewed against a rubric, and advances toward a capstone project.

## Product Shape

The first version has five parts:

1. Course Pack

   A course pack defines modules, labs, commands, evidence, rubrics, and a capstone. The MVP ships with one built-in course: `lh-agent-systems`.

2. Lab Runner

   A lab is a concrete task with expected commands and evidence. Example: inspect a Telegram formatting bug, run `go test ./internal/gateway/telegram`, and submit a short root-cause note.

3. TA Agent

   In the first CLI version, this is represented by prompts, rubrics, and evidence submission. In the later agent version, this becomes a real instructor/lab/critic/examiner multi-agent loop.

4. Evaluator

   The MVP accepts explicit evidence from the learner. The next version should run declared commands, capture outputs, and produce a review automatically.

5. Learning Memory

   Progress is persisted in the LuckyHarness home under:

   ```text
   ~/.luckyharness/learning/progress.json
   ```

   This makes CLI, Telegram, and GUI share the same state.

## CLI MVP

Commands:

```text
lh learn list
lh learn start <course>
lh learn current
lh learn lab
lh learn submit <evidence>
lh learn progress
```

Example:

```bash
lh learn start lh-agent-systems
lh learn lab
lh learn submit "go test ./internal/gateway/telegram passed; root cause was an unclosed code fence after truncation"
lh learn progress
```

## Built-in Course

The bundled `lh-agent-systems` course has four modules:

1. `m1-tool-trace`

   Teaches visible tool traces, Telegram delivery, HTML/code-block boundaries, and regression testing.

2. `m2-context-packer`

   Teaches context window budgeting, typed memory, history relevance, and benchmark gates.

3. `m3-multiagent-orchestration`

   Teaches multi-agent routing decisions using MDP, stochastic shortest path, Lyapunov guards, and verifier gates.

4. `m4-hermes-lite-capstone`

   Asks the learner to design and validate a compact Hermes-like workflow across CLI, Telegram, trace visibility, and acceptance review.

## Agent Roles

The long-term runtime should use four agent roles:

```text
Instructor Agent -> explains the concept and frames the module
Lab Agent        -> gives the exercise, scaffolds commands, checks evidence
Critic Agent     -> reviews design tradeoffs and failure modes
Examiner Agent   -> applies the rubric and decides whether to advance
```

This mirrors the existing LuckyHarness multi-agent direction:

```text
planner -> executor -> debugger -> acceptor
```

The learning version differs in intent. A normal agent tries to finish the task quickly. A learning agent tries to maximize learner mastery while keeping the work concrete.

## Mathematical Control Model

The learning planner can be modeled as a contextual MDP, where:

```text
State S_t =
  current_module,
  mastery_vector,
  recent_failures,
  hint_count,
  evidence_quality,
  benchmark_score,
  time_spent
```

Actions:

```text
teach_concept
assign_lab
give_hint
run_test
run_review
advance_module
insert_remedial_lab
show_solution
```

Reward:

```text
R =
  learning_gain
+ verified_completion
+ evidence_quality
- excessive_hints
- repeated_failure
- unnecessary_agent_overhead
```

A Lyapunov-style guard can prevent non-convergent teaching loops. Define a learning potential:

```text
V(S) = gap_to_mastery + unresolved_failures + hint_debt
```

The planner should prefer actions where:

```text
E[V(S_{t+1}) - V(S_t)] < 0
```

Example: if the learner fails the same lab twice, repeating the same assignment is rejected. The planner should insert a remedial lab or a smaller hint.

## Telegram Integration

Telegram can expose the same state with commands:

```text
/learn
/learn_start lh-agent-systems
/learn_lab
/learn_submit <evidence>
/learn_progress
```

When a lab uses tools or sub-agents, Telegram should display both:

```text
Tool Trace
Agent Trace
Learning Trace
```

`Learning Trace` should show course/module/lab advancement, not internal chain-of-thought.

## GUI Integration

The dashboard should add a compact learning panel:

```text
Course
Current module
Current lab
Evidence checklist
Recent submissions
Run commands
Progress bar
```

This should be a workbench surface, not a marketing landing page.

## Implementation Roadmap

1. CLI and store MVP

   Implement built-in course definitions, progress persistence, and `lh learn` commands.

2. Command runner

   Let labs declare commands and let LH run them, capture output, and attach evidence automatically.

3. Rubric evaluator

   Score lab submissions with a deterministic rubric first, then optionally call the agent for richer review.

4. Telegram commands

   Add `/learn_*` commands using the same progress store.

5. Multi-agent teaching loop

   Add Instructor/Lab/Critic/Examiner orchestration and emit `Learning Trace`.

6. Benchmark

   Add a small `lh-learning-bench` suite to measure whether the planner advances, remediates, or asks for evidence correctly.

## Current MVP Status

Implemented:

- `internal/learning` course and progress model.
- Built-in `lh-agent-systems` course.
- `lh learn list/start/current/lab/submit/progress`.
- Progress persisted at `~/.luckyharness/learning/progress.json`.
- Unit tests for course validation, progress advancement, and CLI flow.

Not implemented yet:

- Automatic command execution.
- Agent-generated rubric review.
- Telegram `/learn_*` commands.
- GUI learning panel.
- Learning-specific benchmark.
