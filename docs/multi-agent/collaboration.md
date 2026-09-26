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

## Operational Notes

- Register each referenced agent before delegating work.
- An invalid mode, including `mdp`, is rejected rather than silently falling
  back to a different execution strategy.
- `auto` does not invent sub-tasks; it chooses how the provided agent list is
  orchestrated.
- Use `GET /api/v1/agents/task?id=<task-id>` to inspect task state and planner
  metadata.
