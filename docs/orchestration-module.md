# Independent orchestration module

`github.com/yurika0211/luckyagent-orchestrator` owns ordinary delegation,
multi-agent collaboration, background autonomy and their task storage engines.
LuckyAgent embeds it as a Go dependency in the same process.

| Owner | Responsibilities |
| --- | --- |
| `luckyagent-orchestrator/delegate` | Child task state, waiting, cancellation and injected execution |
| `luckyagent-orchestrator/collab` | Team modes, Planner/MDP, registry, aggregation and coordination |
| `luckyagent-orchestrator/autonomy` | Persistent queue, worker pool, heartbeat and recovery |
| `luckyagent-orchestrator/task` | Records, costs, events, observations and task policy |
| `luckyagent-tools` | Model-visible tool definitions, permissions and context adapters |
| `luckyagent-api/resources` | HTTP resource adapters |
| Host `internal/agent` | Actual Agent Loop, context, tools, model selection, execution adapters and result notifications |

The host maps the existing `autonomy.*`/`delegate.*` configuration into engine
options, chooses runtime-home storage paths and owns lifecycle. The tool SDK
injects its existing path rules into the ordinary delegate engine. The new
module does not import the host, tool SDK or transport modules.

Background autonomy persists to `${runtimeHome}/runtime/autonomy_queue.json`.
Foreground durable execution also uses autonomy queue/execution primitives,
but retains separate foreground files which background dispatchers never poll.
The host's `internal/autonomy/turn_execution.go` remains an adapter to the
host-owned durable-turn contract and Agent Loop.

Task records and autonomy queue snapshots retain their existing fields and
formats. No new runtime configuration keys are needed, so `config.example.json`
values are unchanged. Old tools `task`, `collab` and `autonomy` package paths
remain deprecated forwarding aliases; the active host imports the new owner.

The split preserves existing planning behavior: ordinary `subagent` records
its selected mode but executes one child callback. `mode=autonomy_queue` does
not enqueue background work; use `autonomy action=add`. Full team-mode execution
continues through the HTTP collaboration manager's injected `TaskHandler`.

## Joint development

Add all matching checkouts to an ignored workspace:

```bash
go work init . /path/to/luckyagent-tools /path/to/luckyagent-api /path/to/luckyagent-gateway /path/to/luckyagent-orchestrator
go build ./...
```

The initial orchestrator dependency is `v0.1.0`. The host pins published tools
and API commits that use that engine version. Normal builds use `GOWORK=off`
and downloadable module versions; workspaces are optional for local changes.
For subsequent changes, publish orchestrator, then tools and API, then update
the host's pinned versions.
