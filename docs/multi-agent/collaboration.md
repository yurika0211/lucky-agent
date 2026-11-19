# Multi-Agent Collaboration Guide

LuckyAgent exposes collaboration tasks through `POST /api/v1/agents/delegate`.
The request `mode` accepts exactly four values: `auto`, `parallel`, `pipeline`,
and `debate`. `mdp` is not an execution mode and is rejected with a `400`
response.

## Choosing A Mode

| Mode | Execution | Best for |
| --- | --- | --- |
| `parallel` | Runs independent sub-tasks concurrently, then aggregates them. | Independent research, audits, or comparisons. |
| `pipeline` | Runs sub-tasks sequentially; each receives the preceding output. | Work with an explicit dependency chain, such as investigate → implement → test. |
| `debate` | Runs multiple positions and a voting/critic-style resolution. | Reviews, risky decisions, and competing proposals. |
| `auto` | Uses the planner to select one executable mode above. | Requests where the caller cannot reliably choose the orchestration. |

`auto` is the only mode that invokes the MDP planner. MDP (Markov Decision
Process) is an internal decision component, not a separate worker topology.
It chooses among `parallel`, `pipeline`, and `debate`; task metadata records
the selected mode, planner trace, MDP state, action, and current Q-values.

## Auto Planner Inputs

The planner evaluates task shape (independent, sequential, or review-like),
risk, ambiguity, agent count and capabilities. Its MDP state also records:

- input complexity from an explicit token estimate when available, otherwise a
  deterministic text-length and structural-depth estimate;
- current agent load from registered `online`, `busy`, and `offline` states;
- timeout pressure and an optional normalized cost budget.

`cost_budget` is an optional number from `0` to `1`. `0` means no cost
constraint; a positive value increases the preference for lower-cost modes.
It guides planning rather than imposing a billing limit.

Each completed auto-planned task feeds its outcome and duration back into the
Markov model and MDP Q-table. When an MDP snapshot path is configured, the
learned state is persisted and reused after restart. With no history, the
planner falls back to conservative task-shape heuristics.

## API Example

```json
{
  "mode": "auto",
  "description": "Inspect the API, gateway, and UI independently, then summarize risks.",
  "input": "Focus on regressions introduced this week.",
  "agent_ids": ["api", "gateway", "ui"],
  "timeout": 60000000000,
  "cost_budget": 0.45
}
```

The `timeout` field follows Go's JSON duration representation used by the
current HTTP handler (nanoseconds); `60000000000` is 60 seconds. API clients
should read the returned task's `metadata.planner_trace` for an auditable
selection record.

The response carries `execution_session_id`, `parent_session_id`, and `role`.
The execution session is stable for the task, so clients can open
`/api/v1/sessions/{execution_session_id}` during execution and send follow-up
messages to the same sub-agent session after the run completes. The session
stores the sub-agent role prompt and reuses it on later turns.

Collaborative sub-tasks publish `task_progress` and `task_result` messages to
the in-process A2A mailbox. Use `GET /api/v1/agents/messages?to=...` to read
messages and `GET /api/v1/agents/blackboard?task_id=...` for the latest shared
task notes.

## Operational Notes

- Register each referenced agent before delegating work.
- An invalid mode, including `mdp`, is rejected rather than silently falling
  back to a different execution strategy.
- Mode selection chooses the scheduling topology. The Agent then makes a
  separate tool-free planning call to assign distinct responsibilities and
  observable acceptance criteria, once per registered agent in the given order.
  Invalid or duplicated assignments are rejected. A pipeline preserves the
  original request along with the preceding output.
- Use `GET /api/v1/agents/task?id=<task-id>` to inspect task state and planner
  metadata.

## Role execution policy

Agent registrations accept an existing `runtime` profile. These fields now
configure execution rather than relying on prompt instructions:

| Field | Behavior |
| --- | --- |
| `provider`, `model` | Select an immutable provider snapshot for this run. A different provider needs a configured endpoint and an explicit model. Global defaults are unchanged. |
| `cwd` | Sets the child's actual working directory; it must exist and pass the configured sandbox policy. |
| `tool_allowlist` | Limits model schemas and actual calls, including generated tools. Omitted means inherit; an explicit empty array denies all tools. Global denials still apply. |
| `max_iterations`, `timeout` | May reduce the configured `delegate.child.*` limits. |
| `approval_policy` | `inherit`, `manual`, or `auto`. A role cannot enable auto approval when child auto approval is disabled. |
| `allow_delegate` | Requires the global recursive-delegation setting as well. |
| `memory_namespace` | Rejected by the current backend because namespace isolation is not implemented. |

HTTP collaboration and ordinary delegation share execution slots controlled by
`delegate.max_concurrent`. The API reuses the Agent's configured manager and
recovered history. The built-in `local-agent` inherits the current default
provider/model; an explicit registered role selection remains pinned to that role.

## Acceptance and recovery

When a scheduling policy requires verification, a separate tool-free model
review checks the assignment, criteria, result and recent execution transcript.
Missing/invalid reviews fail acceptance; retries receive the previous failure
and output. `outcome.verified` only records an actual successful review.
This is model-based review, not a guarantee of correctness or a replacement
for the requested tests and execution evidence.

Any failed required assignment makes the parent incomplete/failed. Successful
partial results remain available. Unexecuted pipeline stages are recorded as
failed rather than left pending. Debate ballots cannot overwrite positions;
invalid votes, failed positions, or no majority produce an incomplete result.

Aggregation does not equate length with quality. `best` uses an injected scorer
or preserves all unscored candidates; `vote` compares complete trimmed answers
and only reports consensus for a strict majority. `summary` remains a
Unicode-safe preview, not a semantic summary.

Startup restores parent/child history and session links. Interrupted direct
runs are marked failed with an interruption event and are not replayed.
Background autonomy retains its independent queue/checkpoint recovery; it does
not consume foreground task files. Planner feedback is persisted atomically;
ordinary delegation reuses its planner at
`runtime-home/planner/delegate_mdp.json`.

No new configuration keys are required; the existing `delegate.*` example
remains valid.
