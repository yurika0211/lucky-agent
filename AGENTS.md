# LuckyAgent Agent core and launcher

This repository primarily maintains the public Agent core plus a thin `lh`
launcher. Application commands and runtime backends live in independent modules.

## Modules and ownership

- `agent/` is module `github.com/yurika0211/lucky-agent/agent`: Agent loop,
  context planning/packing, tool and approval policy, memory-use rules,
  session-aware/durable execution, convergence and stream events.
- The root is module `github.com/yurika0211/lucky-agent`: `cmd/la/main.go`,
  build/release entrypoints and product documentation. It imports the external
  CLI and still builds the complete `lh` executable.
- `github.com/yurika0211/luckyagent-app`: CLI/REPL, API and channel product
  handlers, shared application adapters, full embedding SDK and benchmarks.
- `github.com/yurika0211/luckyagent-runtime`: provider/model protocols,
  configuration, sessions, memory backend, RAG/embedding, MCP and support
  components. It does not import the core or application.
- Existing tools/API/gateway modules retain generic execution and wire/platform
  implementation. The Agent leaf module must never import luckyagent-app.
- `luckyagent-orchestrator` owns delegate/collab/autonomy/task engines. Agent
  execution and session adapters remain in this repo; tool schemas and tool-side
  policy remain in tools. Inject execution rather than importing the Agent into
  an engine. Consumers may still select an older engine through their pins.
- `luckyagent-android` consumes HTTP/WS contracts; it does not import Go modules.
  Coordinate wire changes with API transport and app handlers.
- `github.com/yurika0211/luckyagent-plugins` owns declarative packages,
  marketplace, snapshots and trust state. `agent/plugin_runtime.go` binds a
  startup snapshot to execution policy; app owns `lh plugin` and HTTP/channel
  command routing. Management changes require restart. See `docs/plugin-module.md`.

The Agent leaf module and root launcher have separate manifests to avoid a
module dependency cycle: launcher -> app -> Agent -> runtime/tools. Use actual
published commit versions; never commit machine-specific replacements.

## Construction and lifecycle

`agent.NewWithDependencies(Options, *Dependencies)` binds prepared runtime
components without backend construction, network startup or file writes.
`Options.TakeOwnership` transfers cleanup only on successful construction.
`StartupHooks` is inert until an application activates it. Default applications
use `luckyagent-app/bootstrap.New`; legacy core New/NewWithMemory remain
compatibility constructors. Shared config.Manager and public runtime storage
contracts retain their existing schema and semantics.

Injected construction reads installed plugins without creating their directories
or connecting MCP. It configures the prepared MCP manager before explicit Start;
plugin tools and child sessions keep parent approval, scope and cancellation.

## Context rules

- Root AGENTS.md/agents.md is project context; runtime manuals also come from
  description/AGENTS.md, description/agents.md and legacy manual paths.
- Preserve the latest user turn and its following messages when fitting history.
- Retrieved memory/RAG are prior evidence, not the current task. Explicit current
  user intent takes precedence. Sessions, durable memory and RAG stay distinct.
- Inspect agent/context_planner.go, system_prompt.go, memory_gate.go and the
  external session/channel adapters when investigating contamination.

## Product compatibility

The runtime home remains ~/.luckyagent. CLI commands, /api/v1 HTTP routes,
SSE/WS/gRPC fields, gateway protocols and session JSON/blob/compact formats are
unchanged by moving source. Check route handlers in luckyagent-app/apiapp and
transport catalogs in luckyagent-api before documenting endpoints.

Configuration schema/defaults live in luckyagent-runtime/config. The example
and GitHub Pages source remain here. Whenever a field or feature changes,
compare config.example.json and update the corresponding product documentation.

## Workflow

1. Identify each affected repository by origin and module path. Read its
   AGENTS.md, inspect git status --short --branch, default-branch HEAD and
   git worktree list. Directory names and an old internal/* checkout are not
   evidence of the current product layout. Preserve unrelated files and stashes;
   use an isolated worktree when required. Repair moved worktree registrations
   before interpreting missing paths as abandoned work.
2. List the provider, direct consumers, launcher and affected clients. Update
   the owning module, its docs/ and actual consumer contracts. Follow
   docs/multi-repo-workflow.md for the delivery record and dependency order.
3. When publication is authorized, publish downloadable dependency commits,
   update consumers' go.mod/go.sum, and link their PRs. An upstream merge alone
   does not complete integration. Verify the version actually selected by the
   launcher with GOWORK=off go list -mod=readonly -m all; do not flag an older
   minimum requirement as stale without checking Go's selected graph.
4. Check both the root and agent/ manifests. Keep local joint-development
   workspaces separate from the published graph; never commit machine paths or
   temporary replacements. Run the checks requested for the task, recording
   directory, HEAD and result. Relevant commands include:

```bash
(cd agent && GOWORK=off go test -mod=readonly -race ./...)
GOWORK=off go test -mod=readonly ./...
GOWORK=off go build -mod=readonly -o lh ./cmd/la
```

Parent ./... skips the nested Agent module. `make test` and CI explicitly check
both. The committed portable go.work selects the root and ./agent, so normal
builds and Docker use the checked-out Agent source. Optional go.local.work files
may select external app/runtime checkouts with an explicit GOWORK path; keep
machine-specific paths out of go.work. Validate published pins with GOWORK=off.

5. For an authorized merge, inspect checks on the current PR head and preserve
   concurrent mainline changes. Report missing/skipped checks explicitly.
6. Synchronize the project progress overview: local implementation, pushed
   commit, PR, default-branch merge, selected consumer version, docs, validation
   and deployment are separate states. Record the audit time and evidence.
   Compare old branches, dirty copies and stashes with delivered code before
   calling them missing features. A retained stash is not itself an open task.

Report exact verified results and remaining limits. Do not claim tests,
publication, merge or deployment succeeded without direct evidence. Keep live
progress and machine-specific paths in the project notes, not this file. Update
the established Obsidian overview and point historical reports to it. This does
not authorize unrelated PR merges or deployment.
